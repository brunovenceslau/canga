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
follow-up.

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
  attestation for `check_published` to verify.
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
