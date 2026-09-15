// Package hooks installs a repository's own git hooks, the ones it keeps under
// .devctl/hooks, into the repository devctl was pointed at.
//
// The hooks live in the repository rather than in devctl, because a hook is a
// property of the project it guards: this repository's pre-commit runs its
// Makefile, and another project's would run something else. devctl only wires
// them up.
//
// That placement has a consequence worth stating plainly: installing these
// hooks means the repository's own tracked content runs on every commit. In a
// repository edited by an agent, an edit to .devctl/hooks is an edit to what
// executes on the machine doing the committing. Install them in repositories
// whose contents you would run anyway.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brunovenceslau/devctl/internal/repo"
)

// SourceDir is where a repository keeps the hooks devctl installs, relative to
// its top level. A forward-slash path because git's core.hooksPath wants one.
const SourceDir = ".devctl/hooks"

const (
	hooksDirPerm fs.FileMode = 0o755
	backupSuffix             = ".bak"
)

var (
	// ErrNoHooks reports a repository with nothing to install.
	ErrNoHooks = errors.New("no hooks to install")

	// ErrConflict reports something already in the way that devctl will not
	// replace on its own.
	ErrConflict = errors.New("refusing to replace what is already there")
)

// Options selects how the hooks are installed.
type Options struct {
	// Symlink links each hook into the git hooks directory instead of pointing
	// core.hooksPath at the source. It exists for a repository that already has
	// hooks of its own, which core.hooksPath would silently stop running.
	Symlink bool

	// Force replaces a conflicting setting, or a file in the way, instead of
	// refusing. A file is never deleted: it is renamed to <name>.bak first.
	Force bool
}

// Report says what Install did, so the caller can print it rather than guess.
type Report struct {
	Root      string   // the repository's top level
	Mode      string   // "core.hooksPath" or "symlink"
	Installed []string // hook names now in effect
	BackedUp  []string // paths moved aside, if any
	Warnings  []string // things that are now true and may surprise
}

// Install wires the repository containing dir up to its own hooks.
func Install(ctx context.Context, dir string, opts Options) (Report, error) {
	root, err := repo.Root(ctx, dir)
	if err != nil {
		return Report{}, err
	}

	names, err := hookNames(root)
	if err != nil {
		return Report{}, err
	}

	if opts.Symlink {
		return installLinks(ctx, root, names, opts)
	}

	return installHooksPath(ctx, root, names, opts)
}

// installHooksPath points git at the source directory. One setting covers every
// hook, now and later, and `git config --unset core.hooksPath` undoes it.
func installHooksPath(ctx context.Context, root string, names []string, opts Options) (Report, error) {
	report := Report{Root: root, Mode: "core.hooksPath", Installed: names}

	current, err := repo.Config(ctx, root, "core.hooksPath")
	if err != nil {
		return Report{}, err
	}

	if current != "" && current != SourceDir && !opts.Force {
		return Report{}, fmt.Errorf(
			"%w: core.hooksPath is already %q, pass --force to replace it", ErrConflict, current)
	}

	// The cost of this mode, stated rather than discovered: git reads hooks from
	// ONE directory, so anything already in the default one stops running.
	shadowed, err := existingHooks(ctx, root)
	if err != nil {
		return Report{}, err
	}

	if len(shadowed) > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf(
			"these hooks will stop running, because git reads only one hooks directory: %s"+
				" (use --symlink to keep them)", strings.Join(shadowed, ", ")))
	}

	if err := repo.SetConfig(ctx, root, "core.hooksPath", SourceDir); err != nil {
		return Report{}, err
	}

	return report, nil
}

// installLinks puts a symlink to each hook in the git hooks directory, so hooks
// the repository did not get from devctl keep working.
func installLinks(ctx context.Context, root string, names []string, opts Options) (Report, error) {
	report := Report{Root: root, Mode: "symlink"}

	commonDir, err := repo.CommonDir(ctx, root)
	if err != nil {
		return Report{}, err
	}

	hooksDir := filepath.Join(commonDir, "hooks")
	if err := os.MkdirAll(hooksDir, hooksDirPerm); err != nil {
		return Report{}, fmt.Errorf("create %s: %w", hooksDir, err)
	}

	for _, name := range names {
		source := filepath.Join(root, filepath.FromSlash(SourceDir), name)
		target := filepath.Join(hooksDir, name)

		// A RELATIVE link, so moving the repository does not break it. An
		// absolute one would point at wherever the repository used to be.
		relative, err := filepath.Rel(hooksDir, source)
		if err != nil {
			return Report{}, fmt.Errorf("link %s: %w", name, err)
		}

		backup, err := clearTarget(target, relative, opts.Force)
		if err != nil {
			return Report{}, err
		}

		if backup != "" {
			report.BackedUp = append(report.BackedUp, backup)
		}

		if err := os.Symlink(relative, target); err != nil {
			if errors.Is(err, fs.ErrExist) {
				// Already the link we wanted; clearTarget left it alone.
				report.Installed = append(report.Installed, name)

				continue
			}

			return Report{}, fmt.Errorf("link %s: %w", name, err)
		}

		report.Installed = append(report.Installed, name)
	}

	return report, nil
}

// clearTarget makes room for a symlink, and reports the backup it made.
//
// Nothing is ever deleted. A file in the way is RENAMED to <name>.bak, and an
// existing .bak is left alone rather than overwritten: the first backup is the
// pristine one, and losing it to a second run would be the one unrecoverable
// mistake this function could make.
func clearTarget(target, want string, force bool) (string, error) {
	existing, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", target, err)
	}

	if existing.Mode()&fs.ModeSymlink != 0 {
		if current, err := os.Readlink(target); err == nil && current == want {
			return "", nil // already ours
		}
	}

	if !force {
		return "", fmt.Errorf("%w: %s exists, pass --force to move it aside", ErrConflict, target)
	}

	backup := target + backupSuffix
	if _, err := os.Lstat(backup); err == nil {
		return "", fmt.Errorf(
			"%w: %s already exists, move it out of the way first", ErrConflict, backup)
	}

	if err := os.Rename(target, backup); err != nil {
		return "", fmt.Errorf("back up %s: %w", target, err)
	}

	return backup, nil
}

// hookNames lists the repository's installable hooks: executable regular files,
// in name order. A non-executable file is skipped rather than installed,
// because git would silently never run it.
func hookNames(root string) ([]string, error) {
	dir := filepath.Join(root, filepath.FromSlash(SourceDir))

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s does not exist", ErrNoHooks, dir)
		}

		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}

		names = append(names, entry.Name())
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("%w: %s holds no executable file", ErrNoHooks, dir)
	}

	sort.Strings(names)

	return names, nil
}

// existingHooks lists hooks already in git's default hooks directory, ignoring
// the .sample files git itself ships.
func existingHooks(ctx context.Context, root string) ([]string, error) {
	commonDir, err := repo.CommonDir(ctx, root)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(filepath.Join(commonDir, "hooks"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("read the git hooks directory: %w", err)
	}

	var found []string

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || strings.HasSuffix(entry.Name(), ".sample") {
			continue
		}

		if info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			found = append(found, entry.Name())
		}
	}

	return found, nil
}
