// Package trash moves files and directories into the operating system's
// trash instead of unlinking them, so every deletion buzzard performs can be
// undone. Each platform keeps its native semantics: NSFileManager with
// Finder put-back on macOS, the freedesktop.org trash specification on
// Linux, and a plain move into ~/.Trash when neither is available.
package trash

import (
	"fmt"
	"os"
	"path/filepath"
)

// Restore moves a previously trashed item back to its original path,
// recreating parent directories as needed.
func Restore(trashedPath, originalPath string) error {
	if _, err := os.Lstat(originalPath); err == nil {
		return fmt.Errorf("restore %s: original path already exists", originalPath)
	}
	if err := os.MkdirAll(filepath.Dir(originalPath), 0o755); err != nil {
		return err
	}
	return os.Rename(trashedPath, originalPath)
}

// uniqueDest returns a destination inside dir for base that does not
// collide with an existing entry, suffixing " 2", " 3", ... like Finder.
func uniqueDest(dir, base string) string {
	dest := filepath.Join(dir, base)
	for i := 2; ; i++ {
		if _, err := os.Lstat(dest); err != nil {
			return dest
		}
		dest = filepath.Join(dir, fmt.Sprintf("%s %d", base, i))
	}
}
