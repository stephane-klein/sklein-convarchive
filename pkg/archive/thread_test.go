package archive

import (
	"strings"
	"testing"
	"time"
)

func TestThreadObjectKeys(t *testing.T) {
	got, err := JSONLThreadObjectKey("claude", "account_demo", 2024, "2024-03-14_101500")
	if err != nil {
		t.Fatal(err)
	}
	if got != "jsonl/claude/account-demo/2024/2024-03-14_101500.jsonl" {
		t.Errorf("jsonl key = %q", got)
	}

	got, err = MarkdownThreadObjectKey("chatgpt", "default", 2024, "2024-01-12_164500")
	if err != nil {
		t.Fatal(err)
	}
	if got != "markdown/chatgpt/default/2024/2024-01-12_164500.md" {
		t.Errorf("markdown key = %q", got)
	}
}

func TestThreadObjectKeysInvalid(t *testing.T) {
	if _, err := JSONLThreadObjectKey("", "a", 2026, "d"); err == nil {
		t.Error("expected error for empty source")
	}
	if _, err := JSONLThreadObjectKey("claude", "a", 2026, ""); err == nil {
		t.Error("expected error for empty datetime")
	}
}

func TestMarshalThreadJSONLSortsChronologically(t *testing.T) {
	loc := time.UTC
	late := &Entry{Source: "claude", Timestamp: time.Date(2024, 7, 8, 9, 0, 0, 0, loc).Format(time.RFC3339)}
	early := &Entry{Source: "claude", Timestamp: time.Date(2024, 7, 8, 6, 0, 0, 0, loc).Format(time.RFC3339)}

	out, err := MarshalThreadJSONL([]*Entry{late, early})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "2024-07-08T06:00:00Z") {
		t.Errorf("first line = %q, want the early entry first", lines[0])
	}
	if !strings.Contains(lines[1], "2024-07-08T09:00:00Z") {
		t.Errorf("second line = %q, want the late entry", lines[1])
	}
}

func TestRenderThreadMarkdown(t *testing.T) {
	loc := time.UTC
	entries := []*Entry{
		{
			Source: "claude", Author: "carla", Content: "première question",
			Timestamp: time.Date(2024, 7, 8, 6, 1, 47, 0, loc).Format(time.RFC3339),
		},
		{
			Source: "claude", Author: "assistant", Content: "une réponse",
			Timestamp: time.Date(2024, 7, 8, 6, 1, 56, 0, loc).Format(time.RFC3339),
		},
	}
	meta := ConversationMeta{Source: "claude", TeamName: "default", DisplayName: "Ma conversation"}

	out, err := RenderThreadMarkdown(entries, meta)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)

	if !strings.HasPrefix(text, "# 2024-07-08 - Ma conversation") {
		t.Errorf("missing title, got:\n%s", text)
	}
	if !strings.Contains(text, "## 2024-07-08") {
		t.Errorf("missing day heading, got:\n%s", text)
	}
	if strings.Contains(text, "racine non archivée") {
		t.Errorf("unexpected orphan placeholder, got:\n%s", text)
	}
	if !strings.Contains(text, "- 06:01 carla    : première question") {
		t.Errorf("missing user line, got:\n%s", text)
	}
	if !strings.Contains(text, "- 06:01 assistant: une réponse") {
		t.Errorf("missing assistant line, got:\n%s", text)
	}
}
