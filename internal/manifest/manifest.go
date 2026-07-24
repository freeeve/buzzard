// Package manifest keeps an append-only JSONL ledger of everything buzzard
// trashes: what, from where, to where, why, and in which run. The ledger is
// what makes deletion an auditable, reversible act instead of a shrug --
// restores append their own records rather than rewriting history.
package manifest

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Actions a record can describe.
const (
	ActionTrash   = "trash"
	ActionRestore = "restore"
)

// Record is one manifest line: a single item trashed or restored.
type Record struct {
	RunID     string    `json:"run_id"`
	Action    string    `json:"action"`
	Time      time.Time `json:"time"`
	Path      string    `json:"path"`
	TrashedTo string    `json:"trashed_to"`
	Category  string    `json:"category,omitempty"`
	Tier      string    `json:"tier,omitempty"`
	Evidence  string    `json:"evidence,omitempty"`
	Bytes     int64     `json:"bytes,omitempty"`
}

// DefaultPath returns the manifest location, ~/.buzzard/manifest.jsonl.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".buzzard", "manifest.jsonl"), nil
}

// Append writes records to the ledger, creating it (and its directory) on
// first use.
func Append(path string, recs []Record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return w.Flush()
}

// ReadAll returns every record in the ledger in file order. A missing file
// is an empty ledger, not an error.
func ReadAll(path string) ([]Record, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var recs []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, err
		}
		recs = append(recs, r)
	}
	return recs, sc.Err()
}

// LastTrashRun returns the records of the most recent trash run that have
// not since been restored, so a restore command can undo exactly the last
// clean and nothing else.
func LastTrashRun(path string) ([]Record, error) {
	recs, err := ReadAll(path)
	if err != nil {
		return nil, err
	}
	lastRun := ""
	for _, r := range recs {
		if r.Action == ActionTrash {
			lastRun = r.RunID
		}
	}
	if lastRun == "" {
		return nil, nil
	}
	restored := make(map[string]bool)
	for _, r := range recs {
		if r.Action == ActionRestore {
			restored[r.TrashedTo] = true
		}
	}
	var out []Record
	for _, r := range recs {
		if r.Action == ActionTrash && r.RunID == lastRun && !restored[r.TrashedTo] {
			out = append(out, r)
		}
	}
	return out, nil
}
