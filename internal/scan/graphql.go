package scan

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Emil-02/gh-spruce/internal/gh"
	"golang.org/x/sync/errgroup"
)

// The fast scanner reads everything through GraphQL in one query per page of
// repositories. The file tree is limited to two levels plus .github/workflows,
// so snapshots are marked Partial.

const lightFields = `
fragment L on Repository {
  name nameWithOwner url description homepageUrl isPrivate isFork isArchived isEmpty isTemplate
  pushedAt stargazerCount diskUsage viewerPermission
  owner { login }
  licenseInfo { spdxId }
  primaryLanguage { name }
  repositoryTopics(first: 20) { nodes { topic { name } } }
}`

const detailFields = `
fragment D on Repository {
  defaultBranchRef { name target { ... on Commit { oid statusCheckRollup { state } } } }
  root: object(expression: "HEAD:") { ... on Tree { entries { name type size object { ... on Tree { entries { name type } } } } } }
  workflows: object(expression: "HEAD:.github/workflows") { ... on Tree { entries { name type } } }
  r1: object(expression: "HEAD:README.md") { ... on Blob { text } }
  r2: object(expression: "HEAD:readme.md") { ... on Blob { text } }
  r3: object(expression: "HEAD:README") { ... on Blob { text } }
  r4: object(expression: "HEAD:Readme.md") { ... on Blob { text } }
  r5: object(expression: "HEAD:README.rst") { ... on Blob { text } }
}`

// rootFields is the fallback for huge repositories whose second tree level
// makes GitHub time out.
const rootFields = `
fragment D on Repository {
  defaultBranchRef { name target { ... on Commit { oid statusCheckRollup { state } } } }
  root: object(expression: "HEAD:") { ... on Tree { entries { name type size } } }
  workflows: object(expression: "HEAD:.github/workflows") { ... on Tree { entries { name type } } }
  r1: object(expression: "HEAD:README.md") { ... on Blob { text } }
  r2: object(expression: "HEAD:readme.md") { ... on Blob { text } }
}`

const ownerQuery = `query($login: String!, $after: String, $privacy: RepositoryPrivacy, $isFork: Boolean) {
  repositoryOwner(login: $login) {
    __typename login
    ... on User { name }
    ... on Organization { name }
    ... on ProfileOwner { pinnedItems(first: 6, types: REPOSITORY) { nodes { ... on Repository { name isPrivate owner { login } } } } }
    profile: repository(name: $login) { name }
    repositories(first: 100, after: $after, privacy: $privacy, isFork: $isFork, ownerAffiliations: OWNER, orderBy: {field: PUSHED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes { ...L }
    }
  }
}` + lightFields

const repoQuery = `query($owner: String!, $name: String!) {
  repositoryOwner(login: $owner) {
    __typename login
    ... on User { name }
    ... on Organization { name }
    profile: repository(name: $owner) { name }
  }
  repository(owner: $owner, name: $name) { ...L ...D }
}` + lightFields + detailFields

// batchQuery fetches trees and READMEs for a batch of repositories in one
// request; GitHub resolves them sequentially, so batches are kept small and
// run in parallel.
func batchQuery(n int, fields string) string {
	var b strings.Builder
	b.WriteString("query($owner: String!")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, ", $n%d: String!", i)
	}
	b.WriteString(") {")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, " r%d: repository(owner: $owner, name: $n%d) { ...D }", i, i)
	}
	b.WriteString(" }")
	return b.String() + fields
}

type gqlEntry struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	Object *struct {
		Entries []gqlEntry `json:"entries"`
	} `json:"object"`
}

type gqlBlob struct {
	Text *string `json:"text"`
}

type gqlRepo struct {
	Name             string    `json:"name"`
	NameWithOwner    string    `json:"nameWithOwner"`
	URL              string    `json:"url"`
	Description      string    `json:"description"`
	HomepageURL      string    `json:"homepageUrl"`
	IsPrivate        bool      `json:"isPrivate"`
	IsFork           bool      `json:"isFork"`
	IsArchived       bool      `json:"isArchived"`
	IsEmpty          bool      `json:"isEmpty"`
	IsTemplate       bool      `json:"isTemplate"`
	PushedAt         time.Time `json:"pushedAt"`
	StargazerCount   int       `json:"stargazerCount"`
	DiskUsage        int64     `json:"diskUsage"`
	ViewerPermission string    `json:"viewerPermission"`
	Owner            struct {
		Login string `json:"login"`
	} `json:"owner"`
	LicenseInfo *struct {
		SPDX string `json:"spdxId"`
	} `json:"licenseInfo"`
	PrimaryLanguage *struct {
		Name string `json:"name"`
	} `json:"primaryLanguage"`
	RepositoryTopics struct {
		Nodes []struct {
			Topic struct {
				Name string `json:"name"`
			} `json:"topic"`
		} `json:"nodes"`
	} `json:"repositoryTopics"`
	DefaultBranchRef *struct {
		Name   string `json:"name"`
		Target struct {
			OID    string `json:"oid"`
			Rollup *struct {
				State string `json:"state"`
			} `json:"statusCheckRollup"`
		} `json:"target"`
	} `json:"defaultBranchRef"`
	Root *struct {
		Entries []gqlEntry `json:"entries"`
	} `json:"root"`
	Workflows *struct {
		Entries []gqlEntry `json:"entries"`
	} `json:"workflows"`
	R1, R2, R3, R4, R5 *gqlBlob
}

type gqlOwner struct {
	Typename string `json:"__typename"`
	Login    string `json:"login"`
	Name     string `json:"name"`
	Pinned   *struct {
		Nodes []struct {
			Name      string `json:"name"`
			IsPrivate bool   `json:"isPrivate"`
			Owner     struct {
				Login string `json:"login"`
			} `json:"owner"`
		} `json:"nodes"`
	} `json:"pinnedItems"`
	Profile *struct {
		Name string `json:"name"`
	} `json:"profile"`
	Repositories struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Nodes []gqlRepo `json:"nodes"`
	} `json:"repositories"`
}

// ErrNotFound is returned when the owner or repository does not exist.
var ErrNotFound = fmt.Errorf("not found")

func (g gqlRepo) toRepo() *Repo {
	r := &Repo{
		Owner: g.Owner.Login, Name: g.Name, FullName: g.NameWithOwner, URL: g.URL,
		Description: strings.TrimSpace(g.Description), Homepage: strings.TrimSpace(g.HomepageURL),
		Private: g.IsPrivate, Fork: g.IsFork, Archived: g.IsArchived, Template: g.IsTemplate,
		Stars: g.StargazerCount, SizeKB: g.DiskUsage, PushedAt: g.PushedAt, Empty: g.IsEmpty, Partial: true,
	}
	switch g.ViewerPermission {
	case "ADMIN":
		r.CanAdmin, r.CanPush = true, true
	case "MAINTAIN", "WRITE":
		r.CanPush = true
	}
	if g.LicenseInfo != nil {
		switch g.LicenseInfo.SPDX {
		case "":
		case "NOASSERTION":
			r.License = "Other"
		default:
			r.License = g.LicenseInfo.SPDX
		}
	}
	if g.PrimaryLanguage != nil {
		r.Language = g.PrimaryLanguage.Name
	}
	for _, t := range g.RepositoryTopics.Nodes {
		r.Topics = append(r.Topics, t.Topic.Name)
	}
	if b := g.DefaultBranchRef; b != nil {
		r.DefaultBranch, r.HeadSHA = b.Name, b.Target.OID
		if b.Target.Rollup != nil {
			r.LastRun = rollupRun(b.Target.Rollup.State, g.URL)
		}
	}
	if g.Root != nil {
		for _, e := range g.Root.Entries {
			r.Tree = append(r.Tree, Entry{Path: e.Name, Type: e.Type, Size: e.Size})
			if e.Object != nil && e.Name != ".github" {
				for _, c := range e.Object.Entries {
					r.Tree = append(r.Tree, Entry{Path: e.Name + "/" + c.Name, Type: c.Type, Size: c.Size})
				}
			}
		}
	}
	if g.Workflows != nil {
		r.Tree = append(r.Tree, Entry{Path: ".github", Type: "tree"}, Entry{Path: ".github/workflows", Type: "tree"})
		for _, e := range g.Workflows.Entries {
			r.Tree = append(r.Tree, Entry{Path: ".github/workflows/" + e.Name, Type: e.Type})
		}
	}
	if g.Root != nil {
		for _, e := range g.Root.Entries {
			if e.Name == ".github" && e.Object != nil {
				for _, c := range e.Object.Entries {
					if c.Name != "workflows" {
						r.Tree = append(r.Tree, Entry{Path: ".github/" + c.Name, Type: c.Type, Size: c.Size})
					}
				}
			}
		}
	}
	names := []string{"README.md", "readme.md", "README", "Readme.md", "README.rst"}
	for i, b := range []*gqlBlob{g.R1, g.R2, g.R3, g.R4, g.R5} {
		if b != nil {
			r.ReadmePath = names[i]
			if b.Text != nil {
				r.Readme = *b.Text
			}
			break
		}
	}
	if r.ReadmePath == "" {
		for _, e := range r.Tree {
			if !strings.Contains(e.Path, "/") && strings.HasPrefix(strings.ToLower(e.Path), "readme") {
				r.ReadmePath = e.Path
			}
		}
	}
	if len(r.Tree) == 0 {
		r.Empty = true
	}
	return r
}

func fetchDetails(ctx context.Context, c *gh.Client, owner string, part []gqlRepo, fields string) error {
	v := map[string]any{"owner": owner}
	for i, r := range part {
		v[fmt.Sprintf("n%d", i)] = r.Name
	}
	out := map[string]*gqlRepo{}
	if err := c.GraphQL(ctx, batchQuery(len(part), fields), v, &out); err != nil {
		return err
	}
	for i := range part {
		if d := out[fmt.Sprintf("r%d", i)]; d != nil {
			part[i].mergeDetail(d)
		}
	}
	return nil
}

func (g *gqlRepo) mergeDetail(d *gqlRepo) {
	g.DefaultBranchRef, g.Root, g.Workflows = d.DefaultBranchRef, d.Root, d.Workflows
	g.R1, g.R2, g.R3, g.R4, g.R5 = d.R1, d.R2, d.R3, d.R4, d.R5
}

func rollupRun(state, url string) *WorkflowRun {
	run := &WorkflowRun{Name: "checks", Status: "completed", URL: url + "/actions"}
	switch state {
	case "SUCCESS":
		run.Conclusion = "success"
	case "FAILURE", "ERROR":
		run.Conclusion = "failure"
	default:
		run.Status = "in_progress"
	}
	return run
}

func (o gqlOwner) profile(viewer string) Profile {
	p := Profile{Login: o.Login, Name: o.Name, Type: o.Typename, HasReadme: o.Profile != nil,
		Authenticated: viewer != "" && strings.EqualFold(viewer, o.Login)}
	if o.Pinned != nil {
		p.PinnedKnown = true
		for _, n := range o.Pinned.Nodes {
			p.Pinned = append(p.Pinned, Pinned{Name: n.Name, Owner: n.Owner.Login, Private: n.IsPrivate})
		}
	}
	return p
}

// RunGraphQL is the fast scanner used by the web service and `--fast`.
// MaxRepos limits how many of the most recently pushed repositories are read.
func RunGraphQL(ctx context.Context, c *gh.Client, opts Options) (*Result, error) {
	viewer := opts.Viewer
	if opts.Owner == "" {
		viewer = Viewer(ctx, c)
		if viewer == "" {
			return nil, fmt.Errorf("cannot determine the authenticated user; pass an owner explicitly")
		}
		opts.Owner = viewer
	}
	if opts.Repo != "" {
		var out struct {
			Owner *gqlOwner `json:"repositoryOwner"`
			Repo  *gqlRepo  `json:"repository"`
		}
		err := c.GraphQL(ctx, repoQuery, map[string]any{"owner": opts.Owner, "name": opts.Repo}, &out)
		if gh.IsNotFoundGraphQL(err) || err == nil && (out.Owner == nil || out.Repo == nil) {
			return nil, fmt.Errorf("repository %s/%s: %w", opts.Owner, opts.Repo, ErrNotFound)
		}
		if err != nil {
			return nil, err
		}
		return &Result{Profile: out.Owner.profile(viewer), Repos: []*Repo{out.Repo.toRepo()}}, nil
	}

	vars := map[string]any{"login": opts.Owner}
	switch opts.Visibility {
	case "public":
		vars["privacy"] = "PUBLIC"
	case "private":
		vars["privacy"] = "PRIVATE"
	}
	if !opts.IncludeForks {
		vars["isFork"] = false
	}
	limit := opts.MaxRepos
	if limit <= 0 {
		limit = 1000
	}
	res := &Result{}
	var listed []gqlRepo
	for page := 0; ; page++ {
		var out struct {
			Owner *gqlOwner `json:"repositoryOwner"`
		}
		err := c.GraphQL(ctx, ownerQuery, vars, &out)
		if gh.IsNotFoundGraphQL(err) || err == nil && out.Owner == nil {
			return nil, fmt.Errorf("owner %q: %w", opts.Owner, ErrNotFound)
		}
		if err != nil {
			return nil, err
		}
		if page == 0 {
			res.Profile = out.Owner.profile(viewer)
		}
		for _, g := range out.Owner.Repositories.Nodes {
			if len(listed) >= limit {
				break
			}
			if g.IsArchived && !opts.IncludeArchived || opts.Ignore != nil && opts.Ignore(g.Name) {
				continue
			}
			listed = append(listed, g)
		}
		pi := out.Owner.Repositories.PageInfo
		if !pi.HasNextPage || len(listed) >= limit {
			break
		}
		vars["after"] = pi.EndCursor
	}

	const batch = 5
	conc := opts.Concurrency
	if conc <= 0 {
		conc = 6
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(conc)
	for start := 0; start < len(listed); start += batch {
		part := listed[start:min(start+batch, len(listed))]
		g.Go(func() error {
			if err := fetchDetails(gctx, c, res.Profile.Login, part, detailFields); err != nil {
				if gh.IsRateLimited(err) || gctx.Err() != nil {
					return err
				}
				// One heavy repository can make the whole batch time out:
				// retry one by one, then with the root level only.
				for i := range part {
					one := part[i : i+1]
					if err := fetchDetails(gctx, c, res.Profile.Login, one, detailFields); err != nil {
						if err := fetchDetails(gctx, c, res.Profile.Login, one, rootFields); err != nil {
							return fmt.Errorf("%s: %w", part[i].Name, err)
						}
					}
				}
			}
			if opts.Progress != nil {
				opts.Progress(min(start+batch, len(listed)), len(listed))
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	for _, l := range listed {
		res.Repos = append(res.Repos, l.toRepo())
	}
	sortRepos(res.Repos)
	return res, nil
}
