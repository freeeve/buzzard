// Package tui is the interactive face of buzzard: browse candidates, mark
// what should go, and clean behind an explicit confirmation. The TUI never
// deletes on its own -- it hands the marked set to the same evidence-checked,
// veto-guarded, manifest-recorded clean flow the CLI uses.
package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freeeve/buzzard/internal/format"
	"github.com/freeeve/buzzard/internal/rules"
	"github.com/freeeve/buzzard/internal/scan"
)

// Stats summarizes a clean executed from the TUI.
type Stats struct {
	Trashed int
	Freed   int64
	Failed  int
	Skipped int
	Log     []string
}

// CleanFunc performs the actual cleaning of the picked candidates.
type CleanFunc func(picks []scan.Candidate) Stats

const (
	modeBrowse = iota
	modeConfirm
	modeDone
)

// The browser opens on the tree and filters to the candidate list on
// demand, so the first thing shown is where the space went.
const (
	viewTree = iota
	viewCands
)

type model struct {
	cands  []scan.Candidate
	byPath map[string]scan.Candidate
	tree   *scan.Node
	stack  []*scan.Node
	tcurs  []int
	clean  CleanFunc
	cursor int
	marked map[string]bool
	view   int
	mode   int
	stats  Stats
	height int
	watch  *exitWatch
}

// browsingTree reports whether the tree view is active. A scan with no
// retained tree falls back to the candidate list.
func (m model) browsingTree() bool {
	return m.view == viewTree && m.tree != nil
}

// exitWatch caps quit latency: terminal input-reader shutdown has been
// observed to hang until the next keypress on some terminals, freezing the
// UI after quit. Once a quit is requested the watchdog gives the program a
// grace period to exit naturally, then forces the exit -- the terminal is
// already restored by the time the shutdown wait begins, so forcing is
// safe.
type exitWatch struct {
	once   sync.Once
	quitAt time.Time
}

// arm starts the countdown; the first quit request wins.
func (w *exitWatch) arm() {
	w.once.Do(func() {
		w.quitAt = time.Now()
		go func() {
			time.Sleep(time.Second)
			fmt.Fprintln(os.Stderr, "buzzard: input shutdown stalled; forcing exit")
			os.Exit(0)
		}()
	})
}

// quit arms the watchdog and tells the program to stop.
func (m model) quit() (tea.Model, tea.Cmd) {
	if m.watch != nil {
		m.watch.arm()
	}
	return m, tea.Quit
}

// Run starts the interactive browser over a completed scan, opening on the
// directory tree. clean is invoked with the marked set when the user
// confirms. Candidates are expected pre-sorted for the list view.
func Run(res *scan.Result, clean CleanFunc) error {
	w := &exitWatch{}
	byPath := make(map[string]scan.Candidate, len(res.Candidates))
	for _, c := range res.Candidates {
		byPath[c.Path] = c
	}
	m := model{
		cands: res.Candidates, byPath: byPath, tree: res.Tree,
		clean: clean, marked: make(map[string]bool), height: 24, watch: w,
	}
	if res.Tree != nil {
		m.stack = []*scan.Node{res.Tree}
		m.tcurs = []int{0}
	} else {
		m.view = viewCands
	}
	_, err := tea.NewProgram(m).Run()
	if os.Getenv("BUZZARD_DEBUG") != "" && !w.quitAt.IsZero() {
		fmt.Fprintf(os.Stderr, "buzzard: tui shutdown took %s\n", time.Since(w.quitAt).Round(time.Millisecond))
	}
	return err
}

// Init implements tea.Model.
func (m model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
	case tea.KeyMsg:
		return m.key(msg.String())
	}
	return m, nil
}

// key routes one keypress according to the current mode.
func (m model) key(k string) (tea.Model, tea.Cmd) {
	if k == "ctrl+c" {
		return m.quit()
	}
	switch m.mode {
	case modeConfirm:
		switch k {
		case "y":
			m.stats = m.clean(m.picks())
			m.mode = modeDone
		case "n", "esc", "q":
			m.mode = modeBrowse
		}
	case modeDone:
		if k == "q" || k == "enter" {
			return m.quit()
		}
	default:
		switch k {
		case "q":
			return m.quit()
		case "tab":
			if m.tree != nil {
				m.view = viewTree + viewCands - m.view
			}
			return m, nil
		case "a":
			for _, c := range m.cands {
				if c.Match.Tier == rules.TierA {
					m.marked[c.Path] = true
				}
			}
			return m, nil
		case "n":
			m.marked = make(map[string]bool)
			return m, nil
		case "c":
			if len(m.picks()) > 0 {
				m.mode = modeConfirm
			}
			return m, nil
		}
		if m.browsingTree() {
			return m.treeKey(k), nil
		}
		switch k {
		case "j", "down":
			if m.cursor < len(m.cands)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case " ", "space":
			if len(m.cands) > 0 {
				p := m.cands[m.cursor].Path
				m.marked[p] = !m.marked[p]
			}
		}
	}
	return m, nil
}

// picks returns the marked candidates in candidate-list order, so the
// clean flow sees the same set whichever view marked them.
func (m model) picks() []scan.Candidate {
	var out []scan.Candidate
	for _, c := range m.cands {
		if m.marked[c.Path] {
			out = append(out, c)
		}
	}
	return out
}

// markedBytes sums the sizes of the marked candidates.
func (m model) markedBytes() int64 {
	var n int64
	for _, c := range m.cands {
		if m.marked[c.Path] {
			n += c.Bytes
		}
	}
	return n
}

// View implements tea.Model.
func (m model) View() string {
	switch m.mode {
	case modeConfirm:
		return m.confirmView()
	case modeDone:
		return m.doneView()
	}
	if m.browsingTree() {
		return m.treeView()
	}
	return m.browseView()
}

// browseView renders the scrolling candidate list.
func (m model) browseView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "buzzard -- %d candidates, %d marked (%s)\n",
		len(m.cands), len(m.picks()), format.Human(m.markedBytes()))
	b.WriteString("[space] mark  [a] all tier A  [n] none  [c] clean  [tab] tree  [q] quit\n\n")
	if len(m.cands) == 0 {
		b.WriteString("  nothing reclaimable found.\n")
		return b.String()
	}
	visible := m.height - 5
	if visible < 3 {
		visible = 3
	}
	start := 0
	if m.cursor >= visible {
		start = m.cursor - visible + 1
	}
	for i := start; i < len(m.cands) && i < start+visible; i++ {
		c := m.cands[i]
		cursor, mark := " ", " "
		if i == m.cursor {
			cursor = ">"
		}
		if m.marked[c.Path] {
			mark = "x"
		}
		fmt.Fprintf(&b, "%s [%s] %s %9s  %-24s idle %-6s %s\n",
			cursor, mark, c.Match.Tier, format.Human(c.Bytes),
			c.Match.Category, format.Idle(c.NewestMod), c.Path)
	}
	return b.String()
}

// confirmView renders the pre-clean confirmation.
func (m model) confirmView() string {
	picks := m.picks()
	var b strings.Builder
	fmt.Fprintf(&b, "trash %d item(s), reclaiming %s?\n\n", len(picks), format.Human(m.markedBytes()))
	for _, c := range picks {
		fmt.Fprintf(&b, "  %9s  %s\n", format.Human(c.Bytes), c.Path)
	}
	b.WriteString("\n[y] trash them  [n] back\n")
	return b.String()
}

// doneView renders the post-clean summary.
func (m model) doneView() string {
	var b strings.Builder
	for _, line := range m.stats.Log {
		b.WriteString(line + "\n")
	}
	fmt.Fprintf(&b, "\nmoved %d item(s) to the trash, %s reclaimable on empty", m.stats.Trashed, format.Human(m.stats.Freed))
	if m.stats.Skipped > 0 {
		fmt.Fprintf(&b, "; %d skipped", m.stats.Skipped)
	}
	if m.stats.Failed > 0 {
		fmt.Fprintf(&b, "; %d FAILED", m.stats.Failed)
	}
	b.WriteString("\nundo with: buzzard -restore\n\n[q] quit\n")
	return b.String()
}
