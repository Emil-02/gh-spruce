package fix

import (
	"context"
	"encoding/base64"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/Emil-02/gh-spruce/internal/config"
	"github.com/Emil-02/gh-spruce/internal/ghtest"
	"github.com/Emil-02/gh-spruce/internal/scan"
)

var now = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

func TestDescriptionFromReadme(t *testing.T) {
	cases := map[string]string{
		"# Title\n\n[![CI](x)](y)\n\n**Temuin** is an AI-powered [lost & found](https://x) platform. It matches items.\n": "Temuin is an AI-powered lost & found platform.",
		"# T\n\n```bash\nnpm i something long enough here\n```\n\nShort.\n\nA calculator written in Python for exams.":    "A calculator written in Python for exams.",
		"# only heading\n\n- list item that is long enough to be a sentence":                                              "",
		"This is a [Next.js](https://nextjs.org) project bootstrapped with create-next-app.":                              "",
	}
	for in, want := range cases {
		if got := DescriptionFromReadme(in); got != want {
			t.Errorf("DescriptionFromReadme(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	long := strings.Repeat("word ", 60)
	if got := DescriptionFromReadme(long); len([]rune(got)) > 160 || !strings.HasSuffix(got, "…") {
		t.Errorf("long description not truncated: %d", len(got))
	}
}

func TestMergeTopics(t *testing.T) {
	got := mergeTopics([]string{"existing"}, []string{"nextjs", "Existing"}, "Built with Supabase and Tailwind.")
	want := []string{"existing", "nextjs", "supabase", "tailwindcss"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeTopics = %v, want %v", got, want)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func nodeRepo() *scan.Repo {
	return &scan.Repo{
		Owner: "octo", Name: "shop", FullName: "octo/shop", DefaultBranch: "main", CanPush: true,
		Language: "TypeScript", ReadmePath: "README.md",
		Readme: "# Shop\n\nAn online shop for handmade anime art prints and stickers.\n\nLive: https://shop.vercel.app",
		Tree: []scan.Entry{
			{Path: "README.md", Type: "blob"}, {Path: "package.json", Type: "blob"}, {Path: "package-lock.json", Type: "blob"},
			{Path: "next.config.ts", Type: "blob"}, {Path: ".env.example", Type: "blob"}, {Path: "src/app/page.tsx", Type: "blob"},
		},
	}
}

func phpRepo() *scan.Repo {
	return &scan.Repo{
		Owner: "octo", Name: "api", FullName: "octo/api", DefaultBranch: "main", CanPush: true,
		Language: "PHP",
		Tree: []scan.Entry{
			{Path: "composer.json", Type: "blob"}, {Path: "app/Http/Controllers/UserController.php", Type: "blob"},
		},
	}
}

func TestPlanPHPCi(t *testing.T) {
	s := ghtest.New(t)
	s.JSON("GET /repos/octo/api/contents/composer.json", 200, map[string]string{
		"content": b64(`{"require":{"php":"^8.1"},"scripts":{"test":"phpunit"}}`)})
	cfg := config.Default()
	rr := checks.Evaluate(phpRepo(), cfg, now)
	p := &Planner{Client: s.Client(), Config: cfg, Holder: "Octo Cat", Now: now}
	patch, err := p.Plan(context.Background(), rr, map[string]bool{"ci": true})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range patch.Files {
		files[f.Path] = f.Content
	}
	ci := files[".github/workflows/ci.yml"]
	for _, want := range []string{"composer install", "composer test", `php-version: "8.1"`} {
		if !strings.Contains(ci, want) {
			t.Errorf("ci.yml missing %q:\n%s", want, ci)
		}
	}
}

func TestPlanPHPCiWithoutTestScript(t *testing.T) {
	s := ghtest.New(t)
	s.JSON("GET /repos/octo/api/contents/composer.json", 200, map[string]string{
		"content": b64(`{"require":{"php":"^8.2"}}`)})
	cfg := config.Default()
	rr := checks.Evaluate(phpRepo(), cfg, now)
	p := &Planner{Client: s.Client(), Config: cfg, Holder: "Octo Cat", Now: now}
	patch, err := p.Plan(context.Background(), rr, map[string]bool{"ci": true})
	if err != nil {
		t.Fatal(err)
	}
	ci := ""
	for _, f := range patch.Files {
		if f.Path == ".github/workflows/ci.yml" {
			ci = f.Content
		}
	}
	if !strings.Contains(ci, "composer install") {
		t.Errorf("ci.yml missing composer install:\n%s", ci)
	}
	if strings.Contains(ci, "composer test") {
		t.Errorf("ci.yml runs composer test without a test script:\n%s", ci)
	}
}

func fakeAPI(t *testing.T) *ghtest.Server {
	s := ghtest.New(t)
	s.JSON("GET /licenses/mit", 200, map[string]string{"body": "MIT License\n\nCopyright (c) [year] [fullname]\n"})
	s.JSON("GET /gitignore/templates/Node", 200, map[string]string{"source": "node_modules/\n"})
	s.JSON("GET /repos/octo/shop/contents/package.json", 200, map[string]string{
		"content": b64(`{"scripts":{"dev":"next dev","lint":"eslint","build":"next build","test":"echo \"Error: no test specified\" && exit 1"}}`)})
	s.JSON("GET /repos/octo/shop/contents/.env.example", 200, map[string]string{
		"content": b64("NEXT_PUBLIC_SUPABASE_URL=\nNEXT_PUBLIC_SUPABASE_ANON_KEY=\nSUPABASE_SECRET=\n")})
	return s
}

func TestPlan(t *testing.T) {
	s := fakeAPI(t)
	cfg := config.Default()
	rr := checks.Evaluate(nodeRepo(), cfg, now)
	p := &Planner{Client: s.Client(), Config: cfg, Holder: "Octo Cat", Now: now}
	patch, err := p.Plan(context.Background(), rr, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range patch.Files {
		files[f.Path] = f.Content
	}
	if !strings.Contains(files["LICENSE"], "Copyright (c) 2026 Octo Cat") {
		t.Errorf("LICENSE = %q", files["LICENSE"])
	}
	ci := files[".github/workflows/ci.yml"]
	for _, want := range []string{"npm ci", "npm run lint", "npm run build", "cache-dependency-path: package-lock.json",
		"NEXT_PUBLIC_SUPABASE_URL: https://example.com", "NEXT_PUBLIC_SUPABASE_ANON_KEY: ci-placeholder"} {
		if !strings.Contains(ci, want) {
			t.Errorf("ci.yml missing %q:\n%s", want, ci)
		}
	}
	if strings.Contains(ci, "npm run test") || strings.Contains(ci, "SUPABASE_SECRET") {
		t.Errorf("ci.yml has placeholder test script or server secret:\n%s", ci)
	}
	if _, ok := files[".gitignore"]; !ok {
		t.Error("missing .gitignore")
	}
	if patch.Description == nil || *patch.Description != "An online shop for handmade anime art prints and stickers." {
		t.Errorf("description = %v", patch.Description)
	}
	if patch.Homepage == nil || *patch.Homepage != "https://shop.vercel.app" {
		t.Errorf("homepage = %v", patch.Homepage)
	}
	if !reflect.DeepEqual(patch.Topics, []string{"nextjs", "react", "typescript"}) {
		t.Errorf("topics = %v", patch.Topics)
	}
	if patch.Archive {
		t.Error("archive must be opt-in")
	}
}

func TestPlanOnlyAndPermissions(t *testing.T) {
	s := fakeAPI(t)
	cfg := config.Default()
	rr := checks.Evaluate(nodeRepo(), cfg, now)
	p := &Planner{Client: s.Client(), Config: cfg, Holder: "x", Now: now}
	patch, err := p.Plan(context.Background(), rr, map[string]bool{"topics": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(patch.Files) != 0 || patch.Description != nil || patch.Topics == nil {
		t.Errorf("--only topics produced %+v", patch)
	}
	r := nodeRepo()
	r.CanPush = false
	patch, _ = p.Plan(context.Background(), checks.Evaluate(r, cfg, now), nil)
	if !patch.Empty() || len(patch.Skipped) == 0 {
		t.Errorf("no-push repo should be skipped: %+v", patch)
	}
}

func applyServer(t *testing.T) *ghtest.Server {
	s := ghtest.New(t)
	s.JSON("GET /repos/octo/shop/git/ref/heads/main", 200, map[string]any{"object": map[string]string{"sha": "head1"}})
	s.JSON("GET /repos/octo/shop/git/commits/head1", 200, map[string]any{"tree": map[string]string{"sha": "tree1"}})
	s.JSON("POST /repos/octo/shop/git/trees", 201, map[string]string{"sha": "tree2"})
	s.JSON("POST /repos/octo/shop/git/commits", 201, map[string]string{"sha": "commit2"})
	s.JSON("PATCH /repos/octo/shop", 200, map[string]any{})
	s.JSON("PUT /repos/octo/shop/topics", 200, map[string]any{})
	return s
}

func patch() *Patch {
	d := "desc"
	return &Patch{Repo: nodeRepo(), FullName: "octo/shop", Description: &d, Topics: []string{"a"},
		Files: []File{{Path: "LICENSE", Content: "MIT", Check: "license"}}}
}

func TestApplyPullRequest(t *testing.T) {
	s := applyServer(t)
	s.JSON("POST /repos/octo/shop/git/refs", 422, map[string]string{"message": "Reference already exists"})
	s.JSON("PATCH /repos/octo/shop/git/refs/heads/spruce/fixes", 200, map[string]any{})
	s.JSON("GET /repos/octo/shop/pulls", 200, []any{})
	s.JSON("POST /repos/octo/shop/pulls", 201, map[string]string{"html_url": "https://github.com/octo/shop/pull/1"})

	out := (&Applier{Client: s.Client()}).Apply(context.Background(), patch())
	if out.Error != "" || out.URL != "https://github.com/octo/shop/pull/1" || out.Mode != "pr" || !out.Metadata {
		t.Fatalf("outcome = %+v", out)
	}
	tree := s.Find(http.MethodPost, "/repos/octo/shop/git/trees")[0].Body
	if tree["base_tree"] != "tree1" {
		t.Errorf("tree body = %v", tree)
	}
	upd := s.Find(http.MethodPatch, "/repos/octo/shop/git/refs/heads/spruce/fixes")
	if len(upd) != 1 || upd[0].Body["force"] != true || upd[0].Body["sha"] != "commit2" {
		t.Errorf("branch update = %+v", upd)
	}
	pr := s.Find(http.MethodPost, "/repos/octo/shop/pulls")[0].Body
	if pr["head"] != Branch || pr["base"] != "main" || !strings.Contains(pr["body"].(string), "LICENSE") {
		t.Errorf("pr body = %v", pr)
	}
	if len(s.Find(http.MethodPatch, "/repos/octo/shop/git/refs/heads/main")) != 0 {
		t.Error("default branch must not be touched in PR mode")
	}
}

func TestApplyDirect(t *testing.T) {
	s := applyServer(t)
	s.JSON("PATCH /repos/octo/shop/git/refs/heads/main", 200, map[string]any{})
	out := (&Applier{Client: s.Client(), Direct: true}).Apply(context.Background(), patch())
	if out.Error != "" || out.Mode != "direct" || !strings.HasSuffix(out.URL, "/commit/commit2") {
		t.Fatalf("outcome = %+v", out)
	}
	if len(s.Find(http.MethodPost, "/repos/octo/shop/pulls")) != 0 {
		t.Error("direct mode must not open a pull request")
	}
}

func TestApplyReportsWorkflowScope(t *testing.T) {
	s := applyServer(t)
	s.JSON("PATCH /repos/octo/shop/git/refs/heads/main", 422, map[string]string{"message": "refusing to allow an OAuth App to create or update workflow `.github/workflows/ci.yml` without `workflow` scope"})
	out := (&Applier{Client: s.Client(), Direct: true}).Apply(context.Background(), patch())
	if !strings.Contains(out.Error, "gh auth refresh -s workflow") {
		t.Errorf("error = %q", out.Error)
	}
}
