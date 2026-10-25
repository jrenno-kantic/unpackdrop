package main

import (
	"reflect"
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
		// Combined short options.
		{[]string{"-vq"}, 0},
		{[]string{"-qk", "x.rar"}, 2},
		{[]string{"-fqx", "x.rar"}, 2},
		// Long options need two dashes; -force would otherwise be -f -o rce.
		{[]string{"-version"}, 2},
		{[]string{"-force", "x.rar"}, 2},
		{[]string{"-L"}, 2},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			if got := run(tc.args); got != tc.want {
				t.Fatalf("run(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

func TestParseArgs(t *testing.T) {
	tests := []struct {
		args []string
		want cliOptions
	}{
		{[]string{"-fq", "a.rar"}, cliOptions{force: true, quiet: true, archives: []string{"a.rar"}}},
		{[]string{"-qkr", "7z", "a.rar"}, cliOptions{quiet: true, keepDir: true, repack: "7z", archives: []string{"a.rar"}}},
		{[]string{"-r7z", "-Lfast", "a.rar"}, cliOptions{repack: "7z", level: "fast", levelSet: true, archives: []string{"a.rar"}}},
		{[]string{"--repack=zip", "--level", "max", "a.rar"}, cliOptions{repack: "zip", level: "max", levelSet: true, archives: []string{"a.rar"}}},
		{[]string{"a.rar", "-nl", "b.rar", "-o", "/out"}, cliOptions{dryRun: true, list: true, output: "/out", archives: []string{"a.rar", "b.rar"}}},
		{[]string{"-ds", "--", "-odd-name.rar"}, cliOptions{deleteSource: true, skipVerify: true, archives: []string{"-odd-name.rar"}}},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			got, _, err := parseArgs(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*got, tc.want) {
				t.Fatalf("got  %+v\nwant %+v", *got, tc.want)
			}
		})
	}
	for _, bad := range [][]string{{"-fx"}, {"-version"}, {"-force", "a.rar"}, {"a.rar", "-repack", "7z"}, {"--level"}, {"--force=maybe"}} {
		if _, _, err := parseArgs(bad); err == nil {
			t.Errorf("parseArgs(%v): want an error", bad)
		}
	}
}

func TestLongOptionHint(t *testing.T) {
	_, fs, _ := parseArgs(nil)
	for args, want := range map[string]string{
		"-version":         "use --version instead of -version",
		"x.rar -repack=7z": "use --repack instead of -repack=7z",
		"-fz":              "",
		"-- -version":      "",
	} {
		if got := longOptionHint(fs, strings.Fields(args)); !strings.Contains(got, want) || (want == "" && got != "") {
			t.Errorf("longOptionHint(%q) = %q, want %q", args, got, want)
		}
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
