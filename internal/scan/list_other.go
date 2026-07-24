//go:build !darwin

package scan

// listDir lists one directory. Without a batched platform API this is the
// portable ReadDir+lstat path.
func (s *Scanner) listDir(dir string, emit func(*entryStat)) error {
	return s.listGeneric(dir, emit)
}
