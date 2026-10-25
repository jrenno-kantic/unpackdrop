package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// printSummary reports a finished unpack in a few lines.
func printSummary(w io.Writer, r *Result) {
	fmt.Fprintf(w, "Done: %s\n", r.Release)
	fmt.Fprintf(w, "  Content:  %s, %s\n", plural(len(r.Files), "file"), formatBytes(r.Size))
	fmt.Fprintf(w, "  Info:     %s\n", metaLine(r))
	if r.Dest != "" {
		fmt.Fprintf(w, "  Folder:   %s\n", r.Dest)
	}
	if r.Archive != "" {
		fmt.Fprintf(w, "  Archive:  %s (%s)\n", r.Archive, formatBytes(r.ArchiveSize))
	}
}

// printPlan reports what a run would do, from a dry-run result.
func printPlan(w io.Writer, r *Result, opt Options) {
	fmt.Fprintf(w, "Dry run: %s\n", r.Release)
	if r.Dest != "" {
		fmt.Fprintf(w, "  Folder:   %s (%s)\n", r.Dest, targetState(r.DestExists, opt.Force))
	}
	if r.Archive != "" {
		state := targetState(r.ArchiveExists, opt.Force)
		if r.ReplacesSource {
			state = "would replace the source archive"
		}
		fmt.Fprintf(w, "  Archive:  %s (%s, level %s)\n", r.Archive, state, opt.Level)
	}
	fmt.Fprintf(w, "  Content:  %s, %s\n", plural(len(r.Files), "file"), formatBytes(r.Size))
	for _, g := range groupTopLevel(r.Files) {
		fmt.Fprintf(w, "    %-40s %12s %10s\n", g.name, g.count, formatBytes(g.size))
	}
	fmt.Fprintf(w, "  Info:     %s\n", metaLine(r))
	source := "kept"
	if r.DeletesSource || r.ReplacesSource {
		source = "would be deleted"
	}
	fmt.Fprintf(w, "  Source:   %s\n", source)
	if r.Available >= 0 {
		fmt.Fprintf(w, "  Disk:     needs about %s, %s available\n", formatBytes(withMargin(r.Need)), formatBytes(r.Available))
	} else {
		fmt.Fprintf(w, "  Disk:     needs about %s (free space unknown)\n", formatBytes(withMargin(r.Need)))
	}
	if len(r.Problems) > 0 {
		fmt.Fprintln(w, "  Problems:")
		for _, p := range r.Problems {
			fmt.Fprintf(w, "    - %s\n", p)
		}
	}
}

// printList writes one kept file per line.
func printList(w io.Writer, r *Result) {
	for _, f := range r.Files {
		fmt.Fprintln(w, f.Path)
	}
}

func targetState(exists, force bool) string {
	switch {
	case exists && force:
		return "exists, would be replaced"
	case exists:
		return "exists"
	}
	return "would be created"
}

func metaLine(r *Result) string {
	if meta := r.Meta(); len(meta) > 0 {
		return strings.Join(meta, ", ")
	}
	return "none"
}

type topLevelGroup struct {
	name  string
	count string // file count for folders, empty for files
	size  int64
}

// groupTopLevel summarizes files by their first path component.
func groupTopLevel(files []FileEntry) []topLevelGroup {
	type agg struct {
		n     int
		size  int64
		isDir bool
	}
	byName := map[string]*agg{}
	for _, f := range files {
		name, _, isDir := strings.Cut(f.Path, "/")
		a, ok := byName[name]
		if !ok {
			a = &agg{}
			byName[name] = a
		}
		a.n++
		a.size += f.Size
		a.isDir = a.isDir || isDir
	}
	groups := make([]topLevelGroup, 0, len(byName))
	for name, a := range byName {
		g := topLevelGroup{name: name, size: a.size}
		if a.isDir {
			g.name += "/"
			g.count = plural(a.n, "file")
		}
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].name < groups[j].name })
	return groups
}

// plural formats a count with thousands separators: "1 file", "1,223 files".
func plural(n int, word string) string {
	if n != 1 {
		word += "s"
	}
	return formatCount(n) + " " + word
}

func formatCount(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
