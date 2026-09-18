// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What a fixture archive's two kinds of entry contain: the binary that is kept,
// and a file that is only there to be walked past.
const (
	licenseName = "LICENSE"
	binaryBody  = "ELF"
	licenseBody = "GPL"
)

// tarEntry is one file in a fixture archive. It is a slice of these rather than
// a map because the order matters to some of the tests, and because a type
// flag other than "regular file" has to be expressible.
type tarEntry struct {
	name     string
	body     string
	typeflag byte
}

// tarGz builds a gzipped tar in memory, the way a release archive is shaped.
func tarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()

	var raw bytes.Buffer

	gz := gzip.NewWriter(&raw)
	writer := tar.NewWriter(gz)

	for _, entry := range entries {
		flag := entry.typeflag
		if flag == 0 {
			flag = tar.TypeReg
		}

		require.NoError(t, writer.WriteHeader(&tar.Header{
			Name:     entry.name,
			Mode:     0o755,
			Size:     int64(len(entry.body)),
			Typeflag: flag,
		}))

		_, err := writer.Write([]byte(entry.body))
		require.NoError(t, err)
	}

	require.NoError(t, writer.Close())
	require.NoError(t, gz.Close())

	return raw.Bytes()
}

// checksumsFile renders a GoReleaser checksums.txt for the named contents.
func checksumsFile(files map[string][]byte) []byte {
	var out strings.Builder

	for _, name := range slices.Sorted(maps(files)) {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(&out, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}

	return []byte(out.String())
}

// maps yields a map's keys. It exists so checksumsFile can sort them, which
// keeps a fixture byte-identical from run to run.
func maps(files map[string][]byte) func(func(string) bool) {
	return func(yield func(string) bool) {
		for name := range files {
			if !yield(name) {
				return
			}
		}
	}
}

func TestPickAssets(t *testing.T) {
	t.Parallel()

	archiveName := "canga-host_0.1.0" + assetSuffix()
	otherName := "canga-host_0.1.0_plan9_mips.tar.gz"

	tests := []struct {
		name      string
		assets    []asset
		expected  string
		expectErr bool
	}{
		{
			name:     "one archive for this platform among several",
			assets:   []asset{{ID: 1, Name: otherName}, {ID: 2, Name: archiveName}, {ID: 3, Name: checksumsName}},
			expected: archiveName,
		},
		{
			// A release carries both builds, both called canga. The sandbox
			// build's archive for this very platform must not count as the
			// host's, or the upgrade refuses a release that does hold exactly
			// one host archive.
			name:     "the sandbox build's archive for this platform is ignored",
			assets:   []asset{{ID: 1, Name: "canga-sandbox_0.1.0" + assetSuffix()}, {ID: 2, Name: archiveName}, {ID: 3, Name: checksumsName}},
			expected: archiveName,
		},
		{
			name:      "only the sandbox build's archive for this platform",
			assets:    []asset{{ID: 1, Name: "canga-sandbox_0.1.0" + assetSuffix()}, {ID: 2, Name: checksumsName}},
			expectErr: true,
		},
		{
			name:      "nothing for this platform",
			assets:    []asset{{ID: 1, Name: otherName}, {ID: 2, Name: checksumsName}},
			expectErr: true,
		},
		{
			name:      "two archives for this platform",
			assets:    []asset{{ID: 1, Name: archiveName}, {ID: 2, Name: "canga-host_0.2.0" + assetSuffix()}, {ID: 3, Name: checksumsName}},
			expectErr: true,
		},
		{
			name:      "no checksums to verify against",
			assets:    []asset{{ID: 1, Name: archiveName}},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			archive, checksums, err := pickAssets(release{Tag: installedVersion, Assets: tt.assets})
			if tt.expectErr {
				require.ErrorIs(t, err, ErrNoAsset)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, archive.Name)
			assert.Equal(t, checksumsName, checksums.Name)
		})
	}
}

// TestAssetSuffixNamesThisPlatform pins the shape the suffix match depends on.
func TestAssetSuffixNamesThisPlatform(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz", assetSuffix())
}

func TestChecksumFor(t *testing.T) {
	t.Parallel()

	checksums := []byte("" +
		"aaaa  canga-host_0.1.0_linux_arm64.tar.gz\n" +
		"bbbb  canga-host_0.1.0_linux_amd64.tar.gz\n" +
		"\n" +
		"malformed-line-with-one-field\n")

	t.Run("names its own line", func(t *testing.T) {
		t.Parallel()

		sum, err := checksumFor(checksums, "canga-host_0.1.0_linux_amd64.tar.gz")
		require.NoError(t, err)
		assert.Equal(t, "bbbb", sum)
	})

	t.Run("a file with no line is refused, not defaulted", func(t *testing.T) {
		t.Parallel()

		_, err := checksumFor(checksums, "canga-host_0.1.0_darwin_arm64.tar.gz")
		require.ErrorIs(t, err, ErrChecksum)
	})

	// The filename is matched for equality, and this is the direction that
	// proves it. An implementation that searched for the asset name as a
	// PATTERN would compile "canga-host_0.1.0_linux_arm64.tar.gz", whose dots are
	// wildcards, and that pattern matches the line below — so a pattern-based
	// lookup hands back cccc for an asset that has no checksum of its own.
	// Written the other way round, with the mangled name as the query, the test
	// passes whether or not the bug is there.
	t.Run("a line that only a pattern would match", func(t *testing.T) {
		t.Parallel()

		nearMiss := []byte("cccc  canga-host_0X1X0_linux_arm64Xtar.gz\n")

		_, err := checksumFor(nearMiss, "canga-host_0.1.0_linux_arm64.tar.gz")
		require.ErrorIs(t, err, ErrChecksum)
	})
}

func TestVerifyChecksum(t *testing.T) {
	t.Parallel()

	archive := []byte("the release archive")
	name := "canga-host_0.1.0" + assetSuffix()
	checksums := checksumsFile(map[string][]byte{name: archive})

	t.Run("the published sum", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, verifyChecksum(archive, checksums, name))
	})

	// Only the HEX is upper-cased. The filename stays as it is, because a
	// release asset's name is case-sensitive and matched for equality.
	t.Run("upper case hex is the same sum", func(t *testing.T) {
		t.Parallel()

		sum, _, _ := bytes.Cut(checksums, []byte("  "))
		shouted := append(bytes.ToUpper(sum), []byte("  "+name+"\n")...)

		require.NoError(t, verifyChecksum(archive, shouted, name))
	})

	t.Run("one byte different is refused", func(t *testing.T) {
		t.Parallel()

		err := verifyChecksum([]byte("the release archivf"), checksums, name)
		require.ErrorIs(t, err, ErrChecksum)
	})
}

func TestExtractBinary(t *testing.T) {
	t.Parallel()

	t.Run("takes canga and ignores the rest", func(t *testing.T) {
		t.Parallel()

		archive := tarGz(t,
			tarEntry{name: licenseName, body: licenseBody},
			tarEntry{name: binaryName, body: binaryBody},
			tarEntry{name: "README.md", body: "# canga"})

		binary, err := extractBinary(archive)
		require.NoError(t, err)
		assert.Equal(t, binaryBody, string(binary))
	})

	t.Run("accepts the ./ spelling", func(t *testing.T) {
		t.Parallel()

		binary, err := extractBinary(tarGz(t, tarEntry{name: "./" + binaryName, body: binaryBody}))
		require.NoError(t, err)
		assert.Equal(t, binaryBody, string(binary))
	})

	// The archive's names are never joined onto a path, so a traversing name is
	// not dangerous here — it is simply not canga. This proves the entry is
	// skipped rather than trusted, which is the property the comment claims.
	t.Run("a traversing name is just not canga", func(t *testing.T) {
		t.Parallel()

		_, err := extractBinary(tarGz(t, tarEntry{name: "../../.ssh/authorized_keys", body: "ssh-ed25519 AAAA"}))
		require.ErrorIs(t, err, ErrNoBinary)
	})

	t.Run("a directory named canga is not a binary", func(t *testing.T) {
		t.Parallel()

		_, err := extractBinary(tarGz(t, tarEntry{name: binaryName, typeflag: tar.TypeDir}))
		require.ErrorIs(t, err, ErrNoBinary)
	})

	t.Run("an empty canga is refused", func(t *testing.T) {
		t.Parallel()

		_, err := extractBinary(tarGz(t, tarEntry{name: binaryName}))
		require.ErrorIs(t, err, ErrNoBinary)
	})

	t.Run("an archive without canga", func(t *testing.T) {
		t.Parallel()

		_, err := extractBinary(tarGz(t, tarEntry{name: licenseName, body: licenseBody}))
		require.ErrorIs(t, err, ErrNoBinary)
	})

	t.Run("not a gzip at all", func(t *testing.T) {
		t.Parallel()

		_, err := extractBinary([]byte("plain text"))
		require.Error(t, err)
	})
}

// TestExtractBinaryStopsAtTheCap proves the decompression limit refuses rather
// than truncating, and that it counts EVERY entry — a bomb hidden in a LICENSE
// nobody keeps is still decompressed on the way to the entry that is kept.
//
// The cap is shrunk for the duration instead of building a 64 MiB fixture on
// every run, which is the only reason maxExpandedBytes is a var. It is shrunk
// to a few KiB rather than a few bytes because tar spends 512 of them on each
// entry header, and those are decompressed bytes like any other.
//
//nolint:paralleltest // it swaps a package-level cap, so it cannot share the process with a parallel test.
func TestExtractBinaryStopsAtTheCap(t *testing.T) {
	original := maxExpandedBytes

	t.Cleanup(func() { maxExpandedBytes = original })

	maxExpandedBytes = 4096

	t.Run("an oversized canga", func(t *testing.T) {
		_, err := extractBinary(tarGz(t, tarEntry{name: binaryName, body: strings.Repeat("A", 8192)}))
		require.ErrorIs(t, err, ErrTooLarge)
	})

	// The entry is skipped, never kept, and still has to be decompressed to
	// reach the one after it. A cap that only measured what it kept would pass
	// this archive straight through.
	t.Run("a bomb in an entry that is thrown away", func(t *testing.T) {
		archive := tarGz(t,
			tarEntry{name: licenseName, body: strings.Repeat("A", 8192)},
			tarEntry{name: binaryName, body: binaryBody})

		_, err := extractBinary(archive)
		require.ErrorIs(t, err, ErrTooLarge)
	})

	t.Run("an ordinary archive is well under it", func(t *testing.T) {
		archive := tarGz(t,
			tarEntry{name: licenseName, body: licenseBody},
			tarEntry{name: binaryName, body: binaryBody})

		binary, err := extractBinary(archive)
		require.NoError(t, err)
		assert.Equal(t, binaryBody, string(binary))
	})
}
