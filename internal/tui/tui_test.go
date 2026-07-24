package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freeeve/buzzard/internal/rules"
	"github.com/freeeve/buzzard/internal/scan"
)

func cands() []scan.Candidate {
	old := time.Now().Add(-400 * 24 * time.Hour)
	return []scan.Candidate{
		{Path: "/src/a/node_modules", Bytes: 4 << 20, NewestMod: old,
			Match: &rules.Match{Category: "node_modules", Tier: rules.TierA, Regen: "npm ci"}},
		{Path: "/src/b/target", Bytes: 2 << 20, NewestMod: old,
			Match: &rules.Match{Category: "cargo target", Tier: rules.TierA, Regen: "cargo build"}},
		{Path: "/src/c/node_modules", Bytes: 1 << 20, NewestMod: old,
			Match: &rules.Match{Category: "node_modules (orphaned)", Tier: rules.TierB, Regen: "n/a"}},
	}
}

func press(t *testing.T, m tea.Model, keys ...string) tea.Model {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case " ":
			msg = tea.KeyMsg{Type: tea.KeySpace}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m, _ = m.Update(msg)
	}
	return m
}

func TestMarkAllTierAAndConfirmClean(t *testing.T) {
	var got []scan.Candidate
	m := tea.Model(model{cands: cands(), marked: map[int]bool{}, height: 24,
		clean: func(picks []scan.Candidate) Stats {
			got = picks
			return Stats{Trashed: len(picks), Freed: 6 << 20, Log: []string{"  trashed x"}}
		}})
	m = press(t, m, "a", "c", "y")
	if len(got) != 2 {
		t.Fatalf("cleaned %d picks, want 2 tier A: %+v", len(got), got)
	}
	view := m.View()
	if !strings.Contains(view, "moved 2 item(s)") || !strings.Contains(view, "buzzard -restore") {
		t.Errorf("done view = %q", view)
	}
}

func TestSpaceTogglesAndConfirmDeclines(t *testing.T) {
	cleaned := false
	m := tea.Model(model{cands: cands(), marked: map[int]bool{}, height: 24,
		clean: func([]scan.Candidate) Stats { cleaned = true; return Stats{} }})
	m = press(t, m, " ", "down", " ", " ", "c", "n")
	mm := m.(model)
	if !mm.marked[0] || mm.marked[1] {
		t.Errorf("marks wrong: %+v", mm.marked)
	}
	if cleaned {
		t.Error("declined confirm still cleaned")
	}
	if mm.mode != modeBrowse {
		t.Errorf("mode = %d, want browse", mm.mode)
	}
}

func TestCleanWithNothingMarkedIsNoop(t *testing.T) {
	m := tea.Model(model{cands: cands(), marked: map[int]bool{}, height: 24,
		clean: func([]scan.Candidate) Stats { t.Fatal("clean called"); return Stats{} }})
	m = press(t, m, "c")
	if m.(model).mode != modeBrowse {
		t.Error("empty clean left browse mode")
	}
}

func TestBrowseViewShowsMarksAndTiers(t *testing.T) {
	m := model{cands: cands(), marked: map[int]bool{0: true}, height: 24, clean: nil}
	view := m.View()
	if !strings.Contains(view, "[x] A") || !strings.Contains(view, "node_modules") {
		t.Errorf("browse view = %q", view)
	}
	if !strings.Contains(view, "1 marked") {
		t.Errorf("marked count missing: %q", view)
	}
}
