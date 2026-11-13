package scan

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Emil-02/gh-spruce/internal/gh"
	"golang.org/x/sync/errgroup"
)

type Options struct {
	Owner           string
	Repo            string // scan a single repository
	IncludeForks    bool
	IncludeArchived bool
	Visibility      string // all | public | private
	Concurrency     int
	Ignore          func(name string) bool
	Progress        func(done, total int)
	// Fast uses the GraphQL scanner (two tree levels, far fewer API calls).
	Fast bool
	// MaxRepos limits the fast scanner to the most recently pushed repositories.
	MaxRepos int
	// Viewer is the authenticated login when already known.
	Viewer string
}

type apiRepo struct {
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	Description   string `json:"description"`
	Homepage      string `json:"homepage"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Fork          bool   `json:"fork"`
	Archived      bool   `json:"archived"`
	IsTemplate    bool   `json:"is_template"`
	HasPages      bool   `json:"has_pages"`
	Topics        []string
	Language      string    `json:"language"`
	Stars         int       `json:"stargazers_count"`
	Size          int64     `json:"size"`
	PushedAt      time.Time `json:"pushed_at"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
	License *struct {
		SPDX string `json:"spdx_id"`
	} `json:"license"`
	Permissions *struct {
		Admin bool `json:"admin"`
		Push  bool `json:"push"`
	} `json:"permissions"`
}

func (a apiRepo) toRepo() *Repo {
	r := &Repo{
		Owner: a.Owner.Login, Name: a.Name, FullName: a.FullName, URL: a.HTMLURL,
		Description: strings.TrimSpace(a.Description), Homepage: strings.TrimSpace(a.Homepage),
		DefaultBranch: a.DefaultBranch, Private: a.Private, Fork: a.Fork, Archived: a.Archived,
		Template: a.IsTemplate, HasPages: a.HasPages, Topics: a.Topics, Language: a.Language,
		Stars: a.Stars, SizeKB: a.Size, PushedAt: a.PushedAt,
	}
	if a.License != nil && a.License.SPDX != "" && a.License.SPDX != "NOASSERTION" {
		r.License = a.License.SPDX
	} else if a.License != nil && a.License.SPDX == "NOASSERTION" {
		r.License = "Other"
	}
	if a.Permissions != nil {
		r.CanPush, r.CanAdmin = a.Permissions.Push, a.Permissions.Admin
	}
	return r
}

// Viewer returns the authenticated login, or "" when the token cannot
// identify a user (e.g. the GITHUB_TOKEN of an Actions run).
func Viewer(ctx context.Context, c *gh.Client) string {
	var u struct {
		Login string `json:"login"`
	}
	if err := c.Get(ctx, "user", &u); err != nil {
		return ""
	}
	return u.Login
}

func Run(ctx context.Context, c *gh.Client, opts Options) (*Result, error) {
	if opts.Fast {
		if opts.Viewer == "" {
			opts.Viewer = Viewer(ctx, c)
		}
		return RunGraphQL(ctx, c, opts)
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	viewer := Viewer(ctx, c)
	if opts.Owner == "" {
		if viewer == "" {
			return nil, fmt.Errorf("cannot determine the authenticated user; pass an owner explicitly")
		}
		opts.Owner = viewer
	}

	var owner struct {
		Login string `json:"login"`
		Name  string `json:"name"`
		Type  string `json:"type"`
	}
	if err := c.Get(ctx, "users/"+url.PathEscape(opts.Owner), &owner); err != nil {
		if gh.IsNotFound(err) {
			return nil, fmt.Errorf("owner %q not found", opts.Owner)
		}
		return nil, err
	}
	res := &Result{Profile: Profile{
		Login: owner.Login, Name: owner.Name, Type: owner.Type,
		Authenticated: strings.EqualFold(viewer, owner.Login),
	}}

	var list []apiRepo
	var err error
	switch {
	case opts.Repo != "":
		var one apiRepo
		err = c.Get(ctx, fmt.Sprintf("repos/%s/%s", url.PathEscape(owner.Login), url.PathEscape(opts.Repo)), &one)
		list = []apiRepo{one}
	case res.Profile.Authenticated:
		list, err = gh.Paginate[apiRepo](ctx, c, "user/repos?affiliation=owner&per_page=100")
	case owner.Type == "Organization":
		list, err = gh.Paginate[apiRepo](ctx, c, "orgs/"+url.PathEscape(owner.Login)+"/repos?type=all&per_page=100")
	default:
		list, err = gh.Paginate[apiRepo](ctx, c, "users/"+url.PathEscape(owner.Login)+"/repos?type=owner&per_page=100")
	}
	if err != nil {
		return nil, fmt.Errorf("listing repositories: %w", err)
	}

	for _, a := range list {
		if strings.EqualFold(a.Name, owner.Login) {
			res.Profile.HasReadme = true
		}
		if opts.Repo == "" {
			if (a.Fork && !opts.IncludeForks) || (a.Archived && !opts.IncludeArchived) {
				continue
			}
			if (opts.Visibility == "public" && a.Private) || (opts.Visibility == "private" && !a.Private) {
				continue
			}
			if opts.Ignore != nil && opts.Ignore(a.Name) {
				continue
			}
		}
		res.Repos = append(res.Repos, a.toRepo())
	}
	if opts.Repo != "" && !res.Profile.HasReadme {
		var tmp apiRepo
		res.Profile.HasReadme = c.Get(ctx, fmt.Sprintf("repos/%s/%s", owner.Login, owner.Login), &tmp) == nil
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(opts.Concurrency)
	done := make(chan struct{}, len(res.Repos))
	for _, r := range res.Repos {
		g.Go(func() error {
			defer func() { done <- struct{}{} }()
			if err := enrich(gctx, c, r); err != nil {
				return fmt.Errorf("%s: %w", r.FullName, err)
			}
			return nil
		})
	}
	go func() {
		n := 0
		for range done {
			n++
			if opts.Progress != nil {
				opts.Progress(n, len(res.Repos))
			}
			if n == len(res.Repos) {
				return
			}
		}
	}()
	if err := g.Wait(); err != nil {
		return nil, err
	}
	res.Profile.Pinned, res.Profile.PinnedKnown = pinned(ctx, c, owner.Login)
	sortRepos(res.Repos)
	return res, nil
}

func sortRepos(repos []*Repo) {
	sort.Slice(repos, func(i, j int) bool { return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name) })
}

func enrich(ctx context.Context, c *gh.Client, r *Repo) error {
	base := "repos/" + r.FullName
	var tree struct {
		SHA       string  `json:"sha"`
		Tree      []Entry `json:"tree"`
		Truncated bool    `json:"truncated"`
	}
	err := c.Get(ctx, fmt.Sprintf("%s/git/trees/%s?recursive=1", base, url.PathEscape(r.DefaultBranch)), &tree)
	switch {
	case err == nil:
	case gh.Status(err) == http.StatusConflict || gh.IsNotFound(err):
		r.Empty = true
		return nil
	default:
		return err
	}
	r.HeadSHA, r.Tree, r.TreeTruncated = tree.SHA, tree.Tree, tree.Truncated
	r.Empty = len(tree.Tree) == 0

	var readme struct {
		Path     string `json:"path"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := c.Get(ctx, base+"/readme", &readme); err == nil {
		r.ReadmePath = readme.Path
		if readme.Encoding == "base64" {
			b, _ := base64.StdEncoding.DecodeString(strings.ReplaceAll(readme.Content, "\n", ""))
			r.Readme = string(b)
		}
	} else if !gh.IsNotFound(err) {
		return err
	}

	if len(r.Workflows()) > 0 {
		var runs struct {
			Runs []WorkflowRun `json:"workflow_runs"`
		}
		path := fmt.Sprintf("%s/actions/runs?branch=%s&per_page=1&exclude_pull_requests=true", base, url.QueryEscape(r.DefaultBranch))
		if err := c.Get(ctx, path, &runs); err == nil && len(runs.Runs) > 0 {
			r.LastRun = &runs.Runs[0]
		}
	}
	return nil
}

func pinned(ctx context.Context, c *gh.Client, login string) ([]Pinned, bool) {
	q := `query($login:String!){repositoryOwner(login:$login){... on ProfileOwner{pinnedItems(first:6,types:REPOSITORY){nodes{... on Repository{name isPrivate owner{login}}}}}}}`
	var out struct {
		Errors []any `json:"errors"`
		Data   struct {
			Owner struct {
				Pinned struct {
					Nodes []struct {
						Name      string `json:"name"`
						IsPrivate bool   `json:"isPrivate"`
						Owner     struct {
							Login string `json:"login"`
						} `json:"owner"`
					} `json:"nodes"`
				} `json:"pinnedItems"`
			} `json:"repositoryOwner"`
		} `json:"data"`
	}
	body := map[string]any{"query": q, "variables": map[string]any{"login": login}}
	if err := c.Send(ctx, http.MethodPost, "graphql", body, &out); err != nil || len(out.Errors) > 0 {
		return nil, false
	}
	var res []Pinned
	for _, n := range out.Data.Owner.Pinned.Nodes {
		res = append(res, Pinned{Name: n.Name, Owner: n.Owner.Login, Private: n.IsPrivate})
	}
	return res, true
}
