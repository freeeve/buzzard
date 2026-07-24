// Command buzzard scans a directory tree and reports disk space that is safe
// to reclaim, graded by evidence. Like its namesake it only circles what is
// already dead: by default it deletes nothing, and when asked to clean it
// moves tier A candidates to the OS trash behind a confirmation, recording
// every move in a manifest that -restore can undo.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/freeeve/buzzard/internal/manifest"
	"github.com/freeeve/buzzard/internal/rules"
	"github.com/freeeve/buzzard/internal/scan"
	"github.com/freeeve/buzzard/internal/trash"
)

const version = "0.2.0"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	clean := flag.Bool("clean", false, "after reporting, move tier A candidates to the OS trash (asks first)")
	yes := flag.Bool("yes", false, "skip the confirmation prompt for -clean")
	restore := flag.Bool("restore", false, "restore everything from the most recent clean and exit")
	manifestPath := flag.String("manifest", "", "manifest location (default ~/.buzzard/manifest.jsonl)")
	rulePacks := flag.String("rules", "", "comma-separated extra rule pack files (also loads ~/.buzzard/rules.d/*.json)")
	flag.Usage = usage
	flag.Parse()
	if *showVersion {
		fmt.Println("buzzard " + version)
		return
	}
	mpath := *manifestPath
	if mpath == "" {
		var err error
		if mpath, err = manifest.DefaultPath(); err != nil {
			fmt.Fprintf(os.Stderr, "buzzard: %v\n", err)
			os.Exit(1)
		}
	}
	if *restore {
		if *clean {
			fmt.Fprintln(os.Stderr, "buzzard: -restore and -clean are mutually exclusive")
			os.Exit(1)
		}
		os.Exit(runRestore(mpath))
	}
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(os.Stderr, "buzzard: %s is not a readable directory\n", root)
		os.Exit(1)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	var extra []string
	if *rulePacks != "" {
		extra = strings.Split(*rulePacks, ",")
	}
	rs, err := rules.Load(home, extra...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "buzzard: %v\n", err)
		os.Exit(1)
	}
	res := scan.New(rs).Run(root)
	report(res, !*clean)
	if *clean {
		os.Exit(runClean(res, rs, mpath, *yes))
	}
}

// runClean trashes the scan's tier A candidates behind a confirmation,
// re-verifying each one's evidence immediately before it is moved, and
// records every move in the manifest.
func runClean(res *scan.Result, rs *rules.Ruleset, mpath string, yes bool) int {
	var picks []scan.Candidate
	var total int64
	for _, c := range res.Candidates {
		if c.Match.Tier == rules.TierA {
			picks = append(picks, c)
			total += c.Bytes
		}
	}
	if len(picks) == 0 {
		fmt.Println("nothing to clean: no tier A candidates.")
		return 0
	}
	if !yes {
		fmt.Printf("trash %d tier A item(s), reclaiming %s? [y/N] ", len(picks), human(total))
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			fmt.Println("aborted; nothing was deleted.")
			return 0
		}
	}
	runID := time.Now().Format("20060102-150405")
	var recs []manifest.Record
	var freed int64
	failed := 0
	for _, c := range picks {
		if m := rs.Classify(c.Path); m == nil || m.Tier != rules.TierA {
			fmt.Printf("  skip %s: evidence changed since the scan\n", c.Path)
			continue
		}
		dest, err := trash.Put(c.Path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  fail %s: %v\n", c.Path, err)
			failed++
			continue
		}
		freed += c.Bytes
		fmt.Printf("  trashed %s (%s)\n", c.Path, human(c.Bytes))
		recs = append(recs, manifest.Record{
			RunID: runID, Action: manifest.ActionTrash, Time: time.Now(),
			Path: c.Path, TrashedTo: dest, Category: c.Match.Category,
			Tier: c.Match.Tier.String(), Evidence: c.Match.Evidence, Bytes: c.Bytes,
		})
	}
	if len(recs) > 0 {
		if err := manifest.Append(mpath, recs); err != nil {
			fmt.Fprintf(os.Stderr, "buzzard: manifest write failed: %v\n", err)
			return 1
		}
	}
	fmt.Printf("\nmoved %d item(s) to the trash, %s reclaimable on empty.\n", len(recs), human(freed))
	fmt.Printf("manifest: %s -- undo with: buzzard -restore\n", mpath)
	if failed > 0 {
		return 1
	}
	return 0
}

// runRestore moves everything from the most recent clean back to where it
// came from, appending restore records to the manifest.
func runRestore(mpath string) int {
	recs, err := manifest.LastTrashRun(mpath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "buzzard: %v\n", err)
		return 1
	}
	if len(recs) == 0 {
		fmt.Println("nothing to restore.")
		return 0
	}
	runID := time.Now().Format("20060102-150405")
	var undo []manifest.Record
	failed := 0
	for _, r := range recs {
		if err := trash.Restore(r.TrashedTo, r.Path); err != nil {
			fmt.Fprintf(os.Stderr, "  fail %s: %v\n", r.Path, err)
			failed++
			continue
		}
		fmt.Printf("  restored %s\n", r.Path)
		undo = append(undo, manifest.Record{
			RunID: runID, Action: manifest.ActionRestore, Time: time.Now(),
			Path: r.Path, TrashedTo: r.TrashedTo,
		})
	}
	if len(undo) > 0 {
		if err := manifest.Append(mpath, undo); err != nil {
			fmt.Fprintf(os.Stderr, "buzzard: manifest write failed: %v\n", err)
			return 1
		}
	}
	fmt.Printf("restored %d of %d item(s).\n", len(undo), len(recs))
	if failed > 0 {
		return 1
	}
	return 0
}

// usage prints command help.
func usage() {
	fmt.Fprintf(os.Stderr, `buzzard %s — finds disk space that is safe to reclaim

Usage: buzzard [flags] [dir]

Scans dir (default: current directory) and reports reclaimable space graded
by evidence. By default buzzard deletes nothing; -clean moves tier A
candidates to the OS trash behind a confirmation and a manifest records
every move so -restore can undo the last clean.

Flags:
`, version)
	flag.PrintDefaults()
}

// report prints candidates grouped by tier, largest first, with the evidence
// and regeneration path for each. The dry-run footer is suppressed when a
// clean is about to run.
func report(res *scan.Result, footer bool) {
	sort.Slice(res.Candidates, func(i, j int) bool {
		return res.Candidates[i].Bytes > res.Candidates[j].Bytes
	})
	var tierA, tierB []scan.Candidate
	var reclaimA, reclaimB int64
	for _, c := range res.Candidates {
		if c.Match.Tier == rules.TierA {
			tierA = append(tierA, c)
			reclaimA += c.Bytes
		} else {
			tierB = append(tierB, c)
			reclaimB += c.Bytes
		}
	}
	fmt.Printf("buzzard scanned %s: %s on disk\n\n", res.Root, human(res.TotalBytes))
	printTier("TIER A — regenerable by contract", tierA)
	printTier("TIER B — probably disposable, review each", tierB)
	fmt.Printf("reclaimable: %s by contract (tier A), %s more after review (tier B)\n",
		human(reclaimA), human(reclaimB))
	if res.Errors > 0 {
		fmt.Printf("(%d entries unreadable and skipped)\n", res.Errors)
	}
	if footer {
		fmt.Println("\nnothing was deleted. buzzard only circles what is already dead.")
	}
}

// printTier prints one tier section, or nothing if the tier is empty.
func printTier(title string, cs []scan.Candidate) {
	if len(cs) == 0 {
		return
	}
	fmt.Println(title)
	for _, c := range cs {
		fmt.Printf("  %9s  %-28s %s\n", human(c.Bytes), c.Match.Category, c.Path)
		fmt.Printf("             idle %-22s regen: %s\n", idle(c.NewestMod), c.Match.Regen)
		fmt.Printf("             why: %s\n", c.Match.Evidence)
	}
	fmt.Println()
}

// idle renders how long ago a subtree was last modified.
func idle(t time.Time) string {
	if t.IsZero() {
		return "(empty)"
	}
	d := time.Since(t)
	switch {
	case d > 365*24*time.Hour:
		return fmt.Sprintf("%.1fy", d.Hours()/(365*24))
	case d > 30*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(30*24)))
	case d > 24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return "<1d"
	}
}

// human renders a byte count with binary prefixes.
func human(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
