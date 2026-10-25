package main

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Format is a repack archive format.
type Format string

const (
	FormatNone Format = ""
	FormatZip  Format = "zip"
	FormatRar  Format = "rar"
	Format7z   Format = "7z"
)

var sevenZipBinaries = []string{"7zz", "7z", "7za"}

// Level is a repack compression level, mapped to each tool's own setting.
type Level string

const (
	LevelStore  Level = "store"
	LevelFast   Level = "fast"
	LevelNormal Level = "normal" // the tools' defaults
	LevelMax    Level = "max"
)

func parseLevel(s string) (Level, error) {
	switch l := Level(strings.ToLower(s)); l {
	case "":
		return LevelNormal, nil
	case LevelStore, LevelFast, LevelNormal, LevelMax:
		return l, nil
	}
	return "", fmt.Errorf("unsupported compression level %q (want store, fast, normal or max)", s)
}

// rarSwitch returns rar's -m switch (0 store ... 5 best).
func (l Level) rarSwitch() string {
	switch l {
	case LevelStore:
		return "-m0"
	case LevelFast:
		return "-m1"
	case LevelMax:
		return "-m5"
	}
	return "-m3"
}

// sevenZipSwitch returns 7z's -mx switch (0 store ... 9 ultra).
func (l Level) sevenZipSwitch() string {
	switch l {
	case LevelStore:
		return "-mx0"
	case LevelFast:
		return "-mx1"
	case LevelMax:
		return "-mx9"
	}
	return "-mx5"
}

// flateLevel returns the deflate level for zip; store uses no compression.
func (l Level) flateLevel() int {
	switch l {
	case LevelFast:
		return flate.BestSpeed
	case LevelMax:
		return flate.BestCompression
	}
	return flate.DefaultCompression
}

func parseFormat(s string) (Format, error) {
	switch f := Format(strings.ToLower(strings.TrimPrefix(s, "."))); f {
	case FormatNone, FormatZip, FormatRar, Format7z:
		return f, nil
	}
	return "", fmt.Errorf("unsupported repack format %q (want zip, rar or 7z)", s)
}

func (f Format) ext() string { return "." + string(f) }

// tool returns the external binary needed to build the format, or "" when
// the format is written natively.
func (f Format) tool() (string, error) {
	switch f {
	case FormatRar:
		if _, err := exec.LookPath("rar"); err != nil {
			return "", fmt.Errorf("repacking to rar requires the rar tool in PATH")
		}
		return "rar", nil
	case Format7z:
		for _, b := range sevenZipBinaries {
			if _, err := exec.LookPath(b); err == nil {
				return b, nil
			}
		}
		return "", fmt.Errorf("repacking to 7z requires one of %s in PATH", strings.Join(sevenZipBinaries, ", "))
	}
	return "", nil
}

// pack archives the contents of srcDir (without srcDir itself as a root
// folder) into archivePath, which must not exist yet.
func pack(ctx context.Context, f Format, level Level, srcDir, archivePath string, bar *Bar, total int64) error {
	if f == FormatZip {
		return zipDir(ctx, srcDir, archivePath, level, bar)
	}
	tool, err := f.tool()
	if err != nil {
		return err
	}
	entries, err := topLevelEntries(srcDir)
	if err != nil {
		return err
	}
	var args []string
	switch f {
	case FormatRar:
		// -r: recurse, -ol: store symlinks as links, -ma5: RAR5, -idc: no banner.
		args = []string{"a", "-r", "-ol", "-ma5", level.rarSwitch(), "-idc", "-y", "--", archivePath}
	case Format7z:
		// -snl: store symlinks as links, -spd: no wildcard matching on names,
		// -bsp1: progress on stdout, -bso0: no other output.
		args = []string{"a", "-t7z", level.sevenZipSwitch(), "-snl", "-spd", "-bso0", "-bse2", "-bsp1", "-y", "--", archivePath}
	}
	return runWithPercent(ctx, srcDir, bar, total, tool, append(args, entries...)...)
}

// verify tests the integrity of a freshly written archive.
func verify(ctx context.Context, f Format, archivePath string, bar *Bar, total int64) error {
	if f == FormatZip {
		return verifyZip(ctx, archivePath, bar)
	}
	tool, err := f.tool()
	if err != nil {
		return err
	}
	var args []string
	switch f {
	case FormatRar:
		args = []string{"t", "-idc", "-p-", "--", archivePath}
	case Format7z:
		args = []string{"t", "-bso0", "-bse2", "-bsp1", "--", archivePath}
	}
	return runWithPercent(ctx, filepath.Dir(archivePath), bar, total, tool, args...)
}

// runWithPercent runs an archiver and maps the percentages it prints on
// stdout to the progress bar. Unlike extraction, warnings are fatal: the
// unpacked folder may be deleted afterwards, so the archive must be complete.
func runWithPercent(ctx context.Context, dir string, bar *Bar, total int64, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 5 * time.Second
	var tail tailBuffer
	cmd.Stdout = io.MultiWriter(&tail, &percentWriter{fn: func(p int) {
		bar.Set(total * int64(p) / 100)
	}})
	cmd.Stderr = &tail
	err := cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("%s failed: %w%s", name, err, tail.suffix())
	}
	return nil
}

// percentWriter extracts "NN%" tokens from a progress stream.
type percentWriter struct {
	fn     func(int)
	digits []byte
}

func (p *percentWriter) Write(b []byte) (int, error) {
	for _, c := range b {
		switch {
		case isDigit(c):
			if len(p.digits) < 3 {
				p.digits = append(p.digits, c)
			} else {
				p.digits = p.digits[:0] // too long to be a percentage
			}
		case c == '%' && len(p.digits) > 0:
			n := 0
			for _, d := range p.digits {
				n = n*10 + int(d-'0')
			}
			if n <= 100 {
				p.fn(n)
			}
			p.digits = p.digits[:0]
		default:
			p.digits = p.digits[:0]
		}
	}
	return len(b), nil
}

func topLevelEntries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("nothing to pack in %s", dir)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// zipDir writes srcDir's contents into a new zip, keeping file modes,
// modification times and symlinks.
func zipDir(ctx context.Context, srcDir, archivePath string, level Level, bar *Bar) (err error) {
	out, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	buf := bufio.NewWriterSize(out, 1<<20)
	zw := zip.NewWriter(buf)
	method := zip.Deflate
	if level == LevelStore {
		method = zip.Store
	} else {
		zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
			return flate.NewWriter(w, level.flateLevel())
		})
	}

	var done int64
	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == srcDir {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)

		switch {
		case d.IsDir():
			hdr.Name += "/"
			hdr.Method = zip.Store
			_, err = zw.CreateHeader(hdr)
			return err
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			hdr.Method = zip.Store
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			_, err = io.WriteString(w, target)
			return err
		case d.Type().IsRegular():
			hdr.Method = method
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			return copyFile(ctx, w, path, func(n int64) {
				done += n
				bar.Set(done)
			})
		default:
			return fmt.Errorf("unsupported file type: %s", rel)
		}
	})
	if err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := buf.Flush(); err != nil {
		return err
	}
	return out.Sync()
}

func copyFile(ctx context.Context, w io.Writer, path string, progress func(int64)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // read-only: nothing to flush
	_, err = io.Copy(w, &progressReader{ctx: ctx, r: f, fn: progress})
	return err
}

// verifyZip reads every entry back; archive/zip checks each CRC32 at EOF.
func verifyZip(ctx context.Context, archivePath string, bar *Bar) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }() // read-only: nothing to flush
	var done int64
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
		_, err = io.Copy(io.Discard, &progressReader{ctx: ctx, r: rc, fn: func(n int64) {
			done += n
			bar.Set(done)
		}})
		_ = rc.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return nil
}

type progressReader struct {
	ctx context.Context
	r   io.Reader
	fn  func(int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.r.Read(b)
	if n > 0 {
		p.fn(int64(n))
	}
	return n, err
}
