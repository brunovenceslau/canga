// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package workspace opens a repository beside the sandbox environment that
// runs agents on it, as one workspace of a terminal multiplexer.
//
// The two live apart on purpose: an environment file kept inside the tree it
// edits is one an agent in that sandbox could rewrite. Both paths are derived
// from the repository's URL, the clone by the same layout `canga git clone`
// uses and the environment by the same <host>/<owner>/<repo> tail under a root
// only the host knows, so opening the pair takes the URL and nothing else.
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/brunovenceslau/canga/internal/repo"
)

// EnvsDirVar names the root the sandbox environments hang under.
//
// It is host-only and has no default. The environments live in whatever
// repository the user keeps them in, checked out wherever they chose, so any
// guess would be someone else's layout, and a wrong guess would open a pane in
// a directory that merely happens to exist.
const EnvsDirVar = "CANGA_HOST_ENVS_DIR"

var (
	// ErrNoEnvsDir reports that EnvsDirVar is unset. It is a usage error: the
	// command cannot work until the variable is set, and retrying changes
	// nothing.
	ErrNoEnvsDir = errors.New(EnvsDirVar + " is not set")

	// ErrNotCloned reports a repository with no clone at the deterministic
	// path. It is refused rather than cloned on the spot, so opening a
	// workspace never fetches anything.
	ErrNotCloned = errors.New("repository is not cloned")

	// ErrNoEnv reports a repository with no environment directory under the
	// envs root.
	ErrNoEnv = errors.New("no sandbox environment for the repository")
)

// Target is what a workspace opens: a name for it and the two directories its
// panes start in, both absolute.
type Target struct {
	// Name is the path after the host, "<owner>/<repo>" or, for a nested
	// group, every group down to the repo, which is what tells two workspaces
	// apart in a sidebar. The host is left out: it is github.com for nearly
	// everything.
	Name string

	// EnvDir holds the repository's sandbox environment.
	EnvDir string

	// RepoDir is the repository's clone.
	RepoDir string
}

// Resolve derives the Target for a repository URL, and refuses unless both of
// its directories already exist: a pane opened on a missing directory starts
// somewhere else without saying so.
func Resolve(url string) (Target, error) {
	tail, err := repo.Path(url)
	if err != nil {
		return Target{}, err
	}

	root, err := envsDir()
	if err != nil {
		return Target{}, err
	}

	repoDir, err := repo.TargetDir(url)
	if err != nil {
		return Target{}, err
	}

	// repo.Path always yields "<host>/" plus at least two segments.
	_, name, _ := strings.Cut(tail, "/")

	// The tail keeps its case, as it does for the clone: the environments are
	// laid out by the same readable <host>/<owner>/<repo> spelling. repo.Path
	// has already refused "." and "..", which keeps this join inside root.
	target := Target{
		Name:    name,
		EnvDir:  filepath.Join(root, filepath.FromSlash(tail)),
		RepoDir: repoDir,
	}

	// The hint does not repeat the URL: echoing it raw could print a
	// credential, and repo.Redact drops the user from an scp-style URL, which
	// would turn the hint into a command that clones as someone else.
	if err := requireDir(target.RepoDir); err != nil {
		return Target{}, fmt.Errorf("%w: %w (clone it with `canga git clone <url>`)",
			ErrNotCloned, err)
	}

	if err := requireDir(target.EnvDir); err != nil {
		return Target{}, fmt.Errorf("%w: %w", ErrNoEnv, err)
	}

	return target, nil
}

// envsDir resolves EnvsDirVar to an absolute path. Absolute because the panes
// start there, and a terminal multiplexer resolves a relative directory against
// its own idea of where it is, not canga's.
func envsDir() (string, error) {
	dir := os.Getenv(EnvsDirVar)
	if dir == "" {
		return "", fmt.Errorf("%w: point it at the directory holding the "+
			"<host>/<owner>/<repo> environment directories", ErrNoEnvsDir)
	}

	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %s %s: %w", EnvsDirVar, dir, err)
	}

	return absolute, nil
}

// errNotADirectory reports a path that exists but cannot hold a pane.
var errNotADirectory = errors.New("not a directory")

// requireDir fails unless dir is an existing directory. Every error names dir,
// so the caller's message says which path to create.
func requireDir(dir string) error {
	info, err := os.Stat(dir)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s does not exist", dir)
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("%s: %w", dir, errNotADirectory)
	}

	return nil
}
