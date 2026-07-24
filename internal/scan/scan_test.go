package scan

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	home := b.TempDir()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := New(rules.Default(home)).Run(root)
		if res.Errors > 0 {
			b.Fatalf("scan errors: %d", res.Errors)
		}
	}
}

// BenchmarkScanWidth sweeps the walker pool width to find where overlapping
// syscalls stops paying on this hardware; the scan is syscall-bound, so
// width, not CPU count, sets the throughput ceiling.
func BenchmarkScanWidth(b *testing.B) {
	root := benchTree(b)
	home := b.TempDir()
	for _, w := range []int{4, 8, 16, 32, 64, 128, 256} {
		b.Run(fmt.Sprintf("w%d", w), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				s := &Scanner{
					rules: rules.Default(home),
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
