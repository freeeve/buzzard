package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freeeve/buzzard/internal/rules"
	"github.com/freeeve/buzzard/internal/scan"
)

// sampleTree mirrors the candidate set from cands(): /src with two child
// directories, one holding a claimed node_modules.
func sampleTree() *scan.Node {
	nm := &scan.Node{
		Name: "node_modules", Bytes: 4 << 20, Reclaimable: 4 << 20,
		Match: &rules.Match{Category: "node_modules", Tier: rules.TierA, Regen: "npm ci"},
	}
	a := &scan.Node{Name: "a", Bytes: 5 << 20, Reclaimable: 4 << 20, Children: []*scan.Node{nm}}
	b := &scan.Node{Name: "b", Bytes: 9 << 20}
	return &scan.Node{Name: "/src", Own: 1 << 20, Bytes: 15 << 20, Reclaimable: 4 << 20,
		Children: []*scan.Node{a, b}}
}

func treeModel() model {
	cs := cands()
	byPath := make(map[string]scan.Candidate, len(cs))
	for _, c := range cs {
		byPath[c.Path] = c
	}
	t := sampleTree()
	return model{
		cands: cs, byPath: byPath, tree: t, stack: []*scan.Node{t}, tcurs: []int{0},
		marked: map[string]bool{}, height: 24,
	}
}

func TestRowsForOrdersLargestFirstWithLooseFiles(t *testing.T) {
	rows := rowsFor(sampleTree(), "/src")
	want := []string{"b/", "a/", "(files here)"}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i].label != w {
			t.Errorf("row %d = %q, want %q", i, rows[i].label, w)
		}
	}
	if rows[1].path != "/src/a" {
		t.Errorf("path = %q, want /src/a", rows[1].path)
	}
}

// TestRowsForBreaksTiesByName keeps unstable filesystem order out of the
// browser, the same guarantee the printed breakdown makes (task 022).
func TestRowsForBreaksTiesByName(t *testing.T) {
	mk := func(first, second string) *scan.Node {
		return &scan.Node{Name: "/r", Children: []*scan.Node{
			{Name: first, Bytes: 100}, {Name: second, Bytes: 100},
		}}
	}
	if a, b := rowsFor(mk("x", "m"), "/r"), rowsFor(mk("m", "x"), "/r"); a[0].label != "m/" || b[0].label != "m/" {
		t.Errorf("tie order unstable: %q vs %q", a[0].label, b[0].label)
	}
}

// TestCandidateRowIsNotOpenable documents that the scan stops at a claimed
// directory, so there is nothing under one to descend into.
func TestCandidateRowIsNotOpenable(t *testing.T) {
	rows := rowsFor(sampleTree().Children[0], "/src/a")
	if len(rows) != 1 || rows[0].label != "node_modules/" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if rows[0].openable() {
		t.Error("candidate row reported as openable")
	}
}

func TestTreeDescendAndAscend(t *testing.T) {
	m := treeModel()
	if got := m.curPath(); got != "/src" {
		t.Fatalf("start path = %q, want /src", got)
	}
	m = m.treeKey("down")  // b/ is largest, so cursor 0 is b; move to a/
	m = m.treeKey("enter") // descend into a/
	if got := m.curPath(); got != "/src/a" {
		t.Errorf("after descend = %q, want /src/a", got)
	}
	m = m.treeKey("backspace")
	if got := m.curPath(); got != "/src" {
		t.Errorf("after ascend = %q, want /src", got)
	}
	m = m.treeKey("backspace")
	if got := m.curPath(); got != "/src" {
		t.Errorf("ascending past the root = %q, want /src", got)
	}
}

func TestTreeMarksCandidateAndFeedsPicks(t *testing.T) {
	m := treeModel()
	m = m.treeKey("down")  // to a/
	m = m.treeKey("enter") // into a/
	m = m.treeKey(" ")     // mark node_modules
	picks := m.picks()
	if len(picks) != 1 || picks[0].Path != "/src/a/node_modules" {
		t.Fatalf("picks = %+v, want the marked node_modules", picks)
	}
	if m.markedBytes() != 4<<20 {
		t.Errorf("marked bytes = %d, want %d", m.markedBytes(), 4<<20)
	}
}

// TestTreeWillNotMarkUnknownCandidate guards the -min-size interaction: a
// candidate filtered out of the list must not be markable from the tree,
// or the UI would promise a clean that never runs.
func TestTreeWillNotMarkUnknownCandidate(t *testing.T) {
	m := treeModel()
	m.byPath = map[string]scan.Candidate{}
	m = m.treeKey("down")
	m = m.treeKey("enter")
	m = m.treeKey(" ")
	if len(m.picks()) != 0 {
		t.Errorf("marked a candidate absent from the list: %+v", m.picks())
	}
}

func TestTreeViewShowsSizesAndReclaim(t *testing.T) {
	view := treeModel().treeView()
	for _, want := range []string{"/src", "b/", "a/", "4.0 MiB reclaimable", "(files here)"} {
		if !strings.Contains(view, want) {
			t.Errorf("tree view missing %q:\n%s", want, view)
		}
	}
}

// TestTabTogglesViews checks the two views share one marked set.
func TestTabTogglesViews(t *testing.T) {
	m := tea.Model(treeModel())
	if !strings.Contains(m.View(), "[tab] candidates") {
		t.Fatal("did not open on the tree view")
	}
	m = press(t, m, "tab")
	if !strings.Contains(m.View(), "candidates,") {
		t.Errorf("tab did not reach the candidate list:\n%s", m.View())
	}
	m = press(t, m, "a") // mark all tier A from the list view
	m = press(t, m, "tab")
	mm := m.(model)
	if !mm.marked["/src/a/node_modules"] {
		t.Error("marks made in the list view are not visible in the tree")
	}
	if !strings.Contains(mm.View(), "[tab] candidates") {
		t.Error("second tab did not return to the tree")
	}
}

// TestTreeFallsBackWhenNoTree covers a scan whose tree is absent: the
// browser must still work as the candidate list it used to be.
func TestTreeFallsBackWhenNoTree(t *testing.T) {
	m := model{cands: cands(), marked: map[string]bool{}, height: 24}
	if m.browsingTree() {
		t.Fatal("claimed to browse a nil tree")
	}
	if !strings.Contains(m.View(), "candidates,") {
		t.Errorf("did not fall back to the candidate list:\n%s", m.View())
	}
}
