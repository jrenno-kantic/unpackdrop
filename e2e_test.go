package main

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// buildRelease creates a release like the real ones, but from another group:
// Some.App.v2.0-XYZ.rar > Some.App.v2.0-XYZ/{outerMeta, xyz*.zip} >
// [sub/] {grp.partN.rar, zipMeta}. Odd zips put their files in a subfolder.
// Metadata files contain their own name.
func buildRelease(t *testing.T, dir string, outerMeta, zipMeta []string) string {
	t.Helper()
	const release = "Some.App.v2.0-XYZ"

	payload := filepath.Join(dir, "payload")
	touch(t, payload, "Docs/readme.txt")
	data := make([]byte, 300*1024) // random, so -m0 volumes stay ~100 KiB each
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(payload, "app.dmg"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	vols := filepath.Join(dir, "vols")
	if err := os.Mkdir(vols, 0o755); err != nil {
		t.Fatal(err)
	}
	rarCmd := exec.Command("rar", "a", "-r", "-m0", "-v100k", "-idq", "-y", "--", filepath.Join(vols, "grp.rar"), "app.dmg", "Docs")
	rarCmd.Dir = payload
	if b, err := rarCmd.CombinedOutput(); err != nil {
		t.Fatalf("rar volumes: %v\n%s", err, b)
	}
	parts, _ := filepath.Glob(filepath.Join(vols, "grp.part*.rar"))
	if len(parts) < 3 {
		t.Fatalf("expected several volumes, got %v", parts)
	}

	relDir := filepath.Join(dir, "outer", release)
	if err := os.MkdirAll(relDir, 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, relDir, outerMeta...)
	metaSrc := filepath.Join(dir, "zipmeta")
	touch(t, metaSrc, zipMeta...)
	for i, p := range parts {
		prefix := ""
		if i%2 == 1 {
			prefix = "sub/"
		}
		entries := map[string]string{prefix + filepath.Base(p): p}
		for _, m := range zipMeta {
			entries[prefix+m] = filepath.Join(metaSrc, m)
		}
		writeZip(t, filepath.Join(relDir, "xyz"+string(rune('a'+i))+".zip"), entries)
	}

	archive := filepath.Join(dir, release+".rar")
	outer := exec.Command("rar", "a", "-r", "-idq", "-y", "--", archive, release)
	outer.Dir = filepath.Join(dir, "outer")
	if b, err := outer.CombinedOutput(); err != nil {
		t.Fatalf("outer rar: %v\n%s", err, b)
	}
	return archive
}

func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, src := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		in, err := os.Open(src)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(w, in)
		_ = in.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func requireTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
}

// pathWith sets PATH to a directory holding only links to the given tools.
func pathWith(t *testing.T, tools ...string) {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range tools {
		p, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not installed", tool)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
}

func TestRarExtractorFallback(t *testing.T) {
	pathWith(t, "unrar", "rar")
	if got, err := rarExtractor(); err != nil || got != "unrar" {
		t.Fatalf("with both: got %q, %v; want unrar", got, err)
	}
	pathWith(t, "rar")
	if got, err := rarExtractor(); err != nil || got != "rar" {
		t.Fatalf("rar only: got %q, %v; want rar", got, err)
	}
	pathWith(t)
	if _, err := rarExtractor(); err == nil {
		t.Fatal("no extractor: want an error")
	}
	if err := checkTools(); err == nil {
		t.Fatal("checkTools: want an error")
	}
}

// TestUnpackWithRarOnly runs a full unpack when unrar is not installed.
func TestUnpackWithRarOnly(t *testing.T) {
	requireTools(t, "rar", "unzip")
	archive := buildRelease(t, t.TempDir(), []string{"xyz.nfo", "file_id.diz"}, []string{"xyz.nfo"})
	pathWith(t, "rar", "unzip")
	if err := checkTools(); err != nil {
		t.Fatal(err)
	}
	res, err := Unpack(context.Background(), Options{Archive: archive, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := listFiles(res.Dest)
	sort.Strings(got)
	if want := []string{"Docs/readme.txt", "app.dmg", "file_id.diz", "xyz.nfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
}

func TestUnpackOtherGroup(t *testing.T) {
	requireTools(t, "rar", "unrar", "unzip")
	tests := []struct {
		name               string
		outerMeta, zipMeta []string
		wantMeta           []string
		wantWarning        bool
	}{
		{
			name:      "nfo in outer folder and zips",
			outerMeta: []string{"xyz.nfo", "file_id.diz"},
			zipMeta:   []string{"xyz.nfo", "file_id.diz"},
			wantMeta:  []string{"file_id.diz", "xyz.nfo"},
		},
		{
			name:     "nfo only inside the zips, upper case",
			zipMeta:  []string{"XYZ.NFO", "file_id.diz"},
			wantMeta: []string{"XYZ.NFO", "file_id.diz"},
		},
		{
			name:      "several nfo files",
			outerMeta: []string{"xyz.nfo", "release.nfo"},
			wantMeta:  []string{"release.nfo", "xyz.nfo"},
		},
		{
			name:        "no nfo",
			outerMeta:   []string{"file_id.diz"},
			wantMeta:    []string{"file_id.diz"},
			wantWarning: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			archive := buildRelease(t, t.TempDir(), tc.outerMeta, tc.zipMeta)
			out := t.TempDir()
			var log strings.Builder

			res, err := Unpack(context.Background(), Options{Archive: archive, OutputDir: out, Log: &log})
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(out, "Some.App.v2.0-XYZ"); res.Dest != want {
				t.Fatalf("dest = %q, want %q", res.Dest, want)
			}
			got, _ := listFiles(res.Dest)
			want := append([]string{"Docs/readme.txt", "app.dmg"}, tc.wantMeta...)
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("files = %v, want %v", got, want)
			}
			for _, m := range tc.wantMeta {
				if b, _ := os.ReadFile(filepath.Join(res.Dest, m)); string(b) != m {
					t.Fatalf("%s content = %q", m, b)
				}
			}
			if warned := strings.Contains(log.String(), "no .nfo file found"); warned != tc.wantWarning {
				t.Fatalf("warning = %v, want %v; log:\n%s", warned, tc.wantWarning, log.String())
			}
			// Only the result is left in the output directory: no work folder.
			entries, _ := os.ReadDir(out)
			if len(entries) != 1 {
				t.Fatalf("output dir has %d entries, want 1", len(entries))
			}
		})
	}
}

func TestUnpackRefusesWhenDiskIsFull(t *testing.T) {
	requireTools(t, "rar", "unrar", "unzip")
	archive := buildRelease(t, t.TempDir(), []string{"xyz.nfo"}, nil)
	out := t.TempDir()

	saved := availableBytes
	defer func() { availableBytes = saved }()
	availableBytes = func(string) (int64, bool) { return 1 << 20, true } // 1 MiB free

	_, err := Unpack(context.Background(), Options{Archive: archive, OutputDir: out})
	if err == nil || !strings.Contains(err.Error(), "not enough disk space") {
		t.Fatalf("err = %v, want a disk space error", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("output dir not empty after refusal: %v", entries)
	}

	// A dry run inspects the zips in the temp directory, so only a full
	// output disk is reported, as a problem rather than an error.
	availableBytes = func(dir string) (int64, bool) {
		if strings.HasPrefix(dir, out) {
			return 1 << 20, true
		}
		return saved(dir)
	}
	res, err := Unpack(context.Background(), Options{Archive: archive, OutputDir: out, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "not enough disk space") || res.Available != 1<<20 {
		t.Fatalf("problems = %v, available = %d", res.Problems, res.Available)
	}
}

func TestDryRunMatchesRealRun(t *testing.T) {
	requireTools(t, "rar", "unrar", "unzip")
	archive := buildRelease(t, t.TempDir(), []string{"xyz.nfo", "file_id.diz"}, []string{"xyz.nfo"})
	out := filepath.Join(t.TempDir(), "not-created-yet")

	plan, err := Unpack(context.Background(), Options{Archive: archive, OutputDir: out, DryRun: true, Repack: FormatZip, Level: LevelFast})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("dry run created the output directory: %v", err)
	}
	if len(plan.Problems) != 0 || plan.Need <= 0 {
		t.Fatalf("unexpected plan: problems=%v need=%d", plan.Problems, plan.Need)
	}
	if !strings.HasSuffix(plan.Archive, "Some.App.v2.0-XYZ.zip") || plan.Dest != "" {
		t.Fatalf("plan targets: archive=%q dest=%q", plan.Archive, plan.Dest)
	}

	real, err := Unpack(context.Background(), Options{Archive: archive, OutputDir: out, Repack: FormatZip, Level: LevelFast})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Files, real.Files) || plan.Size != real.Size {
		t.Fatalf("plan files = %v (%d)\nreal files = %v (%d)", plan.Files, plan.Size, real.Files, real.Size)
	}
	if real.ArchiveSize <= 0 {
		t.Fatalf("archive size = %d", real.ArchiveSize)
	}

	// Running the plan again now reports the existing archive as a problem.
	again, err := Unpack(context.Background(), Options{Archive: archive, OutputDir: out, DryRun: true, Repack: FormatZip})
	if err != nil {
		t.Fatal(err)
	}
	if !again.ArchiveExists || len(again.Problems) != 1 || !strings.Contains(again.Problems[0], "already exists") {
		t.Fatalf("second plan: exists=%v problems=%v", again.ArchiveExists, again.Problems)
	}
}
