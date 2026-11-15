package templates

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCITemplatesAreValidYAML(t *testing.T) {
	variants := []map[string]any{
		{"Branch": "main", "HasTests": true},
		{"Branch": "master", "Dir": "app", "HasTests": false},
	}
	node := map[string]any{"PM": "pnpm", "Install": "pnpm install", "Run": "pnpm run", "Scripts": []string{"lint", "build"},
		"Lockfile": "pnpm-lock.yaml", "Env": []struct{ Key, Value string }{{"NEXT_PUBLIC_URL", "https://example.com"}}}
	php := map[string]any{"Test": true, "PHPVersion": "8.3"}
	for _, name := range []string{"node", "flutter", "dart", "python", "go", "maven", "gradle", "java", "rust", "php"} {
		for _, v := range variants {
			data := map[string]any{}
			for k, x := range v {
				data[k] = x
			}
			if name == "node" {
				for k, x := range node {
					data[k] = x
				}
			}
			if name == "php" {
				for k, x := range php {
					data[k] = x
				}
			}
			out, err := Render(name+".yml", data)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			var doc struct {
				On   map[string]any `yaml:"on"`
				Jobs map[string]struct {
					Steps []map[string]any `yaml:"steps"`
				} `yaml:"jobs"`
			}
			if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("%s: invalid YAML: %v\n%s", name, err, out)
			}
			if len(doc.Jobs["build"].Steps) < 2 || doc.On["push"] == nil {
				t.Errorf("%s: unexpected workflow:\n%s", name, out)
			}
			if d, _ := v["Dir"].(string); d != "" && !strings.Contains(out, "working-directory: app") {
				t.Errorf("%s: missing working-directory", name)
			}
		}
	}
	if HasCI("static") || !HasCI("go") {
		t.Error("HasCI mismatch")
	}
}

func TestPHPTemplate(t *testing.T) {
	out, err := Render("php.yml", map[string]any{"Branch": "main", "Test": true, "PHPVersion": "8.3"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "composer install") {
		t.Errorf("php template without composer install:\n%s", out)
	}
	if !strings.Contains(out, "composer test") {
		t.Errorf("php template without the test step:\n%s", out)
	}
	out, err = Render("php.yml", map[string]any{"Branch": "main", "Test": false, "PHPVersion": "8.1"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "composer test") {
		t.Errorf("php template with a test step it should not have:\n%s", out)
	}
	if !strings.Contains(out, `php-version: "8.1"`) {
		t.Errorf("php template without the php version:\n%s", out)
	}
}
