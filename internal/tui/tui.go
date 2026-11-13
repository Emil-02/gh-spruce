// Package tui is the interactive repository browser (gh spruce tui).
package tui

import (
	"fmt"
	"strings"

	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/Emil-02/gh-spruce/internal/report"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Model struct {
	rep      *checks.Report
	repos    []checks.RepoReport
	cursor   int
	offset   int
	selected map[string]bool
	width    int
	height   int
	byName   bool

	// Fix is true when the user pressed "f"; Selected returns the repos.
	Fix  bool
	Open string
}

func New(rep *checks.Report) *Model {
	m := &Model{rep: rep, selected: map[string]bool{}, width: 100, height: 30}
	m.repos = append(m.repos, rep.Repos...)
	return m
}

func (m *Model) Selected() []checks.RepoReport {
	var out []checks.RepoReport
	for _, r := range m.repos {
		if m.selected[r.Repo.FullName] {
			out = append(out, r)
		}
	}
	return out
}

func fixable(r checks.RepoReport) bool {
	for _, x := range r.Results {
		c, _ := checks.ByID(x.ID)
		if x.Fixable && !c.OptIn {
			return true
		}
	}
	return false
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) listHeight() int {
	h := m.height - 6
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.repos)-1 {
				m.cursor++
			}
		case "pgdown":
			m.cursor = min(len(m.repos)-1, m.cursor+m.listHeight())
		case "pgup":
			m.cursor = max(0, m.cursor-m.listHeight())
		case "home", "g":
			m.cursor = 0
		case "end", "G":
			m.cursor = len(m.repos) - 1
		case " ", "x":
			if len(m.repos) > 0 {
				n := m.repos[m.cursor].Repo.FullName
				m.selected[n] = !m.selected[n]
			}
		case "a":
			all := true
			for _, r := range m.repos {
				if fixable(r) && !m.selected[r.Repo.FullName] {
					all = false
				}
			}
			for _, r := range m.repos {
				if fixable(r) {
					m.selected[r.Repo.FullName] = !all
				}
			}
		case "s":
			m.byName = !m.byName
			m.sort()
		case "o":
			if len(m.repos) > 0 {
				m.Open = m.repos[m.cursor].Repo.URL
				return m, tea.Quit
			}
		case "f", "enter":
			if len(m.Selected()) == 0 && len(m.repos) > 0 {
				m.selected[m.repos[m.cursor].Repo.FullName] = true
			}
			m.Fix = true
			return m, tea.Quit
		}
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if lh := m.listHeight(); m.cursor >= m.offset+lh {
		m.offset = m.cursor - lh + 1
	}
	return m, nil
}

func (m *Model) sort() {
	cur := ""
	if len(m.repos) > 0 {
		cur = m.repos[m.cursor].Repo.FullName
	}
	m.repos = append(m.repos[:0], m.rep.Repos...)
	if m.byName {
		for i := 1; i < len(m.repos); i++ {
			for j := i; j > 0 && strings.ToLower(m.repos[j].Repo.Name) < strings.ToLower(m.repos[j-1].Repo.Name); j-- {
				m.repos[j], m.repos[j-1] = m.repos[j-1], m.repos[j]
			}
		}
	}
	for i, r := range m.repos {
		if r.Repo.FullName == cur {
			m.cursor = i
		}
	}
}

var (
	title  = lipgloss.NewStyle().Bold(true)
	faint  = lipgloss.NewStyle().Faint(true)
	cursor = lipgloss.NewStyle().Reverse(true)
	pane   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8")).Padding(0, 1)
)

func (m *Model) View() string {
	head := fmt.Sprintf("%s  %s  %s", title.Render("gh-spruce"), faint.Render(m.rep.Profile.Login),
		report.ScoreStyle(m.rep.Score).Bold(true).Render(fmt.Sprintf("%d/100 %s", m.rep.Score, m.rep.Grade)))

	leftW := 38
	if m.width < 80 {
		leftW = m.width - 4
	}
	var rows []string
	lh := m.listHeight()
	for i := m.offset; i < len(m.repos) && i < m.offset+lh; i++ {
		r := m.repos[i]
		mark := "  "
		if m.selected[r.Repo.FullName] {
			mark = "● "
		}
		name := r.Repo.Name
		if len(name) > leftW-12 {
			name = name[:leftW-13] + "…"
		}
		line := fmt.Sprintf("%s%s %-*s", mark, report.ScoreStyle(r.Score).Render(fmt.Sprintf("%3d", r.Score)), leftW-8, name)
		if r.Repo.Private {
			line += "🔒"
		}
		if i == m.cursor {
			line = cursor.Render(line)
		}
		rows = append(rows, line)
	}
	left := pane.Width(leftW).Height(lh).Render(strings.Join(rows, "\n"))

	body := left
	if m.width >= 80 && len(m.repos) > 0 {
		rightW := m.width - leftW - 8
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, pane.Width(rightW).Height(lh).Render(m.detail(m.repos[m.cursor], rightW)))
	}
	help := faint.Render("↑/↓ move · space select · a select fixable · f fix selected · o open · s sort · q quit")
	return head + "\n" + body + "\n" + help
}

func (m *Model) detail(r checks.RepoReport, w int) string {
	var b strings.Builder
	vis := "public"
	if r.Repo.Private {
		vis = "private"
	}
	fmt.Fprintf(&b, "%s  %s\n", title.Render(r.Repo.FullName), report.ScoreStyle(r.Score).Render(fmt.Sprintf("%d %s", r.Score, r.Grade)))
	var kinds []string
	for _, k := range r.Stack.Kinds {
		kinds = append(kinds, string(k))
	}
	meta := vis
	if len(kinds) > 0 {
		meta += " · " + strings.Join(kinds, ", ")
	}
	if r.Repo.Stars > 0 {
		meta += fmt.Sprintf(" · ★%d", r.Repo.Stars)
	}
	fmt.Fprintln(&b, faint.Render(meta))
	if r.Repo.Description != "" {
		fmt.Fprintln(&b, lipgloss.NewStyle().Width(w).Render(r.Repo.Description))
	}
	fmt.Fprintln(&b)
	for _, x := range r.Results {
		if x.Status == checks.Skip {
			continue
		}
		line := report.Mark(x.Status) + " " + x.Title
		if x.Fixable {
			line += lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(" [fix]")
		}
		fmt.Fprintln(&b, line)
		if x.Status != checks.Pass && x.Message != "" {
			fmt.Fprintln(&b, faint.Width(w).Render("   "+x.Message))
		}
	}
	return b.String()
}

// Run starts the program in the alternate screen.
func Run(m *Model) error {
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}
