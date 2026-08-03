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

// DayBuffer accumulates JSONL lines grouped by day. Lines of a day are sorted
// chronologically (oldest first) before being written.
type DayBuffer struct {
	lines map[string][]dayLine
}

type dayLine struct {
	ts   int64
	data []byte
}

// NewDayBuffer creates an empty day buffer.
func NewDayBuffer() *DayBuffer {
	return &DayBuffer{lines: make(map[string][]dayLine)}
}

// Add appends a JSONL line to the buffer of the day derived from the entry timestamp.
func (b *DayBuffer) Add(entry *Entry) error {
	line, err := MarshalEntry(entry)
	if err != nil {
		return err
	}

	ts, err := time.Parse(time.RFC3339, entry.Timestamp)
	if err != nil {
		return fmt.Errorf("failed to parse entry timestamp %q: %w", entry.Timestamp, err)
	}
	day := ts.Format("2006-01-02")

	b.lines[day] = append(b.lines[day], dayLine{ts: ts.UnixMilli(), data: append(line, '\n')})
	return nil
}

// Days returns the sorted list of day keys.
func (b *DayBuffer) Days() []string {
	days := make([]string, 0, len(b.lines))
	for day := range b.lines {
		days = append(days, day)
	}
	sort.Strings(days)
	return days
}

// LineCount returns the number of buffered lines for a day.
func (b *DayBuffer) LineCount(day string) int {
	return len(b.lines[day])
}

// Content returns the buffered lines of a day as a single byte slice, in
// chronological order (oldest first).
func (b *DayBuffer) Content(day string) ([]byte, error) {
	lines := b.lines[day]
	sort.Slice(lines, func(i, j int) bool {
		return lines[i].ts < lines[j].ts
	})

	var buf bytes.Buffer
	for _, line := range lines {
		buf.Write(line.data)
	}
	return buf.Bytes(), nil
}

// Flush uploads each day's buffered lines as a single S3 object in
// the layout jsonl/mattermost/<year>/<month>/<day>/<date>.jsonl.
// Lines of a day are written in chronological order (oldest first).
func (b *DayBuffer) Flush(ctx context.Context, uploader *Uploader) error {
	for _, day := range b.Days() {
		key, err := DailyObjectKey(day)
		if err != nil {
			return err
		}

		content, err := b.Content(day)
		if err != nil {
			return err
		}

		if err := uploader.Put(ctx, key, content, "application/x-ndjson"); err != nil {
			return err
		}
	}
	return nil
}

// DailyObjectKey computes the object storage key for a day (format YYYY-MM-DD):
// jsonl/mattermost/<year>/<month>/<day>/<date>.jsonl
func DailyObjectKey(day string) (string, error) {
	ts, err := time.Parse("2006-01-02", day)
	if err != nil {
		return "", fmt.Errorf("invalid day %q: %w", day, err)
	}
	return fmt.Sprintf("jsonl/mattermost/%d/%02d/%02d/%s.jsonl",
		ts.Year(), int(ts.Month()), ts.Day(), day), nil
}
