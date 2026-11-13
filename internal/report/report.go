// Package report renders audit results as a terminal table, JSON or Markdown.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/charmbracelet/lipgloss"
)

var (
	bold   = lipgloss.NewStyle().Bold(true)
	faint  = lipgloss.NewStyle().Faint(true)
	green  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellow = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	red    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

func ScoreStyle(score int) lipgloss.Style {
	switch {
	case score >= 85:
		return green
	case score >= 55:
		return yellow
	}
	return red
}

func Mark(s checks.Status) string {
	switch s {
	case checks.Pass:
		return green.Render("✓")
	case checks.Warn:
		return yellow.Render("~")
	case checks.Fail:
		return red.Render("✗")
	}
	return faint.Render("·")
}

func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func JSON(w io.Writer, r *checks.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// Table writes a compact, colored overview. verbose adds per-issue detail.
func Table(w io.Writer, r *checks.Report, verbose bool) {
	vis := map[bool]string{true: "private", false: "public"}
	fmt.Fprintf(w, "\n%s %s  %s\n\n", bold.Render("gh-spruce"), faint.Render("· "+r.Profile.Login+fmt.Sprintf(" · %d repos", len(r.Repos))),
		bold.Inherit(ScoreStyle(r.Score)).Render(fmt.Sprintf("Profile score %d/100 (%s)", r.Score, r.Grade)))

	nameW := 4
	for _, rr := range r.Repos {
		if l := len(rr.Repo.Name); l > nameW {
			nameW = l
		}
	}
	if nameW > 32 {
		nameW = 32
	}
	fmt.Fprintf(w, "  %s  %s  %s  %s\n", faint.Render(pad("SCORE", 6)), faint.Render(pad("REPO", nameW)), faint.Render(pad("VIS", 7)), faint.Render("ISSUES"))
	for _, rr := range r.Repos {
		name := rr.Repo.Name
		if len(name) > nameW {
			name = name[:nameW-1] + "…"
		}
		var issues []string
		for _, x := range rr.Failing() {
			issues = append(issues, Mark(x.Status)+" "+x.ID)
		}
		if len(issues) == 0 {
			issues = []string{green.Render("all good")}
		}
		fmt.Fprintf(w, "  %s  %s  %s  %s\n",
			ScoreStyle(rr.Score).Render(fmt.Sprintf("%3d %-2s", rr.Score, rr.Grade)),
			pad(name, nameW), faint.Render(pad(vis[rr.Repo.Private], 7)), strings.Join(issues, "  "))
		if verbose {
			for _, x := range rr.Failing() {
				line := fmt.Sprintf("         %s %s", Mark(x.Status), x.Title)
				if x.Message != "" {
					line += faint.Render(" — " + x.Message)
				}
				fmt.Fprintln(w, line)
				if x.Hint != "" {
					fmt.Fprintln(w, faint.Render("             ↳ "+x.Hint))
				}
			}
		}
	}

	fmt.Fprintf(w, "\n%s\n", bold.Render("Profile"))
	for _, p := range r.ProfileResults {
		line := fmt.Sprintf("  %s %s", Mark(p.Status), p.Title)
		if p.Message != "" {
			line += faint.Render(" — " + p.Message)
		}
		fmt.Fprintln(w, line)
		if p.Hint != "" && p.Status != checks.Pass {
			fmt.Fprintln(w, faint.Render("      ↳ "+p.Hint))
		}
	}

	sum := append([]checks.Summary{}, r.Summary...)
	// Order by impact on the score: weight × number of failing repositories.
	impact := func(s checks.Summary) int {
		c, _ := checks.ByID(s.ID)
		w := c.Weight
		if c.ID == "empty" {
			w = 100
		}
		return w*s.Failing + s.Failing
	}
	sort.SliceStable(sum, func(i, j int) bool { return impact(sum[i]) > impact(sum[j]) })
	var fixable []string
	header := false
	for _, s := range sum {
		if s.Failing == 0 {
			continue
		}
		if !header {
			fmt.Fprintf(w, "\n%s\n", bold.Render("Most common issues"))
			header = true
		}
		c, _ := checks.ByID(s.ID)
		tag := ""
		if c.Fixable && !c.OptIn {
			tag = green.Render(" fixable")
			fixable = append(fixable, s.ID)
		}
		fmt.Fprintf(w, "  %3d/%-3d %s%s\n", s.Failing, s.Total, c.Title, tag)
	}
	if len(fixable) > 0 {
		fmt.Fprintf(w, "\n%s %s\n", faint.Render("Preview fixes with:"), "gh spruce fix --dry-run --only "+strings.Join(fixable, ","))
	}
	if !verbose {
		fmt.Fprintln(w, faint.Render("Details: gh spruce audit -v"))
	}
	fmt.Fprintln(w)
}

func mdEscape(s string) string {
	return strings.NewReplacer("|", `\|`, "\n", " ").Replace(s)
}

func mdMark(s checks.Status) string {
	switch s {
	case checks.Pass:
		return "✅"
	case checks.Warn:
		return "⚠️"
	case checks.Fail:
		return "❌"
	}
	return "➖"
}

func Markdown(w io.Writer, r *checks.Report) {
	fmt.Fprintf(w, "# Repository health: %s\n\n**Profile score: %d/100 (%s)** · %d repositories · generated %s by [gh-spruce](https://github.com/Emil-02/gh-spruce)\n\n",
		r.Profile.Login, r.Score, r.Grade, len(r.Repos), r.GeneratedAt.Format("2006-01-02"))
	fmt.Fprintln(w, "| Repository | Visibility | Score | Issues |")
	fmt.Fprintln(w, "|---|---|---:|---|")
	for _, rr := range r.Repos {
		vis := "public"
		if rr.Repo.Private {
			vis = "private"
		}
		var issues []string
		for _, x := range rr.Failing() {
			issues = append(issues, mdMark(x.Status)+" "+x.ID)
		}
		fmt.Fprintf(w, "| [%s](%s) | %s | %d (%s) | %s |\n", mdEscape(rr.Repo.Name), rr.Repo.URL, vis, rr.Score, rr.Grade, strings.Join(issues, " "))
	}
	fmt.Fprintln(w, "\n## Profile")
	for _, p := range r.ProfileResults {
		fmt.Fprintf(w, "- %s **%s**", mdMark(p.Status), p.Title)
		if p.Message != "" {
			fmt.Fprintf(w, " — %s", mdEscape(p.Message))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "\n## Details")
	for _, rr := range r.Repos {
		f := rr.Failing()
		if len(f) == 0 {
			continue
		}
		fmt.Fprintf(w, "\n### %s (%d)\n\n", mdEscape(rr.Repo.Name), rr.Score)
		for _, x := range f {
			fmt.Fprintf(w, "- %s **%s**", mdMark(x.Status), x.Title)
			if x.Message != "" {
				fmt.Fprintf(w, " — %s", mdEscape(x.Message))
			}
			if x.Hint != "" {
				fmt.Fprintf(w, "  \n  _%s_", mdEscape(x.Hint))
			}
			fmt.Fprintln(w)
		}
	}
}
