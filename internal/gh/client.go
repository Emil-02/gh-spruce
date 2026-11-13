// Package gh is a thin wrapper around the go-gh REST client that adds
// pagination, typed errors and JSON helpers.
package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

type Client struct {
	rest *api.RESTClient
}

type Options struct {
	Host      string
	Token     string
	Transport http.RoundTripper
	CacheTTL  time.Duration
}

func New(opts Options) (*Client, error) {
	o := api.ClientOptions{
		Host:      opts.Host,
		AuthToken: opts.Token,
		Transport: opts.Transport,
		Headers:   map[string]string{"X-GitHub-Api-Version": "2022-11-28"},
	}
	if opts.CacheTTL > 0 {
		o.EnableCache = true
		o.CacheTTL = opts.CacheTTL
	}
	rest, err := api.NewRESTClient(o)
	if err != nil {
		return nil, err
	}
	return &Client{rest: rest}, nil
}

// Status returns the HTTP status code of an API error, or 0.
func Status(err error) int {
	var he *api.HTTPError
	if errors.As(err, &he) {
		return he.StatusCode
	}
	return 0
}

func IsNotFound(err error) bool { return Status(err) == http.StatusNotFound }

func (c *Client) Get(ctx context.Context, path string, out any) error {
	return c.rest.DoWithContext(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) Send(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	return c.rest.DoWithContext(ctx, method, path, body, out)
}

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// Paginate follows Link headers and appends every page (a JSON array) into out.
func Paginate[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	for path != "" {
		resp, err := c.rest.RequestWithContext(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var page []T
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decoding %s: %w", path, err)
		}
		all = append(all, page...)
		path = ""
		if m := nextLink.FindStringSubmatch(resp.Header.Get("Link")); m != nil {
			path = m[1]
		}
	}
	return all, nil
}
