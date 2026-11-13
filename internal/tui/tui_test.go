package tui

import (
	"strings"
	"testing"

	"github.com/Emil-02/gh-spruce/internal/checks"
	"github.com/Emil-02/gh-spruce/internal/scan"
	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestModel(t *testing.T) {
	rep := &checks.Report{Profile: scan.Profile{Login: "octo"}, Score: 50, Grade: "D", Repos: []checks.RepoReport{
		{Repo: &scan.Repo{Name: "a", FullName: "octo/a"}, Score: 10, Results: []checks.Result{{ID: "license", Title: "Open-source license", Status: checks.Fail, Fixable: true}}},
		{Repo: &scan.Repo{Name: "b", FullName: "octo/b"}, Score: 90},
	}}
	m := New(rep)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	if v := m.View(); !strings.Contains(v, "octo/a") || !strings.Contains(v, "Open-source license") {
		t.Errorf("view:\n%s", v)
	}
	m.Update(key("a"))
	if got := m.Selected(); len(got) != 1 || got[0].Repo.Name != "a" {
		t.Errorf("select fixable = %v", got)
	}
	m.Update(key("down"))
	m.Update(key(" "))
	if len(m.Selected()) != 2 {
		t.Errorf("space did not select")
	}
	_, cmd := m.Update(key("f"))
	if !m.Fix || cmd == nil {
		t.Error("f should request fixing and quit")
	}
}
