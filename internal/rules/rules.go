// Package rules classifies directories as reclaimable disk space based on
// structural evidence, never on name alone. Each rule requires proof that the
// directory is regenerable (a lockfile, a manifest, a platform contract)
// before it may claim a candidate, and every match carries the evidence it
// cited so the report can explain itself.
package rules

import (
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

// Rule inspects a directory and either claims it with a Match or declines.
type Rule func(dir string) *Match

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

// nodeModules claims node_modules directories. A sibling lockfile proves the
// contents are regenerable byte-for-byte (Tier A); a bare package.json makes
// the directory only probably disposable (Tier B).
func nodeModules(dir string) *Match {
	if filepath.Base(dir) != "node_modules" {
		return nil
	}
	parent := filepath.Dir(dir)
	if _, ok := anyExists(parent, "package.json"); !ok {
		return &Match{
			Category: "node_modules (orphaned)",
			Tier:     TierB,
			Evidence: "no package.json beside it; project may be gone",
			Regen:    "none needed if the project is gone",
		}
	}
	if lock, ok := anyExists(parent, "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "bun.lock"); ok {
		return &Match{
			Category: "node_modules",
			Tier:     TierA,
			Evidence: "sibling package.json + " + lock,
			Regen:    "npm ci (or yarn/pnpm/bun install)",
		}
	}
	return &Match{
		Category: "node_modules",
		Tier:     TierB,
		Evidence: "sibling package.json but no lockfile",
		Regen:    "npm install",
	}
}

// cargoTarget claims Rust target directories proven by a sibling Cargo.toml.
func cargoTarget(dir string) *Match {
	if filepath.Base(dir) != "target" {
		return nil
	}
	if _, ok := anyExists(filepath.Dir(dir), "Cargo.toml"); !ok {
		return nil
	}
	return &Match{
		Category: "cargo target",
		Tier:     TierA,
		Evidence: "sibling Cargo.toml",
		Regen:    "cargo build",
	}
}

// pythonVenv claims virtualenvs identified by pyvenv.cfg inside the
// directory itself; a sibling dependency manifest upgrades them to Tier A.
func pythonVenv(dir string) *Match {
	if _, ok := anyExists(dir, "pyvenv.cfg"); !ok {
		return nil
	}
	if m, ok := anyExists(filepath.Dir(dir), "requirements.txt", "pyproject.toml", "uv.lock", "Pipfile.lock", "poetry.lock"); ok {
		return &Match{
			Category: "python venv",
			Tier:     TierA,
			Evidence: "pyvenv.cfg inside + sibling " + m,
			Regen:    "recreate venv and reinstall deps",
		}
	}
	return &Match{
		Category: "python venv",
		Tier:     TierB,
		Evidence: "pyvenv.cfg inside, no dependency manifest beside it",
		Regen:    "recreate venv (dependency list unknown)",
	}
}

// toolCache claims per-project tool caches that are always regenerable
// because the tool rebuilds them on demand.
func toolCache(dir string) *Match {
	switch filepath.Base(dir) {
	case "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".parcel-cache", ".turbo":
		return &Match{
			Category: filepath.Base(dir),
			Tier:     TierA,
			Evidence: "tool cache; rebuilt on demand",
			Regen:    "rebuilt automatically on next run",
		}
	}
	return nil
}

// buildOutput claims build output directories only when the surrounding
// project structure proves they are derived artifacts.
func buildOutput(dir string) *Match {
	switch filepath.Base(dir) {
	case ".next", ".nuxt", ".output":
	default:
		return nil
	}
	if _, ok := anyExists(filepath.Dir(dir), "package.json"); !ok {
		return nil
	}
	return &Match{
		Category: "build output " + filepath.Base(dir),
		Tier:     TierA,
		Evidence: "framework build dir + sibling package.json",
		Regen:    "npm run build",
	}
}

// fixedPath describes a well-known absolute path whose purgeability is a
// platform or package-manager contract rather than a structural inference.
type fixedPath struct {
	rel      string // relative to $HOME
	category string
	evidence string
	regen    string
}

// fixedPaths lists cache locations that the owning tool or OS documents as
// safe to purge. All are Tier A by contract.
var fixedPaths = []fixedPath{
	{"Library/Developer/Xcode/DerivedData", "Xcode DerivedData", "Xcode regenerates DerivedData on build", "rebuild in Xcode"},
	{"Library/Caches", "macOS user caches", "macOS documents ~/Library/Caches as purgeable", "apps rebuild caches on demand"},
	{".npm", "npm cache", "npm cache is content-addressed and re-fetched", "refilled on next npm install"},
	{".cache", "XDG cache", "XDG contract: $HOME/.cache is non-essential", "apps rebuild caches on demand"},
	{".cargo/registry", "cargo registry cache", "crates re-downloaded from registry", "refilled on next cargo build"},
	{"go/pkg/mod/cache", "Go module cache", "modules re-downloaded and checksum-verified", "refilled on next go build"},
	{".gradle/caches", "Gradle caches", "Gradle re-resolves dependencies", "refilled on next gradle build"},
}

// Ruleset holds the structural rules plus fixed-path rules resolved against
// a specific home directory.
type Ruleset struct {
	structural []Rule
	fixed      map[string]*Match
}

// Default returns the built-in ruleset with fixed paths resolved under home.
func Default(home string) *Ruleset {
	rs := &Ruleset{
		structural: []Rule{nodeModules, cargoTarget, pythonVenv, toolCache, buildOutput},
		fixed:      make(map[string]*Match, len(fixedPaths)),
	}
	for _, fp := range fixedPaths {
		rs.fixed[filepath.Join(home, fp.rel)] = &Match{
			Category: fp.category,
			Tier:     TierA,
			Evidence: fp.evidence,
			Regen:    fp.regen,
		}
	}
	return rs
}

// Classify returns the first match a rule claims for dir, or nil if no rule
// produces sufficient evidence. Absolute fixed paths win over structural
// rules because their evidence is a documented contract.
func (rs *Ruleset) Classify(dir string) *Match {
	if m, ok := rs.fixed[dir]; ok {
		return m
	}
	for _, rule := range rs.structural {
		if m := rule(dir); m != nil {
			return m
		}
	}
	return nil
}
