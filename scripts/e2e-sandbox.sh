#!/bin/sh
# SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
# SPDX-License-Identifier: GPL-3.0-or-later

# e2e-sandbox.sh - drive a built sandbox canga the way an agent in an sbx
# sandbox does, against a store laid out the way the host shares it:
#
#   scripts/e2e-sandbox.sh bin/sandbox/canga
#
# HOME is the sandbox's own, repositories sit at their HOST paths, and the
# environment file hands over CANGA_REMINDERS_DIR and CANGA_SRC_DIR. Every
# check prints one line; the first failure stops the run with exit 1.

main() {
	set -eu

	if [ $# -ne 1 ]; then
		echo "usage: e2e-sandbox.sh <path to the sandbox canga>" >&2
		exit 2
	fi

	canga=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT

	host_src="$work/Users/someone/src"
	store="$work/Users/someone/.local/share/canga/reminders"
	mkdir -p "$host_src/github.com/acme" "$host_src/local" "$work/home/agent" "$store"

	export HOME="$work/home/agent"
	export CANGA_REMINDERS_DIR="$store"
	export CANGA_SRC_DIR="$host_src"
	export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
	unset CANGA_HOST_BASE_DIR

	git init -q -b main "$host_src/github.com/acme/widget"
	git -C "$host_src/github.com/acme/widget" remote add origin https://github.com/acme/widget.git
	git init -q -b main "$host_src/local/OS"
	git init -q -b main "$work/outside"

	"$canga" --version | grep -q ' (sandbox, ' || fail "--version does not name the sandbox build"
	pass "--version names the sandbox build"

	id=$("$canga" -C "$host_src/github.com/acme/widget" reminders add origin keyed)
	"$canga" -C "$host_src/github.com/acme/widget" reminders list | grep -q "^$id	origin keyed\$" ||
		fail "an origin-keyed reminder does not list"
	[ -f "$store/github.com/acme/widget/repo/items/$id.md" ] || fail "the origin key is not github.com/acme/widget"
	pass "origin present: keyed by github.com/acme/widget"

	id=$(cd "$host_src/local/OS" && "$canga" reminders add path keyed)
	[ -f "$store/local/!o!s/repo/items/$id.md" ] || fail "the origin-less key is not local/OS, escaped as local/!o!s"
	pass "no origin: keyed by its path below CANGA_SRC_DIR, local/OS"

	expect_usage "rm is host-only" "host build only" "$canga" reminders rm "$id"
	expect_usage "outside CANGA_SRC_DIR" "is outside it" "$canga" -C "$work/outside" reminders list
	expect_usage "CANGA_SRC_DIR unset" "CANGA_SRC_DIR is empty or unset" \
		env CANGA_SRC_DIR= "$canga" -C "$host_src/local/OS" reminders list
	expect_usage "CANGA_HOST_BASE_DIR still set" "was renamed to CANGA_SRC_DIR" \
		env CANGA_HOST_BASE_DIR="$host_src" "$canga" -C "$host_src/local/OS" reminders list

	# The auditor's PoC 11: an intermediate key directory replaced with a link.
	mkdir -p "$work/hostpath"
	ln -s "$work/hostpath" "$store/github.com/acme/other"
	git init -q -b main "$work/other"
	git -C "$work/other" remote add origin https://github.com/acme/other/sub.git
	if "$canga" -C "$work/other" reminders add escaped >/dev/null 2>&1; then
		fail "an add through a link planted in the store succeeded"
	fi
	[ -z "$(ls -A "$work/hostpath")" ] || fail "an add wrote through a link planted in the store"
	pass "a link planted below the store root is refused, nothing written"

	# Ship-gate round 2: a FIFO planted as an item hung list. A watchdog
	# kills a hung list, so a hang fails the gate instead of stalling it
	# (timeout(1) is not on a stock Mac, where make ci runs this too).
	mkfifo "$store/local/!o!s/repo/items/20260101T000000.000000Z-00000000.md"
	(cd "$host_src/local/OS" && exec "$canga" reminders list) >"$work/listed" 2>&1 &
	lister=$!
	# Polls rather than sleeping 20s, so it ends with the list instead of
	# holding the gate's output open after it.
	(
		ticks=0
		while [ "$ticks" -lt 200 ] && kill -0 "$lister" 2>/dev/null; do
			sleep 0.1
			ticks=$((ticks + 1))
		done
		# KILL: canga turns TERM and INT into a cancellation, which a read
		# blocked in the kernel never sees.
		kill -9 "$lister" 2>/dev/null
	) >/dev/null 2>&1 &
	status=0
	wait "$lister" || status=$?
	[ "$status" -eq 0 ] || fail "list with a FIFO planted in items/ failed or hung (exit $status): $(cat "$work/listed")"
	grep -q "path keyed\$" "$work/listed" || fail "list with a FIFO planted in items/ lost the real item"
	pass "a FIFO planted as an item is skipped, not waited on"

	echo "e2e-sandbox: all checks passed"
}

# expect_usage runs the rest of its arguments as one command and requires
# exit 2 with the given text on stderr, so each refusal is the one meant.
expect_usage() {
	what=$1
	says=$2
	shift 2
	status=0
	"$@" >/dev/null 2>"$work/stderr" || status=$?
	[ "$status" -eq 2 ] || fail "$what: exit $status, want 2: $(cat "$work/stderr")"
	grep -qF -- "$says" "$work/stderr" || fail "$what: stderr lacks \"$says\": $(cat "$work/stderr")"
	pass "$what: exit 2"
}

pass() { echo "ok   $1"; }

fail() {
	echo "FAIL $1" >&2
	exit 1
}

main "$@"
