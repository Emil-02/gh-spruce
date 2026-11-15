package checks

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/Emil-02/gh-spruce/internal/scan"
	"github.com/Emil-02/gh-spruce/internal/stack"
)

// IsProfileRepo reports whether r is the special <user>/<user> profile README repository.
func IsProfileRepo(r *scan.Repo) bool { return strings.EqualFold(r.Name, r.Owner) }

// IsEmpty reports whether a repository has no meaningful content.
func IsEmpty(r *scan.Repo) bool {
	if r.Empty {
		return true
	}
	for _, e := range r.Tree {
		if e.Type == "tree" && e.Path != ".github" && !strings.HasPrefix(e.Path, ".github/") {
			return false
		}
		if e.Type != "blob" {
			continue
		}
		switch strings.ToLower(path.Base(e.Path)) {
		case "readme", "readme.md", "readme.txt", "readme.rst", "license", "license.md", "license.txt",
			".gitignore", ".gitattributes", ".editorconfig":
			continue
		}
		return false
	}
	return true
}

var emptyCheck = Check{
	ID: "empty", Title: "Repository has content", Weight: 0, Severity: High, Fixable: true, OptIn: true,
	Run: func(c *Context) (Status, string, string) {
		if IsEmpty(c.Repo) {
			return Fail, "repository is empty (only README/LICENSE/.gitignore)", "push the project, or archive/delete it so it does not dilute your profile"
		}
		return Pass, "", ""
	},
}

var boilerplate = []string{
	"this is a [next.js](https://nextjs.org) project bootstrapped with",
	"this is a [next.js](https://nextjs.org/) project bootstrapped with",
	"a new flutter project.",
	"getting started with create react app",
	"this template provides a minimal setup to get react working in vite",
	"this template should help get you started developing with vue 3 in vite",
	"## getting started\n\nthis project is a starting point for a flutter application",
	"this is an expo project created with create-expo-app",
	"this project was generated with [angular cli]",
	"## project setup\n\n```\nnpm install\n```",
	"laravel is a web application framework with expressive, elegant syntax",
	"vue create",
	"ng new",
}

func IsBoilerplateReadme(s string) bool {
	l := strings.ToLower(s)
	for _, b := range boilerplate {
		if strings.Contains(l, b) {
			return true
		}
	}
	return false
}

var readmeCheck = Check{
	ID: "readme", Title: "Meaningful README", Weight: 15, Severity: High, Fixable: true,
	Run: func(c *Context) (Status, string, string) {
		r := c.Repo
		switch {
		case r.ReadmePath == "":
			return Fail, "no README", "explain what it is, a screenshot, and how to run it"
		case IsBoilerplateReadme(r.Readme):
			return Fail, "README is still the framework boilerplate", "replace it with a project description"
		case len(strings.TrimSpace(r.Readme)) < c.Config.MinReadmeLen:
			return Warn, fmt.Sprintf("README is short (%d chars)", len(strings.TrimSpace(r.Readme))), "add features, tech stack and setup instructions"
		}
		return Pass, "", ""
	},
}

var licenseCheck = Check{
	ID: "license", Title: "Open-source license", Weight: 15, Severity: High, PublicOnly: true, Fixable: true,
	Run: func(c *Context) (Status, string, string) {
		switch c.Repo.License {
		case "":
			return Fail, "no LICENSE", "without a license nobody may legally reuse the code"
		case "Other":
			return Warn, "license not recognized by GitHub", "use a standard license text so GitHub can detect it"
		}
		return Pass, c.Repo.License, ""
	},
}

var descriptionCheck = Check{
	ID: "description", Title: "Repository description", Weight: 10, Severity: Medium, Fixable: true,
	Run: func(c *Context) (Status, string, string) {
		if c.Repo.Description == "" {
			return Fail, "no description", "one sentence shown in search results and on your profile"
		}
		return Pass, "", ""
	},
}

var topicsCheck = Check{
	ID: "topics", Title: "Topics", Weight: 10, Severity: Medium, PublicOnly: true, Fixable: true,
	Run: func(c *Context) (Status, string, string) {
		n := len(c.Repo.Topics)
		switch {
		case n == 0:
			return Fail, "no topics", "topics make the repo discoverable in GitHub search"
		case n < c.Config.MinTopics:
			return Warn, fmt.Sprintf("only %d topic(s)", n), fmt.Sprintf("add at least %d", c.Config.MinTopics)
		}
		return Pass, "", ""
	},
}

var deployLink = regexp.MustCompile(`https?://[a-zA-Z0-9.-]+\.(vercel\.app|netlify\.app|pages\.dev|github\.io|web\.app|firebaseapp\.com|onrender\.com|fly\.dev|up\.railway\.app|surge\.sh)(/[^\s)"'<>\]]*)?`)

var (
	fencedCode = regexp.MustCompile("(?s)```.*?```")
	inlineCode = regexp.MustCompile("`[^`\n]*`")
)

// DemoURL returns the most likely live demo URL of a repository. Links inside
// code are ignored because they are usually configuration examples.
func DemoURL(r *scan.Repo) string {
	prose := inlineCode.ReplaceAllString(fencedCode.ReplaceAllString(r.Readme, ""), "")
	if ms := deployLink.FindAllString(prose, -1); len(ms) > 0 {
		best := ms[0]
		slug := strings.ToLower(strings.NewReplacer("_", "-", ".", "-").Replace(r.Name))
		for _, m := range ms {
			if strings.Contains(strings.ToLower(m), slug) {
				best = m
				break
			}
		}
		return strings.TrimRight(best, ".,")
	}
	if r.HasPages {
		if strings.EqualFold(r.Name, r.Owner+".github.io") {
			return "https://" + strings.ToLower(r.Owner) + ".github.io/"
		}
		return fmt.Sprintf("https://%s.github.io/%s/", strings.ToLower(r.Owner), r.Name)
	}
	return ""
}

var homepageCheck = Check{
	ID: "homepage", Title: "Live demo link", Weight: 5, Severity: Low, PublicOnly: true, Fixable: true,
	Run: func(c *Context) (Status, string, string) {
		if c.Repo.Homepage != "" {
			return Pass, c.Repo.Homepage, ""
		}
		if u := DemoURL(c.Repo); u != "" {
			return Fail, "homepage not set (found " + u + ")", "set it so visitors can try the project in one click"
		}
		return Skip, "", ""
	},
}

func hasStack(c *Context) bool {
	return len(c.Stack.Kinds) > 1 || len(c.Stack.Kinds) == 1 && !c.Stack.Is(stack.Static)
}

var ciCheck = Check{
	ID: "ci", Title: "Continuous integration", Weight: 10, Severity: Medium, Fixable: true,
	Run: func(c *Context) (Status, string, string) {
		if !hasStack(c) {
			return Skip, "", ""
		}
		if len(c.Repo.Workflows()) == 0 {
			return Fail, "no GitHub Actions workflow", "a build/lint workflow proves the project actually works"
		}
		return Pass, "", ""
	},
}

var ciGreenCheck = Check{
	ID: "ci-green", Title: "CI passing", Weight: 5, Severity: Medium,
	Run: func(c *Context) (Status, string, string) {
		run := c.Repo.LastRun
		if run == nil || run.Status != "completed" {
			return Skip, "", ""
		}
		switch run.Conclusion {
		case "success", "skipped", "neutral":
			return Pass, "", ""
		case "failure", "timed_out", "startup_failure":
			return Fail, fmt.Sprintf("last run of %q failed", run.Name), run.URL
		}
		return Warn, fmt.Sprintf("last run of %q: %s", run.Name, run.Conclusion), run.URL
	},
}

var imageRef = regexp.MustCompile(`(?i)!\[[^\]]*\]\([^)]+\)|<img\s|<video\s|\.(png|jpe?g|gif|webp|svg)\)`)
var badgeOnly = regexp.MustCompile(`(?i)shields\.io|badge\.svg|badgen\.net|/badges?/`)

func HasScreenshot(readme string) bool {
	for _, m := range imageRef.FindAllStringIndex(readme, -1) {
		line := readme[m[0]:]
		if i := strings.IndexAny(line, ")>\n"); i > 0 {
			line = line[:i]
		}
		if !badgeOnly.MatchString(line) {
			return true
		}
	}
	return false
}

var screenshotCheck = Check{
	ID: "screenshot", Title: "Screenshot or demo GIF", Weight: 5, Severity: Low, PublicOnly: true,
	Run: func(c *Context) (Status, string, string) {
		if !c.Stack.IsApp() || c.Repo.ReadmePath == "" {
			return Skip, "", ""
		}
		if HasScreenshot(c.Repo.Readme) {
			return Pass, "", ""
		}
		return Fail, "README has no screenshot", "add docs/screenshot.png and reference it in the README"
	},
}

var gitignoreCheck = Check{
	ID: "gitignore", Title: ".gitignore", Weight: 5, Severity: Low, Fixable: true,
	Run: func(c *Context) (Status, string, string) {
		if !hasStack(c) {
			return Skip, "", ""
		}
		if c.Repo.Has(".gitignore") || (c.Stack.Dir != "" && c.Repo.Has(path.Join(c.Stack.Dir, ".gitignore"))) {
			return Pass, "", ""
		}
		return Fail, "no .gitignore", ""
	},
}

var junkDirs = []string{"node_modules", ".next", ".dart_tool", "__pycache__", ".venv", "venv", ".gradle", ".pytest_cache", ".mypy_cache"}

// JunkPaths returns committed dependency/build directories.
func JunkPaths(r *scan.Repo, st stack.Info) []string {
	dirs := append([]string{}, junkDirs...)
	if st.Is(stack.Flutter) || st.Is(stack.Dart) {
		dirs = append(dirs, "build")
	}
	if st.Is(stack.Rust) || st.Is(stack.Maven) {
		dirs = append(dirs, "target")
	}
	found := map[string]bool{}
	var out []string
	for _, e := range r.Tree {
		parts := strings.Split(e.Path, "/")
		for i, p := range parts[:len(parts)-1] {
			for _, d := range dirs {
				if p == d {
					dir := strings.Join(parts[:i+1], "/") + "/"
					if !found[dir] {
						found[dir] = true
						out = append(out, dir)
					}
				}
			}
		}
		if base := path.Base(e.Path); e.Type == "blob" && (base == ".DS_Store" || base == "Thumbs.db") && !found[base] {
			found[base] = true
			out = append(out, base)
		}
	}
	return out
}

var junkCheck = Check{
	ID: "junk", Title: "No committed build artifacts", Weight: 5, Severity: Medium,
	Run: func(c *Context) (Status, string, string) {
		j := JunkPaths(c.Repo, c.Stack)
		if len(j) == 0 {
			return Pass, "", ""
		}
		return Fail, "committed: " + short(j, 3), fmt.Sprintf("git rm -r --cached %s && add it to .gitignore", j[0])
	},
}

var largeFilesCheck = Check{
	ID: "large-files", Title: "No large files", Weight: 5, Severity: Medium,
	Run: func(c *Context) (Status, string, string) {
		limit := int64(c.Config.LargeFileMB) << 20
		var big []string
		for _, e := range c.Repo.Tree {
			if e.Type == "blob" && e.Size > limit {
				big = append(big, fmt.Sprintf("%s (%d MB)", e.Path, e.Size>>20))
			}
		}
		if len(big) > 0 {
			return Fail, short(big, 2), "use Git LFS or GitHub Releases for binaries"
		}
		if c.Repo.SizeKB > 100*1024 {
			return Warn, fmt.Sprintf("repository is %d MB", c.Repo.SizeKB>>10), "history probably contains build output; clean it with git filter-repo"
		}
		return Pass, "", ""
	},
}

var envExample = regexp.MustCompile(`(?i)\.(example|sample|template|dist|defaults?|test|ci)$`)

// SecretPaths returns committed files that usually hold credentials.
// Only file names are inspected; contents are never downloaded or printed.
func SecretPaths(r *scan.Repo) []string {
	var out []string
	for _, e := range r.Tree {
		if e.Type != "blob" {
			continue
		}
		base := strings.ToLower(path.Base(e.Path))
		bad := false
		switch {
		case base == ".env" || strings.HasPrefix(base, ".env.") && !envExample.MatchString(base):
			bad = true
		case base == "id_rsa" || base == "id_dsa" || base == "id_ecdsa" || base == "id_ed25519":
			bad = true
		case strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".pfx") || strings.HasSuffix(base, ".jks") ||
			strings.HasSuffix(base, ".keystore") || base == "key.properties":
			bad = true
		case strings.HasSuffix(base, ".pem") && strings.Contains(base, "key"), strings.HasSuffix(base, ".key"):
			bad = true
		case strings.Contains(base, "firebase-adminsdk") && strings.HasSuffix(base, ".json"),
			strings.HasPrefix(base, "service-account") && strings.HasSuffix(base, ".json"),
			base == "credentials.json" || base == "client_secret.json" || strings.HasPrefix(base, "client_secret_"):
			bad = true
		}
		if bad {
			out = append(out, e.Path)
		}
	}
	return out
}

var secretsCheck = Check{
	ID: "secrets", Title: "No committed secrets", Weight: 10, Severity: Critical,
	Run: func(c *Context) (Status, string, string) {
		s := SecretPaths(c.Repo)
		if len(s) == 0 {
			return Pass, "", ""
		}
		return Fail, "possible secret file(s): " + short(s, 3), "rotate the credentials, delete the file and purge it from history (git filter-repo); scan contents with gitleaks"
	},
}

var staleCheck = Check{
	ID: "stale", Title: "Recently maintained", Weight: 3, Severity: Low,
	Run: func(c *Context) (Status, string, string) {
		if c.Repo.PushedAt.IsZero() {
			return Skip, "", ""
		}
		days := int(c.Now.Sub(c.Repo.PushedAt).Hours() / 24)
		if days > c.Config.StaleDays {
			return Warn, fmt.Sprintf("no pushes for %d days", days), "archive it if you are done with it"
		}
		return Pass, "", ""
	},
}

var securityCheck = Check{
	ID: "security-md", Title: "Security policy", Weight: 2, Severity: Low, PublicOnly: true, Fixable: true,
	Run: func(c *Context) (Status, string, string) {
		if !hasStack(c) {
			return Skip, "", ""
		}
		for _, p := range []string{"SECURITY.md", ".github/SECURITY.md", "docs/SECURITY.md"} {
			if c.Repo.Has(p) {
				return Pass, "", ""
			}
		}
		return Fail, "no SECURITY.md", "tell people how to report vulnerabilities privately"
	},
}

func short(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(items[:n], ", "), len(items)-n)
}
