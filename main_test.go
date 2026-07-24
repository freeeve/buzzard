package main

import (
	"testing"

	"github.com/freeeve/buzzard/internal/scan"
)

// tree builds a root node with the given children for breakdown tests.
func tree(own int64, kids ...*scan.Node) *scan.Node {
	n := &scan.Node{Name: "/root", Own: own, Bytes: own}
	for _, k := range kids {
		n.Children = append(n.Children, k)
		n.Bytes += k.Bytes
		n.Reclaimable += k.Reclaimable
	}
	return n
}

func kid(name string, bytes, reclaim int64) *scan.Node {
	return &scan.Node{Name: name, Bytes: bytes, Reclaimable: reclaim}
}

func TestBreakdownRowsOrderLargestFirst(t *testing.T) {
	rows := breakdownRows(tree(0, kid("small", 10, 0), kid("big", 300, 5), kid("mid", 100, 0)))
	want := []string{"big/", "mid/", "small/"}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i].name != w {
			t.Errorf("row %d = %q, want %q", i, rows[i].name, w)
		}
	}
}

// TestBreakdownRowsBreakTiesByName locks out the filesystem's unstable
// listing order, which already leaked into the candidate report (task 022).
func TestBreakdownRowsBreakTiesByName(t *testing.T) {
	forward := breakdownRows(tree(0, kid("alpha", 100, 0), kid("beta", 100, 0)))
	reverse := breakdownRows(tree(0, kid("beta", 100, 0), kid("alpha", 100, 0)))
	if forward[0].name != "alpha/" || reverse[0].name != "alpha/" {
		t.Errorf("equal-sized rows not ordered by name: %q then %q",
			forward[0].name, reverse[0].name)
	}
}

// TestBreakdownRowsIncludeLooseFiles keeps the section a full account: bytes
// sitting directly in the root are a line, not a silent omission.
func TestBreakdownRowsIncludeLooseFiles(t *testing.T) {
	rows := breakdownRows(tree(50, kid("sub", 100, 0)))
	var total int64
	var found bool
	for _, r := range rows {
		total += r.bytes
		if r.name == "(files here)" {
			found = true
			if r.bytes != 50 {
				t.Errorf("loose files = %d, want 50", r.bytes)
			}
		}
	}
	if !found {
		t.Error("no (files here) row for the root's own bytes")
	}
	if total != 150 {
		t.Errorf("rows total %d, want 150", total)
	}
}

func TestBreakdownRowsOmitsEmptyLooseFiles(t *testing.T) {
	for _, r := range breakdownRows(tree(0, kid("sub", 100, 0))) {
		if r.name == "(files here)" {
			t.Error("emitted a (files here) row for zero loose bytes")
		}
	}
}

// TestBreakdownRowsAccountForEveryByte is the invariant that makes the
// section trustworthy: what it prints has to add up to what was scanned.
func TestBreakdownRowsAccountForEveryByte(t *testing.T) {
	root := tree(7, kid("a", 100, 40), kid("b", 250, 0), kid("c", 33, 33))
	var bytes, reclaim int64
	for _, r := range breakdownRows(root) {
		bytes += r.bytes
		reclaim += r.reclaimable
	}
	if bytes != root.Bytes {
		t.Errorf("rows sum to %d, tree has %d", bytes, root.Bytes)
	}
	if reclaim != root.Reclaimable {
		t.Errorf("reclaimable sums to %d, tree has %d", reclaim, root.Reclaimable)
	}
}

func TestBreakdownRowsNilTree(t *testing.T) {
	if rows := breakdownRows(nil); rows != nil {
		t.Errorf("got %v, want nil", rows)
	}
}
