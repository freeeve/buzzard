//go:build linux

package scan

import "syscall"

// mtimeSec returns the modification time of st in unix seconds.
func mtimeSec(st *syscall.Stat_t) int64 {
	return st.Mtim.Sec
}
