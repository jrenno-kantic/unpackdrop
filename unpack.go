package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	partVolumeRe = regexp.MustCompile(`(?i)^(.*)\.part(\d+)\.rar$`)
	oldVolumeRe  = regexp.MustCompile(`(?i)^(.*)\.r(\d{2})$`)
	rarRe        = regexp.MustCompile(`(?i)\.rar$`)
)

// Options configures a single unpack run.
type Options struct {
	Archive      string
	OutputDir    string // defaults to the archive's directory
	Force        bool   // replace an existing destination folder
	DeleteSource bool   // remove the source archive on success
	Repack       Format // when set, pack the result into <release>.<format>
	Level        Level  // with Repack, compression level (default normal)
	KeepDir      bool   // with Repack, also keep the unpacked folder
	SkipVerify   bool   // with Repack, do not test the new archive
	DryRun       bool   // report what would be kept and where, write nothing to OutputDir
	Progress     bool
	Log          io.Writer
}

// FileEntry is a file of the release, relative to its root.
type FileEntry struct {
	Path string
	Size int64
}

// Result describes a successful unpack, or the plan of a dry run.
type Result struct {
	Release     string
	Dest        string      // unpacked folder; empty if repacked and not kept
	Archive     string      // repacked archive; empty without Repack
	ArchiveSize int64       // 0 in a dry run
	Files       []FileEntry // sorted by path
	Size        int64       // total size of Files

	// Dry run only.
	DryRun         bool
	DestExists     bool     // Dest exists and would be replaced (with --force)
	ArchiveExists  bool     // Archive exists and would be replaced (with --force)
	ReplacesSource bool     // Archive would replace the source archive
	DeletesSource  bool     // the source archive would be deleted
	Need           int64    // estimated peak disk use in the output directory
	Available      int64    // free space there, -1 if unknown
	Problems       []string // reasons the real run would fail
}

// Meta returns the .nfo/.diz files at the root of the release.
func (r *Result) Meta() []string {
	var meta []string
	for _, f := range r.Files {
		if !strings.Contains(f.Path, "/") && isMetaFile(f.Path) {
			meta = append(meta, f.Path)
		}
	}
	return meta
}

// Unpack extracts a release archive and keeps only the .diz/.nfo metadata and
// the payload of the inner multi-part rar. All intermediate files are removed.
//
// Expected layout:
//
//	release.rar
//	└── Release/
//	    ├── file_id.diz, *.nfo
//	    └── *.zip  (each: [dir/] <name>.partN.rar, file_id.diz, *.nfo)
//
// Any .diz or .nfo file is kept, whatever the releasing group names it.
func Unpack(ctx context.Context, opt Options) (*Result, error) {
	archive, err := filepath.Abs(opt.Archive)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(archive)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}

	outDir := opt.OutputDir
	if outDir == "" {
		outDir = filepath.Dir(archive)
	}
	if outDir, err = filepath.Abs(outDir); err != nil {
		return nil, err
	}

	// The work directory lives inside outDir so the final move is a rename on
	// the same filesystem. A dry run works in the system temp directory
	// instead, so nothing is written to outDir. It is always removed,
	// including on failure/cancel.
	workParent, workPattern := outDir, ".unpackdrop-*"
	if opt.DryRun {
		workParent, workPattern = os.TempDir(), "unpackdrop-dryrun-*"
	} else if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(workParent, workPattern)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := os.RemoveAll(work); err != nil {
			logf(opt, "warning: could not remove temporary directory %s: %v\n", work, err)
		}
	}()

	var (
		sourceDir  = filepath.Join(work, "source")
		volumesDir = filepath.Join(work, "volumes")
		metaDir    = filepath.Join(work, "meta")
		payloadDir = filepath.Join(work, "payload")
	)
	for _, d := range []string{sourceDir, volumesDir, metaDir, payloadDir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			return nil, err
		}
	}

	steps := 3
	switch {
	case opt.DryRun:
		steps = 2
	case opt.Repack != FormatNone && opt.SkipVerify:
		steps = 4
	case opt.Repack != FormatNone:
		steps = 5
	}
	step := func(n int, label string) string { return fmt.Sprintf("[%d/%d] %s", n, steps, label) }

	res := &Result{DryRun: opt.DryRun, Available: -1}
	// In a dry run, conditions that would stop the real run are collected
	// instead of returned, so the whole plan can still be shown.
	problem := func(err error) error {
		if opt.DryRun {
			res.Problems = append(res.Problems, err.Error())
			return nil
		}
		return err
	}

	// Step 1: outer rar.
	sourceSize, err := rarUnpackedSize(ctx, archive)
	if err != nil {
		return nil, err
	}
	if err := requireSpace(work, sourceSize, "extracting the archive"); err != nil {
		return nil, err
	}
	if err := withProgress(opt, step(1, "Extracting archive"), sourceSize, sourceDir, func() error {
		return unrarExtract(ctx, archive, sourceDir)
	}); err != nil {
		return nil, err
	}

	release, err := releaseName(sourceDir, archive)
	if err != nil {
		return nil, err
	}
	res.Release = release
	dest := filepath.Join(outDir, release)
	keepDir := opt.Repack == FormatNone || opt.KeepDir
	if keepDir {
		res.Dest = dest
		if exists(dest) {
			res.DestExists = true
			if !opt.Force {
				if err := problem(errExists(dest)); err != nil {
					return nil, err
				}
			}
		}
	}
	if opt.Repack != FormatNone {
		res.Archive = filepath.Join(outDir, release+opt.Repack.ext())
		switch {
		case res.Archive == archive:
			// Only allowed when the user asked for the source to go away.
			res.ReplacesSource = true
			if !opt.DeleteSource {
				err := fmt.Errorf("repacked archive would overwrite the source %s (use --output or --delete-source)", archive)
				if err := problem(err); err != nil {
					return nil, err
				}
			}
		case exists(res.Archive):
			res.ArchiveExists = true
			if !opt.Force {
				if err := problem(errExists(res.Archive)); err != nil {
					return nil, err
				}
			}
		}
	}
	res.DeletesSource = opt.DeleteSource && !res.ReplacesSource
	logf(opt, "Release: %s\n", release)

	meta := newMetaSet(metaDir)
	zips, err := collect(sourceDir, volumesDir, meta)
	if err != nil {
		return nil, err
	}

	// Step 2: inner zips. Each one is extracted to its own folder, its rar
	// volumes are moved aside and everything else is discarded right away to
	// keep disk usage low.
	var zipTotal int64
	for _, z := range zips {
		n, err := zipUnpackedSize(z)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(z), err)
		}
		zipTotal += n
	}
	bar := NewBar(opt.Log, step(2, fmt.Sprintf("Unpacking %d zip(s)", len(zips))), zipTotal, opt.Progress)
	var done int64
	for i, z := range zips {
		dir := filepath.Join(work, fmt.Sprintf("zip-%04d", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			bar.Abort()
			return nil, err
		}
		stop := watchDir(dir, done, bar)
		err := unzipExtract(ctx, z, dir)
		stop()
		if err != nil {
			bar.Abort()
			return nil, fmt.Errorf("%s: %w", filepath.Base(z), err)
		}
		done += dirSize(dir)
		bar.Set(done)
		if _, err := collect(dir, volumesDir, meta); err != nil {
			bar.Abort()
			return nil, err
		}
		if err := os.RemoveAll(dir); err != nil {
			bar.Abort()
			return nil, err
		}
		if err := os.Remove(z); err != nil {
			bar.Abort()
			return nil, err
		}
	}
	bar.Finish()

	// Step 3: multi-part rar(s).
	firsts, err := firstVolumes(volumesDir)
	if err != nil {
		return nil, err
	}
	var payload []FileEntry
	var payloadSize int64
	for _, v := range firsts {
		entries, err := rarList(ctx, v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(v), err)
		}
		for _, e := range entries {
			payload = append(payload, e)
			payloadSize += e.Size
		}
	}

	if opt.DryRun {
		return planDryRun(opt, res, outDir, payload, meta, sourceSize, dirSize(volumesDir), payloadSize)
	}

	if err := requireSpace(work, payloadSize, "extracting the payload"); err != nil {
		return nil, err
	}
	if err := withProgress(opt, step(3, "Extracting payload"), payloadSize, payloadDir, func() error {
		for _, v := range firsts {
			if err := unrarExtract(ctx, v, payloadDir); err != nil {
				return fmt.Errorf("%s: %w", filepath.Base(v), err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// The volumes and the outer folder are no longer needed: free their space
	// before repacking.
	for _, d := range []string{volumesDir, sourceDir} {
		if err := os.RemoveAll(d); err != nil {
			return nil, err
		}
	}

	// Metadata goes to the root of the payload; never clobber payload files.
	for _, src := range meta.paths() {
		target := filepath.Join(payloadDir, filepath.Base(src))
		if _, err := os.Lstat(target); err == nil {
			logf(opt, "warning: %s already present in payload, keeping payload version\n", filepath.Base(src))
			continue
		}
		if err := os.Rename(src, target); err != nil {
			return nil, err
		}
	}

	if res.Files, err = listEntries(payloadDir); err != nil {
		return nil, err
	}
	res.Size = totalSize(res.Files)
	if !hasNFO(res.Files) {
		logf(opt, "warning: no .nfo file found in %s\n", filepath.Base(archive))
	}

	// Steps 4-5: repack and verify before anything is moved into place, so a
	// failure leaves existing outputs untouched.
	if opt.Repack != FormatNone {
		// Worst case (store), the archive is as large as its content.
		if err := requireSpace(work, res.Size, "repacking"); err != nil {
			return nil, err
		}
		tmpArchive := filepath.Join(work, "repack"+opt.Repack.ext())
		label := fmt.Sprintf("Packing %s (%s)", opt.Repack, opt.Level)
		bar := NewBar(opt.Log, step(4, label), res.Size, opt.Progress)
		if err := pack(ctx, opt.Repack, opt.Level, payloadDir, tmpArchive, bar, res.Size); err != nil {
			bar.Abort()
			return nil, fmt.Errorf("repack: %w", err)
		}
		bar.Finish()
		if !opt.SkipVerify {
			bar = NewBar(opt.Log, step(5, "Verifying archive"), res.Size, opt.Progress)
			if err := verify(ctx, opt.Repack, tmpArchive, bar, res.Size); err != nil {
				bar.Abort()
				return nil, fmt.Errorf("verify: %w", err)
			}
			bar.Finish()
		}
		if st, err := os.Stat(tmpArchive); err == nil {
			res.ArchiveSize = st.Size()
		}

		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if opt.Force && !res.ReplacesSource {
			if err := os.RemoveAll(res.Archive); err != nil {
				return nil, err
			}
		}
		if err := os.Rename(tmpArchive, res.Archive); err != nil {
			return nil, err
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if keepDir {
		if opt.Force {
			if err := os.RemoveAll(dest); err != nil {
				return nil, err
			}
		}
		if err := os.Rename(payloadDir, dest); err != nil {
			return nil, err
		}
	}

	if res.DeletesSource {
		if err := os.Remove(archive); err != nil {
			return nil, fmt.Errorf("unpacked but could not delete source: %w", err)
		}
	}
	return res, nil
}

// planDryRun completes a dry-run result from the payload listing.
func planDryRun(opt Options, res *Result, outDir string, payload []FileEntry, meta *metaSet, sourceSize, volumesSize, payloadSize int64) (*Result, error) {
	files := payload
	atRoot := map[string]bool{}
	for _, f := range payload {
		atRoot[strings.ToLower(f.Path)] = true
	}
	for _, m := range meta.paths() {
		name := filepath.Base(m)
		if atRoot[strings.ToLower(name)] {
			continue // the payload's own copy wins, as in a real run
		}
		var size int64
		if st, err := os.Stat(m); err == nil {
			size = st.Size()
		}
		files = append(files, FileEntry{Path: name, Size: size})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	res.Files = files
	res.Size = totalSize(files)
	if !hasNFO(files) {
		logf(opt, "warning: no .nfo file found in %s\n", filepath.Base(opt.Archive))
	}

	// Peak use of a real run: the extracted archive, then volumes + payload,
	// then payload + new archive (volumes are freed before repacking).
	res.Need = max(sourceSize, volumesSize+payloadSize)
	if opt.Repack != FormatNone {
		res.Need = max(res.Need, 2*payloadSize)
	}
	dir := existingParent(outDir)
	if avail, ok := availableBytes(dir); ok {
		res.Available = avail
		if avail < withMargin(res.Need) {
			res.Problems = append(res.Problems, fmt.Sprintf("not enough disk space in %s: need about %s, %s available",
				dir, formatBytes(withMargin(res.Need)), formatBytes(avail)))
		}
	}
	return res, nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func errExists(path string) error {
	return fmt.Errorf("destination already exists: %s (use -f/--force to overwrite)", path)
}

func totalSize(files []FileEntry) int64 {
	var n int64
	for _, f := range files {
		n += f.Size
	}
	return n
}

func withProgress(opt Options, label string, total int64, dir string, fn func() error) error {
	bar := NewBar(opt.Log, label, total, opt.Progress)
	stop := watchDir(dir, 0, bar)
	err := fn()
	stop()
	if err != nil {
		bar.Abort()
		return err
	}
	bar.Finish()
	return nil
}

// collect walks dir, moves rar volumes into volumesDir, hands metadata files
// to meta and returns the zip archives found.
func collect(dir, volumesDir string, meta *metaSet) ([]string, error) {
	var zips []string
	candidates := map[string]metaEntry{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		name := d.Name()
		switch {
		case strings.EqualFold(filepath.Ext(name), ".zip"):
			zips = append(zips, path)
		case isMetaFile(name):
			rel, _ := filepath.Rel(dir, path)
			depth := strings.Count(rel, string(filepath.Separator))
			key := strings.ToLower(name)
			if cur, ok := candidates[key]; !ok || depth < cur.depth {
				candidates[key] = metaEntry{path: path, depth: depth}
			}
		case isRarVolume(name):
			target := filepath.Join(volumesDir, name)
			if _, err := os.Lstat(target); err == nil {
				return fmt.Errorf("duplicate rar volume: %s", name)
			}
			return os.Rename(path, target)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for key, c := range candidates {
		if err := meta.keep(key, c.path); err != nil {
			return nil, err
		}
	}
	sort.Slice(zips, func(i, j int) bool { return naturalLess(zips[i], zips[j]) })
	return zips, nil
}

// firstVolumes returns the first volume of every rar set in dir, after
// checking that each set has no missing volumes.
func firstVolumes(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type set struct {
		first string
		nums  []int
	}
	sets := map[string]*set{}
	get := func(key string) *set {
		s, ok := sets[key]
		if !ok {
			s = &set{}
			sets[key] = s
		}
		return s
	}
	for _, e := range entries {
		name := e.Name()
		if m := partVolumeRe.FindStringSubmatch(name); m != nil {
			n, _ := strconv.Atoi(m[2])
			s := get("part:" + strings.ToLower(m[1]))
			s.nums = append(s.nums, n)
			if n == 1 {
				s.first = name
			}
		} else if m := oldVolumeRe.FindStringSubmatch(name); m != nil {
			n, _ := strconv.Atoi(m[2])
			s := get("old:" + strings.ToLower(m[1]))
			s.nums = append(s.nums, n+1) // .r00 is the 2nd volume
		} else if rarRe.MatchString(name) {
			s := get("old:" + strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))))
			s.nums = append(s.nums, 0)
			s.first = name
		}
	}

	var firsts []string
	for key, s := range sets {
		sort.Ints(s.nums)
		start := 1
		if strings.HasPrefix(key, "old:") {
			start = 0
		}
		if s.first == "" {
			return nil, fmt.Errorf("rar set %q: first volume is missing", strings.SplitN(key, ":", 2)[1])
		}
		for i, n := range s.nums {
			if n != start+i {
				return nil, fmt.Errorf("rar set %q: volume %d is missing or duplicated", strings.SplitN(key, ":", 2)[1], start+i)
			}
		}
		firsts = append(firsts, filepath.Join(dir, s.first))
	}
	if len(firsts) == 0 {
		return nil, errors.New("no rar volumes found inside the zip archives")
	}
	sort.Strings(firsts)
	return firsts, nil
}

// releaseName uses the single top-level folder of the extracted archive, or
// falls back to the archive name without extension.
func releaseName(sourceDir, archive string) (string, error) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return "", err
	}
	name := ""
	if len(entries) == 1 && entries[0].IsDir() {
		name = entries[0].Name()
	} else {
		base := filepath.Base(archive)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("cannot determine a valid release name")
	}
	return name, nil
}

func isMetaFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".diz" || ext == ".nfo"
}

func hasNFO(files []FileEntry) bool {
	for _, f := range files {
		if strings.EqualFold(filepath.Ext(f.Path), ".nfo") {
			return true
		}
	}
	return false
}

func isRarVolume(name string) bool {
	return rarRe.MatchString(name) || oldVolumeRe.MatchString(name)
}

// metaSet stores one file per (case-insensitive) name in its own directory,
// so it survives the cleanup of the folder it was found in. The first file
// kept for a name wins, which favours the outer archive over the zips.
type metaSet struct {
	dir   string
	names map[string]string // lower-case name -> stored path
}

type metaEntry struct {
	path  string
	depth int
}

func newMetaSet(dir string) *metaSet { return &metaSet{dir: dir, names: map[string]string{}} }

func (m *metaSet) keep(key, path string) error {
	if _, ok := m.names[key]; ok {
		return nil
	}
	target := filepath.Join(m.dir, filepath.Base(path))
	if err := os.Rename(path, target); err != nil {
		return err
	}
	m.names[key] = target
	return nil
}

func (m *metaSet) paths() []string {
	out := make([]string, 0, len(m.names))
	for _, p := range m.names {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// listEntries returns the regular files and symlinks under root, sorted.
func listEntries(root string) ([]FileEntry, error) {
	var files []FileEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		size := int64(0)
		if info.Mode().IsRegular() {
			size = info.Size()
		}
		files = append(files, FileEntry{Path: filepath.ToSlash(rel), Size: size})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}

// listFiles returns the paths of listEntries.
func listFiles(root string) ([]string, error) {
	entries, err := listEntries(root)
	paths := make([]string, len(entries))
	for i, e := range entries {
		paths[i] = e.Path
	}
	return paths, err
}

// naturalLess compares strings so that "cr-2.zip" sorts before "cr-10.zip".
func naturalLess(a, b string) bool {
	for a != "" && b != "" {
		ca, cb := a[0], b[0]
		if isDigit(ca) && isDigit(cb) {
			na, ra := splitDigits(a)
			nb, rb := splitDigits(b)
			ia, _ := strconv.ParseUint(na, 10, 64)
			ib, _ := strconv.ParseUint(nb, 10, 64)
			if ia != ib {
				return ia < ib
			}
			a, b = ra, rb
			continue
		}
		if ca != cb {
			return ca < cb
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func splitDigits(s string) (string, string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

func logf(opt Options, format string, args ...any) {
	if opt.Log != nil {
		fmt.Fprintf(opt.Log, format, args...)
	}
}
