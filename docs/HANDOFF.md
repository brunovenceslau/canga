<!--
SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
SPDX-License-Identifier: GPL-3.0-or-later
-->

# Handoff notes

This is the maintainer's working log, kept in the repository so it outlives
any one working session. It is not user documentation: to use canga, start
at the [README](../README.md); for how it works and why, read
[How canga works](design.md). Entries are dated and describe the code as it
was when they were written, so where an old entry and the README or
`docs/design.md` disagree, those two describe the current behavior. Open
items sit under a section's "Left open", "Pending" or "Open" heading, and
are marked done or closed in place when a later change resolves them.

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
  2. [accepted] GNU Make's own argv sharp edge: a `TAG=$(shell ...)` value
     given on make's command line is expanded by make at parse time,
     unconditionally, regardless of which target runs - pre-existing GNU
     Make behavior, not something a recipe can fix from inside itself,
     and it only matters if something builds a `TAG=` argument from
     untrusted input before invoking `make`. Accepted: the orchestrator
     measured (2026-09-27) that no file under `.github/`, `scripts/`,
     `install_*.sh` or the Makefile does that - `grep -rn 'TAG=' .github
     scripts Makefile install_*.sh` hits only the Makefile's own help
     text (line 71) and usage message (line 265). Reopen condition: if
     anything starts building a `TAG=` argument from untrusted input.

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
- [accepted] HANDOFF residual 2 from the PR #34 entry (GNU Make's own
  `TAG=$(shell ...)` argv-expansion sharp edge) is unrelated to this PR's
  scope. Accepted there (see the PR #34 entry above for the why and the
  reopen condition); no longer open.
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
  (security-auditor Low, under "Ship-gate round 2 fixes (2026-09-27)" below,
  in this same section), what a build
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
- [accepted] **The draft is mutable until it is published.** Between
  GoReleaser's upload and the publish step, anyone with `contents: write`
  (a person, or another workflow) could change the draft's assets. The
  publish step's pre-publish digest comparison against `dist/` narrows
  that window to the seconds between that comparison and the PATCH. That
  comparison covers every asset the run built - `canga-host_*.tar.gz`,
  `canga-sandbox_*.tar.gz` and `checksums.txt` alike (`.github/workflows/
  release.yml`'s "Publish the release" step, `$tmp/want` vs `$tmp/got`).
  `check_attested` (`scripts/sbx-kit-pin.sh`) is narrower than the
  original wording claimed: it downloads and verifies only the two
  `canga-sandbox_` archives for the tag named, comparing the download
  against the pinned sha256 first and only then calling
  `attestation_verify` - never `checksums.txt` or the `canga-host_`
  archives - and it binds to whatever the tag currently serves, not to
  this specific run's bytes - a later re-run at the same tag is exactly
  what item "Immutability is not retroactive" below covers. Accepted: the
  mutable-draft window is narrowed to seconds by the pre-publish digest
  comparison, and the post-publish comparison (round-2 ship-gate finding
  R2-L2, below), which does cover all three asset kinds, is race-free
  once it runs and will catch a swap in that narrowed window - it cannot
  undo a swap already published, only fail the run loudly once it
  detects one. Reopen condition: if GitHub offers an immutable-draft or
  atomic publish-with-assets primitive, if the pre-publish comparison is
  ever removed, or if a direct API edit of a draft's assets by someone
  with `contents: write` - the same access this bullet's opening
  sentence already names as the threat - is ever observed to slip past
  both comparisons undetected.
- [closed: Path B for `install_host.sh`, `install_sandbox.sh` and `canga
  upgrade` by PR #41's release floor, Path A by deleting the old Release
  runs] **Immutability is not retroactive.** Enabling it left
  v0.10.4 and earlier mutable, and only v0.10.5 on carries attestation
  (`grep -c attest-build-provenance` over each tag's own
  `.github/workflows/release.yml` reads 0 for v0.1.0 through v0.10.4 and
  1 for v0.10.5 - the first attested run). Separately, `git show
  <tag>:.github/workflows/release.yml | grep -c check-previous` (and the
  same for `check-clobber`) reads 0 for both steps, for v0.1.0 through
  v0.10.2, and 1 for both, for v0.10.3, v0.10.4 and v0.10.5 - neither step
  (added together in "Release path: the workflow is the one official
  path" above) existed before v0.10.3. A workflow re-run uses the tag's
  own `release.yml` and the original run's `GITHUB_SHA`/`GITHUB_REF`
  (GitHub docs, "Re-running workflows and jobs"), so a re-run of any tag
  whose `release.yml` predates these steps has no automatic refusal
  against recreating that tag's release from scratch.

  Narrowed by deletion, not closed. Operator decision
  (2026-09-27, verbatim pt-BR): "Remova todas as versões que não são
  attested. Mais prático. Ninguém usa ainda, só eu." Every GitHub release
  that is not attested has been deleted with `gh release delete <tag>
  --yes` (no `--cleanup-tag`, so the tag itself is untouched): v0.1.0,
  v0.2.0, v0.3.0, v0.4.0, v0.5.0, v0.6.0, v0.7.0, v0.8.0, v0.9.0, v0.10.0,
  v0.10.1, v0.10.2 and v0.10.4. v0.10.3 already carried no release before
  this (its asset-less release was deleted separately; see the PR #33
  entry above), so v0.10.5 - attested, immutable - is now the only
  release this repository has. The tags all stay: the Go module proxy
  and checksum database already cache several of these versions (see the
  PR #33 entry's v0.10.3 argument), so none of them may ever move. This
  narrows, but does not close, round-3 ship-gate finding F1 (an explicit
  install of v0.10.4 or earlier via `install_host.sh`/`install_sandbox.sh`
  accepted swapped bytes checked only against that same release's own
  `checksums.txt`): an explicit install naming one of these tags fails
  today with a download error, until a release is recreated on it (Path B
  below).

  Narrowed, not closed: two distinct re-creation paths remain, of
  different severity.

  Path A - re-running one of these tags' own original Actions run.
  Closed on 2026-09-27 by deleting those runs (see "Path A closed:
  the old Release runs are deleted" under "Release floor for
  installers and canga upgrade (PR #41)" below); what follows is the
  measurement that led there.
  Measured (2026-09-27): `gh run list --workflow release.yml --limit 50
  --json databaseId,headBranch,createdAt` returns exactly 17 runs total
  (below the 50-row limit, so this is every Release run the repository
  has ever had), created between v0.1.0's 34977708345
  (2026-09-15T13:51:14Z) and v0.10.5's own 36345101054
  (2026-09-27T19:38:33Z). GitHub's "up to 30 days after its initial run"
  re-run window (GitHub docs, "Re-running workflows and jobs") means none
  of these 17 runs has elapsed as of today: even the earliest, v0.1.0's,
  stays re-runnable until 2026-10-15T13:51:14Z, and every later run's
  window closes later still. A re-run executes that tag's own
  `release.yml` and `.goreleaser.yml`. For v0.1.0 through v0.10.2 -
  neither of which carries `check-previous` or `check-clobber` (measured
  above) - nothing in the tag's own workflow refuses a re-run, and its
  own `.goreleaser.yml` carries no `release:` block at all (checked
  directly at v0.10.2, v0.9.0 and v0.1.0), so GoReleaser's default
  applies: `release --clean` builds fresh binaries and publishes a brand
  new, non-draft release immediately - there is no draft step, no
  "Publish the release" step and no attestation to reach or pass,
  because none of those exist before v0.10.5's own `release.yml`. For
  v0.10.3 and v0.10.4, whose own `release.yml` does carry
  `check-previous` and `check-clobber`: `check-clobber` is not the gate
  here (a security-auditor Medium finding corrected this) - it 404s
  against the now-deleted release and returns 0, "has no release yet;
  nothing to clobber" (`cmd_check_clobber`, `scripts/sbx-kit-pin.sh`),
  never reaching its own "origin's main already pins" refusal at all.
  The actual gate is `check-previous`: with no published release below
  the tag it dies with "no published release below <tag>"
  (`cmd_check_previous`, same script); if an older release were recreated
  first, `pick above` still finds v0.10.5 and `older_line` dies, since the
  workflow never sets `SBX_KIT_OLDER_LINE`. Either way the re-run is
  refused before GoReleaser runs, for v0.10.3 and v0.10.4 alike, for as
  long as v0.10.5 stays published. Bound:
  re-running needs Actions write access (not merely repository push
  access - the "release tags" ruleset restricts a fresh `v*` tag push to
  the admin role, but a re-run needs no new tag push). v0.10.3 and
  v0.10.4 are protected by `check-previous` regardless of the window, per
  above. v0.1.0 through v0.10.2 have no such protection and are
  otherwise unmitigated until each run's own 30-day window closes for
  good - the last one among them, v0.10.2's own initial run
  (36217176585, 2026-09-26T04:13:21Z), closes 2026-10-26T04:13:21Z.

  Path B - direct release re-creation through the API, bypassing the
  Actions workflow entirely. Security-auditor Medium finding: deletion
  used no `--cleanup-tag`, so every one of these tags remains, and
  `check-previous`/`check-clobber` are steps inside the Release
  workflow - they run only when that workflow executes, never when
  someone instead calls `gh release create <old-tag> <assets...>`
  directly. Anyone holding `contents: write` (the repository admin, a
  workflow token granted that permission, or an agent session using the
  operator's own token) can create a release on any of these old tags
  with arbitrary assets and a `checksums.txt` they wrote themselves to
  match. Verified against the Go source: `canga upgrade`'s verification
  (`internal/upgrade/verify.go`, `verifyChecksum`/`checksumFor`) checks
  only that the downloaded archive's sha256 matches the line for it in
  that same release's own `checksums.txt` - nothing cross-checks against
  an attestation or any pin outside that one release - and
  `verifyRuns` (`internal/upgrade/install.go`) only executes the staged
  binary once to confirm it reports the expected tag and role, which a
  forged binary can print regardless. `install_host.sh` and
  `install_sandbox.sh` do the identical check (`checksums.txt` downloaded
  from the same release, `sha256sum -c` against it). So an explicit
  `install_host.sh <old-tag>`, `install_sandbox.sh <old-tag>` or
  `canga upgrade --tag <old-tag>` would accept a release recreated this
  way for any tag below v0.10.5. This path has no time bound and no
  automatic gate at all - it is not something the deletion narrowed.

  What the deletion actually closed: every unattested artifact that
  existed before 2026-09-27 is gone, and an unprivileged reader can no
  longer download one by simply naming an old tag. What it did not
  close: an actor who already holds `contents: write` can still make an
  old tag resolve to whatever they publish there, and today's installers
  and `canga upgrade --tag` do not refuse a tag below v0.10.5 outright.
  Marked `[narrowed; follow-up planned]` per the operator's decision
  (2026-09-27, verbatim): "a1 b1" - (A1) record this residual as narrowed
  with a follow-up planned, rather than closed; (B1) the four post-cap
  Low/Nit items from this same round applied separately, in this pass.
  Planned fix, not yet built: `install_host.sh`, `install_sandbox.sh` and
  `canga upgrade --tag` refuse any tag below v0.10.5 outright, closing
  Path B without waiting on Path A's 30-day windows. Reopen condition:
  this stays open until that follow-up ships.

  **Shipped (PR #41), tightened after a round-1 ship-gate re-audit.**
  The planned fix above is built: `install_host.sh` and
  `install_sandbox.sh` (POSIX `sh` awk) and `internal/upgrade.Run`/
  `wantedTag` (Go, `semver.Compare`) all require a release tag to be
  BOTH canonical `vX.Y.Z` (no pre-release, no build metadata, no
  leading zero, no missing "v") AND at or above v0.10.5, before
  downloading anything - applied to an explicit `--tag` and to
  whichever tag GitHub's own `/releases/latest` resolves to. The
  canonical-shape requirement is what a round-1 finding added: without
  it, a release published under a bare-digit tag ("1.0.0", not
  "v1.0.0" - outside the repository's `refs/tags/v*` ruleset, so no
  admin approval needed to create it) would have cleared the floor
  once normalized.

  Precisely what this closes, and what it does not: Path B (a
  `contents: write` actor recreating one of the OLD, deleted-release
  tags below v0.10.5, or reaching an equivalent tag by any spelling
  the check treats as the same release) is closed for these three
  install paths - refused before any archive or checksum is fetched.
  It does NOT stop a `contents: write` actor from publishing a BRAND
  NEW canonical tag at or above v0.10.5 (say, "v0.10.11") with
  whatever bytes they choose: that tag clears the floor exactly as a
  legitimate release would, because the floor's job is telling an old
  or unprotected tag apart from a covered one, not vouching for a new
  tag's contents. The only control on a new `v*` tag is the
  repository's own ruleset, which requires admin approval to create
  or move one - a separate, existing protection this PR does not
  change. Path A (the re-run window on v0.1.0 through v0.10.2's own
  original Actions runs) is a workflow-level re-run, not an
  install-time check, so the floor does not touch it; it was closed
  separately, on 2026-09-27, by deleting those runs. See "Release floor for installers and canga
  upgrade (PR #41)" below for the full account.

  Attribution: this entry's check-previous/check-clobber-per-tag
  measurement and its original deletion-resolved rewrite were written by
  build-5, on top of a round-1 security-auditor Medium finding that
  build-4 fixed first (operator quote "1"), correcting the original
  v0.10.4-and-earlier coverage claim. The attestation-per-tag measurement
  (`grep -c attest-build-provenance`) folded into this entry's opening
  paragraph was the stray, unvetted addition described in "Rework
  debrief: an unvetted write under a frozen ship gate" below - the
  round-3 ship gate re-audited it in full and found it accurate, so it
  was kept rather than reverted. This pass (build-6) corrects build-5's
  own misattribution of v0.10.4's refusal to `check-clobber` instead of
  `check-previous` (round-4 security-auditor Medium F2), and narrows the
  `[resolved]` marker build-5 gave this entry back down to
  `[narrowed; follow-up planned]` after round-4's security-auditor Medium
  F1 showed the deletion does not stop a `contents: write` actor from
  recreating an old release directly (operator quote "a1 b1").
- **Confirmed by the v0.10.5 run (2026-09-27):** GitHub does report a
  `digest` for a draft's assets - the publish step's pre-publish digest
  comparison against `dist/` passed without needing a fix, and the
  published release's assets carry a `digest` each. The re-GET loop
  settled inside its retry budget: run 36345101054's "Publish the
  release" step succeeded, and GitHub now serves the release with
  `immutable: true`; the orchestrator did not capture the exact re-GET
  attempt count from the run log, so only "within the 6-try budget" is
  measured here, not the precise attempt number. Both were previously
  unmeasurable without a GitHub write; see "v0.10.5: the first attested
  release" below for the full account.
- **Confirmed: the v0.10.5 release was the first real end-to-end run
  (2026-09-27).** Run 36345101054 ran every step green, including
  "Attest build provenance", "Verify the attestation before publishing"
  and "Publish the release" - the draft, the attestation, the
  pre-publish verification and the publish-by-id step all ran for real,
  not just validated by actionlint and `goreleaser check` as before.
  `make release-kit-bump TAG=v0.10.5` was then run for real (PR #37);
  see "v0.10.5: the first attested release" below.
- **Confirmed: immutable releases were enabled before v0.10.5
  (2026-09-27).** Per the operator's decision above, the orchestrator
  enabled them through the API after this PR merged
  (`PUT repos/brunovenceslau/canga/immutable-releases`, answered 204;
  the setting read `{"enabled":false}` before and `{"enabled":true}`
  after) and before v0.10.5 was tagged; `make release-preflight`'s
  `check-immutable` then passed. The failure mode this bullet described
  stays documented for any repository where the setting is off: if a
  release is tagged first, the run does not
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
  mutable until it is published" and "Confirmed by the v0.10.5 run"
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
  and mutable. Corrected under "Confirmed: immutable releases were enabled before
  v0.10.5 (2026-09-27)" above.
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
- [declined, operator-agreed] Pinning `--source-digest` to the resolved
  commit sha: not deferred hardening, declined outright. The check
  matches by tag NAME (`--cert-identity ...release.yml@refs/tags/<tag>`,
  `--source-ref refs/tags/<tag>`; `scripts/sbx-kit-pin.sh`'s
  `attestation_verify`, ~lines 469-497) - a commit-pinned
  `--source-digest` would only add protection if the tag could name a
  different commit than the one the release was built from.
  Attestation is required only from v0.10.5 on (`attested_from`).
  Immutable releases were enabled 2026-09-27, before v0.10.5 (see "Left
  open" above), and `gh api repos/brunovenceslau/canga/immutable-releases`
  reads `{"enabled":true,...}`. What is measured, not assumed, for a given
  release: v0.10.5's own release reads `immutable: true` via
  `gh api repos/brunovenceslau/canga/releases/tags/v0.10.5` (measured
  2026-09-27), and the workflow's own post-publish re-GET loop
  ("Publish the release") goes red, for any future release, if GitHub
  does not settle it to `immutable == true` within the retry budget - so
  a release whose run finished green is one the workflow observed as
  immutable at that moment; a red run can still leave a published,
  mutable release (see README "Releasing"'s refusal table).
  `check_published` itself does not re-check `.immutable` at pin time,
  which is exactly why the reopen condition below matters. Per GitHub's
  immutable-releases documentation
  (<https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases>,
  read 2026-09-27): for an immutable release, git tags "cannot be moved"
  and "cannot be deleted while the release exists"; after the release is
  deleted, the tag name "cannot" be reused. A deleted release also makes
  `check_published` refuse: `release_assets` reads the tag's release with
  `gh api repos/${repo}/releases/tags/$1`, which 404s once the release is
  gone, and that failure dies with "could not read the assets of release
  $1 with gh" - there is no release left to check. Before a release
  exists at all, the "release tags" ruleset (id 24082205, `refs/tags/v*`,
  creation/update/deletion, active; "Left open" above) restricts tag
  create, move and delete to the admin role. Honest residual: GitHub's
  documentation does not say whether any actor can bypass the
  immutable-release tag lock. If some actor can, the admin is the
  plausible one, since the admin already holds the ruleset's bypass and
  already controls the trust root (can merge a PR changing the script,
  `attested_from`, or `release.yml`) regardless. `--source-digest` would
  not remove admin power, so it adds nothing against the one actor left
  with any plausible route. Reopen condition: if immutable releases are
  disabled, or the ruleset is removed, this reopens. Operator agreement
  (2026-09-27, verbatim pt-BR): "Também vamos argumentar A onde ele foi
  salvo pra que não seja mais pendencia."
- [done] `timeout-minutes` on the Release job: pre-existing gap, not
  introduced by this PR; the job ran under GitHub's default 6-hour
  ceiling with no explicit shorter one. Resolved by "Release job timeout
  and the closed pendings (PR #39)" below.
- [accepted] The post-publish re-GET loop aborts on the first transient
  `gh api` error rather than retrying within its own budget - fails closed
  and loud, which is the right default for a check guarding a published,
  soon-to-be-immutable release. Confirmed by the v0.10.5 run (2026-09-27):
  run 36345101054 published clean, with no transient `gh api` error large
  enough to exercise this abort path. Still accepted as-is; revisit only
  if real transient-error noise shows up on a future run.

## v0.10.5: the first attested release (PR #37)

The measured, end-to-end confirmation that the PR #36 section's "Left
open" entries "Confirmed by the v0.10.5 run" and "Confirmed: the v0.10.5
release was the first real end-to-end run" pointed at:
this is what the orchestrator verified directly against the tag, the
Actions run, the published release and the independently downloaded
assets - not inferred from source reading or static checks.

- **Tag `v0.10.5`**: annotated, SSH-signed, on `de8b737` (main after
  PR #36). `make release-preflight` passed - immutable releases on;
  v0.10.4 checked by digest and uploader only, since it is below the
  `attested_from` cutover; no clobber. The push printed "Bypassed rule
  violations for refs/tags/v0.10.5: Cannot create ref due to creations
  being restricted", i.e. the "release tags" ruleset (id 24082205, see
  "Left open" above) enforced, and the admin bypass applied.
- **Release run 36345101054**: every step succeeded, including "Attest
  build provenance", "Verify the attestation before publishing" and
  "Publish the release" - so draft assets do report `digest`, and
  `immutable: true` settled inside the re-GET retry window (see
  "Confirmed by the v0.10.5 run" above).
- **Published release**: `draft: false`, `prerelease: false`,
  `immutable: true`, author `github-actions[bot]`, all 7 assets uploaded
  by `github-actions[bot]`.
- **Independent verification (gh 2.101.0)**: both `canga-sandbox_`
  archives pass `sha256sum -c` against `checksums.txt`, and each passes
  `gh attestation verify --cert-identity
  https://github.com/brunovenceslau/canga/.github/workflows/release.yml@refs/tags/v0.10.5
  --source-ref refs/tags/v0.10.5 --deny-self-hosted-runners` (exit 0
  each). A negative control with the identity's ref changed to
  `@refs/tags/v0.10.4` exits 1. `scripts/sbx-kit-pin.sh
  check-attestation v0.10.5 <amd64 archive>` accepts.
- **PR #37** (the kit bump from `make release-kit-bump TAG=v0.10.5`): one
  signed commit, `a2326ad`, on the tag commit, touching only
  `sbx-kit/spec.yaml` - `CANGA_VERSION` `0.10.5`, amd64 sha256
  `299d6b51a161ea285ee97c887a04af4ab76ce06e54995d23fffd8702311de46a`,
  arm64
  `f9d9f1a2c595eb3ab4250ed792aa89cdd00a7cd2ee47f37cdb803b93d8d6c4e9` -
  equal to the served digests. `check-previous v0.10.6` at `a2326ad`
  exits 0 through the full path (digest, uploader, attestation of both
  archives) - the first time that path ran against a real, attested
  release. Merged as `a865d40`; main CI green.
- **Process lesson** (rework debrief, per the house rule that a lesson
  from findings a better process would have prevented gets written down
  here). The orchestrator first told the operator to run
  `make release-preflight` before creating the tag; the preflight checks
  the tag at HEAD, so it refused ("HEAD carries no tag"). README's
  "Releasing" step 2 already had the right order (tag, preflight, push).
  Lesson: derive operator command sequences from the README's numbered
  steps, not from memory. Static-tool question: the preflight's own
  refusal message already names the fix, so no new tool is needed here.
- **Still pending:** of the two `[pending]` items the PR #36 section's
  "Ship-gate round 3 fixes" recorded, only `timeout-minutes` on the
  Release job remained pending. `--source-digest` pinning to the resolved
  commit sha is now `[declined, operator-agreed]` there instead of
  `[pending]`. Both are resolved by "Release job timeout and the closed
  pendings (PR #39)" below.
- **Operator decision on this entry** (2026-09-27). After PR #37 merged,
  the orchestrator offered one optional follow-up: a docs PR recording in
  this file that the v0.10.5 run resolved the PR #36 section's two open
  v0.10.5 items (now "Confirmed by the v0.10.5 run" and "Confirmed: the
  v0.10.5 release was the first real end-to-end run"). The operator's answer,
  verbatim pt-BR: "Vamos resoler o Opcional." This section and the
  "Confirmed" bullets above are that follow-up.

## Release job timeout and the closed pendings (PR #39)

Closes both `[pending]` items the PR #36 section's "Ship-gate round 3
fixes" left open, plus one documentation nit in that same section.

- **`timeout-minutes: 30` on the Release job.** The `release` job in
  `.github/workflows/release.yml` had no `timeout-minutes`, so it ran
  under GitHub's default 6-hour ceiling. All 9 successful runs from
  v0.6.0 (35308883501) through v0.10.5 (36345101054) finished in
  1m04s-1m56s wall time (`gh run list --workflow release.yml --json
  createdAt,updatedAt,conclusion`, `updatedAt` minus `createdAt`,
  measured 2026-09-27):

  | Release | Run | Seconds |
  | --- | --- | --- |
  | v0.6.0 | 35308883501 | 74 |
  | v0.7.0 | 35310355978 | 72 |
  | v0.8.0 | 35316294090 | 86 |
  | v0.9.0 | 35648494082 | 64 |
  | v0.10.0 | 36206853553 | 73 |
  | v0.10.1 | 36208337412 | 82 |
  | v0.10.2 | 36217176585 | 77 |
  | v0.10.4 | 36232145634 | 79 |
  | v0.10.5 | 36345101054 | 116 |

  30 minutes gives roughly 15x headroom over the slowest of those (116s),
  including the post-publish re-GET retry loop, while stopping a hung run
  far sooner than 6 hours. A timeout before "Publish the release" leaves
  at most a draft, which the next run of the same tag replaces: GoReleaser
  v2.18.2's `release.replace_existing_draft: true` deletes the previous
  draft matched by name and recreates it (`.goreleaser.yml`; see also
  "Design" above). The edge case of two same-named drafts existing at
  once is refused by the workflow's own check, not assumed away: the
  "Publish the release" step requires exactly one draft release for the
  pushed tag and fails ("want exactly one draft release for
  <tag>, found <count>") otherwise. One timeout during or after "Publish
  the release" may leave the release already public. README "Releasing"'s
  refusal table gained the matching row, including that `gh api
  repos/.../releases/tags/<tag>` does not return a draft release, so a
  404 there means nothing is public yet and a re-run is safe.
- **HANDOFF citation fix.** The "Tag ruleset for `v*`" entry under this
  section's "Left open" cited "round-2 ship-gate finding R2-L1
  ("Release provenance" section above)" - the section title was
  truncated, and the direction was backwards: R2-L1 is defined below, in
  this same section's "Ship-gate round 2 fixes (2026-09-27)". Fixed to
  cite it by its full title and correct direction. A grep of the whole
  file for other truncated `"Release provenance"` citations, or
  above/below directions of this same kind, found none.
- **`--source-digest` pinning: declined, not deferred.** The "Ship-gate
  round 3 fixes" entry is now `[declined, operator-agreed]`, with the
  argument recorded there: the attestation check matches by tag name, not
  commit, so a commit-pinned `--source-digest` only adds protection if a
  tag could name a different commit than the one its release was built
  from; v0.10.5 is attested and measured immutable, and the workflow's
  own post-publish re-GET loop goes red for any future release that does
  not settle immutable, so immutable releases plus the `v*` tag ruleset
  make a tag's binding to its published release's commit hold without
  `--source-digest` - except for whichever actor, if any, can bypass the
  immutable-release tag lock, which GitHub's documentation does not say;
  if one exists, the admin is the plausible candidate, since the admin
  already holds the ruleset's bypass and the trust root regardless. See
  that entry for the full argument and the reopen condition.
- **Accepted gap (test-engineer).** No CI check validates the Release
  workflow's own semantics (for example, that `timeout-minutes` is
  present and set, or that a step it depends on still exists under the
  name this file cites) or that README/HANDOFF prose describing
  `release.yml` stays consistent with the file itself. The timeout is
  observable only in a real run that actually hits it, which nothing here
  exercises. Accepted as-is: a workflow-semantics or docs-consistency
  check is real, not-yet-started tooling work, not a one-line fix, and
  nothing has drifted yet that this would have caught.

Operator decisions (2026-09-27, verbatim pt-BR): for B and C together,
"Combiunado, escolha 1 (b+c num PR só)."; for A, "Também vamos argumentar A
onde ele foi salvo pra que não seja mais pendencia."

## Open residuals closed or narrowed (PR #40)

The operator accepted closing two of the three open residuals rather than
leaving them pending. Operator agreement (verbatim pt-BR, 2026-09-27):
"aceitar". The third was narrowed, not closed, by a separate, later
operator decision to delete every unattested release. Operator decision
(verbatim pt-BR, 2026-09-27): "Remova todas as versões que não são
attested. Mais prático. Ninguém usa ainda, só eu."

1. HANDOFF residual 2 from the PR #34 entry, GNU Make's own `TAG=$(shell
   ...)` argv-expansion sharp edge - "Release path: the workflow is the
   one official path"'s "Addendum: ship-gate round-2 fixes on this same
   change (2026-09-27)" subsection, "Two residuals deferred, not fixed
   here" list, item 2 (see that entry for the measurement), and its
   second mention under "sbx-kit-pin: rewrite verifies, and
   canga-sandbox_ assets must carry this repo's Actions identity
   (PR #35)"'s "Left open". Still accepted, unchanged by this section.
2. "The draft is mutable until it is published", under "Release
   provenance: attestation, draft-first, immutable releases (PR #36)"'s
   "Left open". Still accepted, unchanged by this section.
3. "Immutability is not retroactive", under the same section's "Left
   open" - narrowed, not closed, by deleting every unattested release
   (operator decision, verbatim pt-BR, 2026-09-27: "Remova todas as
   versões que não são attested. Mais prático. Ninguém usa ainda, só
   eu."). A round-4 ship-gate Medium finding showed the deletion does
   not stop a `contents: write` actor from recreating an old release
   directly, so that entry was marked `[narrowed; follow-up planned]`
   (operator quote "a1 b1"), not `[resolved]`. That follow-up shipped in
   PR #41: `install_host.sh`, `install_sandbox.sh` and `canga upgrade
   --tag` (and its newest-release path) now refuse any release tag
   below v0.10.5 before downloading anything, which closes Path B for
   these three install paths. Path A (the re-run window) was closed
   separately by deleting the old Release runs; see that entry for the
   measurements, the deletion
   list and "Release floor for installers and canga upgrade (PR #41)"
   below for the fix itself.

### Rework debrief: an unvetted write under a frozen ship gate

During this PR's round-2 ship gate, the ship node meant to resume one of
its round-1 persona agents with a message but instead launched a fork of
itself, which inherited the round-2 fix instructions and wrote them into
`docs/HANDOFF.md` in the frozen worktree after both round-2 personas had
reported. The ship node caught and disclosed it; the write was confined to
this file (`git status --short` showed only `docs/HANDOFF.md` changed, and
the main tree was clean). Because a write under a completed verification
pass invalidates its baseline, the orchestrator did not revert the stray
write: it kept it and had round 3 re-audit the whole diff with fresh
personas, rather than a targeted re-check, so every line the fork added
was independently checked rather than discarded unread. Round 3 found the
stray content accurate and it stands in "Immutability is not
retroactive" above, attributed there to this fork.

Lesson: a ship node that resumes a child must use the resume call, never a
spawn; a fork inherits the node's full brief, including fix text meant for
the builder. Static-tool question: the gate could record the worktree's
`git diff` hash when it freezes the tree and compare it before reporting,
which turns an unvetted write into a mechanical refusal instead of relying
on the node noticing. Not built here.

## Release floor for installers and canga upgrade (PR #41)

Closes Path B of "Immutability is not retroactive" (above) for the three
install paths that entry named: `install_host.sh`, `install_sandbox.sh`
and `canga upgrade --tag` (Go, `internal/upgrade` and `internal/cli`,
shared by both the host and sandbox builds) now refuse a release tag
before downloading anything unless it is BOTH canonical `vX.Y.Z` (no
pre-release, no build metadata, no leading zero, no missing "v") AND at
or above v0.10.5, the first release `release.yml` attests with build
provenance. Operator decision (2026-09-27, verbatim): "a1 b1" - A1 = this
follow-up.

**Round-1 ship-gate findings, both closed in the same pass.** Finding 1:
the first version of this fix compared the three numeric fields in awk
without checking their shape first, so a pre-release of the floor itself
("v0.10.5-rc1") or a leading zero ("v00.10.5") coerced to the same
number as the floor and was wrongly accepted, while Go's
`semver.Compare` correctly refused both - the two implementations
disagreed. `checkFloor` (`internal/upgrade/floor.go`) and both scripts'
awk now require the ONE canonical shape above before comparing anything
numerically, and `TestReleaseFloorShapeAgreesAcrossInstallersAndGo`
(`release_floor_test.go`) runs an eighteen-entry table of tag shapes
through both scripts and `upgrade.CheckReleaseFloor` and requires all
three to agree, tag for tag. One deliberate side effect: `checkFloor`'s
canonical-shape requirement is stricter than `isReleaseTag`, which stays
exactly as lenient as it always was everywhere else in the package (a
bare-major tag or a pre-release still names a real release as far as
`isNewer`/`sameTag` are concerned) - so `canga upgrade --tag` now refuses
a pre-release or a 2-component tag even when its numeric core sits above
the floor, which it did not before this PR.

Finding 2: `internal/upgrade.Run`'s newest-release path checked the
floor against `found.Tag` after implicitly trusting `normalizeTag` to
prepend a missing "v", so a release published under a tag with no "v" at
all (say, "1.0.0") - a tag the repository's `refs/tags/v*` ruleset never
protects, reachable by any `contents: write` actor with no admin
approval - would clear the floor once normalized to "v1.0.0".
`checkFloor` no longer normalizes; it refuses `found.Tag` outright if it
is not already canonical. `Run` additionally refuses outright if an
explicit `--tag` resolves to a release document naming a different tag
(`ErrTagMismatch`) - defense in depth, since a `byTag` lookup already
404s on a mismatched tag rather than serve a different release's
document, so this path is not known to be reachable through the real
GitHub API today.

`install_host.sh`'s and `internal/upgrade.Run`'s newest-release paths
(no tag given, following `/releases/latest`) apply the same floor to
whichever tag they resolve to, not only to an explicit `--tag`: defense
in depth against Path B recreating an old tag's release with today's
publish date, which would make it the newest one by GitHub's own
reckoning. `install_sandbox.sh` has no such path; its tag is always
explicit.

Compared as three dot-separated numbers (`X.Y.Z`, no leading `v`), never
as text - v0.9.10 sorts above v0.9.9 - the same way `version_at_least` in
`scripts/sbx-kit-pin.sh` and `semver.Compare` in `internal/upgrade`
already do.

One value, kept in four places rather than one, because none of the four
can read it from the others at the point they need it: a POSIX `sh`
installer piped straight from `curl` into `sh` has no working tree to
read a shared file from, and `scripts/sbx-kit-pin.sh`'s own
`attested_from` predates this PR by two releases (PR #37) and is read by
a different program entirely (`gh`-based release tooling, not `curl`
and `sh`). `install_host.sh` and `install_sandbox.sh` each set
`min_version=0.10.5`; `internal/upgrade` (`floor.go`) sets
`MinReleaseTag = "v0.10.5"`; `scripts/sbx-kit-pin.sh` already set
`attested_from="0.10.5"`. `TestReleaseFloorMatchesAcrossInstallersAndCanga`
(`release_floor_test.go`, repository root) reads all four back and fails
if any one drifts from the others - the one source-of-truth gate the
four hand-written copies otherwise have no way to enforce among
themselves.

**Precisely what the floor does not cover.** It tells an old or
otherwise unprotected tag apart from a covered one; it does not vouch
for a NEW tag's contents. A `contents: write` actor can still publish a
brand new tag at or above v0.10.5, in the one shape this check accepts,
with whatever bytes they choose - that clears the floor exactly as a
legitimate release would, because nothing in this PR checks who
published a release or what is inside it. The only control on a new
`v*` tag is the repository's own ruleset, which requires admin approval
to create or move one; this PR neither replaces nor changes that
control.

### Path A closed: the old Release runs are deleted

Path A (re-running v0.1.0 through v0.10.2's own original Actions runs,
"Immutability is not retroactive" above) is a workflow-level re-run,
which no install-time check can reach. GitHub's documentation is silent
on whether disabling a workflow blocks re-running its past runs, so
disabling `release.yml` was not a measured control (and would also have
blocked new releases). Deleting the runs is: a deleted run cannot be
re-run. Operator decision (2026-09-27, verbatim): "1".

Measured (2026-09-27): `gh run list --workflow release.yml --limit 100`
listed 17 runs; the 16 other than v0.10.5's (36345101054) were deleted
with `gh api -X DELETE repos/brunovenceslau/canga/actions/runs/<id>`,
failed ones included, since "Re-run failed jobs" also applies to them:
36232145634 (v0.10.4), 36226282318 (v0.10.3), 36217176585 (v0.10.2),
36208337412 (v0.10.1), 36206853553 (v0.10.0), 35648494082 (v0.9.0),
35316294090 (v0.8.0), 35310355978 (v0.7.0), 35308883501 (v0.6.0),
35298710956 (v0.5.0), 35187656571 (v0.4.0), 35179335378 (v0.3.0),
35029357639, 35028878420 and 35026407125 (v0.2.0), 34977708345
(v0.1.0). Afterwards the list shows only 36345101054, and `gh api -X
POST repos/brunovenceslau/canga/actions/runs/36217176585/rerun`
answers 404 Not Found.

Cost: those runs' logs are gone. Earlier entries in this file cite some
of them as evidence (for example the v0.10.2 build-time sums in the
"PR #31: the v0.10.2 pin, rebuilt after the fact" entry); the values
that mattered were already copied into this file, and the run links in
those entries no longer resolve.

### Rework debrief: a local gate that skipped the linter

The first push of this PR went red on CI's Lint leg (11 golangci-lint
issues: goconst, gocyclo on `upgrade.Run`, testifylint require-error,
wsl_v5). The local gate runs for this PR and for PR #39 reported `golangci-lint` and `govulncheck` as "not installed,
CI runs them" and moved on, although the Makefile has pinned installers
for both (`make tool-lint`, `make tool-vuln`). The ship gate's three
rounds passed with the linter never run.

Lesson: a missing tool that the repository pins and installs itself is
not a skip condition; install it with the repository's own target and
run `make lint` and `make vuln` locally before any gate reports. Static
tool question: the builder and ship-gate briefs could require
`make tools lint vuln` as their first gate step, so a missing linter
fails loudly instead of being reported as skipped. Applied from this
PR on, in the orchestrator's briefs.

### Pending

- [pending, post-cap Info from the round-3 ship gate] README's by-hand
  host install block (under "Install a release binary") computes the
  newest tag and downloads it in one paste, with no pause to check the
  tag's shape and floor first. The prose above it now tells the reader
  to check; a one-line shape guard inside the block would make the
  check mechanical. Not a security-model surface; left for a later docs
  pass.

## Reminders: an origin-less repository is keyed by its path below the clone base

canga has no ADR log; this entry is the decision record. Before it, v0.10.5
keyed a store only by the `origin` remote, so a repository with none failed
with `no origin remote ... No such remote 'origin'` (exit 2). The operator
keeps origin-less trees under `~/src/local/<name>` and clones under
`~/src/<host>/<owner>/<repo>`.

### Operator decision

Operator rule (verbatim, pt-BR, 2026-09-27): "As chaves deveriam funcionar
sem o `src` sempre. `local/OS` aponta parar `~/src/local/OS` e
`github.com/brunovenceslau/foo` deveria apontar para
`~/src/github.com/brunovenceslau/foo`."

Read as: a reminders key IS the repository's path relative to the clone
base. Implemented as: an `origin`, when present, stays the key unchanged
(for a `canga git clone` it already equals that path); a repository with NO
`origin` remote is keyed by its working tree's path below the base.

### Design

- **One definition of the base.** The root is `repo.BaseDir`, the one
  `canga git clone` already uses (`${CANGA_SRC_DIR:-$HOME/src}`), and the
  variable name is the exported `repo.BaseDirVar`. No second variable: two
  names for one directory could disagree silently and split a list.
- **Per build.** `cli.App.BaseDir` is set by each build. The host build uses
  `repo.BaseDir`. The sandbox build uses `repo.HandedBaseDir`, which reads
  `CANGA_SRC_DIR` only, requires it absolute, and never defaults: the
  sandbox's `$HOME` is `/home/agent` while repositories are mounted at their
  host paths, so `$HOME/src` would name a directory nothing lives under,
  and a relative value would be resolved against a working directory the
  host never had. This follows the `CANGA_REMINDERS_DIR` precedent (a
  literal host path handed over in the environment file). A nil `BaseDir`
  keeps the origin-only rule.
- **The rename to `CANGA_SRC_DIR`.** The variable was `CANGA_HOST_BASE_DIR`.
  Both builds now read it, and the scheme from `951ec14` reserves
  `CANGA_HOST_` for what only the host build reads, so the operator chose
  `CANGA_SRC_DIR` (operator approval of this scope, 2026-09-28: "pode
  seguir"). Precedent checked: `951ec14` and the removed-name test in
  `internal/cli/app_test.go` ("the pre-canga variables are not read") did
  neither refuse nor warn; the old names were silently not read. That is
  exactly the failure the operator asked to avoid here, since ignoring
  the old name falls back to `$HOME/src`. So this rename REFUSES instead:
  while `CANGA_HOST_BASE_DIR` is set, `repo.BaseDir` and
  `repo.HandedBaseDir` fail with `repo.ErrRenamedVariable` (exit 2, naming
  the new variable), even if `CANGA_SRC_DIR` is set too. It only fires
  where the base is read (`git clone` into the layout, `workspace`, and the
  reminders fallback); a repository with an origin never reads it. README's
  rename table has the row.
- **Filesystem, not repository content.** The working tree is the nearest
  directory holding a `.git` entry, walked up from the canonical `-C`
  directory, and git's `rev-parse --show-toplevel` must be the same
  directory by file identity (`os.SameFile`), not by string.
  A `core.worktree` in `.git/config`, or `GIT_WORK_TREE`, that relocates the
  working tree fails with `repo.ErrWorktreeElsewhere` instead of choosing a
  key. Both base and toplevel are made canonical (below) before
  `filepath.Rel`; the result is refused when it is `.`, `..`, starts with
  `../`, or is absolute (`repo.ErrNotUnderBase`), and each segment is held
  to `repo.IsSafeSegment`, the rule URL segments already meet
  (`repo.ErrBadPath`). The directory then goes through `repo.EscapePath`
  like an origin key's, so `local/OS` is stored under `local/!o!s`.
- **Only a truly absent origin falls back.** The fallback runs only when
  `git remote get-url origin` fails AND `git remote` does not list `origin`.
  Measured on git 2.53: an `origin` with only a `pushurl` or `fetch`, or an
  empty `url`, makes `get-url` print the remote's name, which fails as
  `ErrBadURL` and never reaches the fallback.
- **Errors keep their exit code.** A fallback refusal that means "this
  location gives no key" (the sentinels in `cli.keySentinels`: outside the
  base, no such base, a bad or reserved segment, a relocated or unverified
  worktree, an unreadable spelling, base unset in the sandbox, the renamed
  variable) wraps `repo.ErrNoOrigin`, so it is still exit 2, and names why.
  Anything else, an I/O failure such as EACCES or EIO, a cancellation, or a
  missing git, passes through as a runtime failure, exit 1 (ship-gate
  round 1, code-reviewer).

### macOS case: measured, then fixed

The first version compared the `.git` walk's toplevel with git's answer as
strings, and took the key from `filepath.EvalSymlinks`. The operator
measured on a Mac (APFS, case-insensitive), for one directory created as
`$T/src/local/OS` and reached as `$T/SRC/LOCAL/os`:

```
pwd    = $T/SRC/LOCAL/os          pwd -P = $T/src/local/OS
git rev-parse --show-toplevel (cd or -C) = $T/src/local/OS
filepath.EvalSymlinks($T/SRC/LOCAL/os)  = $T/SRC/LOCAL/os   (typed case kept)
Rel($T/src, that)                        = ../SRC/LOCAL/os
os.Getwd                                 = $T/SRC/LOCAL/os
```

So a wrong-case `-C` or working directory was refused with a misleading
`ErrWorktreeElsewhere`, and a wrong-case `CANGA_SRC_DIR` put every tree
"outside" the base. The fix:

- **Identity, not strings.** `toplevel` compares the walked directory and
  git's with `os.SameFile`. `core.worktree` and `GIT_WORK_TREE` pointing
  elsewhere are still a different file, so still refused.
- **Canonical spelling.** `canonical` resolves symbolic links, then walks
  each component. An ASCII component whose case-flipped spelling is not
  the same file sits in a directory that tells case apart, and the typed
  spelling is kept without reading the directory, so Linux and
  case-sensitive volumes never read one (a directory with execute but no
  read permission still resolves). Otherwise the parent is read and the
  entry that is the same file is taken. No cgo. Base and toplevel both go
  through it, so `$T/SRC` and `$T/src/LOCAL/os` key as `local/OS`.
- **Fail closed.** When the spelling cannot be established (an unreadable
  parent, a failing Lstat, no matching entry), `canonical` fails with
  `repo.ErrUnreadableSpelling` instead of keeping the typed spelling: on
  APFS the typed spelling is exactly the one that opens a second store
  (ship-gate round 1, code-reviewer).
- **Unicode normalization** is covered by the same mechanism, since the
  match is by file identity: a name typed NFC and stored NFD resolves to
  the stored bytes. It has no effect on keys in practice, because
  `IsSafeSegment` admits ASCII only, so a non-ASCII segment is refused
  either way.
- **Tests.** Linux CI cannot reproduce a folding volume, so the derivation
  reads the filesystem through a small seam (`fileSystem`), and
  `key_case_test.go` supplies `foldingFS`, which matches components
  case-insensitively over a real directory and keeps the typed case from
  `EvalSymlinks`, as measured above. Two mutations were checked: dropping
  the respelling fails `TestCanonical`, `TestPathUnder_CaseInsensitive` and
  `TestToplevel_CaseInsensitive`; going back to string comparison fails
  `TestToplevel_CaseInsensitive`.
- **Re-check on a Mac.** Not run on a Mac by this change; darwin/arm64 and
  darwin/amd64 host binaries were built for the operator to repeat the
  measurement.

### Key collisions

- **Path key equal to an origin key.** Intended, not merely tolerated: an
  origin-less tree at `<base>/github.com/acme/widget` is, by the layout's
  own definition, `github.com/acme/widget`, so it reads that repository's
  list. Pinned by `TestConfig_OriginLessAtTheCloneLayoutSharesTheOriginKey`.
- **A key that is a prefix of another** (a path key `local/OS` and a
  repository nested at `local/OS/repo/x`): refused for location-derived
  keys. A segment the store uses inside a key's directory (`repo`, `items`,
  `order`, `tmp`; `repo.IsReservedSegment`, pinned against `cli.ScopeRepo`
  and `store.DataDirs` by `TestReservedSegmentsCoverTheStore`) is not a key
  segment, so no path key can nest inside another store's scope data
  (ship-gate round 1, code-reviewer). Between ORIGIN keys of different
  depth (GitLab subgroups) the nesting was already reachable and stays
  inert: the store only reads regular files in `items/` and skips
  directories there (`Store.items`).
- **Linked worktrees** share the main tree's key; see below.
- **Trust.** A key is a LABEL, not an authenticated identity: an origin is
  whatever the repository's configuration says. The sandbox build can only
  `list` and `add`, and could already address any origin-derived key with
  `git remote add origin <url>`; the fallback adds no reach an agent did
  not have. What it guarantees is that a key from the fallback names a
  directory the process was actually pointed at, not one chosen by
  repository configuration.

### Round 3: ship-gate findings, worktrees, store links, platforms (2026-09-28)

Operator decisions (verbatim, pt-BR, 2026-09-28):

- Worktrees: "2) (a)" - key a linked worktree by its main working tree.
- Store symlink: "Corrija symlink neste PR mesmo."
- Platforms: "precisaremos de builds do canga no release tanto para darwin
  (hosts) quanto para linux (sandboxes)"; earlier: "precisamos publicar
  darwin/amd64 [sempre host, como darwin/arm64] e linux amd64 [sempre
  sandbox, como linux/arm64] também"; "O host é MacOnly então deveria ter
  um gate de build em mac. Tecnicamente só a versão de sandbox roda no
  Linux. Deveríamos testar exatamente o uso esperado nos gates."; "Veja
  qual é o ubuntu que o sbx rodaria no intel e use a mesma versão. LATEST
  != 24.04."; and, confirming the removal of the linux host build: "Sim!
  Por não termos host linux como já conversamos, pode remover."

What changed:

- **Linked worktrees (code-reviewer, Required).** `repo.KeyTree` keys a
  linked worktree by its main working tree, the parent of the resolved
  `git rev-parse --git-common-dir`, and only when the link checks out both
  ways by `os.SameFile`: the worktree's git directory sits in
  `<common>/worktrees/`, its `gitdir` file names this tree's `.git`, and
  `<common>` is the `.git` of the directory taken as the main tree.
  Otherwise `repo.ErrWorktreeUnverified`, never the worktree's own path.
  The main tree's path goes through `canonical`. A submodule, whose git
  directory is its common directory, is keyed by where it is. Tests: a
  worktree under `<main>/.claude/worktrees/x`, a sibling `local/OS-wt`, a
  forged `gitdir`, a forged `commondir`, a submodule, and the case
  canonicalization of the main tree.
- **Store links below the root (security-auditor, Medium, pre-existing).**
  Only the leaf store directory was refused as a link; `os.MkdirAll` and
  `os.OpenRoot` followed a link in an intermediate key directory, so a
  sandbox replacing `$REM/local/!o!s/sub` with a link to a host path made
  the host's `reminders add` write there (auditor PoC 11, rc=0). Now
  `store.Config.Base` is the reminders root, which every production caller
  sets: the store opens it once with `os.OpenRoot` and enters each
  directory below it relative to its open parent (`descend`/`enter`),
  refusing a link before the open and re-checking after it that the handle
  is the directory at that name (a link swapped in and out is caught). A
  link pointing INSIDE the root is refused too, since `os.Root` alone
  would follow it into another repository's store. The root itself may be
  a link (a symlinked `~/.local/share`). Tests plant the link at the first,
  an intermediate and the leaf component, pointing outside and inside the
  root, for both openers, and assert nothing was written; a host command
  test and the sandbox E2E repeat the PoC.
- **The rest of round 1** (code-reviewer Optional/Nit, security-auditor
  Low/Info, test-engineer): an unresolvable base is `repo.ErrNoSuchBase`
  (not `ErrNotUnderBase`), an unresolvable tree is its own runtime error,
  and `CANGA_SRC_DIR` is named in a message only when it is set; the
  exit-code split above; reserved segments; fail-closed spelling; the key
  documented as a label; tests for `GIT_WORK_TREE`, NFC/NFD
  (`normalizingFS`), `ErrRenamedVariable` in `TestExitCode`, the sandbox
  build's renamed-variable exit 2, a trailing-slash base, the `toplevel`
  Stat failures, and both `dotGitAbove` failure branches.

### Rule: gates test exactly the expected usage

Gates test exactly the expected usage: host on macOS, sandbox on Linux, both
architectures each (operator rule, 2026-09-28, quoted above).

- **Published set.** The host build is published for darwin/arm64 and
  darwin/amd64 only, the sandbox build for linux/arm64 and linux/amd64
  only: `.goreleaser.yml`, the Makefile's `HOST_PLATFORMS` and
  `SANDBOX_PLATFORMS` (`make cross`), and `upgrade.PlatformFor` agree, and
  `platforms_test.go` pins all three. `install_host.sh` refuses Linux and
  points to `install_sandbox.sh`; `canga upgrade` refuses a build off its
  operating system with `upgrade.ErrUnsupportedPlatform`, exit 2, after
  the tag check and before any request.
- **Test workflow.** `host` runs `make test-host` and a host build on
  `macos-26` (arm64) and `macos-26-intel` (x64), including
  `TestHost_APFSWrongCaseKeysAsStored`, which runs only on darwin, on a
  real volume, and skips only when that volume is case-sensitive. `sandbox`
  runs `make test-sandbox` and `make e2e-sandbox` (`scripts/e2e-sandbox.sh`)
  on `ubuntu-26.04-arm` and `ubuntu-26.04`. `cross` runs on `ubuntu-26.04`.
  shellcheck v0.11.0 and jq 1.8.1 are installed by `scripts/ci-tools.sh`,
  pinned by sha256 for each of the four platforms (GitHub's asset digests;
  jq's own `sha256sum.txt` agrees), and zsh from apt where it is absent.
- **Pinned labels.** No workflow uses `*-latest`: lint, security and
  release moved to `ubuntu-26.04` too. The Ubuntu version tracks the sbx
  sandbox image (`docker/sandbox-templates:claude-code`, multi-arch,
  measured Ubuntu 26.04.1 in the operator's arm64 sandbox) and moves with
  it. Both `ubuntu-26.04` labels have been GA since 2026-09-17.
  actionlint v1.7.12 does not list them yet, so `.github/actionlint.yaml`
  declares them.
- **Lint for both systems.** `make lint` runs `go vet` and golangci-lint
  once more with `GOOS=darwin`, so a darwin-only file is linted on any
  machine.

### Round 4: ship-gate round 2 findings (2026-09-28)

One consolidated fix pass over the 18 findings of the second ship-gate
round (round 2 of 3), with the orchestrator's decisions applied. Each fix
has a test written first and seen failing, unless noted as coverage of
behavior that already held.

- **macOS legs (code-reviewer, Required).** Two `TestCanonical` subtests ran
  `osFS{}` on the real filesystem assuming it tells case apart; APFS folds.
  `foldsCase` probes the temporary volume (a lower-case file named in upper
  case, compared by `os.SameFile`): on a folding volume the wrong-case
  subtest now asserts the stored spelling comes back (a real-APFS check of
  `canonical`), and the two-entries subtest skips. Sweep of `./internal/...`
  and `./cmd/...` for the same assumption: case-pair and symlink-sensitive
  path assertions were read (`grep` for mixed-case pairs, `EqualFold`,
  `ToLower`, `/proc`, `runtime.GOOS`, `Root`/`CommonDir`/`Toplevel` result
  comparisons); every other path comparison already resolves the temporary
  directory (`mkdirs`, `resolve`), and the escaped-key tests compare strings,
  never on-disk entries. No other case-sensitive assumption found; a Linux
  sandbox cannot mount a folding volume (the kernel lacks `CONFIG_UNICODE`
  and has no vfat), so only the macOS legs prove it.
- **The worktree `gitdir` record (security-auditor, Medium).** It was read
  with `os.ReadFile`: a link to any host file printed that file on stderr,
  a FIFO hung the host build, `/dev/zero` exhausted memory. New package
  `internal/boundedread`: open with `O_RDONLY|O_NOFOLLOW|O_NONBLOCK`, judge
  the OPEN handle by `fstat` (regular files only), re-`Lstat` the name and
  require the same file (catches an opener that follows links, and a swap),
  read through `io.LimitReader` with a cap. The seam's `ReadFile` became
  `ReadRegular`. The record is capped at 4096 bytes (Linux `PATH_MAX`; the
  record is one path and a newline). Repository-supplied strings in the
  refusal are `strconv.Quote`d and truncated to 128 bytes, and a
  `*fs.PathError` is reduced to its operation and errno (`why`), so an
  escape sequence never reaches the terminal. Tests:
  `TestKeyTree_GitdirRecord` (link to a secret, link to a valid copy, FIFO,
  oversized, escape sequence), `TestRegular`.
- **`.git` links (security-auditor, Low).** A `.git` that is a symbolic link
  is refused, `repo.ErrDotGitLink` (a key sentinel, exit 2). Every identity
  comparison is by `Lstat` (`sameEntry` replaced `sameFile`); a linked
  worktree's `.git` must be a regular file, and the main tree's `.git` a real
  directory that is the common directory. Tests: `TestKeyTree_DotGitLinks`,
  and "a .git that is a link" in `TestConfig_OriginLessRefusals`.
- **Third check coverage (test-engineer, High):** `TestKeyTree_ThirdCheck`,
  a planted common directory that passes checks 1 and 2, and a main tree
  whose `.git` only links to it (passes under `Stat`, refused under `Lstat`).
- **Bare and `--separate-git-dir` worktrees (code-reviewer, Optional):** the
  refusal now says there is no main working tree and to give the repository
  an `origin`. `TestKeyTree_NoMainTree`, one subtest per layout.
- **`ErrWorktreeUnverified` end to end (test-engineer, Medium):** "a forged
  worktree" in `TestConfig_OriginLessRefusals`, exit 2 (coverage).
- **`repoName`'s `case has` (test-engineer, Medium): live, not dead.**
  Configs tried on git 2.53: `remote.origin.fetch` only and an empty `url`
  (both print the name: `ErrBadURL`), a `remotes/origin` or
  `branches/origin` file (`get-url` answers, `git remote` does not list it:
  the reverse case), `url..insteadOf`, two urls, `skipDefaultUpdate`,
  `vcs`, `remote.Origin.url` (exit 2, and `git remote` lists `Origin`, not
  `origin`). A url of only whitespace (`remote.origin.url " "`) reaches it:
  `get-url` prints a blank line, `Origin` reads no origin, `git remote`
  lists `origin`. Test: "an origin git lists but gives no url for";
  removing the branch fails it.
- **Fail closed on real APFS (test-engineer, Medium):**
  `TestHost_APFSUnreadableSpellingFailsClosed` (darwin only) makes the
  parent execute-only (mode 000 would fail the typed lookup itself, a plain
  permission error that never reaches the spelling) and asserts
  `ErrUnreadableSpelling`, exit 2. Not runnable here.
- **Store I/O branches (test-engineer, Low):** `TestOpen_ReportsIOFailures`
  (read-only base, unsearchable and unopenable directories, a name over
  NAME_MAX, the parent and the directory locked after the open through the
  hook); `enter` and `checkRoot` are at 100% statement coverage.
- **Relative `CANGA_SRC_DIR` at the CLI (test-engineer, Low):**
  `TestSandbox_RelativeBaseIsAUsageError`; accepting a relative base fails
  it.
- **Nested keys (code-reviewer, Optional): narrowed, not restructured**
  (coordinator's decision, taken in ship-gate round 3: no layout change).
  README and the
  `reservedSegments` comment now say what is prevented (a location key
  inside another location key's scope data) and what is not (an origin key
  inside a location key's directory). The structural fix is an open item
  below.
- **`make ci` runs `e2e-sandbox` (code-reviewer, Optional).**
- **Store reads (security-auditor, Low, pre-existing):** items, `Get` and
  the order document go through `boundedread` via `Store.read`
  (`os.Root.OpenFile`, whose link following the re-`Lstat` refuses, and
  `os.Root` refusing a link out of the root is also reported as a link).
  Caps: 1 MiB per item (a reminder is a line or a few paragraphs; more than
  a macOS command line carries, so no `add` comes near it), 4 MiB per order
  document (about 34 bytes per id: over 100,000 ids). `List` skips an entry
  that is not a regular file, as it skips a directory, and fails on an
  oversized item; `Get` and the order read fail on both; `Add` refuses text
  whose item would exceed the cap (`store.ErrTextTooLarge`), so what it
  writes always reads back. Tests: `TestStore_ReadsOnlySmallRegularItems`,
  `TestStore_ReadsOnlySmallRegularOrder` (FIFO, link out of the store, link
  to another item, oversized, at the limit), and an E2E step planting a FIFO
  item, with a watchdog that sends KILL: canga turns TERM and INT into a
  cancellation, which a read blocked in the kernel never sees (measured:
  with the old read the step hung until KILL; TERM did nothing).
- **`reminders path` (security-auditor, Info):** its help and README say it
  only prints a path, and whatever opens it later follows what is there by
  then.
- **git failures classified (security-auditor, Info, pre-existing).**
  `repo.Origin` runs `git remote get-url origin` with `LC_ALL=C` and maps to
  `ErrNoOrigin` (exit 2) only git answering about the directory: exit 2
  (git-remote(1)'s "no such remote"), `fatal: not a git repository`,
  dubious ownership, or a directory that does not exist. Anything else,
  such as an unreadable directory or `.git/config`, is `ErrGitRefused`,
  exit 1, and never reaches the fallback. Contained in `repo.Origin`.
  Tests: four new `TestOrigin` subtests (dubious ownership via
  `GIT_TEST_ASSUME_DIFFERENT_OWNER`, missing directory, mode-000 parent,
  mode-000 config) and "a directory git cannot enter" in
  `TestConfig_RuntimeFailuresAreNotUsageErrors`.
- **Nits:** the `race` comment moved back above `race`; the Makefile's long
  comment line rewrapped; `install_test.go`'s fixture no longer builds
  `canga-host_*_linux_*` archives.

### Ship-gate round 3: GO, with capped-round pending items (2026-09-28)

The ship gate reached GO on its third round. The round budget for
Optional/Nit/Low/Info findings (3 rounds) is exhausted, so these are
recorded here rather than fixed in this PR:

- **`-C` on a regular file (code-reviewer, Optional).** `-C <regular
  file>` exits 1 (`git refused ... Not a directory`) instead of the usual
  exit 2. Fix: in `internal/repo/repo.go`'s `originRefusal`, treat
  `statErr == nil && !info.IsDir()` the same as a missing directory
  (`ErrNoOrigin`), and add a test.
- **`item.encode()` recomputed in the retry loop (code-reviewer, Nit).**
  `internal/store/store.go:387-390` calls `len(item.encode())` on every
  pass of the id-retry loop. Fix: compute it once, before the loop.
- **`maxOrderBytes` has no at-the-limit test (test-engineer, Low).** Add a
  test for a document exactly at `maxOrderBytes` (`internal/store/order.go`).
- **Relative gitdir branch uncovered (test-engineer, Low).** The relative
  gitdir-pointer branch at `internal/repo/key.go:451` has 0% coverage. Add
  a test with a relative pointer.
- **Oversized or unreadable order document fails `list` and `reorder`
  (security-auditor, Info N1).** A planted oversized item, or an unreadable
  order document, fails the whole host `list` (and `reorder`) instead of
  degrading. Fix: skip oversized items in `internal/store/store.go`'s
  `items()` the way non-regular ones are already skipped, and fall back to
  an older order version or no order when the order document cannot be
  read. No new capability follows from this, since the sandbox can already
  delete the store.
- **A racy `os.Root` escape error reaches `list` raw (security-auditor,
  Info N2).** Under a race, an `os.Root` escape error at
  `internal/boundedread/boundedread.go:55` reaches `items()` unmapped and
  fails `list` (measured 42 of 600 runs; transient, no leak). Fix: map it
  to `ErrNotRegular` in `Store.read` (`internal/store/store.go`).
- **HANDOFF wording (code-reviewer, Nit): fixed in this same PR, not
  deferred.** The two "Nested keys" references above (around the round 4
  section and in "Open") called that deferral an "orchestrator decision"
  taken "in round 4". It was the coordinator's decision, taken in
  ship-gate round 3. Corrected above.

Also recorded, not fixed (security-auditor, optional hardening, not a
finding): `originRefusal` in `internal/repo/repo.go` matches "dubious
ownership" against the whole of `exit.Stderr`; it could instead match only
the first stderr line with the `fatal: ` prefix, narrowing what counts as
git's own refusal.

### Round 5: the first macOS run found an installer checksum bypass (2026-09-28)

The first run of `TestInstallHost` on the macOS legs (run 36441444235,
both `macos-26` and `macos-26-intel`) failed `{bash,sh}/no_checksum_line`:
exit 0 where 1 was expected, with the archive installed. The fixture was
right (the "absent" checksums.txt lists no platform at all); the installer
was not. `install_host.sh` handed the checker whatever
`awk '$2 == a'` found and relied on it refusing empty input. GNU's and
uutils' `sha256sum` and `shasum` do; the `sha256sum` on the macOS runners
(the image lists no GNU coreutils) exits 0, and `install_host.sh` prefers
`sha256sum` over `shasum` when both exist. The same case with `shasum`,
and a mismatched hash with `sha256sum`, were refused on the same runners.
`main` carries the same code; the published v0.10.5 checksums.txt lists
both darwin assets, so a normal install there is still verified. The gap
is an asset checksums.txt does not list.

- **Fix:** both installers now count the asset's lines in checksums.txt
  and refuse (`lists <asset> N times, not exactly once`) unless there is
  exactly one, then hand that one line to the checker. Zero lines and
  duplicated lines both refuse, whatever the checker does with them.
  `install_sandbox.sh` made the same assumption and gets the same check.
  The sha256sum-then-shasum preference is unchanged: with the count in
  the script, the checker's empty-input behavior no longer matters.
- **Tests:** a third bin directory whose `sha256sum` exits 0 on empty
  input and otherwise delegates to the real one (`_lenientSha256sum`),
  so the macOS behavior runs on Linux; new cases for an absent line under
  it and for a line listed twice, in both `TestInstallHost` and
  `TestInstallSandbox`, plus a listed asset and a mismatch under the
  lenient checker. The absent-line and twice cases installed (exit 0) on
  the unfixed scripts. The existing `no_checksum_line` cases are
  unchanged.
- **Approval:** the installers are the ask-first publish surface. The
  operator chose to fix them inside this PR (2026-09-28), verbatim, pt-BR:
  "vamos de 1 mesmo;".
- **Security re-audit follow-up (Low, closed here):** the count alone
  still left the line's shape to the checker, and a checker that skips
  lines it cannot parse and exits 0 would pass a malformed one (non-hex or
  short hash, tab or single-space separator, leading space). Both
  installers no longer use check mode (`-c`) at all: they require the one
  line to be exactly goreleaser's shape (64 lowercase hex digits, two
  spaces, the name), compute the archive's sha256 themselves
  (`sha256sum` or `shasum -a 256` on the file) and compare the two
  strings. An uppercase hash is refused rather than normalized: goreleaser
  writes lowercase, so any other shape is not what the release pipeline
  wrote. The lenient test checker now also skips lines it cannot parse;
  new cases cover each malformed shape (under it and under `shasum`), and
  a sandbox `linux_amd64` install. Under the same approval as above.

### Open

- `sbx env run --clone` may add an `origin` to the clone of an origin-less
  host repository. The sandbox's copy then keys by that origin while the
  host keys by path, and the two read different lists. Not measured;
  docker-sbx follow-up.
- The docker-sbx per-repository mount of the store must cover the
  location-derived key, for example `local/!o!s/repo`, not only
  `<host>/<owner>/<repo>` ones. docker-sbx follow-up.
- The GitHub-hosted matrix (both macOS legs, both Ubuntu 26.04 legs, the
  pinned labels in lint, security and release) has not run: it runs when
  the PR does. Until then those legs are unverified, including whether the
  shared packages' tests, which ran only on Linux before, pass on macOS.
- The docker-sbx environment files need `CANGA_SRC_DIR` set to the
  host's base (for example `/Users/bvenceslau/src`) for the sandbox build to
  key origin-less repositories, and any `CANGA_HOST_BASE_DIR` they set
  must be renamed, or every base-reading command refuses. That change
  belongs to docker-sbx.
- The kit's `agentInstructions` now describe the fallback, but the kit
  still pins v0.10.5, which does not have it. They become true when the pin
  moves to the first release carrying this change.
- An origin key can nest inside a location key's directory (ship-gate
  round 3, "Nested keys"). The structural fix renames the scope directory
  to a name no key segment can take (for example `@repo`, which
  `IsSafeSegment` refuses), which changes the store layout and needs a
  migration of every existing store, host and sandbox side. Deferred by
  the coordinator's decision, taken in ship-gate round 3; inert meanwhile.
- Round 4's darwin-only and folding-volume assertions
  (`TestHost_APFSUnreadableSpellingFailsClosed`, the folding branch of
  `TestCanonical`, and every new test's behavior on APFS and macOS's git)
  run only on the macOS legs.
- **`make test-host` hides a skip.** `go test -race -shuffle=on
  $(HOST_TEST_PKGS)` (`Makefile:153-155`) runs without `-v`, so a package
  that only passes and skips reports the same `ok` line as one that ran
  every test for real. The two real-APFS tests
  (`cmd/host/canga/apfs_darwin_test.go`) each `t.Skip`/`t.Skipf` when the
  condition they need is not met: `TestHost_APFSWrongCaseKeysAsStored`
  when `os.Stat` of the wrong-case path fails for any reason (the
  case-sensitive volume it is written for, but also any other Stat error,
  which it reports as a case-sensitive volume), and
  `TestHost_APFSUnreadableSpellingFailsClosed` on the same condition or
  when running as root. A `macos-26`/`macos-26-intel` Test
  workflow leg that lost its case-folding volume, or started running as
  root, would silently skip the exact tests meant to prove the fix on
  real APFS (docs/HANDOFF.md, "macOS case: measured, then fixed" and
  "Round 4: ship-gate round 2 findings") and the gate would still read
  green. Fix direction: run `test-host` with `-v` and grep/report `---
  SKIP` lines (or a `go test -json` pass that counts skips), and fail or
  warn on an unexpected skip of an APFS-only test on a runner that is
  supposed to be real APFS.
- **README by-hand blocks: `trap 'exit 1' INT TERM` is untested** (both
  "Install by hand, without the script" blocks, under "Install a release
  binary" and "Install it in a sandbox"; ship-gate round 3, F1, raised by
  test-engineer, code-reviewer Optional and security-auditor Low). Deleting
  it leaves `TestReadmeInstallBlocks` green, yet without it dash (TERM) and
  zsh (INT, TERM) leave the tempdir with the downloaded archive behind after
  a Ctrl-C.
  Fix: statically assert both blocks carry the trap, or better a case where
  the fake curl sleeps and the test sends INT and TERM, asserting a non-zero
  rc, a surviving caller and no `tmp.*` left behind.
- **HUP leaves the tempdir under dash** (F2, security-auditor Info): closing
  the terminal ends the block with rc=129 and the tempdir stays. Fix:
  `trap 'exit 1' HUP INT TERM` in both README by-hand blocks.
- **curl calls lack `--proto '=https' --proto-redir '=https'`** (F3,
  security-auditor Info): every `curl` in both README by-hand blocks, and
  `install_host.sh:81,160,161`, `install_sandbox.sh:142,143`. Change the scripts
  and the README together, in a separate PR.
- **The host (macOS) README block is not verified on real macOS in this
  PR's local gates** (F4, security-auditor Info): only Linux with
  `/usr/bin/shasum`, and `GOOS=darwin` vet and lint ran. The
  `macos-26`/`macos-26-intel` legs running `make test-host` are what cover
  it. Result: the first run (6b26aa2) failed both legs, 15 subtests each
  (the three matching cases across sh, bash, bash-posix, dash and
  zsh-interactive): `runFence` derived the block's directory from the test's
  scratch root (`TMPDIR=root`), but macOS's `mktemp -d` does not honour
  `TMPDIR` and made the directory under `/private/var/folders/.../T/`, so
  the assertion at `readme_install_test.go:248` failed. Fixed in efc67dc
  (the directory is read from tar's logged cwd, checked to be a `tmp.*`
  directory other than the caller's, and checked to be gone). The rerun on
  efc67dc passed every leg, `macos-26` and `macos-26-intel` included
  (run 36508280716). The `darwin_arm64` asset on the Intel leg is by design:
  the README block hardcodes it and tells an Intel Mac to write `darwin_amd64`.
  Debrief: the round-3 gate inferred from reading that `EvalSymlinks` handled
  `/private/var`; the symlink was not the cause (a symlinked `TMPDIR` on
  Linux passes the old test), and only a run on the real system, or a `mktemp`
  that drops `TMPDIR` on Linux, reproduces it. The test now keeps both as
  cases ("match with a mktemp that ignores TMPDIR", "match with the scratch
  tree behind a symlink"), so the local Linux gate covers the class.
- **The `zsh-interactive` subtest skips when zsh is absent**
  (`readme_install_test.go:391-392`, `:413-419`; F5, test-engineer optional), so
  on the Ubuntu legs the interactive-paste guard may never run. Accept, or
  install zsh on the Ubuntu leg.
- **`runFence` needs a comment on why `perl` is linked** (F8, code-reviewer
  Nit): added in this PR (`readme_install_test.go:160`); recorded here only
  so the ship-gate list is complete.
