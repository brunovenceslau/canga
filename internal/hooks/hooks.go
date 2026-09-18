// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package hooks installs a repository's own git hooks, the ones it keeps under
// .canga/hooks, into the repository canga was pointed at.
//
// The hooks live in the repository rather than in canga, because a hook is a
// property of the project it guards: this repository's pre-commit runs its
// Makefile, and another project's would run something else. canga only wires
// them up.
//
// That placement has a consequence worth stating plainly: installing these
// hooks means the repository's own tracked content runs on every commit. In a
// repository edited by an agent, an edit to .canga/hooks is an edit to what
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

	"github.com/brunovenceslau/canga/internal/repo"
)

// SourceDir is where a repository keeps the hooks canga installs, relative to
// its top level. A forward-slash path because git's core.hooksPath wants one.
const SourceDir = ".canga/hooks"

// legacySourceDir is where those hooks lived before the tool was named canga.
// A core.hooksPath still pointing there was set by this command, so replacing
// it is not the conflict --force exists to confirm.
const legacySourceDir = ".devctl/hooks"

const (
	hooksDirPerm fs.FileMode = 0o755
	backupSuffix             = ".bak"
)

var (
	// ErrNoHooks reports a repository with nothing to install.
	ErrNoHooks = errors.New("no hooks to install")

	// ErrConflict reports something already in the way that canga will not
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
//
// It is returned ALONGSIDE an error too, not only on success: a run that moved
// a file aside and then failed has already changed the repository, and saying
// nothing about it is the worst of both outcomes.
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

	current, set, err := repo.Config(ctx, root, "core.hooksPath")
	if err != nil {
		return Report{}, err
	}

	if set && current != SourceDir && current != legacySourceDir && !opts.Force {
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
// the repository did not get from canga keep working.
func installLinks(ctx context.Context, root string, names []string, opts Options) (Report, error) {
	report := Report{Root: root, Mode: "symlink"}

	commonDir, err := repo.CommonDir(ctx, root)
	if err != nil {
		return Report{}, err
	}

	hooksDir := filepath.Join(commonDir, "hooks")

	// git reads hooks from ONE directory. If core.hooksPath points somewhere
	// other than the directory these links go into, git never looks at them and
	// installing would report success for hooks that can never run.
	//
	// The test is where it points, not whether it is set. `core.hooksPath =
	// .git/hooks` is the standard way to neutralise a GLOBAL setting, and those
	// hooks do run; refusing it would reject the very fix for the problem this
	// check is about. The empty string is caught by the same comparison, since
	// it resolves to the repository root rather than to the hooks directory,
	// and git reads no hooks at all with it.
	if pointsElsewhere, err := hooksPathElsewhere(ctx, root, hooksDir); err != nil {
		return Report{}, err
	} else if pointsElsewhere != "" {
		if !opts.Force {
			return Report{}, fmt.Errorf(
				"%w: core.hooksPath is %s, so git ignores %s where these links go;"+
					" point it at that directory, install without --symlink, or pass --force",
				ErrConflict, pointsElsewhere, hooksDir)
		}

		report.Warnings = append(report.Warnings, fmt.Sprintf(
			"core.hooksPath is %s, so git will ignore these links until it points at %s",
			pointsElsewhere, hooksDir))
	}

	if err := os.MkdirAll(hooksDir, hooksDirPerm); err != nil {
		return report, fmt.Errorf("create %s: %w", hooksDir, err)
	}

	for _, name := range names {
		source := filepath.Join(root, filepath.FromSlash(SourceDir), name)
		target := filepath.Join(hooksDir, name)

		// A RELATIVE link, so moving the repository does not break it. An
		// absolute one would point at wherever the repository used to be.
		relative, err := filepath.Rel(hooksDir, source)
		if err != nil {
			return report, fmt.Errorf("link %s: %w", name, err)
		}

		// The link a pre-rename install made for this hook, which is ours to
		// replace just like the current one.
		legacy, err := filepath.Rel(hooksDir,
			filepath.Join(root, filepath.FromSlash(legacySourceDir), name))
		if err != nil {
			return report, fmt.Errorf("link %s: %w", name, err)
		}

		// The accumulated report travels WITH the error from here on. Failing on
		// the third hook after moving the first one aside, and then returning an
		// empty report, leaves the user hunting for a file they were never told
		// was renamed.
		backup, err := clearTarget(target, relative, legacy, opts.Force)
		if err != nil {
			return report, err
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

			return report, fmt.Errorf("link %s: %w", name, err)
		}

		report.Installed = append(report.Installed, name)
	}

	return report, nil
}

// hooksPathElsewhere reports core.hooksPath, quoted for a message, when it is
// set and resolves to something other than hooksDir. It returns "" when git
// will read hooksDir, whether because the key is unset or because it points
// there.
//
// A relative core.hooksPath is resolved against the repository's top level,
// which is where git runs hooks from.
func hooksPathElsewhere(ctx context.Context, root, hooksDir string) (string, error) {
	current, set, err := repo.Config(ctx, root, "core.hooksPath")
	if err != nil {
		return "", err
	}

	if !set {
		return "", nil
	}

	resolved := current
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, resolved)
	}

	if sameDir(resolved, hooksDir) {
		return "", nil
	}

	if current == "" {
		return "set to the empty string", nil
	}

	return fmt.Sprintf("set to %q", current), nil
}

// sameDir reports whether two paths name one directory, following symlinks
// where they exist so /var and /private/var do not read as different on macOS.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}

	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)

	return errA == nil && errB == nil && resolvedA == resolvedB
}

// clearTarget makes room for a symlink, and reports the backup it made.
//
// Nothing the user made is ever deleted. A file in the way is RENAMED to
// <name>.bak, and an existing .bak is left alone rather than overwritten: the
// first backup is the pristine one, and losing it to a second run would be the
// one unrecoverable mistake this function could make. The one thing removed
// outright is a link this command itself made before the rename, into
// .devctl/hooks, which carries nothing to back up.
func clearTarget(target, want, legacy string, force bool) (string, error) {
	existing, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", target, err)
	}

	if existing.Mode()&fs.ModeSymlink != 0 {
		current, err := os.Readlink(target)
		if err == nil && current == want {
			return "", nil // already ours
		}

		// Ours from before the rename, pointing into .devctl/hooks: replaced
		// without --force and without a backup, since this command made it.
		if err == nil && current == legacy {
			if err := os.Remove(target); err != nil {
				return "", fmt.Errorf("replace %s: %w", target, err)
			}

			return "", nil
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
