// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"
	"fmt"
	"os"
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
	// empty when none resolved — in which case signing was left off.
	Key string
}

// On reports whether the repository was left signing its commits.
func (s Signing) On() bool { return s.Key != "" }

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
		return stamped, nil
	}

	// The three writes are one decision: a key without commit.gpgsign leaves the
	// clone configured to sign and not signing. Written in a fixed order, so a
	// half-written config after a failure is always the same half.
	settings := [][2]string{
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
