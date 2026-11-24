//go:build !unix

package main

import "errors"

// freeBytes has no portable implementation outside unix. The doctor reports
// that as a failed check with a clear reason rather than pretending the disk
// has space.
func freeBytes(string) (uint64, error) {
	return 0, errors.New("free-space checks are not supported on this platform")
}
