package checks

import (
	"math"
	"sort"
	"time"

	"github.com/Emil-02/gh-spruce/internal/config"
	"github.com/Emil-02/gh-spruce/internal/scan"
	"github.com/Emil-02/gh-spruce/internal/stack"
)

type RepoReport struct {
	Repo    *scan.Repo `json:"repo"`
	Stack   stack.Info `json:"stack"`
	Score   int        `json:"score"`
	Grade   string     `json:"grade"`
	Results []Result   `json:"results"`
}

// Failing returns non-passing, applicable results.
func (r RepoReport) Failing() []Result {
	var out []Result
	for _, x := range r.Results {
		if x.Status == Fail || x.Status == Warn {
			out = append(out, x)
		}
	}
	return out
}

func (r RepoReport) Result(id string) (Result, bool) {
	for _, x := range r.Results {
		if x.ID == id {
			return x, true
		}
	}
	return Result{}, false
}

type Summary struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Failing int    `json:"failing"`
	Total   int    `json:"applicable"`
}

type Report struct {
	Profile        scan.Profile `json:"profile"`
	Score          int          `json:"score"`
	Grade          string       `json:"grade"`
	Repos          []RepoReport `json:"repos"`
	ProfileResults []Result     `json:"profile_checks"`
	Summary        []Summary    `json:"summary"`
	GeneratedAt    time.Time    `json:"generated_at"`
}

func Grade(score int) string {
	switch {
	case score >= 95:
		return "A+"
	case score >= 85:
		return "A"
	case score >= 70:
		return "B"
	case score >= 55:
		return "C"
	case score >= 40:
		return "D"
	}
	return "F"
}

// Evaluate runs every enabled check against a repository and scores it.
// Passing checks earn their full weight, warnings earn half. An empty repo
// scores 0; a critical failure caps the score at 40.
func Evaluate(r *scan.Repo, cfg *config.Config, now time.Time) RepoReport {
	ctx := &Context{Repo: r, Stack: stack.Detect(r), Config: cfg, Now: now}
	rep := RepoReport{Repo: r, Stack: ctx.Stack}
	empty := IsEmpty(r)
	var earned, total float64
	critical := false
	for _, c := range All() {
		if cfg.Disabled(c.ID) {
			continue
		}
		w := c.Weight
		if v, ok := cfg.Weights[c.ID]; ok {
			w = v
		}
		res := Result{ID: c.ID, Title: c.Title, Severity: c.Severity, Weight: w, Fixable: c.Fixable}
		switch {
		case c.PublicOnly && r.Private:
			res.Status = Skip
		case empty && c.ID != "empty":
			res.Status = Skip
		case IsProfileRepo(r) && c.ID != "readme" && c.ID != "secrets" && c.ID != "empty":
			res.Status = Skip
		default:
			res.Status, res.Message, res.Hint = c.Run(ctx)
		}
		if res.Status == Pass || res.Status == Skip {
			res.Fixable = false
		}
		rep.Results = append(rep.Results, res)
		if res.Status == Skip {
			continue
		}
		total += float64(w)
		switch res.Status {
		case Pass:
			earned += float64(w)
		case Warn:
			earned += float64(w) / 2
		case Fail:
			critical = critical || c.Severity == Critical
		}
	}
	switch {
	case empty:
		rep.Score = 0
	case total == 0:
		rep.Score = 100
	default:
		rep.Score = int(math.Round(earned / total * 100))
	}
	if critical && rep.Score > 40 {
		rep.Score = 40
	}
	rep.Grade = Grade(rep.Score)
	return rep
}

// Build evaluates a whole scan. The profile score is the average score of
// public repositories (what visitors see; all repositories when none are
// public), with 10% reserved for profile-level checks.
func Build(res *scan.Result, cfg *config.Config, now time.Time) *Report {
	rep := &Report{Profile: res.Profile, GeneratedAt: now.UTC()}
	var sumPublic, nPublic, sumAll int
	counts := map[string]*Summary{}
	for _, r := range res.Repos {
		rr := Evaluate(r, cfg, now)
		rep.Repos = append(rep.Repos, rr)
		sumAll += rr.Score
		if !r.Private {
			sumPublic += rr.Score
			nPublic++
		}
		for _, x := range rr.Results {
			if x.Status == Skip {
				continue
			}
			s := counts[x.ID]
			if s == nil {
				s = &Summary{ID: x.ID, Title: x.Title}
				counts[x.ID] = s
			}
			s.Total++
			if x.Status == Fail || x.Status == Warn {
				s.Failing++
			}
		}
	}
	for _, c := range All() {
		if s, ok := counts[c.ID]; ok {
			rep.Summary = append(rep.Summary, *s)
		}
	}

	rep.ProfileResults = profileChecks(res, rep)
	var repoAvg float64
	switch {
	case nPublic > 0:
		repoAvg = float64(sumPublic) / float64(nPublic)
	case len(rep.Repos) > 0:
		repoAvg = float64(sumAll) / float64(len(rep.Repos))
	}
	passed := 0
	for _, p := range rep.ProfileResults {
		if p.Status == Pass {
			passed++
		}
	}
	prof := 1.0
	if n := len(rep.ProfileResults); n > 0 {
		prof = float64(passed) / float64(n)
	}
	rep.Score = int(math.Round(repoAvg*0.9 + prof*10))
	if len(rep.Repos) == 0 {
		rep.Score = 0
	}
	rep.Grade = Grade(rep.Score)
	sort.SliceStable(rep.Repos, func(i, j int) bool { return rep.Repos[i].Score < rep.Repos[j].Score })
	return rep
}

func profileChecks(res *scan.Result, rep *Report) []Result {
	var out []Result
	if res.Profile.Type == "User" {
		r := Result{ID: "profile-readme", Title: "Profile README", Severity: Medium, Weight: 5}
		if res.Profile.HasReadme {
			r.Status = Pass
		} else {
			r.Status, r.Message, r.Hint = Fail, "no "+res.Profile.Login+"/"+res.Profile.Login+" repository", "create it to show an introduction on your profile"
		}
		out = append(out, r)
	}
	if !res.Profile.PinnedKnown {
		return out
	}
	r := Result{ID: "pinned", Title: "Pinned repositories", Severity: Medium, Weight: 5}
	var private []string
	for _, p := range res.Profile.Pinned {
		if p.Private {
			private = append(private, p.Name)
		}
	}
	switch {
	case len(private) > 0:
		r.Status, r.Message, r.Hint = Fail, "private pinned repo(s) are hidden from visitors: "+short(private, 3), "make them public or pin other repos"
	case len(res.Profile.Pinned) < 4:
		r.Status, r.Message = Warn, "fewer than 4 pinned repositories"
		r.Hint = "pin your best work: " + short(bestPublic(rep, 6), 6)
	default:
		r.Status = Pass
	}
	out = append(out, r)
	return out
}

func bestPublic(rep *Report, n int) []string {
	var cands []RepoReport
	for _, r := range rep.Repos {
		if !r.Repo.Private && !IsEmpty(r.Repo) && !r.Repo.Fork && r.Repo.Name != rep.Profile.Login {
			cands = append(cands, r)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Repo.Stars != cands[j].Repo.Stars {
			return cands[i].Repo.Stars > cands[j].Repo.Stars
		}
		return cands[i].Score > cands[j].Score
	})
	var out []string
	for i := 0; i < len(cands) && i < n; i++ {
		out = append(out, cands[i].Repo.Name)
	}
	return out
}
