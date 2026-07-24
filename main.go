// Command buzzard scans a directory tree and reports disk space that is safe
// to reclaim, graded by evidence. Like its namesake it only circles what is
// already dead: this version deletes nothing and prints regeneration commands
// alongside every candidate it names.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/freeeve/buzzard/internal/rules"
	"github.com/freeeve/buzzard/internal/scan"
)

const version = "0.1.3"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Usage = usage
	flag.Parse()
	if *showVersion {
		fmt.Println("buzzard " + version)
		return
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
	res := scan.New(rules.Default(home)).Run(root)
	report(res)
}

// usage prints command help.
func usage() {
	fmt.Fprintf(os.Stderr, `buzzard %s — finds disk space that is safe to reclaim

Usage: buzzard [flags] [dir]

Scans dir (default: current directory) and reports reclaimable space graded
by evidence. Buzzard only circles what is already dead: it deletes nothing.

Flags:
`, version)
	flag.PrintDefaults()
}

// report prints candidates grouped by tier, largest first, with the evidence
// and regeneration path for each.
func report(res *scan.Result) {
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
	fmt.Println("\nnothing was deleted. buzzard only circles what is already dead.")
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
