// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// The environment variables that override what the machine's global git config
// says. Naming the key explicitly is the predictable answer: it is never derived
// from the ssh-agent or from the repository being cloned.
const (
	envSigningKey     = "DEVCTL_SIGNING_KEY"
	envAllowedSigners = "DEVCTL_ALLOWED_SIGNERS"
)

// Signing reports what StampSigning wrote into a repository's local config.
type Signing struct {
	// AllowedSigners is the allowed-signers file wired into the repository, or
	// empty when none resolved and none was wired.
	AllowedSigners string

	// Key is the signing key commit and tag signing were turned on with, or
	// empty when none resolved — in which case nothing was stamped.
	Key string

	// Inherited reports a repository that signs WITHOUT anything stamped,
	// because git configuration outside it already turns commit.gpgsign on.
	// That is a sandbox: /etc/gitconfig carries the format, the flag and a key
	// command, and no user.signingkey for the fallback to find.
	Inherited bool
}

// On reports whether the repository was left signing its commits.
func (s Signing) On() bool { return s.Key != "" || s.Inherited }

// StampSigning writes SSH signing configuration into a repository's LOCAL
// config, and reports what it wrote.
//
// It exists because the HOST has no system-level signing configuration, so a
// plain clone there signs nothing. Inside a sandbox /etc/gitconfig already
// carries it and this is redundant — which is not a reason to delete it, since
// the machine that needs it is the one where the clone is made by hand.
//
// A clone where neither value resolves is left ALONE rather than pointed at a
// file that does not exist: a repository configured to verify against a missing
// allowed-signers file fails every `git log --show-signature`, which is worse
// than not being configured at all.
func StampSigning(ctx context.Context, dir string) (Signing, error) {
	var stamped Signing

	allowed, err := resolveSetting(ctx, envAllowedSigners, "gpg.ssh.allowedSignersFile")
	if err != nil {
		return stamped, err
	}

	if allowed != "" {
		if err := SetConfig(ctx, dir, "gpg.ssh.allowedSignersFile", allowed); err != nil {
			return stamped, err
		}

		stamped.AllowedSigners = allowed
	}

	key, err := resolveSetting(ctx, envSigningKey, "user.signingkey")
	if err != nil {
		return stamped, err
	}

	if key == "" {
		// Nothing to stamp, which is not the same as not signing. Asked of the
		// new repository itself, so the answer is the effective value git will
		// obey; it has no local setting yet for that to be confused with.
		stamped.Inherited, err = signsCommits(ctx, dir)

		return stamped, err
	}

	// The four writes are one decision: a key without commit.gpgsign leaves the
	// clone configured to sign and not signing. Written in a fixed order, so a
	// half-written config after a failure is always the same half.
	//
	// gpg.format is written rather than inherited because inheriting it is what
	// made this break outside the dotfiles framework: git's default format is
	// openpgp, so a machine that does not set gpg.format=ssh globally answers an
	// SSH key with `gpg: skipped "…": No secret key` on every commit. This
	// configuration is SSH signing by definition — it is what the key and the
	// allowed-signers file are — so the clone says so instead of depending on
	// the machine to have said it. The cost is deliberate: a machine that signs
	// with GPG must not hand its key over through DEVCTL_SIGNING_KEY or a global
	// user.signingkey, because the clone will be configured for SSH regardless.
	settings := [][2]string{
		{"gpg.format", "ssh"},
		{"user.signingkey", key},
		{"commit.gpgsign", "true"},
		{"tag.gpgsign", "true"},
	}

	for _, setting := range settings {
		if err := SetConfig(ctx, dir, setting[0], setting[1]); err != nil {
			return stamped, err
		}
	}

	stamped.Key = key

	return stamped, nil
}

// resolveSetting reads a signing setting from the environment, falling back to
// the machine's GLOBAL git config. See GlobalConfig for why the fallback is
// global-scoped rather than effective.
func resolveSetting(ctx context.Context, env, key string) (string, error) {
	if value := os.Getenv(env); value != "" {
		return value, nil
	}

	value, err := GlobalConfig(ctx, key)
	if err != nil {
		return "", fmt.Errorf("resolving the signing configuration: %w", err)
	}

	return value, nil
}

// signsCommits reports whether commit.gpgsign is effectively true in dir, from
// any scope. --type=bool normalizes yes, on and 1, which git accepts as true.
func signsCommits(ctx context.Context, dir string) (bool, error) {
	out, err := gitCommand(ctx, dir, nil,
		[]string{"config", "--type=bool", "--get", "commit.gpgsign"}).Output()
	if err == nil {
		return string(out) == "true\n", nil
	}

	// Exit 1 is git's "no such key", which means signing is off.
	if exit, ran := errors.AsType[*exec.ExitError](err); ran && exit.ExitCode() == 1 &&
		ctx.Err() == nil {
		return false, nil
	}

	return false, classify(ctx, dir, ErrGitRefused, err)
}
