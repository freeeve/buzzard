# buzzard

Finds disk space that is safe to reclaim — and can prove it.

Named for the turkey vulture, genus *Cathartes* — Greek for "the purifier."
It only eats what's already dead. So does this tool: buzzard never claims a
directory is reclaimable on its name alone; every candidate carries the
structural evidence for the claim and the command that regenerates it.

## Usage

```sh
buzzard ~/src            # report what is reclaimable (deletes nothing)
buzzard -i ~/src         # interactive: browse, mark, clean behind a confirm
buzzard -clean ~/src     # move tier A candidates to the OS trash (asks first)
buzzard -restore         # put back everything from the most recent clean
buzzard -dupes ~/src     # also list duplicate files (identical content)
```

Candidates rank by reclaim value -- size weighted by idle time -- and
anything modified in the last 15 minutes or held open by a running process
is flagged in use and skipped by cleaning.

```
buzzard scanned /Users/you/src: 94.2 GiB on disk

TIER A — regenerable by contract
    4.1 GiB  node_modules                 /Users/you/src/webapp/node_modules
             idle 14mo                    regen: npm ci (or yarn/pnpm/bun install)
             why: sibling package.json + package-lock.json
    2.3 GiB  cargo target                 /Users/you/src/rusty/target
             idle 2mo                     regen: cargo build
             why: sibling Cargo.toml

TIER B — probably disposable, review each
  812.4 MiB  node_modules (orphaned)      /Users/you/src/old/node_modules
             idle 2.1y                    regen: none needed if the project is gone
             why: no package.json beside it; project may be gone

reclaimable: 6.4 GiB by contract (tier A), 812.4 MiB more after review (tier B)

nothing was deleted. buzzard only circles what is already dead.
```

## The safety model

"Safe to delete" is never detected — it is a claim supported by evidence:

- **Tier A — regenerable by contract.** A lockfile pins the exact contents
  (`node_modules` + `package-lock.json`), the build system rebuilds it
  (`target` + `Cargo.toml`), or the platform documents the path as purgeable
  (`~/Library/Caches`, Xcode DerivedData, package-manager caches).
- **Tier B — probably disposable.** The evidence is suggestive but not a
  contract: a `node_modules` with no lockfile, a venv whose dependency
  manifest is gone. Worth a human glance.
- **Everything else is not classified.** A big old directory of unknown files
  is your call, not buzzard's.

Sizing is honest: allocated blocks (not apparent size), hardlinked inodes
counted once, symlinks never followed, and directory inodes at their real
allocated size -- zero on APFS, real blocks on filesystems that allocate
them (some tools pad a flat 4 KiB per directory instead). The number
reported is the number deletion would actually free.

Cleaning is reversible by construction: items go to the OS trash (never
`rm`) -- NSFileManager with Finder put-back on macOS, the freedesktop.org
trash spec on Linux -- every move is recorded in `~/.buzzard/manifest.jsonl`
with the evidence that justified it, each candidate's evidence is re-checked
immediately before it moves, and `-restore` undoes the last clean.

## Roadmap

- Sequential/narrow scan mode for spinning disks
- Classification from directory listings (no evidence probes)
- gdu JSON import for report interchange

## Custom rule packs

The rules are data. Drop JSON packs in `~/.buzzard/rules.d/` (or pass
`-rules extra.json`) to teach buzzard new categories:

```json
{
  "rules": [
    {
      "match": {"basenames": ["bazel-out"]},
      "variants": [
        {
          "category": "bazel output",
          "tier": "A",
          "regen": "bazel build",
          "evidence": [{"sibling_any": ["WORKSPACE", "MODULE.bazel"]}]
        }
      ]
    }
  ]
}
```

A rule matches by `basenames`, by a file it must contain (`contains_any`),
or by a fixed `home_path`. Variants are tried in order; the first whose
evidence all holds claims the directory, and the report cites what matched.
Every variant needs evidence or an explicit `why`, plus a `regen` command.
Built-in rules take precedence, and a user pack cannot silently redefine a
built-in fixed path.

## Performance

On macOS, buzzard lists directories with `getattrlistbulk`, retrieving the
stat facts for many entries per syscall instead of one `lstat` per file.
Measured with hyperfine on a quiet M3 Max (load-gated, warm cache, APFS,
50k files / 5k dirs), July 2026:

| tool | mean |
|---|---|
| buzzard v0.1.3 | 36.7 ± 3.7 ms |
| gdu (master, `-npc`) | 70.3 ± 3.6 ms |

1.92 ± 0.22× faster, with ~2.9× less total CPU — while also classifying
reclaim candidates during the walk. Other platforms use a portable
ReadDir+lstat walker, verified equivalent by test.

## Install

```sh
go install github.com/freeeve/buzzard@latest
```
