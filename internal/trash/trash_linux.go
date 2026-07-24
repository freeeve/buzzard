//go:build linux

package trash

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// Put moves path into the user's trash per the freedesktop.org trash
// specification: the item lands in $XDG_DATA_HOME/Trash/files and a
// .trashinfo record preserves the original path and deletion time so
// desktop trash tools can restore it.
func Put(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	filesDir := filepath.Join(dataHome, "Trash", "files")
	infoDir := filepath.Join(dataHome, "Trash", "info")
	for _, d := range []string{filesDir, infoDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
	}
	dest := uniqueTrashName(filesDir, infoDir, filepath.Base(abs))
	name := filepath.Base(dest)
	info := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		(&url.URL{Path: abs}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
	infoPath := filepath.Join(infoDir, name+".trashinfo")
	if err := os.WriteFile(infoPath, []byte(info), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(abs, dest); err != nil {
		os.Remove(infoPath)
		return "", err
	}
	return dest, nil
}

// uniqueTrashName picks a name free in both Trash/files and Trash/info, as
// the spec requires the pair to share a basename.
func uniqueTrashName(filesDir, infoDir, base string) string {
	name := base
	for i := 2; ; i++ {
		full := filepath.Join(filesDir, name)
		info := filepath.Join(infoDir, name+".trashinfo")
		_, errF := os.Lstat(full)
		_, errI := os.Lstat(info)
		if errF != nil && errI != nil {
			return full
		}
		name = fmt.Sprintf("%s %d", base, i)
	}
}
