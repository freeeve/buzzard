package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/freeeve/buzzard/internal/format"
	"github.com/freeeve/buzzard/internal/scan"
)

// treeRow is one line of the tree browser. node is nil for the synthetic
// entry covering bytes that sit loose in the directory being viewed.
type treeRow struct {
	node        *scan.Node
	label       string
	path        string
	bytes       int64
	reclaimable int64
}

// openable reports whether descending into this row would show anything.
// Rule-claimed directories are leaves: the scan stops at a candidate, so
// there is nothing beneath one to browse.
func (r treeRow) openable() bool {
	return r.node != nil && len(r.node.Children) > 0
}

// rowsFor lists a directory's children largest first, plus an entry for
// the bytes sitting directly in it, so the rows account for the whole
// node rather than only its subdirectories. Ties break on name because
// filesystem listing order is not stable and must not reach the display.
func rowsFor(n *scan.Node, base string) []treeRow {
	if n == nil {
		return nil
	}
	rows := make([]treeRow, 0, len(n.Children)+1)
	for _, c := range n.Children {
		rows = append(rows, treeRow{
			node:        c,
			label:       c.Name + "/",
			path:        filepath.Join(base, c.Name),
			bytes:       c.Bytes,
			reclaimable: c.Reclaimable,
		})
	}
	if n.Own > 0 {
		rows = append(rows, treeRow{label: "(files here)", bytes: n.Own})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].bytes != rows[j].bytes {
			return rows[i].bytes > rows[j].bytes
		}
		return rows[i].label < rows[j].label
	})
	return rows
}

// cur returns the directory currently being browsed.
func (m model) cur() *scan.Node {
	if len(m.stack) == 0 {
		return m.tree
	}
	return m.stack[len(m.stack)-1]
}

// curPath returns the absolute path of the directory being browsed. The
// root node carries the full scanned path; every level below it carries a
// basename.
func (m model) curPath() string {
	if len(m.stack) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.stack))
	parts = append(parts, m.stack[0].Name)
	for _, n := range m.stack[1:] {
		parts = append(parts, n.Name)
	}
	return filepath.Join(parts...)
}

// rows returns the display rows for the current directory.
func (m model) rows() []treeRow {
	return rowsFor(m.cur(), m.curPath())
}

// treeCursor returns the cursor position within the current directory.
func (m model) treeCursor() int {
	if len(m.tcurs) == 0 {
		return 0
	}
	return m.tcurs[len(m.tcurs)-1]
}

// markable reports whether a row names a candidate the clean flow knows
// about. Candidates hidden by -min-size are absent from the candidate set,
// so marking them would promise a clean that never happens.
func (m model) markable(r treeRow) bool {
	if r.node == nil || r.node.Match == nil {
		return false
	}
	_, ok := m.byPath[r.path]
	return ok
}

// treeView renders the directory browser.
func (m model) treeView() string {
	var b strings.Builder
	cur := m.cur()
	fmt.Fprintf(&b, "%s  %s", m.curPath(), format.Human(cur.Bytes))
	if cur.Reclaimable > 0 {
		fmt.Fprintf(&b, "  (%s reclaimable)", format.Human(cur.Reclaimable))
	}
	fmt.Fprintf(&b, "\n%d marked (%s)\n", len(m.picks()), format.Human(m.markedBytes()))
	b.WriteString("[enter] open  [bksp] up  [space] mark  [c] clean  [tab] candidates  [q] quit\n\n")

	rows := m.rows()
	if len(rows) == 0 {
		b.WriteString("  (empty)\n")
		return b.String()
	}
	cursor := m.treeCursor()
	visible := m.height - 6
	if visible < 3 {
		visible = 3
	}
	start := 0
	if cursor >= visible {
		start = cursor - visible + 1
	}
	for i := start; i < len(rows) && i < start+visible; i++ {
		r := rows[i]
		point := " "
		if i == cursor {
			point = ">"
		}
		box := "   "
		if m.markable(r) {
			box = "[ ]"
			if m.marked[r.path] {
				box = "[x]"
			}
		}
		reclaim := ""
		if r.reclaimable > 0 {
			reclaim = format.Human(r.reclaimable) + " reclaimable"
		}
		open := " "
		if r.openable() {
			open = ">"
		}
		fmt.Fprintf(&b, "%s %s %9s  %-28s %s%s\n",
			point, box, format.Human(r.bytes), r.label+open, reclaim, tierTag(r))
	}
	return b.String()
}

// tierTag labels a row the rules have claimed.
func tierTag(r treeRow) string {
	if r.node == nil || r.node.Match == nil {
		return ""
	}
	return "  [" + r.node.Match.Tier.String() + "]"
}

// treeKey handles one keypress in the tree browser.
func (m model) treeKey(k string) model {
	rows := m.rows()
	cursor := m.treeCursor()
	switch k {
	case "j", "down":
		if cursor < len(rows)-1 {
			cursor++
		}
	case "k", "up":
		if cursor > 0 {
			cursor--
		}
	case "enter", "l", "right":
		if cursor < len(rows) && rows[cursor].openable() {
			m.stack = append(m.stack, rows[cursor].node)
			m.tcurs = append(m.tcurs, 0)
			return m
		}
	case "backspace", "h", "left":
		if len(m.stack) > 1 {
			m.stack = m.stack[:len(m.stack)-1]
			m.tcurs = m.tcurs[:len(m.tcurs)-1]
		}
		return m
	case " ", "space":
		if cursor < len(rows) && m.markable(rows[cursor]) {
			p := rows[cursor].path
			m.marked[p] = !m.marked[p]
		}
	}
	if len(m.tcurs) > 0 {
		m.tcurs[len(m.tcurs)-1] = cursor
	}
	return m
}
