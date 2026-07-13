//go:build unix

package daemon

import (
	"os"
	"syscall"
)

// sysIdentity extracts device+inode from a unix stat view. os.FileInfo.Sys()
// returns *syscall.Stat_t on unix (Linux, Darwin, the BSDs), which carries the
// device and inode numbers that uniquely identify a file across a rename-over
// install (D-A). Fields are widened to uint64 so the comparison is platform-width
// independent (Dev/Ino are int32 on Darwin, uint64 on Linux).
func sysIdentity(info os.FileInfo) (dev, ino uint64, ok bool) {
	st, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	return uint64(st.Dev), uint64(st.Ino), true
}
