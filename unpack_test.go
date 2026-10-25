package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func touch(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(dir, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFirstVolumes(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		want    []string
		wantErr string
	}{
		{name: "part style", files: []string{"grp.part2.rar", "grp.part1.rar", "grp.part3.rar"}, want: []string{"grp.part1.rar"}},
		{name: "zero padded", files: []string{"grp.part01.rar", "grp.part02.rar"}, want: []string{"grp.part01.rar"}},
		{name: "old style", files: []string{"grp.rar", "grp.r00", "grp.r01"}, want: []string{"grp.rar"}},
		{name: "single rar", files: []string{"grp.rar"}, want: []string{"grp.rar"}},
		{name: "missing middle", files: []string{"grp.part1.rar", "grp.part3.rar"}, wantErr: "volume 2"},
		{name: "missing first", files: []string{"grp.part2.rar"}, wantErr: "first volume"},
		{name: "old style gap", files: []string{"grp.rar", "grp.r01"}, wantErr: "volume 1"},
		{name: "empty", files: nil, wantErr: "no rar volumes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			touch(t, dir, tc.files...)
			got, err := firstVolumes(dir)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, g := range got {
				names = append(names, filepath.Base(g))
			}
			if strings.Join(names, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v", names, tc.want)
			}
		})
	}
}

func TestCollect(t *testing.T) {
	src, vols, metaDir := t.TempDir(), t.TempDir(), t.TempDir()
	touch(t, src,
		"Rel/file_id.diz", "Rel/GRP.NFO", "Rel/cr-10.zip", "Rel/cr-2.zip",
		"Rel/sub/file_id.diz", "Rel/sub/grp.part1.rar", "Rel/junk.txt",
	)
	meta := newMetaSet(metaDir)
	zips, err := collect(src, vols, meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(zips) != 2 || filepath.Base(zips[0]) != "cr-2.zip" {
		t.Fatalf("zips = %v", zips)
	}
	got := meta.paths()
	want := []string{filepath.Join(metaDir, "GRP.NFO"), filepath.Join(metaDir, "file_id.diz")}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("meta = %v, want %v", got, want)
	}
	// The shallowest copy must win.
	if b, _ := os.ReadFile(want[1]); string(b) != "Rel/file_id.diz" {
		t.Fatalf("kept wrong file_id.diz: %q", b)
	}

	// A later stage must not replace metadata already kept.
	zipDir := t.TempDir()
	touch(t, zipDir, "file_id.diz", "extra.nfo")
	if _, err := collect(zipDir, vols, meta); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(want[1]); string(b) != "Rel/file_id.diz" {
		t.Fatalf("file_id.diz was replaced: %q", b)
	}
	if len(meta.paths()) != 3 {
		t.Fatalf("meta = %v", meta.paths())
	}
	if _, err := os.Stat(filepath.Join(vols, "grp.part1.rar")); err != nil {
		t.Fatalf("volume not moved: %v", err)
	}
}

func TestParseUnrarTechList(t *testing.T) {
	out := `
Archive: grp.part1.rar
Details: RAR 5, volume 1, lock

        Name: CMT
        Type: Service
        Size: 137
 Packed size: 137

        Name: Extras/keygen
        Type: File
        Size: 2058898
 Packed size: 774402

        Name: Extras
        Type: Directory

        Name: app.dmg
        Type: File
        Size: 31883884
 Packed size: 5000000

        Name: app.dmg
        Type: File
        Size: 31883884
`
	got := parseUnrarTechList([]byte(out + `
        Name: Extras/link
        Type: Unix symbolic link
        Size: 0
`))
	want := []FileEntry{{"Extras/keygen", 2058898}, {"app.dmg", 31883884}, {"Extras/link", 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReleaseName(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "My.Release-GRP/a.zip")
	if got, _ := releaseName(dir, "/x/archive.rar"); got != "My.Release-GRP" {
		t.Fatalf("got %q", got)
	}
	dir = t.TempDir()
	touch(t, dir, "a.zip", "b.zip")
	if got, _ := releaseName(dir, "/x/Other.Release.rar"); got != "Other.Release" {
		t.Fatalf("got %q", got)
	}
}

func TestNaturalLess(t *testing.T) {
	in := []string{"cr-10.zip", "cr-2.zip", "cr-1.zip", "cr-1a.zip"}
	sort.Slice(in, func(i, j int) bool { return naturalLess(in[i], in[j]) })
	if strings.Join(in, ",") != "cr-1.zip,cr-1a.zip,cr-2.zip,cr-10.zip" {
		t.Fatalf("got %v", in)
	}
}
