// Package web serves health cards, badges and reports over HTTP. It powers
// the Vercel deployment (api/index.go) and the local dev server.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Emil-02/gh-spruce/internal/card"
	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/Emil-02/gh-spruce/internal/config"
	"github.com/Emil-02/gh-spruce/internal/gh"
	"github.com/Emil-02/gh-spruce/internal/scan"
	"golang.org/x/sync/singleflight"
)

type Config struct {
	Tokens    []string
	Blocked   []string
	Transport http.RoundTripper
	Now       func() time.Time
	MaxRepos  int
	CacheTTL  time.Duration
	Timeout   time.Duration
}

type Server struct {
	cfg     Config
	pool    *pool
	reports *ttlCache[*checks.Report]
	group   singleflight.Group
	blocked map[string]bool
	rules   *config.Config
}

func New(cfg Config) (*Server, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxRepos <= 0 {
		cfg.MaxRepos = 100
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 10 * time.Minute
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 25 * time.Second
	}
	p, err := newPool(cfg.Tokens, cfg.Transport, cfg.Now)
	if err != nil {
		return nil, err
	}
	s := &Server{cfg: cfg, pool: p, reports: newCache[*checks.Report](256, cfg.Now), blocked: map[string]bool{}, rules: config.Default()}
	for _, b := range cfg.Blocked {
		s.blocked[strings.ToLower(strings.TrimSpace(b))] = true
	}
	return s, nil
}

func splitEnv(name string) []string {
	var out []string
	for _, v := range strings.Split(os.Getenv(name), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Default builds a server from GITHUB_TOKENS (comma-separated) or
// GITHUB_TOKEN, and BLOCKED_USERS.
var Default = sync.OnceValue(func() *Server {
	tokens := splitEnv("GITHUB_TOKENS")
	if len(tokens) == 0 {
		tokens = splitEnv("GITHUB_TOKEN")
	}
	s, err := New(Config{Tokens: tokens, Blocked: splitEnv("BLOCKED_USERS")})
	if err != nil {
		log.Printf("gh-spruce web: %v", err)
		s, _ = New(Config{})
	}
	return s
})

var (
	userRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	repoRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

var errBlocked = errors.New("this profile opted out of gh-spruce")

type httpError struct {
	status int
	msg    string
}

func (e *httpError) Error() string { return e.msg }

func badRequest(msg string) error { return &httpError{http.StatusBadRequest, msg} }

func classify(err error) (int, string) {
	var he *httpError
	switch {
	case errors.As(err, &he):
		return he.status, he.msg
	case errors.Is(err, scan.ErrNotFound) || gh.IsNotFound(err):
		return http.StatusNotFound, "not found on GitHub"
	case errors.Is(err, errBlocked):
		return http.StatusForbidden, err.Error()
	case errors.Is(err, ErrRateLimited):
		return http.StatusTooManyRequests, err.Error()
	case errors.Is(err, ErrNoTokens):
		return http.StatusServiceUnavailable, "server is not configured"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "GitHub took too long, try again"
	}
	return http.StatusBadGateway, "could not reach GitHub"
}

func (s *Server) validUser(u string) error {
	if !userRe.MatchString(u) {
		return badRequest("invalid GitHub username")
	}
	if s.blocked[strings.ToLower(u)] {
		return errBlocked
	}
	return nil
}

func (s *Server) report(ctx context.Context, owner, repo string) (*checks.Report, error) {
	if err := s.validUser(owner); err != nil {
		return nil, err
	}
	if repo != "" && !repoRe.MatchString(repo) {
		return nil, badRequest("invalid repository name")
	}
	key := strings.ToLower(owner + "/" + repo)
	if r, err, ok := s.reports.get(key); ok {
		return r, err
	}
	v, err, _ := s.group.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.Timeout)
		defer cancel()
		var res *scan.Result
		err := s.pool.Do(ctx, func(c *gh.Client) error {
			var err error
			res, err = scan.RunGraphQL(ctx, c, scan.Options{Owner: owner, Repo: repo, Visibility: "public", MaxRepos: s.cfg.MaxRepos, Concurrency: 10})
			return err
		})
		if err != nil {
			if errors.Is(err, scan.ErrNotFound) {
				s.reports.set(key, nil, err, time.Minute)
			}
			return nil, err
		}
		rep := checks.Build(res, s.rules, s.cfg.Now())
		s.reports.set(key, rep, nil, s.cfg.CacheTTL)
		return rep, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*checks.Report), nil
}

const (
	longCache  = "public, max-age=3600, s-maxage=43200, stale-while-revalidate=86400"
	pageCache  = "public, max-age=600, s-maxage=3600, stale-while-revalidate=86400"
	errorCache = "public, max-age=60, s-maxage=300"
)

func writeSVG(w http.ResponseWriter, status int, cache, svg string) {
	h := w.Header()
	h.Set("Content-Type", "image/svg+xml; charset=utf-8")
	h.Set("Cache-Control", cache)
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(svg))
}

// ServeHTTP routes /api/<route> and /u/<user>. On Vercel every path is
// rewritten to /api/index?route=<route>.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	route := q.Get("route")
	if route == "" {
		p := strings.Trim(r.URL.Path, "/")
		switch {
		case strings.HasPrefix(p, "u/"):
			route = "page"
			q.Set("user", strings.TrimPrefix(p, "u/"))
		default:
			route = strings.TrimPrefix(p, "api/")
		}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	switch route {
	case "card":
		s.card(w, r, q)
	case "badge":
		s.badge(w, r, q)
	case "report":
		s.reportJSON(w, r, q)
	case "page":
		s.page(w, r, q.Get("user"))
	case "health":
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"ok":true,"tokens":%d}`, len(s.pool.clients))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) card(w http.ResponseWriter, r *http.Request, q url.Values) {
	get := q.Get
	theme := get("theme")
	if theme == "" {
		theme = "dark"
	}
	if _, ok := card.Themes[theme]; !ok {
		writeSVG(w, http.StatusBadRequest, errorCache, card.ErrorCard("unknown theme "+theme, "dark"))
		return
	}
	rep, err := s.report(r.Context(), get("user"), "")
	if err != nil {
		status, msg := classify(err)
		writeSVG(w, status, errorCache, card.ErrorCard(get("user")+": "+msg, theme))
		return
	}
	var svg string
	if get("layout") == "compact" {
		svg, err = card.RenderCompact(rep, theme)
	} else {
		svg, err = card.Render(rep, theme)
	}
	if err != nil {
		writeSVG(w, http.StatusInternalServerError, errorCache, card.ErrorCard(err.Error(), theme))
		return
	}
	writeSVG(w, http.StatusOK, longCache, svg)
}

func (s *Server) badge(w http.ResponseWriter, r *http.Request, q url.Values) {
	get := q.Get
	label := get("label")
	if len(label) > 30 {
		label = label[:30]
	}
	var (
		rep   *checks.Report
		err   error
		owner = get("user")
		repo  string
	)
	if full := get("repo"); full != "" {
		var ok bool
		owner, repo, ok = strings.Cut(full, "/")
		if !ok {
			err = badRequest("use repo=owner/name")
		}
		if label == "" {
			label = "spruce"
		}
	} else if label == "" {
		label = "repo health"
	}
	if err == nil {
		rep, err = s.report(r.Context(), owner, repo)
	}
	if err != nil {
		status, msg := classify(err)
		if status == http.StatusNotFound {
			msg = "not found"
		} else if len(msg) > 24 {
			msg = "error"
		}
		writeSVG(w, status, errorCache, card.Badge(label, msg, "#9f9f9f"))
		return
	}
	score, grade := rep.Score, rep.Grade
	if repo != "" && len(rep.Repos) == 1 {
		score, grade = rep.Repos[0].Score, rep.Repos[0].Grade
	}
	writeSVG(w, http.StatusOK, longCache, card.ScoreBadge(label, score, grade))
}

func (s *Server) reportJSON(w http.ResponseWriter, r *http.Request, q url.Values) {
	user := q.Get("user")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	rep, err := s.report(r.Context(), user, "")
	if err != nil {
		status, msg := classify(err)
		w.Header().Set("Cache-Control", errorCache)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
		return
	}
	w.Header().Set("Cache-Control", pageCache)
	_ = json.NewEncoder(w).Encode(rep)
}

//go:embed templates/*.html
var templateFS embed.FS

var pageTmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"tone": func(score int) string {
		switch {
		case score >= 85:
			return "good"
		case score >= 55:
			return "ok"
		}
		return "bad"
	},
	"icon": func(s checks.Status) string {
		switch s {
		case checks.Pass:
			return "✓"
		case checks.Warn:
			return "~"
		case checks.Fail:
			return "✗"
		}
		return "·"
	},
	"partial": func(rep *checks.Report) bool {
		for _, r := range rep.Repos {
			if r.Repo.Partial {
				return true
			}
		}
		return false
	},
	"public": func(rep *checks.Report) int {
		n := 0
		for _, r := range rep.Repos {
			if !r.Repo.Private {
				n++
			}
		}
		return n
	},
}).ParseFS(templateFS, "templates/*.html"))

type pageData struct {
	Limit  int
	User   string
	Base   string
	Report *checks.Report
	Error  string
	Status int
}

func baseURL(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	scheme := "https"
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
		scheme = "http"
	}
	return scheme + "://" + host
}

func (s *Server) page(w http.ResponseWriter, r *http.Request, user string) {
	d := pageData{User: user, Base: baseURL(r), Status: http.StatusOK, Limit: s.cfg.MaxRepos}
	rep, err := s.report(r.Context(), user, "")
	cache := pageCache
	if err != nil {
		d.Status, d.Error = classify(err)
		cache = errorCache
	} else {
		d.Report = rep
		d.User = rep.Profile.Login
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", cache)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.WriteHeader(d.Status)
	if err := pageTmpl.ExecuteTemplate(w, "page.html", d); err != nil {
		log.Printf("render page: %v", err)
	}
}
