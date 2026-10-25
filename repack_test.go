package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{"": FormatNone, "zip": FormatZip, ".RAR": FormatRar, "7z": Format7z} {
		got, err := parseFormat(in)
		if err != nil || got != want {
			t.Errorf("parseFormat(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := parseFormat("tar"); err == nil {
		t.Error("parseFormat(tar) should fail")
	}
}

func TestPercentWriter(t *testing.T) {
	var got []int
	w := &percentWriter{fn: func(p int) { got = append(got, p) }}
	// Shapes seen from rar and 7zz, split across writes on purpose.
	for _, chunk := range []string{"Adding  a.bin   \b\b\b\b  1", "%\b\b\b\b OK\n", "  0M Scan  ", " 42% 1222 + f\b\b 100%", " 1234% 7"} {
		_, _ = w.Write([]byte(chunk))
	}
	if want := []int{1, 42, 100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// makeTree builds a small release tree with a nested folder and a symlink.
func makeTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	touch(t, dir, "file_id.diz", "grp.nfo", "App.app/Contents/Info.plist", "Extras/tool")
	if err := os.Symlink("Contents/Info.plist", filepath.Join(dir, "App.app/link")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRepackRoundTrip(t *testing.T) {
	for _, f := range []Format{FormatZip, FormatRar, Format7z} {
		t.Run(string(f), func(t *testing.T) {
			if _, err := f.tool(); err != nil {
				t.Skip(err)
			}

			src := makeTree(t)
			archive := filepath.Join(t.TempDir(), "Release"+f.ext())
			bar := NewBar(io.Discard, "pack", 0, false)
			ctx := context.Background()
			if err := pack(ctx, f, LevelNormal, src, archive, bar, dirSize(src)); err != nil {
				t.Fatal(err)
			}
			if err := verify(ctx, f, archive, bar, dirSize(src)); err != nil {
				t.Fatal(err)
			}

			out := t.TempDir()
			var cmd *exec.Cmd
			switch f {
			case FormatZip:
				cmd = exec.Command("unzip", "-qq", archive, "-d", out)
			case FormatRar:
				tool, err := rarExtractor()
				if err != nil {
					t.Fatal(err)
				}
				cmd = exec.Command(tool, "x", "-idq", "--", archive, out+"/")
			case Format7z:
				tool, _ := f.tool()
				cmd = exec.Command(tool, "x", "-bso0", "-o"+out, "--", archive)
			}
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("extract: %v\n%s", err, b)
			}

			// Entries must sit at the archive root, not under a folder.
			want, _ := listFiles(src)
			got, _ := listFiles(out)
			sort.Strings(want)
			sort.Strings(got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("files = %v, want %v", got, want)
			}
			if target, err := os.Readlink(filepath.Join(out, "App.app/link")); err != nil || target != "Contents/Info.plist" {
				t.Fatalf("symlink not preserved: %q, %v", target, err)
			}
			if b, _ := os.ReadFile(filepath.Join(out, "Extras/tool")); string(b) != "Extras/tool" {
				t.Fatalf("content mismatch: %q", b)
			}
		})
	}
}

func TestVerifyZipDetectsCorruption(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "data.bin"), make([]byte, 64*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "r.zip")
	bar := NewBar(io.Discard, "pack", 0, false)
	if err := pack(context.Background(), FormatZip, LevelNormal, src, archive, bar, 0); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	b[60] ^= 0xFF // inside the compressed data of the only entry
	if err := os.WriteFile(archive, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verify(context.Background(), FormatZip, archive, bar, 0); err == nil {
		t.Fatal("verify should fail on a corrupted archive")
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]Level{"": LevelNormal, "store": LevelStore, "FAST": LevelFast, "normal": LevelNormal, "max": LevelMax} {
		if got, err := parseLevel(in); err != nil || got != want {
			t.Errorf("parseLevel(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := parseLevel("ultra"); err == nil {
		t.Error("parseLevel(ultra) should fail")
	}
	if LevelStore.rarSwitch() != "-m0" || LevelMax.sevenZipSwitch() != "-mx9" || LevelNormal.rarSwitch() != "-m3" {
		t.Error("unexpected tool switches")
	}
}

func TestZipLevels(t *testing.T) {
	src := t.TempDir()
	data := []byte(strings.Repeat("compressible ", 50000))
	if err := os.WriteFile(filepath.Join(src, "a.txt"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	sizes := map[Level]int64{}
	for _, l := range []Level{LevelStore, LevelFast, LevelMax} {
		archive := filepath.Join(t.TempDir(), "r.zip")
		bar := NewBar(io.Discard, "pack", 0, false)
		if err := pack(context.Background(), FormatZip, l, src, archive, bar, 0); err != nil {
			t.Fatal(err)
		}
		if err := verify(context.Background(), FormatZip, archive, bar, 0); err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(archive)
		sizes[l] = st.Size()
	}
	if sizes[LevelStore] < int64(len(data)) || sizes[LevelFast] >= sizes[LevelStore] || sizes[LevelMax] > sizes[LevelFast] {
		t.Fatalf("unexpected sizes: %v", sizes)
	}
}
