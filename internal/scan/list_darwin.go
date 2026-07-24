//go:build darwin

package scan

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// getattrlistbulk trap number (bsd/kern/syscalls.master); the syscall
// package's frozen tables predate the call.
const sysGetattrlistbulk = 461

// Attribute request bits and object types from bsd/sys/attr.h and
// bsd/sys/vnode.h. Within a group, returned attributes are packed in
// ascending bit order.
const (
	attrBitMapCount   = 5
	attrCmnReturned   = 0x80000000
	attrCmnName       = 0x00000001
	attrCmnDevID      = 0x00000002
	attrCmnObjType    = 0x00000008
	attrCmnModTime    = 0x00000400
	attrCmnFileID     = 0x02000000
	attrFileLinkCount = 0x00000001
	attrFileAllocSize = 0x00000004
	attrDirAllocSize  = 0x00000008
	objTypeDir        = 2
)

// attrlist mirrors struct attrlist from bsd/sys/attr.h.
type attrlist struct {
	bitmapcount uint16
	reserved    uint16
	commonattr  uint32
	volattr     uint32
	dirattr     uint32
	fileattr    uint32
	forkattr    uint32
}

// attrBufPool recycles the per-directory attribute buffers so steady-state
// listing allocates nothing per call.
var attrBufPool = sync.Pool{New: func() any {
	b := make([]byte, 128<<10)
	return &b
}}

// listDir lists one directory with getattrlistbulk, which returns name,
// object type, inode, link count, allocated size, and mtime for many
// entries per syscall: no per-file lstat, no per-file path, no per-file
// name allocation. Filesystems that reject the call fall back to the
// portable path for that directory.
func (s *Scanner) listDir(dir string, emit func(*entryStat)) error {
	if s.useGeneric {
		return s.listGeneric(dir, emit)
	}
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	al := attrlist{
		bitmapcount: attrBitMapCount,
		commonattr:  attrCmnReturned | attrCmnName | attrCmnDevID | attrCmnObjType | attrCmnModTime | attrCmnFileID,
		dirattr:     attrDirAllocSize,
		fileattr:    attrFileLinkCount | attrFileAllocSize,
	}
	bufp := attrBufPool.Get().(*[]byte)
	defer attrBufPool.Put(bufp)
	buf := *bufp
	emitted := false
	for {
		n, _, errno := syscall.Syscall6(sysGetattrlistbulk, uintptr(fd),
			uintptr(unsafe.Pointer(&al)), uintptr(unsafe.Pointer(&buf[0])),
			uintptr(len(buf)), 0, 0)
		if errno != 0 {
			if emitted {
				// Mid-listing failure: counting the directory partial
				// beats double-emitting entries through the fallback.
				atomic.AddInt64(&s.errs, 1)
				return nil
			}
			return s.listGeneric(dir, emit)
		}
		if n == 0 {
			return nil
		}
		s.parseBulk(dir, buf, int(n), emit)
		emitted = true
	}
}

// parseBulk walks one getattrlistbulk result buffer holding count records.
// Records the filesystem returned without the needed attributes are
// re-statted individually by name; malformed records end the batch and are
// counted as an error.
func (s *Scanner) parseBulk(dir string, buf []byte, count int, emit func(*entryStat)) {
	off := 0
	var e entryStat
	for i := 0; i < count; i++ {
		if off+24 > len(buf) {
			atomic.AddInt64(&s.errs, 1)
			return
		}
		recLen := int(binary.LittleEndian.Uint32(buf[off:]))
		if recLen < 24 || off+recLen > len(buf) {
			atomic.AddInt64(&s.errs, 1)
			return
		}
		rec := buf[off : off+recLen]
		off += recLen
		// attribute_set_t{common, vol, dir, file, fork} follows the length.
		retCmn := binary.LittleEndian.Uint32(rec[4:])
		retDir := binary.LittleEndian.Uint32(rec[12:])
		retFile := binary.LittleEndian.Uint32(rec[16:])
		p := 24
		e = entryStat{}
		var nameOff, nameLen int
		if retCmn&attrCmnName != 0 && p+8 <= recLen {
			dataOff := int(int32(binary.LittleEndian.Uint32(rec[p:])))
			nameOff, nameLen = p+dataOff, int(binary.LittleEndian.Uint32(rec[p+4:]))
			p += 8
		}
		if retCmn&attrCmnDevID != 0 && p+4 <= recLen {
			e.dev = uint64(binary.LittleEndian.Uint32(rec[p:]))
			p += 4
		}
		var objType uint32
		if retCmn&attrCmnObjType != 0 && p+4 <= recLen {
			objType = binary.LittleEndian.Uint32(rec[p:])
			p += 4
		}
		if retCmn&attrCmnModTime != 0 && p+16 <= recLen {
			e.mtimeSec = int64(binary.LittleEndian.Uint64(rec[p:]))
			p += 16
		}
		if retCmn&attrCmnFileID != 0 && p+8 <= recLen {
			e.ino = binary.LittleEndian.Uint64(rec[p:])
			p += 8
		}
		name := ""
		if nameLen > 1 && nameOff > 0 && nameOff+nameLen <= recLen {
			name = string(rec[nameOff : nameOff+nameLen-1])
		}
		if objType == objTypeDir {
			if name == "" {
				atomic.AddInt64(&s.errs, 1)
				continue
			}
			e.name, e.isDir = name, true
			if retDir&attrDirAllocSize != 0 && p+8 <= recLen {
				e.bytes = int64(binary.LittleEndian.Uint64(rec[p:]))
			} else {
				// Directory alloc size withheld: stat the directory itself.
				var st syscall.Stat_t
				if syscall.Lstat(join(dir, name), &st) == nil {
					e.bytes = st.Blocks * 512
				} else {
					atomic.AddInt64(&s.errs, 1)
				}
			}
			emit(&e)
			continue
		}
		hasLink := retFile&attrFileLinkCount != 0
		hasSize := retFile&attrFileAllocSize != 0
		if hasLink && p+4 <= recLen {
			e.nlink = binary.LittleEndian.Uint32(rec[p:])
			p += 4
		}
		if hasSize && p+8 <= recLen {
			e.bytes = int64(binary.LittleEndian.Uint64(rec[p:]))
			p += 8
		}
		if !hasLink || !hasSize {
			// Attributes withheld (unusual filesystem): stat this one entry.
			if name == "" {
				atomic.AddInt64(&s.errs, 1)
				continue
			}
			var st syscall.Stat_t
			if syscall.Lstat(join(dir, name), &st) != nil {
				atomic.AddInt64(&s.errs, 1)
				continue
			}
			e.dev, e.ino = uint64(st.Dev), uint64(st.Ino)
			e.nlink, e.bytes = uint32(st.Nlink), st.Blocks*512
			e.mtimeSec = mtimeSec(&st)
		}
		emit(&e)
	}
}
