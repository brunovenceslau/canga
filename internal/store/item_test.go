package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestItem_Summary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "single line", text: "one line", want: "one line"},
		{name: "first line only", text: "headline\nand the rest", want: "headline"},
		{name: "empty", text: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, Item{Text: tt.text}.Summary())
		})
	}
}

func TestItem_RoundTrip(t *testing.T) {
	t.Parallel()

	want := Item{
		ID:      "20260915T142233.482913Z-9f3a1c",
		Created: time.Date(2026, 9, 15, 14, 22, 33, 482913000, time.UTC),
		Repo:    "github.com/owner/repo",
		Scope:   "repo",
		Text:    "wire the reminders store into the uninstall purge warning",
	}

	got := decodeItem(want.ID, []byte(want.encode()))
	assert.Equal(t, want.Text, got.Text)
	assert.Equal(t, want.Repo, got.Repo)
	assert.Equal(t, want.Scope, got.Scope)
	assert.True(t, got.Created.Equal(want.Created))
}

func TestItem_MultiLineBodyIsPreserved(t *testing.T) {
	t.Parallel()

	// A blank line inside the body must not be mistaken for the header
	// separator, which only the FIRST one is.
	want := Item{ID: "a", Text: "headline\n\nthe rest\n\nand more"}

	got := decodeItem(want.ID, []byte(want.encode()))
	assert.Equal(t, want.Text, got.Text)
	assert.Equal(t, "headline", got.Summary())
}

// TestDecodeItem_TreatsTheFilenameAsTheID: the "id:" header is advisory, so a
// file copied to a new name still works.
func TestDecodeItem_TreatsTheFilenameAsTheID(t *testing.T) {
	t.Parallel()

	encoded := Item{ID: "original", Text: "body"}.encode()

	got := decodeItem("renamed", []byte(encoded))
	assert.Equal(t, "renamed", got.ID)
	assert.Equal(t, "body", got.Text)
}

// TestDecodeItem_Survives is the contract that keeps one bad file from hiding
// every other reminder: decoding never fails, it just keeps what it can.
func TestDecodeItem_Survives(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		text string
	}{
		{name: "no header at all", data: "just a note\n", text: "just a note"},
		{name: "unparseable timestamp", data: "created: yesterday\n\nbody\n", text: "body"},
		{name: "unknown header", data: "colour: blue\n\nbody\n", text: "body"},
		{name: "empty file", data: "", text: ""},
		{name: "header but empty body", data: "id: a\n\n", text: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := decodeItem("a", []byte(tt.data))
			assert.Equal(t, tt.text, got.Text)
		})
	}
}

func TestStore_ReadsAHandWrittenFile(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t)

	// The point of one plain file per item: an editor, or another tool, can put
	// one there and devctl lists it.
	require.NoError(t, writeItemFile(reminders, "hand-written", "dropped in by hand\n"))

	items, err := reminders.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "hand-written", items[0].ID)
	assert.Equal(t, "dropped in by hand", items[0].Text)
}

// TestStore_IgnoresNonItemFiles keeps the items directory from being a trap: a
// stray file must not become a reminder, and must not break the listing.
func TestStore_IgnoresNonItemFiles(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t)

	item, err := reminders.Add("real")
	require.NoError(t, err)

	for _, name := range []string{"notes.txt", ".hidden" + itemExt, "not an id" + itemExt, itemExt} {
		require.NoError(t, writeRaw(reminders, name, "junk\n"))
	}

	items, err := reminders.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, item.ID, items[0].ID)
}
