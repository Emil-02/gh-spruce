package web

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Emil-02/gh-spruce/internal/gh"
)

// ErrNoTokens means the server has no GitHub token configured.
var ErrNoTokens = errors.New("no GitHub token configured")

// ErrRateLimited means every token is out of quota.
var ErrRateLimited = errors.New("GitHub API rate limit reached, try again later")

// pool rotates requests over several tokens and rests exhausted ones.
type pool struct {
	clients []*gh.Client
	next    atomic.Uint64
	mu      sync.Mutex
	resting map[int]time.Time
	now     func() time.Time
}

func newPool(tokens []string, transport http.RoundTripper, now func() time.Time) (*pool, error) {
	p := &pool{resting: map[int]time.Time{}, now: now}
	for _, t := range tokens {
		c, err := gh.New(gh.Options{Host: "github.com", Token: t, Transport: transport})
		if err != nil {
			return nil, err
		}
		p.clients = append(p.clients, c)
	}
	return p, nil
}

func (p *pool) available(i int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	until, ok := p.resting[i]
	return !ok || p.now().After(until)
}

func (p *pool) rest(i int) {
	p.mu.Lock()
	p.resting[i] = p.now().Add(10 * time.Minute)
	p.mu.Unlock()
}

// Do runs fn with the next available client, moving on when a token is rate limited.
func (p *pool) Do(ctx context.Context, fn func(*gh.Client) error) error {
	n := len(p.clients)
	if n == 0 {
		return ErrNoTokens
	}
	start := int(p.next.Add(1))
	for k := 0; k < n; k++ {
		i := (start + k) % n
		if !p.available(i) {
			continue
		}
		err := fn(p.clients[i])
		if !gh.IsRateLimited(err) {
			return err
		}
		p.rest(i)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return ErrRateLimited
}
