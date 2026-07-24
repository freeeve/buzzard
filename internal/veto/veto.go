// Package veto answers one question just before deletion: is this candidate
// in use right now? A directory whose evidence says "regenerable" is still
// the wrong thing to trash while a build is writing into it, so cleaning
// consults these checks and skips anything they flag.
package veto

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Result explains why a candidate must not be deleted right now; a nil
// Result means no objection.
type Result struct {
	Reason string
}

// Recent vetoes a candidate whose subtree was modified within window --
// the cheapest possible signal that something is actively writing there.
func Recent(newestMod time.Time, window time.Duration) *Result {
	if newestMod.IsZero() {
		return nil
	}
	if age := time.Since(newestMod); age < window {
		return &Result{Reason: fmt.Sprintf("modified %s ago", age.Round(time.Second))}
	}
	return nil
}

// OpenHandles vetoes a candidate if any running process holds a file open
// under dir, using lsof with a deadline. An absent lsof or a timeout is
// reported as no objection: the mtime veto still stands, and blocking every
// clean on a slow lsof would make the safety feature unusable.
func OpenHandles(ctx context.Context, dir string) *Result {
	if _, err := exec.LookPath("lsof"); err != nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, "lsof", "-t", "+D", dir).Output()
	// lsof exits 1 when nothing matched; only non-empty output matters.
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	pids := strings.Fields(string(out))
	if len(pids) == 0 {
		return nil
	}
	return &Result{Reason: fmt.Sprintf("open files held by pid(s) %s", strings.Join(pids, ", "))}
}
