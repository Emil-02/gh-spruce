package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/Emil-02/gh-spruce/internal/scan"
)

func sample() *checks.Report {
	return &checks.Report{
		Profile: scan.Profile{Login: "octo"}, Score: 60, Grade: "C",
		Repos: []checks.RepoReport{{
			Repo: &scan.Repo{Name: "a|b", URL: "https://github.com/octo/a"}, Score: 50, Grade: "D",
			Results: []checks.Result{{ID: "license", Title: "Open-source license", Status: checks.Fail, Message: "no LICENSE", Hint: "add one"}},
		}},
		Summary: []checks.Summary{{ID: "license", Title: "Open-source license", Failing: 1, Total: 1}},
	}
}

func TestMarkdown(t *testing.T) {
	var b bytes.Buffer
	Markdown(&b, sample())
	out := b.String()
	for _, want := range []string{"**Profile score: 60/100 (C)**", `[a\|b](https://github.com/octo/a)`, "❌ license", "_add one_"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q:\n%s", want, out)
		}
	}
}

func TestTableAndJSON(t *testing.T) {
	var b bytes.Buffer
	Table(&b, sample(), true)
	for _, want := range []string{"Profile score 60/100 (C)", "a|b", "no LICENSE", "gh spruce fix --dry-run --only license"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("table missing %q:\n%s", want, b.String())
		}
	}
	b.Reset()
	if err := JSON(&b, sample()); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b.Bytes(), &v); err != nil || v["score"].(float64) != 60 {
		t.Errorf("json = %s (%v)", b.String(), err)
	}
}
