<!--
SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
SPDX-License-Identifier: GPL-3.0-or-later
-->

# canga

**A small, careful CLI for the repositories and agent sandboxes of a working
day.** It puts every clone at a predictable path, syncs them without ever
losing work, opens a repository beside its sandbox, and keeps a per-repository
TODO list that you on your Mac and the agents in your sandboxes share.

It is built for one setup: a Mac running coding agents in
[Docker Sandboxes](https://docs.docker.com/ai/sandboxes/) (`sbx`), with
[cmux](https://github.com/manaflow-ai/cmux) as the terminal. The git commands
and reminders work without either; `canga workspace` needs both.

A day with canga, once it is [installed](#install) and your sandbox
environments are set up:

```sh
cd "$(canga git clone git@github.com:acme/widget.git)"   # ~/src/github.com/acme/widget
canga git sync                                           # fetch, fast-forward, never force
canga workspace https://github.com/acme/widget           # the repo beside its sandbox, in cmux
canga reminders add drop the debug flag from the parser  # a TODO that outlives the session
canga reminders list                                     # the same list, from any sandbox
canga upgrade                                            # replace itself with a verified release
```

canga is one binary built in two roles: a **host build** for your Mac and a
**sandbox build** for the Linux sandboxes your agents run in. Both read and
write the same reminders.

**Try it** on a Mac in under a minute: install, confirm, clone. The
installer verifies the archive against the release's `checksums.txt` and
refuses a tag below [the release floor](#the-release-floor); for who built
the release, see [Verify a release](#verify-a-release).

```sh
curl -fsSL https://raw.githubusercontent.com/brunovenceslau/canga/main/install_host.sh | sh
canga --version
cd "$(canga git clone https://github.com/brunovenceslau/canga)"
```

If the installer says `~/.local/bin` is not on your PATH, add it before
running the next line. The clone lands in
`~/src/github.com/brunovenceslau/canga`. When no signing key is set and no
git configuration outside the clone turns signing on, the clone also prints
a `canga: signing OFF` line on stderr naming what to set; that is expected
until you set up signing (see [Signing](docs/design.md#signing)).

## Contents

- [How it fits together](#how-it-fits-together)
- [Install](#install)
  - [The release floor](#the-release-floor)
  - [Install a release binary](#install-a-release-binary)
  - [Build from source](#build-from-source)
  - [Install it in a sandbox](#install-it-in-a-sandbox)
  - [Use the sbx kit](#use-the-sbx-kit)
  - [How a sandbox shares the host's list](#how-a-sandbox-shares-the-hosts-list)
  - [Verify a release](#verify-a-release)
  - [macOS reports "Apple could not verify canga is free of malware"](#macos-reports-apple-could-not-verify-canga-is-free-of-malware)
- [Commands](#commands)
  - [`canga git clone`](#canga-git-clone)
  - [`canga git sync`](#canga-git-sync)
  - [`canga git setup-hooks`](#canga-git-setup-hooks)
  - [`canga workspace`](#canga-workspace)
  - [`canga reminders`](#canga-reminders)
  - [`canga upgrade`](#canga-upgrade)
  - [Shell completion](#shell-completion)
- [Configuration](#configuration)
- [Exit codes](#exit-codes)
- [Contributing](#contributing)
  - [Commit hook](#commit-hook)
  - [Releasing](#releasing)
  - [Immutable releases](#immutable-releases)
  - [Move the sbx kit's pin](#move-the-sbx-kits-pin)
- [Further reading](#further-reading)
- [License](#license)

## How it fits together

| Build | Runs on | Commands |
| --- | --- | --- |
| host | your Mac (darwin/arm64 and darwin/amd64; published for macOS only) | `git` (clone, sync, hook setup), `workspace`, `reminders`, `upgrade`, `completion` |
| sandbox | inside an agent sandbox (linux/arm64 and linux/amd64; published for Linux only) | `reminders list` and `add`, `upgrade` |

The role is fixed when the binary is built, not chosen at runtime: the
sandbox build does not contain the host's commands, so nothing an agent sets
can reach them. `canga --version` names the role.

Every command is keyed by the repository you are standing in, derived from
its `origin` remote. `git@github.com:acme/widget.git`,
`https://github.com/acme/widget.git` and `ssh://git@github.com:22/acme/widget`
all name `github.com/acme/widget`, so the same repository gets the same clone
path and the same reminders whichever URL you used. A repository with no
`origin` is keyed by its path below the clone base. The full rules are in
[Which key a repository gets](docs/design.md#which-key-a-repository-gets).

In Brazilian barracks slang, your *canga* is your partner in a pair: the one
you do not leave and who does not leave you. Each build covers one side, the
Mac and the sandbox, and neither makes sense without the other.

## Install

Neither the host nor the sandbox install needs a GitHub account or a
credential.

### The release floor

The install scripts and `canga upgrade` install only a tag that is canonical
`vX.Y.Z` (no pre-release, no build metadata, no leading zero) and at least
v0.10.5. Releases before v0.10.5 carry no build provenance and were deleted,
but their tags remain and could be recreated with other bytes, so the tools
refuse them outright. Why, in full:
[The release floor](docs/design.md#the-release-floor).

### Install a release binary

On your Mac, run the install script. It downloads the newest release,
verifies it against the release's `checksums.txt`, and puts `canga` in
`~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/brunovenceslau/canga/main/install_host.sh | sh
```

To install one release instead of the newest, pass its tag:

```sh
curl -fsSL https://raw.githubusercontent.com/brunovenceslau/canga/main/install_host.sh | sh -s -- v0.10.5
```

Then confirm it. The output is the version, the role (`host`), the commit
and the Go version:

```sh
canga --version
```

You run the installer once. From then on, [`canga upgrade`](#canga-upgrade)
replaces the binary for you.

What the script refuses:

- **Linux**, before downloading anything. The host build is published for
  macOS only; the script points you to
  [Install it in a sandbox](#install-it-in-a-sandbox).
- **A tag below [the release floor](#the-release-floor)**, before
  downloading the archive.
- **A checksum that does not match**, or a `checksums.txt` with no line for
  the archive, before extracting anything.

On success it prints the installed version, then warns if `~/.local/bin` is
not on your PATH.

<details>
<summary>Install by hand, without the script</summary>

The block downloads the newest release into a temporary directory, checks
the archive against `checksums.txt`, and extracts only `canga` into
`~/.local/bin`. On an Intel Mac, write `darwin_amd64` in place of
`darwin_arm64`.

These steps do not apply [the release floor](#the-release-floor): check
yourself that the tag is canonical `vX.Y.Z` and at least v0.10.5. They prove
integrity (the archive is the one `checksums.txt` lists), not provenance;
for who built it, see [Verify a release](#verify-a-release).

```sh
releases=https://github.com/brunovenceslau/canga/releases
tag=$(basename "$(curl -fsSL -o /dev/null -w '%{url_effective}' "$releases/latest")")
asset=canga-host_${tag#v}_darwin_arm64.tar.gz

(
  dir=$(mktemp -d) && cd "$dir" || exit 1
  trap 'rm -rf "$dir"' EXIT
  trap 'exit 1' INT TERM
  curl -fsSLO "$releases/download/$tag/$asset" || exit 1
  curl -fsSLO "$releases/download/$tag/checksums.txt" || exit 1

  line=$(awk -v a="$asset" '$2 == a' checksums.txt)
  count=$(printf '%s\n' "$line" | awk 'NF { n++ } END { print n + 0 }')
  [ "$count" = 1 ] || { echo "checksums.txt lists $asset $count times, not exactly once" >&2; exit 1; }
  want=${line%%" "*}
  [ "${#want}" -eq 64 ] && printf '%s' "$want" | grep -Eq '^[0-9a-f]{64}$' && [ "$line" = "$want  $asset" ] || {
    echo "the checksums.txt line for $asset is not a lowercase sha256, two spaces and the name" >&2; exit 1; }
  got=$(shasum -a 256 "$asset" | awk '{ print $1 }')
  printf '%s' "$got" | grep -Eq '^[0-9a-f]{64}$' || { echo "could not compute the sha256 of $asset" >&2; exit 1; }
  [ "$got" = "$want" ] || { echo "$asset has sha256 $got, but checksums.txt lists $want" >&2; exit 1; }

  mkdir -p "$HOME/.local/bin" && tar -xzf "$asset" -C "$HOME/.local/bin" canga
)
```

A refusal or a failed download prints the reason and leaves your shell open;
`tar` runs only when every check passed. Why each line is written the way it
is: [The by-hand install check](docs/design.md#the-by-hand-install-check).

</details>

### Build from source

```sh
GOBIN="$HOME/.local/bin" go install github.com/brunovenceslau/canga/cmd/host/canga@latest
```

`GOBIN` puts the binary on your PATH; the default, `$(go env GOPATH)/bin`,
usually is not. A binary built this way reports its version as `dev`, because
`go install` on a module path applies no ldflags, and `canga upgrade` then
needs an explicit `--tag`. To stamp the version in, build from a checkout
with `make install`.

### Install it in a sandbox

Sandboxes are Linux VMs, and releases ship the sandbox build for Linux only.
In the sandbox's provisioning step, run as root, pin a tag:

```sh
curl -fsSL https://raw.githubusercontent.com/brunovenceslau/canga/main/install_sandbox.sh | sh -s -- vX.Y.Z
```

This installs a root-owned `canga` in `/usr/local/bin`. Root ownership keeps
a process without root from replacing it; it is no boundary against an agent
with sudo, which a Docker Sandbox grants its agent user. The script verifies
the archive and applies [the release floor](#the-release-floor) exactly as
the host script does.

Pin the tag rather than following `latest`, so every sandbox built from one
definition runs the same binary. `canga --version` prints the version, the
role (`sandbox`), the commit and the Go version.

With Docker Sandboxes, the [sbx kit](#use-the-sbx-kit) does all of this for
you.

<details>
<summary>Install by hand, without the script</summary>

Set `tag` to the release this sandbox pins, and write `linux_amd64` in place
of `linux_arm64` on an amd64 sandbox. Unlike the script, this installs into
the current user's `~/.local/bin`, and it does not apply
[the release floor](#the-release-floor): check yourself that the tag is
canonical `vX.Y.Z` and at least v0.10.5. It proves integrity, not
provenance; see [Verify a release](#verify-a-release).

```sh
releases=https://github.com/brunovenceslau/canga/releases
tag=vX.Y.Z
asset=canga-sandbox_${tag#v}_linux_arm64.tar.gz

(
  dir=$(mktemp -d) && cd "$dir" || exit 1
  trap 'rm -rf "$dir"' EXIT
  trap 'exit 1' INT TERM
  curl -fsSLO "$releases/download/$tag/$asset" || exit 1
  curl -fsSLO "$releases/download/$tag/checksums.txt" || exit 1

  line=$(awk -v a="$asset" '$2 == a' checksums.txt)
  count=$(printf '%s\n' "$line" | awk 'NF { n++ } END { print n + 0 }')
  [ "$count" = 1 ] || { echo "checksums.txt lists $asset $count times, not exactly once" >&2; exit 1; }
  want=${line%%" "*}
  [ "${#want}" -eq 64 ] && printf '%s' "$want" | grep -Eq '^[0-9a-f]{64}$' && [ "$line" = "$want  $asset" ] || {
    echo "the checksums.txt line for $asset is not a lowercase sha256, two spaces and the name" >&2; exit 1; }
  got=$(sha256sum "$asset" | awk '{ print $1 }')
  printf '%s' "$got" | grep -Eq '^[0-9a-f]{64}$' || { echo "could not compute the sha256 of $asset" >&2; exit 1; }
  [ "$got" = "$want" ] || { echo "$asset has sha256 $got, but checksums.txt lists $want" >&2; exit 1; }

  mkdir -p "$HOME/.local/bin" && tar -xzf "$asset" -C "$HOME/.local/bin" canga
)
```

It makes the same checks as the host block above, with `sha256sum` in place
of `shasum -a 256`.

</details>

### Use the sbx kit

With [Docker Sandboxes](https://docs.docker.com/ai/sandboxes/) (`sbx`), a kit
installs the sandbox build for you. The kit is this repository's
[`sbx-kit/`](sbx-kit/spec.yaml) directory. It installs the release it pins,
verified against that release's sha256, as a root-owned
`/usr/local/bin/canga`, and allows the egress canga needs: `github.com`,
`api.github.com` and `release-assets.githubusercontent.com`.

Before you start:

- The sandbox has `curl`, from its image or from a kit listed before this
  one. Without it the kit stops with `canga: curl is not installed`.
- sbx allows kits from this repository's owner. By default sbx loads kits
  from `docker.io/` only. The command below replaces the whole list, so
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

2. Check that sbx accepts the kit at that commit. An allowlist problem shows
   up here instead of when the sandbox starts:

   ```sh
   sbx kit validate "git+https://github.com/brunovenceslau/canga.git#ref=<commit>&dir=sbx-kit"
   ```

3. List the kit in your environment file, with that commit as `ref`:

   ```yaml
   kits:
     - git+https://github.com/brunovenceslau/canga.git#ref=<commit>&dir=sbx-kit
   ```

4. Start the sandbox. The kit's install step ends by printing
   `canga --version`, such as `v0.10.5 (sandbox, <commit>, <go>)`.

Pin a commit, not a branch or a tag. The kit's release pin moves in a pull
request after each release (see [Releasing](#releasing)), so a tag's kit
names the release before it, and a branch changes under you. The merge
commit of the newest `chore(sbx-kit): pin canga vX.Y.Z` pull request is the
ref that installs the newest release.

The kit installs the binary only. Sharing the host's reminders still needs
the mount and the variable in
[How a sandbox shares the host's list](#how-a-sandbox-shares-the-hosts-list).
The kit does not register a Claude Code hook: sbx manages the sandbox's
`~/.claude/settings.json`, and a kit has no field for hooks.

#### sbx says the kit's source is not in your allowlist

`sbx kit validate`, and any command that loads the kit, refuses it with
`its source is not in your allowlist` when `kit.allowedSources` does not name
`github.com/brunovenceslau/`, and prints your current allowlist. Add the
entry, keeping every one already listed, with the `sbx settings set` command
under [Use the sbx kit](#use-the-sbx-kit), then run the command again.

### How a sandbox shares the host's list

For the sandbox build to read the reminders you keep on the host, the
sandbox's environment needs:

1. The host's store directory for that repository, mounted into the sandbox.
2. `CANGA_REMINDERS_DIR` set to the host's store root. It is a host path:
   inside the sandbox, `$HOME` is not the host's home.
3. Only for repositories with no `origin`: `CANGA_SRC_DIR` set to the host's
   clone base, also a host path, with the repository mounted at its host
   path below it.

```sh
CANGA_REMINDERS_DIR=/Users/you/.local/share/canga/reminders \
CANGA_SRC_DIR=/Users/you/src \
  canga reminders list
```

Without `CANGA_REMINDERS_DIR`, the sandbox build uses a store inside the
sandbox, which the host never sees. The store's layout is in
[Where the reminders live](docs/design.md#where-the-reminders-live).

### Verify a release

The checksum checks above prove a download is the file a release names. To
check who built it: from v0.10.5 on, every archive and `checksums.txt`
carries a build provenance attestation, a Sigstore-signed statement that
those exact bytes were built by this repository's `release.yml` for that
tag on a GitHub-hosted runner. Check one with `gh` 2.93.0 or newer:

```sh
gh release download v0.10.5 -R brunovenceslau/canga \
  -p canga-sandbox_0.10.5_linux_amd64.tar.gz
gh attestation verify canga-sandbox_0.10.5_linux_amd64.tar.gz \
  --repo brunovenceslau/canga \
  --cert-identity https://github.com/brunovenceslau/canga/.github/workflows/release.yml@refs/tags/v0.10.5 \
  --source-ref refs/tags/v0.10.5 \
  --deny-self-hosted-runners
```

This binds the bytes to `release.yml` at whatever commit the tag named when
the release was built, not to reviewed content; the repository's tag
ruleset is what restricts who can push a tag. What the check does and does
not prove, and why these flags: [Verify a release](docs/releasing.md#verify-a-release)
in the release docs.

### macOS reports "Apple could not verify canga is free of malware"

canga is not notarized: notarization requires a paid Apple Developer Program
membership, which this tool does not have. Gatekeeper only evaluates a file
carrying the `com.apple.quarantine` extended attribute, and the program that
fetched the file decides whether to attach it. Safari, Chrome and Firefox
attach it. `gh`, `curl`, `wget` and `go install` do not, so every install
path above produces a binary that runs without a prompt.

Double-clicking a quarantined archive in Finder copies the attribute onto
every file it extracts, and the extracted `canga` stays quarantined when you
move it. Extracting the same archive with `tar` does not.

Check a file for the attribute (prints it, or `No such xattr`):

```sh
xattr -p com.apple.quarantine <path-to-canga>
```

Use `-p` rather than `xattr -l`: a file can carry
`com.apple.metadata:kMDItemWhereFroms` and nothing else, and Gatekeeper leaves
that file alone. To repair a quarantined binary, strip the attribute:

```sh
xattr -d com.apple.quarantine <path-to-canga>
```

## Commands

| Command | What it does | Build |
| --- | --- | --- |
| [`canga git clone`](#canga-git-clone) | Clone into a deterministic `~/src/<host>/<owner>/<repo>` layout, hardened and signing-ready | host |
| [`canga git sync`](#canga-git-sync) | Fetch every remote and fast-forward every tracking branch; never force | host |
| [`canga git setup-hooks`](#canga-git-setup-hooks) | Install the git hooks a repository keeps under `.canga/hooks` | host |
| [`canga workspace`](#canga-workspace) | Open a repository beside its sandbox environment in cmux | host |
| [`canga reminders`](#canga-reminders) | A per-repository TODO list that outlives the session | host: all; sandbox: `list`, `add` |
| [`canga upgrade`](#canga-upgrade) | Replace this binary with a verified release | both |
| [`canga completion`](#shell-completion) | Generate shell completion | host |

Every command takes `-C, --repo <dir>` to act on a repository other than the
current directory. Scripts and hooks should use it rather than changing
directory. Output meant for pipes goes to stdout; every diagnostic goes to
stderr. `canga <command> --help` has the details.

### `canga git clone`

```sh
canga git clone git@github.com:acme/widget.git         # lands in ~/src/github.com/acme/widget
cd "$(canga git clone https://github.com/acme/widget)"  # the path is all stdout carries
canga git clone https://github.com/acme/widget /tmp/scratch   # an explicit target
```

A clone lands at `${CANGA_SRC_DIR:-$HOME/src}/<host>/<owner>/<repo>`, taken
from the URL with the scheme, userinfo, port and `.git` suffix removed, so the
same repository lands at the same path on every machine, whichever protocol
you used. Nested owners are kept: a GitLab subgroup lands at
`gitlab.com/group/sub/proj`.

The resolved path is the only thing printed on stdout, which is what makes
the `cd "$(...)"` form safe. git's progress and every diagnostic go to
stderr.

**It refuses** a target that already holds anything (exit `1`; an empty
directory is fine), and a URL no path can be derived from (exit `2`, before
anything is created, with any credential stripped from the message). It
never merges into or writes over an existing tree.

**It hardens the transport.** The `ext` and `fd` remote helpers, which run
the URL as a command, are turned off on git's command line and removed from
`GIT_ALLOW_PROTOCOL`, so no configuration can turn them back on; objects are
checked on both sides of the fetch.

**It sets up SSH signing** in the clone's local config, from
`CANGA_HOST_SIGNING_KEY` and `CANGA_HOST_ALLOWED_SIGNERS`, or from your
global git config. It writes `gpg.format=ssh`, so a clone made this way
signs with SSH, never GPG. When no key resolves it stamps nothing and tells
you on stderr whether git configuration outside the clone signs anyway
(`signing OFF` when it does not).

Details: [Transport hardening](docs/design.md#transport-hardening),
[Signing](docs/design.md#signing).

### `canga git sync`

```sh
canga git sync                                  # the repository you are standing in
canga git sync -C ~/src/github.com/acme/widget  # or any other
```

Fetches every remote with `--prune` and `--tags`, then fast-forwards each
local branch that tracks an upstream. **It never resets, forces, merges
non-linearly or deletes anything**, so every outcome is recoverable, which
makes it safe to run across every repository on a machine without reading
them first:

```sh
find ~/src -maxdepth 4 -name .git -type d -exec dirname {} \; | while read -r r; do
  canga git sync -C "$r"
done
```

`-maxdepth 4` matches the `<host>/<owner>/<repo>` layout; raise it if you
keep repositories in nested groups.

A branch that moved is printed on stdout as `<branch><TAB><upstream>`, one
per line, so a sweep's output is a change log rather than an inventory.
A branch with nothing to bring in prints nothing. Refusals and the
dirty-tree notice go to stderr.

| Situation | What happens |
| --- | --- |
| Modified tracked files | The run stops before any branch is touched, and says so. Exit `0`. |
| Untracked files only | The tree does not count as dirty, and the sync proceeds. |
| Branch already level with, or ahead of, its upstream | Nothing to bring in, and nothing printed. |
| Branch diverged from its upstream | Reported on stderr in git's own words, and left exactly where it is. Exit `0`. |
| Branch git refuses for another reason | Same: git's words are printed rather than a guess at them. A branch checked out in a linked worktree, a rebase in progress, or an incoming commit that would overwrite an untracked file all land here. |
| Branch with no upstream | Left out of the report. Nothing was ever asked of it. |
| Branch whose upstream was deleted | Reported on stderr as `upstream <remote>/<branch> is gone`, and left where it is. It may hold commits that were never pushed. Exit `0`. |
| Detached HEAD | Not an error. Every branch is updated without a checkout. |
| Fetch failed | Exit `1`. Deciding branch states against a stale view of the remote would be guessing. |
| Interrupted with Ctrl-C | Exit `1`, naming the cancellation. A branch that was never asked about is never reported as refused. |
| Bare repository, or not a repository | Exit `2`. Syncing needs a working tree. |

The fetch carries the same transport hardening as `canga git clone`. How a
branch is advanced: [How sync advances a branch](docs/design.md#how-sync-advances-a-branch).

### `canga git setup-hooks`

```sh
canga git setup-hooks            # point core.hooksPath at .canga/hooks
canga git setup-hooks --symlink  # or link each hook into .git/hooks
```

Installs the git hooks a repository keeps, tracked, under `.canga/hooks`. By
default it points git's `core.hooksPath` at that directory, which covers
every hook at once and is undone with `git config --unset core.hooksPath`.
git reads hooks from only one directory, so anything already in `.git/hooks`
stops running; the command says so when that is the case, and `--symlink`
links each hook individually instead, which keeps them.

`--force` replaces a conflicting setting and moves any file in the way to
`<name>.bak`. It never deletes, and it never overwrites an existing `.bak`:
the first backup is the pristine one. The command works from any
subdirectory of the repository, and fails with the reason outside one.

The hooks are the repository's own files, so installing them means its
content runs on every commit. Install them in repositories whose contents you
would run anyway.

### `canga workspace`

```sh
canga workspace https://github.com/acme/widget
```

Opens a repository and its sandbox environment side by side in one new
[cmux](https://github.com/manaflow-ai/cmux) workspace, named `acme/widget`
and focused:

| Pane | Starts in | Runs |
| --- | --- | --- |
| Left | `~/src/github.com/acme/docker-sbx/envs/github.com/acme/widget-env`, the environment | `sbx env run --clone --auto-approve`, the repository's sandbox |
| Right, focused | `~/src/github.com/acme/widget`, the clone, where `canga git clone` puts it | nothing |

Use it when you keep each repository's sandbox environment outside the
repository, so that an agent in the sandbox cannot edit the environment that
runs it.

cmux types the `sbx` command into the left pane when its terminal starts,
the way you would. `--auto-approve` applies the environment plan without a
confirmation prompt, since the workspace opens focused on the right pane and
a prompt on the left would go unnoticed. When the sandbox exits, the pane
keeps its shell in the environment directory, so you can start it again from
there. If the left pane shows a prompt and no sandbox, cmux gave up waiting
for the terminal (it waits a few seconds and drops the command without a
message): type `sbx env run --clone --auto-approve` yourself.

**Where the environment is found.** Environments live in their own
repository, cloned under the same base as every other clone:
`<base>/<environments repository>/envs/<host>/<owner>/<repo>-env`. By default
the environments repository is the `docker-sbx` beside the opened repository
(`<host>/<owner>/docker-sbx`), so `github.com/acme/widget` finds its
environment in `github.com/acme/docker-sbx` with nothing to configure. For a
nested group it is the `docker-sbx` in the same innermost group:
`gitlab.com/acme/platform/widget` looks in `gitlab.com/acme/platform/docker-sbx`.
To use environments kept elsewhere, for example to open another owner's
repository with your own environments, set `CANGA_HOST_ENVS_REPO` to a path
under the base (not a URL):

```sh
export CANGA_HOST_ENVS_REPO=github.com/brunovenceslau/docker-sbx
canga workspace https://github.com/acme/widget
```

The left pane gets `GIT_CEILING_DIRECTORIES` set to the environments
repository's `envs/`, so a `git` command there answers "not a git
repository" instead of silently acting on the enclosing environments
clone. To keep that working through your shell's rc files, and to get it in
plain terminals too, see
[Keeping git out of the envs repository](docs/design.md#keeping-git-out-of-the-envs-repository).
Environment directories created before the `-env` suffix need a one-time
rename: [Migrating](docs/migrating.md#environment-directories-without-the--env-suffix).

**Requirements.**

- cmux 0.64.23 or later. The workspace is created with one
  `cmux new-workspace --layout` call, checked against 0.64.23 and 0.64.25.
- Run it from a terminal inside cmux. cmux's default socket mode accepts
  commands only from its own terminals, and its refusal is printed as it is.
- The clone, the environments repository's clone, and the environment
  directory in it already exist. `canga git clone` makes both clones.
- `sbx` on the `PATH` of the shells cmux opens. canga does not check for it:
  if it is missing, the left pane shows that shell's `command not found`.

**What it refuses.** Each refusal happens before cmux is called, and nothing
is created or cloned:

| Situation | Exit |
| --- | --- |
| `CANGA_HOST_ENVS_REPO` is absolute, a URL, or has an empty, `.`, `..` or otherwise invalid segment | `2` |
| A URL no `<host>/<owner>/<repo>` can be derived from | `2` |
| No clone at the derived path. The message suggests `canga git clone <url>` | `1` |
| No environment directory at the derived path. The message names the path and the fixes: clone the environments repository with `canga git clone`, update it with `canga git sync`, create the directory, or set `CANGA_HOST_ENVS_REPO` | `1` |
| `cmux` is not on your PATH | `1` |
| cmux fails. Its own message is shown | `1` |

On success, stdout carries cmux's own reply, such as `OK workspace:3`.

### `canga reminders`

A per-repository TODO list that outlives the session that wrote it. An idea
raised mid-task, on a topic unrelated to the work at hand, is otherwise lost
when the session ends. Every session on the same repository, on the host or
in any sandbox, sees the same list.

```sh
canga reminders add drop the temporary debug flag from the parser
canga reminders list
canga reminders reorder 20260915T142233.482913Z-9f3a1c07  # bump one to the top
canga reminders path                                       # where they live
canga reminders rm 20260915T142233.482913Z-9f3a1c07
```

| Subcommand | What it does | Sandbox build |
| --- | --- | --- |
| `add <text>...` | Record a reminder and print its id. The words are joined with spaces, so no quoting is needed. | yes |
| `list` (`ls`) | Print one `<id><TAB><text>` record per line, ordered items first. The text is the reminder's first line. | yes |
| `reorder <id>...` | Move the named reminders to the front, in the order given | no |
| `path [<id>]` | Print the store directory, or one reminder's file, for an editor or a script | no |
| `rm <id>...` (`remove`) | Remove reminders; every id is attempted and the failures are reported together | no |

`canga todo` is an alias for `canga reminders`. Only records go to stdout, so
`list` pipes.

An agent may surface the list and record an idea, but the list belongs to
the person on the host, so only the host build removes or reorders it. In
the sandbox build those subcommands refuse with exit `2` and say so.

Each reminder is a plain file under
`${XDG_DATA_HOME:-$HOME/.local/share}/canga/reminders/`, one directory per
repository, and editing one by hand is a supported way to use it. The store
takes no lock: several sandboxes and the host can write it at once, and no
write ever overwrites another. Sandboxes can write the store too, so treat a
file `path` hands you like any file a sandbox could replace. Details:
[Where the reminders live](docs/design.md#where-the-reminders-live) and
[Concurrency](docs/design.md#concurrency).

To share the list with a sandbox, see
[How a sandbox shares the host's list](#how-a-sandbox-shares-the-hosts-list).

### `canga upgrade`

```sh
canga upgrade                # install the newest release
canga upgrade --check        # say what is available, change nothing
canga upgrade --tag v0.10.5  # install exactly that release
```

Replaces the running binary with a published release, in place. Each build
replaces itself with the same build: the host build installs a `canga-host_`
archive, the sandbox build a `canga-sandbox_` one. Only the release tag goes
to stdout, so `v=$(canga upgrade)` is the version now installed.

Before it replaces anything:

- The tag must pass [the release floor](#the-release-floor).
- The archive must match its line in the release's `checksums.txt`. That
  proves the download is intact, not that the release is genuine: the same
  account publishes both. That is integrity, not authenticity; for
  provenance, see [Verify a release](#verify-a-release).
- The new binary is written beside the old one and **run once** to confirm
  it reports the version and build it was downloaded as. Only then is it
  renamed over the old one, so a bad archive leaves the working binary
  untouched.

It prints the path it is about to write, and `--check` prints the same path
without touching it. It replaces the file a symlink points to, not the
symlink; keeps the file's mode; and leaves no backup behind, since the
previous release is one `canga upgrade --tag` away.

A GitHub token is optional. `GH_TOKEN`, then `GITHUB_TOKEN`, then whatever
`gh auth token` answers raises GitHub's rate limit from 60 to 5000 requests
an hour; with none, or with one GitHub rejects, the release is read
anonymously.

**It refuses** on an operating system its build is not published for (exit
`2`, before any request), and from a binary that is not a release, such as
one built by `go install` (`dev`) or past its last tag
(`v0.1.0-3-gabc1234`): name the release you mean with `--tag`.

Details: [What upgrade verifies](docs/design.md#what-upgrade-verifies).

#### Upgrade it in a sandbox

`canga upgrade` moves a running sandbox to a newer release. The pin still
decides the release every sandbox starts with: recreating the sandbox
installs the pinned release again. To change the release of every sandbox,
change the pin.

The sandbox's egress policy must allow `api.github.com`, where canga reads
the release, and `release-assets.githubusercontent.com`, where GitHub
redirects the download. And you must be able to write to the directory that
holds the binary, since the new binary is staged there. So run it with
`sudo` when root owns the binary, as it does after `install_sandbox.sh` or
the kit:

```sh
sudo canga upgrade
```

A copy in a directory you own, such as `~/.local/bin`, needs no `sudo`. On
success, stdout holds the installed tag and stderr says
`canga: installed <new> over <old> at <path>`. `canga --version` then reports
the new release and the role `sandbox`.

`sudo` resets the environment by default, so `GH_TOKEN` does not reach the
upgrade and canga reads the release anonymously. The repository is public,
so that still works, within GitHub's anonymous limit.

### Shell completion

The host build generates completion for bash, zsh, fish and PowerShell. For
zsh, generate it once, at install time, and source the cached file; never
`eval` a generator on the shell startup path:

```sh
mkdir -p "${XDG_CACHE_HOME:-$HOME/.cache}/canga"
canga completion zsh > "${XDG_CACHE_HOME:-$HOME/.cache}/canga/_canga"
```

Then, in `~/.zshrc`, after `compinit`:

```sh
source "${XDG_CACHE_HOME:-$HOME/.cache}/canga/_canga"
```

`canga completion <shell> --help` has the steps for each shell.

`reminders rm` and `reorder` complete real stored ids, each shown with its
reminder's first line. `-C` and `git clone`'s optional target complete
directories only. The URL arguments of `git clone` and `workspace` complete
nothing: a half-typed URL is not a path, and falling back to file completion
there would offer the current directory's contents.

## Configuration

canga has no config file. Everything is an environment variable, and every
one is optional on the host.

| Variable | Default | What it sets | Read by |
| --- | --- | --- | --- |
| `CANGA_SRC_DIR` | `$HOME/src` | Root of the clone layout, and what an origin-less repository's key is relative to. In a sandbox there is no default: set it to the host's base, a host path. | both |
| `CANGA_REMINDERS_DIR` | `${XDG_DATA_HOME:-$HOME/.local/share}/canga/reminders` | Root of the reminders store. In a sandbox, set it to the host's store root. | both |
| `CANGA_HOST_SIGNING_KEY` | `git config --global user.signingkey` | SSH key stamped into a new clone. | `git clone` |
| `CANGA_HOST_ALLOWED_SIGNERS` | `git config --global gpg.ssh.allowedSignersFile` | Allowed-signers file wired into a new clone, so `git log --show-signature` works there. | `git clone` |
| `CANGA_HOST_ENVS_REPO` | `<host>/<owner>/docker-sbx`, beside the opened repository | The environments repository, as a path under the base, such as `github.com/acme/sandboxes`. | `workspace` |
| `GH_TOKEN`, `GITHUB_TOKEN` | `gh auth token` | GitHub token for a higher rate limit. | `upgrade` |
| `CI` | unset | When set to anything, drops git's `\r` progress meter. | `git clone` |

`CANGA_HOST_BASE_DIR`, the old name of `CANGA_SRC_DIR`, is refused with exit
`2` while it is set. Upgrading from devctl, agtctl or an older canga:
[Migrating](docs/migrating.md).

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | success |
| `1` | a runtime failure (including git failing to read a repository, such as an unreadable directory or `.git/config`), a clone target that already holds something, an id with nothing behind it, or a workspace whose clone or environment directory is missing |
| `2` | a bad invocation, a directory that is not a repository, or has no usable `origin` and no key from its location, `CANGA_HOST_BASE_DIR` still set, an upgrade with no release to work from or on an operating system the build is not published for, or `workspace` with a `CANGA_HOST_ENVS_REPO` outside the base directory |

## Contributing

canga is written in Go (1.27 or later: the reminders store relies on its
`os.Root` fixes). Every gate is a `make` target, and CI invokes the target
rather than restating it, so what CI runs is what you checked locally.

```sh
make tools   # install the pinned golangci-lint and govulncheck
make ci      # lint, license check, cross, test, e2e-sandbox, govulncheck: green before a push
```

| Target | What it does |
| --- | --- |
| `make build` | Build `bin/host/canga` and `bin/sandbox/canga`, version and commit stamped in |
| `make install` | `go install` the host build into `GOBIN` |
| `make fix` | Apply every automatic fix: `go fix`, the formatters, `--fix` linters |
| `make lint` | `go vet` and golangci-lint, once for each published OS |
| `make test` | `go test -race -shuffle=on ./...` with coverage |
| `make test-host` | The host leg: host build, shared packages, `install_host.sh` |
| `make test-sandbox` | The sandbox leg: sandbox build, shared packages, release tooling |
| `make e2e-sandbox` | Build the sandbox binary and drive it as an agent would |
| `make race` | The multi-process store race gate, verbosely |
| `make cross` | Compile exactly the published set of platforms |
| `make license-check` | Every commentable tracked file carries its SPDX tag |
| `make release-preflight` | Checks to run on a signed tag before pushing it |
| `make release-kit-bump TAG=vX.Y.Z` | Pin the sbx kit to a release the workflow published |

Gates test exactly the expected usage: the host build's tests run on macOS,
arm64 and Intel (`macos-26`, `macos-26-intel`), including a test of the
reminders key on a real case-insensitive APFS volume; the sandbox build's
tests and its E2E run on Linux, arm64 and x64 (`ubuntu-26.04-arm`,
`ubuntu-26.04`). Runner labels are pinned, never `*-latest`, and the Ubuntu
version tracks the sbx sandbox image. `make race RACE_PROCS=12` runs the
store's concurrency gate at full size; the default is sized for CI.
`make race RACE_STORE_DIR=/path` runs it against a filesystem of your
choosing, which is how the store's invariants were checked over a virtiofs
mount rather than assumed to hold there.

### Commit hook

`.canga/hooks/pre-commit` runs `make pre-commit`, which applies every fix a
tool can apply on its own, and then refuses the commit if anything changed.
It refuses rather than amending on purpose: a hook that rewrites files and
lets the commit through commits something you never read. Install it with
[`canga git setup-hooks`](#canga-git-setup-hooks).

`make pre-commit` is deliberately a fast subset rather than `make ci`. A hook
slow enough to be annoying is a hook that gets `--no-verify`d, and then it
guards nothing.

### Releasing

A signed `v*` tag, pushed, is the whole trigger: `.github/workflows/release.yml`
builds every platform with GoReleaser, attests it, and publishes the release.
Nothing local builds or uploads an artifact. In short:

1. Check that the previous release's kit bump has merged.
2. `git tag -s vX.Y.Z -m vX.Y.Z`, then `make release-preflight`, then
   `git push origin vX.Y.Z`.
3. Watch the run: `gh run watch --repo brunovenceslau/canga`.
4. Once it is green, `make release-kit-bump TAG=vX.Y.Z`, then push the
   branch it prints and open, review and merge its pull request.

The full runbook and the prerequisites (immutable releases and a tag
ruleset) are in [docs/releasing.md](docs/releasing.md). Every refusal, with
its fix, is in its table
[What stops a release](docs/releasing.md#what-stops-a-release).

### Immutable releases

The repository runs with GitHub's immutable releases setting on: no asset of
a published release can be replaced, added or deleted, and its tag cannot be
moved. A bad release is fixed by a new patch release. `make
release-preflight` refuses unless the setting is on. Details:
[Immutable releases](docs/releasing.md#immutable-releases).

### Move the sbx kit's pin

`sbx-kit/spec.yaml` pins a release by version and by the sha256 of both
`canga-sandbox_` archives, so the pin can only move after the release
exists: `make release-kit-bump TAG=vX.Y.Z` verifies the published archives
and commits the new pin on branch `chore/sbx-kit-vX.Y.Z`. If that branch is
lost before it merges, run the same command again; it re-derives the
identical pin from the release. Nothing merges it for you, and
`make release-preflight` refuses the next release until it has merged. How
it verifies, older release lines, and the lower-level `rewrite`:
[Move the sbx kit's pin](docs/releasing.md#move-the-sbx-kits-pin).

## Further reading

- [How canga works](docs/design.md): the guarantees behind each command,
  what they protect against, and where they stop.
- [Releasing](docs/releasing.md): the maintainer's release runbook,
  provenance, and the sbx kit pin.
- [Migrating](docs/migrating.md): moving from devctl and agtctl, and other
  one-time changes.
- [docs/HANDOFF.md](docs/HANDOFF.md): the project's working log and open
  items.

## License

canga is free software under the [GNU General Public License v3.0 or
later](LICENSE):

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
