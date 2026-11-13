package checks

import (
	"strings"
	"testing"
	"time"

	"github.com/Emil-02/gh-spruce/internal/config"
	"github.com/Emil-02/gh-spruce/internal/scan"
)

var now = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

const goodReadme = `# demo

A demo application that shows how to do things well, with a long enough description to pass.

![screenshot](docs/screenshot.png)

## Getting started

Install the dependencies, run the development server and open the browser. This paragraph
exists to make the README comfortably longer than the minimum length used by the readme check.
`

func healthy() *scan.Repo {
	return &scan.Repo{
		Owner: "octo", Name: "demo", FullName: "octo/demo", DefaultBranch: "main",
		Description: "A demo", Homepage: "https://demo.vercel.app", Topics: []string{"a", "b", "c"},
		License: "MIT", PushedAt: now.Add(-24 * time.Hour), ReadmePath: "README.md", Readme: goodReadme,
		LastRun: &scan.WorkflowRun{Name: "CI", Status: "completed", Conclusion: "success"},
		Tree: []scan.Entry{
			{Path: "README.md", Type: "blob"}, {Path: "LICENSE", Type: "blob"}, {Path: ".gitignore", Type: "blob"},
			{Path: "SECURITY.md", Type: "blob"}, {Path: "package.json", Type: "blob"}, {Path: "next.config.ts", Type: "blob"},
			{Path: ".github/workflows/ci.yml", Type: "blob"}, {Path: "src/app/page.tsx", Type: "blob"},
		},
	}
}

func status(rr RepoReport, id string) Status {
	r, _ := rr.Result(id)
	return r.Status
}

func TestHealthyRepoScores100(t *testing.T) {
	rr := Evaluate(healthy(), config.Default(), now)
	if rr.Score != 100 || rr.Grade != "A+" {
		for _, f := range rr.Failing() {
			t.Logf("%s: %s %s", f.ID, f.Status, f.Message)
		}
		t.Fatalf("score = %d (%s), want 100", rr.Score, rr.Grade)
	}
}

func TestEmptyRepoScoresZero(t *testing.T) {
	r := healthy()
	r.Tree = []scan.Entry{{Path: "README.md", Type: "blob"}}
	rr := Evaluate(r, config.Default(), now)
	if rr.Score != 0 || status(rr, "empty") != Fail || status(rr, "license") != Skip {
		t.Fatalf("got score %d, empty=%s license=%s", rr.Score, status(rr, "empty"), status(rr, "license"))
	}
}

func TestSecretsCapScore(t *testing.T) {
	r := healthy()
	r.Tree = append(r.Tree, scan.Entry{Path: "app/.env.local", Type: "blob"}, scan.Entry{Path: ".env.example", Type: "blob"})
	rr := Evaluate(r, config.Default(), now)
	res, _ := rr.Result("secrets")
	if res.Status != Fail || rr.Score > 40 {
		t.Fatalf("secrets=%s score=%d", res.Status, rr.Score)
	}
	if strings.Contains(res.Message, ".env.example") || !strings.Contains(res.Message, "app/.env.local") {
		t.Errorf("unexpected message %q", res.Message)
	}
}

func TestPrivateSkipsPublicOnlyChecks(t *testing.T) {
	r := healthy()
	r.Private, r.License, r.Topics = true, "", nil
	rr := Evaluate(r, config.Default(), now)
	for _, id := range []string{"license", "topics", "homepage", "security-md", "screenshot"} {
		if s := status(rr, id); s != Skip {
			t.Errorf("%s = %s, want skip", id, s)
		}
	}
	if rr.Score != 100 {
		t.Errorf("score = %d", rr.Score)
	}
}

func TestProfileRepoOnlyNeedsReadme(t *testing.T) {
	r := healthy()
	r.Name, r.License, r.Topics, r.Description = "octo", "", nil, ""
	rr := Evaluate(r, config.Default(), now)
	if rr.Score != 100 {
		t.Errorf("profile repo score = %d, failing %v", rr.Score, rr.Failing())
	}
}

func TestWarningsEarnHalf(t *testing.T) {
	r := healthy()
	r.Topics = []string{"one"}
	rr := Evaluate(r, config.Default(), now)
	if status(rr, "topics") != Warn || rr.Score != 95 {
		t.Errorf("topics=%s score=%d, want warn 95", status(rr, "topics"), rr.Score)
	}
}

func TestConfigDisableAndWeights(t *testing.T) {
	r := healthy()
	r.License = ""
	cfg := config.Default()
	cfg.Disable = []string{"license"}
	if rr := Evaluate(r, cfg, now); rr.Score != 100 {
		t.Errorf("disabled license still counted: %d", rr.Score)
	}
	cfg = config.Default()
	cfg.Weights = map[string]int{"license": 0}
	if rr := Evaluate(r, cfg, now); rr.Score != 100 {
		t.Errorf("zero weight still counted: %d", rr.Score)
	}
}

func TestBoilerplateReadme(t *testing.T) {
	r := healthy()
	r.Readme = "This is a [Next.js](https://nextjs.org) project bootstrapped with create-next-app."
	rr := Evaluate(r, config.Default(), now)
	if status(rr, "readme") != Fail {
		t.Errorf("readme = %s", status(rr, "readme"))
	}
}

func TestJunkAndLargeFiles(t *testing.T) {
	r := healthy()
	r.Tree = append(r.Tree,
		scan.Entry{Path: "node_modules/a/index.js", Type: "blob"},
		scan.Entry{Path: "web/.next/cache/x", Type: "blob"},
		scan.Entry{Path: "assets/video.mp4", Type: "blob", Size: 50 << 20})
	rr := Evaluate(r, config.Default(), now)
	j, _ := rr.Result("junk")
	if j.Status != Fail || !strings.Contains(j.Message, "node_modules/") || !strings.Contains(j.Message, "web/.next/") {
		t.Errorf("junk = %+v", j)
	}
	if status(rr, "large-files") != Fail {
		t.Errorf("large-files = %s", status(rr, "large-files"))
	}
}

func TestCIGreen(t *testing.T) {
	r := healthy()
	r.LastRun.Conclusion = "failure"
	if s := status(Evaluate(r, config.Default(), now), "ci-green"); s != Fail {
		t.Errorf("ci-green = %s", s)
	}
	r.LastRun.Status = "in_progress"
	if s := status(Evaluate(r, config.Default(), now), "ci-green"); s != Skip {
		t.Errorf("in-progress ci-green = %s", s)
	}
}

func TestDemoURL(t *testing.T) {
	r := &scan.Repo{Owner: "Octo", Name: "shop", Readme: "Set `NEXT_PUBLIC_SITE_URL` (e.g. `https://example.vercel.app`)\n\n```\nhttps://code.netlify.app\n```\nLive: https://other.vercel.app and https://my-shop.vercel.app."}
	if got := DemoURL(r); got != "https://my-shop.vercel.app" {
		t.Errorf("DemoURL = %q", got)
	}
	r = &scan.Repo{Owner: "Octo", Name: "site", HasPages: true}
	if got := DemoURL(r); got != "https://octo.github.io/site/" {
		t.Errorf("pages DemoURL = %q", got)
	}
	r = &scan.Repo{Owner: "Octo", Name: "Octo.github.io", HasPages: true}
	if got := DemoURL(r); got != "https://octo.github.io/" {
		t.Errorf("user site DemoURL = %q", got)
	}
}

func TestHasScreenshot(t *testing.T) {
	cases := map[string]bool{
		"![CI](https://github.com/a/b/actions/workflows/ci.yml/badge.svg)": false,
		"![License](https://img.shields.io/badge/license-MIT-green)":       false,
		"![Home](docs/home.png)":                                       true,
		`<img src="assets/screen.jpg" width="300">`:                    true,
		"[![CI](https://x/badge.svg)](https://x)\n\n![demo](demo.gif)": true,
	}
	for in, want := range cases {
		if got := HasScreenshot(in); got != want {
			t.Errorf("HasScreenshot(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildProfile(t *testing.T) {
	empty := &scan.Repo{Owner: "octo", Name: "empty", FullName: "octo/empty", Empty: true}
	priv := healthy()
	priv.Name, priv.Private = "secret", true
	res := &scan.Result{
		Profile: scan.Profile{Login: "octo", Type: "User", HasReadme: true, PinnedKnown: true, Pinned: []scan.Pinned{{Name: "secret", Private: true}}},
		Repos:   []*scan.Repo{healthy(), empty, priv},
	}
	rep := Build(res, config.Default(), now)
	// Public average (100 + 0) / 2 = 50 → 45 + profile 1/2 * 10 = 50.
	if rep.Score != 50 {
		t.Errorf("profile score = %d, want 50", rep.Score)
	}
	if rep.Repos[0].Repo.Name != "empty" {
		t.Errorf("repos not sorted by score: first is %s", rep.Repos[0].Repo.Name)
	}
	var pinned Result
	for _, r := range rep.ProfileResults {
		if r.ID == "pinned" {
			pinned = r
		}
	}
	if pinned.Status != Fail || !strings.Contains(pinned.Message, "secret") {
		t.Errorf("pinned = %+v", pinned)
	}
}
