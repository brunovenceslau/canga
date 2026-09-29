<!--
SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
SPDX-License-Identifier: GPL-3.0-or-later
-->

# Releasing

This is the maintainer's runbook: how a canga release is cut, what can stop
it, how its provenance is checked, and how the sbx kit's pin follows it. To
install or verify a release as a user, see the [README](../README.md).

- [How a release happens](#how-a-release-happens)
- [Prerequisites](#prerequisites)
- [Publish a version](#publish-a-version)
- [What stops a release](#what-stops-a-release)
- [Why GoReleaser packages the release](#why-goreleaser-packages-the-release)
- [Verify a release](#verify-a-release)
- [Immutable releases](#immutable-releases)
- [Move the sbx kit's pin](#move-the-sbx-kits-pin)

## How a release happens

`.github/workflows/release.yml` is the one official release path: a signed
`v*` tag, pushed, is the whole trigger. It builds every platform with
GoReleaser, attests what it built, and publishes the release itself, notes
and all - nothing local builds or uploads a release artifact any more. The
one thing left for a human to do afterwards is move the sbx kit's pin,
which needs the finished release's `checksums.txt` and cannot happen before
it exists.

## Prerequisites

Two repository settings, both on before any tag is pushed:

- [Immutable releases](#immutable-releases). `make release-preflight` checks
  this one itself (`check-immutable`).
- A ruleset on `refs/tags/v*` restricting tag creation, update and deletion
  to the admin role. Nothing here checks it independently; see
  `docs/HANDOFF.md`, "Left open", for the exact payload. The ruleset is what
  actually restricts who can push a `v*` tag at all - and so who can trigger
  a release, or tag a commit whose `release.yml` an attestation would then
  vouch for (see [Verify a release](#verify-a-release)).

Signing is the only local setup: `make release-kit-bump` commits the pin
signed (`git commit -S`), so git must already be set up to sign commits, as
it is for any commit to this repository.

## Publish a version

1. Check that the previous release's kit bump has merged: `main`'s
   `sbx-kit/spec.yaml` must pin the newest published release. Step 2's
   `make release-preflight` refuses otherwise; see
   [Move the sbx kit's pin](#move-the-sbx-kits-pin).

2. Tag the commit, signed, and run the preflight before pushing it.

   ```sh
   git tag -s vX.Y.Z -m vX.Y.Z
   make release-preflight
   git push origin vX.Y.Z
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
      and attaches two `canga-host_` archives (darwin, amd64 and arm64),
      two `canga-sandbox_` archives (linux, amd64 and arm64), and
      `checksums.txt`.
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
   make release-kit-bump TAG=vX.Y.Z
   ```

   This downloads the release's `checksums.txt` and both `canga-sandbox_`
   archives, checks them against each other and against the digests GitHub
   serves, and commits the pin, signed, on branch `chore/sbx-kit-vX.Y.Z` -
   from a temporary detached worktree at the tag, so the checkout you ran
   this from is left exactly as it was.

5. Push that branch and open its pull request. `release-kit-bump` prints
   both commands; they are left to you because they publish:

   ```sh
   git push -u origin chore/sbx-kit-vX.Y.Z
   gh pr create --base main --head chore/sbx-kit-vX.Y.Z --fill
   ```

6. Review and merge that pull request. Its merge commit is the ref an
   environment pins ([Use the sbx kit](../README.md#use-the-sbx-kit)).

Run step 4 again, before its branch is pushed or merged, and it changes
nothing: it verifies the existing branch pins the same hashes and stops.

A published release is final. Immutable releases are on (see
[Immutable releases](#immutable-releases)), so GitHub refuses to change a
published release's assets at all, and a bad release is fixed by cutting a
new patch release, never by rebuilding the old one.
`SBX_KIT_ALLOW_CLOBBER` is a local override for `check-clobber`, not
something the workflow reads. It dates from before the setting was on, when
rebuilding a tag's archives was still possible, for the case where breaking
every sandbox pinned to the old archives was genuinely the point. With
immutable releases on, it only gets a rebuild past `check-clobber`: GitHub
still refuses to change the published assets.

## What stops a release

Every condition below is checked before anything slow, so a mistake costs a
second rather than a wasted Release run or a broken sandbox pin. The check
that raises it is in parentheses.

| It stops when | Do this |
| --- | --- |
| HEAD carries no tag (`release-preflight`) | Tag the commit you are releasing. |
| HEAD carries more than one tag (`release-preflight`) | Delete the tag you are not releasing. |
| `sbx-kit/spec.yaml` at the tag does not pin the newest published release below it, or pins hashes GitHub does not serve for it (`check-previous`, run by `release-preflight` and the workflow) | Merge the previous release's `chore/sbx-kit-vX.Y.Z` pull request, then re-tag on top of it. |
| The tag already carries `canga-sandbox_` archives, and its kit bump is pushed or merged (`check-clobber`, run by `release-preflight` and the workflow) | Cut a new patch release. (`SBX_KIT_ALLOW_CLOBBER=<the tag>` only skips this local check; with immutable releases on, GitHub still refuses the rebuild.) |
| `gh` answers 404 for the tag's release and either cannot see `brunovenceslau/canga` at all or sees it under another name, so the 404 proves nothing; the refusal names what `gh` saw (`check-clobber`, run by `release-preflight` and the workflow) | Authenticate `gh` with a token that can read `brunovenceslau/canga`, or update the script's `repo=` if it was renamed or transferred. |
| The tag is below the newest published release, an older line (`release-preflight`, the workflow, and `release-kit-bump` each make this check, independently) | Release from the newest line, or set `SBX_KIT_OLDER_LINE=<the tag>`. |
| `gh` is not installed (`release-kit-bump`) | Install `gh`. |
| `release-kit-bump` was run without `TAG=`, or `TAG` is not exactly `vX.Y.Z` | Pass it: `make release-kit-bump TAG=vX.Y.Z`. |
| The tag as fetched into this checkout resolves to a different commit than GitHub resolves it to (`release-kit-bump`) | Something is wrong with `origin` or with GitHub's own view of the tag. Do not proceed until they agree. |
| The tag predates `scripts/sbx-kit-pin.sh` (`release-kit-bump`) | Nothing to do: the kit has already moved past any release this old. |
| A release at or above v0.10.5 has an archive with no build provenance attestation from `release.yml` for its own tag, or one whose downloaded bytes are not the pinned ones (`check-previous`, `release-kit-bump`) | Do not pin it. Cut a new patch release through the workflow. |
| `gh` is older than 2.93.0 and the release needs its attestation checked (`check-previous`, `release-kit-bump`, the workflow's pre-publish check) | Upgrade `gh`. |
| The repository does not have immutable releases on, or `gh`'s token cannot tell because it lacks admin access (`release-preflight`) | Turn immutable releases on, or run the preflight as a repository admin. |
| The release published but did not settle to `immutable == true` within the retry budget (the workflow's "Publish the release" step) | See [A release that did not settle to immutable](#a-release-that-did-not-settle-to-immutable). |
| The job runs past its 30-minute `timeout-minutes` (`release.yml`) | See [A run that timed out](#a-run-that-timed-out). |

### A release that did not settle to immutable

The release is already public: do not re-run the workflow for this tag.
Check the setting with `make release-preflight` (`check-immutable`). If it
was off, turn it on and cut a new patch release. If it is on, the release
may already be immutable; confirm with:

```sh
gh api repos/brunovenceslau/canga/releases/tags/<tag> --jq .immutable
```

### A run that timed out

Recovery is the same as for any other failed run, at whichever step it was
in:

- **Before "Publish the release":** the tag has at most a draft, which the
  next run of the same tag replaces (GoReleaser deletes and recreates the
  name-matched draft).
- **During or after that step:** the release may already be public. Check
  with the command below. This endpoint does not return a draft release, so
  a 404 means nothing is public yet and a re-run is safe. A JSON answer
  means the release is already published: confirm `immutable` before
  deciding whether to cut a new patch release instead of re-running.

```sh
gh api repos/brunovenceslau/canga/releases/tags/<tag> --jq '{draft, immutable}'
```

### Why HEAD must carry exactly one tag

A second tag on the same commit has no single answer to "which release is
this": `release-preflight` cannot tell which one you mean, even though the
workflow only ever sees whichever ref its own push triggered it from.

## Why GoReleaser packages the release

The workflow calls GoReleaser rather than packaging with `tar` and `shasum`,
so `.goreleaser.yml` stays the single definition of the artifact format. The
reason is `canga upgrade`: each build finds its asset by its own prefix
(`canga-host_` or `canga-sandbox_`) and the `_<os>_<arch>.tar.gz` suffix, and
reads `checksums.txt` by exact filename. A second packaging implementation
that drifted from the first would break upgrading, for whoever ran it next,
rather than releasing, for whoever changed it.

## Verify a release

The split with the README is deliberate: the README's
[Verify a release](../README.md#verify-a-release) holds the command a user
runs, with the exact `gh attestation verify` flags, and states its caveat in
one sentence. This section is the full account of what that command proves
and what it does not.

From v0.10.5 on, every archive and `checksums.txt` of a release carries a
build provenance attestation: a Sigstore-signed statement, stored on this
repository, that those exact bytes were built by `release.yml` running for
that release's tag on a GitHub-hosted runner.

The flags in the README command are the ones the kit pin checks use.
`--cert-identity` requires the signing certificate's workflow identity to
be exactly that URL. The shorter-looking `--signer-workflow` is not used on
purpose: `gh` turns its value into a regular expression anchored only at the
start, so a workflow file or ref that merely begins with the expected one
would pass too.

What this proves is narrower than it sounds: it binds the bytes to
`release.yml` **at whatever commit the tag named when the release was
built**, not to reviewed or merged content, and the check matches that
binding by the tag's name alone - not by which commit the tag names now.
Anyone who can push a `v*` tag can tag a commit that carries a modified
`release.yml`, and that run's attestation verifies the same way - the
command cannot tell the two apart. What actually keeps a tag from being
moved to another commit after the fact, and so restricts who can push a
`v*` tag at all, is the repository's tag ruleset (see
[Prerequisites](#prerequisites)), not this attestation.

Releases before v0.10.5 have no attestation, so for those the command fails
with a 404; they are checked by digest and uploader alone (see
[Move the sbx kit's pin](#move-the-sbx-kits-pin)).

## Immutable releases

The repository runs with GitHub's immutable releases setting on. It was
enabled on 2026-09-27, after the draft-first workflow above had merged and
before v0.10.5 was tagged (`docs/HANDOFF.md`, "Confirmed: immutable releases
were enabled before v0.10.5 (2026-09-27)"). The order mattered: an immutable
release refuses any asset change once it is published, so a workflow that
published first and uploaded afterwards would fail on its own upload. Draft
first works, because a draft stays editable until the workflow publishes
it.

What it changes:

- No asset of a published release can be replaced, added or deleted, by
  anyone, `SBX_KIT_ALLOW_CLOBBER` included. A bad release is fixed by a new
  patch release.
- The release's tag cannot be moved or deleted.
- GitHub adds a release attestation of its own on publish, which
  `gh release verify` checks. It is not what the kit pin checks rely on:
  they check the build provenance above, which names the workflow that
  built the bytes, not only the release that holds them.

## Move the sbx kit's pin

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
new tag sits on top of it. The Release workflow makes the same check. Before
any of this existed the pin moved by hand, and the kit stayed on v0.8.0
through v0.9.0, v0.10.0 and v0.10.1.

### Older release lines

The kit follows the newest release line and never moves backwards. A
release below the newest published one (a fix on an older line) is refused
unless `SBX_KIT_OLDER_LINE` names its tag, such as `SBX_KIT_OLDER_LINE=v0.9.1`
for both `make release-preflight` and `make release-kit-bump TAG=v0.9.1`.
With it set, the preflight only asks that the kit at the tag pins some
published release below it, and the bump step prints a warning and commits
no branch.

### A lost bump branch

If the bump branch is lost before it merges, run `make release-kit-bump
TAG=vX.Y.Z` again. There is nothing to reconstruct by hand: the target
always downloads the tag's `checksums.txt` and archives fresh from the
release and re-derives the pin from them, so a lost branch and a first run
go through the exact same path and commit the exact same pin (a new commit,
re-signed, but pinning the identical version and hashes).

### The lower-level `rewrite`

`scripts/sbx-kit-pin.sh rewrite X.Y.Z <checksums.txt>` is the primitive
underneath `bump`: it rewrites `CANGA_VERSION` and both `sha256` values in
the *working tree's* kit, with no commit and no branch. It exists for the
one case `bump` cannot cover - a release cut before this script existed, so
there is no tag-side copy of it to run `bump` from (the v0.10.2 pin was
moved this way). Like `bump`, it always checks the sums it is given against
release `vX.Y.Z`'s published digests before writing anything, and refuses
on any mismatch, a missing digest, a draft or a prerelease, an asset not
uploaded under the `github-actions[bot]` identity, or, from v0.10.5 on, an
archive without a verifying build provenance attestation from
`release.yml` - there is no flag to skip any of it.

### The v0.10.5 cutover

The cutover is a constant in `scripts/sbx-kit-pin.sh` (`attested_from`),
with no flag or variable to move it: releases below it were published
before the workflow attested anything, and are checked by digest and
uploader alone, which proves some workflow in this repository uploaded
them, not the Release workflow specifically. The constant changes only by a
reviewed pull request.
