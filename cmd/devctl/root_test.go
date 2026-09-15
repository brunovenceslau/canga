package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execute runs one devctl invocation against a FRESH command tree — cobra
// accumulates flag state across Execute calls, so reusing one would leak the
// previous test's flags into this one.
func execute(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)

	// Run BEFORE reading the buffer: Go evaluates return operands left to
	// right, so returning out.String() alongside the call would capture the
	// buffer as it was before the command ever wrote to it.
	err := cmd.ExecuteContext(t.Context())

	return out.String(), err
}

func TestRoot_UnknownCommandIsAUsageError(t *testing.T) {
	t.Parallel()

	_, err := execute(t, "bogus")
	require.Error(t, err)
	assert.Equal(t, exitUsage, exitCode(err))
}

func TestRoot_UnknownFlagIsAUsageError(t *testing.T) {
	t.Parallel()

	_, err := execute(t, "--nope")
	require.Error(t, err)
	assert.Equal(t, exitUsage, exitCode(err))
}

func TestRoot_BarePrintsHelp(t *testing.T) {
	t.Parallel()

	out, err := execute(t)
	require.NoError(t, err)
	assert.Contains(t, out, "Usage:")
}

func TestRoot_ReportsItsVersion(t *testing.T) {
	t.Parallel()

	out, err := execute(t, "--version")
	require.NoError(t, err)
	assert.Contains(t, out, version)
}
