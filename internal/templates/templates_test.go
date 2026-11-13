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
	for _, name := range []string{"node", "flutter", "dart", "python", "go", "maven", "gradle", "java", "rust"} {
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
