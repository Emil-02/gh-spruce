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
	"strings"
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

// GraphQLError is returned when the GraphQL API reports errors.
type GraphQLError struct {
	Type    string
	Message string
}

func (e *GraphQLError) Error() string {
	if e.Type != "" {
		return "GraphQL " + e.Type + ": " + e.Message
	}
	return "GraphQL: " + e.Message
}

// GraphQL runs a query and decodes its data into out. Errors are returned
// only when the response has no usable data or the API is rate limited.
func (c *Client) GraphQL(ctx context.Context, query string, vars map[string]any, out any) error {
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.Send(ctx, http.MethodPost, "graphql", map[string]any{"query": query, "variables": vars}, &resp); err != nil {
		return err
	}
	for _, e := range resp.Errors {
		if e.Type == "RATE_LIMITED" {
			return &GraphQLError{Type: e.Type, Message: e.Message}
		}
	}
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		if len(resp.Errors) > 0 {
			return &GraphQLError{Type: resp.Errors[0].Type, Message: resp.Errors[0].Message}
		}
		return &GraphQLError{Message: "empty response"}
	}
	return json.Unmarshal(resp.Data, out)
}

// IsRateLimited reports whether err means the token is out of quota.
func IsRateLimited(err error) bool {
	var ge *GraphQLError
	if errors.As(err, &ge) {
		return ge.Type == "RATE_LIMITED"
	}
	var he *api.HTTPError
	if errors.As(err, &he) {
		if he.StatusCode == http.StatusTooManyRequests {
			return true
		}
		if he.StatusCode == http.StatusForbidden {
			return he.Headers.Get("X-Ratelimit-Remaining") == "0" || strings.Contains(strings.ToLower(he.Message), "rate limit")
		}
	}
	return false
}

// IsNotFoundGraphQL reports a GraphQL NOT_FOUND error.
func IsNotFoundGraphQL(err error) bool {
	var ge *GraphQLError
	return errors.As(err, &ge) && ge.Type == "NOT_FOUND"
}
