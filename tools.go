package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const outputTailLimit = 4096

// rarExtractors lists the binaries that can extract rar archives, in order of
// preference. rar accepts the same switches as unrar for x and lt. unar (The
// Unarchiver) has its own RAR decoder and lists archives with its companion
// lsar; it is the one Homebrew still ships, since the rar cask was disabled.
var rarExtractors = []string{"unrar", "rar", "unar"}

// rarExtractor returns the first available rar extractor in PATH.
func rarExtractor() (string, error) {
	for _, b := range rarExtractors {
		if _, err := exec.LookPath(b); err != nil {
			continue
		}
		if b == "unar" {
			if _, err := exec.LookPath("lsar"); err != nil {
				continue // unar can extract but not list without lsar
			}
		}
		return b, nil
	}
	return "", fmt.Errorf("no rar extractor found in PATH (want %s)", strings.Join(rarExtractors, ", "))
}

// checkTools verifies that the external extractors are available in PATH.
func checkTools() error {
	var missing []string
	if _, err := rarExtractor(); err != nil {
		missing = append(missing, strings.Join(rarExtractors, ", ")+" (one of)")
	}
	if _, err := exec.LookPath("unzip"); err != nil {
		missing = append(missing, "unzip")
	}
	if len(missing) > 0 {
		return fmt.Errorf("required tool(s) not found in PATH: %s", strings.Join(missing, ", "))
	}
	return nil
}

// unrarExtract extracts archive (or the volume set starting at archive) into dest.
func unrarExtract(ctx context.Context, archive, dest string) error {
	tool, err := rarExtractor()
	if err != nil {
		return err
	}
	if tool == "unar" {
		// -q: quiet, -f: overwrite, -D: no extra containing folder. unar exits
		// 1 on failure, so no exit code is tolerated as a warning; without -p
		// it fails instead of prompting for a password.
		return runTool(ctx, false, tool, "-q", "-f", "-D", "-o", dest, "--", archive)
	}
	// x: keep paths, -idq: quiet, -o+: overwrite, -p-: never prompt for a
	// password, -y: assume yes, -c-: do not print the archive comment.
	return runTool(ctx, true, tool, "x", "-idq", "-o+", "-p-", "-y", "-c-", "--", archive, withSlash(dest))
}

// unzipExtract extracts a zip archive into dest.
func unzipExtract(ctx context.Context, archive, dest string) error {
	return runTool(ctx, true, "unzip", "-qq", "-o", archive, "-d", dest)
}

// rarUnpackedSize returns the total uncompressed size of the files in a rar
// archive, following every volume of a set.
func rarUnpackedSize(ctx context.Context, archive string) (int64, error) {
	entries, err := rarList(ctx, archive)
	if err != nil {
		return 0, err
	}
	return totalSize(entries), nil
}

// rarList returns the files and symlinks of a rar archive. -v lists every
// volume of a set: without it, entries whose headers live in later volumes
// are missed.
func rarList(ctx context.Context, archive string) ([]FileEntry, error) {
	tool, err := rarExtractor()
	if err != nil {
		return nil, err
	}
	if tool == "unar" {
		return lsarList(ctx, archive)
	}
	cmd := exec.CommandContext(ctx, tool, "lt", "-v", "-p-", "-c-", "--", archive)
	cmd.WaitDelay = 5 * time.Second
	var stderr tailBuffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && exitCode(err) > 1 {
		return nil, fmt.Errorf("%s lt failed: %w%s", tool, err, stderr.suffix())
	}
	return parseUnrarTechList(out), nil
}

// parseUnrarTechList extracts files and symlinks from `unrar lt -v` output.
// Directories and service entries (comments) are skipped. Files split across
// volumes appear once per volume and are returned once.
func parseUnrarTechList(out []byte) []FileEntry {
	var (
		entries []FileEntry
		name    string
		kind    string
		seen    = map[string]bool{}
	)
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "Name":
			name, kind = value, ""
		case "Type":
			kind = value
		case "Size":
			isFile := kind == "File"
			isLink := strings.Contains(strings.ToLower(kind), "link")
			if (!isFile && !isLink) || seen[name] {
				continue
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				continue
			}
			if isLink {
				n = 0
			}
			seen[name] = true
			entries = append(entries, FileEntry{Path: filepath.ToSlash(name), Size: n})
		}
	}
	return entries
}

// lsarList lists a rar archive with lsar, which follows every volume of a set.
func lsarList(ctx context.Context, archive string) ([]FileEntry, error) {
	cmd := exec.CommandContext(ctx, "lsar", "-j", "--", archive)
	cmd.WaitDelay = 5 * time.Second
	var stderr tailBuffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("lsar failed: %w%s", err, stderr.suffix())
	}
	return parseLsarJSON(out)
}

// parseLsarJSON extracts files and symlinks from `lsar -j` output.
func parseLsarJSON(out []byte) ([]FileEntry, error) {
	var doc struct {
		Contents []struct {
			Name  string `json:"XADFileName"`
			Size  int64  `json:"XADFileSize"`
			IsDir bool   `json:"XADIsDirectory"`
			Link  bool   `json:"XADIsLink"`
		} `json:"lsarContents"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("lsar: unexpected output: %w", err)
	}
	var entries []FileEntry
	seen := map[string]bool{}
	for _, c := range doc.Contents {
		if c.IsDir || c.Name == "" || seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		size := c.Size
		if c.Link {
			size = 0
		}
		entries = append(entries, FileEntry{Path: filepath.ToSlash(c.Name), Size: size})
	}
	return entries, nil
}

// zipUnpackedSize returns the total uncompressed size of a zip archive.
func zipUnpackedSize(archive string) (int64, error) {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return 0, err
	}
	defer func() { _ = r.Close() }() // read-only: nothing to flush
	var total int64
	for _, f := range r.File {
		if !f.FileInfo().IsDir() {
			total += int64(f.UncompressedSize64)
		}
	}
	return total, nil
}

// runTool runs an extractor. Exit code 1 (warnings only) is tolerated for both
// unrar and unzip when allowWarnings is true.
func runTool(ctx context.Context, allowWarnings bool, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 5 * time.Second
	var out tailBuffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	// Stdin is left nil (/dev/null) so any interactive prompt fails fast.
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil {
		return nil
	}
	if allowWarnings && exitCode(err) == 1 {
		return nil
	}
	return fmt.Errorf("%s failed: %w%s", name, err, out.suffix())
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func withSlash(dir string) string {
	if strings.HasSuffix(dir, "/") {
		return dir
	}
	return dir + "/"
}

// tailBuffer keeps only the last outputTailLimit bytes written to it.
type tailBuffer struct {
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > outputTailLimit {
		t.buf = t.buf[len(t.buf)-outputTailLimit:]
	}
	return len(p), nil
}

func (t *tailBuffer) suffix() string {
	s := strings.TrimSpace(string(t.buf))
	if s == "" {
		return ""
	}
	return "\n" + s
}
