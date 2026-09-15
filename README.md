<!--
SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
SPDX-License-Identifier: GPL-3.0-or-later
-->

# devctl

A developer control tool for the repositories and sandboxes of a working day.

Every subcommand is keyed by the repository the current directory belongs to,
derived from its `origin` remote. The derivation is deterministic, so
`git@github.com:acme/widget.git`, `https://github.com/acme/widget.git` and
`ssh://git@github.com:22/acme/widget` all name the same repository —
`github.com/acme/widget` — no matter which one you happened to clone with.

## Install

devctl lives in a private repository. Both paths below need a GitHub account
with access to it: `go install` authenticates through git, and `gh` through your
`gh auth login` token.

### Build from source

```sh
GOPRIVATE='github.com/brunovenceslau/*' GOBIN="$HOME/.local/bin" \
  go install github.com/brunovenceslau/devctl/cmd/devctl@latest
```

`GOPRIVATE` keeps the request away from `proxy.golang.org` and the checksum
database, neither of which can read a private repository. `GOBIN` puts the
binary in a directory on your PATH, because the default, `$(go env GOPATH)/bin`,
is not on it.

A build from source reports its version as `dev`. The tag is stamped in by the
release build, and `go install` applies no ldflags.

### Install a release binary

Download with `gh`, verify against the published checksums, then extract:

```sh
tag=v0.1.0
asset=devctl_${tag#v}_darwin_arm64.tar.gz   # or darwin_amd64, linux_amd64, linux_arm64

gh release download "$tag" -R brunovenceslau/devctl -p "$asset" -p checksums.txt --clobber
grep "  $asset\$" checksums.txt | shasum -a 256 -c -
mkdir -p "$HOME/.local/bin"
tar -xzf "$asset" -C "$HOME/.local/bin" devctl
```

The archive also carries `LICENSE` and `README.md`. Naming `devctl` in the `tar`
command extracts the binary alone.

`--clobber` lets you run the block again in a directory that already holds an
earlier download. Without it `gh` refuses the whole command rather than replace
a file. Overwriting is safe here because the next line verifies whatever landed.

Confirm the result:

```sh
devctl --version
```

### macOS reports "Apple could not verify devctl is free of malware"

macOS prints that when Gatekeeper evaluates a binary Apple has not notarized.
devctl is not notarized. Notarization requires a paid Apple Developer Program
membership, which this tool does not have.

Gatekeeper only evaluates a file carrying the `com.apple.quarantine` extended
attribute, and that attribute is not part of the download. The program that
fetched the file decides whether to attach it. A web browser always attaches it.
`gh`, `curl`, `wget`, and `go install` never do, so the commands above produce a
binary that runs without a prompt.

To repair a binary already downloaded through a browser, strip the attribute:

```sh
xattr -d com.apple.quarantine "$HOME/.local/bin/devctl"
```

`xattr -l <file>` prints what a file carries. Empty output means Gatekeeper
leaves it alone.

Double-clicking a quarantined archive in Finder copies the attribute onto every
file it extracts. Extracting the same archive with `tar` on the command line
does not.

## `devctl reminders`

A per-repository TODO store that outlives the session that wrote it. An idea
raised mid-task, on a topic unrelated to the work at hand, is otherwise lost
when the session ends.

```sh
devctl reminders add drop the temporary debug flag from the parser
devctl reminders list
devctl reminders reorder 20260915T142233.482913Z-9f3a1c   # bump one to the top
devctl reminders path                                     # where they live
devctl reminders rm 20260915T142233.482913Z-9f3a1c
```

`list` prints one `<id><TAB><text>` record per line, so it pipes. Diagnostics go
to stderr; only records go to stdout.

`-C, --repo` points a command at a repository other than the current directory,
which is what a hook or a script should use rather than changing directory.

### Where the reminders live

```
${DEVCTL_REMINDERS_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/devctl/reminders}/
  <host>/<owner>/<repo>/<scope>/
    items/<id>.md      # one file per reminder — the unit of atomicity
    order/<N>          # versioned order documents; the highest N is current
    tmp/               # staging, deliberately outside the items glob
```

An uppercase letter in the path is encoded as `!` plus its lowercase form, so
`github.com/Acme/Widget` is stored under `github.com/!acme/!widget`. This is
Go's own encoding, the one the module cache uses (`~/go/pkg/mod/github.com` has
`!burnt!sushi` entries for the same reason). It is load-bearing here: a store is
shared between a mac, whose APFS folds case, and a linux sandbox, whose
filesystem does not. Unencoded, `Acme/Widget` and `acme/widget` are two
directories on one side and one on the other, so the two sides disagree about
whether they are looking at the same list. The readable spelling is kept in each
item's `repo:` header.

`DEVCTL_REMINDERS_DIR` exists because `$HOME` is not the same on both sides of a
sandbox boundary: a sandbox is handed the path rather than left to derive a
different one. The store sits under the XDG **data** directory, not a cache or
state directory, because a reminder is your own data and an uninstall must not
take it.

Each item is a plain file. Editing one by hand is a supported way to use this —
`devctl reminders path <id>` exists to hand one to an editor — and a file
dropped into `items/` by any other tool is listed like any other.

### Concurrency

Several sessions, in several sandbox VMs, plus you on the host, operate on one
store at the same time. The store takes **no lock**:

- An item is published with `link(2)`, never by writing to its final name.
  Linking onto a name that exists fails and leaves the existing file
  byte-identical, so one call is both the uniqueness check and the atomic
  publish.
- `add` and `rm` never touch the order document. A new item is simply not in it
  and lists at the end; a removed item's stale entry is skipped on read and
  collected by the next `reorder`.
- `reorder` is the one read-modify-write, and it uses the same primitive as
  optimistic concurrency control: it publishes `order/<N+1>`, and if that name
  is taken it re-reads the winner's result and recomputes rather than
  overwriting it.

Nothing is ever overwritten, and no killed process can leave a stale lock
behind, because there is none to leave.

Every path is resolved through an `os.Root` rooted at the store directory, so a
crafted id cannot address anything outside it by construction rather than by
validation. Go 1.27 is a correctness floor, not a preference: before it, a
symlink opened with a trailing slash escaped a `Root`.

## Shell completion

```sh
devctl completion zsh > "${XDG_CACHE_HOME:-$HOME/.cache}/devctl/_devctl"
```

Generate it once, at install time, and source the cached file — never `eval` a
generator on the shell startup path.

`rm` and `reorder` complete **real stored ids**, each shown with its reminder's
first line as the description.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | success |
| `1` | a runtime failure, or an id with nothing behind it |
| `2` | a bad invocation, or a directory with no usable `origin` |

## Development

```sh
make tools   # install the pinned golangci-lint and govulncheck
make fix     # apply every automatic fix: go fix, the formatters, --fix linters
make ci      # lint + test + govulncheck — must be green before a push
make race    # the multi-process store race gate, verbosely
```

Every gate is a `make` target, and CI invokes the target rather than restating
it, so what CI runs is what a push was checked against locally.
`make race RACE_PROCS=12` runs the store's concurrency gate at full size; the
default is sized for CI. `make race RACE_STORE_DIR=/path` runs it against a
filesystem of your choosing, which is how the store's invariants were checked
over a virtiofs mount rather than assumed to hold there.

### Commit hook

`.devctl/hooks/pre-commit` runs `make pre-commit`, which applies every fix a
tool can apply on its own, and then refuses the commit if anything changed. It
refuses rather than amending on purpose: a hook that rewrites files and lets the
commit through commits something you never read.

Install it:

```sh
devctl setup hooks
```

That points git's `core.hooksPath` at `.devctl/hooks`, which covers every hook
at once and is undone with `git config --unset core.hooksPath`. git reads hooks
from only one directory, so anything already in `.git/hooks` stops running;
`devctl setup hooks` says so when that is the case, and `--symlink` links each
hook individually instead, which keeps them.

`--force` replaces a conflicting setting and moves any file in the way to
`<name>.bak`. It never deletes, and it never overwrites an existing `.bak`: the
first backup is the pristine one.

The command works from any subdirectory of the repository, and fails with the
reason if it is run outside one.

The hooks are the repository's own tracked files, so installing them means its
content runs on every commit. Install them in repositories whose contents you
would run anyway.

`make pre-commit` is deliberately a fast subset rather than `make ci`. A hook
slow enough to be annoying is a hook that gets `--no-verify`d, and then it
guards nothing.

## License

    devctl, a developer control tool for repositories and sandboxes
    Copyright (C) 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>

    This program is free software: you can redistribute it and/or modify
    it under the terms of the GNU General Public License as published by
    the Free Software Foundation, either version 3 of the License, or
    (at your option) any later version.

    This program is distributed in the hope that it will be useful,
    but WITHOUT ANY WARRANTY; without even the implied warranty of
    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
    GNU General Public License for more details.

    You should have received a copy of the GNU General Public License
    along with this program.  If not, see <https://www.gnu.org/licenses/>.

The full text is in [LICENSE](LICENSE), verbatim from the Free Software
Foundation. Every file that can hold a comment carries an SPDX tag pointing
here, which `make license-check` enforces:

```
SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
SPDX-License-Identifier: GPL-3.0-or-later
```

`go.sum` has no comment syntax and so carries no tag, and a licence text is
never edited.
