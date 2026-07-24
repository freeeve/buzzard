// Package scan walks a filesystem tree, measures true on-disk usage, and
// collects reclaim candidates classified by the rules package. Sizing uses
// allocated blocks rather than apparent size so sparse files and dataless
// cloud placeholders report what deletion would actually free, and hardlinked
// files are counted once per inode. Directory listing goes through a
// platform layer: batched attribute listing on darwin, ReadDir plus
// stack-buffer lstat elsewhere, so per-file work stays off the heap.
package scan

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/freeeve/buzzard/internal/rules"
)

// defaultWalkers bounds concurrent directory walkers. The scan is
// syscall-bound, so the kernel, not the CPU count, sets the useful width:
// a gated sweep on SSD showed throughput peaking at 8 and flat-to-worse
// beyond it as goroutine churn grows.
const defaultWalkers = 8

// Candidate is a directory a rule claimed, sized and dated for the report.
type Candidate struct {
	Path      string
	Match     *rules.Match
	Bytes     int64
	NewestMod time.Time
}

// Node is one directory in the scanned tree, retained so the report can
// account for where space went rather than only what is reclaimable. Own
// covers the directory's own inode plus the files directly inside it;
// Bytes adds every child subtree. A node claimed by a rule is a leaf: the
// scan does not descend past a candidate.
type Node struct {
	Name        string
	Own         int64
	Bytes       int64
	Reclaimable int64
	Match       *rules.Match
	Children    []*Node
}

// Result summarizes a completed scan. Tree is the directory tree rooted at
// Root, whose Bytes equals TotalBytes.
type Result struct {
	Root       string
	TotalBytes int64
	Tree       *Node
	Candidates []Candidate
	Errors     int64
}

// Scanner walks a tree concurrently, deduplicating hardlinks across the
// whole scan and classifying directories as it descends.
type Scanner struct {
	rules      *rules.Ruleset
	useGeneric bool

	sem   chan struct{}
	wg    sync.WaitGroup
	total int64
	errs  int64

	mu    sync.Mutex
	seen  map[inode]struct{}
	found []Candidate
}

type inode struct {
	dev uint64
	ino uint64
}

// New returns a Scanner using the given ruleset.
func New(rs *rules.Ruleset) *Scanner {
	return &Scanner{
		rules: rs,
		sem:   make(chan struct{}, defaultWalkers),
		seen:  make(map[inode]struct{}),
	}
}

// Run scans root and returns the sized, classified result. Unreadable
// entries are counted as errors and skipped rather than aborting the scan.
func (s *Scanner) Run(root string) *Result {
	root = filepath.Clean(root)
	var rootBytes int64
	var st syscall.Stat_t
	if syscall.Lstat(root, &st) == nil {
		rootBytes = st.Blocks * 512
	} else {
		atomic.AddInt64(&s.errs, 1)
	}
	tree := &Node{Name: root, Own: rootBytes}
	s.wg.Add(1)
	s.walkDir(root, true, rootBytes, tree)
	s.wg.Wait()
	rollup(tree)
	return &Result{
		Root:       root,
		TotalBytes: atomic.LoadInt64(&s.total),
		Tree:       tree,
		Candidates: s.found,
		Errors:     atomic.LoadInt64(&s.errs),
	}
}

// rollup totals each subtree bottom-up once the walk has finished. It runs
// single-threaded after the wait, so no node needs synchronization: during
// the walk each node is written only by the goroutine that owns it.
func rollup(n *Node) {
	n.Bytes = n.Own
	for _, c := range n.Children {
		rollup(c)
		n.Bytes += c.Bytes
		n.Reclaimable += c.Reclaimable
	}
	if n.Match != nil {
		n.Reclaimable = n.Bytes
	}
}

// join concatenates a directory and entry name without the parse-and-clean
// work of filepath.Join; scan paths are built from already-clean parents.
func join(dir, name string) string {
	if len(dir) > 0 && dir[len(dir)-1] == filepath.Separator {
		return dir + name
	}
	return dir + string(filepath.Separator) + name
}

// walkDir processes one directory, spawning bounded goroutines for child
// directories when a semaphore slot is free and recursing inline otherwise.
// ownBytes is the directory's own allocated size, observed by whoever listed
// it, so a claimed candidate can charge its own inode to the reclaim total.
func (s *Scanner) walkDir(dir string, isRoot bool, ownBytes int64, node *Node) {
	defer s.wg.Done()
	if !isRoot {
		if m := s.rules.Classify(dir); m != nil {
			var newestSec int64
			bytes := ownBytes + s.sizeSubtree(dir, &newestSec)
			atomic.AddInt64(&s.total, bytes)
			var newest time.Time
			if newestSec > 0 {
				newest = time.Unix(newestSec, 0)
			}
			node.Own, node.Match = bytes, m
			s.mu.Lock()
			s.found = append(s.found, Candidate{Path: dir, Match: m, Bytes: bytes, NewestMod: newest})
			s.mu.Unlock()
			return
		}
	}
	atomic.AddInt64(&s.total, ownBytes)
	err := s.listDir(dir, func(e *entryStat) {
		if !e.isDir {
			b := s.dedupBytes(e)
			atomic.AddInt64(&s.total, b)
			node.Own += b
			return
		}
		path := join(dir, e.name)
		own := e.bytes
		// Children are appended here, serially in this directory's own
		// listing callback, so the slice needs no lock; each child
		// goroutine then writes only the node handed to it.
		child := &Node{Name: e.name, Own: own}
		node.Children = append(node.Children, child)
		s.wg.Add(1)
		select {
		case s.sem <- struct{}{}:
			go func() {
				defer func() { <-s.sem }()
				s.walkDir(path, false, own, child)
			}()
		default:
			s.walkDir(path, false, own, child)
		}
	})
	if err != nil {
		atomic.AddInt64(&s.errs, 1)
	}
}

// sizeSubtree measures the on-disk bytes under dir and tracks the newest
// modification time (unix seconds) seen, sharing the scan-wide hardlink
// dedup so a subtree total never double counts an inode seen elsewhere.
func (s *Scanner) sizeSubtree(dir string, newestSec *int64) int64 {
	var bytes int64
	err := s.listDir(dir, func(e *entryStat) {
		if e.isDir {
			bytes += e.bytes + s.sizeSubtree(join(dir, e.name), newestSec)
			return
		}
		bytes += s.dedupBytes(e)
		if e.mtimeSec > *newestSec {
			*newestSec = e.mtimeSec
		}
	})
	if err != nil {
		atomic.AddInt64(&s.errs, 1)
	}
	return bytes
}

// dedupBytes returns the allocated size recorded in e, counting
// multiply-linked inodes only once across the entire scan.
func (s *Scanner) dedupBytes(e *entryStat) int64 {
	if e.nlink > 1 {
		key := inode{dev: e.dev, ino: e.ino}
		s.mu.Lock()
		_, dup := s.seen[key]
		if !dup {
			s.seen[key] = struct{}{}
		}
		s.mu.Unlock()
		if dup {
			return 0
		}
	}
	return e.bytes
}
