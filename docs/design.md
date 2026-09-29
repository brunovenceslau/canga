<!--
SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
SPDX-License-Identifier: GPL-3.0-or-later
-->

# How canga works

The [README](../README.md) says what each command does. This page says how,
and why it is built that way: the guarantees each command makes, what they
protect against, and where they stop. Read it before changing behavior, or
when you need to know exactly what a check proves.

- [Two builds, one binary](#two-builds-one-binary)
- [Which key a repository gets](#which-key-a-repository-gets)
- [Where a clone lands](#where-a-clone-lands)
- [Transport hardening](#transport-hardening)
- [Signing](#signing)
- [How sync advances a branch](#how-sync-advances-a-branch)
- [Where the environment is found](#where-the-environment-is-found)
- [Keeping git out of the envs repository](#keeping-git-out-of-the-envs-repository)
- [What upgrade verifies](#what-upgrade-verifies)
- [The release floor](#the-release-floor)
- [The by-hand install check](#the-by-hand-install-check)
- [Where the reminders live](#where-the-reminders-live)
- [Concurrency](#concurrency)

## Two builds, one binary

The role is fixed when the binary is built: the sandbox build does not
contain the host's commands, so nothing an agent sets at runtime can reach
them. `canga --version` names the role.

The sandbox build has no `git`, `workspace` or `completion`, and no
`reminders rm`, `reorder` or `path`. An agent may surface the list and
record an idea, but the list belongs to the person on the host, so only the
host build removes or reorders it. Running a left-out command in the sandbox
build refuses with exit `2` and says where it lives, and help does not list
it:

```
$ canga reminders rm 20260918T015659.641699Z-125ec4d3
canga: reminders rm is available in the host build only, not in the sandbox build
```

## Which key a repository gets

Every subcommand is keyed by the repository the current directory belongs
to. The derivation is deterministic, so `git@github.com:acme/widget.git`,
`https://github.com/acme/widget.git` and `ssh://git@github.com:22/acme/widget`
all name the same repository, `github.com/acme/widget`, no matter which one
you happened to clone with.

A repository with an `origin` remote is keyed by that remote's
`<host>/<owner>/<repo>`, whatever directory it sits in.

A repository with no `origin` at all is keyed by where its working tree is:
its path below the clone base, the same `${CANGA_SRC_DIR:-$HOME/src}` that
`canga git clone` lays repositories out under. `~/src/local/OS` keys as
`local/OS`. A tree at exactly the place `canga git clone` would put a
repository gets that repository's key, so an origin-less
`~/src/github.com/acme/widget` reads the same list as a clone of
`github.com/acme/widget`. That shared key is the rule, not a collision: the
layout defines that directory as that repository.

The location is read from the filesystem, never from the repository's own
configuration:

- The working tree is the nearest directory holding a `.git`, from `-C` (or
  the current directory) upwards, and git must report the same one. A
  `core.worktree` or `GIT_WORK_TREE` that moves the working tree elsewhere is
  refused, so nothing inside a repository can pick another repository's key.
  "The same" is decided by file identity, not by comparing path strings.
- Symbolic links are resolved on both sides first, so a link is another name
  for the same key, and a link under the base that points out of it is
  outside.
- Each path component is taken in its on-disk spelling. On a
  case-insensitive volume, such as APFS by default, `-C ~/SRC/LOCAL/os` or a
  `CANGA_SRC_DIR` typed in another case still keys as `local/OS`.
- The tree must be strictly below the base. The base itself, anywhere
  outside it, and a path segment a URL could not carry either (a space, a
  shell metacharacter) are refused with exit `2`, as a repository with no
  `origin` was before.
- An `origin` that exists but names no usable URL is refused as before; only
  a repository with no `origin` remote falls back to its location.
- A linked worktree (`git worktree add`) is keyed by its main working tree,
  wherever it sits, so every worktree of a repository reads one list:
  `~/src/local/OS/.claude/worktrees/x` and `~/src/local/OS-wt` both key as
  `local/OS`. The link is believed only when it checks out both ways: the
  worktree's git directory sits in the main repository's `.git/worktrees/`,
  that directory's `gitdir` names this worktree's `.git`, and the common
  directory is the main tree's `.git`. A forged `gitdir` or `commondir` is
  refused, never keyed by the worktree's own path instead. A submodule is
  keyed by where it is. A worktree of a bare repository, or of one made with
  `--separate-git-dir`, has no main working tree and is refused, exit `2`;
  give the repository an `origin` to key it by that.
- A `.git` that is a symbolic link is refused, exit `2`: it would let one
  directory pass for another repository's main tree or worktree. The
  worktree's `gitdir` record is read only as a regular file of at most 4096
  bytes, never through a link or from a FIFO, and what it says is quoted,
  never printed raw.
- A segment the store uses inside a key's directory (`repo`, `items`,
  `order`, `tmp`) is refused in a location-derived key, so no origin-less
  repository's store sits inside another origin-less repository's data. It
  does not stop an `origin` key from nesting inside a location key's
  directory: an origin-less `~/src/github.com/acme` beside a clone of
  `github.com/acme/repo` puts the clone's store at
  `github.com/acme/repo/...` inside the first's. The two stay apart,
  because the store reads only regular files in `items/` and skips
  directories there, but they share a directory tree.

The key is a label that says which list a repository reads, not an
authenticated identity: an `origin` is whatever the repository's own
configuration says, and anything able to write that configuration can point
it at any key. What the location rule adds is that a location-derived key
names the directory canga was pointed at, never one the repository's
configuration chose.

The sandbox build never defaults the base, because its `$HOME` is not the
host's: repositories are mounted at their host paths. Its environment sets
`CANGA_SRC_DIR` to the host's base, an absolute host path, exactly as it
sets `CANGA_REMINDERS_DIR`. Unset, an origin-less repository is refused there
and the message names the variable.

## Where a clone lands

```
${CANGA_SRC_DIR:-$HOME/src}/<host>/<owner>/<repo>
```

The three segments come from the URL, with the scheme, any userinfo, any port
and any `.git` suffix removed. Nested owners are kept, so a GitLab subgroup
lands at `gitlab.com/group/sub/proj`. The spelling is the repository's own:
only the reminder store, which is shared between a case-folding and a
case-sensitive filesystem, encodes case (see
[Where the reminders live](#where-the-reminders-live)).

## Transport hardening

Every clone runs with four git options on the command line, where no
repository or user configuration can override them:

```
-c protocol.ext.allow=never -c protocol.fd.allow=never
-c transfer.fsckObjects=true -c fetch.fsckObjects=true
```

The `ext` and `fd` remote helpers run the rest of the URL as a command, so a
URL such as `ext::sh -c ...` is a command execution dressed as a repository.
Turning them off on the command line means a machine whose git config sets
`protocol.ext.allow=always` still refuses one.

One setting outranks a `-c` option: the `GIT_ALLOW_PROTOCOL` environment
variable, which replaces git's protocol policy when it is set. canga removes
`ext` and `fd` from it before running git and keeps every other entry, so an
allow-list such as `https:ssh` still restricts what it restricted. A list
that named only `ext` is left empty, which allows nothing. `file` is left at
git's default, allowed, so local-path clones keep working. The two
`fsckObjects` options make the fetch reject a malformed object graph rather
than write it to disk first.

`canga git sync`'s fetch carries the same hardening.

## Signing

After the clone, SSH signing is written into the new repository's **local**
config: the allowed-signers file when one resolves, and `gpg.format=ssh`,
`user.signingkey`, `commit.gpgsign` and `tag.gpgsign` when a key resolves.

`gpg.format` is written rather than inherited. git's default format is
openpgp, so on a machine that does not set `gpg.format=ssh` globally, a
stamped SSH key would fail every commit with `gpg: skipped "...": No secret
key`. The consequence is deliberate: a clone made by `canga git clone` signs
with SSH, so do not hand a GPG key to `CANGA_HOST_SIGNING_KEY` or leave one in
the global `user.signingkey` and expect it to be used here.

The key fallback reads the **global** git config rather than the effective
one, so the key is the machine's identity and never the local key of
whatever repository you ran the command in. Nothing global is written.

When neither a key nor an allowed-signers file resolves, the clone is left
alone rather than pointed at a file that does not exist. `canga` then
reports on stderr whether the clone signs anyway, because git configuration
outside it turns `commit.gpgsign` on. A sandbox is that case: its
`/etc/gitconfig` carries the format, the flag and a key command, and no
`user.signingkey`. Otherwise it reports `signing OFF` and names what to set.

## How sync advances a branch

Each branch is asked first whether its upstream is already an ancestor of
it. If it is, there is nothing to fast-forward and nothing is run. That
question is not an optimization: without it the two paths below disagree
about the same repository, because `merge --ff-only` answers "Already up to
date" for a branch that is ahead of its upstream while `fetch` refuses the
same state.

The checked-out branch then advances with `git merge --ff-only`, which
refuses rather than touch a working tree it would have to change. Every
other branch advances with `git fetch . <upstream>:<branch>`, and the
missing `+` in front of that refspec is the safety property itself: without
it git refuses a non fast-forward update instead of overwriting the branch.

## Where the environment is found

`canga workspace` looks for a repository's sandbox environment in its own
repository, cloned under the same base directory as every other clone, one
directory per environment:

```
${CANGA_SRC_DIR:-$HOME/src}/<environments repository>/envs/<host>/<owner>/<repo>-env
```

The trailing `-env` is there so the environment directory's basename never
matches the clone's: without it, both directories end in the same `<repo>`,
which is easy to mistake for one another in a listing, a pane or a shell
prompt. The suffix follows [Docker Sandboxes' own documented environment
file layout](https://docs.docker.com/ai/sandboxes/configuration/environment-files/),
which names an environment directory for `web-app` as `web-app-env/`.

The environment directory is not a git repository of its own: it has no
`.git`, and sits inside the working tree of the repository that holds the
environments. Left alone, a `git` command run inside it would walk up past
it and act on that enclosing repository instead, and the `-env` suffix does
not change that; it only keeps the two directories' names apart. The left
pane guards against this; see
[Keeping git out of the envs repository](#keeping-git-out-of-the-envs-repository).

`CANGA_HOST_ENVS_REPO` is a path, not a URL, and must stay under the base
directory. Each segment follows the rule a URL's segments do: letters,
digits and `._~+-`, and never `.` or `..` alone. An absolute path is refused
too. The error does not repeat the value, so a URL pasted by mistake does not
print its credential. The `<host>/<owner>/<repo>` segments keep their case,
as the clone's do.

The environments repository is the one repository whose environment cannot
live apart from it: opening `github.com/acme/docker-sbx` itself finds its
environment inside its own clone.

## Keeping git out of the envs repository

`canga workspace` sets `GIT_CEILING_DIRECTORIES` in the left pane's own
environment, ahead of anything typed into its shell, to the environments
repository's `envs/` directory: the directory every environment sits under.
A `git` command run from the environment pane then answers "not a git
repository" instead of silently acting on the environments repository's
clone.

This guards against an accident, not against someone working around it:
`git -C <environments clone>`, a `GIT_DIR` set explicitly, unsetting
`GIT_CEILING_DIRECTORIES` for the command, or a shell rc that overwrites
rather than composes with it (see below) all reach the environments
repository just as before. It is a fence around the paths git wanders into
by default, not an isolation boundary.

That comes at a price: the pane itself cannot `git add`, `git commit` or
otherwise work on the environment as a git repository any more. Do that from
the environments repository's own clone, or from the pane with
`git -C <environments clone>`.

canga composes its entry with whatever `GIT_CEILING_DIRECTORIES` it
inherited, rather than replacing it, so a value your shell already set is
kept, not lost. Its own rc files still run after cmux sets the variable,
though, so a dotfiles line that assigns `GIT_CEILING_DIRECTORIES` outright,
instead of composing with it, drops canga's entry the moment the shell
starts. Compose it there too, both to survive that and to protect a plain
terminal that `cd`s into the environment directory without going through
`canga workspace` at all, which gets no protection otherwise:

```sh
envs_ceiling="${CANGA_SRC_DIR:-$HOME/src}/github.com/acme/docker-sbx/envs"
case ":${GIT_CEILING_DIRECTORIES:-}:" in
  *":$envs_ceiling:"*) ;;
  *) export GIT_CEILING_DIRECTORIES="$envs_ceiling${GIT_CEILING_DIRECTORIES:+:$GIT_CEILING_DIRECTORIES}" ;;
esac
```

Replace `github.com/acme/docker-sbx` with whatever `CANGA_HOST_ENVS_REPO` is
set to, or, if it is unset, with `<host>/<owner>/docker-sbx` for the owner
of the repository you open most, the same default `canga workspace` itself
falls back to. The `case` guards against duplicating the entry: sourced
twice, in the same shell or across bash and zsh's own rc files, it finds the
entry already there and leaves `GIT_CEILING_DIRECTORIES` alone.

`envs_ceiling` must resolve to an absolute path: git silently ignores a
relative `GIT_CEILING_DIRECTORIES` entry. Set `CANGA_SRC_DIR` itself to an
absolute path if you export it; canga's own `--layout` value never has this
problem, since it always resolves the base directory to an absolute path
first.

## What upgrade verifies

`canga upgrade` downloads a release artifact and swaps one executable file
for another.

**The tag.** `--tag` (and the release the newest-release path resolves to)
is refused unless it passes [the release floor](#the-release-floor).

**The archive.** It is checked against the SHA-256 the release publishes in
`checksums.txt`, and an archive with no line of its own there is refused
rather than waved through. Be clear about what that proves: the bytes
downloaded are the bytes the release names, so a corrupted or truncated
download is caught. It does **not** prove the release is genuine, since the
same account publishes the asset and the checksum beside it. That is
integrity, not authenticity. For provenance, see
[Verify a release](../README.md#verify-a-release).

**The binary.** The new binary is written beside the old one, flushed to
disk, and **run once** to confirm it reports the version and the build
(`host` or `sandbox`) it was downloaded as. Only then is it renamed over the
target. An archive holding something that is not canga, a binary for the
wrong platform, or the other build, fails at that step with the working
binary untouched, instead of after taking its place on your PATH.

Nothing is written outside the directory the binary already lives in, and
no backup is left behind: any release at or above
[the release floor](#the-release-floor) is one `canga upgrade --tag` away,
and a stale `canga.bak` on PATH is a worse problem than the backup solves.
A release below the floor cannot be installed again: `--tag` refuses it.

### Which file it replaces

The path is resolved through symlinks, and the resolved file is the one
replaced. `~/.local/bin` is often a directory of symlinks placed by a
dotfiles manager, and replacing the *name* rather than the file behind it
would quietly turn one of those links into a regular file.

An install names the file it replaced, a run that fails after choosing a
release names the file it was working on, and `--check` names the file it
would replace without touching it. A run that fails before choosing one,
such as on a network error, prints only the error. Whether it also reports having followed a symlink is
up to the operating system rather than to how you invoked it: on Linux the
kernel hands back an already-resolved path, so there is no symlink left to
mention, while on macOS it does not. The file replaced is the right one on
both.

The mode of the file being replaced is kept, so a canga deliberately
installed `0700` does not come back world-executable. Only the owner's
execute bit is restored unconditionally, since an install you cannot run is
not one.

### The token

A GitHub token is optional: with none, `canga upgrade` reads the release
anonymously. What a token buys is GitHub's authenticated rate limit, 5000
requests an hour against 60 for an anonymous client, which one shared
outbound address can exhaust on its own.

`GH_TOKEN` is read first, then `GITHUB_TOKEN`, and failing both, whatever
`gh auth token` answers. That last one is why a token you never exported is
still used on a Mac: `gh` keeps it in the keychain. `gh` is optional: export
`GH_TOKEN` and it is never consulted, install neither and the upgrade still
runs.

A token GitHub rejects, such as an expired or revoked one, does not stop the
upgrade. canga retries that request without it and makes every later
request anonymously. If the anonymous request fails too, the error names
both failures, so the stale token is still reported.

`sudo` resets the environment by default, so in a sandbox `sudo canga
upgrade` does not see `GH_TOKEN` and reads the release anonymously. The
repository is public, so the upgrade still works, within GitHub's anonymous
limit of 60 requests an hour.

### When it refuses to guess

A binary built by `go install`, or from a tree that has moved past its last
tag, reports `dev` or `v0.1.0-3-gabc1234` rather than a release. Neither
names a published artifact, and `v0.1.0-3-gabc1234` sorts *below* `v0.1.0`
under semver, so treating it as a release would offer older code as an
upgrade. Those builds are refused, and `--tag` is how you say which release
you meant.

## The release floor

`install_host.sh`, `install_sandbox.sh` and `canga upgrade` (for `--tag` and
for the newest release alike) refuse a tag before downloading its archive
unless it is both canonical `vX.Y.Z` (no pre-release, no build metadata, no
leading zero) and at or above v0.10.5.

Every release before v0.10.5 was deleted for carrying no build provenance
attestation, tags kept, and could be recreated by anyone with write access,
with bytes and a `checksums.txt` of their own choosing (see the
"Immutability is not retroactive" item in the handoff notes'
[release provenance section](HANDOFF.md#release-provenance-attestation-draft-first-immutable-releases-pr-36)). Only
v0.10.5 onward is published, but the old tags still exist, so an empty
release list was not a guarantee on its own: the tools refuse those tags
outright rather than rely on there being nothing published there to find.

The floor tells an old or otherwise unprotected tag apart from a covered
one. It does not vouch for a brand new tag's contents, which is what the
repository's own tag-protection ruleset is for (see
[Releasing](releasing.md#prerequisites)).

The by-hand install blocks in the README do not apply the floor: whoever
runs them checks the tag themselves.

## The by-hand install check

The README's two by-hand install blocks are a third copy of the checksum
check `install_host.sh` and `install_sandbox.sh` make, and
`readme_install_test.go` runs each one, unchanged, against a fake release.
They are written the way they are for these reasons:

- `$releases/latest` redirects to the newest release, so `%{url_effective}`
  names its tag without parsing any JSON. `-L` makes curl follow that
  redirect; without it, the effective URL is `$releases/latest` itself and
  the tag comes out as `latest`. (Host block only; the sandbox block pins
  `tag` by hand.)
- `awk` selects the one line of `checksums.txt` naming this asset, matching
  the filename field for equality; `grep` would read the dots in the
  filename as wildcards.
- The asset must be listed exactly once: an absent asset yields no line, and
  a release whose `checksums.txt` somehow names it twice is refused rather
  than letting either line win by accident.
- The line must be exactly what goreleaser writes (64 lowercase hex digits,
  two spaces, the name) before its digest is trusted at all, and only then
  is it compared, string for string, against the archive's own sha256,
  computed with `shasum -a 256` on macOS and `sha256sum` in a sandbox.
- This mirrors `install_host.sh`'s own check rather than piping to a
  checker's `-c`/check mode: on the macOS runners' `sha256sum`, check mode
  exits 0 on empty input instead of refusing it, which would have silently
  "verified" an asset `checksums.txt` never listed at all (see
  [the handoff notes](HANDOFF.md#round-5-the-first-macos-run-found-an-installer-checksum-bypass-2026-09-28)).
- Everything after the variables is one subshell, `( ... )`, so a refusal
  (`exit 1`) leaves only that subshell: the reason is printed to stderr,
  your own shell stays open, and the `tar` line, the last one inside it,
  never runs. A failed download stops it the same way.
- It works in a fresh temporary directory that it removes on the way out,
  so an archive or `checksums.txt` already lying in your current directory
  is never the one verified, and the archive is not kept: only `canga` is
  installed.
- The archive also carries `LICENSE` and `README.md`. Naming `canga` in the
  `tar` command extracts the binary alone.
- The blocks hold no `!` and no trailing `#` comment, because a block is
  pasted into interactive shells, which expand the first and may misread
  the second.

## Where the reminders live

```
${CANGA_REMINDERS_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/canga/reminders}/
  <host>/<owner>/<repo>/<scope>/
    items/<id>.md      # one file per reminder: the unit of atomicity
    order/<N>          # versioned order documents; the highest N is current
    tmp/               # staging, deliberately outside the items glob
```

An uppercase letter in the path is encoded as `!` plus its lowercase form, so
`github.com/Acme/Widget` is stored under `github.com/!acme/!widget`. This is
Go's own encoding, the one the module cache uses (`~/go/pkg/mod/github.com`
has `!burnt!sushi` entries for the same reason). It is load-bearing here: a
store is shared between a Mac, whose APFS folds case, and a Linux sandbox,
whose filesystem does not. Unencoded, `Acme/Widget` and `acme/widget` are two
directories on one side and one on the other, so the two sides disagree
about whether they are looking at the same list. The readable spelling is
kept in each item's `repo:` header.

`CANGA_REMINDERS_DIR` is read by both builds. It exists because `$HOME` is
not the same on both sides of a sandbox boundary: a sandbox is handed the
host's path rather than left to derive a different one. On the host it is
normally unset. The store sits under the XDG **data** directory, not a cache
or state directory, because a reminder is your own data and an uninstall
must not take it.

Each item is a plain file. Editing one by hand is a supported way to use
this (`canga reminders path <id>` exists to hand one to an editor), and a
file dropped into `items/` by any other tool is listed like any other.

Sandboxes can write the store, so canga reads an item or the order document
only as a regular file, never through a symbolic link and never waiting on a
FIFO, and only up to a size: 1 MiB for an item (more than a macOS command
line can carry) and 4 MiB for the order document (over 100,000 ids). `list`
skips an entry of `items/` that is not a regular file, as it skips a
directory, and fails on an item over the limit; `add` refuses text that would
make one. `path` only prints a path: the editor opens it later, and follows
whatever is there by then, so open an item a sandbox could have replaced
with the same care as any file it can write.

## Concurrency

Several sessions, in several sandbox VMs, plus you on the host, operate on
one store at the same time. The store takes **no lock**:

- An item is published with `link(2)`, never by writing to its final name.
  Linking onto a name that exists fails and leaves the existing file
  byte-identical, so one call is both the uniqueness check and the atomic
  publish.
- `add` and `rm` never touch the order document. A new item is simply not in
  it and lists at the end; a removed item's stale entry is skipped on read
  and collected by the next `reorder`.
- `reorder` is the one read-modify-write, and it uses the same primitive as
  optimistic concurrency control: it publishes `order/<N+1>`, and if that
  name is taken it re-reads the winner's result and recomputes rather than
  overwriting it.

Nothing is ever overwritten, and no killed process can leave a stale lock
behind, because there is none to leave. `make race` runs this as a
multi-process gate.

Every path is resolved through an `os.Root` rooted at the store directory,
so a crafted id cannot address anything outside it by construction rather
than by validation. Go 1.27 is a correctness floor, not a preference: before
it, a symlink opened with a trailing slash escaped a `Root`.

Nothing below the store root may be a symbolic link: not the store
directory, and not any directory between the root and it. A `Root` follows
its own path, and one rooted at the store root would still follow a link
that stays inside it, so a link at any of those levels would carry reads and
writes to another directory, or to another repository's store. Everything
below the root is shared with sandboxes, which can write it, so canga opens
the root once and enters each directory below it relative to its parent,
refusing a link at every step, and checks after each open that the directory
it holds is still the one at that name. The root itself, and anything above
it, such as a symlinked `~/.local/share`, is your own layout and may be a
link.
