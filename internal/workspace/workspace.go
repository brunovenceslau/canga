// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package workspace opens a repository beside the sandbox environment that
// runs agents on it, as one workspace of a terminal multiplexer.
//
// The two live apart on purpose: an environment file kept inside the tree it
// edits is one an agent in that sandbox could rewrite. Both are clones under
// the same base directory `canga git clone` uses, and both paths come from the
// repository's URL: the repository's own clone, and the directory for its
// <host>/<owner>/<repo> tail under the envs/ directory of the repository that
// holds the environments. Opening the pair takes the URL and nothing else.
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/brunovenceslau/canga/internal/repo"
)

// EnvsRepoVar names the repository that holds the sandbox environments, as its
// path under the clone base, for example "github.com/acme/docker-sbx".
//
// Unset, it is the docker-sbx repository of the same owner as the repository
// being opened, <host>/<owner>/docker-sbx. Either way it is a clone like any
// other, so every git source stays under the one base directory and there is
// no second root to configure.
const EnvsRepoVar = "CANGA_HOST_ENVS_REPO"

const (
	// defaultEnvsRepoName is the repository name EnvsRepoVar defaults to,
	// under the opened repository's own owner.
	defaultEnvsRepoName = "docker-sbx"

	// envsSubdir is the directory, inside the environments' repository, that
	// holds one <host>/<owner>/<repo> directory per environment.
	envsSubdir = "envs"
)

var (
	// ErrBadEnvsRepo reports an EnvsRepoVar that is not a relative path under
	// the clone base. It is a usage error: the command cannot work until the
	// variable is fixed, and retrying changes nothing.
	ErrBadEnvsRepo = errors.New(EnvsRepoVar + " must be a path under the clone base")

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

	envsRepo, err := envsRepoPath(tail)
	if err != nil {
		return Target{}, err
	}

	base, err := repo.BaseDir()
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
	// and envsRepoPath have already refused "." and "..", which keeps this
	// join inside base.
	target := Target{
		Name: name,
		EnvDir: filepath.Join(base, filepath.FromSlash(envsRepo), envsSubdir,
			filepath.FromSlash(tail)),
		RepoDir: repoDir,
	}

	// The hint does not repeat the URL: echoing it raw could print a
	// credential, and repo.Redact drops the user from an scp-style URL, which
	// would turn the hint into a command that clones as someone else.
	if err := requireDir(target.RepoDir); err != nil {
		return Target{}, fmt.Errorf("%w: %w (clone it with `canga git clone <url>`)",
			ErrNotCloned, err)
	}

	// The hint lists the ways this happens, most likely first: the
	// environments' repository is not cloned, its clone predates the
	// repository's environment, the environment was never created, or the
	// environments live in another repository.
	if err := requireDir(target.EnvDir); err != nil {
		return Target{}, fmt.Errorf("%w: %w (clone the repository that holds the "+
			"environments with `canga git clone <url>`, update it with `canga git "+
			"sync`, create the directory, or set %s to that repository's path "+
			"under the clone base, such as github.com/<owner>/%s)",
			ErrNoEnv, err, EnvsRepoVar, defaultEnvsRepoName)
	}

	return target, nil
}

// envsRepoPath is the slash-separated path, under the clone base, of the
// repository that holds the environments for the repository whose layout tail
// is tail.
//
// A set EnvsRepoVar must be relative and name real segments: an absolute path
// or a ".." would take the environment pane outside the clone base, which is
// the one place this command promises to stay in.
func envsRepoPath(tail string) (string, error) {
	value := strings.TrimRight(os.Getenv(EnvsRepoVar), "/")
	if value == "" {
		// repo.Path yields "<host>/<owner>.../<repo>"; the environments'
		// repository sits beside the opened one, under the same owner.
		return path.Join(path.Dir(tail), defaultEnvsRepoName), nil
	}

	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("%w: %q is absolute; use a path such as "+
			"github.com/<owner>/%s", ErrBadEnvsRepo, value, defaultEnvsRepoName)
	}

	for segment := range strings.SplitSeq(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: %q has an empty, \".\" or \"..\" segment",
				ErrBadEnvsRepo, value)
		}
	}

	return value, nil
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
