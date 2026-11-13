package fix

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Emil-02/gh-spruce/internal/gh"
)

const Branch = "spruce/fixes"

type Outcome struct {
	Repo     string `json:"repo"`
	Mode     string `json:"mode"` // pr | direct | metadata
	URL      string `json:"url,omitempty"`
	Metadata bool   `json:"metadata_updated"`
	Error    string `json:"error,omitempty"`
}

type Applier struct {
	Client *gh.Client
	Direct bool
}

// Apply writes metadata through the repository API and files through a
// single commit made with the Git Data API.
func (a *Applier) Apply(ctx context.Context, p *Patch) Outcome {
	out := Outcome{Repo: p.FullName}
	fail := func(err error) Outcome {
		out.Error = err.Error()
		if strings.Contains(out.Error, "workflow") && strings.Contains(out.Error, "scope") {
			out.Error += " (run: gh auth refresh -s workflow)"
		}
		return out
	}
	base := "repos/" + p.FullName

	if len(p.Files) > 0 {
		u, err := a.commit(ctx, p)
		if err != nil {
			return fail(err)
		}
		out.URL = u
		out.Mode = "pr"
		if a.Direct {
			out.Mode = "direct"
		}
	}

	meta := map[string]any{}
	if p.Description != nil {
		meta["description"] = *p.Description
	}
	if p.Homepage != nil {
		meta["homepage"] = *p.Homepage
	}
	if len(meta) > 0 {
		if err := a.Client.Send(ctx, http.MethodPatch, base, meta, nil); err != nil {
			return fail(fmt.Errorf("updating metadata: %w", err))
		}
		out.Metadata = true
	}
	if p.Topics != nil {
		if err := a.Client.Send(ctx, http.MethodPut, base+"/topics", map[string]any{"names": p.Topics}, nil); err != nil {
			return fail(fmt.Errorf("updating topics: %w", err))
		}
		out.Metadata = true
	}
	if p.Archive {
		if err := a.Client.Send(ctx, http.MethodPatch, base, map[string]any{"archived": true}, nil); err != nil {
			return fail(fmt.Errorf("archiving: %w", err))
		}
		out.Metadata = true
	}
	if out.Mode == "" {
		out.Mode = "metadata"
		out.URL = "https://github.com/" + p.FullName
	}
	return out
}

func (a *Applier) commit(ctx context.Context, p *Patch) (string, error) {
	r := p.Repo
	base := "repos/" + r.FullName
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := a.Client.Get(ctx, base+"/git/ref/heads/"+escapeRef(r.DefaultBranch), &ref); err != nil {
		return "", fmt.Errorf("reading %s: %w", r.DefaultBranch, err)
	}
	head := ref.Object.SHA
	var parent struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := a.Client.Get(ctx, base+"/git/commits/"+head, &parent); err != nil {
		return "", err
	}
	type entry struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	var entries []entry
	for _, f := range p.Files {
		entries = append(entries, entry{Path: f.Path, Mode: "100644", Type: "blob", Content: f.Content})
	}
	var tree, commit struct {
		SHA string `json:"sha"`
	}
	if err := a.Client.Send(ctx, http.MethodPost, base+"/git/trees", map[string]any{"base_tree": parent.Tree.SHA, "tree": entries}, &tree); err != nil {
		return "", fmt.Errorf("creating tree: %w", err)
	}
	msg := CommitMessage(p)
	if err := a.Client.Send(ctx, http.MethodPost, base+"/git/commits", map[string]any{"message": msg, "tree": tree.SHA, "parents": []string{head}}, &commit); err != nil {
		return "", fmt.Errorf("creating commit: %w", err)
	}

	if a.Direct {
		if err := a.Client.Send(ctx, http.MethodPatch, base+"/git/refs/heads/"+escapeRef(r.DefaultBranch), map[string]any{"sha": commit.SHA}, nil); err != nil {
			return "", fmt.Errorf("updating %s: %w", r.DefaultBranch, err)
		}
		return fmt.Sprintf("https://github.com/%s/commit/%s", r.FullName, commit.SHA), nil
	}

	err := a.Client.Send(ctx, http.MethodPost, base+"/git/refs", map[string]any{"ref": "refs/heads/" + Branch, "sha": commit.SHA}, nil)
	if gh.Status(err) == http.StatusUnprocessableEntity {
		err = a.Client.Send(ctx, http.MethodPatch, base+"/git/refs/heads/"+escapeRef(Branch), map[string]any{"sha": commit.SHA, "force": true}, nil)
	}
	if err != nil {
		return "", fmt.Errorf("pushing branch %s: %w", Branch, err)
	}

	var open []struct {
		URL string `json:"html_url"`
	}
	q := fmt.Sprintf("%s/pulls?state=open&head=%s", base, url.QueryEscape(r.Owner+":"+Branch))
	if err := a.Client.Get(ctx, q, &open); err == nil && len(open) > 0 {
		return open[0].URL, nil
	}
	var pr struct {
		URL string `json:"html_url"`
	}
	body := map[string]any{"title": strings.SplitN(msg, "\n", 2)[0], "head": Branch, "base": r.DefaultBranch, "body": PRBody(p)}
	if err := a.Client.Send(ctx, http.MethodPost, base+"/pulls", body, &pr); err != nil {
		return "", fmt.Errorf("opening pull request: %w", err)
	}
	return pr.URL, nil
}

func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func CommitMessage(p *Patch) string {
	var ids []string
	for _, f := range p.Files {
		ids = append(ids, f.Check)
	}
	return "chore: spruce up repository (" + strings.Join(ids, ", ") + ")"
}

func PRBody(p *Patch) string {
	var b strings.Builder
	b.WriteString("This pull request was opened by [gh-spruce](https://github.com/Emil-02/gh-spruce) to improve repository health.\n\n")
	for _, f := range p.Files {
		fmt.Fprintf(&b, "- `%s` — %s\n", f.Path, describe(f.Check))
	}
	for _, f := range p.Files {
		if f.Check == "ci" {
			b.WriteString("\n> [!NOTE]\n> The CI workflow was generated from the detected stack. If the build needs environment variables or secrets, add them under **Settings → Secrets and variables → Actions** before merging.\n")
			break
		}
	}
	b.WriteString("\nReview the changes, tweak anything you like, and merge. ✨\n")
	return b.String()
}

func describe(check string) string {
	switch check {
	case "license":
		return "adds an open-source license so others can legally use the code"
	case "readme":
		return "adds a README skeleton (fill in the placeholders)"
	case "ci":
		return "adds a GitHub Actions workflow that installs, lints, builds and tests"
	case "gitignore":
		return "ignores dependencies, build output and local secrets"
	case "security-md":
		return "explains how to report vulnerabilities privately"
	}
	return check
}
