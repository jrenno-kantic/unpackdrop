//go:build darwin || linux

package main

import "syscall"

// diskAvailable returns the bytes available to unprivileged users on the
// filesystem holding dir.
func diskAvailable(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true //nolint:unconvert // field types differ per OS
}
