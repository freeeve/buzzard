//go:build darwin

package scan

import "syscall"

// mtimeSec returns the modification time of st in unix seconds.
func mtimeSec(st *syscall.Stat_t) int64 {
	return st.Mtimespec.Sec
}
