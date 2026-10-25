// Command unpackdrop unpacks scene-style release archives: an outer rar that
// contains zips, which in turn contain a multi-part rar. Only the .diz/.nfo
// metadata and the multi-part rar payload are kept.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	fs := flag.NewFlagSet("unpackdrop", flag.ContinueOnError)

	// Every option has a short and a long name bound to the same variable.
	type optionHelp struct{ short, long, arg, usage string }
	var help []optionHelp
	stringOpt := func(short, long, arg, usage string) *string {
		p := new(string)
		fs.StringVar(p, short, "", usage)
		fs.StringVar(p, long, "", usage)
		help = append(help, optionHelp{short, long, arg, usage})
		return p
	}
	boolOpt := func(short, long, usage string) *bool {
		p := new(bool)
		fs.BoolVar(p, short, false, usage)
		fs.BoolVar(p, long, false, usage)
		help = append(help, optionHelp{short, long, "", usage})
		return p
	}

	output := stringOpt("o", "output", "DIR", "output directory (default: same directory as each archive)")
	force := boolOpt("f", "force", "overwrite an existing destination folder or archive")
	quiet := boolOpt("q", "quiet", "no progress bar, errors only")
	deleteSource := boolOpt("d", "delete-source", "delete the source archive after a successful unpack")
	repack := stringOpt("r", "repack", "FORMAT", "repack the result into <release>.<FORMAT> (zip, rar or 7z), without a root folder")
	level := stringOpt("L", "level", "LEVEL", "with --repack, compression: store, fast, normal (default) or max")
	keepDir := boolOpt("k", "keep-dir", "with --repack, also keep the unpacked folder")
	skipVerify := boolOpt("s", "skip-verify", "with --repack, do not test the new archive (faster, less safe)")
	dryRun := boolOpt("n", "dry-run", "show what would be kept and where, without writing to the output directory")
	list := boolOpt("l", "list", "print every kept file (to stdout)")
	showVersion := boolOpt("v", "version", "print version and exit")
	help = append(help, optionHelp{"h", "help", "", "show this help"})

	fs.Usage = func() {
		w := fs.Output()
		fmt.Fprintf(w, "Usage: unpackdrop [options] <archive.rar> [archive.rar...]\n\n")
		fmt.Fprintf(w, "Extracts a release rar, unpacks its zips and the inner multi-part rar,\n")
		fmt.Fprintf(w, "then keeps only the .diz/.nfo files and the payload. Requires unrar (or rar)\n")
		fmt.Fprintf(w, "and unzip, plus rar or 7zz/7z/7za to repack into those formats.\n\n")
		fmt.Fprintf(w, "Options:\n")
		for _, h := range help {
			names := fmt.Sprintf("-%s, --%s", h.short, h.long)
			if h.arg != "" {
				names += " " + h.arg
			}
			fmt.Fprintf(w, "  %-26s %s\n", names, h.usage)
		}
	}

	// Allow flags to be placed before or after positional arguments.
	var archives []string
	rest := argv
	for {
		if err := fs.Parse(rest); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		archives = append(archives, rest[0])
		rest = rest[1:]
	}

	if *showVersion {
		fmt.Println("unpackdrop", versionString())
		return 0
	}
	if len(archives) == 0 {
		fs.Usage()
		return 2
	}
	format, err := parseFormat(*repack)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	lvl, err := parseLevel(*level)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	if format == FormatNone {
		for _, o := range []struct {
			name string
			set  bool
		}{{"--keep-dir", *keepDir}, {"--skip-verify", *skipVerify}, {"--level", *level != ""}} {
			if o.set {
				fmt.Fprintf(os.Stderr, "error: %s requires --repack\n", o.name)
				return 2
			}
		}
	}
	if err := checkTools(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if _, err := format.tool(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	failed := 0
	for _, a := range archives {
		opt := Options{
			Archive:      a,
			OutputDir:    *output,
			Force:        *force,
			DeleteSource: *deleteSource,
			Repack:       format,
			Level:        lvl,
			KeepDir:      *keepDir,
			SkipVerify:   *skipVerify,
			DryRun:       *dryRun,
			Progress:     !*quiet,
		}
		if !*quiet {
			opt.Log = os.Stderr
			fmt.Fprintf(os.Stderr, "==> %s\n", a)
		}
		res, err := Unpack(ctx, opt)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(os.Stderr, "\ninterrupted, temporary files removed")
				return 130
			}
			fmt.Fprintf(os.Stderr, "error: %s: %v\n", a, err)
			failed++
			continue
		}
		switch {
		case res.DryRun:
			printPlan(os.Stdout, res, opt)
			if len(res.Problems) > 0 {
				failed++
			}
		case !*quiet:
			printSummary(os.Stderr, res)
		}
		if *list {
			printList(os.Stdout, res)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}
