package manifest

import (
	"path/filepath"
	"testing"
	"time"
)

func rec(run, action, trashedTo string) Record {
	return Record{
		RunID:     run,
		Action:    action,
		Time:      time.Now(),
		Path:      "/src/junk",
		TrashedTo: trashedTo,
		Category:  "node_modules",
		Tier:      "A",
		Bytes:     42,
	}
}

func TestAppendAndReadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "manifest.jsonl")
	want := []Record{rec("r1", ActionTrash, "/trash/a"), rec("r1", ActionTrash, "/trash/b")}
	if err := Append(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAll(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TrashedTo != "/trash/a" || got[1].Category != "node_modules" {
		t.Errorf("round trip mismatch: %+v", got)
	}
}

func TestReadAllMissingFileIsEmpty(t *testing.T) {
	got, err := ReadAll(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil || got != nil {
		t.Errorf("missing file: got %v, %v", got, err)
	}
}

func TestLastTrashRunSelectsLatestRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.jsonl")
	if err := Append(path, []Record{
		rec("r1", ActionTrash, "/trash/old"),
		rec("r2", ActionTrash, "/trash/a"),
		rec("r2", ActionTrash, "/trash/b"),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := LastTrashRun(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].RunID != "r2" {
		t.Errorf("last run: %+v", got)
	}
}

func TestLastTrashRunSkipsRestored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.jsonl")
	if err := Append(path, []Record{
		rec("r1", ActionTrash, "/trash/a"),
		rec("r1", ActionTrash, "/trash/b"),
		rec("r1-undo", ActionRestore, "/trash/a"),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := LastTrashRun(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TrashedTo != "/trash/b" {
		t.Errorf("restored item not excluded: %+v", got)
	}
}
