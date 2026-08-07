package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// MarshalEntry encodes an Entry as a single JSONL line (without trailing newline).
func MarshalEntry(entry *Entry) ([]byte, error) {
	b, err := json.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal entry: %w", err)
	}
	return b, nil
}

// MonthBuffer accumulates JSONL lines grouped by conversation and month. Lines
// of a conversation+month are sorted chronologically (oldest first) before
// being written.
type MonthBuffer struct {
	lines map[string][]monthLine
}

type monthLine struct {
	ts   int64
	data []byte
}

// NewMonthBuffer creates an empty month buffer.
func NewMonthBuffer() *MonthBuffer {
	return &MonthBuffer{lines: make(map[string][]monthLine)}
}

// Add appends a JSONL line to the buffer of the conversation+month derived
// from the entry timestamp and the conversation metadata.
func (b *MonthBuffer) Add(entry *Entry, meta ConversationMeta) error {
	line, err := MarshalEntry(entry)
	if err != nil {
		return err
	}

	ts, err := time.Parse(time.RFC3339, entry.Timestamp)
	if err != nil {
		return fmt.Errorf("failed to parse entry timestamp %q: %w", entry.Timestamp, err)
	}
	month := ts.Format("2006-01")
	key := monthKey(meta.TeamName, meta.DisplayName, month)

	b.lines[key] = append(b.lines[key], monthLine{ts: ts.UnixMilli(), data: append(line, '\n')})
	return nil
}

// Keys returns the sorted list of conversation+month keys.
func (b *MonthBuffer) Keys() []string {
	keys := make([]string, 0, len(b.lines))
	for k := range b.lines {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// LineCount returns the number of buffered lines for a key.
func (b *MonthBuffer) LineCount(key string) int {
	return len(b.lines[key])
}

// Content returns the buffered lines of a conversation+month as a single byte
// slice, in chronological order (oldest first).
func (b *MonthBuffer) Content(key string) ([]byte, error) {
	lines := b.lines[key]
	sort.Slice(lines, func(i, j int) bool {
		return lines[i].ts < lines[j].ts
	})

	var buf bytes.Buffer
	for _, line := range lines {
		buf.Write(line.data)
	}
	return buf.Bytes(), nil
}

// Flush uploads each conversation+month's buffered lines as a single S3 object
// in the layout jsonl/mattermost/<team>/<display-slug>/<year>/<month>.jsonl.
// Lines of a conversation+month are written in chronological order (oldest first).
func (b *MonthBuffer) Flush(ctx context.Context, uploader ObjectPutter) error {
	for _, key := range b.Keys() {
		teamName, displayName, month, err := ParseMonthKey(key)
		if err != nil {
			return err
		}

		objKey, err := JSONLObjectKey(teamName, displayName, month)
		if err != nil {
			return err
		}

		content, err := b.Content(key)
		if err != nil {
			return err
		}

		if err := uploader.Put(ctx, objKey, content, "application/x-ndjson"); err != nil {
			return err
		}
	}
	return nil
}

// JSONLObjectKey computes the object storage key for a conversation+month:
// jsonl/mattermost/<team>/<display-slug>/<year>/<month>.jsonl
func JSONLObjectKey(teamName, displayName, month string) (string, error) {
	path, err := conversationObjectPath(teamName, displayName, month)
	if err != nil {
		return "", err
	}
	return "jsonl/" + path + ".jsonl", nil
}
