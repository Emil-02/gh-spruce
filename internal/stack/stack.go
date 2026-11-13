// Package stack infers what a repository is built with from its file tree.
package stack

import (
	"path"
	"sort"
	"strings"

	"github.com/Emil-02/gh-spruce/internal/scan"
)

type Kind string

const (
	Next    Kind = "nextjs"
	Vite    Kind = "vite"
	Node    Kind = "node"
	Flutter Kind = "flutter"
	Dart    Kind = "dart"
	Python  Kind = "python"
	Go      Kind = "go"
	Maven   Kind = "maven"
	Gradle  Kind = "gradle"
	Java    Kind = "java"
	Rust    Kind = "rust"
	Static  Kind = "static"
)

type Info struct {
	Kinds []Kind `json:"kinds"`
	// Dir of the primary project manifest relative to repo root ("" = root).
	Dir            string   `json:"dir,omitempty"`
	PackageManager string   `json:"package_manager,omitempty"` // npm | pnpm | yarn | bun
	HasTests       bool     `json:"has_tests"`
	Extras         []string `json:"extras,omitempty"` // supabase, tailwindcss, docker, ...
}

func (i Info) Is(k Kind) bool {
	for _, x := range i.Kinds {
		if x == k {
			return true
		}
	}
	return false
}

func (i Info) Primary() Kind {
	if len(i.Kinds) == 0 {
		return ""
	}
	return i.Kinds[0]
}

// IsApp reports whether the project is user-facing (screenshots make sense).
func (i Info) IsApp() bool {
	return i.Is(Next) || i.Is(Vite) || i.Is(Flutter) || i.Is(Static)
}

var skipDirs = []string{"node_modules/", "vendor/", ".git/", "build/", "dist/", ".dart_tool/", "venv/", ".venv/", "example/", "examples/"}

func skipped(p string) bool {
	for _, d := range skipDirs {
		if strings.HasPrefix(p, d) || strings.Contains(p, "/"+d) {
			return true
		}
	}
	return false
}

// depth returns the number of directory levels of p.
func depth(p string) int { return strings.Count(p, "/") }

func Detect(r *scan.Repo) Info {
	var info Info
	seen := map[Kind]bool{}
	add := func(k Kind) {
		if !seen[k] {
			seen[k] = true
			info.Kinds = append(info.Kinds, k)
		}
	}
	files := map[string]bool{}
	var pyFiles, javaFiles, htmlFiles int
	manifestDir := map[string]string{}
	setDir := func(key, p string) {
		d := path.Dir(p)
		if d == "." {
			d = ""
		}
		if cur, ok := manifestDir[key]; !ok || depth(p) < depth(cur+"/x") {
			manifestDir[key] = d
		}
	}
	for _, e := range r.Tree {
		if e.Type != "blob" || skipped(e.Path) || depth(e.Path) > 2 && !strings.HasSuffix(e.Path, ".py") && !strings.HasSuffix(e.Path, ".java") {
			continue
		}
		base := path.Base(e.Path)
		files[e.Path] = true
		switch {
		case base == "package.json":
			setDir("node", e.Path)
		case base == "pubspec.yaml":
			setDir("dart", e.Path)
		case base == "go.mod":
			setDir("go", e.Path)
		case base == "pom.xml":
			setDir("maven", e.Path)
		case base == "build.gradle" || base == "build.gradle.kts":
			if !strings.Contains(e.Path, "android/") {
				setDir("gradle", e.Path)
			}
		case base == "Cargo.toml":
			setDir("rust", e.Path)
		case base == "pyproject.toml" || base == "requirements.txt" || base == "setup.py":
			setDir("python", e.Path)
		case strings.HasSuffix(base, ".py"):
			pyFiles++
		case strings.HasSuffix(base, ".java"):
			javaFiles++
		case strings.HasSuffix(base, ".html"):
			htmlFiles++
		}
	}
	has := func(dir string, names ...string) bool {
		for _, n := range names {
			if files[path.Join(dir, n)] {
				return true
			}
		}
		return false
	}

	order := []string{"dart", "node", "go", "rust", "maven", "gradle", "python"}
	// Prefer the manifest closest to the root as the primary stack.
	sort.SliceStable(order, func(i, j int) bool {
		di, iok := manifestDir[order[i]]
		dj, jok := manifestDir[order[j]]
		if iok != jok {
			return iok
		}
		return depth(di+"/x") < depth(dj+"/x")
	})
	for _, key := range order {
		dir, ok := manifestDir[key]
		if !ok {
			continue
		}
		if info.Kinds == nil {
			info.Dir = dir
		}
		switch key {
		case "dart":
			if has(dir, "lib/main.dart", "android/build.gradle", "android/app/build.gradle", "android/app/build.gradle.kts", "web/index.html") {
				add(Flutter)
			} else {
				add(Dart)
			}
		case "node":
			switch {
			case has(dir, "next.config.js", "next.config.mjs", "next.config.ts", "next.config.cjs"):
				add(Next)
			case has(dir, "vite.config.js", "vite.config.ts", "vite.config.mjs"):
				add(Vite)
			default:
				add(Node)
			}
			if info.PackageManager == "" {
				switch {
				case has(dir, "pnpm-lock.yaml"):
					info.PackageManager = "pnpm"
				case has(dir, "yarn.lock"):
					info.PackageManager = "yarn"
				case has(dir, "bun.lockb", "bun.lock"):
					info.PackageManager = "bun"
				default:
					info.PackageManager = "npm"
				}
			}
		case "go":
			add(Go)
		case "rust":
			add(Rust)
		case "maven":
			add(Maven)
		case "gradle":
			add(Gradle)
		case "python":
			add(Python)
		}
	}
	if !seen[Python] && pyFiles > 0 {
		add(Python)
	}
	if !seen[Maven] && !seen[Gradle] && javaFiles > 0 {
		add(Java)
	}
	if len(info.Kinds) == 0 && htmlFiles > 0 {
		add(Static)
	}

	for _, e := range r.Tree {
		p := strings.ToLower(e.Path)
		switch {
		case strings.HasPrefix(p, "test/") || strings.HasPrefix(p, "tests/") || strings.Contains(p, "/test/") || strings.Contains(p, "/tests/") ||
			strings.Contains(p, "__tests__/") || strings.Contains(p, ".test.") || strings.Contains(p, ".spec.") ||
			strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, "_test.dart") || strings.HasPrefix(path.Base(p), "test_"):
			if !skipped(p) && !strings.HasSuffix(p, "test/widget_test.dart") {
				info.HasTests = true
			}
		}
	}
	extra := func(name string, ok bool) {
		if ok {
			info.Extras = append(info.Extras, name)
		}
	}
	extra("supabase", r.Has("supabase/config.toml") || len(r.Find("supabase/migrations/*")) > 0)
	extra("tailwindcss", len(r.Find("tailwind.config.*")) > 0 || hasDep(r.Readme, "tailwind"))
	extra("docker", len(r.Find("Dockerfile")) > 0 || len(r.Find("docker-compose.y*ml")) > 0 || len(r.Find("compose.y*ml")) > 0)
	extra("firebase", len(r.Find("firebase.json")) > 0 || len(r.Find("google-services.json")) > 0)
	return info
}

func hasDep(readme, word string) bool { return strings.Contains(strings.ToLower(readme), word) }

// Topics suggests GitHub topics for the repository.
func Topics(i Info, language string) []string {
	var t []string
	for _, k := range i.Kinds {
		switch k {
		case Next:
			t = append(t, "nextjs", "react")
		case Vite:
			t = append(t, "vite")
		case Node:
			t = append(t, "nodejs")
		case Flutter:
			t = append(t, "flutter", "dart", "mobile-app")
		case Dart:
			t = append(t, "dart")
		case Python:
			t = append(t, "python")
		case Go:
			t = append(t, "golang")
		case Maven, Gradle, Java:
			t = append(t, "java")
		case Rust:
			t = append(t, "rust")
		case Static:
			t = append(t, "html", "css", "website")
		}
	}
	switch strings.ToLower(language) {
	case "typescript":
		t = append(t, "typescript")
	case "javascript":
		t = append(t, "javascript")
	case "c++":
		t = append(t, "cpp")
	case "c#":
		t = append(t, "csharp")
	case "":
	default:
		t = append(t, Slug(language))
	}
	t = append(t, i.Extras...)
	return Dedupe(t)
}

func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func Dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = Slug(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
