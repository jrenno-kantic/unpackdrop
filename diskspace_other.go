//go:build !darwin && !linux

package main

// diskAvailable is not implemented on this platform; space checks are skipped.
func diskAvailable(string) (int64, bool) { return 0, false }
