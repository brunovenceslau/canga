// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
)

// checksumsName is the file GoReleaser attaches to every release; see the
// `checksum:` block in .goreleaser.yml.
const checksumsName = "checksums.txt"

// binaryName is the one entry extracted from an archive. The archive also
// carries LICENSE and README.md, which are ignored.
const binaryName = "devctl"

// maxExpandedBytes caps what the whole archive may expand to, counted across
// EVERY entry rather than only the devctl one. The binaries published so far
// are around 6 MiB uncompressed.
//
// Counting every entry is the point. Capping only the entry that is kept leaves
// a decompression bomb hidden in a LICENSE entry perfectly effective: tar walks
// past a skipped entry by reading its payload, so those bytes are decompressed
// whether or not anything keeps them.
//
// It is a var rather than a const ONLY so that the test for the cap can shrink
// it: proving the limit with the real value means building a 64 MiB fixture on
// every run. Nothing assigns to it outside that test.
var maxExpandedBytes int64 = 64 << 20

var (
	// ErrChecksum reports an archive whose SHA-256 is not the one the release
	// published — or one the release did not publish a checksum for at all,
	// which is refused rather than waved through.
	ErrChecksum = errors.New("checksum mismatch")

	// ErrNoBinary reports an archive with no devctl in it.
	ErrNoBinary = errors.New("no devctl in the release archive")
)

// assetPrefix is what an archive of THIS binary is named by.
//
// The platform suffix alone stopped being enough the day a release began to
// carry a second binary: agtctl ships linux archives beside devctl's, so
// "_linux_arm64.tar.gz" names two files and the upgrade would refuse both. The
// prefix is the binary's own name, which is the one part of the name_template
// that cannot change without renaming the project.
const assetPrefix = binaryName + "_"

// assetSuffix is what an archive for this platform is named by.
//
// It is matched as a SUFFIX rather than rebuilt from the version, which the
// name_template in .goreleaser.yml would let us do. Rebuilding ties this code
// to that template: change the template and the upgrade stops finding anything,
// on a code path nobody runs until the day it matters. The suffix carries the
// only part that has to be right — which platform the file is for.
func assetSuffix() string {
	return "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
}

// pickAssets finds devctl's archive for this platform and the checksums file
// beside it.
func pickAssets(rel release) (archive, checksums asset, err error) {
	suffix := assetSuffix()

	var matches []asset

	for _, candidate := range rel.Assets {
		switch {
		case strings.HasPrefix(candidate.Name, assetPrefix) && strings.HasSuffix(candidate.Name, suffix):
			matches = append(matches, candidate)
		case candidate.Name == checksumsName:
			checksums = candidate
		}
	}

	if len(matches) != 1 {
		return asset{}, asset{}, fmt.Errorf(
			"%w: %s has %d assets named %s*%s", ErrNoAsset, rel.Tag, len(matches), assetPrefix, suffix)
	}

	if checksums.Name == "" {
		return asset{}, asset{}, fmt.Errorf("%w: %s publishes no %s", ErrNoAsset, rel.Tag, checksumsName)
	}

	return matches[0], checksums, nil
}

// verifyChecksum checks an archive against the release's checksums.txt.
//
// What this proves and what it does not: it proves the bytes downloaded are the
// bytes the release names, so a truncated or corrupted download is caught. It
// does NOT prove the release itself is genuine — whoever can publish the asset
// publishes the checksum beside it. That is integrity, not authenticity, and
// the README says so in those words rather than implying more.
func verifyChecksum(archive, checksums []byte, name string) error {
	want, err := checksumFor(checksums, name)
	if err != nil {
		return err
	}

	sum := sha256.Sum256(archive)

	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%w for %s: release says %s, download is %s", ErrChecksum, name, want, got)
	}

	return nil
}

// checksumFor reads one line out of a GoReleaser checksums.txt, which is
// "<sha256>  <filename>" per line.
//
// The filename is matched for EQUALITY, the same choice the README's `awk`
// makes and for the same reason: a pattern match would read the dots in
// "devctl_0.1.0_linux_arm64.tar.gz" as wildcards. A file with no line of its
// own produces an error, never an empty checksum that would then compare equal
// to nothing.
func checksumFor(checksums []byte, name string) (string, error) {
	for line := range strings.Lines(string(checksums)) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return fields[0], nil
		}
	}

	return "", fmt.Errorf("%w: %s is absent from %s", ErrChecksum, name, checksumsName)
}

// extractBinary pulls devctl out of a gzipped tar.
//
// The archive's own entry names are never joined onto a path — the only thing
// done with a name is comparing it to a constant — so an entry called
// "../../.ssh/authorized_keys" is not dangerous here, it is simply not devctl.
// Path traversal is impossible by construction rather than by validation, which
// is the difference between a rule and a guarantee.
func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("read the release archive: %w", err)
	}

	defer func() { _ = gz.Close() }()

	// The budget sits between gzip and tar, so it counts what was DECOMPRESSED,
	// which is the quantity a bomb inflates — not what was downloaded, which
	// the size caps in github.go already bound.
	reader := tar.NewReader(&cappedReader{reader: gz, left: maxExpandedBytes})

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%w", ErrNoBinary)
		}

		if err != nil {
			return nil, fmt.Errorf("read the release archive: %w", err)
		}

		if header.Typeflag != tar.TypeReg || strings.TrimPrefix(header.Name, "./") != binaryName {
			continue
		}

		return readEntry(reader, header.Name)
	}
}

// readEntry reads one tar entry. The budget that bounds it belongs to the whole
// archive and is applied by the reader underneath, so there is no second cap
// here to drift out of step with the first.
func readEntry(reader io.Reader, name string) ([]byte, error) {
	binary, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read %s out of the release archive: %w", name, err)
	}

	if len(binary) == 0 {
		return nil, fmt.Errorf("%w: %s is empty", ErrNoBinary, name)
	}

	return binary, nil
}

// cappedReader fails past a byte budget rather than stopping at it.
//
// io.LimitReader cannot express this: it signals its limit with io.EOF, and
// every reader above it — tar, io.Copy, io.ReadAll — reads io.EOF as a clean
// end of input. A bomb would be reported as a truncated archive, or as success
// with short content, which is exactly the outcome a cap exists to prevent.
type cappedReader struct {
	reader io.Reader
	left   int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		return 0, fmt.Errorf("%w: the release archive expands past %d bytes", ErrTooLarge, maxExpandedBytes)
	}

	if int64(len(p)) > c.left {
		p = p[:c.left]
	}

	read, err := c.reader.Read(p)
	c.left -= int64(read)

	return read, err
}
