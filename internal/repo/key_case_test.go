// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// matchingFS simulates a volume that looks names up by an equivalence wider
// than byte equality, over a real, byte-exact one, so Linux CI can exercise
// what the operator measured on a Mac:
//
//	filepath.EvalSymlinks($T/SRC/LOCAL/os) = $T/SRC/LOCAL/os  (typed case kept)
//	git rev-parse --show-toplevel          = $T/src/local/OS  (on-disk case)
//
// Every lookup matches each component against the real directory entries with
// equal; EvalSymlinks keeps the typed spelling unless a link was followed;
// ReadDirNames reports the names as stored.
type matchingFS struct {
	equal func(typed, stored string) bool
}

// foldingFS folds case, as APFS does by default.
var foldingFS = matchingFS{equal: strings.EqualFold}

// exactFS matches names byte for byte, as a case-sensitive volume does.
var exactFS = matchingFS{equal: func(typed, stored string) bool { return typed == stored }}

// normalizingFS treats a composed "é" (NFC) and "e" plus a combining acute
// (NFD) as one name, as APFS does. The two spellings stand in for Unicode
// normalization, which the standard library does not implement.
var normalizingFS = matchingFS{equal: func(typed, stored string) bool {
	nfc := func(s string) string { return strings.ReplaceAll(s, "é", "é") }

	return nfc(typed) == nfc(stored)
}}

// lookup maps a typed path onto the stored spelling of each component.
func (m matchingFS) lookup(path string) (string, error) {
	current := string(filepath.Separator)

	for component := range strings.SplitSeq(strings.TrimPrefix(filepath.Clean(path), current), current) {
		if component == "" {
			continue
		}

		entries, err := os.ReadDir(current)
		if err != nil {
			return "", err
		}

		match := ""

		for _, entry := range entries {
			if entry.Name() == component || m.equal(component, entry.Name()) {
				match = entry.Name()

				break
			}
		}

		if match == "" {
			return "", &fs.PathError{Op: "lstat", Path: path, Err: fs.ErrNotExist}
		}

		current = filepath.Join(current, match)
	}

	return current, nil
}

func (m matchingFS) Lstat(path string) (fs.FileInfo, error) {
	stored, err := m.lookup(path)
	if err != nil {
		return nil, err
	}

	return os.Lstat(stored)
}

func (m matchingFS) Stat(path string) (fs.FileInfo, error) {
	stored, err := m.lookup(path)
	if err != nil {
		return nil, err
	}

	return os.Stat(stored)
}

func (m matchingFS) ReadRegular(path string, limit int64) ([]byte, error) {
	stored, err := m.lookup(path)
	if err != nil {
		return nil, err
	}

	return osFS{}.ReadRegular(stored, limit)
}

func (m matchingFS) ReadDirNames(path string) ([]string, error) {
	stored, err := m.lookup(path)
	if err != nil {
		return nil, err
	}

	return osFS{}.ReadDirNames(stored)
}

func (m matchingFS) EvalSymlinks(path string) (string, error) {
	stored, err := m.lookup(path)
	if err != nil {
		return "", err
	}

	resolved, err := filepath.EvalSymlinks(stored)
	if err != nil {
		return "", err
	}

	if resolved == stored {
		return filepath.Clean(path), nil
	}

	return resolved, nil
}

func (matchingFS) SameFile(a, b fs.FileInfo) bool { return os.SameFile(a, b) }

// failingFS is foldingFS with one operation failing, for the paths that have
// to fail closed.
type failingFS struct {
	matchingFS

	lstat, stat, readDir func(path string) bool // true: fail this call
}

var errInjected = &fs.PathError{Op: "injected", Path: "x", Err: syscall.EACCES}

func (f failingFS) Lstat(path string) (fs.FileInfo, error) {
	if f.lstat != nil && f.lstat(path) {
		return nil, errInjected
	}

	return f.matchingFS.Lstat(path)
}

func (f failingFS) Stat(path string) (fs.FileInfo, error) {
	if f.stat != nil && f.stat(path) {
		return nil, errInjected
	}

	return f.matchingFS.Stat(path)
}

func (f failingFS) ReadDirNames(path string) ([]string, error) {
	if f.readDir != nil && f.readDir(path) {
		return nil, errInjected
	}

	return f.matchingFS.ReadDirNames(path)
}

// foldsCase reports whether the volume holding dir looks names up without
// regard to case, as APFS does by default: a probe created in lower case is
// the same file when named in upper case.
func foldsCase(t *testing.T, dir string) bool {
	t.Helper()

	probe := filepath.Join(dir, "case-probe")
	require.NoError(t, os.WriteFile(probe, nil, 0o600))
	t.Cleanup(func() { _ = os.Remove(probe) })

	lower, err := os.Lstat(probe)
	require.NoError(t, err)

	upper, err := os.Lstat(filepath.Join(dir, "CASE-PROBE"))
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}

	require.NoError(t, err)

	return os.SameFile(lower, upper)
}

// caseLayout makes $T/src/local/OS on disk and returns $T resolved.
func caseLayout(t *testing.T) string {
	t.Helper()

	return mkdirs(t, t.TempDir(), "src/local/OS")
}

func TestCanonical(t *testing.T) {
	t.Parallel()

	t.Run("a case-insensitive volume yields the on-disk spelling", func(t *testing.T) {
		t.Parallel()

		root := caseLayout(t)

		got, err := canonical(foldingFS, filepath.Join(root, "SRC", "LOCAL", "os"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(root, "src", "local", "OS"), got)
	})

	t.Run("a normalization-insensitive volume yields the stored bytes", func(t *testing.T) {
		t.Parallel()

		root := mkdirs(t, t.TempDir(), "src/local/café")

		got, err := canonical(normalizingFS, filepath.Join(root, "src", "local", "café"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(root, "src", "local", "café"), got)
	})

	// The real filesystem, whichever the machine has: the macOS legs of the
	// Test workflow run this on APFS, which folds case, and the Linux legs on
	// a volume that does not (ship-gate round 2, code-reviewer Required: this
	// assumed case-sensitive and would have failed on a Mac).
	t.Run("the real filesystem keeps the stored spelling", func(t *testing.T) {
		t.Parallel()

		root := caseLayout(t)
		path := filepath.Join(root, "src", "local", "OS")

		got, err := canonical(osFS{}, path)
		require.NoError(t, err)
		assert.Equal(t, path, got)

		wrong := filepath.Join(root, "SRC", "local", "OS")

		got, err = canonical(osFS{}, wrong)
		if foldsCase(t, root) {
			require.NoError(t, err)
			assert.Equal(t, path, got, "a folding volume answers with the stored spelling")
		} else {
			require.ErrorIs(t, err, fs.ErrNotExist, "a wrong spelling is another, missing, directory")
		}
	})

	t.Run("two entries differing only in case stay distinct", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		if foldsCase(t, dir) {
			t.Skipf("%s folds case: two such entries cannot exist there", dir)
		}

		// An exact name is taken as is, never folded onto a sibling.
		root := mkdirs(t, dir, "src/Repo", "src/repo")

		for _, name := range []string{"Repo", "repo"} {
			got, err := canonical(osFS{}, filepath.Join(root, "src", name))
			require.NoError(t, err)
			assert.Equal(t, filepath.Join(root, "src", name), got)
		}
	})

	t.Run("a case-sensitive directory is never read", func(t *testing.T) {
		t.Parallel()

		root := caseLayout(t)

		// Every ReadDir fails: on a volume that tells case apart, the typed
		// spelling is resolved without one, so a directory with execute but
		// no read permission still works.
		got, err := canonical(failingFS{
			matchingFS: exactFS,
			readDir:    func(string) bool { return true },
		}, filepath.Join(root, "src", "local", "OS"))
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(root, "src", "local", "OS"), got)
	})

	// Fail closed: on a folding volume the typed spelling is exactly the one
	// that opens a second store, so it is never kept as a fallback.
	t.Run("an unreadable parent on a folding volume fails closed", func(t *testing.T) {
		t.Parallel()

		root := caseLayout(t)

		_, err := canonical(failingFS{
			matchingFS: foldingFS,
			// The parent of "os", by then spelled as stored.
			readDir: func(path string) bool { return filepath.Base(path) == "local" },
		},
			filepath.Join(root, "SRC", "LOCAL", "os"))
		require.ErrorIs(t, err, ErrUnreadableSpelling)
		require.ErrorIs(t, err, syscall.EACCES)
	})

	t.Run("a failing lstat of the flipped spelling fails closed", func(t *testing.T) {
		t.Parallel()

		root := caseLayout(t)

		_, err := canonical(failingFS{
			matchingFS: foldingFS,
			// "local" is only ever looked up as LOCAL's flipped spelling.
			lstat: func(path string) bool { return filepath.Base(path) == "local" },
		},
			filepath.Join(root, "SRC", "LOCAL", "os"))
		require.ErrorIs(t, err, ErrUnreadableSpelling)
	})

	t.Run("a failing lstat of the component itself fails", func(t *testing.T) {
		t.Parallel()

		root := caseLayout(t)

		_, err := canonical(failingFS{
			matchingFS: foldingFS,
			lstat:      func(path string) bool { return filepath.Base(path) == "os" },
		},
			filepath.Join(root, "SRC", "LOCAL", "os"))
		require.ErrorIs(t, err, syscall.EACCES)
	})
}

// TestPathUnder_CaseInsensitive: a wrong-case tree, a wrong-case base, or both,
// key as the on-disk spelling does, so a Mac never splits one list in two.
func TestPathUnder_CaseInsensitive(t *testing.T) {
	t.Parallel()

	root := caseLayout(t)

	for _, tc := range []struct{ base, top string }{
		{"src", "SRC/LOCAL/os"},
		{"SRC", "src/local/OS"},
		{"Src", "sRc/LoCaL/Os"},
		{"SRC/", "src/local/OS/"},
	} {
		got, err := pathUnder(foldingFS,
			filepath.Join(root, filepath.FromSlash(tc.base)),
			filepath.Join(root, filepath.FromSlash(tc.top)))
		require.NoError(t, err, "base %s top %s", tc.base, tc.top)
		assert.Equal(t, "local/OS", got, "base %s top %s", tc.base, tc.top)
	}
}

// TestToplevel_CaseInsensitive: git reports the on-disk spelling, the walk
// starts from the typed one, and identity is decided by the file, not the
// string. A worktree git places elsewhere is still refused.
func TestToplevel_CaseInsensitive(t *testing.T) {
	t.Parallel()

	root := mkdirs(t, t.TempDir(), "src/local/OS/.git", "src/local/OS/docs", "src/local/victim")
	onDisk := filepath.Join(root, "src", "local", "OS")

	for _, typed := range []string{
		filepath.Join(root, "SRC", "LOCAL", "os"),
		filepath.Join(root, "SRC", "LOCAL", "os", "DOCS"),
	} {
		got, err := toplevel(foldingFS, typed, onDisk)
		require.NoError(t, err, typed)
		assert.Equal(t, onDisk, got, typed)
	}

	// git's answer in ANOTHER spelling of the same directory, here upper case
	// on the folding volume and a symbolic link on the real one: identity is
	// the file's, so both are accepted.
	got, err := toplevel(foldingFS, onDisk, filepath.Join(root, "SRC", "LOCAL", "OS"))
	require.NoError(t, err)
	assert.Equal(t, onDisk, got)

	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(onDisk, link))

	got, err = toplevel(osFS{}, onDisk, link)
	require.NoError(t, err)
	assert.Equal(t, onDisk, got)

	_, err = toplevel(foldingFS, filepath.Join(root, "SRC", "LOCAL", "os"),
		filepath.Join(root, "src", "local", "victim"))
	require.ErrorIs(t, err, ErrWorktreeElsewhere)

	_, err = toplevel(foldingFS, filepath.Join(root, "SRC", "nope"), onDisk)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

// TestToplevel_Failures: every filesystem failure on the way is an error, and
// none of them is mistaken for an answer.
func TestToplevel_Failures(t *testing.T) {
	t.Parallel()

	root := mkdirs(t, t.TempDir(), "repo/.git", "repo/sub", "plain")
	repo := filepath.Join(root, "repo")

	t.Run("stat of the walked tree", func(t *testing.T) {
		t.Parallel()

		_, err := toplevel(failingFS{matchingFS: foldingFS, stat: func(p string) bool { return p == repo }},
			filepath.Join(repo, "sub"), filepath.Join(root, "REPO"))
		require.ErrorIs(t, err, syscall.EACCES)
	})

	t.Run("stat of git's answer", func(t *testing.T) {
		t.Parallel()

		gitTop := filepath.Join(root, "REPO")
		_, err := toplevel(failingFS{matchingFS: foldingFS, stat: func(p string) bool { return p == gitTop }},
			filepath.Join(repo, "sub"), gitTop)
		require.ErrorIs(t, err, syscall.EACCES)
	})

	t.Run("an unreadable .git on the way up", func(t *testing.T) {
		t.Parallel()

		_, err := dotGitAbove(failingFS{
			matchingFS: foldingFS,
			lstat:      func(p string) bool { return filepath.Base(p) == ".git" },
		}, filepath.Join(repo, "sub"))
		require.ErrorIs(t, err, syscall.EACCES)
		require.NotErrorIs(t, err, ErrNotARepository)
	})

	t.Run("no .git all the way to the root", func(t *testing.T) {
		t.Parallel()

		_, err := dotGitAbove(foldingFS, filepath.Join(root, "plain"))
		require.ErrorIs(t, err, ErrNotARepository)
	})
}

// TestKeyTree_CaseInsensitive: a linked worktree's main tree is canonical
// too, so git naming the common directory in another case keys it as the
// stored spelling does.
func TestKeyTree_CaseInsensitive(t *testing.T) {
	t.Parallel()

	root := mkdirs(t, t.TempDir(), "src/local/OS/.git/worktrees/wt", "src/local/OS-wt")
	main := filepath.Join(root, "src", "local", "OS")
	worktree := filepath.Join(root, "src", "local", "OS-wt")
	gitDir := filepath.Join(main, ".git", "worktrees", "wt")

	require.NoError(t, os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "gitdir"),
		[]byte(filepath.Join(worktree, ".git")+"\n"), 0o600))

	got, err := keyTree(foldingFS, worktree,
		filepath.Join(root, "SRC", "LOCAL", "os", ".git", "worktrees", "wt"),
		filepath.Join(root, "SRC", "LOCAL", "os", ".git"))
	require.NoError(t, err)
	assert.Equal(t, main, got)
}

// An Lstat that fails is never a "different file": errors.Is keeps it
// visible. A link is not the entry it points at.
func TestSameEntry(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	require.NoError(t, sameEntry(osFS{}, dir, dir))
	require.Error(t, sameEntry(osFS{}, dir, t.TempDir()))
	require.ErrorIs(t, sameEntry(osFS{}, filepath.Join(dir, "missing"), dir), fs.ErrNotExist)

	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(dir, link))
	require.Error(t, sameEntry(osFS{}, link, dir))
}
