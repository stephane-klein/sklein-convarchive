package claude

import (
	"strings"
	"testing"
	"time"
)

const sampleExport = `[
  {
    "uuid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1",
    "name": "Conversation de demonstration",
    "summary": "",
    "created_at": "2024-07-08T06:01:47.313862+00:00",
    "model": "claude-sonnet-4-5-20250929",
    "platform": "CLAUDE_AI",
    "chat_messages": [
      {
        "uuid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2",
        "text": "",
        "content": [
          {
            "type": "text",
            "text": "Quelle est la capitale de la Norvege ?"
          }
        ],
        "sender": "human",
        "index": 0,
        "created_at": "2024-07-08T06:01:47.697664+00:00",
        "parent_message_uuid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa0"
      },
      {
        "uuid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa3",
        "text": "",
        "content": [
          {
            "type": "text",
            "text": "La capitale de la Norvege est Oslo."
          }
        ],
        "sender": "assistant",
        "index": 1,
        "created_at": "2024-07-08T06:01:56.188293+00:00",
        "parent_message_uuid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2"
      },
      {
        "uuid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa4",
        "text": "",
        "content": [
          {
            "type": "tool_use",
            "name": "web_search",
            "input": {"query": "recherche de test"}
          },
          {
            "type": "tool_result",
            "name": "web_search",
            "is_error": false,
            "content": []
          },
          {
            "type": "token_budget"
          }
        ],
        "sender": "assistant",
        "index": 2,
        "created_at": "2024-07-08T06:02:00.000000+00:00",
        "parent_message_uuid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa3",
        "attachments": [
          {"id": "a1", "file_name": "note.txt", "file_type": "txt"}
        ]
      }
    ]
  }
]`

func TestParse(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}

	threads, err := Parse(strings.NewReader(sampleExport), ExportOptions{Owner: "carla", Loc: loc})
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}

	thread := threads[0]
	if thread.Source != "claude" {
		t.Errorf("source = %q, want claude", thread.Source)
	}
	if thread.DisplayName != "Conversation de demonstration" {
		t.Errorf("display name = %q", thread.DisplayName)
	}
	if got := thread.CreatedAt.Truncate(time.Second).In(time.UTC); got != time.Date(2024, 7, 8, 6, 1, 47, 0, time.UTC) {
		t.Errorf("created at = %v, want 2024-07-08T06:01:47Z", got)
	}

	if len(thread.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(thread.Entries))
	}

	human := thread.Entries[0]
	if human.Source != "claude" {
		t.Errorf("entry source = %q, want claude", human.Source)
	}
	if human.Author != "carla" {
		t.Errorf("human author = %q, want carla", human.Author)
	}
	if human.Timestamp != "2024-07-08T08:01:47+02:00" {
		t.Errorf("human timestamp = %q, want local offset", human.Timestamp)
	}
	if human.Content != "Quelle est la capitale de la Norvege ?" {
		t.Errorf("human content = %q", human.Content)
	}
	if human.ThreadID != "" {
		t.Errorf("thread id = %q, want empty (flat thread)", human.ThreadID)
	}

	assistant := thread.Entries[1]
	if assistant.Author != "assistant" {
		t.Errorf("assistant author = %q, want assistant", assistant.Author)
	}

	tools := thread.Entries[2]
	wantContent := `[tool_use: web_search] {"query":"recherche de test"}

[tool_result: web_search]

[attachment: note.txt]`
	if tools.Content != wantContent {
		t.Errorf("tool content =\n%q\nwant\n%q", tools.Content, wantContent)
	}
	if v := tools.Metadata["model"]; v != "claude-sonnet-4-5-20250929" {
		t.Errorf("metadata model = %v", v)
	}
	if tools.Raw == nil {
		t.Error("raw message missing")
	}
}

func TestParseSkipsNoContent(t *testing.T) {
	empty := `[
	  {
	    "uuid": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa5",
	    "name": "Empty",
	    "created_at": "2024-07-08T06:01:47.000000+00:00",
	    "chat_messages": []
	  }
	]`
	threads, err := Parse(strings.NewReader(empty), ExportOptions{Owner: "carla"})
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(threads) != 1 || len(threads[0].Entries) != 0 {
		t.Fatalf("unexpected threads: %+v", threads)
	}
}

func TestParseInvalidJSON(t *testing.T) {
	_, err := Parse(strings.NewReader("not json"), ExportOptions{})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
