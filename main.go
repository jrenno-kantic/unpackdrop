// Command unpackdrop unpacks nested release archives: an outer rar that
// contains zips, which in turn contain a multi-part rar. Only the .diz/.nfo
// metadata and the multi-part rar payload are kept.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/pflag"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// cliOptions holds the parsed command line.
type cliOptions struct {
	output, repack, level                       string
	force, quiet, deleteSource, keepDir         bool
	skipVerify, dryRun, list, showVersion, help bool
	levelSet                                    bool
	archives                                    []string
}

// newFlagSet declares the options. pflag gives every option a short and a long
// name, lets short options be combined (-fq) and accepts options before or
// after the archive names. A backquoted word in a usage string names the
// option's argument in the help.
func newFlagSet(o *cliOptions) *pflag.FlagSet {
	fs := pflag.NewFlagSet("unpackdrop", pflag.ContinueOnError)
	fs.SortFlags = false
	fs.SetOutput(io.Discard) // errors are reported by run
	fs.StringVarP(&o.output, "output", "o", "", "write results to `DIR` (default: same directory as each archive)")
	fs.BoolVarP(&o.force, "force", "f", false, "overwrite an existing destination folder or archive")
	fs.BoolVarP(&o.quiet, "quiet", "q", false, "no progress bar, errors only")
	fs.BoolVarP(&o.deleteSource, "delete-source", "d", false, "delete the source archive after a successful unpack")
	fs.StringVarP(&o.repack, "repack", "r", "", "repack the result into <release>.`FORMAT` (zip, rar or 7z), without a root folder")
	fs.StringVarP(&o.level, "level", "L", "", "with --repack, compression `LEVEL`: store, fast, normal (default) or max")
	fs.BoolVarP(&o.keepDir, "keep-dir", "k", false, "with --repack, also keep the unpacked folder")
	fs.BoolVarP(&o.skipVerify, "skip-verify", "s", false, "with --repack, do not test the new archive (faster, less safe)")
	fs.BoolVarP(&o.dryRun, "dry-run", "n", false, "show what would be kept and where, without writing to the output directory")
	fs.BoolVarP(&o.list, "list", "l", false, "print every kept file (to stdout)")
	fs.BoolVarP(&o.showVersion, "version", "v", false, "print version and exit")
	fs.BoolVarP(&o.help, "help", "h", false, "show this help")
	return fs
}

// parseArgs parses argv into options and archive names.
func parseArgs(argv []string) (*cliOptions, *pflag.FlagSet, error) {
	o := &cliOptions{}
	fs := newFlagSet(o)
	// Checked first: pflag would read -force as -f -o rce and succeed.
	if hint := longOptionHint(fs, argv); hint != "" {
		return nil, fs, errors.New(hint)
	}
	if err := fs.Parse(argv); err != nil {
		return nil, fs, err
	}
	o.levelSet = fs.Changed("level")
	o.archives = fs.Args()
	return o, fs, nil
}

// longOptionHint explains a long option written with one dash, such as
// -version, which pflag would read as the combined short options -v -e -r...
func longOptionHint(fs *pflag.FlagSet, argv []string) string {
	for _, a := range argv {
		if a == "--" {
			break
		}
		name, _, _ := strings.Cut(strings.TrimPrefix(a, "-"), "=")
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(name) > 1 && fs.Lookup(name) != nil {
			return fmt.Sprintf("long options take two dashes: use --%s instead of %s", name, a)
		}
	}
	return ""
}

func usage(w io.Writer, fs *pflag.FlagSet) {
	fmt.Fprintf(w, "Usage: unpackdrop [options] <archive.rar> [archive.rar...]\n\n")
	fmt.Fprintf(w, "Extracts a release rar, unpacks its zips and the inner multi-part rar,\n")
	fmt.Fprintf(w, "then keeps only the .diz/.nfo files and the payload. Requires unrar, rar or\n")
	fmt.Fprintf(w, "unar, and unzip, plus rar or 7zz/7z/7za to repack into those formats.\n")
	fmt.Fprintf(w, "Short options can be combined, e.g. -fq.\n\n")
	fmt.Fprintf(w, "Options:\n%s", fs.FlagUsages())
}

func run(argv []string) int {
	o, fs, err := parseArgs(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\nRun 'unpackdrop --help' for usage.\n", err)
		return 2
	}
	if o.help {
		usage(os.Stdout, fs)
		return 0
	}
	if o.showVersion {
		fmt.Println("unpackdrop", versionString())
		return 0
	}
	if len(o.archives) == 0 {
		usage(os.Stderr, fs)
		return 2
	}
	format, err := parseFormat(o.repack)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	lvl, err := parseLevel(o.level)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 2
	}
	if format == FormatNone {
		for _, o := range []struct {
			name string
			set  bool
		}{{"--keep-dir", o.keepDir}, {"--skip-verify", o.skipVerify}, {"--level", o.levelSet}} {
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
	for _, a := range o.archives {
		opt := Options{
			Archive:      a,
			OutputDir:    o.output,
			Force:        o.force,
			DeleteSource: o.deleteSource,
			Repack:       format,
			Level:        lvl,
			KeepDir:      o.keepDir,
			SkipVerify:   o.skipVerify,
			DryRun:       o.dryRun,
			Progress:     !o.quiet,
		}
		if !o.quiet {
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
		case !o.quiet:
			printSummary(os.Stderr, res)
		}
		if o.list {
			printList(os.Stdout, res)
		}
	}
	if failed > 0 {
		return 1
	}
	return 0
}
