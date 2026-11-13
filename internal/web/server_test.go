package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeGitHub struct {
	calls     atomic.Int32
	rateLimit bool
}

func (f *fakeGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	b, _ := io.ReadAll(r.Body)
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	_ = json.Unmarshal(b, &req)
	var body any
	switch {
	case f.rateLimit:
		body = map[string]any{"errors": []any{map[string]string{"type": "RATE_LIMITED", "message": "API rate limit exceeded"}}}
	case req.Variables["login"] == "ghost" || req.Variables["owner"] == "ghost":
		body = map[string]any{"data": map[string]any{"repositoryOwner": nil}}
	case strings.Contains(req.Query, "repositories(first: 100"):
		if req.Variables["privacy"] != "PUBLIC" {
			body = map[string]any{"errors": []any{map[string]string{"message": "web must only request public repositories"}}}
			break
		}
		body = map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{
			"__typename": "User", "login": "Octo", "name": "Octo <Cat>",
			"repositories": map[string]any{"pageInfo": map[string]any{}, "nodes": []any{
				map[string]any{"name": "app", "nameWithOwner": "Octo/app", "url": "https://github.com/Octo/app", "owner": map[string]string{"login": "Octo"},
					"description": "An app", "licenseInfo": map[string]string{"spdxId": "MIT"}},
			}},
		}}}
	case strings.Contains(req.Query, "...D }"), strings.Contains(req.Query, "repository(owner: $owner, name: $name)"):
		repo := map[string]any{
			"defaultBranchRef": map[string]any{"name": "main"},
			"root":             map[string]any{"entries": []any{map[string]any{"name": "README.md", "type": "blob"}, map[string]any{"name": "main.go", "type": "blob"}, map[string]any{"name": "go.mod", "type": "blob"}}},
			"r1":               map[string]any{"text": "# app"},
		}
		if strings.Contains(req.Query, "$name") {
			repo["name"], repo["nameWithOwner"], repo["owner"] = "app", "Octo/app", map[string]string{"login": "Octo"}
			body = map[string]any{"data": map[string]any{"repositoryOwner": map[string]any{"__typename": "User", "login": "Octo"}, "repository": repo}}
		} else {
			body = map[string]any{"data": map[string]any{"r0": repo}}
		}
	default:
		body = map[string]any{"errors": []any{map[string]string{"message": "unexpected"}}}
	}
	out, _ := json.Marshal(body)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(out))), Request: r}, nil
}

func newTestServer(t *testing.T, f *fakeGitHub, tokens ...string) *Server {
	if len(tokens) == 0 {
		tokens = []string{"t1"}
	}
	s, err := New(Config{Tokens: tokens, Transport: f, Blocked: []string{"Hidden"},
		Now: func() time.Time { return time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(s *Server, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "http://gh-spruce.test"+path, nil)
	s.ServeHTTP(w, r)
	return w
}

func TestCardAndCaching(t *testing.T) {
	f := &fakeGitHub{}
	s := newTestServer(t, f)
	w := get(s, "/api/card?user=Octo")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/svg+xml") || !strings.Contains(w.Body.String(), "Octo's repo health") {
		t.Fatalf("card: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "s-maxage=43200") || w.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("headers: %v", w.Header())
	}
	calls := f.calls.Load()
	get(s, "/api/card?user=octo&layout=compact&theme=light")
	get(s, "/u/Octo")
	if f.calls.Load() != calls {
		t.Errorf("second request should be served from cache (%d → %d calls)", calls, f.calls.Load())
	}
	if w := get(s, "/api/card?user=Octo&theme=neon"); w.Code != 400 {
		t.Errorf("unknown theme = %d", w.Code)
	}
}

func TestBadges(t *testing.T) {
	s := newTestServer(t, &fakeGitHub{})
	w := get(s, "/api/badge?repo=Octo/app")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `aria-label="spruce: `) {
		t.Fatalf("repo badge: %d %s", w.Code, w.Body.String())
	}
	w = get(s, "/api/badge?user=Octo&label=health")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `aria-label="health: `) {
		t.Fatalf("user badge: %d %s", w.Code, w.Body.String())
	}
	if w := get(s, "/api/badge?repo=noslash"); w.Code != 400 {
		t.Errorf("bad repo = %d", w.Code)
	}
	if w := get(s, "/api/badge?repo=ghost/x"); w.Code != 404 || !strings.Contains(w.Body.String(), "not found") {
		t.Errorf("missing repo = %d %s", w.Code, w.Body.String())
	}
}

func TestErrors(t *testing.T) {
	s := newTestServer(t, &fakeGitHub{})
	cases := map[string]int{
		"/api/card?user=ghost":      404,
		"/api/card?user=bad%20name": 400,
		"/api/card?user=hidden":     403,
		"/api/card?user=" + url.QueryEscape(strings.Repeat("a", 40)): 400,
		"/api/report?user=ghost": 404,
		"/u/ghost":               404,
		"/api/nope":              404,
	}
	for path, want := range cases {
		if w := get(s, path); w.Code != want {
			t.Errorf("%s = %d, want %d", path, w.Code, want)
		}
	}
	w := get(s, "/api/card?user=ghost")
	if !strings.Contains(w.Header().Get("Cache-Control"), "s-maxage=300") || !strings.Contains(w.Body.String(), "not found") {
		t.Errorf("error card headers/body: %v %s", w.Header(), w.Body.String())
	}
}

func TestRateLimitRotatesTokens(t *testing.T) {
	f := &fakeGitHub{rateLimit: true}
	s := newTestServer(t, f, "t1", "t2")
	w := get(s, "/api/card?user=Octo")
	if w.Code != 429 || f.calls.Load() != 2 {
		t.Errorf("code=%d calls=%d, want 429 after trying both tokens", w.Code, f.calls.Load())
	}
	if w := get(s, "/api/card?user=Other"); w.Code != 429 || f.calls.Load() != 2 {
		t.Errorf("resting tokens should not be retried: code=%d calls=%d", w.Code, f.calls.Load())
	}
	empty, _ := New(Config{})
	if w := get(empty, "/api/card?user=Octo"); w.Code != 503 {
		t.Errorf("no tokens = %d", w.Code)
	}
}

func TestPageAndReport(t *testing.T) {
	s := newTestServer(t, &fakeGitHub{})
	w := get(s, "/api/index?route=page&user=Octo")
	body := w.Body.String()
	for _, want := range []string{"Octo &lt;Cat&gt;", "/api/card?user=Octo", "https://gh-spruce.test/u/Octo", "/api/badge?repo=Octo/app", "top two directory levels"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "Octo <Cat>") {
		t.Error("page is not escaped")
	}
	w = get(s, "/api/report?user=Octo")
	var rep struct {
		Score int `json:"score"`
		Repos []struct {
			Repo struct {
				Partial bool `json:"partial"`
			} `json:"repo"`
		} `json:"repos"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil || len(rep.Repos) != 1 || !rep.Repos[0].Repo.Partial {
		t.Errorf("report: %v %s", err, w.Body.String())
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("report should allow CORS")
	}
	if w := get(s, "/api/health"); w.Code != 200 || !strings.Contains(w.Body.String(), `"tokens":1`) {
		t.Errorf("health: %s", w.Body.String())
	}
}
