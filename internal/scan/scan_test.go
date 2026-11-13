package scan_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/Emil-02/gh-spruce/internal/ghtest"
	"github.com/Emil-02/gh-spruce/internal/scan"
)

func TestRunOwnAccount(t *testing.T) {
	s := ghtest.New(t)
	s.JSON("GET /user", 200, map[string]string{"login": "octo"})
	s.JSON("GET /users/octo", 200, map[string]string{"login": "octo", "name": "Octo Cat", "type": "User"})
	s.Handle("GET /user/repos", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`[{"name":"empty","full_name":"octo/empty","default_branch":"main","owner":{"login":"octo"},"private":true}]`))
			return
		}
		w.Header().Set("Link", `<https://api.github.com/user/repos?page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[
		 {"name":"app","full_name":"octo/app","default_branch":"main","owner":{"login":"octo"},"topics":["x"],"license":{"spdx_id":"MIT"},"permissions":{"push":true}},
		 {"name":"octo","full_name":"octo/octo","default_branch":"main","owner":{"login":"octo"}},
		 {"name":"forked","full_name":"octo/forked","fork":true,"default_branch":"main","owner":{"login":"octo"}},
		 {"name":"ignored","full_name":"octo/ignored","default_branch":"main","owner":{"login":"octo"}}
		]`))
	})
	s.JSON("GET /repos/octo/app/git/trees/main", 200, map[string]any{"sha": "abc", "tree": []map[string]any{
		{"path": "README.md", "type": "blob", "size": 10}, {"path": ".github/workflows/ci.yml", "type": "blob"}}})
	s.JSON("GET /repos/octo/app/readme", 200, map[string]string{"path": "README.md", "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte("# app"))})
	s.JSON("GET /repos/octo/app/actions/runs", 200, map[string]any{"workflow_runs": []map[string]string{{"name": "CI", "status": "completed", "conclusion": "success"}}})
	s.JSON("GET /repos/octo/octo/git/trees/main", 200, map[string]any{"tree": []map[string]any{{"path": "README.md", "type": "blob"}}})
	s.JSON("GET /repos/octo/empty/git/trees/main", 409, map[string]string{"message": "Git Repository is empty."})
	s.JSON("POST /graphql", 200, map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{"pinnedItems": map[string]any{
		"nodes": []map[string]any{{"name": "app", "isPrivate": false, "owner": map[string]string{"login": "octo"}}}}}}})

	res, err := scan.Run(context.Background(), s.Client(), scan.Options{Ignore: func(n string) bool { return n == "ignored" }})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Profile.Authenticated || !res.Profile.HasReadme || len(res.Profile.Pinned) != 1 || !res.Profile.PinnedKnown {
		t.Errorf("profile = %+v", res.Profile)
	}
	names := map[string]*scan.Repo{}
	for _, r := range res.Repos {
		names[r.Name] = r
	}
	if len(res.Repos) != 3 || names["forked"] != nil || names["ignored"] != nil {
		t.Fatalf("repos = %v", names)
	}
	app := names["app"]
	if app.License != "MIT" || !app.CanPush || app.Readme != "# app" || app.LastRun == nil || app.LastRun.Conclusion != "success" || len(app.Tree) != 2 {
		t.Errorf("app = %+v", app)
	}
	if !names["empty"].Empty || !names["empty"].Private {
		t.Errorf("empty = %+v", names["empty"])
	}
}

func TestRunOtherUserUsesPublicEndpoint(t *testing.T) {
	s := ghtest.New(t)
	s.JSON("GET /user", 403, map[string]string{"message": "Resource not accessible by integration"})
	s.JSON("GET /users/someone", 200, map[string]string{"login": "someone", "type": "User"})
	s.JSON("GET /users/someone/repos", 200, []any{})
	res, err := scan.Run(context.Background(), s.Client(), scan.Options{Owner: "someone"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile.Authenticated || len(s.Find("GET", "/user/repos")) != 0 {
		t.Errorf("should not list private endpoint: %+v", res.Profile)
	}
}

func TestRunWithoutOwnerNeedsUser(t *testing.T) {
	s := ghtest.New(t)
	if _, err := scan.Run(context.Background(), s.Client(), scan.Options{}); err == nil {
		t.Fatal("expected error when the viewer is unknown")
	}
}
