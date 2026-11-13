// Package scan collects a read-only snapshot of every repository that the
// checks need, using as few API calls as possible.
package scan

import (
	"path"
	"strings"
	"time"
)

type Entry struct {
	Path string `json:"path"`
	Type string `json:"type"` // blob | tree | commit
	Size int64  `json:"size,omitempty"`
}

type WorkflowRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"html_url"`
}

type Repo struct {
	Owner         string    `json:"owner"`
	Name          string    `json:"name"`
	FullName      string    `json:"full_name"`
	URL           string    `json:"url"`
	Description   string    `json:"description"`
	Homepage      string    `json:"homepage"`
	DefaultBranch string    `json:"default_branch"`
	Private       bool      `json:"private"`
	Fork          bool      `json:"fork"`
	Archived      bool      `json:"archived"`
	Template      bool      `json:"is_template"`
	HasPages      bool      `json:"has_pages"`
	CanPush       bool      `json:"can_push"`
	CanAdmin      bool      `json:"can_admin"`
	Topics        []string  `json:"topics"`
	License       string    `json:"license"` // SPDX id, "" when none
	Language      string    `json:"language"`
	Stars         int       `json:"stars"`
	SizeKB        int64     `json:"size_kb"`
	PushedAt      time.Time `json:"pushed_at"`

	Empty bool `json:"empty"`
	// Partial means only the top two directory levels were scanned.
	Partial       bool         `json:"partial,omitempty"`
	HeadSHA       string       `json:"head_sha,omitempty"`
	Tree          []Entry      `json:"-"`
	TreeTruncated bool         `json:"tree_truncated,omitempty"`
	ReadmePath    string       `json:"readme_path,omitempty"`
	Readme        string       `json:"-"`
	LastRun       *WorkflowRun `json:"last_run,omitempty"`
}

// Has reports whether a file exists at p (case-insensitive).
func (r *Repo) Has(p string) bool {
	for _, e := range r.Tree {
		if strings.EqualFold(e.Path, p) {
			return true
		}
	}
	return false
}

// Find returns tree blobs whose path matches the glob (matched against the
// full path and the base name).
func (r *Repo) Find(glob string) []Entry {
	var out []Entry
	for _, e := range r.Tree {
		if e.Type != "blob" {
			continue
		}
		if ok, _ := path.Match(glob, e.Path); ok {
			out = append(out, e)
			continue
		}
		if ok, _ := path.Match(glob, path.Base(e.Path)); ok {
			out = append(out, e)
		}
	}
	return out
}

func (r *Repo) Workflows() []Entry {
	var out []Entry
	for _, e := range r.Tree {
		if e.Type == "blob" && strings.HasPrefix(e.Path, ".github/workflows/") &&
			(strings.HasSuffix(e.Path, ".yml") || strings.HasSuffix(e.Path, ".yaml")) {
			out = append(out, e)
		}
	}
	return out
}

type Pinned struct {
	Name    string `json:"name"`
	Owner   string `json:"owner"`
	Private bool   `json:"private"`
}

type Profile struct {
	Login         string   `json:"login"`
	Name          string   `json:"name"`
	Type          string   `json:"type"` // User | Organization
	Authenticated bool     `json:"authenticated"`
	HasReadme     bool     `json:"has_profile_readme"`
	Pinned        []Pinned `json:"pinned"`
	// PinnedKnown is false when pinned items could not be read (e.g. limited tokens).
	PinnedKnown bool `json:"pinned_known"`
}

type Result struct {
	Profile Profile `json:"profile"`
	Repos   []*Repo `json:"repos"`
}
