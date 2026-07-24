// Command buzzard scans a directory tree and reports disk space that is safe
// to reclaim, graded by evidence. Like its namesake it only circles what is
// already dead: by default it deletes nothing, and when asked to clean it
// moves tier A candidates to the OS trash behind a confirmation, recording
// every move in a manifest that -restore can undo.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/freeeve/buzzard/internal/dupes"
	"github.com/freeeve/buzzard/internal/format"
	"github.com/freeeve/buzzard/internal/manifest"
	"github.com/freeeve/buzzard/internal/rules"
	"github.com/freeeve/buzzard/internal/scan"
	"github.com/freeeve/buzzard/internal/trash"
	"github.com/freeeve/buzzard/internal/tui"
	"github.com/freeeve/buzzard/internal/veto"
)

// activeWindow is how recently a candidate's subtree must have been
// modified to be considered in active use.
const activeWindow = 15 * time.Minute

const version = "0.6.3"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	clean := flag.Bool("clean", false, "after reporting, move tier A candidates to the OS trash (asks first)")
	yes := flag.Bool("yes", false, "skip the confirmation prompt for -clean")
	restore := flag.Bool("restore", false, "restore everything from the most recent clean and exit")
	manifestPath := flag.String("manifest", "", "manifest location (default ~/.buzzard/manifest.jsonl)")
	rulePacks := flag.String("rules", "", "comma-separated extra rule pack files (also loads ~/.buzzard/rules.d/*.json)")
	findDupes := flag.Bool("dupes", false, "also report duplicate files (identical content, >= 1 MiB)")
	interactive := flag.Bool("i", false, "interactive mode: browse, mark, and clean candidates")
	minSize := flag.String("min-size", "1M", "hide candidates smaller than this (e.g. 500K, 1M, 0)")
	showAll := flag.Bool("all", false, "show candidates of any size (same as -min-size 0)")
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
	floor, err := format.ParseSize(*minSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "buzzard: -min-size: %v\n", err)
		os.Exit(1)
	}
	if *showAll {
		floor = 0
	}
	res := scan.New(rs).Run(root)
	sortByScore(res)
	hiddenCount, hiddenBytes := applyFloor(res, floor)
	if *interactive {
		err := tui.Run(res, func(picks []scan.Candidate) tui.Stats {
			var log []string
			trashed, freed, failed, skipped := executeClean(picks, rs, mpath, func(s string) {
				log = append(log, s)
			})
			return tui.Stats{Trashed: trashed, Freed: freed, Failed: failed, Skipped: skipped, Log: log}
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "buzzard: %v\n", err)
			os.Exit(1)
		}
		return
	}
	report(res, !*clean && !*findDupes)
	if hiddenCount > 0 {
		fmt.Printf("(+ %d smaller candidates under %s, %s total -- show with -all)\n",
			hiddenCount, format.Human(floor), format.Human(hiddenBytes))
	}
	if *findDupes {
		reportDupes(root)
	}
	if *clean {
		os.Exit(runClean(res, rs, mpath, *yes))
	}
}

// applyFloor drops candidates smaller than floor from the result so every
// output mode and the clean picks agree on what is visible, returning what
// was hidden for the rollup line.
func applyFloor(res *scan.Result, floor int64) (count int, bytes int64) {
	if floor <= 0 {
		return 0, 0
	}
	kept := res.Candidates[:0]
	for _, c := range res.Candidates {
		if c.Bytes >= floor {
			kept = append(kept, c)
			continue
		}
		count++
		bytes += c.Bytes
	}
	res.Candidates = kept
	return count, bytes
}

// sortByScore orders candidates by reclaim value for both output modes.
func sortByScore(res *scan.Result) {
	sort.Slice(res.Candidates, func(i, j int) bool {
		return score(res.Candidates[i]) > score(res.Candidates[j])
	})
}

// executeClean re-verifies, vetoes, trashes, and records the picked
// candidates, narrating each step through log. It is the single deletion
// path shared by the CLI and the TUI.
func executeClean(picks []scan.Candidate, rs *rules.Ruleset, mpath string, log func(string)) (trashed int, freed int64, failed, skipped int) {
	runID := time.Now().Format("20060102-150405")
	var recs []manifest.Record
	for _, c := range picks {
		if m := rs.Classify(c.Path); m == nil || m.Tier != c.Match.Tier {
			log(fmt.Sprintf("  skip %s: evidence changed since the scan", c.Path))
			skipped++
			continue
		}
		if v := veto.Recent(c.NewestMod, activeWindow); v != nil {
			log(fmt.Sprintf("  skip %s: in use (%s)", c.Path, v.Reason))
			skipped++
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		v := veto.OpenHandles(ctx, c.Path)
		cancel()
		if v != nil {
			log(fmt.Sprintf("  skip %s: in use (%s)", c.Path, v.Reason))
			skipped++
			continue
		}
		dest, err := trash.Put(c.Path)
		if err != nil {
			log(fmt.Sprintf("  fail %s: %v", c.Path, err))
			failed++
			continue
		}
		trashed++
		freed += c.Bytes
		log(fmt.Sprintf("  trashed %s (%s)", c.Path, format.Human(c.Bytes)))
		recs = append(recs, manifest.Record{
			RunID: runID, Action: manifest.ActionTrash, Time: time.Now(),
			Path: c.Path, TrashedTo: dest, Category: c.Match.Category,
			Tier: c.Match.Tier.String(), Evidence: c.Match.Evidence, Bytes: c.Bytes,
		})
	}
	if len(recs) > 0 {
		if err := manifest.Append(mpath, recs); err != nil {
			log(fmt.Sprintf("manifest write failed: %v", err))
			failed++
		}
	}
	return trashed, freed, failed, skipped
}

// reportDupes prints the largest groups of byte-identical files under root.
func reportDupes(root string) {
	const minSize = 1 << 20
	const topN = 20
	groups, err := dupes.Find(root, minSize)
	if err != nil {
		fmt.Fprintf(os.Stderr, "buzzard: dupes: %v\n", err)
		return
	}
	if len(groups) == 0 {
		fmt.Println("DUPLICATES: none at 1 MiB or larger.")
		return
	}
	var wasted int64
	for _, g := range groups {
		wasted += g.Wasted()
	}
	fmt.Printf("DUPLICATES — identical content, largest waste first (top %d of %d groups)\n", min(topN, len(groups)), len(groups))
	for i, g := range groups {
		if i == topN {
			break
		}
		fmt.Printf("  %9s wasted  %d copies of %s\n", format.Human(g.Wasted()), len(g.Paths), format.Human(g.Size))
		for _, p := range g.Paths {
			fmt.Printf("             %s\n", p)
		}
	}
	fmt.Printf("total duplicate waste: %s (keep one copy of each)\n\n", format.Human(wasted))
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
		fmt.Printf("trash %d tier A item(s), reclaiming %s? [y/N] ", len(picks), format.Human(total))
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if answer := strings.ToLower(strings.TrimSpace(line)); answer != "y" && answer != "yes" {
			fmt.Println("aborted; nothing was deleted.")
			return 0
		}
	}
	trashed, freed, failed, _ := executeClean(picks, rs, mpath, func(s string) { fmt.Println(s) })
	fmt.Printf("\nmoved %d item(s) to the trash, %s reclaimable on empty.\n", trashed, format.Human(freed))
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
	fmt.Printf("buzzard scanned %s: %s on disk\n\n", res.Root, format.Human(res.TotalBytes))
	printBreakdown(res.Tree)
	printTier("TIER A — regenerable by contract", tierA)
	printTier("TIER B — probably disposable, review each", tierB)
	fmt.Printf("reclaimable: %s by contract (tier A), %s more after review (tier B)\n",
		format.Human(reclaimA), format.Human(reclaimB))
	if res.Errors > 0 {
		fmt.Printf("(%d entries unreadable and skipped)\n", res.Errors)
	}
	if footer {
		fmt.Println("\nnothing was deleted. buzzard only circles what is already dead.")
	}
}

// breakdownTop is how many entries the breakdown lists before folding the
// rest into a rollup line. A tail of exactly one is shown rather than
// rolled up, since "(1 more)" hides nothing and costs a line either way.
const breakdownTop = 10

// row is one line of the storage breakdown.
type row struct {
	name        string
	bytes       int64
	reclaimable int64
}

// breakdownRows turns a scanned tree into the lines of the breakdown:
// one per immediate child, plus an entry for the root's own loose files,
// ordered largest first. Ties break on name so the report does not inherit
// the filesystem's unstable listing order. The rows always account for the
// full subtree -- callers roll up the tail rather than dropping it.
func breakdownRows(tree *scan.Node) []row {
	if tree == nil {
		return nil
	}
	rows := make([]row, 0, len(tree.Children)+1)
	for _, c := range tree.Children {
		rows = append(rows, row{name: c.Name + "/", bytes: c.Bytes, reclaimable: c.Reclaimable})
	}
	if tree.Own > 0 {
		rows = append(rows, row{name: "(files here)", bytes: tree.Own})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].bytes != rows[j].bytes {
			return rows[i].bytes > rows[j].bytes
		}
		return rows[i].name < rows[j].name
	})
	return rows
}

// printBreakdown accounts for where the scanned bytes went. Every byte the
// scan counted appears on some line, so the section reads as an account
// rather than a highlight reel; the reclaim column is the part no other
// disk tool can fill in. Reclaimable totals ignore -min-size, which hides
// small candidates from the listing without changing what is actually
// reclaimable.
func printBreakdown(tree *scan.Node) {
	rows := breakdownRows(tree)
	if len(rows) < 2 {
		return
	}
	shown := rows
	var tail []row
	if len(rows) > breakdownTop+1 {
		shown, tail = rows[:breakdownTop], rows[breakdownTop:]
	}
	fmt.Println("WHERE IT WENT")
	for _, r := range shown {
		printBreakdownRow(r)
	}
	if len(tail) > 0 {
		var agg row
		agg.name = fmt.Sprintf("(%d more)", len(tail))
		for _, r := range tail {
			agg.bytes += r.bytes
			agg.reclaimable += r.reclaimable
		}
		printBreakdownRow(agg)
	}
	fmt.Println()
}

// printBreakdownRow renders one breakdown line, leaving the reclaim column
// as a dash when a rule claimed nothing beneath it.
func printBreakdownRow(r row) {
	if r.reclaimable == 0 {
		fmt.Printf("  %9s  %-28s %9s\n", format.Human(r.bytes), r.name, "--")
		return
	}
	fmt.Printf("  %9s  %-28s %9s reclaimable\n",
		format.Human(r.bytes), r.name, format.Human(r.reclaimable))
}

// printTier prints one tier section, or nothing if the tier is empty.
func printTier(title string, cs []scan.Candidate) {
	if len(cs) == 0 {
		return
	}
	fmt.Println(title)
	for _, c := range cs {
		active := ""
		if v := veto.Recent(c.NewestMod, activeWindow); v != nil {
			active = "  [in use: " + v.Reason + "]"
		}
		fmt.Printf("  %9s  %-28s %s%s\n", format.Human(c.Bytes), c.Match.Category, c.Path, active)
		fmt.Printf("             idle %-22s regen: %s\n", format.Idle(c.NewestMod), c.Match.Regen)
		fmt.Printf("             why: %s\n", c.Match.Evidence)
	}
	fmt.Println()
}

// score orders candidates by reclaim value: size weighted by how long the
// subtree has sat idle, so a smaller two-year-old cache can outrank a large
// dependency dir rebuilt this morning.
func score(c scan.Candidate) float64 {
	idleDays := 0.0
	if !c.NewestMod.IsZero() {
		idleDays = time.Since(c.NewestMod).Hours() / 24
		if idleDays < 0 {
			idleDays = 0
		}
	}
	return float64(c.Bytes) * (1 + math.Log2(1+idleDays))
}
