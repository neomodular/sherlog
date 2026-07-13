//go:build !unix

package daemon

import "os"

// sysIdentity reports that no device/inode view is available on this platform
// (notably Windows, where os.FileInfo.Sys() is not a *syscall.Stat_t). The watcher
// falls back to the portable mtime+size comparison, which still catches the
// rename-over and delete installs that matter (D-A: portable fallback).
func sysIdentity(info os.FileInfo) (dev, ino uint64, ok bool) {
	return 0, 0, false
}
