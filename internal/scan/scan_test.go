package scan

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/freeeve/buzzard/internal/rules"
)

// writeBytes creates a file of n bytes at the given relative path.
func writeBytes(t *testing.T, root, rel string, n int) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, bytes.Repeat([]byte{'x'}, n), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func newScanner(t *testing.T) *Scanner {
	t.Helper()
	return New(rules.Default(t.TempDir()))
}

func TestTotalCoversAllocatedSize(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, root, "a/one.bin", 1<<20)
	writeBytes(t, root, "b/two.bin", 1<<20)
	res := newScanner(t).Run(root)
	if res.TotalBytes < 2<<20 {
		t.Errorf("total = %d, want >= %d", res.TotalBytes, 2<<20)
	}
	if res.Errors != 0 {
		t.Errorf("errors = %d, want 0", res.Errors)
	}
}

func TestHardlinksCountedOnce(t *testing.T) {
	root := t.TempDir()
	orig := writeBytes(t, root, "a/file.bin", 1<<20)
	if err := os.Link(orig, filepath.Join(root, "a", "link.bin")); err != nil {
		t.Skipf("hardlinks unsupported here: %v", err)
	}
	res := newScanner(t).Run(root)
	if res.TotalBytes >= 2<<20 {
		t.Errorf("total = %d; hardlinked inode counted twice", res.TotalBytes)
	}
}

func TestCandidateSizedAndClassified(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, root, "app/package.json", 10)
	writeBytes(t, root, "app/package-lock.json", 10)
	writeBytes(t, root, "app/node_modules/dep/index.js", 1<<20)
	res := newScanner(t).Run(root)
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(res.Candidates))
	}
	c := res.Candidates[0]
	if c.Match.Category != "node_modules" {
		t.Errorf("category = %q", c.Match.Category)
	}
	if c.Bytes < 1<<20 {
		t.Errorf("candidate bytes = %d, want >= %d", c.Bytes, 1<<20)
	}
	if c.NewestMod.IsZero() {
		t.Error("newest mtime not recorded")
	}
	if res.TotalBytes < c.Bytes {
		t.Errorf("total %d < candidate %d; candidate not folded into total", res.TotalBytes, c.Bytes)
	}
}

func TestNestedCandidateNotDoubleCounted(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, root, "app/package.json", 10)
	writeBytes(t, root, "app/package-lock.json", 10)
	writeBytes(t, root, "app/node_modules/dep/package.json", 10)
	writeBytes(t, root, "app/node_modules/dep/node_modules/inner/x.js", 1<<20)
	res := newScanner(t).Run(root)
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1 (outer only)", len(res.Candidates))
	}
}

var (
	benchOnce sync.Once
	benchRoot string
)

// benchTree lazily builds a shared fixture of 500 directories x 20 small
// files (10k files) plus a few classifiable candidates, reused across all
// benchmarks in the package so tree construction never pollutes timings.
func benchTree(b *testing.B) string {
	benchOnce.Do(func() {
		root, err := os.MkdirTemp("", "buzzard-bench")
		if err != nil {
			b.Fatal(err)
		}
		payload := bytes.Repeat([]byte{'x'}, 256)
		for d := 0; d < 500; d++ {
			dir := filepath.Join(root, fmt.Sprintf("proj%02d", d%50), fmt.Sprintf("dir%03d", d))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				b.Fatal(err)
			}
			for f := 0; f < 20; f++ {
				name := filepath.Join(dir, fmt.Sprintf("file%03d.dat", f))
				if err := os.WriteFile(name, payload, 0o644); err != nil {
					b.Fatal(err)
				}
			}
		}
		app := filepath.Join(root, "proj00", "webapp")
		for _, f := range []string{"package.json", "package-lock.json"} {
			if err := os.MkdirAll(app, 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(app, f), []byte("{}"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(app, "node_modules", "dep"), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(app, "node_modules", "dep", "index.js"), payload, 0o644); err != nil {
			b.Fatal(err)
		}
		benchRoot = root
	})
	return benchRoot
}

func TestMain(m *testing.M) {
	code := m.Run()
	if benchRoot != "" {
		os.RemoveAll(benchRoot)
	}
	os.Exit(code)
}

// BenchmarkScan measures a full scan of the shared 10k-file fixture; the
// per-file allocation count is the number to drive down.
func BenchmarkScan(b *testing.B) {
	root := benchTree(b)
	rs := rules.Default(b.TempDir())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := New(rs).Run(root)
		if res.Errors > 0 {
			b.Fatalf("scan errors: %d", res.Errors)
		}
	}
}

// TestTotalMatchesReferenceWalk locks scan totals to an independent
// lstat-sum over every file and directory including the root: deleting the
// tree frees directory inode blocks too, so the total must include them.
func TestTotalMatchesReferenceWalk(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, root, "a/one.bin", 1<<20)
	writeBytes(t, root, "a/b/two.bin", 1<<19)
	writeBytes(t, root, "c/empty/.keep", 0)
	var want int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		var st syscall.Stat_t
		if err := syscall.Lstat(path, &st); err != nil {
			return err
		}
		want += st.Blocks * 512
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res := newScanner(t).Run(root)
	if res.TotalBytes != want {
		t.Errorf("TotalBytes = %d, reference lstat-sum = %d", res.TotalBytes, want)
	}
}

// TestPlatformListMatchesGeneric locks the platform bulk-listing path to the
// portable ReadDir+lstat path: totals, candidates, and hardlink dedup must
// agree exactly on a tree with hardlinks, symlinks, and a candidate. On
// platforms without a bulk path the two scans are identical by construction.
func TestPlatformListMatchesGeneric(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, root, "app/package.json", 10)
	writeBytes(t, root, "app/package-lock.json", 10)
	writeBytes(t, root, "app/node_modules/dep/index.js", 1<<20)
	orig := writeBytes(t, root, "data/big.bin", 1<<19)
	if err := os.Link(orig, filepath.Join(root, "data", "hard.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(orig, filepath.Join(root, "data", "soft.lnk")); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()

	platform := New(rules.Default(home)).Run(root)
	generic := New(rules.Default(home))
	generic.useGeneric = true
	want := generic.Run(root)

	if platform.TotalBytes != want.TotalBytes {
		t.Errorf("TotalBytes: platform %d != generic %d", platform.TotalBytes, want.TotalBytes)
	}
	if platform.Errors != want.Errors {
		t.Errorf("Errors: platform %d != generic %d", platform.Errors, want.Errors)
	}
	if len(platform.Candidates) != len(want.Candidates) {
		t.Fatalf("Candidates: platform %d != generic %d", len(platform.Candidates), len(want.Candidates))
	}
	for i, c := range platform.Candidates {
		w := want.Candidates[i]
		if c.Path != w.Path || c.Bytes != w.Bytes || c.Match.Category != w.Match.Category {
			t.Errorf("candidate %d: platform %+v != generic %+v", i, c, w)
		}
		if c.NewestMod.Unix() != w.NewestMod.Unix() {
			t.Errorf("candidate %d mtime: platform %v != generic %v", i, c.NewestMod, w.NewestMod)
		}
	}
}

// BenchmarkScanGeneric measures the portable listing path on the shared
// fixture, the in-run A/B partner for BenchmarkScan's platform path.
func BenchmarkScanGeneric(b *testing.B) {
	root := benchTree(b)
	rs := rules.Default(b.TempDir())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := New(rs)
		s.useGeneric = true
		if res := s.Run(root); res.Errors > 0 {
			b.Fatalf("scan errors: %d", res.Errors)
		}
	}
}

// BenchmarkScanWidth sweeps the walker pool width to find where overlapping
// syscalls stops paying on this hardware; the scan is syscall-bound, so
// width, not CPU count, sets the throughput ceiling.
func BenchmarkScanWidth(b *testing.B) {
	root := benchTree(b)
	rs := rules.Default(b.TempDir())
	for _, w := range []int{4, 8, 16, 32, 64, 128, 256} {
		b.Run(fmt.Sprintf("w%d", w), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				s := &Scanner{
					rules: rs,
					sem:   make(chan struct{}, w),
					seen:  make(map[inode]struct{}),
				}
				if res := s.Run(root); res.Errors > 0 {
					b.Fatalf("scan errors: %d", res.Errors)
				}
			}
		})
	}
}

func TestSymlinksNotFollowed(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, root, "real/big.bin", 1<<20)
	scanRoot := filepath.Join(root, "scanme")
	if err := os.MkdirAll(scanRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(scanRoot, "escape")); err != nil {
		t.Fatal(err)
	}
	res := newScanner(t).Run(scanRoot)
	if res.TotalBytes >= 1<<20 {
		t.Errorf("total = %d; scan followed a symlink out of the tree", res.TotalBytes)
	}
}

// findChild returns the named child of n, or nil.
func findChild(n *Node, name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestTreeTotalMatchesScanTotal locks the accounting invariant: the
// breakdown is an account of the whole scan, so the root node has to carry
// exactly the bytes the scan reported.
func TestTreeTotalMatchesScanTotal(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, root, "a/one.bin", 1<<20)
	writeBytes(t, root, "a/deep/two.bin", 1<<20)
	writeBytes(t, root, "b/three.bin", 1<<20)
	writeBytes(t, root, "loose.bin", 1<<20)
	res := newScanner(t).Run(root)
	if res.Tree == nil {
		t.Fatal("nil tree")
	}
	if res.Tree.Bytes != res.TotalBytes {
		t.Errorf("tree bytes = %d, total = %d; breakdown would not sum to the report",
			res.Tree.Bytes, res.TotalBytes)
	}
	var sum int64
	for _, c := range res.Tree.Children {
		sum += c.Bytes
	}
	if sum+res.Tree.Own != res.Tree.Bytes {
		t.Errorf("children %d + own %d != tree %d", sum, res.Tree.Own, res.Tree.Bytes)
	}
	if findChild(res.Tree, "a") == nil || findChild(res.Tree, "b") == nil {
		t.Error("expected children a and b in the tree")
	}
}

// TestTreeAttributesReclaimable checks the column that makes the breakdown
// worth printing: how much of a subtree a rule has claimed.
func TestTreeAttributesReclaimable(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, root, "proj/app/package.json", 10)
	writeBytes(t, root, "proj/app/package-lock.json", 10)
	writeBytes(t, root, "proj/app/node_modules/dep/index.js", 1<<20)
	writeBytes(t, root, "keep/data.bin", 1<<20)
	res := newScanner(t).Run(root)
	proj, keep := findChild(res.Tree, "proj"), findChild(res.Tree, "keep")
	if proj == nil || keep == nil {
		t.Fatal("expected proj and keep children")
	}
	if proj.Reclaimable < 1<<20 {
		t.Errorf("proj reclaimable = %d, want >= %d (node_modules beneath it)",
			proj.Reclaimable, 1<<20)
	}
	if proj.Reclaimable > proj.Bytes {
		t.Errorf("proj reclaimable %d exceeds its size %d", proj.Reclaimable, proj.Bytes)
	}
	if keep.Reclaimable != 0 {
		t.Errorf("keep reclaimable = %d, want 0", keep.Reclaimable)
	}
	if res.Tree.Reclaimable != proj.Reclaimable+keep.Reclaimable {
		t.Errorf("root reclaimable %d != sum of children %d",
			res.Tree.Reclaimable, proj.Reclaimable+keep.Reclaimable)
	}
}

// TestCandidateNodeIsLeaf documents that the scan stops at a claimed
// directory, so its node carries the whole subtree and no children.
func TestCandidateNodeIsLeaf(t *testing.T) {
	root := t.TempDir()
	writeBytes(t, root, "app/package.json", 10)
	writeBytes(t, root, "app/package-lock.json", 10)
	writeBytes(t, root, "app/node_modules/dep/sub/deep.js", 1<<20)
	res := newScanner(t).Run(root)
	app := findChild(res.Tree, "app")
	if app == nil {
		t.Fatal("no app node")
	}
	nm := findChild(app, "node_modules")
	if nm == nil {
		t.Fatal("no node_modules node")
	}
	if nm.Match == nil {
		t.Fatal("node_modules was not claimed by a rule")
	}
	if len(nm.Children) != 0 {
		t.Errorf("candidate node has %d children, want 0 (scan should not descend)", len(nm.Children))
	}
	if nm.Reclaimable != nm.Bytes || nm.Bytes < 1<<20 {
		t.Errorf("candidate node: bytes = %d, reclaimable = %d", nm.Bytes, nm.Reclaimable)
	}
}

// TestNewWidthClampsAndScans checks the walker knob: any width produces
// the same totals, and a nonsense width falls back rather than deadlocking
// on a zero-capacity semaphore.
func TestNewWidthClampsAndScans(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 12; i++ {
		writeBytes(t, root, fmt.Sprintf("d%02d/sub/f.bin", i), 4096)
	}
	want := New(rules.Default(t.TempDir())).Run(root).TotalBytes
	for _, w := range []int{-1, 0, 1, 2, 8, 64} {
		res := NewWidth(rules.Default(t.TempDir()), w).Run(root)
		if res.TotalBytes != want {
			t.Errorf("width %d: total = %d, want %d", w, res.TotalBytes, want)
		}
		if res.Errors != 0 {
			t.Errorf("width %d: errors = %d", w, res.Errors)
		}
	}
}

// TestTreeBuildIsRaceFree exercises the tree across a wide, deep fixture so
// -race can catch unsynchronized node writes from the concurrent walkers.
func TestTreeBuildIsRaceFree(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 40; i++ {
		writeBytes(t, root, fmt.Sprintf("d%02d/sub/deep/file.bin", i), 4096)
	}
	res := newScanner(t).Run(root)
	if got := len(res.Tree.Children); got != 40 {
		t.Errorf("children = %d, want 40", got)
	}
	if res.Tree.Bytes != res.TotalBytes {
		t.Errorf("tree bytes = %d, total = %d", res.Tree.Bytes, res.TotalBytes)
	}
}
