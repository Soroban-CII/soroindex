//go:build unix

package main

import "syscall"

// freeBytes reports the space available to an unprivileged writer on the
// filesystem holding path. Bavail, not Bfree, is what a new transaction can
// actually use, which is the number that matters before a sync.
func freeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
