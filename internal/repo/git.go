// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// transportFlags returns the options that make the transport and object-integrity guarantees of a clone
// or a fetch SELF-CONTAINED instead of borrowed from whatever git configuration
// the machine happens to carry.
//
// They are passed as command-line `-c` options, and that is the whole point: a
// -c option overrides even a config file setting `protocol.ext.allow=always`, so
// `devctl clone ext::…` can never reach the remote-helper transport — which
// executes a shell command — regardless of how the host is configured. Only the
// documented schemes (ssh, https, scp-like) are meant to travel. `file` is
// deliberately left at git's default (allowed) so local-path clones still work.
//
// fsckObjects on both sides makes a fetch refuse a malformed or malicious object
// graph rather than writing it into the object store first.
//
// Ported verbatim from dotfiles-host's zsh/dev.zsh `_dev_git_safe`. The list is
// on that repo's ask-first security list: change it with the user, not in
// passing.
//
// A function rather than a package variable, so no caller can append to or
// overwrite the security list: each call builds a fresh slice.
func transportFlags() []string {
	return []string{
		"-c", "protocol.ext.allow=never",
		"-c", "protocol.fd.allow=never",
		"-c", "transfer.fsckObjects=true",
		"-c", "fetch.fsckObjects=true",
	}
}

// git runs one git command in dir and returns its trimmed stdout.
//
// refused is the sentinel to report when git RAN and refused, which is the case
// that means the caller pointed devctl somewhere wrong. A missing binary or a
// cancelled context are classified separately; see classify.
func git(ctx context.Context, dir string, refused error, args ...string) (string, error) {
	return gitWith(ctx, dir, nil, refused, args)
}

// gitWith is git, with git-level `-c` options in front of the subcommand.
func gitWith(
	ctx context.Context,
	dir string,
	flags []string,
	refused error,
	args []string,
) (string, error) {
	//nolint:gosec // the program name is a constant and every argument is passed
	// separately, so no shell ever parses dir — which is the whole point of not
	// building a command string.
	out, err := exec.CommandContext(ctx, "git", gitArgs(dir, flags, args)...).Output()
	if err != nil {
		return "", classify(ctx, dir, refused, err)
	}

	return strings.TrimSpace(string(out)), nil
}

// streamGit runs one git command with its output wired to the caller's writers,
// for the one operation whose progress meter IS the user's only feedback.
//
// It reports a refusal without repeating what git said, because git said it
// straight to the user's terminal: capturing that output to re-render it would
// print the same diagnostic twice, and hiding it would leave a clone that can
// take minutes looking like it had nothing to say.
func streamGit(
	ctx context.Context,
	dir string,
	flags []string,
	out, errOut io.Writer,
	args ...string,
) error {
	//nolint:gosec // as in gitWith: constant program name, separate arguments.
	cmd := exec.CommandContext(ctx, "git", gitArgs(dir, flags, args)...)
	cmd.Stdout = out
	cmd.Stderr = errOut

	if err := cmd.Run(); err != nil {
		return classifyStream(ctx, err, args)
	}

	return nil
}

// gitArgs renders git's own argument list: -C first, then any -c options, then
// the subcommand and its arguments.
//
// The order is git's, not a preference — a `-c` after the subcommand is an
// argument OF the subcommand, so the hardening would be silently handed to
// `clone` as a path. An empty dir omits -C entirely, which is what clone needs:
// it is the one operation with no repository to run in yet.
func gitArgs(dir string, flags, args []string) []string {
	full := make([]string, 0, 2+len(flags)+len(args))

	if dir != "" {
		full = append(full, "-C", dir)
	}

	full = append(full, flags...)

	return append(full, args...)
}

// classify says what actually went wrong when git could not be asked.
//
// Collapsing every failure into ErrNoOrigin would answer a missing git binary,
// or a Ctrl-C, with "no origin remote in <dir>" — which is not just misleading
// but the wrong exit code, since those are runtime failures rather than the
// caller having pointed devctl at the wrong directory.
func classify(ctx context.Context, dir string, refused, err error) error {
	// Operation-neutral: classify is shared by every git call, and naming one
	// of them would report an operation that never ran. A Ctrl-C during
	// `devctl setup hooks` used to say "reading the origin".
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("running git in %s: %w", dir, ctxErr)
	}

	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%w, so no repository can be identified", ErrGitMissing)
	}

	// git ran and refused: not a repository, no such remote, or a repository it
	// will not read. All three are fixed by pointing devctl somewhere else or by
	// changing git's configuration, never by running the same command again.
	if exit, ran := errors.AsType[*exec.ExitError](err); ran {
		return fmt.Errorf("%w: %s%s", refused, dir, gitSaid(exit))
	}

	return fmt.Errorf("running git in %s: %w", dir, err)
}

// classifyStream is classify for a command that printed its own diagnostic.
func classifyStream(ctx context.Context, err error, args []string) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("running git %s: %w", args[0], ctxErr)
	}

	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%w, so nothing can be cloned", ErrGitMissing)
	}

	if exit, ran := errors.AsType[*exec.ExitError](err); ran {
		return fmt.Errorf("git %s exited %d", args[0], exit.ExitCode())
	}

	return fmt.Errorf("running git %s: %w", args[0], err)
}

// gitSaid renders the first line of git's own diagnostic, which Output()
// captures into Stderr and would otherwise discard.
//
// It matters most for the arrangement this tool exists for: a host repository
// mounted into a sandbox trips git's dubious-ownership check, and without git's
// own words the user is told only "no origin remote" and sent looking for a
// remote that was never the problem. Only the first line travels, so a long
// hint block cannot bury the command's own message.
func gitSaid(exit *exec.ExitError) string {
	first, _, _ := strings.Cut(strings.TrimSpace(string(exit.Stderr)), "\n")
	if first == "" {
		return ""
	}

	return ": " + first
}

// gitStatus runs a git command that ANSWERS with its exit status, and reports
// whether it exited zero.
//
// It exists for the queries where a non-zero status is a legitimate answer
// rather than a failure — `merge-base --is-ancestor` being the one that matters
// here. Collapsing the two is what lets a cancelled context read as "no", which
// is a wrong answer delivered confidently.
func gitStatus(ctx context.Context, dir string, args ...string) (bool, error) {
	//nolint:gosec // as in gitWith: constant program name, separate arguments.
	err := exec.CommandContext(ctx, "git", gitArgs(dir, nil, args)...).Run()
	if err == nil {
		return true, nil
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, fmt.Errorf("running git %s in %s: %w", args[0], dir, ctxErr)
	}

	// Exit 1 is the "no" this is asked for. Anything else is git failing to
	// answer at all, which is not a no.
	if exit, ran := errors.AsType[*exec.ExitError](err); ran && exit.ExitCode() == 1 {
		return false, nil
	}

	return false, classify(ctx, dir, ErrGitRefused, err)
}
