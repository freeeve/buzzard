//go:build darwin && !cgo

package trash

import (
	"os"
	"path/filepath"
)

// Put moves path into ~/.Trash by rename. Without cgo there is no
// NSFileManager, so Finder shows the item but offers no put-back; buzzard's
// own manifest still records where it came from.
func Put(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dest := uniqueDest(filepath.Join(home, ".Trash"), filepath.Base(path))
	if err := os.Rename(path, dest); err != nil {
		return "", err
	}
	return dest, nil
}
