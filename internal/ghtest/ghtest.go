// Package ghtest provides a tiny fake GitHub API for tests.
package ghtest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Emil-02/gh-spruce/internal/gh"
)

type Request struct {
	Method, Path string
	Body         map[string]any
}

type Server struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	routes   map[string]func(w http.ResponseWriter, r *http.Request)
	Requests []Request
}

func New(t *testing.T) *Server {
	s := &Server{t: t, routes: map[string]func(http.ResponseWriter, *http.Request){}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// JSON registers a fixed JSON response for "METHOD /path" (query ignored).
func (s *Server) JSON(route string, status int, v any) {
	s.routes[route] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
}

func (s *Server) Handle(route string, h func(w http.ResponseWriter, r *http.Request)) {
	s.routes[route] = h
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	b, _ := io.ReadAll(r.Body)
	if len(b) > 0 {
		_ = json.Unmarshal(b, &body)
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	s.mu.Lock()
	s.Requests = append(s.Requests, Request{Method: r.Method, Path: r.URL.Path, Body: body})
	s.mu.Unlock()
	if h, ok := s.routes[r.Method+" "+r.URL.Path]; ok {
		h(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"message":"Not Found"}`)
}

// Find returns recorded requests matching method and path prefix.
func (s *Server) Find(method, prefix string) []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Request
	for _, r := range s.Requests {
		if r.Method == method && strings.HasPrefix(r.Path, prefix) {
			out = append(out, r)
		}
	}
	return out
}

type rewrite struct{ target *url.URL }

func (rw rewrite) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host = rw.target.Scheme, rw.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

// Client returns a real gh.Client whose traffic goes to the fake server.
func (s *Server) Client() *gh.Client {
	u, _ := url.Parse(s.srv.URL)
	c, err := gh.New(gh.Options{Host: "github.com", Token: "test-token", Transport: rewrite{u}})
	if err != nil {
		s.t.Fatal(err)
	}
	return c
}
