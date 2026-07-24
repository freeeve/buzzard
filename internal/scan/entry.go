package scan

import (
	"os"
	"sync/atomic"
	"syscall"
)

// entryStat carries the stat facts the scanner needs for one directory
// entry. Bulk listings leave name empty for non-directories, since file
// paths are never materialized on that path.
type entryStat struct {
	name     string
	isDir    bool
	dev      uint64
	ino      uint64
	nlink    uint32
	bytes    int64
	mtimeSec int64
}

// listGeneric lists dir portably: one ReadDir plus one lstat per file.
// It is the only listing path on non-darwin platforms and the fallback for
// filesystems that reject bulk attribute listing. Per-entry stat failures
// are counted and skipped; only an unreadable directory returns an error.
func (s *Scanner) listGeneric(dir string, emit func(*entryStat)) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var e entryStat
	for _, d := range entries {
		var st syscall.Stat_t
		if syscall.Lstat(join(dir, d.Name()), &st) != nil {
			atomic.AddInt64(&s.errs, 1)
			continue
		}
		if d.IsDir() {
			e = entryStat{name: d.Name(), isDir: true, bytes: st.Blocks * 512}
			emit(&e)
			continue
		}
		e = entryStat{
			name:     d.Name(),
			dev:      uint64(st.Dev),
			ino:      uint64(st.Ino),
			nlink:    uint32(st.Nlink),
			bytes:    st.Blocks * 512,
			mtimeSec: mtimeSec(&st),
		}
		emit(&e)
	}
	return nil
}
