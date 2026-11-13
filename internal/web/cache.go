package web

import (
	"sync"
	"time"
)

type entry[V any] struct {
	val     V
	err     error
	expires time.Time
}

// ttlCache is a small per-instance cache; the CDN does the heavy lifting.
type ttlCache[V any] struct {
	mu    sync.Mutex
	items map[string]entry[V]
	order []string
	max   int
	now   func() time.Time
}

func newCache[V any](max int, now func() time.Time) *ttlCache[V] {
	return &ttlCache[V]{items: map[string]entry[V]{}, max: max, now: now}
}

func (c *ttlCache[V]) get(key string) (V, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[key]
	if !ok || c.now().After(e.expires) {
		var zero V
		return zero, nil, false
	}
	return e.val, e.err, true
}

func (c *ttlCache[V]) set(key string, v V, err error, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok {
		c.order = append(c.order, key)
	}
	c.items[key] = entry[V]{val: v, err: err, expires: c.now().Add(ttl)}
	for len(c.order) > c.max {
		delete(c.items, c.order[0])
		c.order = c.order[1:]
	}
}
