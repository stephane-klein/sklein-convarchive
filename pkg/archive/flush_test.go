package archive

import (
	"context"
	"strings"
	"testing"
)

type fakePutter struct {
	uploads      map[string][]byte
	contentTypes map[string]string
}

func (f *fakePutter) Put(_ context.Context, key string, data []byte, contentType string) error {
	f.uploads[key] = append([]byte(nil), data...)
	if f.contentTypes == nil {
		f.contentTypes = map[string]string{}
	}
	f.contentTypes[key] = contentType
	return nil
}

func TestMonthBufferFlushKey(t *testing.T) {
	b := NewMonthBuffer()
	meta := ConversationMeta{TeamName: "team-nimbus", DisplayName: "@zack"}
	if err := b.Add(&Entry{Timestamp: "2026-01-05T10:00:00+01:00", Content: "a"}, meta); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(&Entry{Timestamp: "2026-01-06T10:00:00+01:00", Content: "b"}, meta); err != nil {
		t.Fatal(err)
	}

	key := MonthKey("team-nimbus", "@zack", "2026-01")
	putter := &fakePutter{uploads: map[string][]byte{}}
	if err := b.FlushKey(context.Background(), putter, key); err != nil {
		t.Fatal(err)
	}

	objKey, err := JSONLObjectKey("team-nimbus", "@zack", "2026-01")
	if err != nil {
		t.Fatal(err)
	}
	data, ok := putter.uploads[objKey]
	if !ok {
		t.Fatalf("expected an upload of %q, got %v", objKey, putter.uploads)
	}

	// The two lines are written chronologically (oldest first).
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), data)
	}
	if !strings.Contains(lines[0], `"a"`) {
		t.Errorf("first line = %q, want entry a", lines[0])
	}
	if !strings.Contains(lines[1], `"b"`) {
		t.Errorf("second line = %q, want entry b", lines[1])
	}

	// The key is freed after the flush.
	if b.LineCount(key) != 0 {
		t.Errorf("key %q should be freed after flush", key)
	}
}

func TestMonthBufferFlushKeyEmptyIsNoop(t *testing.T) {
	b := NewMonthBuffer()
	putter := &fakePutter{uploads: map[string][]byte{}}
	if err := b.FlushKey(context.Background(), putter, MonthKey("team-nimbus", "@zack", "2026-01")); err != nil {
		t.Fatal(err)
	}
	if len(putter.uploads) != 0 {
		t.Fatalf("expected no upload, got %v", putter.uploads)
	}
}

func TestMarkdownBufferFlushKey(t *testing.T) {
	b := NewMarkdownBuffer()
	meta := ConversationMeta{TeamName: "team-nimbus", Type: "D", DisplayName: "@zack", Participants: []string{"zack"}}
	if err := b.Add(&Entry{Timestamp: "2026-01-05T10:00:00+01:00", Content: "hello"}, meta); err != nil {
		t.Fatal(err)
	}

	key := MonthKey("team-nimbus", "@zack", "2026-01")
	putter := &fakePutter{uploads: map[string][]byte{}}
	if err := b.FlushKey(context.Background(), putter, key); err != nil {
		t.Fatal(err)
	}

	objKey, err := MarkdownObjectKey("team-nimbus", "@zack", "2026-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := putter.uploads[objKey]; !ok {
		t.Fatalf("expected an upload of %q, got %v", objKey, putter.uploads)
	}
	if b.LineCount(key) != 0 {
		t.Errorf("key %q should be freed after flush", key)
	}
}
