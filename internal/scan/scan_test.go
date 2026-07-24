package scan

import (
	"bytes"
	"os"
	"path/filepath"
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
