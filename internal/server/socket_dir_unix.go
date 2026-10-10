//go:build unix

package server

import (
	"os"
	"syscall"
)

// fileOwner returns the owning uid of info.
func fileOwner(info os.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
