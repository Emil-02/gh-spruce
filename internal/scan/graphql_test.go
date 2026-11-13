package scan_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Emil-02/gh-spruce/internal/ghtest"
	"github.com/Emil-02/gh-spruce/internal/scan"
)

func graphqlServer(t *testing.T, handle func(query string, vars map[string]any) any) *ghtest.Server {
	s := ghtest.New(t)
	s.Handle("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(handle(req.Query, req.Variables))
	})
	return s
}

func light(name string, extra map[string]any) map[string]any {
	m := map[string]any{"name": name, "nameWithOwner": "octo/" + name, "url": "https://github.com/octo/" + name,
		"owner": map[string]string{"login": "octo"}, "pushedAt": "2026-11-01T00:00:00Z", "viewerPermission": "READ",
		"repositoryTopics": map[string]any{"nodes": []any{map[string]any{"topic": map[string]string{"name": "go"}}}}}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func detail(readme string) map[string]any {
	return map[string]any{
		"defaultBranchRef": map[string]any{"name": "main", "target": map[string]any{"oid": "abc", "statusCheckRollup": map[string]string{"state": "FAILURE"}}},
		"root": map[string]any{"entries": []any{
			map[string]any{"name": "README.md", "type": "blob", "size": 10},
			map[string]any{"name": "go.mod", "type": "blob", "size": 10},
			map[string]any{"name": ".github", "type": "tree", "object": map[string]any{"entries": []any{
				map[string]any{"name": "workflows", "type": "tree"}, map[string]any{"name": "SECURITY.md", "type": "blob"}}}},
			map[string]any{"name": "cmd", "type": "tree", "object": map[string]any{"entries": []any{map[string]any{"name": "main.go", "type": "blob", "size": 5}}}},
		}},
		"workflows": map[string]any{"entries": []any{map[string]any{"name": "ci.yml", "type": "blob"}}},
		"r1":        map[string]any{"text": readme},
	}
}

func TestRunGraphQL(t *testing.T) {
	var detailCalls atomic.Int32
	s := graphqlServer(t, func(q string, v map[string]any) any {
		switch {
		case strings.Contains(q, "repositories(first: 100"):
			if v["privacy"] != "PUBLIC" || v["isFork"] != false {
				t.Errorf("unexpected vars %v", v)
			}
			return map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{
				"__typename": "User", "login": "octo", "name": "Octo", "profile": map[string]string{"name": "octo"},
				"pinnedItems": map[string]any{"nodes": []any{map[string]any{"name": "app", "isPrivate": false, "owner": map[string]string{"login": "octo"}}}},
				"repositories": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": []any{
					light("app", map[string]any{"licenseInfo": map[string]string{"spdxId": "MIT"}, "viewerPermission": "ADMIN"}),
					light("old", map[string]any{"isArchived": true}),
					light("lib", nil), light("x1", nil), light("x2", nil), light("x3", nil),
				}},
			}}}
		case strings.Contains(q, "...D }"):
			detailCalls.Add(1)
			out := map[string]any{}
			for k := range v {
				if strings.HasPrefix(k, "n") {
					out["r"+k[1:]] = detail("# " + v[k].(string))
				}
			}
			return map[string]any{"data": out}
		}
		t.Fatalf("unexpected query %s", q)
		return nil
	})
	res, err := scan.RunGraphQL(context.Background(), s.Client(), scan.Options{Owner: "octo", Visibility: "public", Viewer: "octo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repos) != 5 || detailCalls.Load() != 1 {
		t.Fatalf("repos=%d detailCalls=%d", len(res.Repos), detailCalls.Load())
	}
	if !res.Profile.HasReadme || !res.Profile.PinnedKnown || !res.Profile.Authenticated || res.Profile.Type != "User" {
		t.Errorf("profile = %+v", res.Profile)
	}
	app := res.Repos[0]
	if app.Name != "app" || app.License != "MIT" || !app.CanPush || !app.Partial || app.Readme != "# app" || app.DefaultBranch != "main" {
		t.Errorf("app = %+v", app)
	}
	if app.LastRun == nil || app.LastRun.Conclusion != "failure" {
		t.Errorf("rollup = %+v", app.LastRun)
	}
	for _, p := range []string{".github/workflows/ci.yml", ".github/SECURITY.md", "cmd/main.go", "go.mod"} {
		if !app.Has(p) {
			t.Errorf("tree missing %s: %v", p, app.Tree)
		}
	}
}

func TestRunGraphQLLimitAndSingleRepo(t *testing.T) {
	s := graphqlServer(t, func(q string, v map[string]any) any {
		if strings.Contains(q, "repository(owner: $owner, name: $name)") {
			if v["name"] == "missing" {
				return map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{"login": "octo"}, "repository": nil},
					"errors": []any{map[string]string{"type": "NOT_FOUND", "message": "nope"}}}
			}
			r := light("one", nil)
			for k, val := range detail("# one") {
				r[k] = val
			}
			return map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{"__typename": "User", "login": "octo"}, "repository": r}}
		}
		if strings.Contains(q, "repositories(first: 100") {
			return map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{"__typename": "User", "login": "octo",
				"repositories": map[string]any{"pageInfo": map[string]any{"hasNextPage": true, "endCursor": "c"}, "nodes": []any{light("a", nil), light("b", nil), light("c", nil)}}}}}
		}
		return map[string]any{"data": map[string]any{}}
	})
	res, err := scan.RunGraphQL(context.Background(), s.Client(), scan.Options{Owner: "octo", MaxRepos: 2})
	if err != nil || len(res.Repos) != 2 {
		t.Fatalf("limit: %v %d", err, len(res.Repos))
	}
	res, err = scan.RunGraphQL(context.Background(), s.Client(), scan.Options{Owner: "octo", Repo: "one"})
	if err != nil || len(res.Repos) != 1 || res.Repos[0].Readme != "# one" {
		t.Fatalf("single: %v %+v", err, res)
	}
	if _, err := scan.RunGraphQL(context.Background(), s.Client(), scan.Options{Owner: "octo", Repo: "missing"}); !errors.Is(err, scan.ErrNotFound) {
		t.Errorf("missing repo error = %v", err)
	}
}

func TestRunGraphQLOwnerNotFound(t *testing.T) {
	s := graphqlServer(t, func(string, map[string]any) any {
		return map[string]any{"data": map[string]any{"repositoryOwner": nil}}
	})
	if _, err := scan.RunGraphQL(context.Background(), s.Client(), scan.Options{Owner: "ghost"}); !errors.Is(err, scan.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestRunGraphQLFallsBackForHeavyRepos(t *testing.T) {
	var rootOnly atomic.Int32
	s := ghtest.New(t)
	s.Handle("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(req.Query, "repositories(first: 100"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{"__typename": "User", "login": "octo",
				"repositories": map[string]any{"pageInfo": map[string]any{}, "nodes": []any{light("small", nil), light("huge", nil)}}}}})
		case strings.Contains(req.Query, "...D }"):
			heavy := strings.Contains(req.Query, "object { ... on Tree")
			if heavy && (req.Variables["n0"] == "huge" || req.Variables["n1"] == "huge") {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			if !heavy {
				rootOnly.Add(1)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"r0": detail("# " + req.Variables["n0"].(string))}})
		}
	})
	res, err := scan.RunGraphQL(context.Background(), s.Client(), scan.Options{Owner: "octo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Repos) != 2 || res.Repos[0].Readme != "# huge" || res.Repos[1].Readme != "# small" || rootOnly.Load() != 1 {
		t.Errorf("fallback: rootOnly=%d repos=%+v", rootOnly.Load(), res.Repos)
	}
}
