package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatCount(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1223: "1,223", 1234567: "1,234,567"} {
		if got := formatCount(n); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", n, got, want)
		}
	}
	if plural(1, "file") != "1 file" || plural(2, "file") != "2 files" {
		t.Error("plural")
	}
}

func TestSummaryAndPlan(t *testing.T) {
	r := &Result{
		Release: "Rel-GRP",
		Dest:    "/out/Rel-GRP",
		Files: []FileEntry{
			{"App.app/a", 10}, {"App.app/b", 20}, {"grp.nfo", 3}, {"file_id.diz", 2}, {"Docs/grp.nfo", 1},
		},
		Size: 36,
	}
	if got := strings.Join(r.Meta(), ","); got != "grp.nfo,file_id.diz" {
		t.Fatalf("Meta() = %q (nested nfo must not count)", got)
	}

	var b bytes.Buffer
	printSummary(&b, r)
	for _, want := range []string{"Done: Rel-GRP", "5 files, 36 B", "Info:     grp.nfo, file_id.diz", "Folder:   /out/Rel-GRP"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("summary missing %q:\n%s", want, b.String())
		}
	}
	if strings.Contains(b.String(), "App.app/a") {
		t.Errorf("summary should not list files:\n%s", b.String())
	}

	r.DryRun, r.DestExists, r.Problems = true, true, []string{"destination already exists"}
	b.Reset()
	printPlan(&b, r, Options{})
	for _, want := range []string{"Dry run: Rel-GRP", "(exists)", "App.app/", "2 files", "Problems:", "destination already exists"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("plan missing %q:\n%s", want, b.String())
		}
	}
}
