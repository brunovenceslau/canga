package store

import (
	"fmt"
	"strings"
	"time"
)

// itemTimeLayout is RFC 3339 with a fixed microsecond field, matching the
// resolution an id carries. Fixed-width rather than time.RFC3339Nano, which
// drops trailing zeros and so would vary the column from one item to the next.
// time.Parse with time.RFC3339 reads it back, fraction and all.
const itemTimeLayout = "2006-01-02T15:04:05.000000Z07:00"

// Item is one reminder.
type Item struct {
	ID      string
	Created time.Time
	Repo    string
	Scope   string
	Text    string
}

// Summary is the item's first line, which is what a listing shows and what a
// shell renders beside a completion candidate.
func (i Item) Summary() string {
	line, _, _ := strings.Cut(i.Text, "\n")

	return line
}

// encode renders an item as its file: header lines, a blank line, then the body
// verbatim. Plain text on purpose — the point of one file per item is that the
// file stays editable by hand, and `devctl reminders path` exists to hand one
// to an editor.
func (i Item) encode() string {
	var out strings.Builder

	// The "id:" header is ADVISORY. The filename is the id, so a file copied to
	// a new name still works; the header is here for a file read on its own.
	fmt.Fprintf(&out, "id: %s\n", i.ID)
	fmt.Fprintf(&out, "created: %s\n", i.Created.Format(itemTimeLayout))

	if i.Repo != "" {
		fmt.Fprintf(&out, "repo: %s\n", i.Repo)
	}

	if i.Scope != "" {
		fmt.Fprintf(&out, "scope: %s\n", i.Scope)
	}

	out.WriteString("\n")
	out.WriteString(i.Text)
	out.WriteString("\n")

	return out.String()
}

// decodeItem parses an item file. It cannot fail: a hand-edited or truncated
// file still lists, with whatever survived. Refusing to decode would mean one
// malformed file could hide every other reminder in the store, which is a worse
// failure than a missing timestamp.
func decodeItem(id string, data []byte) Item {
	item := Item{ID: id}

	header, body, ok := strings.Cut(string(data), "\n\n")
	if !ok {
		// No header block at all: treat the whole file as the reminder.
		item.Text = strings.TrimRight(string(data), "\n")

		return item
	}

	for line := range strings.SplitSeq(header, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}

		switch key {
		case "created":
			if when, err := time.Parse(time.RFC3339, value); err == nil {
				item.Created = when
			}
		case "repo":
			item.Repo = value
		case "scope":
			item.Scope = value
		}
	}

	item.Text = strings.TrimRight(body, "\n")

	return item
}
