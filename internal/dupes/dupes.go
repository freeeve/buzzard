// Package dupes finds files with identical content. Candidates bucket by
// size first, so hashing only touches files that could possibly collide,
// and hardlinked paths to the same inode are recognized as one file rather
// than reported as a duplicate of themselves.
package dupes

import (
	"crypto/sha256"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// Group is a set of paths whose contents are byte-identical.
type Group struct {
	Size  int64
	Paths []string
}

// Wasted returns the bytes reclaimable by deleting all but one copy.
func (g *Group) Wasted() int64 {
	return g.Size * int64(len(g.Paths)-1)
}

type candidate struct {
	path string
	dev  uint64
	ino  uint64
}

// Find walks root and returns groups of identical files of at least
// minSize bytes, ordered by wasted bytes descending. Symlinks are ignored
// and unreadable files are skipped.
func Find(root string, minSize int64) ([]Group, error) {
	bySize := make(map[int64][]candidate)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() < minSize {
			return nil
		}
		c := candidate{path: path}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			c.dev, c.ino = uint64(st.Dev), uint64(st.Ino)
		}
		bySize[info.Size()] = append(bySize[info.Size()], c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	var groups []Group
	for size, cands := range bySize {
		if len(cands) < 2 {
			continue
		}
		byHash := make(map[[sha256.Size]byte][]string)
		seen := make(map[[2]uint64]bool)
		for _, c := range cands {
			key := [2]uint64{c.dev, c.ino}
			if c.ino != 0 && seen[key] {
				continue
			}
			seen[key] = true
			sum, err := hashFile(c.path)
			if err != nil {
				continue
			}
			byHash[sum] = append(byHash[sum], c.path)
		}
		for _, paths := range byHash {
			if len(paths) < 2 {
				continue
			}
			sort.Strings(paths)
			groups = append(groups, Group{Size: size, Paths: paths})
		}
	}
	sort.Slice(groups, func(i, j int) bool {
		if w1, w2 := groups[i].Wasted(), groups[j].Wasted(); w1 != w2 {
			return w1 > w2
		}
		return groups[i].Paths[0] < groups[j].Paths[0]
	})
	return groups, nil
}

// hashFile returns the sha256 of a file's contents.
func hashFile(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}
