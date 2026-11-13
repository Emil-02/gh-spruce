package card

import (
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/Emil-02/gh-spruce/internal/scan"
)

var update = flag.Bool("update", false, "update golden files")

func report() *checks.Report {
	res := func(id string, s checks.Status) checks.Result { return checks.Result{ID: id, Status: s} }
	return &checks.Report{
		Profile: scan.Profile{Login: "o<c>to"}, Score: 77, Grade: "B",
		GeneratedAt: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC),
		Repos: []checks.RepoReport{
			{Repo: &scan.Repo{Name: "a"}, Results: []checks.Result{res("license", checks.Pass), res("ci", checks.Fail), res("topics", checks.Pass), res("readme", checks.Pass)}},
			{Repo: &scan.Repo{Name: "b"}, Results: []checks.Result{res("license", checks.Fail), res("ci", checks.Skip), res("topics", checks.Warn), res("readme", checks.Pass)}},
			{Repo: &scan.Repo{Name: "secret", Private: true}, Results: []checks.Result{res("ci", checks.Pass)}},
		},
	}
}

func TestRenderGolden(t *testing.T) {
	for _, theme := range []string{"dark", "light"} {
		got, err := Render(report(), theme)
		if err != nil {
			t.Fatal(err)
		}
		path := "testdata/" + theme + ".svg"
		if *update {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run go test ./internal/card -update)", err)
		}
		if got != string(want) {
			t.Errorf("%s card differs from %s; run go test ./internal/card -update and review the diff", theme, path)
		}
	}
}

func TestRenderContent(t *testing.T) {
	got, _ := Render(report(), "dark")
	for _, want := range []string{"o&lt;c&gt;to", "1 / 2", "0 / 1", "2 / 2", ">2<", "77/100", "2026-11-14"} {
		if !strings.Contains(got, want) {
			t.Errorf("card missing %q", want)
		}
	}
	if _, err := Render(report(), "neon"); err == nil {
		t.Error("expected unknown theme error")
	}
}
