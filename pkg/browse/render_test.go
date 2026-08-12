package browse

import (
	"strings"
	"testing"
)

func TestRenderCompactJSONL(t *testing.T) {
	data := []byte(`{"source":"mattermost","timestamp":"2017-08-01T10:00:00+02:00","author":"carla","content":"bonjour","thread_id":"","metadata":{},"raw":{"x":1}}
{"source":"mattermost","timestamp":"2017-08-01T10:01:00+02:00","author":"zack","content":"hello world","thread_id":"","metadata":{}}`)

	got := RenderContent(LayerJSONL, data, false, false)
	want := "2017-08-01T10:00:00+02:00  carla: bonjour\n\n2017-08-01T10:01:00+02:00  zack: hello world"
	if got != want {
		t.Errorf("renderCompactJSONL = %q, want %q", got, want)
	}
}

func TestRenderCompactJSONLWrapsLongContent(t *testing.T) {
	long := strings.Repeat("mot ", 60) // 240 chars
	data := []byte(`{"timestamp":"2017-08-01T10:00:00+02:00","author":"carla","content":"` + strings.TrimSpace(long) + `"}`)
	got := RenderContent(LayerJSONL, data, false, false)
	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("long content not wrapped: %d lines", len(lines))
	}
	// Continuation lines must be indented under the message column.
	if !strings.HasPrefix(lines[1], " ") {
		t.Errorf("continuation line not aligned: %q", lines[1])
	}
}

func TestRenderPrettyJSONLOmitsRawByDefault(t *testing.T) {
	data := []byte(`{"source":"mattermost","timestamp":"2017-08-01T10:00:00+02:00","author":"carla","content":"bonjour","raw":{"message":"verbose"}}`)
	got := RenderContent(LayerJSONL, data, true, false)
	if strings.Contains(got, "verbose") {
		t.Errorf("raw field should be omitted by default, got:\n%s", got)
	}
	if !strings.Contains(got, `"content": "bonjour"`) {
		t.Errorf("pretty JSON missing content field:\n%s", got)
	}
}

func TestRenderPrettyJSONLWithRaw(t *testing.T) {
	data := []byte(`{"source":"mattermost","timestamp":"2017-08-01T10:00:00+02:00","author":"carla","content":"bonjour","raw":{"message":"verbose"}}`)
	got := RenderContent(LayerJSONL, data, true, true)
	if !strings.Contains(got, "verbose") {
		t.Errorf("raw field should be present when showRaw=true:\n%s", got)
	}
}

func TestRenderMarkdownPassthrough(t *testing.T) {
	data := []byte("# titre\n\ntexte")
	if got := RenderContent(LayerMarkdown, data, true, true); got != string(data) {
		t.Errorf("markdown layer should be shown as-is, got %q", got)
	}
}

func TestRenderContentHandlesMalformedLines(t *testing.T) {
	data := []byte("not json at all\n")
	if got := RenderContent(LayerJSONL, data, false, false); got != "not json at all" {
		t.Errorf("malformed line should be shown raw, got %q", got)
	}
}
