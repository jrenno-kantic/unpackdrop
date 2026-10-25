package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// availableBytes is a variable so tests can simulate a full disk.
var availableBytes = diskAvailable

// withMargin adds headroom for filesystem overhead and estimation error.
func withMargin(n int64) int64 { return n + n/20 + 32<<20 }

// requireSpace fails when the filesystem holding dir cannot fit need bytes
// (plus margin). The check is skipped when free space cannot be determined.
func requireSpace(dir string, need int64, what string) error {
	avail, ok := availableBytes(existingParent(dir))
	if !ok || avail >= withMargin(need) {
		return nil
	}
	return fmt.Errorf("not enough disk space in %s for %s: need about %s, %s available",
		existingParent(dir), what, formatBytes(withMargin(need)), formatBytes(avail))
}

// existingParent returns dir or its closest existing ancestor.
func existingParent(dir string) string {
	for {
		if _, err := os.Stat(dir); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		dir = parent
	}
}
