// Package scan walks a filesystem tree, measures true on-disk usage, and
// collects reclaim candidates classified by the rules package. Sizing uses
// allocated blocks rather than apparent size so sparse files and dataless
// cloud placeholders report what deletion would actually free, and hardlinked
// files are counted once per inode. Per-file work stats through stack-held
// syscall buffers rather than os.FileInfo to keep allocations off the hot
// path.
package scan

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
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
	rules *rules.Ruleset

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
	entries, err := os.ReadDir(dir)
	if err != nil {
		atomic.AddInt64(&s.errs, 1)
		return
	}
	for _, e := range entries {
		path := join(dir, e.Name())
		if e.IsDir() {
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
			continue
		}
		var st syscall.Stat_t
		if syscall.Lstat(path, &st) != nil {
			atomic.AddInt64(&s.errs, 1)
			continue
		}
		atomic.AddInt64(&s.total, s.dedupBytes(&st))
	}
}

// sizeSubtree measures the on-disk bytes under dir and tracks the newest
// modification time (unix seconds) seen, sharing the scan-wide hardlink
// dedup so a subtree total never double counts an inode seen elsewhere.
func (s *Scanner) sizeSubtree(dir string, newestSec *int64) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		atomic.AddInt64(&s.errs, 1)
		return 0
	}
	var bytes int64
	for _, e := range entries {
		path := join(dir, e.Name())
		if e.IsDir() {
			bytes += s.sizeSubtree(path, newestSec)
			continue
		}
		var st syscall.Stat_t
		if syscall.Lstat(path, &st) != nil {
			atomic.AddInt64(&s.errs, 1)
			continue
		}
		bytes += s.dedupBytes(&st)
		if sec := mtimeSec(&st); sec > *newestSec {
			*newestSec = sec
		}
	}
	return bytes
}

// dedupBytes returns the allocated size recorded in st, counting
// multiply-linked inodes only once across the entire scan.
func (s *Scanner) dedupBytes(st *syscall.Stat_t) int64 {
	if st.Nlink > 1 {
		key := inode{dev: uint64(st.Dev), ino: uint64(st.Ino)}
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
	return st.Blocks * 512
}
