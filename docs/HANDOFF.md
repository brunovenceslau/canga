<!--
SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
SPDX-License-Identifier: GPL-3.0-or-later
-->

# Handoff notes

This file is the durable record for decisions that must survive past a single
sandbox session: findings the operator explicitly declined (with the
agreement quoted), and process debriefs from rework that a better process
would have prevented. Entries are appended in chronological order, oldest
first, grouped by the pull request they came from.

## PR #26: verify `sbx` CLI flags before writing docs

The `sbx` CLI is not installed inside the sandbox, so any flag or CLI-surface
claim about `sbx` (for example, `sbx env run --clone --auto-approve`) must be
verified against operator-supplied `sbx ... --help` output, never guessed.

## PR #29 (closes #27): declined findings and ship-gate rework debrief

PR #29 ("chore(sbx-kit-pin): address ship-gate follow-ups from #27") closed
out the remaining follow-ups from the sbx-kit pin's ship gate opened in
PR #27. Two findings from its own ship gate were declined by the operator,
and the ship gate itself surfaced a process defect worth recording.

### Declined findings (operator-accepted)

Both findings target the sbx-kit pin surface (`scripts/sbx-kit-pin.sh` and
`sbxkitpin_test.go`):

1. `security-auditor` (Low): sanitize `${seen}` before printing it to stderr
   in the `check-clobber` refusal. Declined: the value comes from gh's own
   lookup, and it goes to the operator's terminal, not a shell or a log
   parsed elsewhere. GitHub repository names are restricted to
   `[A-Za-z0-9._-]`, so no terminal-escape injection is expressible through
   this value.
2. `test-engineer` (Info): no test for "gh succeeds with an empty name".
   Declined: the current gh stub cannot express that case, and the
   `${seen:-nothing}` default already covers the refusal message for it.

Operator agreement (2026-09-26): "vou seguir sua recomendação nos 2 decisions
pendentes"

### Rework debrief: verifier tree not frozen during the round-2 ship gate

During PR #29's round-2 ship gate, the builder node edited the tree twice
while the round-2 `test-engineer` verifier was still running, violating the
"verifying subagents freeze their tree" rule. The `test-engineer` noticed the
mismatch and re-verified the converged state, and `make ci` was rerun clean
afterward, so nothing shipped unaudited - but the baseline the verifier had
started against was invalidated mid-review, and the re-verification cost an
extra round that a frozen tree would not have needed.

Lesson: the builder must not write to the tree while any verifier is out;
collect all verdicts first, then apply them in one write pass.

Per house rule, every such lesson asks whether a static tool closes the
class. Proposed (not implemented) mechanical guards, either of which would
turn this into a detectable violation instead of a caught-by-luck one:

- Each verifier records `git rev-parse HEAD` plus a content fingerprint
  (`git status --porcelain` and `git diff | sha256sum`) at the start and end
  of its run, and voids its own verdict if either differs.
- The builder snapshots the tree hash when it dispatches verifiers, and
  refuses to apply any verdict whose recorded baseline hash no longer
  matches the tree at apply time.

## PR #31: the v0.10.2 pin, rebuilt after the fact

The Release workflow for v0.10.3 refused in `sbx-kit-pin.sh check-previous`:
the kit still pinned 0.10.1 while v0.10.2 was published. v0.10.2 was tagged
(at `bf7507a`) before `scripts/sbx-kit-pin.sh` existed, so its release never
produced a `chore/sbx-kit-v0.10.2` bump branch. The branch was not lost; it
never existed. The README recovery recipe ("Move the sbx kit's pin") runs
`bump` from the tag, and at that tag the script is absent, so the recipe
cannot work for this release.

What was done instead:

- PR #31 ("chore(sbx-kit): pin canga v0.10.2") moves the pin in one commit
  on top of `main` (`5358eb9`), not on top of the tag. Its branch therefore
  is not "one verified commit on top of the tag", and a later
  `bump v0.10.2` would refuse it. That is expected; nothing needs to run
  `bump` for v0.10.2 again.
- The hashes were checked against four sources, all equal: `sha256sum -c`
  of the downloaded archives against the release's `checksums.txt`; the
  sha256 digests GitHub serves for the assets; a fresh download hashed
  independently by a ship gate; and, as the one anchor independent of the
  release's own assets, GoReleaser's build-time artifact metadata in the
  v0.10.2 Release run (Actions run 36217176585, at `bf7507a`). That log
  shares the build's trust root and expires with the repository's log
  retention, so the sums it carried are copied here:

  ```
  sha256:6b06a59c79b613546782c31c1b42cf117df214d130ec90e888344ad7733fccf7  canga-sandbox_0.10.2_linux_amd64.tar.gz
  sha256:c7fcbbcdc24b4b72c1fbe770f45350cf99ac28d9f8e8897cb94628977d46976f  canga-sandbox_0.10.2_linux_arm64.tar.gz
  ```

- With that pin committed, `check-previous v0.10.3`,
  `check-previous v0.10.4` and `check-clobber v0.10.3` all pass.
- v0.10.3 is skipped: its tag stays at `5358eb9` (the Go module proxy may
  already hold it), its GitHub release, which had no assets, was deleted on
  2026-09-26 so `check-previous` does not ask for a pin of it, and the next
  release is v0.10.4.
  Operator agreement (2026-09-26): "vamos Pular para v0.10.4".

### Rework debrief: a recovery path that assumed its own tooling

The `check-previous` refusal and the README both describe the missing branch
as lost and send the reader to rerun `bump` from the tag. That holds only
for tags cut after the script existed. v0.10.2 was the last release before
it, so the gap cannot recur for later tags, but the recovery path itself
still relies on `rewrite`, which checks nothing against GitHub.

Static-tool question: a `rewrite` mode that runs the same published-digest
check as `check-previous` would make this recovery mechanical instead of
hand-verified. It touches the release-pin surface, so it waits for an
operator decision and is not a pending item.

## PR #33: v0.10.4, the first release on the recovered pin

Adds what the PR #31 entry above left open, with measurements from
2026-09-26.

- v0.10.3 is burned, not only skipped. The operator deleted its asset-less
  GitHub release; asked who did, the answer was (verbatim, pt-BR): "Fui
  eu". The tag stays at `5358eb9`, and it must never move: the Go checksum
  database already records the version (`sum.golang.org` lookup:
  `github.com/brunovenceslau/canga v0.10.3
  h1:jPrvhGQd6/Ek9tyKJ+JnNCffkSfG16u/QOSjDAInYds=`), and
  `proxy.golang.org` resolves it to `Origin.Hash` `5358eb9`. A re-tag would
  give one version two commits: `go install ...@v0.10.3` keeps serving
  `5358eb9`, and `GOPROXY=direct` fails with a checksum mismatch, which on
  this signing trust root reads as tampering.
- v0.10.4 is the first annotated, SSH-signed tag since v0.4.0 (tag object
  `91a0611`, on `20f5538`; `git tag -v` and GitHub's
  `verification.verified` both pass). Measured on 2026-09-26 with
  `git cat-file -t` over every tag: v0.2.0, v0.4.0 and v0.10.4 are
  annotated and SSH-signed, and GitHub marks all three verified; v0.1.0,
  v0.3.0 and v0.5.0 through v0.10.3 are lightweight, the kind GitHub's
  "create release" makes, although README "Releasing" step 2 says
  `git tag -a`. The operator chose the
  annotated, signed form (verbatim, pt-BR: "Anotada e assinada") so the
  tag a kit pin descends from carries its own verifiable signature, like
  the commits do. The Release workflow and GoReleaser build it unchanged
  (run 36232145634, all steps green).
- v0.10.4 was built by the Release workflow, not by a local `make release`,
  so no bump branch came with it; `release.yml` never writes one. Its bump
  came from the README "Move the sbx kit's pin" lost-branch recipe
  (`bump v0.10.4` from the tag, on the release's own `checksums.txt` and
  archives), which works for any tag cut after `scripts/sbx-kit-pin.sh`
  existed. Until the workflow path produces the bump itself, every release
  it builds needs that recipe run by hand.

## Release path: the workflow is the one official path

Operator decision (2026-09-26), answering "which release path is official"
(verbatim, pt-BR): "Workflow oficial (Recomendado)". Meaning: a signed
annotated tag, pushed, is the whole release action; `.github/workflows/release.yml`
builds every platform with GoReleaser and publishes the release itself
(`release --clean`, `changelog.use: github-native` for GitHub's own
generated notes). `make release` (the local build-and-upload target) is
removed, not merely deprecated: nothing local builds or uploads a release
artifact any more.

What this made unnecessary, and why:

- The local build path existed only because the workflow, at the time
  README "Releasing" was written, "currently cannot start a job on the
  account" (that README line was stale by this point: Release runs
  36217176585 (v0.10.2) and 36232145634 (v0.10.4) had already succeeded and
  published their releases, per the PR #33 entry above). With the workflow
  confirmed working and now the operator's explicit choice, the local path
  became a second, divergent definition of how a release gets built, worth
  removing rather than maintaining alongside the real one.
- `make release`'s manual "create the release on GitHub by hand" step
  existed only to get GitHub's generated notes, since GoReleaser's own
  changelog needed `filters`/`sort` tuning to read as well. `changelog.use:
  github-native` gets the same generated notes from GoReleaser itself, once
  it is the one doing the publishing, which is what let the manual step go.
- What survives: `make release-preflight` (the two sbx-kit checks,
  `check-previous` and `check-clobber`, now run on the tag before it is
  pushed rather than before a local build) and a new `make release-kit-bump
  TAG=vX.Y.Z`, which downloads the published release's `checksums.txt` and
  `canga-sandbox_` archives and runs `scripts/sbx-kit-pin.sh bump` from a
  temporary detached worktree at the tag - the same operation the README
  "Move the sbx kit's pin" lost-branch recipe used to spell out by hand for
  v0.10.4's bump; that recipe now collapses into re-running the target.
  `tool-release` (the local GoReleaser install) and its `GORELEASER_VERSION`
  pin are removed with it: nothing local invokes GoReleaser to build with
  any more.
- Verified against the real v0.10.4 release (2026-09-26): `make
  release-kit-bump TAG=v0.10.4` downloaded that release's `checksums.txt`
  and archives, checked them against each other and against GitHub's served
  digests, and committed a signed `chore/sbx-kit-v0.10.4` branch matching
  byte-for-byte the `sbx-kit/spec.yaml` already merged in PR #33 - then the
  branch was deleted locally, since main already pins v0.10.4 and nothing
  was meant to land from this check.

### Addendum: ship-gate round-2 fixes on this same change (2026-09-27)

- **Shell injection via `$(TAG)` (ship gate round 1, code-reviewer Critical /
  security-auditor Medium).** The first cut of `release-kit-bump` spliced
  make's own `$(TAG)` into the recipe text, which make expands to raw,
  unquoted text before the shell ever parses it - a value like
  `v1'; rm -rf /; echo '` closes the quote it lands in and runs as shell
  regardless of how the surrounding text looks quoted. Fixed by reading TAG
  only as the shell's own `$$TAG` (an ordinary parameter expansion, never
  make-substituted text) plus a strict `vX.Y.Z` guard as the recipe's first
  line, before `gh`, `git` or `mktemp` run at all. Static-tool question: does
  a tool close this class? Two do, at different points in the pipeline -
  `makefile_release_kit_bump_test.go`'s `TestReleaseKitBump_TagGuard` is a
  regression test pinning the exact payloads that used to work (quote
  breakout, backtick, `$(...)`, and, after the round-2 pass below, an
  embedded newline), so a future edit that reintroduces `$(TAG)` in recipe
  text fails CI immediately; no linter in this repo's `.golangci.yml` flags
  `$(VAR)` of a user-supplied make variable inside a recipe body specifically
  (that is a Makefile-syntax question, outside a Go linter's reach, and
  `shellcheck`, which does understand shell injection, is never handed the
  recipe as make expands it) - closing that class generically would need a
  bespoke Makefile linter, which is not worth building for one target; the
  regression test is the right-sized static gate here.
- **Round-2 finding (security-auditor Low): the TAG guard was
  line-oriented.** `printf '%s\n' "$TAG" | grep -Eq '^v...$'` reports success
  as soon as ANY line of a multi-line value matches, not every line, so
  `TAG=$'v0.10.4\n--upload-pack=touch X'` used to pass the guard on its
  first line alone. `git fetch`'s own ref-name validation happened to refuse
  that value today ("invalid refspec"), so the gap failed closed in
  practice, but the Makefile comment overclaimed that TAG could never reach
  a command as anything but a value. Fixed two ways: the guard now also
  checks `wc -l` on the unterminated value is exactly 0, rejecting any
  embedded newline outright before the line content is even considered; and
  the fetch itself now names an explicit refspec after `--`
  (`git fetch -q origin -- "refs/tags/$tag:refs/tags/$tag"`), as defense in
  depth rather than an independent fix: by the time the fetch runs, the
  guard above has already refused anything `$tag` could hold that would
  read as a fetch option, so the `--` closes the same gap a second way, not
  a different one. `TestReleaseKitBump_TagGuard` gained an
  embedded-newline case in both the argv (`TAG=` on the command line) and
  plain-environment-variable forms.
- **README check-table overclaim.** The "Releasing" check table implied
  `release-kit-bump` reruns every `release-preflight` check; it does not -
  `release-preflight` and the workflow are what run `check-previous` and
  `check-clobber` before a push, and `release-kit-bump` runs its own,
  different set (tag-guard, GitHub cross-check, script-presence). Fixed by
  annotating each table row with which target(s) actually make that check.
- **Script run from the tag's own worktree, and the GitHub tag
  cross-check.** `release-kit-bump` stands up a temporary detached worktree
  at the tag's own verified commit and runs that worktree's copy of
  `scripts/sbx-kit-pin.sh`, never the invoking checkout's, so a tag's own
  reviewed script judges its own release; and before trusting any of it, the
  tag as fetched into the checkout is cross-checked against the commit
  GitHub's API resolves the tag to, refusing on any disagreement between
  `origin` and the canonical repository.
- **Round-2 finding (security-auditor Info): the worktree re-resolved the
  tag by name.** `git worktree add --detach "$wt" "$tag"` looked the tag up
  by name again, after the cross-check above had already verified which
  commit it names - a needless second resolution of something already
  proven. Fixed to pass the verified commit sha (`$here`) instead, so the
  worktree can only ever be the commit just checked, never a fresh lookup.
- **Round-2 finding (security-auditor Info): an old tag's own script can't
  gain later hardening.** By design (the round-1 script-integrity fix
  above), `release-kit-bump` always runs the tag's OWN copy of
  `scripts/sbx-kit-pin.sh`. That is correct for judging the tag's own
  release, but it also means a check added to the script after a tag was
  cut never applies when that tag is re-bumped later - only a new release
  ships with the newer script. Documented with a one-line comment in the
  Makefile rather than changed: retroactive enforcement would mean running a
  DIFFERENT script than the one that reviewed the tag, which is the exact
  failure mode round 1 closed.
- **Round-2 finding (code-reviewer Optional): `brunovenceslau/canga`
  hardcoded four times in the recipe.** Replaced with one make variable,
  `override REPO_SLUG := brunovenceslau/canga`. The `override` directive is
  load-bearing, not decoration: it is what keeps this from becoming a second
  instance of the `$(TAG)` injection class - `override` refuses an ordinary
  `make release-kit-bump REPO_SLUG=x` argument, so `$(REPO_SLUG)` in recipe
  text stays as safe as writing the literal against that path. It is not a
  defense against a caller who invokes make with a different makefile
  (`-f other.mk`) or an `--eval` that redefines the variable outright - but
  a caller who can do either of those already has arbitrary shell
  execution, which `override` was never trying to stop; it only closes the
  ordinary, unprivileged `REPO_SLUG=x` path. A plain (non-`override`)
  variable would have reintroduced a user-overridable `$(VAR)` into recipe
  text on that same ordinary path, which is exactly what TAG's own fix moved
  away from.
- **Two residuals deferred, not fixed here:**
  1. `check_published` (in `scripts/sbx-kit-pin.sh`) verifies a release
     asset's digest matches the pinned sha256, but does not require the
     asset's uploader be `github-actions[bot]` - so a release re-published
     or edited by a human with write access would still pass. Deferred to
     the next PR, which reworks the pin script's verification end to end,
     per the operator's own sequencing (2026-09-27, verbatim pt-BR):
     "Sincronize canga com seu origin/main e resolva as pendencias uma a
     uma" (sync canga with its origin/main and resolve the pending items
     one at a time). Resolved in "sbx-kit-pin: rewrite verifies, and
     canga-sandbox_ assets must carry this repo's Actions identity" below.
  2. GNU Make's own argv sharp edge: a `TAG=$(shell ...)` value given on
     make's command line is expanded by make at parse time, unconditionally,
     regardless of which target runs - this is pre-existing GNU Make
     behavior, not something a recipe can fix from inside itself, and it
     only matters if something builds a `TAG=` argument from untrusted
     input before invoking `make`. Nothing in this repository does that
     today.

## sbx-kit-pin: rewrite verifies, and canga-sandbox_ assets must carry this repo's Actions identity (PR #35)

Resolves residual 1 from the PR #34 entry above, plus two operator decisions
on the same surface, the five round-3 ship-gate leftovers from that PR, and
this PR's own round-1 ship-gate fixes (below).

### Operator decisions

- (2026-09-26, deciding between a verified and an unverified `rewrite`,
  verbatim pt-BR): "rewrite sempre verifica (Recomendado)". `rewrite` now
  always checks the sums it is given against release `vX.Y.Z`'s published
  digests before writing the kit, the same `check_published` check
  `check-previous` and `bump` already ran - with no flag to skip it. It was
  the manual recovery path used, unverified, for the v0.10.2 pin (see the
  PR #31 entry above); it is not any more.
- (2026-09-27, sequencing this work right after PR #34, verbatim pt-BR): "Na
  P2, ja a seguir (Recomendado)". `check_published` now also requires each
  `canga-sandbox_` asset's uploader to be `github-actions[bot]`
  (`.assets[].uploader.login` from `gh api .../releases/tags/<tag>`). What
  that identity actually proves is narrower than this decision first read:
  see "Round-1 ship-gate fix: the uploader identity claim" below for the
  corrected wording, added the same day the round-1 ship gate caught the
  overclaim.

### Uploader measurement (2026-09-27)

Every `canga-sandbox_*` asset of every published (non-draft,
non-prerelease) release, `gh api repos/brunovenceslau/canga/releases
--paginate`:

| Release | canga-sandbox_ assets | Uploader |
| --- | --- | --- |
| v0.10.4 | amd64, arm64 | `github-actions[bot]` |
| v0.10.2 | amd64, arm64 | `github-actions[bot]` |
| v0.10.1 | amd64, arm64 | `github-actions[bot]` |
| v0.10.0 | amd64, arm64 | `github-actions[bot]` |
| v0.9.0 | amd64, arm64 | `github-actions[bot]` |
| v0.8.0 | amd64, arm64 | `github-actions[bot]` |
| v0.7.0 | amd64, arm64 | `github-actions[bot]` |
| v0.6.0 | amd64, arm64 | `github-actions[bot]` |
| v0.5.0 | amd64, arm64 | `github-actions[bot]` |
| v0.4.0 through v0.1.0 | none | n/a (predate the sandbox archive) |

v0.10.3 carries no assets (its release was deleted; see the PR #33 entry
above), so it never reaches this check. Every release the pin script can
still be asked to check against - including the one currently pinned on
main, v0.10.4 (PR #33), and every release reachable through
`SBX_KIT_OLDER_LINE` or an older-line `check-previous` (v0.5.0 through
v0.10.1) - was uploaded by `github-actions[bot]`. No published release with
`canga-sandbox_` assets was human-uploaded, so this change refuses nothing
that passes today; the "older line uploaded by a human" case the task asked
to decide on has no real instance to decide against. The decision, for when
one does appear: refuse it, with no bypass flag, the same as any other
`check_published` refusal - a hand-uploaded archive on an older line is
exactly what this check exists to catch, and adding an override would
reopen the gap the uploader check closes. This is a decision worth
revisiting only if a legitimate need for a human-uploaded archive surfaces;
none has.

### Round-1 ship-gate fix: the uploader identity claim (2026-09-27)

**Finding (security-auditor Medium).** `uploader.login == github-actions[bot]`
proves "a GITHUB_TOKEN of some workflow run in brunovenceslau/canga", not
"the Release workflow specifically": any workflow in this repository
granted `contents: write` could run `gh release upload --clobber` itself
and produce that exact same identity, since every workflow's default
`GITHUB_TOKEN` uploads under the one shared `github-actions[bot]` login.
The sha256 checks are unaffected by this - a re-upload still has to match
the pinned digest, or `check_published` refuses it on that alone - but the
uploader check's OWN claim was too strong.

Operator decision (2026-09-27, verbatim pt-BR): "Reescrever agora + fechar
na P3 (Recomendado)" (reword now, close the actual gap in P3). Fixed here:
every comment, error message, README paragraph and this file's own wording
that said or implied "only the Release workflow may publish" now says what
the check actually establishes - a workflow run in this repository, not
release.yml specifically (see `check_published`'s own comment in
`scripts/sbx-kit-pin.sh` for the full explanation, and README "Move the sbx
kit's pin").

**Pending item 3 (deferred to P3, per the operator decision above):** bind
`canga-sandbox_` assets to `release.yml` specifically, with build
provenance attestation - `check_published` additionally running
`gh attestation verify --signer-workflow
brunovenceslau/canga/.github/workflows/release.yml` against each asset. The
Release workflow does not currently generate an attestation
(`actions/attest-build-provenance` or GoReleaser's own signing step is not
wired in yet), so this is real, not-yet-started work, not a one-line
follow-up. Resolved in "Release provenance: attestation, draft-first,
immutable releases (PR #36)" below.

### Round-1 ship-gate fixes: the rest (2026-09-27)

Operator decision (2026-09-27, verbatim pt-BR): "Corrigir todos agora
(Recomendado)" (fix all of them now), covering every finding below except
the uploader-identity gap itself (pending item 3 above).

- **[sec Low] TOCTOU / double parse.** `rewrite` and `bump` verified one
  parse of `checksums.txt` via `sandbox_sum`, then `render` re-parsed the
  same file a second time to get the same two sums. Fixed: `render`'s
  signature changed from `render <version> <checksums> <in> <out>` to
  `render <version> <amd64 sum> <arm64 sum> <in> <out>` - both callers parse
  once and pass the verified strings through, closing both the TOCTOU
  window (the file could change between the two reads) and the duplicate
  `sandbox_sum` call the round-1 code-reviewer pass flagged as the same
  issue from the readability side.
- **[sec Low] gh host not pinned.** A stray `GH_HOST` in the calling
  environment would have redirected every `gh` call at another host,
  silently. Fixed: `scripts/sbx-kit-pin.sh` now `export`s `GH_HOST=github.com`
  near the top, unconditionally, and the `release-kit-bump` Makefile recipe
  does the same before its first `gh` call. Every existing test's fake `gh`
  now refuses to run at all unless `GH_HOST=github.com` reaches it, so this
  is regression-tested by the whole existing suite, not only a dedicated
  case.
- **[sec Low] EXIT-only trap.** A bare `trap ... EXIT` does not fire on
  INT/TERM/HUP in every `/bin/sh` (dash included), so Ctrl-C or a TERM from
  a job scheduler could leave a `spec.yaml.XXXXXX` temp file or a scratch
  directory behind. Fixed with `trap_cleanup_file`/`trap_cleanup_dir`
  helpers in the script (each installs the same cleanup on EXIT, HUP, INT
  and TERM, exiting 128+signum after cleanup rather than re-raising) and
  the equivalent three extra `trap` lines in the Makefile recipe.
- **[sec Info] Asset-name parsing.** `check_published` trusted GitHub to
  never serve an asset name containing whitespace; it now also requires the
  whole `release_jq` line to split into exactly three awk fields
  (`NF == 3`) before treating it as a match, so a name that somehow
  contained an embedded space can never be misread as if its trailing words
  were the digest and uploader fields.
- **[sec Info] No draft/prerelease check.** `rewrite` and `bump` take a
  version/tag directly from the caller and never checked whether that
  specific release was a draft or a prerelease (`check-previous` was
  already indirectly safe: `published_tags` excludes both). Fixed in
  `release_assets`: the same `gh api` call already made for the asset
  digests now also carries `.draft`/`.prerelease` (a `_meta <draft>
  <prerelease>` line ahead of the asset lines, from `release_jq`), so the
  refusal costs no second network round trip.
- **[code-reviewer Optional] assets_jq's "never" overclaim.** jq's `//`
  operator substitutes only for `null` or `false`, never for `""` - so
  `.digest // "-"` alone would still print an empty field the day GitHub
  (or a fixture) returned `""` instead of `null`. Fixed by mapping both
  `null` and `""` to `"-"` in jq itself (an `orDash` helper shared by
  `assets_jq` and the new `release_jq`), so the comment's claim is now
  actually true instead of true-for-null-only.
- **[code-reviewer Optional] No test through real jq.** Every existing test
  fed the fake `gh` canned text directly, never exercising the actual
  `assets_jq`/`release_jq` strings through a real jq interpreter - a syntax
  error in either would only have surfaced against the real GitHub API.
  Added `TestSbxKitPin_ReleaseJQ`, which extracts both `--jq` strings
  straight out of `scripts/sbx-kit-pin.sh` (so it cannot silently drift from
  what the script actually runs) and pipes a realistic release document -
  null digest, null uploader, an empty-string digest, an empty-string
  uploader, an unrelated extra asset - through the real `jq` binary. `jq`
  was not yet an explicit CI dependency; `.github/workflows/test.yml` now
  installs it explicitly rather than relying on the runner image carrying
  it.
- **[code-reviewer Nit] Makefile comment/code order mismatch.** The
  invariants list above `release-kit-bump` described the cross-check
  against GitHub as the LAST thing that happens, when it actually runs
  before the worktree is created. Reordered to match the recipe's actual
  execution order.
- **[code-reviewer Nit] Ambiguous "serves no digest".** The same message
  fired both for "this asset is not in the release at all" and "the asset
  is there, but GitHub reports no digest for it". Split into two: "carries
  no `<asset>` at all" and "reports no digest for `<asset>`" - and a third
  case, "lists N assets named `<asset>`", for a release that names the same
  asset more than once (see the duplicate-asset test-engineer finding
  below).
- **[test-engineer] New cases**, all in `sbxkitpin_test.go`: a duplicate
  asset name (refused as ambiguous, rather than silently taking whichever
  line an unordered match happens to pick first); a combined failure (wrong
  digest AND wrong uploader together) asserting the digest error wins,
  deterministically, because it is checked first; an asset name with an
  embedded space (refused as "carries no ... at all", never misread); a
  draft and a prerelease release (each refused by `release_assets`); and a
  checksums.txt built for one version fed to `bump` under a different
  version's tag (refused by `sandbox_sum`'s own filename-equality check,
  the same "cross-version confusion" property `rewrite`'s "another
  version's lines" case already covered, now covered on the `bump` path
  too).

### PR #34 round-3 leftovers, addressed here

1. The `override REPO_SLUG` entry above ("its value can never come from a
   caller") is reworded: `override` refuses an ordinary
   `make release-kit-bump REPO_SLUG=x` argument, not a caller invoking make
   with a different makefile (`-f other.mk`) or an `--eval` that redefines
   the variable outright - such a caller already has arbitrary shell
   execution, which `override` was never a defense against.
2. The same entry's credit to the fetch's explicit refspec after `--` is
   reworded to say what it actually is: defense in depth, redundant with
   the anchored `^v...$` guard that already runs first, not an independent
   fix.
3. `release-kit-bump`'s TAG guard now also refuses a value longer than 64
   characters, before `gh`, `git` or `mktemp` run. `TestReleaseKitBump_TagGuard`
   gained a case for it.
4. `gh release download ... -R $(REPO_SLUG) ...` is now `-R "$(REPO_SLUG)"`,
   the one unquoted use of a make variable left in the recipe.
5. The Makefile comment above `release-kit-bump` (about 59 lines) is
   trimmed to the invariants a maintainer changing that recipe needs to
   keep, pointing to README "Move the sbx kit's pin" and this file for the
   fuller history.

### Left open

- **Pending item 3** (P3, per the operator decision quoted above): bind
  `canga-sandbox_` assets to `release.yml` specifically via build provenance
  attestation. Not started; the Release workflow does not yet produce an
  attestation for `check_published` to verify. Resolved in "Release
  provenance: attestation, draft-first, immutable releases (PR #36)" below,
  for every release from v0.10.5 on.
- HANDOFF residual 2 from the PR #34 entry (GNU Make's own `TAG=$(shell
  ...)` argv-expansion sharp edge) is unrelated to this PR's scope and
  remains open, unchanged.
- `rewrite`'s new verification step means it now needs `gh` and network
  access to run at all; it previously worked fully offline against a local
  checksums.txt. This is the intended trade (a manual recovery path that
  trusts nothing beats one that works offline by trusting a claim), not a
  regression flagged for further discussion.
- The INT/TERM trap fix is demonstrated end to end by
  `TestSbxKitPin_CheckPrevious_SignalCleanup`: a real `check-previous`,
  genuinely blocked on a stubbed slow `gh release list`, killed with SIGINT
  or SIGTERM once its scratch directory already exists on disk. Reverted in
  a scratch copy (not this worktree), the same test fails exactly as the
  finding predicted: the process dies with Go's `ExitCode() == -1` (killed
  by the signal's own default disposition, proving the bare `trap ... EXIT`
  never ran) and the scratch directory survives. With the fix, the process
  exits 130 (SIGINT) or 143 (SIGTERM) and the directory is gone - dash
  defers the signal until the blocking `gh` call returns (observed: a fixed
  ~5s delay matching `FAKE_GH_SLEEP`, not an immediate interrupt), so the
  demonstration is real but not instantaneous.
  `trap_cleanup_dir` (used by `check-previous`, `check-clobber` and `bump`)
  is exactly what this test exercises; `trap_cleanup_file` (`rewrite`'s own
  temp file) is the identically-shaped sibling function, not literally the
  same code path, and was not independently exercised by a real-signal
  test: `rewrite`'s vulnerable window (between `mktemp` and `mv`) is a few
  milliseconds of local `awk`, too narrow to hit deterministically without
  adding a test-only delay hook to production code, which was deliberately
  not added. Not a gap in the fix itself, a gap in how far a real-signal
  test can reach without instrumenting the script.

## Release provenance: attestation, draft-first, immutable releases (PR #36)

Resolves pending item 3 from the PR #35 entry above: `check_published` now
binds `canga-sandbox_` archives to `release.yml` itself, not only to "some
workflow in this repository", for every release from v0.10.5 on - bound to
whatever commit the tag named when the release was built, and matched by
tag name alone, so the `v*` tag ruleset (see "Left open" below) is what
keeps that binding meaningful.

### Operator decisions

- (2026-09-26, choosing how releases get provenance, verbatim pt-BR):
  "Attestation + immutable (Recomendado)". The Release workflow creates the
  release as a draft, uploads the assets, generates a build provenance
  attestation (`actions/attest-build-provenance`, pinned by full commit
  SHA), then publishes; immutable releases get enabled on the repository.
- (2026-09-27, verbatim pt-BR): "Reescrever agora + fechar na P3
  (Recomendado)". The uploader-identity wording was corrected in PR #35;
  this PR closes the gap itself: `check_published` verifies the
  attestation with `gh attestation verify`, bound to release.yml. The
  decision named `--signer-workflow`; the ship gate's round 1 found that
  flag is a prefix match (below), so the binding uses `--cert-identity`
  instead - the exact form of the same identity. Put to the operator with
  this PR, since it departs from the flag the decision's wording named
  (2026-09-27, verbatim pt-BR): "Concordo, usar --cert-identity
  (Recomendado)".
- (2026-09-27, deciding how releases published before any attestation
  existed are treated, verbatim pt-BR): "Corte fixo na v0.10.5
  (Recomendado)". A fixed constant in the script (`attested_from="0.10.5"`):
  releases below it pass with the digest and uploader checks as before;
  v0.10.5 onward REQUIRE a valid attestation. No bypass flag; the constant
  changes only by a reviewed PR.
- (2026-09-27, deciding who enables immutable releases and when, verbatim
  pt-BR): "Eu ligo após o merge da P3 (Recomendado)". The orchestrator
  enables immutable releases through the API AFTER this PR merges and
  BEFORE v0.10.5 is tagged. This PR changes no repository setting.
- (2026-09-27, deciding whether to apply the `v*` tag ruleset proposed
  under "Left open" below, verbatim pt-BR): "Sim, eu aplico após a P3
  (Recomendado)". Decided, not yet applied: the orchestrator applies the
  ruleset through the API together with enabling immutable releases,
  AFTER this PR merges and BEFORE v0.10.5 is tagged. This PR changes no
  repository setting; the payload is kept under "Left open" for that step
  to use.

### Design

- **Draft first, public last** (`.github/workflows/release.yml`,
  `.goreleaser.yml`). GoReleaser creates the release as a draft
  (`release.draft: true`) and uploads every asset to it; the workflow then
  attests, verifies the attestation, and only then publishes the draft by
  id. Why the order matters: an immutable release refuses any asset change
  once published, so publish-then-upload would fail on its own upload
  under immutability; a draft stays editable until the workflow publishes
  it. Measured against GoReleaser's source (v2.18.2, which `version: "~>
  v2"` resolved to on 2026-09-27 and which `release.yml` now pins exactly,
  `internal/client/github.go`): GoReleaser already
  creates every release as a draft while uploading and un-drafts it in
  `PublishRelease`, which returns early, leaving the draft, when
  `release.draft` is set. It finds an existing release with
  `GetReleaseByTag`, which never returns a draft, so a rerun after a failed
  run would add a second draft for the same tag;
  `release.replace_existing_draft: true` deletes the previous draft (matched
  by name, which `release.name_template: "{{ .Tag }}"` pins to the tag,
  the same as GoReleaser's default in `internal/pipe/release/release.go`,
  written out so the name and the publish step's tag match cannot drift)
  first. `changelog.use: github-native`
  is unaffected: the notes are generated from the tag range, not from the
  release's draft state. GoReleaser also refuses outright to update an
  immutable release ("already exists and is immutable"), so under
  immutability a rerun of a published tag fails before uploading anything.
- **Publish by id, not by tag.** The publish step lists the repository's
  releases, requires exactly one draft whose `tag_name` is the pushed tag,
  compares the draft's assets with `dist/` (the exact set of names, each
  with the `sha256:` digest GitHub serves for it), and only then PATCHes
  that id to `draft=false`, checking the answer is a published release for
  the same tag. `gh release edit <tag>` would fall back to the first draft
  matching the tag without saying so. The tag reaches jq only through
  `--arg`, never spliced into the filter. `immutable` is not read from that
  PATCH response (round-2 ship-gate finding R2-L2, security-auditor Low: a
  single PATCH response is not trusted for a setting that can lag behind
  the publish call): the step re-GETs the release by id, up to 6 times
  with a short sleep, until it reports `draft == false`, the same tag, and
  `immutable == true`, and only then repeats the asset name+digest
  comparison against `dist/` from that final GET - race-free, since
  nothing can change an immutable release's assets. It fails red only once
  the retry budget is exhausted.
- **Attestation subjects.** `subject-path` over `dist/canga-host_*.tar.gz`,
  `dist/canga-sandbox_*.tar.gz` and `dist/checksums.txt`, rather than
  `subject-checksums: dist/checksums.txt`: the latter attests only the
  files `checksums.txt` lists, not `checksums.txt` itself, and hashes a
  file's claims rather than the bytes on disk.
- **Pinned action.** `actions/attest-build-provenance` v4.2.2, commit
  `4d101475d8b20a2381f78447822ac1eab6504dd8`, resolved read-only on
  2026-09-27 with `gh api repos/actions/attest-build-provenance/releases/latest`
  and `gh api repos/actions/attest-build-provenance/git/ref/tags/v4.2.2`
  (a lightweight tag, so the ref's sha is the commit). v4 is a thin wrapper
  over `actions/attest`, which it pins by SHA itself (v4.2.1, `508db95`).
- **Permissions.** The workflow level is `permissions: {}`; the one job
  gets `contents: write` (as before), `id-token: write` (the OIDC token the
  Sigstore signing certificate is issued against) and `attestations: write`
  (store the attestation), and nothing else. `artifact-metadata: write` is
  not needed: it is for storage records, which only apply with
  `push-to-registry`.
- **Verify before publishing.** The workflow runs `scripts/sbx-kit-pin.sh
  check-attestation <tag> <files>` on every attested file while the release
  is still a draft: the same `attestation_verify` function, flags and gh
  floor the kit pin checks use, from one definition. Under immutability a
  published release with an attestation that does not match the policy
  could never be pinned or fixed in place; this catches it while a rerun
  still costs nothing. It is a same-job, same-disk check: it proves the
  attestation and the policy agree about the bytes this run built, which
  catches configuration and policy mistakes, not tampering. The
  independent check is `check_attested`, later, which downloads the
  published archives from GitHub and verifies those.
- **The script's check** (`check_attested` in `scripts/sbx-kit-pin.sh`,
  run by `check_published` after its digest and uploader checks, for a
  version at or above `attested_from`). It downloads both
  `canga-sandbox_` archives into the caller's trap-cleaned scratch
  directory (`gh attestation verify` needs the bytes: an attestation is
  looked up by the artifact's sha256), refuses unless each download is the
  pinned sha256, then runs `gh attestation verify <file> --repo
  brunovenceslau/canga --cert-identity
  https://github.com/brunovenceslau/canga/.github/workflows/release.yml@refs/tags/<tag>
  --source-ref refs/tags/<tag> --deny-self-hosted-runners`. One path for
  all three callers (`rewrite`, `check-previous`, `bump`), even though
  `bump` already holds the archives: simpler than two ways to get the
  bytes, and the download is checked against the same pin. `rewrite` gained
  a scratch directory of its own for it, removed before the kit's temp
  file exists, so each has exactly one trap guarding it at a time.
- **`--cert-identity`, not `--signer-workflow`.** Measured in gh's source
  (v2.101.0, `pkg/cmd/attestation/verify/policy.go`,
  `validateSignerWorkflow`): a `--signer-workflow` value becomes `^` + the
  quoted URL, a regular expression anchored only at the start, so it is a
  prefix match against the signing certificate's workflow identity
  (`https://github.com/<repo>/<workflow path>@<ref>`); even with the
  `@refs/tags/<tag>` appended, an identity that merely starts with it
  passes. `--cert-identity` is an exact string comparison (sigstore-go
  v1.3.0, `SubjectAlternativeNameMatcher.Verify`, `actualCert.SubjectAlternativeName
  != s.SubjectAlternativeName`); gh makes it mutually exclusive with
  `--signer-workflow`. Live (2026-09-27, gh 2.101.0, gh's own attested
  v2.101.0 linux arm64 archive): `--cert-identity
  https://github.com/cli/cli/.github/workflows/deployment.yml@refs/heads/trunk`
  with `--source-ref refs/heads/trunk --deny-self-hosted-runners` exits 0;
  the same identity truncated to `...@refs/heads/trun`, truncated to
  `.../deployment.y`, with a wrong ref, or with a wrong workflow file each
  exit 1; and, for contrast, `--signer-workflow
  cli/cli/.github/workflows/deployment.y` exits 0 - the prefix match,
  demonstrated. `--source-ref` stays: the identity names the workflow's
  ref, and `--source-ref` pins the source repository's ref independently.
- **gh version floor: 2.93.0**, checked by the script before any
  attestation call (`gh_can_attest`), and only when one is verified, so an
  older gh still checks the releases below the cutover. From gh's release
  notes (read-only, `gh api repos/cli/cli/releases`): 2.49.0 introduced
  `gh attestation`, 2.67.0 fixed a false exit 0 when no attestation of the
  requested predicate type existed, 2.68.0 added `--source-ref` (checked in
  the v2.68.0 source: `--cert-identity`, `--source-ref` and
  `--deny-self-hosted-runners` are all there), and 2.93.0 stopped sending
  the GitHub token to TUF repository mirrors (GHSA-8xvp-7hj6-mcj9). The
  first cut of this PR used 2.97.0, for 2.97.0's fix of `--signer-workflow`
  regex escaping; with `--cert-identity` that fix no longer applies, and
  2.93.0 is the newest release whose fix does. The Release workflow's
  pre-publish check goes through the same script, so the runner image's gh
  is held to the same floor.
- **Cutover comparison.** `version_at_least` compares X.Y.Z component by
  component as numbers in POSIX awk, never as text (0.10.10 is above
  0.10.5). It fails closed: awk prints `ge` or `lt`, and anything else
  (awk missing, crashing, or silent) is a refusal. Reading awk's exit
  status alone would have taken a crash for "below the cutover", which
  skips the attestation check.
- **Immutable releases, enforced where a token can see them.** `GET
  repos/<repo>/immutable-releases` answers only a token with admin access
  (measured 2026-09-27: the operator's token reads
  `{"enabled":false,"enforced_by_owner":false}` for this repository, and
  gets 404 for `octocat/Hello-World`, where it is not an admin). A
  workflow's `GITHUB_TOKEN` cannot be granted repository administration,
  so the workflow cannot check it before publishing. The enforcement point
  is therefore `make release-preflight`, which now runs
  `scripts/sbx-kit-pin.sh check-immutable` first, on the operator's token,
  and refuses unless it reads `true` (a 404 or any other answer is a
  refusal). Backstop in the workflow: after publishing, the publish step
  goes red if the release GitHub returns is not `immutable: true` (the
  field is on every release object; v0.10.4 reads `false`). That is
  detection, not prevention: the release is already public by then.
- **One run per tag.** `concurrency: release-${{ github.ref }}`,
  `cancel-in-progress: false`: a second run of the same tag queues instead
  of deleting the first run's draft through `replace_existing_draft`, and
  no run is cancelled halfway through an upload.
- **check-clobber stays.** A draft left by a failed run answers 404 by
  tag, so check-clobber treats it as "no release yet", which is right: no
  kit can pin a draft. Under immutability GitHub itself refuses to replace
  a published release's assets, `SBX_KIT_ALLOW_CLOBBER` included; the check
  still refuses first, with the reason and the way out (a new patch
  release). Only comments changed.
- **Makefile unchanged.** `release-kit-bump` runs the tag's own
  `scripts/sbx-kit-pin.sh bump`, which does its own download for the
  attestation check. A tag cut before this PR carries a script without the
  check, which is fine: every such tag is below the cutover.

### Tests

(Written for the first cut; see "Ship-gate round 1 fixes" below for the
flag, floor and test changes since.) `sbxkitpin_test.go`'s fake gh now
answers `gh --version`, `gh release
download` (from `$FAKE_GH/<tag>.files/`), and `gh attestation verify`,
which logs its whole argument list and passes only for a file whose sha256
is listed in `$FAKE_GH/attested`. `TestSbxKitPin_Attestation` covers:
releases below the cutover pass without any attestation call (0.9.99,
0.10.4, and 0.10.4 with a gh 2.46.0 that could not verify one); at or above
it (0.10.5, 0.10.10, 0.11.0, 1.0.0) a verifying attestation passes with the
exact expected `gh attestation verify` argument lists, and a missing one is
refused by gh's own failure on the first archive; one archive attested and
not the other; gh below the floor; an unreadable gh version; archives the
release cannot serve; downloaded bytes that differ from the pin (attested,
so only the sha256 check can catch it); and `rewrite` at the cutover,
attested and not. `TestSbxKitPin_Bump` now runs above the cutover with
attested archives, asserts the exact calls, and gained a "no attestation"
refusal; `TestReleaseKitBump_CommitsFromValidTag` asserts the tag's own
script made the exact calls through the Makefile target. Each was
reverted in a scratch copy, one mutation at a time (skip the attestation
step, drop `--signer-workflow`, drop the `@ref` and `--source-ref`, drop
`--deny-self-hosted-runners`, compare versions as text, skip the download's
sha256 check, skip the gh floor), and the matching tests failed for each.

Read-only checks against GitHub (2026-09-27): `scripts/sbx-kit-pin.sh
check-previous v0.10.5` passes at this PR's HEAD (the kit pins v0.10.4,
below the cutover, "checked by digest and uploader only"). With gh 2.101.0
(built from source into a scratch directory; the sandbox's own gh is
2.46.0, too old for any attestation command), `gh attestation verify` on
the real v0.10.4 amd64 archive answers `HTTP 404` from
`repos/brunovenceslau/canga/attestations/sha256:a91c3bd8...`, as expected
for an unattested release, and a scratch copy of the script with the
cutover moved to 0.10.4 refuses it with the new message. As a positive
control of the exact flag shape, gh's own attested v2.101.0 linux arm64
archive verifies with `--signer-workflow
cli/cli/.github/workflows/deployment.yml@refs/heads/trunk --source-ref
refs/heads/trunk --deny-self-hosted-runners`, and fails with a wrong ref
or a wrong workflow file.

### Flaky test fixed on the way: TestSbxKitPin_CheckPrevious_SignalCleanup

`make ci` failed once on this branch with the SIGINT case exiting -1 (killed
by the signal's default disposition) after 0.09s, its scratch directory left
behind. Not a trap regression: the test signalled as soon as the scratch
directory appeared, but `mktemp -d` returns a few commands before
`trap_cleanup_dir` installs the traps, so under load the signal could land
in between. Reproduced deterministically in a scratch copy by widening that
window (`sleep 1` between the two lines): the old test failed exactly as in
CI. Fixed in the test, not the script: the fake gh now creates
`release-list.started` before its deliberate sleep, and the test signals
only once that marker exists (the script is then inside gh, with its traps
in place). With the widened window the new test passes; with the HUP/INT/TERM
traps removed from `trap_cleanup_dir` it still fails, so it still proves the
fix it was written for. Static-tool question: no linter sees a test that
waits on the wrong readiness signal; the lesson is to make a fake announce
the exact state a test needs, rather than inferring it from a side effect
that happens earlier.

### Ship-gate round 1 fixes (2026-09-27)

Round 1 of the ship gate was GO with no must-fix finding; every
non-blocking finding below is applied, per the house rule that they are
included by default.

- [security-auditor Low] `--signer-workflow` is a prefix match: replaced by
  `--cert-identity` in the script (`attestation_verify`) and, through the
  new `check-attestation` subcommand, in the workflow. See the design
  bullet above for the measurements.
- [security-auditor Low] Added the per-tag `concurrency` group; the
  publish step now re-checks the draft's asset set and digests against
  `dist/` before publishing, and checks the PATCH answer (`draft == false`,
  same `tag_name`).
- [security-auditor Low] "Enable immutable releases before v0.10.5" is now
  enforced by `make release-preflight` (`check-immutable`), with a
  post-publish backstop in the workflow; see the design bullet above for
  why the workflow cannot check it first.
- [security-auditor Low] GoReleaser is pinned to v2.18.2 in `release.yml`
  instead of `~> v2`.
- [security-auditor Low] A tag ruleset for `v*`: not implemented (a
  repository setting, the operator's call); proposed under "Left open".
- [security-auditor Info] `version_at_least` fails closed; the workflow's
  pre-publish check now has the gh floor (it goes through the script); the
  publish step's jq takes the tag through `--arg`.
- [code-reviewer Optional] `release.name_template: "{{ .Tag }}"` pinned
  explicitly; the same-job verification and the draft-mutable window are
  written down (the design bullet above and "Left open" below).
- [code-reviewer Nit, test-engineer] New tests: the gh floor boundary
  (2.93.0 passes, 2.92.99 refuses); `version_at_least` run directly, taken
  from the script's own text (numeric edges, and an awk that crashes, says
  nothing, or says something else: each a refusal); `check_attested`'s
  `mkdir` failing; a partial download that gh reports as a failure, and
  one it reports as a success (the script's own "the download carries no
  ..." check); `check-attestation` (exact argument lists, a file not
  attested, a gh below the floor, a missing file, usage); `check-immutable`
  (on, off, empty, a 404, usage). Each fails with its fix reverted in a
  scratch copy.

### Left open

- **Tag ruleset for `v*` (decided, applied post-merge).** Today anyone with
  push access can create, move or delete a `v*` tag, and a tag push is what
  starts a release - and, per round-2 ship-gate finding R2-L1
  (security-auditor Low, "Release provenance" section above), what a build
  provenance attestation cannot restrict on its own: it binds bytes to
  `release.yml` at whatever commit the tag named when the release was
  built, not to reviewed content, and the check matches that binding by
  tag name alone, so whoever can push a tag could tag a commit with a
  modified `release.yml`, or move an existing tag to one.
  Operator decision (2026-09-27, verbatim pt-BR): "Sim, eu aplico após a P3
  (Recomendado)" - no longer just a proposal. The orchestrator applies this
  ruleset through the API after this PR merges, together with enabling
  immutable releases and before v0.10.5 is tagged; this PR itself changes
  no repository setting. Repository ruleset (`POST
  repos/brunovenceslau/canga/rulesets`):
  `{"name": "release tags", "target": "tag", "enforcement": "active",
  "conditions": {"ref_name": {"include": ["refs/tags/v*"], "exclude": []}},
  "rules": [{"type": "creation"}, {"type": "update"}, {"type": "deletion"}],
  "bypass_actors": [{"actor_id": 5, "actor_type": "RepositoryRole",
  "bypass_mode": "always"}]}` - only the admin role (actor_id 5) may
  create, move or delete release tags. Immutable releases already protect
  a published release's tag; this covers tags before their release exists.
  Verify the payload against GitHub's rulesets documentation before
  applying it.
- **The draft is mutable until it is published.** Between GoReleaser's
  upload and the publish step, anyone with `contents: write` (a person, or
  another workflow) could change the draft's assets. The publish step's
  pre-publish digest comparison against `dist/` narrows that window to the
  seconds between that comparison and the PATCH; `check_attested` later
  refuses any archive whose bytes were not attested by this run. The
  post-publish comparison (round-2 ship-gate finding R2-L2, below) is
  race-free once it runs, since it reads from the same re-GET that
  confirmed `immutable == true`, but the residual window before that - the
  seconds between the pre-publish comparison and the PATCH - is unchanged.
- **Immutability is not retroactive.** Enabling it leaves v0.10.4 and
  earlier mutable. A rerun of the Release workflow on an already-published,
  pre-immutability tag still replaces its assets and then attests the new
  bytes (GoReleaser updates a published, mutable release in place);
  `check-clobber` refuses that while a kit pins the tag, as before. This
  residual predates this PR.
- **Unverified until the v0.10.5 run:** whether GitHub reports a `digest`
  for a draft's assets (the publish step refuses a draft whose digests do
  not match `dist/`, so a draft without digests would block publishing
  until fixed), and how many re-GETs, if any, it takes after the PATCH
  before `immutable` reports `true` (the publish step retries up to 6
  times with a short sleep before treating that as a real refusal; if a
  genuinely-immutable release never settles within that budget, the retry
  count needs raising, not the check removed). Neither could be measured
  without a GitHub write.
- **The first real end-to-end run is the v0.10.5 release.** Nothing here
  has run in Actions yet: the draft, the attestation, the pre-publish
  verification and the publish-by-id step are validated by actionlint,
  `goreleaser check` and the reading of GoReleaser's and gh's sources
  above, not by a real run. Watch that run's legs individually, then run
  `make release-kit-bump TAG=v0.10.5` (it exercises the attested path for
  real), and check the release page shows the attestation.
- **Immutable releases are not on yet.** Per the operator's decision above,
  the orchestrator enables them through the API after this PR merges and
  before v0.10.5 is tagged. If v0.10.5 is tagged first, the run does not
  stop at the draft: GoReleaser builds and uploads as usual, and the
  publish step publishes the release. It is the re-GET loop right after
  that publishes go red on - it retries up to 6 times waiting for
  `immutable == true`, and with the setting off GitHub never reports that,
  so the run exhausts its budget and fails, with the release already
  public and mutable. Recovery is the same as any other run that fails
  there (README's "Releasing" refusal table): turn immutable releases on
  and cut a new patch release.
- **gh on the runner and on the operator's mac.** The pre-publish step uses
  the runner image's preinstalled gh, and `check-previous`/
  `release-kit-bump` on a release at or above v0.10.5 need gh 2.93.0 or
  newer locally. The first release that needs it locally is the one after
  v0.10.5 (its preflight checks v0.10.5), plus `make release-kit-bump
  TAG=v0.10.5` itself.

### Ship-gate round 2 fixes (2026-09-27)

Round 2 of the ship gate found no Critical or Required/High/Medium
finding; both Low findings below are applied, per the house rule that
they are included by default.

- [code-reviewer Required] The switch from the operator's decision
  (`--signer-workflow`) to `--cert-identity` (see "Operator decisions"
  above) needed the operator's own agreement, not just a note that it was
  flagged - added there, verbatim pt-BR: "Concordo, usar --cert-identity
  (Recomendado)".
- [security-auditor R2-L2, Low] The publish step trusted a single PATCH
  response for `immutable`. `.github/workflows/release.yml`'s "Publish the
  release" step now re-GETs the release by id, up to 6 times with a 5s
  sleep, until `draft == false`, the tag matches, and `immutable == true`,
  and takes the post-publish asset name+digest comparison against `dist/`
  from that final GET, which is race-free once it runs (see "The draft is
  mutable until it is published" and "Unverified until the v0.10.5 run"
  above). It fails red only after the retry budget is exhausted. `GH_HOST`
  stays pinned, `jq` still takes the tag through `--arg`, and
  `actionlint`/`goreleaser check` stay clean.
- [security-auditor R2-L1, Low] README and this file overclaimed that
  attestation binds "the Release workflow specifically", read by a
  reader as reviewed content. It binds `release.yml` **at whatever commit
  the tag named when the release was built**, not reviewed or merged
  content, and the check matches that binding by tag name alone - not by
  whichever commit the tag names now: whoever can push a `v*` tag could
  tag a commit carrying a modified `release.yml`, or move an existing tag
  to one. Softened in README ("Verify a release", "Move the sbx kit's
  pin") and in `scripts/sbx-kit-pin.sh` (the `check_published` header
  comment and `attestation_verify`'s own comment, which now states this
  caveat directly). The actual control on who can push a `v*` tag at all,
  or move one after the fact, is the tag ruleset, decided by the operator
  and moved from a proposal to "decided, applied post-merge" under "Left
  open" above, with the operator's verbatim pt-BR agreement recorded there
  and in "Operator decisions": "Sim, eu aplico após a P3 (Recomendado)".
  README's "Releasing" section now lists both repository settings -
  immutable releases and the `v*` tag ruleset - as prerequisites before
  any tag is pushed.

### Ship-gate round 3 fixes (2026-09-27)

Round 3 of the ship gate found no Critical or Required/High/Medium
finding. Operator decision on the round's capped items (2026-09-27,
verbatim): "Seguir a recomendação (Recomendado)" - the three fix-now items
below are applied, the two pending items are recorded here rather than
fixed now, and the one accepted item is recorded as accepted, all per that
same decision.

- [R3-L1, fix now] `.github/workflows/release.yml`'s post-publish failure
  message conflated "immutable releases is off" with "GitHub has not
  reported immutable yet" - two different causes with two different
  recoveries, read by an operator mid-incident as one. Reworded (echo text
  only) to say the release is already public, that the workflow must not
  be re-run for this tag, name both causes, and give the recovery: check
  the setting with `make release-preflight` (`check-immutable`); if it was
  off, turn it on and cut a new patch release; if it is on, the release
  may already be immutable - confirm with `gh api
  repos/brunovenceslau/canga/releases/tags/<tag> --jq .immutable`. README's
  "Releasing" refusal table gained the matching row.
- [R3-L3, fix now] This file said tagging v0.10.5 before immutable
  releases are enabled "still works" - true only up through the publish
  call. With the post-publish re-GET loop in place, an unpublished-immutable
  run does not stop at "still mutable": it exhausts its 6-try retry budget
  waiting for `immutable == true`, which GitHub never reports with the
  setting off, and the run goes red, with the release already published
  and mutable. Corrected under "Immutable releases are not on yet" above.
- [R3-L2, fix now] "`release.yml` as it read at the tagged commit"
  overclaimed a live binding. `--cert-identity` and `--source-ref` match by
  tag NAME, not by commit, so until the `v*` tag ruleset is enforced, a tag
  can be moved to point at another commit and an attestation for that
  commit's `release.yml` still verifies under the same tag name. Reworded
  everywhere the phrase appeared - README ("Verify a release"),
  `scripts/sbx-kit-pin.sh` (the `check_published` header comment and
  `attestation_verify`'s own comment), and this file (the "Tag ruleset for
  `v*`" entry under "Left open", the R2-L1 entry above, and this section's
  own opening summary line) - to say the binding is to whatever commit the
  tag named when the release was built, and that the `v*` tag ruleset is
  what keeps a tag from moving and so keeps that binding meaningful.
- [pending] Pinning `--source-digest` to the resolved commit sha: deferred
  hardening, narrowed once the `v*` tag ruleset is enforced (a tag that
  cannot move makes the tag-name match above equivalent to a commit match).
  Not implemented in this PR.
- [pending] `timeout-minutes` on the Release job: pre-existing gap, not
  introduced by this PR; the job runs under GitHub's default 6-hour
  ceiling with no explicit shorter one. Not implemented in this PR.
- [accepted] The post-publish re-GET loop aborts on the first transient
  `gh api` error rather than retrying within its own budget - fails closed
  and loud, which is the right default for a check guarding a published,
  soon-to-be-immutable release. Revisit only if real transient-error noise
  shows up on the v0.10.5 run.
