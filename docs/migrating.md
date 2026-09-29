<!--
SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
SPDX-License-Identifier: GPL-3.0-or-later
-->

# Migrating from older setups

What changed across canga's history, and what to do if a machine or a
sandbox still runs an older setup. A fresh install needs none of this; start
at the [README](../README.md).

- [Moving from devctl and agtctl](#moving-from-devctl-and-agtctl)
- [Environment directories without the `-env` suffix](#environment-directories-without-the--env-suffix)
- [Sandbox builds without `canga upgrade`](#sandbox-builds-without-canga-upgrade)

## Moving from devctl and agtctl

canga was two binaries, `devctl` and `agtctl`, and up to v0.5.0 its git
commands sat at the top level. What changed, and what you do:

| Before | Now | What to do |
| --- | --- | --- |
| `devctl`, `agtctl` | `canga` (host build, sandbox build) | Install the host build once by hand; `devctl upgrade` does not select `canga-host_` archives. |
| `${XDG_DATA_HOME}/devctl/reminders` | `${XDG_DATA_HOME}/canga/reminders` | Nothing, if you run any `canga reminders` command on the host before starting a sandbox that mounts the new path: that command moves the store in one rename and says so on stderr. |
| `DEVCTL_REMINDERS_DIR` | `CANGA_REMINDERS_DIR` | Rename it in each sandbox environment file, together with the mount path. The old name is not read. |
| `DEVCTL_BASE_DIR`, `DEVCTL_SIGNING_KEY`, `DEVCTL_ALLOWED_SIGNERS` | `CANGA_SRC_DIR`, `CANGA_HOST_SIGNING_KEY`, `CANGA_HOST_ALLOWED_SIGNERS` | Rename them wherever you set them. The old names are not read. Its first successor, `CANGA_HOST_BASE_DIR`, is itself retired (see its row below); go straight to `CANGA_SRC_DIR`. |
| `canga-host_` archives for linux | none: the host build is published for darwin only | Nothing on a Mac. On Linux, which only runs the sandbox build, use `install_sandbox.sh`; `install_host.sh` and the host build's `canga upgrade` refuse there. |
| `CANGA_HOST_BASE_DIR` | `CANGA_SRC_DIR` | Rename it wherever you set it, sandbox environment files included. While the old name is set, every command that reads the clone base refuses with exit `2` and names the new one, rather than fall back to `$HOME/src`. |
| `canga clone`, `canga sync`, `canga setup hooks` (v0.5.0) | `canga git clone`, `canga git sync`, `canga git setup-hooks` | Use the new names wherever you call them. The old names were removed, not aliased, and fail with a usage error (exit `2`). |
| `.devctl/hooks` | `.canga/hooks` | Move the directory and run `canga git setup-hooks`. It replaces its own earlier setup without `--force`: a `core.hooksPath` of `.devctl/hooks`, or, with `--symlink`, links into `.devctl/hooks`. |

The store moves only when the old directory exists, the new one does not,
and `CANGA_REMINDERS_DIR` is unset. If the rename fails, the command stops
rather than start an empty store beside the old one. Shell completion never
moves it.

If the new directory already exists, for example because a sandbox that
mounts it was created first, nothing is moved: renaming over a mounted
directory would swap it out from under the running sandbox. Every
`canga reminders` command then names both directories on stderr until you
move each repository's items across by hand and remove the old directory.

## Environment directories without the `-env` suffix

`canga workspace` looks for a repository's environment at
`envs/<host>/<owner>/<repo>-env` (see
[Where the environment is found](design.md#where-the-environment-is-found)).
The `-env` suffix is a breaking change for a `docker-sbx` clone created
before it. For each existing environment directory under
`envs/<host>/<owner>/<repo>`, rename its leaf segment to add the suffix, then
commit the rename:

```sh
git -C ~/src/github.com/acme/docker-sbx mv envs/github.com/acme/widget envs/github.com/acme/widget-env
```

## Sandbox builds without `canga upgrade`

Sandbox builds up to v0.6.0 do not have `canga upgrade`. They answer it with
`upgrade is available in the host build only, not in the sandbox build`.
Move those sandboxes to a release that has it by changing the pin, or by
running `install_sandbox.sh` again. Only v0.10.5 onward is published, so
that is the oldest release either can install (see
[The release floor](design.md#the-release-floor)).
