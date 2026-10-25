package main

import (
	"strings"
	"testing"
)

func TestRunFlags(t *testing.T) {
	tests := []struct {
		args []string
		want int
	}{
		{[]string{"-v"}, 0},
		{[]string{"--version"}, 0},
		{[]string{"-h"}, 0},
		{[]string{"--help"}, 0},
		{[]string{}, 2},
		{[]string{"--bogus"}, 2},
		{[]string{"-r", "tar", "x.rar"}, 2},
		{[]string{"--repack=tar", "x.rar"}, 2},
		{[]string{"-k", "x.rar"}, 2},
		{[]string{"--keep-dir", "x.rar"}, 2},
		{[]string{"-s", "x.rar"}, 2},
		{[]string{"--skip-verify", "x.rar"}, 2},
		{[]string{"-L", "max", "x.rar"}, 2},
		{[]string{"-r", "zip", "--level", "ultra", "x.rar"}, 2},
		// Flags after the positional argument are still parsed.
		{[]string{"x.rar", "-k"}, 2},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			if got := run(tc.args); got != tc.want {
				t.Fatalf("run(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

func TestVersionString(t *testing.T) {
	saved := version
	defer func() { version = saved }()

	version = ""
	if got, want := versionString(), strings.TrimSpace(embeddedVersion)+" "; !strings.HasPrefix(got, want) {
		t.Fatalf("versionString() = %q, want prefix %q", got, want)
	}
	version = "9.9.9"
	if got := versionString(); !strings.HasPrefix(got, "9.9.9 ") {
		t.Fatalf("ldflags override ignored: %q", got)
	}
}
