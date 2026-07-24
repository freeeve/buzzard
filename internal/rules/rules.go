// Package rules classifies directories as reclaimable disk space based on
// structural evidence, never on name alone. The rules themselves are data:
// a pack (see pack.go) declares match gates and ordered evidence variants,
// and every match carries the evidence it cited so the report can explain
// itself. The built-in pack is embedded; Compile accepts external packs.
package rules

import (
	"encoding/json"
	"path/filepath"
	"syscall"
)

// Tier grades how confident buzzard is that deleting a candidate is safe.
type Tier int

const (
	// TierA marks directories regenerable by contract: a lockfile pins the
	// exact contents, or the platform documents the path as purgeable.
	TierA Tier = iota
	// TierB marks directories that are probably disposable but deserve a
	// human glance before deletion.
	TierB
)

// String returns the display label for a tier.
func (t Tier) String() string {
	if t == TierA {
		return "A"
	}
	return "B"
}

// Match describes a directory a rule has claimed, with the evidence cited.
type Match struct {
	Category string
	Tier     Tier
	Evidence string
	Regen    string
}

// Ruleset holds a compiled pack resolved against a home directory.
type Ruleset struct {
	byBase map[string][]*compiledRule
	gated  []*compiledRule
	fixed  map[string]*Match
}

// Default returns the built-in ruleset with fixed paths resolved under
// home. The embedded pack is part of the build, so failing to compile it is
// a programming error, not a runtime condition.
func Default(home string) *Ruleset {
	var p Pack
	if err := json.Unmarshal(defaultPackJSON, &p); err != nil {
		panic("rules: embedded default pack is invalid JSON: " + err.Error())
	}
	rs, err := Compile(&p, home)
	if err != nil {
		panic("rules: embedded default pack failed to compile: " + err.Error())
	}
	return rs
}

// Classify returns the first match a rule claims for dir, or nil if no rule
// produces sufficient evidence. Fixed paths win over structural rules
// because their evidence is a documented contract.
func (rs *Ruleset) Classify(dir string) *Match {
	if m, ok := rs.fixed[dir]; ok {
		return m
	}
	base := filepath.Base(dir)
	for _, cr := range rs.byBase[base] {
		if m := cr.eval(dir, base); m != nil {
			return m
		}
	}
	for _, cr := range rs.gated {
		if _, ok := anyExists(dir, cr.gate.ContainsAny...); !ok {
			continue
		}
		if m := cr.eval(dir, base); m != nil {
			return m
		}
	}
	return nil
}

// anyExists reports whether any of the named files exists in dir, returning
// the first name found. It probes with syscall.Access rather than os.Lstat:
// this path runs for directories the scanner visits, and a miss must not
// allocate an error value.
func anyExists(dir string, names ...string) (string, bool) {
	for _, n := range names {
		if syscall.Access(dir+string(filepath.Separator)+n, 0) == nil {
			return n, true
		}
	}
	return "", false
}
