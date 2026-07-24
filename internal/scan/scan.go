// Package scan walks a filesystem tree, measures true on-disk usage, and
// collects reclaim candidates classified by the rules package. Sizing uses
// allocated blocks rather than apparent size so sparse files and dataless
// cloud placeholders report what deletion would actually free, and hardlinked
// files are counted once per inode.
package scan

import (
	"io/fs"
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
	res := &Result{
		Root:       root,
		TotalBytes: atomic.LoadInt64(&s.total),
		Candidates: s.found,
		Errors:     atomic.LoadInt64(&s.errs),
	}
	return res
}

// walkDir processes one directory, spawning bounded goroutines for child
// directories when a semaphore slot is free and recursing inline otherwise.
func (s *Scanner) walkDir(dir string, isRoot bool) {
	defer s.wg.Done()
	if !isRoot {
		if m := s.rules.Classify(dir); m != nil {
			bytes, newest := s.sizeSubtree(dir)
			atomic.AddInt64(&s.total, bytes)
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
		path := filepath.Join(dir, e.Name())
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
		atomic.AddInt64(&s.total, s.fileBytes(e))
	}
}

// sizeSubtree measures the on-disk bytes and newest modification time under
// dir, sharing the scan-wide hardlink dedup so a subtree total never double
// counts an inode already seen elsewhere.
func (s *Scanner) sizeSubtree(dir string) (int64, time.Time) {
	var bytes int64
	var newest time.Time
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			atomic.AddInt64(&s.errs, 1)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		bytes += s.fileBytes(d)
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return bytes, newest
}

// fileBytes returns the allocated size of a non-directory entry, counting
// multiply-linked inodes only once across the entire scan. Symlinks are
// never followed; the link itself is measured.
func (s *Scanner) fileBytes(d fs.DirEntry) int64 {
	info, err := d.Info()
	if err != nil {
		atomic.AddInt64(&s.errs, 1)
		return 0
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size()
	}
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
