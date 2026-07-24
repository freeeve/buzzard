package veto

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRecentVetoesFreshMtime(t *testing.T) {
	if v := Recent(time.Now().Add(-time.Minute), 15*time.Minute); v == nil {
		t.Error("fresh mtime not vetoed")
	}
	if v := Recent(time.Now().Add(-time.Hour), 15*time.Minute); v != nil {
		t.Errorf("stale mtime vetoed: %s", v.Reason)
	}
	if v := Recent(time.Time{}, 15*time.Minute); v != nil {
		t.Errorf("zero mtime vetoed: %s", v.Reason)
	}
}

func TestOpenHandlesSeesHeldFile(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof not available")
	}
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "held.dat"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if v := OpenHandles(ctx, dir); v == nil {
		t.Error("held file not detected")
	}
}

func TestOpenHandlesQuietDir(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof not available")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cold.dat"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if v := OpenHandles(ctx, dir); v != nil {
		t.Errorf("quiet dir vetoed: %s", v.Reason)
	}
}
