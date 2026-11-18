// Package fix turns failing checks into concrete repository changes and
// applies them through the GitHub API (no local clone needed).
package fix

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/Emil-02/gh-spruce/internal/config"
	"github.com/Emil-02/gh-spruce/internal/gh"
	"github.com/Emil-02/gh-spruce/internal/scan"
	"github.com/Emil-02/gh-spruce/internal/stack"
	"github.com/Emil-02/gh-spruce/internal/templates"
)

type File struct {
	Path    string `json:"path"`
	Content string `json:"-"`
	Check   string `json:"check"`
}

type Patch struct {
	Repo        *scan.Repo `json:"-"`
	FullName    string     `json:"repo"`
	Checks      []string   `json:"checks"`
	Files       []File     `json:"files,omitempty"`
	Description *string    `json:"description,omitempty"`
	Homepage    *string    `json:"homepage,omitempty"`
	Topics      []string   `json:"topics,omitempty"`
	Archive     bool       `json:"archive,omitempty"`
	Skipped     []string   `json:"skipped,omitempty"`
}

func (p *Patch) Empty() bool {
	return len(p.Files) == 0 && p.Description == nil && p.Homepage == nil && p.Topics == nil && !p.Archive
}

// Lines describes the patch for previews.
func (p *Patch) Lines() []string {
	var out []string
	for _, f := range p.Files {
		out = append(out, fmt.Sprintf("+ %s  (%s)", f.Path, f.Check))
	}
	if p.Description != nil {
		out = append(out, fmt.Sprintf("~ description: %q", *p.Description))
	}
	if p.Homepage != nil {
		out = append(out, "~ homepage: "+*p.Homepage)
	}
	if p.Topics != nil {
		out = append(out, "~ topics: "+strings.Join(p.Topics, ", "))
	}
	if p.Archive {
		out = append(out, "~ archive repository")
	}
	for _, s := range p.Skipped {
		out = append(out, "! "+s)
	}
	return out
}

type Planner struct {
	Client *gh.Client
	Config *config.Config
	Holder string
	Now    time.Time

	mu    sync.Mutex
	cache map[string]string
}

// Selected reports whether a check should be fixed given the --only set.
func Selected(c checks.Check, only map[string]bool) bool {
	if !c.Fixable {
		return false
	}
	if len(only) > 0 {
		return only[c.ID]
	}
	return !c.OptIn
}

func (p *Planner) Plan(ctx context.Context, rr checks.RepoReport, only map[string]bool) (*Patch, error) {
	r := rr.Repo
	patch := &Patch{Repo: r, FullName: r.FullName}
	if r.Archived {
		return patch, nil
	}
	if !r.CanPush {
		for _, x := range rr.Results {
			if c, _ := checks.ByID(x.ID); x.Fixable && Selected(c, only) {
				patch.Skipped = append(patch.Skipped, "no push access")
				break
			}
		}
		return patch, nil
	}
	licenseComing := false
	if x, ok := rr.Result("license"); ok && x.Fixable && x.Status == checks.Fail {
		lc, _ := checks.ByID("license")
		licenseComing = Selected(lc, only)
	}
	for _, x := range rr.Results {
		c, _ := checks.ByID(x.ID)
		if !x.Fixable || !Selected(c, only) {
			continue
		}
		var err error
		applied := true
		switch x.ID {
		case "empty":
			patch.Archive = true
		case "readme":
			applied, err = p.readme(rr, x, patch, licenseComing)
		case "license":
			applied, err = p.license(ctx, rr, x, patch)
		case "description":
			if d := DescriptionFromReadme(r.Readme); d != "" {
				patch.Description = &d
			} else {
				patch.Skipped = append(patch.Skipped, "description: README has no usable first paragraph")
				applied = false
			}
		case "topics":
			patch.Topics = mergeTopics(r.Topics, stack.Topics(rr.Stack, r.Language), r.Readme)
			if len(patch.Topics) == len(r.Topics) {
				patch.Topics = nil
				patch.Skipped = append(patch.Skipped, "topics: nothing to infer")
				applied = false
			}
		case "homepage":
			u := checks.DemoURL(r)
			patch.Homepage = &u
		case "ci":
			applied, err = p.ci(ctx, rr, patch)
		case "gitignore":
			applied, err = p.gitignore(ctx, rr, patch)
		case "security-md":
			s, e := templates.Render("SECURITY.md.tmpl", r)
			err = e
			patch.Files = append(patch.Files, File{Path: "SECURITY.md", Content: s, Check: x.ID})
		default:
			applied = false
		}
		if err != nil {
			return nil, fmt.Errorf("%s: planning %s: %w", r.FullName, x.ID, err)
		}
		if applied {
			patch.Checks = append(patch.Checks, x.ID)
		}
	}
	return patch, nil
}

func (p *Planner) cached(ctx context.Context, key string, fetch func() (string, error)) (string, error) {
	p.mu.Lock()
	if p.cache == nil {
		p.cache = map[string]string{}
	}
	v, ok := p.cache[key]
	p.mu.Unlock()
	if ok {
		return v, nil
	}
	v, err := fetch()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	p.cache[key] = v
	p.mu.Unlock()
	return v, nil
}

func (p *Planner) license(ctx context.Context, rr checks.RepoReport, x checks.Result, patch *Patch) (bool, error) {
	if x.Status != checks.Fail || rr.Repo.License != "" {
		return false, nil
	}
	key := strings.ToLower(p.Config.License)
	body, err := p.cached(ctx, "license:"+key, func() (string, error) {
		var l struct {
			Body string `json:"body"`
		}
		err := p.Client.Get(ctx, "licenses/"+url.PathEscape(key), &l)
		return l.Body, err
	})
	if err != nil {
		return false, fmt.Errorf("license %q: %w", key, err)
	}
	holder := p.Config.Holder
	if holder == "" {
		holder = p.Holder
	}
	year := fmt.Sprint(p.Now.Year())
	body = strings.NewReplacer("[year]", year, "[yyyy]", year, "[fullname]", holder, "[name of copyright owner]", holder).Replace(body)
	patch.Files = append(patch.Files, File{Path: "LICENSE", Content: body, Check: "license"})
	return true, nil
}

func (p *Planner) readme(rr checks.RepoReport, x checks.Result, patch *Patch, licenseComing bool) (bool, error) {
	r := rr.Repo
	if x.Status != checks.Fail {
		return false, nil
	}
	target := "README.md"
	if r.ReadmePath != "" {
		target = r.ReadmePath
	}
	data := map[string]any{
		"Name": r.Name, "FullName": r.FullName, "Description": r.Description, "Dir": rr.Stack.Dir,
		"Demo": checks.DemoURL(r), "Stack": stackNames(rr.Stack), "Setup": setupSteps(rr.Stack, r),
		"License": r.License,
	}
	if r.License == "" && licenseComing {
		data["License"] = strings.ToUpper(p.Config.License)
	}
	s, err := templates.Render("README.md.tmpl", data)
	if err != nil {
		return false, err
	}
	patch.Files = append(patch.Files, File{Path: target, Content: s, Check: "readme"})
	return true, nil
}

var ciTemplate = map[stack.Kind]string{
	stack.Next: "node", stack.Vite: "node", stack.Node: "node", stack.Flutter: "flutter", stack.Dart: "dart",
	stack.Python: "python", stack.Go: "go", stack.Maven: "maven", stack.Gradle: "gradle", stack.Java: "java",
	stack.Rust: "rust", stack.PHP: "php",
}

type envVar struct{ Key, Value string }

type composerFile struct {
	Scripts map[string]string `json:"scripts"`
	Require map[string]string `json:"require"`
}

var phpVersionRE = regexp.MustCompile(`\d+(?:\.\d+)*`)

// phpVersion reads the PHP version from composer.json's require constraint and
// falls back to 8.3 when the file says nothing useful.
func phpVersion(c composerFile) string {
	if m := phpVersionRE.FindString(c.Require["php"]); m != "" {
		return m
	}
	return "8.3"
}

func (p *Planner) ci(ctx context.Context, rr checks.RepoReport, patch *Patch) (bool, error) {
	r, st := rr.Repo, rr.Stack
	name, ok := ciTemplate[st.Primary()]
	if !ok || !templates.HasCI(name) {
		patch.Skipped = append(patch.Skipped, "ci: no template for this stack")
		return false, nil
	}
	data := map[string]any{"Branch": r.DefaultBranch, "Dir": st.Dir, "HasTests": st.HasTests}
	in := func(f string) string { return path.Join(st.Dir, f) }
	switch name {
	case "node":
		var pkg struct {
			Scripts        map[string]string `json:"scripts"`
			PackageManager string            `json:"packageManager"`
		}
		raw, err := p.fileContent(ctx, r, in("package.json"))
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal([]byte(raw), &pkg); err != nil {
			patch.Skipped = append(patch.Skipped, "ci: package.json is not valid JSON")
			return false, nil
		}
		pm := st.PackageManager
		lock := map[string]string{"npm": "package-lock.json", "pnpm": "pnpm-lock.yaml", "yarn": "yarn.lock"}[pm]
		if lock != "" && !r.Has(in(lock)) {
			lock = ""
		}
		install := map[string]string{"npm": "npm install", "pnpm": "pnpm install", "yarn": "yarn install", "bun": "bun install"}[pm]
		if pm == "npm" && lock != "" {
			install = "npm ci"
		}
		run := map[string]string{"npm": "npm run", "pnpm": "pnpm run", "yarn": "yarn", "bun": "bun run"}[pm]
		var scripts []string
		for _, s := range []string{"lint", "typecheck", "type-check", "build", "test"} {
			if v, ok := pkg.Scripts[s]; ok && !strings.Contains(v, "no test specified") && (s != "test" || !strings.Contains(v, "--watch")) {
				scripts = append(scripts, s)
			}
		}
		if len(scripts) == 0 {
			patch.Skipped = append(patch.Skipped, "ci: package.json has no lint/build/test scripts")
			return false, nil
		}
		data["PM"], data["Install"], data["Run"], data["Scripts"] = pm, install, run, scripts
		data["PMPinned"] = strings.HasPrefix(pkg.PackageManager, "pnpm@")
		if lock != "" {
			data["Lockfile"] = in(lock)
		}
		data["Env"] = p.exampleEnv(ctx, r, st)
	case "python":
		if r.Has(in("requirements.txt")) {
			data["Requirements"] = "requirements.txt"
		}
	case "gradle":
		data["Gradlew"] = r.Has(in("gradlew"))
	case "php":
		var composer composerFile
		raw, err := p.fileContent(ctx, r, in("composer.json"))
		if err != nil {
			return false, err
		}
		if err := json.Unmarshal([]byte(raw), &composer); err != nil {
			patch.Skipped = append(patch.Skipped, "ci: composer.json is not valid JSON")
			return false, nil
		}
		data["Test"] = composer.Scripts["test"] != ""
		data["PHPVersion"] = phpVersion(composer)
	}
	s, err := templates.Render(name+".yml", data)
	if err != nil {
		return false, err
	}
	patch.Files = append(patch.Files, File{Path: ".github/workflows/ci.yml", Content: s, Check: "ci"})
	return true, nil
}

var envKey = regexp.MustCompile(`(?m)^\s*(?:export\s+)?([A-Z][A-Z0-9_]*)\s*=`)

// exampleEnv reads variable names (never values) from .env.example so builds
// that validate their configuration get harmless placeholders.
func (p *Planner) exampleEnv(ctx context.Context, r *scan.Repo, st stack.Info) []envVar {
	for _, name := range []string{".env.example", ".env.sample", ".env.template"} {
		f := path.Join(st.Dir, name)
		if !r.Has(f) {
			continue
		}
		raw, err := p.fileContent(ctx, r, f)
		if err != nil {
			return nil
		}
		var out []envVar
		for _, m := range envKey.FindAllStringSubmatch(raw, -1) {
			k := m[1]
			if !strings.HasPrefix(k, "NEXT_PUBLIC_") && !strings.HasPrefix(k, "VITE_") && !strings.HasPrefix(k, "PUBLIC_") {
				continue
			}
			v := "ci-placeholder"
			if strings.Contains(k, "URL") {
				v = "https://example.com"
			}
			out = append(out, envVar{k, v})
		}
		return out
	}
	return nil
}

var gitignoreTemplates = map[stack.Kind][]string{
	stack.Next: {"Node"}, stack.Vite: {"Node"}, stack.Node: {"Node"}, stack.Flutter: {"Dart"}, stack.Dart: {"Dart"},
	stack.Python: {"Python"}, stack.Go: {"Go"}, stack.Maven: {"Maven", "Java"}, stack.Gradle: {"Gradle", "Java"},
	stack.Java: {"Java"}, stack.Rust: {"Rust"},
}

const flutterIgnore = `
# Flutter
.flutter-plugins
.flutter-plugins-dependencies
.pub-cache/
.pub/
/build/
**/android/.gradle/
**/android/local.properties
**/ios/Pods/
**/ios/Flutter/Generated.xcconfig
`

func (p *Planner) gitignore(ctx context.Context, rr checks.RepoReport, patch *Patch) (bool, error) {
	var parts []string
	seen := map[string]bool{}
	for _, k := range rr.Stack.Kinds {
		for _, t := range gitignoreTemplates[k] {
			if seen[t] {
				continue
			}
			seen[t] = true
			src, err := p.cached(ctx, "gitignore:"+t, func() (string, error) {
				var g struct {
					Source string `json:"source"`
				}
				err := p.Client.Get(ctx, "gitignore/templates/"+t, &g)
				return g.Source, err
			})
			if err != nil {
				return false, err
			}
			parts = append(parts, "# ---- "+t+" ----\n"+strings.TrimSpace(src)+"\n")
		}
	}
	if rr.Stack.Is(stack.Flutter) {
		parts = append(parts, flutterIgnore)
	}
	if len(parts) == 0 {
		return false, nil
	}
	parts = append(parts, "\n# Secrets\n.env\n.env.*\n!.env.example\n")
	patch.Files = append(patch.Files, File{Path: ".gitignore", Content: strings.Join(parts, "\n"), Check: "gitignore"})
	return true, nil
}

func (p *Planner) fileContent(ctx context.Context, r *scan.Repo, file string) (string, error) {
	var f struct {
		Content string `json:"content"`
	}
	u := fmt.Sprintf("repos/%s/contents/%s?ref=%s", r.FullName, (&url.URL{Path: file}).EscapedPath(), url.QueryEscape(r.DefaultBranch))
	if err := p.Client.Get(ctx, u, &f); err != nil {
		return "", err
	}
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	return string(b), err
}

var (
	mdLink    = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	htmlTag   = regexp.MustCompile(`<[^>]+>`)
	emphasis  = regexp.MustCompile("[*_`]+")
	spaces    = regexp.MustCompile(`\s+`)
	sentences = regexp.MustCompile(`^(.{20,}?[.!?])(\s|$)`)
)

// DescriptionFromReadme extracts the first prose paragraph of a README,
// cleaned of markdown and trimmed to GitHub's sensible length.
func DescriptionFromReadme(readme string) string {
	if checks.IsBoilerplateReadme(readme) {
		return ""
	}
	inCode := false
	for _, para := range strings.Split(strings.ReplaceAll(readme, "\r\n", "\n"), "\n\n") {
		t := strings.TrimSpace(para)
		if strings.Count(t, "```")%2 == 1 {
			inCode = !inCode
			continue
		}
		if inCode || t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "|") ||
			strings.HasPrefix(t, "-") || strings.HasPrefix(t, "*") && !strings.HasPrefix(t, "**") || strings.HasPrefix(t, "<!--") ||
			strings.HasPrefix(t, "---") || strings.HasPrefix(t, "[!") || strings.HasPrefix(t, "![") || strings.HasPrefix(t, "[![") {
			continue
		}
		t = strings.TrimPrefix(t, "> ")
		t = mdLink.ReplaceAllString(t, "$1")
		t = htmlTag.ReplaceAllString(t, "")
		t = emphasis.ReplaceAllString(t, "")
		t = strings.TrimSpace(spaces.ReplaceAllString(t, " "))
		if len([]rune(t)) < 20 {
			continue
		}
		if m := sentences.FindStringSubmatch(t); m != nil && len([]rune(m[1])) <= 160 {
			t = m[1]
		}
		if rs := []rune(t); len(rs) > 160 {
			t = strings.TrimSpace(string(rs[:157])) + "…"
		}
		return t
	}
	return ""
}

var knownTopics = map[string]string{
	"supabase": "supabase", "firebase": "firebase", "tailwind": "tailwindcss", "gemini": "gemini-api",
	"openai": "openai", "fastapi": "fastapi", "flask": "flask", "django": "django", "streamlit": "streamlit",
	"pandas": "pandas", "tensorflow": "tensorflow", "pytorch": "pytorch", "scikit-learn": "scikit-learn",
	"laravel": "laravel", "express": "express", "prisma": "prisma", "docker": "docker", "telegram": "telegram-bot",
}

func mergeTopics(existing, inferred []string, readme string) []string {
	all := append([]string{}, existing...)
	all = append(all, inferred...)
	low := strings.ToLower(readme)
	keys := make([]string, 0, len(knownTopics))
	for k := range knownTopics {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.Contains(low, k) {
			all = append(all, knownTopics[k])
		}
	}
	out := stack.Dedupe(all)
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

func stackNames(st stack.Info) []string {
	names := map[stack.Kind]string{
		stack.Next: "Next.js", stack.Vite: "Vite", stack.Node: "Node.js", stack.Flutter: "Flutter", stack.Dart: "Dart",
		stack.Python: "Python", stack.Go: "Go", stack.Maven: "Java (Maven)", stack.Gradle: "Java (Gradle)",
		stack.Java: "Java", stack.Rust: "Rust", stack.Static: "HTML / CSS / JavaScript",
	}
	var out []string
	for _, k := range st.Kinds {
		out = append(out, names[k])
	}
	extras := map[string]string{"supabase": "Supabase", "tailwindcss": "Tailwind CSS", "docker": "Docker", "firebase": "Firebase"}
	for _, e := range st.Extras {
		out = append(out, extras[e])
	}
	return out
}

func setupSteps(st stack.Info, r *scan.Repo) []string {
	run := map[string]string{"npm": "npm run dev", "pnpm": "pnpm dev", "yarn": "yarn dev", "bun": "bun dev"}
	switch st.Primary() {
	case stack.Next, stack.Vite, stack.Node:
		return []string{st.PackageManager + " install", run[st.PackageManager]}
	case stack.Flutter:
		return []string{"flutter pub get", "flutter run"}
	case stack.Dart:
		return []string{"dart pub get", "dart run"}
	case stack.Python:
		steps := []string{}
		if r.Has(path.Join(st.Dir, "requirements.txt")) {
			steps = append(steps, "pip install -r requirements.txt")
		}
		for _, m := range []string{"main.py", "app.py", "run.py", "manage.py"} {
			if r.Has(path.Join(st.Dir, m)) {
				return append(steps, "python "+m)
			}
		}
		var top []string
		for _, e := range r.Tree {
			if e.Type == "blob" && path.Dir(e.Path) == path.Clean("./"+st.Dir) && strings.HasSuffix(e.Path, ".py") {
				top = append(top, path.Base(e.Path))
			}
		}
		if len(top) == 1 {
			steps = append(steps, "python "+top[0])
		}
		return steps
	case stack.Go:
		return []string{"go run ."}
	case stack.Maven:
		return []string{"mvn package"}
	case stack.Gradle:
		return []string{"./gradlew build"}
	case stack.Rust:
		return []string{"cargo run"}
	case stack.Static:
		return []string{"# open index.html in your browser"}
	}
	return nil
}
