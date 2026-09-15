# devctl

A developer control tool for the repositories and sandboxes of a working day.

Every subcommand is keyed by the repository the current directory belongs to,
derived from its `origin` remote.

This commit is the project scaffold: the module, the gates, and the command
tree's root. The subcommands land on top of it.

## Install

```sh
go install github.com/brunovenceslau/devctl/cmd/devctl@latest
```

Or download a release archive for `darwin`/`linux` on `amd64`/`arm64` from the
[releases page](https://github.com/brunovenceslau/devctl/releases).

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | success |
| `1` | a runtime failure |
| `2` | a bad invocation |

## Development

```sh
make tools   # install the pinned golangci-lint and govulncheck
make ci      # lint + test + govulncheck — must be green before a push
```

Every gate is a `make` target, and CI invokes the target rather than restating
it, so what CI runs is what a push was checked against locally.

## License

MIT. See [LICENSE](LICENSE).
