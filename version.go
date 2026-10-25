package main

import (
	_ "embed"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

//go:embed VERSION
var embeddedVersion string

// version overrides the VERSION file when set at build time:
//
//	go build -ldflags "-X main.version=1.2.3"
var version string

// versionString returns the version, plus the VCS revision when the binary
// was built from a git checkout.
func versionString() string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = strings.TrimSpace(embeddedVersion)
	}
	if v == "" {
		v = "dev"
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		var rev string
		var dirty bool
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			if dirty {
				rev += "-dirty"
			}
			v += " (" + rev + ")"
		}
	}
	return fmt.Sprintf("%s %s %s/%s", v, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}
