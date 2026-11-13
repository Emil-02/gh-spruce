package stack

import (
	"reflect"
	"testing"

	"github.com/Emil-02/gh-spruce/internal/scan"
)

func repo(paths ...string) *scan.Repo {
	r := &scan.Repo{}
	for _, p := range paths {
		r.Tree = append(r.Tree, scan.Entry{Path: p, Type: "blob"})
	}
	return r
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name  string
		repo  *scan.Repo
		kinds []Kind
		dir   string
		pm    string
		tests bool
	}{
		{"next pnpm", repo("package.json", "next.config.ts", "pnpm-lock.yaml", "src/app/page.tsx"), []Kind{Next}, "", "pnpm", false},
		{"vite", repo("package.json", "vite.config.ts", "src/App.test.tsx"), []Kind{Vite}, "", "npm", true},
		{"flutter", repo("pubspec.yaml", "lib/main.dart", "test/widget_test.dart"), []Kind{Flutter}, "", "", false},
		{"flutter with tests", repo("pubspec.yaml", "lib/main.dart", "test/prayer_test.dart"), []Kind{Flutter}, "", "", true},
		{"dart package", repo("pubspec.yaml", "lib/src/x.dart"), []Kind{Dart}, "", "", false},
		{"nested node", repo("README.md", "web/package.json", "web/next.config.mjs", "web/yarn.lock"), []Kind{Next}, "web", "yarn", false},
		{"loose python", repo("kamus.py", "data/words.txt"), []Kind{Python}, "", "", false},
		{"python with tests", repo("requirements.txt", "app.py", "tests/test_app.py"), []Kind{Python}, "", "", true},
		{"go", repo("go.mod", "main.go", "x_test.go"), []Kind{Go}, "", "", true},
		{"maven", repo("pom.xml", "src/main/java/A.java"), []Kind{Maven}, "", "", false},
		{"plain java", repo("src/Main.java", "src/Util.java"), []Kind{Java}, "", "", false},
		{"static", repo("index.html", "style.css"), []Kind{Static}, "", "", false},
		{"ignores node_modules", repo("node_modules/x/package.json", "index.html"), []Kind{Static}, "", "", false},
		{"android gradle is flutter", repo("pubspec.yaml", "lib/main.dart", "android/app/build.gradle"), []Kind{Flutter}, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Detect(tt.repo)
			if !reflect.DeepEqual(got.Kinds, tt.kinds) || got.Dir != tt.dir || got.PackageManager != tt.pm || got.HasTests != tt.tests {
				t.Errorf("Detect() = %+v, want kinds=%v dir=%q pm=%q tests=%v", got, tt.kinds, tt.dir, tt.pm, tt.tests)
			}
		})
	}
}

func TestTopics(t *testing.T) {
	info := Info{Kinds: []Kind{Next}, Extras: []string{"supabase"}}
	got := Topics(info, "TypeScript")
	want := []string{"nextjs", "react", "typescript", "supabase"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Topics() = %v, want %v", got, want)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Jupyter Notebook": "jupyter-notebook", " C++ ": "c", "--a__b--": "a-b"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
