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
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/freeeve/buzzard/internal/rules"
)

// Candidate is a directory a rule claimed, sized and dated for the report.
type Candidate struct {
	Path      string
	Match     *rules.Match
	Bytes     int64
	NewestMod time.Time
}

// Result summarizes a completed scan.
type Result struct {
	Root       string
	TotalBytes int64
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
		sem:   make(chan struct{}, runtime.NumCPU()*2),
		seen:  make(map[inode]struct{}),
	}
}

// Run scans root and returns the sized, classified result. Unreadable
// entries are counted as errors and skipped rather than aborting the scan.
func (s *Scanner) Run(root string) *Result {
	root = filepath.Clean(root)
	s.wg.Add(1)
	s.walkDir(root, true)
	s.wg.Wait()
	return &Result{
		Root:       root,
		TotalBytes: atomic.LoadInt64(&s.total),
		Candidates: s.found,
		Errors:     atomic.LoadInt64(&s.errs),
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
func (s *Scanner) walkDir(dir string, isRoot bool) {
	defer s.wg.Done()
	if !isRoot {
		if m := s.rules.Classify(dir); m != nil {
			var newestSec int64
			bytes := s.sizeSubtree(dir, &newestSec)
			atomic.AddInt64(&s.total, bytes)
			var newest time.Time
			if newestSec > 0 {
				newest = time.Unix(newestSec, 0)
			}
			s.mu.Lock()
			s.found = append(s.found, Candidate{Path: dir, Match: m, Bytes: bytes, NewestMod: newest})
			s.mu.Unlock()
			return
		}
	}
	err := s.listDir(dir, func(e *entryStat) {
		if !e.isDir {
			atomic.AddInt64(&s.total, s.dedupBytes(e))
			return
		}
		path := join(dir, e.name)
		s.wg.Add(1)
		select {
		case s.sem <- struct{}{}:
			go func() {
				defer func() { <-s.sem }()
				s.walkDir(path, false)
			}()
		default:
			s.walkDir(path, false)
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
			bytes += s.sizeSubtree(join(dir, e.name), newestSec)
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
