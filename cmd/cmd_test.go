package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Emil-02/gh-spruce/internal/gh"
	"github.com/Emil-02/gh-spruce/internal/ghtest"
)

func fake(t *testing.T) *ghtest.Server {
	s := ghtest.New(t)
	s.JSON("GET /user", 200, map[string]string{"login": "octo"})
	s.JSON("GET /users/octo", 200, map[string]string{"login": "octo", "type": "User"})
	s.JSON("GET /user/repos", 200, []map[string]any{
		{"name": "app", "full_name": "octo/app", "default_branch": "main", "owner": map[string]string{"login": "octo"}, "permissions": map[string]bool{"push": true}},
	})
	s.JSON("GET /repos/octo/app/git/trees/main", 200, map[string]any{"tree": []map[string]any{
		{"path": "main.go", "type": "blob"}, {"path": "go.mod", "type": "blob"}}})
	s.JSON("GET /licenses/mit", 200, map[string]string{"body": "MIT [year] [fullname]"})
	s.JSON("GET /gitignore/templates/Go", 200, map[string]string{"source": "*.exe"})
	newClient = func() (*gh.Client, error) { return s.Client(), nil }
	now = func() time.Time { return time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) }
	t.Setenv("NO_COLOR", "1")
	return s
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append(args, "--config", "/dev/null"))
	err := root.Execute()
	return out.String(), err
}

func TestAuditJSONAndFailUnder(t *testing.T) {
	fake(t)
	out, err := run(t, "audit", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Score int `json:"score"`
		Repos []struct {
			Score int `json:"score"`
		} `json:"repos"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil || len(rep.Repos) != 1 {
		t.Fatalf("bad json %v: %s", err, out)
	}
	if _, err := run(t, "audit", "--fail-under", "100"); err == nil {
		t.Error("--fail-under should fail for a non-perfect profile")
	}
}

func TestFixDryRunAndConfirmation(t *testing.T) {
	s := fake(t)
	out, err := run(t, "fix", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"octo/app", "+ LICENSE", "+ .github/workflows/ci.yml", "+ .gitignore", "Dry run"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run missing %q:\n%s", want, out)
		}
	}
	if _, err := run(t, "fix"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("fix without a terminal must require --yes, got %v", err)
	}
	for _, r := range s.Requests {
		if r.Method != "GET" && r.Path != "/graphql" {
			t.Errorf("unexpected write %s %s", r.Method, r.Path)
		}
	}
	if _, err := run(t, "fix", "--only", "secrets"); err == nil {
		t.Error("non-fixable check must be rejected")
	}
}

func TestChecksCommand(t *testing.T) {
	out, err := run(t, "checks")
	if err != nil || !strings.Contains(out, "secrets") || !strings.Contains(out, "opt-in") {
		t.Errorf("checks output: %v\n%s", err, out)
	}
}
