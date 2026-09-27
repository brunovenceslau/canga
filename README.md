<!--
SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
SPDX-License-Identifier: GPL-3.0-or-later
-->

# canga

One binary, `canga`, built in two roles for the repositories and sandboxes of a
working day:

| Build | Runs on | What it does |
| --- | --- | --- |
| host | your Mac (darwin and linux builds) | `git` (clone, sync, hook setup), `workspace` (a repository beside its sandbox environment in cmux), reminders, its own upgrade |
| sandbox | inside an agent sandbox (linux) | reminders `list` and `add`, its own upgrade |

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

## `canga git clone`

Clones a repository into the deterministic layout, so the same repository lands
at the same path on every machine, whichever protocol you cloned it with.

```sh
canga git clone git@github.com:acme/widget.git
# → ~/src/github.com/acme/widget

cd "$(canga git clone https://github.com/acme/widget)"
canga git clone https://github.com/acme/widget /tmp/scratch   # an explicit target
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
is deliberate: a clone made by `canga git clone` signs with SSH, so do not hand a
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

## `canga git sync`

Fetches every remote with `--prune` and `--tags`, then fast-forwards each local
branch that tracks an upstream.

```sh
canga git sync                                  # the repository you are standing in
canga git sync -C ~/src/github.com/acme/widget  # or any other

# a sweep across every clone in the layout
find ~/src -name .git -maxdepth 4 -type d -exec dirname {} \; | while read -r r; do
  canga git sync -C "$r"
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

The fetch carries the same transport hardening as `canga git clone`.

## `canga workspace`

Opens a repository and its sandbox environment side by side, in one new
[cmux](https://github.com/manaflow-ai/cmux) workspace:

```sh
canga workspace https://github.com/acme/widget
```

The workspace is named `acme/widget` and is focused when it opens. It has two
panes:

| Pane | Starts in | Runs |
| --- | --- | --- |
| Left | `~/src/github.com/acme/docker-sbx/envs/github.com/acme/widget-env`, the environment | `sbx env run --clone --auto-approve`, the repository's sandbox |
| Right, focused | `~/src/github.com/acme/widget`, the clone, where `canga git clone` puts it | nothing |

cmux types `sbx env run --clone --auto-approve` into the left pane when its
terminal starts, the way you would. `--auto-approve` applies the environment
plan without the confirmation prompt, since the workspace opens focused on
the right pane and a prompt left in the left one would go unnoticed. When
the sandbox exits, the pane keeps its shell in the environment directory, so
you can start the sandbox again from there. If the
left pane shows a prompt and no sandbox, cmux gave up waiting for the terminal
(it waits a few seconds and drops the command without a message): type
`sbx env run --clone --auto-approve` yourself.

Use it when you keep each repository's sandbox environment outside the
repository, so that an agent in the sandbox cannot edit the environment that
runs it.

### Where the environment is found

The environments live in their own repository, cloned under the same base
directory as every other clone, one directory per environment:

```
${CANGA_HOST_BASE_DIR:-$HOME/src}/<environments repository>/envs/<host>/<owner>/<repo>-env
```

The trailing `-env` is there so the environment directory's basename never
matches the clone's: without it, both directories end in the same `<repo>`,
which is easy to mistake for one another in a listing, a pane or a shell
prompt. The suffix follows [Docker Sandboxes' own documented environment file
layout](https://docs.docker.com/ai/sandboxes/configuration/environment-files/),
which names an environment directory for `web-app` as `web-app-env/`.

The environment directory is not a git repository of its own: it has no
`.git`, and sits inside the working tree of the repository that holds the
environments. Left alone, a `git` command run inside it would walk up past it
and act on that enclosing repository instead, and the `-env` suffix does not
change that; it only keeps the two directories' names apart. The left pane
guards against this; see [Keeping git out of the envs
repository](#keeping-git-out-of-the-envs-repository) below for what it does
and does not cover.

| Variable | Default | What it sets |
| --- | --- | --- |
| `CANGA_HOST_ENVS_REPO` | `<host>/<owner>/docker-sbx`, the `docker-sbx` beside the opened repository | The environments repository, as its path under the base directory, such as `github.com/acme/sandboxes`. |

With the default, `github.com/acme/widget` finds its environment in
`github.com/acme/docker-sbx`, so nothing needs configuring. For a repository in
a nested group, the default is the `docker-sbx` in the same innermost group:
`gitlab.com/acme/platform/widget` looks in `gitlab.com/acme/platform/docker-sbx`,
not in `gitlab.com/acme/docker-sbx`. Set
`CANGA_HOST_ENVS_REPO` when the environments live elsewhere, for example to open
a repository of another owner with your own environments:

```sh
export CANGA_HOST_ENVS_REPO=github.com/brunovenceslau/docker-sbx
canga workspace https://github.com/acme/widget
```

The value is a path, not a URL, and must stay under the base directory. Each
segment follows the rule a URL's segments do: letters, digits and `._~+-`, and
never `.` or `..` alone. An absolute path is refused too. The error does not
repeat the value, so a URL pasted by mistake does not print its credential.
The `<host>/<owner>/<repo>` segments keep their case, as the clone's do.

The environments repository is the one repository whose environment cannot live
apart from it: opening `github.com/acme/docker-sbx` itself finds its
environment inside its own clone.

**Migrating existing environment directories:** the `-env` suffix is a
breaking change for a `docker-sbx` clone created before it. For each existing
environment directory under `envs/<host>/<owner>/<repo>`, rename its leaf
segment to add the suffix, then commit the rename:

```sh
git -C ~/src/github.com/acme/docker-sbx mv envs/github.com/acme/widget envs/github.com/acme/widget-env
```

### Keeping git out of the envs repository

`canga workspace` sets `GIT_CEILING_DIRECTORIES` in the left pane's own
environment, ahead of anything typed into its shell, to the environments
repository's `envs/` directory: the directory every environment sits under. A
`git` command run from the environment pane then answers "not a git
repository" instead of silently acting on the environments repository's clone.

This guards against an accident, not against someone working around it: `git
-C <environments clone>`, a `GIT_DIR` set explicitly, unsetting
`GIT_CEILING_DIRECTORIES` for the command, or a shell rc that overwrites
rather than composes with it (see below) all reach the environments
repository just as before. It is a fence around the paths git wanders into by
default, not an isolation boundary.

That comes at a price: the pane itself cannot `git add`, `git commit` or
otherwise work on the environment as a git repository any more. Do that from
the environments repository's own clone, or from the pane with `git -C
<environments clone>`.

canga composes its entry with whatever `GIT_CEILING_DIRECTORIES` it inherited,
rather than replacing it, so a value your shell already set is kept, not lost.
Its own rc files still run after cmux sets the variable, though, so a
dotfiles line that assigns `GIT_CEILING_DIRECTORIES` outright, instead of
composing with it, drops canga's entry the moment the shell starts. Compose it
there too, both to survive that and to protect a plain terminal that `cd`s
into the environment directory without going through `canga workspace` at all,
which gets no protection otherwise:

```sh
envs_ceiling="${CANGA_HOST_BASE_DIR:-$HOME/src}/github.com/acme/docker-sbx/envs"
case ":${GIT_CEILING_DIRECTORIES:-}:" in
  *":$envs_ceiling:"*) ;;
  *) export GIT_CEILING_DIRECTORIES="$envs_ceiling${GIT_CEILING_DIRECTORIES:+:$GIT_CEILING_DIRECTORIES}" ;;
esac
```

Replace `github.com/acme/docker-sbx` with whatever `CANGA_HOST_ENVS_REPO` is
set to, or, if it is unset, with `<host>/<owner>/docker-sbx` for the owner of
the repository you open most, the same default `canga workspace` itself
falls back to. The `case` guards against duplicating the entry: sourced twice,
in the same shell or across bash and zsh's own rc files, it finds the entry
already there and leaves `GIT_CEILING_DIRECTORIES` alone.

`envs_ceiling` must resolve to an absolute path: git silently ignores a
relative `GIT_CEILING_DIRECTORIES` entry. Set `CANGA_HOST_BASE_DIR` itself to
an absolute path if you export it; canga's own `--layout` value never has this
problem, since it always resolves the base directory to an absolute path
first.

### Requirements

- cmux 0.64.23 or later. The workspace is created with one `cmux new-workspace
  --layout` call, checked against 0.64.23 and 0.64.25.
- Run it from a terminal inside cmux. cmux's default socket mode accepts
  commands only from its own terminals, and its refusal is printed as it is.
- The clone, the environments repository's clone, and the environment
  directory in it already exist. `canga git clone` makes both clones.
- `sbx` on the `PATH` of the shells cmux opens. canga does not check for it:
  if it is missing, the left pane shows that shell's `command not found`.

### What it refuses

Nothing is created or cloned. Each refusal happens before cmux is called:

| Situation | Exit |
| --- | --- |
| `CANGA_HOST_ENVS_REPO` is absolute, a URL, or has an empty, `.`, `..` or otherwise invalid segment | `2` |
| A URL no `<host>/<owner>/<repo>` can be derived from | `2` |
| No clone at the derived path. The message suggests `canga git clone <url>` | `1` |
| No environment directory at the derived path. The message names the path and the fixes: clone the environments repository with `canga git clone`, update it with `canga git sync`, create the directory, or set `CANGA_HOST_ENVS_REPO` | `1` |
| `cmux` is not on your PATH | `1` |
| cmux fails. Its own message is shown | `1` |

On success, stdout carries cmux's own reply, such as `OK workspace:3`.

## `canga upgrade`

Replaces the running binary with a published release, in place. Each build
replaces itself with the same build: the host build installs a `canga-host_`
archive, the sandbox build a `canga-sandbox_` one. See
[Upgrade it in a sandbox](#upgrade-it-in-a-sandbox) for what differs there.

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
once** to confirm it reports the version and the build (`host` or `sandbox`)
it was downloaded as. Only then is it renamed over the target. An archive
holding something that is not canga, a binary for the wrong platform, or the
other build, fails at that step with the working binary
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

The store directory itself must be a real directory. A `Root` follows its own
path, so a symbolic link there would carry every read and write to wherever it
points; canga refuses to open a store whose directory is a link. Links higher
up the path, such as a symlinked `~/.local/share`, still work.

## The sandbox build

The sandbox build is what an agent inside a sandbox runs. It reads and adds to
the same reminders the host build manages, and upgrades itself.

```sh
canga reminders list
canga reminders add check the retry budget before merging
```

Both verbs behave exactly as on the host: `list` prints one `<id><TAB><text>`
record per line, `add` prints the new id, and `-C` names a repository other
than the current directory.

### What it leaves out

The sandbox build has no `git`, `workspace` or `completion`, and no `reminders rm`,
`reorder` or `path`. An agent may surface the list and record an idea, but
the list belongs to the person on the host, so only the host build removes or
reorders it.

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

### Use the sbx kit

With [Docker Sandboxes](https://docs.docker.com/ai/sandboxes/) (`sbx`), you
can let a kit install the sandbox build instead of writing the provisioning
step yourself. The kit is the [`sbx-kit/`](sbx-kit/spec.yaml) directory of this
repository. It installs the release it pins, verified against that release's
sha256, as a root-owned `/usr/local/bin/canga`, and allows the egress canga
needs: `github.com`, `api.github.com` and
`release-assets.githubusercontent.com`.

Before you start:

- The sandbox has `curl`, from its image or from a kit listed before this one.
  The kit stops with `canga: curl is not installed` when it is missing.
- sbx allows kits from this repository's owner. By default sbx loads kits
  from `docker.io/` only, and refuses this kit until `kit.allowedSources`
  names `github.com/brunovenceslau/`. The command replaces the whole list, so
  repeat every source you already allow:

  ```sh
  sbx settings set kit.allowedSources '["docker.io/","github.com/brunovenceslau/"]'
  ```

  `'["*"]'` also works, and allows kits from any remote source. Prefer the
  narrow entry.

To add the kit:

1. Pick the commit to pin, such as the current head of `main`:

   ```sh
   git ls-remote https://github.com/brunovenceslau/canga.git refs/heads/main
   ```

2. Check that sbx accepts the kit at that commit:

   ```sh
   sbx kit validate "git+https://github.com/brunovenceslau/canga.git#ref=<commit>&dir=sbx-kit"
   ```

   An allowlist problem shows up here instead of when the sandbox starts. See
   [sbx says the kit's source is not in your allowlist](#sbx-says-the-kits-source-is-not-in-your-allowlist).

3. List the kit in your environment file, with that commit as `ref`:

   ```yaml
   kits:
     - git+https://github.com/brunovenceslau/canga.git#ref=<commit>&dir=sbx-kit
   ```

4. Start the sandbox. The kit's install step ends by printing
   `canga --version`, such as `v0.10.1 (sandbox, <commit>, <go>)`.

Pin a commit rather than a branch or a tag. The kit's release pin moves in a
pull request after each release (see [Releasing](#releasing)), so a tag's kit
names the release before it, and a branch changes under you. The merge commit
of the newest `chore(sbx-kit): pin canga vX.Y.Z` pull request is the ref that
installs the newest release.

The kit installs the binary only. Sharing the host's reminders list still
needs the mount and the variable described in
[How a sandbox shares the host's list](#how-a-sandbox-shares-the-hosts-list).
The kit does not register a Claude Code hook: sbx manages the sandbox's
`~/.claude/settings.json`, and a kit has no field for hooks.

#### sbx says the kit's source is not in your allowlist

`sbx kit validate`, and any command that loads the kit, refuses it when
`kit.allowedSources` does not name `github.com/brunovenceslau/`:

```
INVALID: kit "git+https://github.com/brunovenceslau/canga.git#ref=<commit>&dir=sbx-kit" cannot be installed — its source is not in your allowlist.

Your current kit.allowedSources:
  - docker.io/
...
ERROR: artifact validation failed
```

The message lists your current allowlist. Add `github.com/brunovenceslau/` to
it, keeping every entry it already lists, then run the command again:

```sh
sbx settings set kit.allowedSources '["docker.io/","github.com/brunovenceslau/"]'
```

### Upgrade it in a sandbox

`canga upgrade` moves a running sandbox to a newer release of the sandbox
build. The pin still decides the release every sandbox starts with: recreating
the sandbox installs the pinned release again. To change the release of every
sandbox, change the pin.

Before you run it:

- The sandbox's egress policy allows `api.github.com`, where canga reads the
  release, and `release-assets.githubusercontent.com`, where GitHub redirects
  the download.
- You can write to the directory that holds the binary. The upgrade stages the
  new binary there before renaming it over the old one.

Run it with `sudo` when root owns the binary, as it owns the install script's
`/usr/local/bin/canga`:

```sh
sudo canga upgrade
```

A copy in a directory you own, such as `~/.local/bin`, needs no `sudo`:

```sh
canga upgrade
```

On success, stdout holds the installed tag and stderr says
`canga: installed <new> over <old> at <path>`. `canga --version` then reports
the new release and the role `sandbox`.

`sudo` resets the environment by default, so `GH_TOKEN` does not reach the
upgrade and canga reads the release anonymously. The repository is public, so
the upgrade still works, within GitHub's anonymous limit of 60 requests an hour.

Sandbox builds up to v0.6.0 do not have this command. They answer
`canga upgrade` with `upgrade is available in the host build only, not in the
sandbox build`. Move those sandboxes to a release that has it by changing the
pin.

`--tag` can install one of those builds: `sudo canga upgrade --tag v0.6.0`
succeeds. After that, `canga upgrade` in the sandbox refuses until the sandbox
is recreated or `install_sandbox.sh` runs again.

## Moving from devctl and agtctl

canga was two binaries, `devctl` and `agtctl`, and up to v0.5.0 its git
commands sat at the top level. What changed, and what you do:

| Before | Now | What to do |
| --- | --- | --- |
| `devctl`, `agtctl` | `canga` (host build, sandbox build) | Install the host build once by hand; `devctl upgrade` does not select `canga-host_` archives. |
| `${XDG_DATA_HOME}/devctl/reminders` | `${XDG_DATA_HOME}/canga/reminders` | Nothing, if you run any `canga reminders` command on the host before starting a sandbox that mounts the new path: that command moves the store in one rename and says so on stderr. |
| `DEVCTL_REMINDERS_DIR` | `CANGA_REMINDERS_DIR` | Rename it in each sandbox environment file, together with the mount path. The old name is not read. |
| `DEVCTL_BASE_DIR`, `DEVCTL_SIGNING_KEY`, `DEVCTL_ALLOWED_SIGNERS` | `CANGA_HOST_BASE_DIR`, `CANGA_HOST_SIGNING_KEY`, `CANGA_HOST_ALLOWED_SIGNERS` | Rename them wherever you set them. |
| `canga clone`, `canga sync`, `canga setup hooks` (v0.5.0) | `canga git clone`, `canga git sync`, `canga git setup-hooks` | Use the new names wherever you call them. The old names were removed, not aliased, and fail with a usage error (exit `2`). |
| `.devctl/hooks` | `.canga/hooks` | Move the directory and run `canga git setup-hooks`. It replaces its own earlier setup without `--force`: a `core.hooksPath` of `.devctl/hooks`, or, with `--symlink`, links into `.devctl/hooks`. |

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

`-C` and `git clone`'s optional target complete directories only. The URL
positions of `git clone` and `workspace` complete nothing: a half-typed URL is not a path, and a shell that
fell back to file completion there would offer the current directory's
contents.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | success |
| `1` | a runtime failure, a clone target that already holds something, an id with nothing behind it, or a workspace whose clone or environment directory is missing |
| `2` | a bad invocation, a directory that is not a repository or has no usable `origin`, an upgrade with no release to work from, or `workspace` with a `CANGA_HOST_ENVS_REPO` outside the base directory |

## Development

```sh
make tools   # install the pinned golangci-lint and govulncheck
make fix     # apply every automatic fix: go fix, the formatters, --fix linters
make ci      # lint + test + govulncheck — must be green before a push
make race    # the multi-process store race gate, verbosely
make release-preflight             # checks to run on a signed tag before pushing it
make release-kit-bump TAG=vX.Y.Z   # pin the sbx kit to a release the workflow published
```

Every gate is a `make` target, and CI invokes the target rather than restating
it, so what CI runs is what a push was checked against locally.
`make race RACE_PROCS=12` runs the store's concurrency gate at full size; the
default is sized for CI. `make race RACE_STORE_DIR=/path` runs it against a
filesystem of your choosing, which is how the store's invariants were checked
over a virtiofs mount rather than assumed to hold there.

### Releasing

`.github/workflows/release.yml` is the one official release path: a signed
`v*` tag, pushed, is the whole trigger. It builds every platform with
GoReleaser, attests what it built, and publishes the release itself, notes
and all - nothing local builds or uploads a release artifact any more. The
one thing left for a human to do afterwards is move the sbx kit's pin,
which needs the finished release's `checksums.txt` and cannot happen before
it exists.

Two repository settings are prerequisites, both on before any tag is
pushed: [immutable releases](#immutable-releases) (`make release-preflight`
checks this one itself, `check-immutable`) and a ruleset on `refs/tags/v*`
restricting tag creation, update and deletion to the admin role (not
independently checked by anything here; see docs/HANDOFF.md, "Left open",
for the exact payload). The ruleset is what actually restricts who can
push a `v*` tag at all - and so who can trigger a release, or tag a commit
whose `release.yml` an attestation would then vouch for (see
[Verify a release](#verify-a-release)).

Signing is the only local setup: `make release-kit-bump` commits that pin
signed (`git commit -S`), so git must already be set up to sign commits, as
it is for any commit to this repository.

To publish a version:

1. Check that the previous release's kit bump has merged: `main`'s
   `sbx-kit/spec.yaml` must pin the newest published release. Step 2's
   `make release-preflight` refuses otherwise; see
   [Move the sbx kit's pin](#move-the-sbx-kits-pin).

2. Tag the commit, signed, and run the preflight before pushing it.

   ```sh
   git tag -s v0.2.0 -m v0.2.0
   make release-preflight
   git push origin v0.2.0
   ```

   `release-preflight` repeats, locally, the two checks the workflow makes
   for itself once the tag lands (`check-previous`, `check-clobber`), so a
   release that would fail in CI fails here first, before it costs a run.
   It also refuses unless the repository has immutable releases on
   (`check-immutable`; see [Immutable releases](#immutable-releases)), the
   one check the workflow cannot make before publishing: GitHub reports
   that setting only to a token with admin access.

3. Watch the Release run the pushed tag triggers.

   ```sh
   gh run watch --repo brunovenceslau/canga
   ```

   The run goes draft first, public last:

   1. GoReleaser (pinned to an exact version in `release.yml`) runs
      `release --clean`: it builds everything and creates the GitHub
      release as a **draft** (`release.draft` in `.goreleaser.yml`),
      with GitHub's own generated notes (`changelog.use: github-native`),
      and attaches four `canga-host_` archives (darwin and linux, amd64 and
      arm64), two `canga-sandbox_` archives (linux), and `checksums.txt`.
   2. `actions/attest-build-provenance` signs a build provenance
      attestation for every archive and `checksums.txt`, naming this
      repository, `release.yml`, the tag, and the runner.
   3. The run checks those attestations verify, with the same policy the
      kit pin checks use (`scripts/sbx-kit-pin.sh check-attestation`; see
      [Verify a release](#verify-a-release)).
   4. It checks the draft still holds exactly the files it built, by the
      digest GitHub serves for each asset, and only then publishes it. It
      goes red if GitHub then reports the published release as not
      immutable.

   Runs of the same tag queue behind each other instead of racing.

   A failed run leaves at most a draft, which nobody can pin (the pin
   checks refuse a draft) and which the next run of the same tag replaces
   (`release.replace_existing_draft`).

4. Once the run is green, make the kit bump.

   ```sh
   make release-kit-bump TAG=v0.2.0
   ```

   This downloads `v0.2.0`'s `checksums.txt` and both `canga-sandbox_`
   archives from the release, checks them against each other and against the
   digests GitHub serves, and commits the pin, signed, on branch
   `chore/sbx-kit-v0.2.0` - from a temporary detached worktree at the tag, so
   the checkout you ran this from is left exactly as it was.

5. Push that branch and open its pull request. `release-kit-bump` prints
   both commands; they are left to you because they publish:

   ```sh
   git push -u origin chore/sbx-kit-v0.2.0
   gh pr create --base main --head chore/sbx-kit-v0.2.0 --fill
   ```

6. Review and merge that pull request. Its merge commit is the ref an
   environment pins ([Use the sbx kit](#use-the-sbx-kit)).

Run step 4 again, before its branch is pushed or merged, and it changes
nothing: it verifies the existing branch pins the same hashes and stops.

A published release is final. Once immutable releases are on (see
[Immutable releases](#immutable-releases)), GitHub refuses to change its
assets at all, so a bad release is fixed by cutting a new patch release,
never by rebuilding the old one. Until then, `SBX_KIT_ALLOW_CLOBBER` (a
local override for `check-clobber`, not something the workflow reads)
exists for the case where breaking every sandbox pinned to the old archives
is genuinely the point; see the table below.

Every condition below is checked before anything slow, so a mistake costs a
second rather than a wasted Release run or a broken sandbox pin:

| It stops when | Do this |
| --- | --- |
| HEAD carries no tag (`release-preflight`) | tag the commit you are releasing |
| HEAD carries more than one tag (`release-preflight`) | delete the tag you are not releasing |
| `sbx-kit/spec.yaml` at the tag does not pin the newest published release below it, or pins hashes GitHub does not serve for it (`check-previous`, run by `release-preflight` and the workflow) | merge the previous release's `chore/sbx-kit-vX.Y.Z` pull request, then re-tag on top of it |
| The tag already carries `canga-sandbox_` archives, and its kit bump is pushed or merged (`check-clobber`, run by `release-preflight` and the workflow) | cut a new patch release, or set `SBX_KIT_ALLOW_CLOBBER=<the tag>` |
| `gh` answers 404 for the tag's release and either cannot see `brunovenceslau/canga` at all or sees it under another name, so the 404 proves nothing (the refusal names what `gh` saw; `check-clobber`, run by `release-preflight` and the workflow) | authenticate `gh` with a token that can read `brunovenceslau/canga`, or update the script's `repo=` if it was renamed or transferred |
| The tag is below the newest published release (an older line; `release-preflight`, the workflow, and `release-kit-bump` each make this check, independently) | release from the newest line, or set `SBX_KIT_OLDER_LINE=<the tag>` |
| `gh` is not installed (`release-kit-bump`) | install `gh`, which is also how canga itself is installed |
| `release-kit-bump` was run without `TAG=`, or `TAG` is not exactly `vX.Y.Z` | pass it: `make release-kit-bump TAG=vX.Y.Z` |
| The tag as fetched into this checkout resolves to a different commit than GitHub resolves it to (`release-kit-bump`) | something is wrong with `origin` or with GitHub's own view of the tag; do not proceed until they agree |
| The tag predates `scripts/sbx-kit-pin.sh` (`release-kit-bump`) | nothing to do: the kit has already moved past any release this old |
| A release at or above v0.10.5 has an archive with no build provenance attestation from `release.yml` for its own tag, or one whose downloaded bytes are not the pinned ones (`check-previous`, `release-kit-bump`) | do not pin it: cut a new patch release through the workflow |
| `gh` is older than 2.93.0 and the release needs its attestation checked (`check-previous`, `release-kit-bump`, the workflow's pre-publish check) | upgrade `gh` |
| The repository does not have immutable releases on, or `gh`'s token cannot tell (it needs admin access; `release-preflight`) | turn immutable releases on, or run the preflight as a repository admin |
| The release published but did not settle to `immutable == true` within the retry budget (the workflow's "Publish the release" step). The release is already public - do not re-run the workflow for this tag | check the setting with `make release-preflight` (`check-immutable`); if it was off, turn it on and cut a new patch release; if it is on, the release may already be immutable - confirm with `gh api repos/brunovenceslau/canga/releases/tags/<tag> --jq .immutable` |

The tag-count check exists because a second tag on the same commit has no
single answer to "which release is this": `release-preflight` cannot tell
which one you mean, even though the workflow only ever sees whichever ref
its own push triggered it from.

The workflow calls GoReleaser rather than packaging with `tar` and `shasum`,
so `.goreleaser.yml` stays the single definition of the artifact format. The
reason is `canga upgrade`: each build finds its asset by its own prefix
(`canga-host_` or `canga-sandbox_`) and the `_<os>_<arch>.tar.gz` suffix, and
reads `checksums.txt` by exact filename. A second packaging implementation
that drifted from the first would break upgrading, for whoever ran it next,
rather than releasing, for whoever changed it.

#### Verify a release

From v0.10.5 on, every archive and `checksums.txt` of a release carries a
build provenance attestation: a Sigstore-signed statement, stored on this
repository, that those exact bytes were built by `release.yml` running for
that release's tag on a GitHub-hosted runner. Anyone can check one with
`gh` 2.93.0 or newer:

```sh
gh release download v0.10.5 -R brunovenceslau/canga \
  -p canga-sandbox_0.10.5_linux_amd64.tar.gz
gh attestation verify canga-sandbox_0.10.5_linux_amd64.tar.gz \
  --repo brunovenceslau/canga \
  --cert-identity https://github.com/brunovenceslau/canga/.github/workflows/release.yml@refs/tags/v0.10.5 \
  --source-ref refs/tags/v0.10.5 \
  --deny-self-hosted-runners
```

These are the flags the kit pin checks use. `--cert-identity` requires the
signing certificate's workflow identity to be exactly that URL. The
shorter-looking `--signer-workflow` is not used on purpose: `gh` turns its
value into a regular expression anchored only at the start, so a workflow
file or ref that merely begins with the expected one would pass too.

What this proves is narrower than it sounds: it binds the bytes to
`release.yml` **at whatever commit the tag named when the release was
built**, not to reviewed or merged content, and the check matches that
binding by the tag's name alone - not by which commit the tag names now.
Anyone who can push a `v*` tag can tag a commit that carries a modified
`release.yml`, and that run's attestation verifies the same way - the
command above cannot tell the two apart. What actually keeps a tag from
being moved to another commit after the fact, and so restricts who can
push a `v*` tag at all, is the repository's tag ruleset (see
[Releasing](#releasing)), not this attestation.

Releases before v0.10.5 have no attestation, so for those the command above
fails with a 404; they are checked by digest and uploader alone (see
[Move the sbx kit's pin](#move-the-sbx-kits-pin)).

#### Immutable releases

The repository is meant to run with GitHub's immutable releases setting on.
It is enabled once the draft-first workflow above has merged, and before
v0.10.5 is tagged. The order matters: an immutable release refuses any
asset change once it is published, so a workflow that published first and
uploaded afterwards would fail on its own upload. Draft first works,
because a draft stays editable until the workflow publishes it.

What it changes, once on:

- No asset of a published release can be replaced, added or deleted, by
  anyone, `SBX_KIT_ALLOW_CLOBBER` included. A bad release is fixed by a new
  patch release.
- The release's tag cannot be moved or deleted.
- GitHub adds a release attestation of its own on publish, which
  `gh release verify` checks. It is not what the kit pin checks rely on:
  they check the build provenance above, which names the workflow that
  built the bytes, not only the release that holds them.

#### Move the sbx kit's pin

`sbx-kit/spec.yaml` pins a release by its version and the sha256 of both
`canga-sandbox_` linux archives. The hashes live in the kit, and are not
downloaded next to the tarball at install time, because the kit is what
other repositories pin by commit: what a reviewer read is exactly what a
sandbox installs. A `checksums.txt` fetched from the same release as the
tarball proves nothing against whoever can replace that release's assets,
since they can replace both.

The hashes exist only once a release is published, so the pin cannot move in
the release commit. `make release-kit-bump TAG=vX.Y.Z` moves it afterwards,
with `scripts/sbx-kit-pin.sh bump`:

- Before anything else, it compares the tag as fetched into your checkout
  against the commit GitHub itself resolves it to, and refuses on any
  disagreement: your `origin` and the canonical repository must name the
  same commit before either is trusted.
- It downloads `checksums.txt` and both `canga-sandbox_` archives from the
  tag's GitHub release into a scratch directory, and stands up a temporary
  detached worktree at the tag - so `bump`'s own requirements (a clean tree,
  a `HEAD` that carries exactly that one tag) are met without touching the
  checkout you ran the command from. `bump` itself then runs from that
  worktree's own copy of `scripts/sbx-kit-pin.sh`, not the one in your
  checkout: the tag's own, reviewed script judges the tag's own release. A
  tag cut before the script existed is refused, not silently run with a
  newer copy.
- It reads that downloaded `checksums.txt`, never a local build, and refuses
  unless it finds exactly one `canga-sandbox_` line for each arch.
- It checks those sums twice more, and refuses on any difference: against
  the archives downloaded alongside it, and against the `sha256:` digest
  GitHub serves for each asset of the release (`gh api
  .../releases/tags/<tag>`; a missing digest is a refusal too). Bytes that
  disagree between the download and what GitHub itself now serves are never
  trusted into a signed pin.
- For a release at or above v0.10.5, it downloads both archives once more,
  checks they are the pinned bytes, and refuses unless each has a build
  provenance attestation from `release.yml` for that release's own tag
  ([Verify a release](#verify-a-release) has the exact check, and its
  caveat about what this does and does not prove). That closes the
  uploader check's own gap - some workflow in this repository uploaded the
  asset, not necessarily `release.yml` - without claiming more than the
  attestation itself proves.
- It rewrites `CANGA_VERSION` and both `sha256` values in the kit as
  committed at the tag, and commits that, signed, on branch
  `chore/sbx-kit-vX.Y.Z`, through a second, nested temporary worktree of its
  own.
- Run again, it changes nothing when that branch is one verified commit on
  top of the tag, touching only the kit, with the same hashes. Any other
  branch of that name is a refusal.
- It pushes nothing. It prints the push and the `gh pr create` command.

Nothing merges the bump for you. So `make release-preflight` refuses the next
release until `sbx-kit/spec.yaml` at the tag pins the newest published
release below it, with the digests GitHub serves for it (and, from v0.10.5
on, a verifying attestation), which is to say until the bump merged and the
new tag sits on top of it. The Release workflow makes the same check. Before any of this existed the pin moved by
hand, and the kit stayed on v0.8.0 through v0.9.0, v0.10.0 and v0.10.1.

The kit follows the newest release line and never moves backwards. A
release below the newest published one (a fix on an older line) is refused
unless `SBX_KIT_OLDER_LINE` names its tag, such as `SBX_KIT_OLDER_LINE=v0.9.1`
for both `make release-preflight` and `make release-kit-bump TAG=v0.9.1`.
With it set, the preflight only asks that the kit at the tag pins some
published release below it, and the bump step prints a warning and commits
no branch.

If the bump branch is lost before it merges, run `make release-kit-bump
TAG=vX.Y.Z` again. There is nothing to reconstruct by hand: the target
always downloads the tag's `checksums.txt` and archives fresh from the
release and re-derives the pin from them, so a lost branch and a first run
go through the exact same path and commit the exact same pin (a new commit,
re-signed, but pinning the identical version and hashes).

`scripts/sbx-kit-pin.sh rewrite X.Y.Z <checksums.txt>` is the lower-level
primitive underneath `bump`: it rewrites `CANGA_VERSION` and both `sha256`
values in the *working tree's* kit, with no commit and no branch. It exists
for the one case `bump` cannot cover - a release cut before this script
existed, so there is no tag-side copy of it to run `bump` from (the v0.10.2
pin was moved this way). Like `bump`, it always checks the sums it is given
against release `vX.Y.Z`'s published digests before writing anything, and
refuses on any mismatch, a missing digest, a draft or a prerelease, an
asset not uploaded under the `github-actions[bot]` identity, or, from
v0.10.5 on, an archive without a verifying build provenance attestation
from `release.yml` - there is no flag to skip any of it.

The v0.10.5 cutover is a constant in `scripts/sbx-kit-pin.sh`
(`attested_from`), with no flag or variable to move it: releases below it
were published before the workflow attested anything, and are checked by
digest and uploader alone, which proves some workflow in this repository
uploaded them, not the Release workflow specifically. The constant changes
only by a reviewed pull request.

### Commit hook

`.canga/hooks/pre-commit` runs `make pre-commit`, which applies every fix a
tool can apply on its own, and then refuses the commit if anything changed. It
refuses rather than amending on purpose: a hook that rewrites files and lets the
commit through commits something you never read.

Install it:

```sh
canga git setup-hooks
```

That points git's `core.hooksPath` at `.canga/hooks`, which covers every hook
at once and is undone with `git config --unset core.hooksPath`. git reads hooks
from only one directory, so anything already in `.git/hooks` stops running;
`canga git setup-hooks` says so when that is the case, and `--symlink` links each
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
