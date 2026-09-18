<!--
SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
SPDX-License-Identifier: GPL-3.0-or-later
-->

# canga

One binary, `canga`, built in two roles for the repositories and sandboxes of a
working day:

| Build | Runs on | What it does |
| --- | --- | --- |
| host | your Mac (darwin and linux builds) | clone, sync, reminders, git hook setup, its own upgrade |
| sandbox | inside an agent sandbox (linux) | reminders `list` and `add`, nothing else |

Both builds are named `canga` and share one reminders list. The role is fixed
when the binary is built: the sandbox build does not contain the host's
commands, so nothing an agent sets at runtime can reach them. `canga --version`
names the role.

In Brazilian barracks slang, your *canga* is your partner in a pair: the one
you do not leave and who does not leave you. Each build covers one side, the
Mac and the sandbox, and neither makes sense without the other.

Every subcommand is keyed by the repository the current directory belongs to,
derived from its `origin` remote. The derivation is deterministic, so
`git@github.com:acme/widget.git`, `https://github.com/acme/widget.git` and
`ssh://git@github.com:22/acme/widget` all name the same repository —
`github.com/acme/widget` — no matter which one you happened to clone with.

## Install

These install the host build, on your machine. For a sandbox, see
[Install it in a sandbox](#install-it-in-a-sandbox). Neither path needs a
GitHub account or a credential.

### Build from source

```sh
GOBIN="$HOME/.local/bin" go install github.com/brunovenceslau/canga/cmd/host/canga@latest
```

`GOBIN` puts the binary in a directory on your PATH, because the default,
`$(go env GOPATH)/bin`, is not on it.

`go install` on a module path applies no ldflags, so a binary built this way
reports its version as `dev`. To stamp the git description in, build from a
checkout with `make install` instead.

### Install a release binary

Run the install script. It downloads the newest release for your system,
verifies it against the release's `checksums.txt`, and puts `canga` in
`~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/brunovenceslau/canga/main/install_host.sh | sh
```

To install one release instead of the newest, pass its tag:

```sh
curl -fsSL https://raw.githubusercontent.com/brunovenceslau/canga/main/install_host.sh | sh -s -- v0.5.0
```

The script stops before extracting anything when the checksum does not match,
or when `checksums.txt` has no line for the archive. It prints the installed
version last, and says so when `~/.local/bin` is not on your PATH.

To do the same by hand, download, verify against the published checksums, then
extract:

```sh
releases=https://github.com/brunovenceslau/canga/releases
tag=$(basename "$(curl -fsSL -o /dev/null -w '%{url_effective}' "$releases/latest")")
asset=canga-host_${tag#v}_darwin_arm64.tar.gz   # or darwin_amd64, linux_amd64, linux_arm64

curl -fsSLO "$releases/download/$tag/$asset"
curl -fsSLO "$releases/download/$tag/checksums.txt"
mkdir -p "$HOME/.local/bin"
awk -v a="$asset" '$2 == a' checksums.txt | shasum -a 256 -c - \
  && tar -xzf "$asset" -C "$HOME/.local/bin" canga
```

`$releases/latest` redirects to the newest release, so `%{url_effective}` names
its tag without parsing any JSON. `-L` makes curl follow that redirect; without
it, the effective URL is `$releases/latest` itself and the tag comes out as
`latest`.

Read the last two lines as one command. `&&` is what makes the checksum a gate:
without it a pasted block runs every line in turn, and a `FAILED` verification is
followed by the extraction it was supposed to stop.

`awk` selects the one line of `checksums.txt` naming this asset, matching the
filename field for equality. `grep` would read the dots in the filename as
wildcards. An asset absent from `checksums.txt` yields no line, and `shasum`
rejects empty input rather than reporting success.

Running the block again in a directory that already holds an earlier download
overwrites it: `curl -O` replaces a file rather than refusing.

The archive also carries `LICENSE` and `README.md`. Naming `canga` in the `tar`
command extracts the binary alone.

Confirm the result. It prints the version, the role (`host`), the commit it was
built from, and the Go version:

```sh
canga --version
```

Do this once. From here on `canga upgrade` replaces the binary for you.

### macOS reports "Apple could not verify canga is free of malware"

macOS prints that when Gatekeeper evaluates a binary Apple has not notarized.
canga is not notarized. Notarization requires a paid Apple Developer Program
membership, which this tool does not have.

Gatekeeper only evaluates a file carrying the `com.apple.quarantine` extended
attribute, and that attribute is not part of the download. The program that
fetched the file decides whether to attach it. Safari, Chrome and Firefox attach
it. `gh`, `curl`, `wget`, and `go install` do not, so the commands above produce
a binary that runs without a prompt.

Ask a specific file whether it carries the attribute:

```sh
xattr -p com.apple.quarantine <path-to-canga>
```

It prints the attribute, or `No such xattr` when there is none. Use it rather
than `xattr -l`, which lists every extended attribute: a file can carry
`com.apple.metadata:kMDItemWhereFroms` and nothing else, and Gatekeeper leaves
that file alone.

To repair a binary that does carry it, strip the attribute:

```sh
xattr -d com.apple.quarantine <path-to-canga>
```

Double-clicking a quarantined archive in Finder copies the attribute onto every
file it extracts, so the `canga` it leaves next to the archive is quarantined
and stays so when you move it. Extracting the same archive with `tar` on the
command line does not.

## `canga clone`

Clones a repository into the deterministic layout, so the same repository lands
at the same path on every machine, whichever protocol you cloned it with.

```sh
canga clone git@github.com:acme/widget.git
# → ~/src/github.com/acme/widget

cd "$(canga clone https://github.com/acme/widget)"
canga clone https://github.com/acme/widget /tmp/scratch   # an explicit target
```

The resolved path is the only thing printed on stdout. git's progress and every
diagnostic go to stderr, which is what makes the command substitution above
safe.

### Where a clone lands

```
${CANGA_HOST_BASE_DIR:-$HOME/src}/<host>/<owner>/<repo>
```

The three segments come from the URL, with the scheme, any userinfo, any port
and any `.git` suffix removed. Nested owners are kept, so a GitLab subgroup
lands at `gitlab.com/group/sub/proj`. The spelling is the repository's own: only
the reminder store, which is shared between a case-folding and a case-sensitive
filesystem, encodes case.

| Variable | Default | What it sets |
| --- | --- | --- |
| `CANGA_HOST_BASE_DIR` | `$HOME/src` | Root of the layout. |
| `CANGA_HOST_SIGNING_KEY` | `git config --global user.signingkey` | Key stamped into the clone. |
| `CANGA_HOST_ALLOWED_SIGNERS` | `git config --global gpg.ssh.allowedSignersFile` | Allowed-signers file wired into the clone, so `git log --show-signature` works there. |
| `CI` | unset | When set to anything, drops git's `\r` progress meter. |

### What it refuses

A target that already holds anything is refused, and exits 1. An existing empty
directory is fine. Nothing here ever merges into, or writes over, a tree that is
already there.

A URL no path can be derived from is refused before anything is created, and
exits 2. Any credential in it is stripped from the message first.

### Transport hardening

Every clone runs with four git options on the command line, where no repository
or user configuration can override them:

```
-c protocol.ext.allow=never -c protocol.fd.allow=never
-c transfer.fsckObjects=true -c fetch.fsckObjects=true
```

The `ext` and `fd` remote helpers run the rest of the URL as a command, so a URL
such as `ext::sh -c …` is a command execution dressed as a repository. Turning
them off on the command line means a machine whose git config sets
`protocol.ext.allow=always` still refuses one.

One setting outranks a `-c` option: the `GIT_ALLOW_PROTOCOL` environment
variable, which replaces git's protocol policy when it is set. canga removes
`ext` and `fd` from it before running git and keeps every other entry, so an
allow-list such as `https:ssh` still restricts what it restricted. A list that
named only `ext` is left empty, which allows nothing. `file` is left at git's default,
allowed, so local-path clones keep working. The two `fsckObjects` options make
the fetch reject a malformed object graph rather than write it to disk first.

### Signing

After the clone, SSH signing is written into the new repository's **local**
config: the allowed-signers file when one resolves, and `gpg.format=ssh`,
`user.signingkey`, `commit.gpgsign` and `tag.gpgsign` when a key resolves.

`gpg.format` is written rather than inherited. git's default format is openpgp,
so on a machine that does not set `gpg.format=ssh` globally, a stamped SSH key
would fail every commit with `gpg: skipped "…": No secret key`. The consequence
is deliberate: a clone made by `canga clone` signs with SSH, so do not hand a
GPG key to `CANGA_HOST_SIGNING_KEY` or leave one in the global `user.signingkey` and
expect it to be used here.

The key fallback reads the **global** git config rather than the effective one,
so the key is the machine's identity and never the local key of whatever
repository you ran the command in. Nothing global is written.

When neither a key nor an allowed-signers file resolves, the clone is left
alone rather than pointed at a file that does not exist. `canga` then reports
on stderr whether the clone signs anyway, because git configuration outside it
turns `commit.gpgsign` on. A sandbox is that case: its `/etc/gitconfig` carries
the format, the flag and a key command, and no `user.signingkey`. Otherwise it
reports `signing OFF` and names what to set.

## `canga sync`

Fetches every remote with `--prune` and `--tags`, then fast-forwards each local
branch that tracks an upstream.

```sh
canga sync                                  # the repository you are standing in
canga sync -C ~/src/github.com/acme/widget  # or any other

# a sweep across every clone in the layout
find ~/src -name .git -maxdepth 4 -type d -exec dirname {} \; | while read -r r; do
  canga sync -C "$r"
done
```

A branch that MOVED is printed on stdout as `<branch><TAB><upstream>`, one per
line, so a sweep pipes and the output is a change log rather than an inventory.
A branch with nothing to bring in prints nothing at all. Refusals and the
dirty-tree notice go to stderr.

### What it never does

It never resets, forces, merges non-linearly or deletes anything. Every outcome
is therefore recoverable, which is what makes it safe to run across every
repository on a machine without reading them first.

| Situation | What happens |
| --- | --- |
| Modified tracked files | The run stops before any branch is touched, and says so. Exit 0. |
| Untracked files only | The tree does not count as dirty, and the sync proceeds. |
| Branch already level with, or ahead of, its upstream | Nothing to bring in, and nothing printed. |
| Branch diverged from its upstream | Reported on stderr in git's own words, and left exactly where it is. Exit 0. |
| Branch git refuses for another reason | Same: git's words are printed rather than a guess at them. A branch checked out in a linked worktree, a rebase in progress, or an incoming commit that would overwrite an untracked file all land here. |
| Branch with no upstream | Left out of the report. Nothing was ever asked of it. |
| Branch whose upstream was deleted | Reported on stderr as `upstream <remote>/<branch> is gone`, and left where it is. It may hold commits that were never pushed. Exit 0. |
| Detached HEAD | Not an error. Every branch is updated without a checkout. |
| Fetch failed | Exit 1. Deciding branch states against a stale view of the remote would be guessing. |
| Interrupted with Ctrl-C | Exit 1, naming the cancellation. A branch that was never asked about is never reported as refused. |
| Bare repository, or not a repository | Exit 2. Both arms below need a working tree. |

Each branch is asked first whether its upstream is already an ancestor of it. If
it is, there is nothing to fast-forward and nothing is run. That question is not
an optimization: without it the two arms below disagree about the same
repository, because `merge --ff-only` answers "Already up to date" for a branch
that is ahead of its upstream while `fetch` refuses the same state.

The checked-out branch then advances with `git merge --ff-only`, which refuses
rather than touch a working tree it would have to change. Every other branch
advances with `git fetch . <upstream>:<branch>`, and the missing `+` in front of
that refspec is the safety property itself: without it git refuses a non
fast-forward update instead of overwriting the branch.

The fetch carries the same transport hardening as `canga clone`.

## `canga upgrade`

Replaces the running binary with a published release, in place.

```sh
canga upgrade                # install the newest release
canga upgrade --check        # say what is available, change nothing
canga upgrade --tag v0.1.0   # install exactly that release
```

Only the release tag goes to stdout, one line, so `v=$(canga upgrade)` is the
version now installed. Everything else is a diagnostic on stderr.

**This is not `dotfiles-upgrade`.** That one fetches git and updates a checkout.
This one downloads a release artifact and swaps one executable file for another.
The two share a verb and nothing else.

### What it verifies

The archive is checked against the SHA-256 the release publishes in
`checksums.txt`, and an archive with no line of its own there is refused rather
than waved through.

Be clear about what that proves: the bytes downloaded are the bytes the release
names, so a corrupted or truncated download is caught. It does **not** prove the
release is genuine — the same account publishes the asset and the checksum
beside it. That is integrity, not authenticity.

The new binary is then written beside the old one, flushed to disk, and **run
once** to confirm it reports the version it was downloaded as. Only then is it
renamed over the target. An archive holding something that is not canga, or a
binary for the wrong platform, fails at that step with the working binary
untouched, instead of after taking its place on your PATH.

Nothing is written outside the directory the binary already lives in, and no
backup is left behind: the previous release is always one `canga upgrade --tag`
away, and a stale `canga.bak` on PATH is a worse problem than the backup solves.

### Which file it replaces

The path is resolved through symlinks, and the resolved file is the one
replaced. `~/.local/bin` holds symlinks from the dotfiles link engine, and
replacing the *name* rather than the file behind it would quietly turn one of
those links into a regular file.

Either way the command prints the path it is about to write, and `--check`
prints the same one without touching it. Whether it also reports having followed
a symlink is up to the operating system rather than to how you invoked it: on
linux the kernel hands back an already-resolved path, so there is no symlink
left to mention, while on macOS it does not. The file replaced is the right one
on both.

The mode of the file being replaced is kept, so a canga deliberately installed
`0700` does not come back world-executable — only the owner's execute bit is
restored unconditionally, since an install you cannot run is not one.

### The token

A GitHub token is optional: with none, `canga upgrade` reads the release
anonymously. What a token buys is GitHub's authenticated rate limit, 5000
requests an hour against 60 for an anonymous client, which one shared outbound
address can exhaust on its own.

`GH_TOKEN` is read first, then `GITHUB_TOKEN`, and failing both, whatever `gh
auth token` answers. That last one is why a token you never exported is still
used on a mac: `gh` keeps it in the keychain. `gh` is optional — export
`GH_TOKEN` and it is never consulted, install neither and the upgrade still
runs.

A token GitHub rejects, such as an expired or revoked one, does not stop the
upgrade. canga retries that request without it and makes every later request
anonymously. If the anonymous request fails too, the error names both failures,
so the stale token is still reported.

### When it refuses to guess

A binary built by `go install`, or from a tree that has moved past its last tag,
reports `dev` or `v0.1.0-3-gabc1234` rather than a release. Neither names a
published artifact, and `v0.1.0-3-gabc1234` sorts *below* `v0.1.0` under semver
— so treating it as a release would offer older code as an upgrade. Those builds
are refused, and `--tag` is how you say which release you meant:

```sh
canga upgrade --tag v0.1.0
```

## `canga reminders`

A per-repository TODO store that outlives the session that wrote it. An idea
raised mid-task, on a topic unrelated to the work at hand, is otherwise lost
when the session ends.

```sh
canga reminders add drop the temporary debug flag from the parser
canga reminders list
canga reminders reorder 20260915T142233.482913Z-9f3a1c   # bump one to the top
canga reminders path                                     # where they live
canga reminders rm 20260915T142233.482913Z-9f3a1c
```

`list` prints one `<id><TAB><text>` record per line, so it pipes. Diagnostics go
to stderr; only records go to stdout.

`-C, --repo` points a command at a repository other than the current directory,
which is what a hook or a script should use rather than changing directory.

### Where the reminders live

```
${CANGA_REMINDERS_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/canga/reminders}/
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

`CANGA_REMINDERS_DIR` is read by both builds. It exists because `$HOME` is not
the same on both sides of a sandbox boundary: a sandbox is handed the host's
path rather than left to derive a different one. On the host it is normally
unset. The store sits under the XDG **data** directory, not a cache or
state directory, because a reminder is your own data and an uninstall must not
take it.

Each item is a plain file. Editing one by hand is a supported way to use this —
`canga reminders path <id>` exists to hand one to an editor — and a file
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

## The sandbox build

The sandbox build is what an agent inside a sandbox runs. It reads and adds to
the same reminders the host build manages, and does nothing else.

```sh
canga reminders list
canga reminders add check the retry budget before merging
```

Both verbs behave exactly as on the host: `list` prints one `<id><TAB><text>`
record per line, `add` prints the new id, and `-C` names a repository other
than the current directory.

### What it leaves out

The sandbox build has no `clone`, `sync`, `setup`, `upgrade` or `completion`,
and no `reminders rm`, `reorder` or `path`. An agent may surface the list and
record an idea, but the list belongs to the person on the host, so only the
host build removes or reorders it.

Those commands are not compiled into the sandbox build. Running one refuses
with exit `2` and says where it lives, and help does not list them:

```
$ canga reminders rm 20260918T015659.641699Z-125ec4d3
canga: reminders rm is available in the host build only, not in the sandbox build
```

### How a sandbox shares the host's list

The sandbox needs two things from its environment:

1. The host's store directory for that repository, mounted into the sandbox.
2. `CANGA_REMINDERS_DIR` set to the host's store root, which is a HOST path:
   inside the sandbox `$HOME` is not the host's home.

```sh
CANGA_REMINDERS_DIR=/Users/you/.local/share/canga/reminders canga reminders list
```

Without `CANGA_REMINDERS_DIR`, the sandbox build falls back to a store inside
the sandbox, which the host never sees. See
[Where the reminders live](#where-the-reminders-live) for the layout under the
root.

### Install it in a sandbox

Releases ship the sandbox build for linux only, because sandboxes are linux
VMs. In a sandbox's provisioning step, run as root, the install script takes
the tag to pin and installs a root-owned `canga` in `/usr/local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/brunovenceslau/canga/main/install_sandbox.sh | sh -s -- vX.Y.Z
```

Root ownership keeps a process without root from replacing the binary. It is
no boundary against an agent with sudo, which a Docker Sandbox grants its agent
user. The script verifies the archive against `checksums.txt` the same way the
host script does.

By hand, verify the archive against `checksums.txt` before extracting it:

```sh
releases=https://github.com/brunovenceslau/canga/releases
tag=vX.Y.Z                                          # the release this sandbox pins
asset=canga-sandbox_${tag#v}_linux_arm64.tar.gz     # or linux_amd64

curl -fsSLO "$releases/download/$tag/$asset"
curl -fsSLO "$releases/download/$tag/checksums.txt"
mkdir -p "$HOME/.local/bin"
awk -v a="$asset" '$2 == a' checksums.txt | sha256sum -c - \
  && tar -xzf "$asset" -C "$HOME/.local/bin" canga
```

Pin the tag rather than following `latest`, so every sandbox built from one
definition runs the same binary. `canga --version` prints the version, the role
(`sandbox`), the commit, and the Go version.

## Moving from devctl and agtctl

canga was two binaries, `devctl` and `agtctl`. What changed, and what you do:

| Before | Now | What to do |
| --- | --- | --- |
| `devctl`, `agtctl` | `canga` (host build, sandbox build) | Install the host build once by hand; `devctl upgrade` does not select `canga-host_` archives. |
| `${XDG_DATA_HOME}/devctl/reminders` | `${XDG_DATA_HOME}/canga/reminders` | Nothing, if you run any `canga reminders` command on the host before starting a sandbox that mounts the new path: that command moves the store in one rename and says so on stderr. |
| `DEVCTL_REMINDERS_DIR` | `CANGA_REMINDERS_DIR` | Rename it in each sandbox environment file, together with the mount path. The old name is not read. |
| `DEVCTL_BASE_DIR`, `DEVCTL_SIGNING_KEY`, `DEVCTL_ALLOWED_SIGNERS` | `CANGA_HOST_BASE_DIR`, `CANGA_HOST_SIGNING_KEY`, `CANGA_HOST_ALLOWED_SIGNERS` | Rename them wherever you set them. |
| `.devctl/hooks` | `.canga/hooks` | Move the directory and run `canga setup hooks`. It replaces its own earlier setup without `--force`: a `core.hooksPath` of `.devctl/hooks`, or, with `--symlink`, links into `.devctl/hooks`. |

The store moves only when the old directory exists, the new one does not, and
`CANGA_REMINDERS_DIR` is unset. If the rename fails, the command stops rather
than start an empty store beside the old one. Shell completion never moves it.

If the new directory already exists, for example because a sandbox that mounts
it was created first, nothing is moved: renaming over a mounted directory would
swap it out from under the running sandbox. Every `canga reminders` command then
names both directories on stderr until you move each repository's items across
by hand and remove the old directory.

## Shell completion

```sh
canga completion zsh > "${XDG_CACHE_HOME:-$HOME/.cache}/canga/_canga"
```

This is the host build's; the sandbox build has no `completion` command. Generate
it once, at install time, and source the cached file — never `eval` a
generator on the shell startup path.

`rm` and `reorder` complete **real stored ids**, each shown with its reminder's
first line as the description.

`-C` and `clone`'s optional target complete directories only. `clone`'s URL
position completes nothing: a half-typed URL is not a path, and a shell that
fell back to file completion there would offer the current directory's
contents.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | success |
| `1` | a runtime failure, a clone target that already holds something, or an id with nothing behind it |
| `2` | a bad invocation, a directory that is not a repository or has no usable `origin`, or an upgrade with no release to work from |

## Development

```sh
make tools   # install the pinned golangci-lint and govulncheck
make fix     # apply every automatic fix: go fix, the formatters, --fix linters
make ci      # lint + test + govulncheck — must be green before a push
make race    # the multi-process store race gate, verbosely
make release # attach the release artifacts to an existing GitHub release
```

Every gate is a `make` target, and CI invokes the target rather than restating
it, so what CI runs is what a push was checked against locally.
`make race RACE_PROCS=12` runs the store's concurrency gate at full size; the
default is sized for CI. `make race RACE_STORE_DIR=/path` runs it against a
filesystem of your choosing, which is how the store's invariants were checked
over a virtiofs mount rather than assumed to hold there.

### Releasing

You create the release on GitHub. `make release` builds the artifacts and
attaches them to it, and never edits the release or its notes. The split exists
to keep GitHub's generated notes, which is the reason to create the release
there in the first place.

Two things have to be installed. `gh` you already have, since it is how canga
is installed. GoReleaser is pinned in the `Makefile` and installed once:

```sh
GOBIN="$HOME/.local/bin" make tool-release
```

`go install` writes to `$(go env GOPATH)/bin` by default, which is on neither
mac's PATH. `GOBIN` puts the binary in a directory that is.

To publish a version:

1. Tag the commit and push the tag.

   ```sh
   git tag -a v0.2.0 -m v0.2.0
   git push origin v0.2.0
   ```

2. Create the release on GitHub from that tag, and let it generate the notes.

3. Build the artifacts and attach them.

   ```sh
   make release
   ```

The last line reports what landed: four `canga-host_` archives (darwin and
linux, amd64 and arm64) and two `canga-sandbox_` archives (linux):

```
release: v0.2.0 now carries 6 archives and checksums.txt
```

Run it again and it replaces those assets instead of failing, so building one
tag twice is safe.

Every condition below is checked before anything is compiled, so a mistake
costs a second rather than a four-platform build:

| It stops when | Do this |
| --- | --- |
| GoReleaser is not installed | `make tool-release` |
| `gh` is not installed | install `gh` |
| HEAD carries no tag | tag the commit you are releasing |
| HEAD carries more than one tag | delete the tag you are not releasing |
| The tag is not on the remote | `git push origin <tag>` |
| The tag names a different commit on the remote | force-push the tag, or build from the commit the release already names |
| The tag has no release on GitHub | create the release, step 2 above |
| The working tree is dirty | commit or stash first. GoReleaser enforces this one |

The three tag checks exist because the tag is resolved twice: `make release`
reads it to pick the release to upload to, and GoReleaser reads it again to name
the archives and stamp `main.version`. Two tags on one commit let those answers
differ, which attaches archives named after one tag to the release of the other.
A tag moved locally after being pushed attaches artifacts to a release that
names a different commit.

`make ci` then runs before the build. A release is the one build nobody
re-checks afterwards, and while the Release workflow cannot start a job, `make
ci` is the only gate between a broken tree and a published binary.

This procedure assumes `.github/workflows/release.yml` is not running. That
workflow triggers on a pushed `v*` tag and creates the release itself, so step 1
would produce the release before step 2 gets to, with workflow notes rather than
the ones you meant, and `make release` would then replace its artifacts with
locally built ones. GitHub Actions currently cannot start a job on the account,
which is why the two paths do not collide today. Whether to keep both, or retire
the workflow now that the local path exists, is still open.

`make release` calls GoReleaser rather than packaging with `tar` and `shasum`,
so `.goreleaser.yml` stays the single definition of the artifact format. The
reason is `canga upgrade`: it finds its asset by the `canga-host_` prefix and the
`_<os>_<arch>.tar.gz` suffix, and reads `checksums.txt` by exact filename. A second packaging
implementation that drifted from the first would break upgrading, for whoever
ran it next, rather than releasing, for whoever changed it.

### Commit hook

`.canga/hooks/pre-commit` runs `make pre-commit`, which applies every fix a
tool can apply on its own, and then refuses the commit if anything changed. It
refuses rather than amending on purpose: a hook that rewrites files and lets the
commit through commits something you never read.

Install it:

```sh
canga setup hooks
```

That points git's `core.hooksPath` at `.canga/hooks`, which covers every hook
at once and is undone with `git config --unset core.hooksPath`. git reads hooks
from only one directory, so anything already in `.git/hooks` stops running;
`canga setup hooks` says so when that is the case, and `--symlink` links each
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

    canga, a developer control tool for repositories and sandboxes
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
