# buzzard

Finds disk space that is safe to reclaim — and can prove it.

Named for the turkey vulture, genus *Cathartes* — Greek for "the purifier."
It only eats what's already dead. So does this tool: buzzard never claims a
directory is reclaimable on its name alone; every candidate carries the
structural evidence for the claim and the command that regenerates it.

## Usage

```sh
buzzard ~/src
```

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
counted once, symlinks never followed. The number reported is the number
deletion would actually free.

## Roadmap

- Deletion via the OS trash (never `rm`), dry-run by default, with a manifest
  of everything removed
- Rule packs as data (community-extensible categories)
- Staleness-aware ranking and active-use veto (open handles, running builds)
- Interactive TUI
- Duplicate detection

## Install

```sh
go install github.com/freeeve/buzzard@latest
```
