package trash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPutAndRestoreFile(t *testing.T) {
	dir := t.TempDir()
	orig := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(orig, []byte("carrion"), 0o644); err != nil {
		t.Fatal(err)
	}
	trashed, err := Put(orig)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(trashed) })
	if _, err := os.Lstat(orig); err == nil {
		t.Error("original still exists after Put")
	}
	if _, err := os.Lstat(trashed); err != nil {
		t.Fatalf("trashed item missing at %s: %v", trashed, err)
	}
	if err := Restore(trashed, orig); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	got, err := os.ReadFile(orig)
	if err != nil || string(got) != "carrion" {
		t.Errorf("restored content = %q, %v", got, err)
	}
}

func TestPutDirectoryTree(t *testing.T) {
	dir := t.TempDir()
	tree := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(filepath.Join(tree, "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "dep", "index.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	trashed, err := Put(tree)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(trashed) })
	if _, err := os.Lstat(tree); err == nil {
		t.Error("tree still exists after Put")
	}
	if _, err := os.Lstat(filepath.Join(trashed, "dep", "index.js")); err != nil {
		t.Errorf("nested file missing in trash: %v", err)
	}
}

func TestPutCollidingNames(t *testing.T) {
	dir := t.TempDir()
	mk := func() string {
		p := filepath.Join(dir, "same-name")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first, err := Put(mk())
	if err != nil {
		t.Fatalf("Put #1: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(first) })
	second, err := Put(mk())
	if err != nil {
		t.Fatalf("Put #2: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(second) })
	if first == second {
		t.Errorf("colliding trash destinations: %s", first)
	}
}

func TestRestoreRefusesExistingOriginal(t *testing.T) {
	dir := t.TempDir()
	orig := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(orig, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	trashed, err := Put(orig)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(trashed) })
	if err := os.WriteFile(orig, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Restore(trashed, orig); err == nil {
		t.Error("Restore overwrote an existing original")
	}
}
