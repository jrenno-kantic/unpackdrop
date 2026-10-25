# Unpack Drop

A command-line tool that unpacks nested release archives, a rar that holds zips
that hold a multi-part rar, and keeps only what matters: the `.diz` and
`.nfo` files and the contents of the multi-part rar. It can also repack the
result into a single zip, rar or 7z archive.

```
Release.rar
└── Release/
    ├── file_id.diz, group.nfo
    └── *.zip                      each: [folder/] name.partN.rar, file_id.diz, group.nfo
        └── name.part1..N.rar      the actual content
```

becomes

```
Release/
├── file_id.diz
├── group.nfo
└── …contents of the multi-part rar
```

Every `.nfo` and `.diz` file is kept, whatever its name or letter case. A
warning is printed if a release contains no `.nfo` file.
When the outer folder and the zips contain files with the same name, the copy
from the outer folder is kept.

Everything else (the zips, the rar volumes, duplicate nfo/diz copies) is deleted.

## Requirements

- Go 1.27+ to build
- `unzip` and one RAR extractor in `PATH`, tried in this order: `unrar`, `rar`,
  then `unar` (with its companion `lsar`).
- To repack: `rar` for rar, `7zz`, `7z` or `7za` for 7z. Zip is written natively.

On macOS, `unzip` is built in. Homebrew disabled its `rar` cask (which also
provided `unrar`) in September 2026, so use `unar` from Homebrew, which has its
own RAR5 decoder:

```bash
brew install unar sevenzip
```

To repack to rar, install RARLAB's `rar` from
[rarlab.com](https://www.rarlab.com/download.htm). Homebrew's `7zz` cannot be
used to extract: it is built without RAR decompression and fails on compressed
entries.

On Debian or Ubuntu (`rar` and `unrar` are in the non-free/multiverse
repositories):

```bash
sudo apt-get install unrar rar 7zip unzip
```

## Install

### Homebrew (macOS)

unpackdrop is published as a cask in
[jrenno-kantic/homebrew-tap](https://github.com/jrenno-kantic/homebrew-tap):

```bash
brew install --cask jrenno-kantic/tap/unpackdrop
```

This adds the tap, installs `unpackdrop` and its `unar` dependency for RAR
extraction. `unzip` ships with macOS. Check the install with:

```bash
unpackdrop --version
```

Optional tools, only needed to repack:

- 7z: `brew install sevenzip`
- rar: RARLAB's `rar` from [rarlab.com](https://www.rarlab.com/download.htm)
  (Homebrew no longer ships it)

To upgrade or remove it:

```bash
brew upgrade --cask unpackdrop
brew uninstall --cask unpackdrop
```

The binary is not signed by Apple. The cask clears its quarantine attribute
after installing, so macOS does not block it on first launch.

### Linux

Download the archive for your platform (`linux_amd64` or `linux_arm64`) from the
[releases page](https://github.com/jrenno-kantic/unpackdrop/releases), check it
against `checksums.txt` and put `unpackdrop` in your `PATH`. For example, for
1.1.0 (replace the version with the latest one):

```bash
curl -LO https://github.com/jrenno-kantic/unpackdrop/releases/download/v1.1.0/unpackdrop_1.1.0_linux_amd64.tar.gz
tar -xzf unpackdrop_1.1.0_linux_amd64.tar.gz unpackdrop
sudo install unpackdrop /usr/local/bin/
```

Then install the archivers listed under [Requirements](#requirements).

### From source

```bash
go install .
```

Or build a local binary with [Task](https://taskfile.dev):

```bash
task build
```

## Usage

```bash
unpackdrop [options] <archive.rar> [archive.rar...]
```

| Option | Description |
|---|---|
| `-o, --output DIR` | Output directory (default: the directory of each archive) |
| `-f, --force` | Overwrite an existing destination folder or archive |
| `-q, --quiet` | No progress bar, errors only |
| `-d, --delete-source` | Delete the source archive after a successful unpack |
| `-r, --repack FORMAT` | Repack the result into `<Release>.<FORMAT>` (`zip`, `rar` or `7z`) |
| `-L, --level LEVEL` | With `--repack`, compression: `store`, `fast`, `normal` (default) or `max` |
| `-k, --keep-dir` | With `--repack`, also keep the unpacked folder |
| `-s, --skip-verify` | With `--repack`, do not test the new archive |
| `-n, --dry-run` | Show what would be kept and where, without writing to the output directory |
| `-l, --list` | Print every kept file, one per line, to stdout |
| `-v, --version` | Print the version and exit |
| `-h, --help` | Show the help |

Options can come before or after the archive names. Short options can be
combined, and a short option's value can follow it directly:

```bash
unpackdrop -fqk -r7z -Lfast Release.rar   # same as -f -q -k -r 7z -L fast
```

Long options take two dashes (`--force`). A long name written with one dash,
such as `-force`, is refused with a hint rather than read as `-f -o rce`. Use
`--` to pass an archive whose name starts with a dash.

### Examples

Unpack next to the archive:

```bash
unpackdrop Release.rar
```

Unpack several archives into another folder:

```bash
unpackdrop -o ~/Releases *.rar
```

Repack into a 7z archive, keeping the unpacked folder as well:

```bash
unpackdrop -r 7z -k -o ~/Releases Release.rar
```

Check what a run would do before doing it:

```bash
unpackdrop -n -r 7z Release.rar
```

Repack quickly into a 7z, without testing it afterwards:

```bash
unpackdrop -r 7z -L fast -s Release.rar
```

Replace the source rar with a clean, repacked one:

```bash
unpackdrop -r rar -d Release.rar
```

## Progress

Each step shows a progress bar with percentage, size, speed and ETA:

```
[1/5] Extracting archive [==============================] 100%  68.5 MiB/68.5 MiB  1.2 GiB/s  ETA 0s
[2/5] Unpacking 15 zip(s) [==============================] 100%  68.5 MiB/68.5 MiB  131.2 MiB/s  ETA 0s
[3/5] Extracting payload [==============================] 100%  190.9 MiB/190.9 MiB  161.4 MiB/s  ETA 0s
[4/5] Packing 7z (normal) [==============================] 100%  190.9 MiB/190.9 MiB  7.3 MiB/s  ETA 0s
[5/5] Verifying archive [==============================] 100%  190.9 MiB/190.9 MiB  143.2 MiB/s  ETA 0s
```

Without `--repack` there are 3 steps, and with `--skip-verify` there are 4. When
the output is not a terminal (a log file or a pipe), one line is printed per
step instead.

At the end, a short summary is printed:

```
Done: Some.App.v2.0-GRP
  Content:  1,223 files, 190.9 MiB
  Info:     file_id.diz, grp.nfo
  Folder:   /Users/me/Releases/Some.App.v2.0-GRP
```

Progress and the summary go to stderr and are hidden by `--quiet`. The file list
from `--list` and the plan from `--dry-run` go to stdout and are printed even
with `--quiet`, so they can be piped:

```bash
unpackdrop -q -l Release.rar | grep '\.app/'
```

## Dry run

`--dry-run` shows what a run with the same options would do: the folder and
archive it would create, the files it would keep, whether the source would be
deleted and how much disk space it needs.

```
Dry run: Some.App.v2.0-GRP
  Archive:  /Users/me/Releases/Some.App.v2.0-GRP.7z (would be created, level fast)
  Content:  1,223 files, 190.9 MiB
    Extras/                                       2 files    4.0 MiB
    Some.App.app/                             1,219 files  186.9 MiB
    file_id.diz                                                363 B
    grp.nfo                                                  8.2 KiB
  Info:     file_id.diz, grp.nfo
  Source:   kept
  Disk:     needs about 432.9 MiB, 608.8 GiB available
```

The list of files lives inside the rar volumes, which live inside the zips, so
it cannot be read without taking the volumes out. A dry run therefore extracts
the outer archive and the zips into the system temporary folder, lists the
multi-part rar without extracting it, then deletes the temporary folder. Nothing
is written to the output directory, which does not even need to exist yet.

Anything that would make the real run fail (an existing destination, not enough
disk space, overwriting the source) is listed under `Problems:`, and the exit
code is then `1`.

## Repacking

- The archive is named after the release folder and placed in the output
  directory. Files are stored at the root of the archive, without the release
  folder.
- Symlinks and file permissions are preserved in all three formats.
- The archive is tested before anything is removed: `rar t`, `7z t`, or
  reading every zip entry back and checking its CRC. `--skip-verify` skips this
  test, which is faster but means the unpacked folder may be deleted without
  confirming that the archive extracts.
- Unless `--keep-dir` is set, the unpacked folder is removed once the archive is
  in place.
- With the default output directory, `--repack rar` would produce a file with
  the same name as the source. This is refused unless `--delete-source` is set,
  in which case the new archive replaces the source.

`--level` maps to each tool's own setting. Release content is mostly already
compressed (`.dmg`, apps), so `fast` is often the best trade-off. Measured on a
191 MB release (times include about 2 s of unpacking):

| Format | `store` | `fast` | `normal` | `max` |
|---|---|---|---|---|
| zip (deflate) | 191 MB · 2.1 s | 113 MB · 3.7 s | 97 MB · 4.7 s | 97 MB · 17.8 s |
| rar (`-m0` … `-m5`) | 191 MB · 2.2 s | 99 MB · 3.6 s | 84 MB · 5.2 s | 84 MB · 7.2 s |
| 7z (`-mx0` … `-mx9`) | 191 MB · 2.3 s | 81 MB · 3.9 s | 52 MB · 28.6 s | 49 MB · 51.2 s |

## Safety

- All work happens in a hidden `.unpackdrop-*` folder inside the output
  directory and is removed on success, failure or Ctrl+C.
- Results are moved into place only at the end. An existing destination is
  refused without `--force`, and with `--force` it is replaced only at that
  last step, so an interrupted run never leaves a half-written result.
- Free disk space is checked before each step that writes a lot: before
  extracting the archive, before extracting the payload and before repacking.
  The run stops with `not enough disk space in DIR for …: need about X, Y
  available` instead of failing halfway. The payload's size is only known once
  the volumes are out of the zips, so that check happens after step 2. Use
  `--dry-run` to see the whole estimate up front. Volumes are deleted as soon as
  the payload is extracted, so repacking does not need room for them.
- Missing or duplicated rar volumes are reported before extraction.
- Password-protected archives fail immediately instead of waiting for input.
- Extraction warnings (exit code 1 from `unrar` or `unzip`) are tolerated.
  Packing warnings are not, because the unpacked folder may be deleted
  afterwards.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | At least one archive failed, a dry run found problems, or a required tool is missing |
| `2` | Invalid command-line usage |
| `130` | Interrupted (Ctrl+C or SIGTERM) |

When several archives are given, a failure does not stop the others; an
interrupt stops the whole run.

## Development

Common tasks are defined in [Taskfile.yml](Taskfile.yml):

| Task | Description |
|---|---|
| `task build` | Build `./unpackdrop` |
| `task snapshot` | Build all release archives into `dist/` with GoReleaser, without publishing |
| `task release:check` | Validate `.goreleaser.yml` |
| `task tag` | Tag the current commit as `v<VERSION>` |
| `task install` | Install into `$GOPATH/bin` |
| `task run -- <args>` | Build and run with arguments |
| `task test` | Run the tests |
| `task test:race` | Run the tests with the race detector |
| `task cover` | Run the tests with a coverage summary |
| `task fmt` / `vet` / `lint` / `tidy` | Format, `go vet`, `golangci-lint`, `go mod tidy` |
| `task check` | Format check, vet, lint and race tests |
| `task clean` | Remove build artifacts and the test cache |
| `task version` | Print the version builds will carry |

The round-trip tests for rar and 7z are skipped when those tools are not
installed.

The only Go dependency is [spf13/pflag](https://github.com/spf13/pflag), for
command-line options.

## Versioning

The version comes from the [VERSION](VERSION) file, which is compiled into the
binary. To release a new version, edit that file. A single build can override
it:

```bash
task build VERSION=1.2.0
# or
go build -ldflags "-X main.version=1.2.0" .
```

When built from a git checkout, the commit is appended, for example
`1.0.0 (1c157447169d)`, with `-dirty` if there were uncommitted changes.

## Releasing

Releases are built by [GoReleaser](https://goreleaser.com) in GitHub Actions
when a `v*` tag is pushed ([release.yml](.github/workflows/release.yml)). Each
release publishes macOS and Linux archives for amd64 and arm64 with a
`checksums.txt`, and updates `Casks/unpackdrop.rb` in
[jrenno-kantic/homebrew-tap](https://github.com/jrenno-kantic/homebrew-tap).

The tap is updated with the `HOMEBREW_TAP_GITHUB_TOKEN` Actions secret: a
fine-grained personal access token with **Contents: Read and write** on
`jrenno-kantic/homebrew-tap` only. Without it, releases still publish but the
cask is not pushed to the tap. When the token expires, the tap step fails:
create a new one and replace the secret with
`gh secret set HOMEBREW_TAP_GITHUB_TOKEN --repo jrenno-kantic/unpackdrop`.

For each release:

1. Set the new version in [VERSION](VERSION) and commit.
2. Run `task tag`, then `git push origin v<VERSION>`.

The workflow refuses a tag that does not match `VERSION`, so `--version` is the
same for released and source builds. Tags with a prerelease suffix, such as
`v1.1.0-rc1`, publish a GitHub prerelease without updating the tap.

The binary is not signed by Apple, so the cask clears the quarantine attribute
after installing it. Without that, macOS would refuse to run it on first launch.

[CI](.github/workflows/ci.yml) runs the tests on Linux and macOS with the
archivers installed, lint, and `goreleaser check` on every push.
