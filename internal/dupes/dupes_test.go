package dupes

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel string, content []byte) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func TestFindGroupsIdenticalContent(t *testing.T) {
	root := t.TempDir()
	blob := bytes.Repeat([]byte{'d'}, 4096)
	write(t, root, "a/one.bin", blob)
	write(t, root, "b/two.bin", blob)
	write(t, root, "c/other.bin", bytes.Repeat([]byte{'x'}, 4096))
	groups, err := Find(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1: %+v", len(groups), groups)
	}
	if len(groups[0].Paths) != 2 || groups[0].Wasted() != 4096 {
		t.Errorf("group = %+v", groups[0])
	}
}

func TestFindSameSizeDifferentContentNotGrouped(t *testing.T) {
	root := t.TempDir()
	write(t, root, "x.bin", bytes.Repeat([]byte{'x'}, 2048))
	write(t, root, "y.bin", bytes.Repeat([]byte{'y'}, 2048))
	groups, err := Find(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Errorf("size-colliding non-dupes grouped: %+v", groups)
	}
}

func TestFindHardlinksAreOneFile(t *testing.T) {
	root := t.TempDir()
	blob := bytes.Repeat([]byte{'h'}, 4096)
	orig := write(t, root, "orig.bin", blob)
	if err := os.Link(orig, filepath.Join(root, "link.bin")); err != nil {
		t.Skipf("hardlinks unsupported: %v", err)
	}
	groups, err := Find(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Errorf("hardlinked pair reported as duplicates: %+v", groups)
	}
}

func TestFindRespectsMinSize(t *testing.T) {
	root := t.TempDir()
	small := []byte("tiny")
	write(t, root, "s1.bin", small)
	write(t, root, "s2.bin", small)
	groups, err := Find(root, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 0 {
		t.Errorf("sub-threshold dupes reported: %+v", groups)
	}
}

func TestFindOrdersByWastedBytes(t *testing.T) {
	root := t.TempDir()
	big := bytes.Repeat([]byte{'b'}, 8192)
	sml := bytes.Repeat([]byte{'s'}, 4096)
	write(t, root, "s1.bin", sml)
	write(t, root, "s2.bin", sml)
	write(t, root, "b1.bin", big)
	write(t, root, "b2.bin", big)
	groups, err := Find(root, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Size != 8192 {
		t.Errorf("order wrong: %+v", groups)
	}
}
