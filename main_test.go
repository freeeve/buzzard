package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/freeeve/buzzard/internal/rules"
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

// testEpoch anchors candidate timestamps so equal-idle candidates score
// exactly equal. Scan timestamps come from second-granularity mtimes, so
// exact ties are ordinary in practice, not a contrived case.
var testEpoch = time.Now().Truncate(time.Hour)

// cand builds a candidate with a fixed idle age measured from testEpoch.
func cand(path string, bytes int64, idle time.Duration) scan.Candidate {
	return scan.Candidate{
		Path: path, Bytes: bytes, NewestMod: testEpoch.Add(-idle),
		Match: &rules.Match{Category: "node_modules", Tier: rules.TierA},
	}
}

func paths(cs []scan.Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Path
	}
	return out
}

// TestSortByScoreIsDeterministicForTies is the regression for task 022:
// the concurrent walk appends candidates in nondeterministic order, so
// equal-valued entries used to swap places between runs of one binary.
func TestSortByScoreIsDeterministicForTies(t *testing.T) {
	idle := 30 * 24 * time.Hour
	forward := &scan.Result{Candidates: []scan.Candidate{
		cand("/src/alpha", 1<<20, idle),
		cand("/src/beta", 1<<20, idle),
		cand("/src/gamma", 1<<20, idle),
	}}
	reverse := &scan.Result{Candidates: []scan.Candidate{
		cand("/src/gamma", 1<<20, idle),
		cand("/src/beta", 1<<20, idle),
		cand("/src/alpha", 1<<20, idle),
	}}
	sortByScore(forward)
	sortByScore(reverse)
	want := []string{"/src/alpha", "/src/beta", "/src/gamma"}
	for i := range want {
		if forward.Candidates[i].Path != want[i] || reverse.Candidates[i].Path != want[i] {
			t.Fatalf("tie order not stable: %v vs %v, want %v",
				paths(forward.Candidates), paths(reverse.Candidates), want)
		}
	}
}

// TestSortByScoreRanksByValue keeps the tiebreak from overriding the
// actual ordering: bigger and idler still wins.
func TestSortByScoreRanksByValue(t *testing.T) {
	res := &scan.Result{Candidates: []scan.Candidate{
		cand("/z/small-old", 1<<20, 365*24*time.Hour),
		cand("/a/big-fresh", 900<<20, time.Hour),
	}}
	sortByScore(res)
	if res.Candidates[0].Path != "/a/big-fresh" {
		t.Errorf("order = %v, want the large candidate first", paths(res.Candidates))
	}
}

// TestSortByScoreUsesOneClockReading guards the comparator against the
// clock moving mid-sort, which made it non-transitive for near-equal
// entries. A large set of identical candidates exercises many comparisons.
func TestSortByScoreUsesOneClockReading(t *testing.T) {
	var cs []scan.Candidate
	for i := 0; i < 500; i++ {
		cs = append(cs, cand(fmt.Sprintf("/src/p%03d", i), 1<<20, 30*24*time.Hour))
	}
	res := &scan.Result{Candidates: cs}
	sortByScore(res)
	for i := 1; i < len(res.Candidates); i++ {
		if res.Candidates[i-1].Path >= res.Candidates[i].Path {
			t.Fatalf("not fully ordered at %d: %q then %q",
				i, res.Candidates[i-1].Path, res.Candidates[i].Path)
		}
	}
}
