package chatgpt

import (
	"strings"
	"testing"
	"time"
)

// A conversation with one regenerated branch (the user message has two
// assistant children), a code block and a multimodal image part.
const sampleExport = `[
  {
    "title": "Conversation de demonstration",
    "create_time": 1719590400.0,
    "conversation_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab1",
    "current_node": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab4",
    "mapping": {
      "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab2": {
        "id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab2",
        "message": null,
        "parent": null,
        "children": ["aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab1"]
      },
      "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab1": {
        "id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab1",
        "message": {
          "id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab1",
          "author": {"role": "user", "name": null, "metadata": {}},
          "create_time": 1719590400.0,
          "content": {
            "content_type": "multimodal_text",
            "parts": [
              {"content_type": "image_asset_pointer", "asset_pointer": "file-service://file-TEST0000000000000000000000"},
              "Decris une image de test"
            ]
          },
          "metadata": {"request_id": "r1"}
        },
        "parent": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab2",
        "children": [
          "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab3",
          "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab4"
        ]
      },
      "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab3": {
        "id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab3",
        "message": {
          "id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab3",
          "author": {"role": "assistant", "name": null, "metadata": {}},
          "create_time": 1719590401.0,
          "content": {
            "content_type": "text",
            "parts": ["Premiere reponse de test"]
          },
          "metadata": {"model_slug": "gpt-4o"}
        },
        "parent": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab1",
        "children": []
      },
      "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab4": {
        "id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab4",
        "message": {
          "id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab4",
          "author": {"role": "assistant", "name": null, "metadata": {}},
          "create_time": 1719590402.0,
          "content": {
            "content_type": "code",
            "language": "python",
            "text": "print('ok')"
          },
          "metadata": {"model_slug": "gpt-4o"}
        },
        "parent": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab1",
        "children": []
      },
      "shell-no-time": {
        "id": "shell-no-time",
        "message": {
          "id": "shell-no-time",
          "author": {"role": "system", "name": null, "metadata": {}},
          "create_time": null,
          "content": {"content_type": "text", "parts": ["system prompt"]},
          "metadata": {}
        },
        "parent": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaab2",
        "children": []
      }
    }
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
	if thread.Source != "chatgpt" {
		t.Errorf("source = %q, want chatgpt", thread.Source)
	}
	if thread.DisplayName != "Conversation de demonstration" {
		t.Errorf("display name = %q", thread.DisplayName)
	}

	// The shell node and the untimestamped system message are dropped; both
	// regenerated branches are kept.
	if len(thread.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(thread.Entries))
	}

	user := thread.Entries[0]
	if user.Author != "carla" {
		t.Errorf("user author = %q, want carla", user.Author)
	}
	wantContent := "[image: file-service://file-TEST0000000000000000000000]\nDecris une image de test"
	if user.Content != wantContent {
		t.Errorf("user content =\n%q\nwant\n%q", user.Content, wantContent)
	}
	if user.Timestamp != "2024-06-28T18:00:00+02:00" {
		t.Errorf("user timestamp = %q", user.Timestamp)
	}
	if user.ThreadID != "" {
		t.Errorf("thread id = %q, want empty (flat thread)", user.ThreadID)
	}

	first := thread.Entries[1]
	if first.Author != "assistant" {
		t.Errorf("branch author = %q, want assistant", first.Author)
	}
	if first.Content != "Premiere reponse de test" {
		t.Errorf("branch content = %q", first.Content)
	}
	if v := first.Metadata["model_slug"]; v != "gpt-4o" {
		t.Errorf("metadata model_slug = %v", v)
	}

	code := thread.Entries[2]
	if code.Author != "assistant" {
		t.Errorf("code author = %q, want assistant", code.Author)
	}
	if code.Content != "```python\nprint('ok')\n```" {
		t.Errorf("code content = %q", code.Content)
	}
}

func TestParseToolMessages(t *testing.T) {
	export := `[
	  {
	    "title": "Tool",
	    "create_time": 1719590403.0,
	    "conversation_id": "c1",
	    "mapping": {
	      "root": {"id": "root", "message": null, "parent": null, "children": ["m1"]},
	      "m1": {
	        "id": "m1",
	        "message": {
	          "id": "m1",
	          "author": {"role": "user", "name": null},
	          "create_time": 1719590404.0,
	          "content": {"content_type": "text", "parts": ["bonjour"]}
	        },
	        "parent": "root",
	        "children": ["m2"]
	      },
	      "m2": {
	        "id": "m2",
	        "message": {
	          "id": "m2",
	          "author": {"role": "tool", "name": "python"},
	          "create_time": 1719590405.0,
	          "content": {"content_type": "execution_output", "text": "'/tmp/result.txt'"}
	        },
	        "parent": "m1",
	        "children": ["m3"]
	      },
	      "m3": {
	        "id": "m3",
	        "message": {
	          "id": "m3",
	          "author": {"role": "assistant", "name": null},
	          "create_time": 1719590406.0,
	          "content": {"content_type": "system_error", "name": "tool_error"}
	        },
	        "parent": "m2",
	        "children": []
	      }
	    }
	  }
	]`
	threads, err := Parse(strings.NewReader(export), ExportOptions{Owner: "carla"})
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}
	if len(threads) != 1 {
		t.Fatalf("threads = %d, want 1", len(threads))
	}
	entries := threads[0].Entries
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	if entries[1].Author != "python" {
		t.Errorf("tool author = %q, want python", entries[1].Author)
	}
	if entries[1].Content != "'/tmp/result.txt'" {
		t.Errorf("tool content = %q", entries[1].Content)
	}
	if entries[2].Content != "[error: tool_error]" {
		t.Errorf("error content = %q", entries[2].Content)
	}
}

func TestParseInvalidJSON(t *testing.T) {
	_, err := Parse(strings.NewReader("not json"), ExportOptions{})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
