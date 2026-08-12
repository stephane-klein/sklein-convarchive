package browse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// RenderContent renders the plaintext content of a month object according to
// its layer: the markdown layer is shown as-is, the jsonl layer as a list of
// parsed entries (or pretty-printed JSON when pretty is true).
func RenderContent(layer string, data []byte, pretty, showRaw bool) string {
	switch layer {
	case LayerJSONL:
		if pretty {
			return renderPrettyJSONL(data, showRaw)
		}
		return renderCompactJSONL(data)
	case LayerMarkdown:
		return string(data)
	default:
		return string(data)
	}
}

// renderCompactJSONL renders each JSONL line as "timestamp  author: content",
// wrapping the content at wrapWidth and aligning continuation lines.
func renderCompactJSONL(data []byte) string {
	const wrapWidth = 100

	var b strings.Builder
	forEachLine(data, func(line []byte) {
		var e entryLine
		if err := json.Unmarshal(line, &e); err != nil {
			b.Write(line)
			b.WriteString("\n\n")
			return
		}

		ts := e.Timestamp
		if ts == "" {
			ts = "?"
		}
		author := e.Author
		if author == "" {
			author = "?"
		}

		prefix := fmt.Sprintf("%s  %s: ", ts, author)
		contentCol := utf8.RuneCountInString(prefix)

		if e.Content == "" {
			b.WriteString(strings.TrimRight(prefix, " "))
			b.WriteString("\n")
			return
		}

		wrapped := wrapText(e.Content, wrapWidth-contentCol)
		b.WriteString(prefix + wrapped[0])
		b.WriteString("\n")
		for _, w := range wrapped[1:] {
			b.WriteString(strings.Repeat(" ", contentCol) + w)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	})
	return strings.TrimRight(b.String(), "\n")
}

// renderPrettyJSONL pretty-prints each JSONL line as indented JSON, omitting
// the "raw" field by default (the raw Mattermost post is verbose and
// redundant). showRaw re-includes it.
func renderPrettyJSONL(data []byte, showRaw bool) string {
	var b strings.Builder
	forEachLine(data, func(line []byte) {
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			b.Write(line)
			b.WriteString("\n")
			return
		}
		if !showRaw {
			delete(m, "raw")
		}

		pretty, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			b.Write(line)
			b.WriteString("\n")
			return
		}
		b.Write(pretty)
		b.WriteString("\n\n")
	})
	return strings.TrimRight(b.String(), "\n")
}

// entryLine is the subset of the common schema needed for the compact view.
type entryLine struct {
	Timestamp string `json:"timestamp"`
	Author    string `json:"author"`
	Content   string `json:"content"`
}

// forEachLine calls fn for each non-empty line of data (without the trailing
// newline).
func forEachLine(data []byte, fn func(line []byte)) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		fn(line)
	}
}

// wrapText wraps a single line of text at the given width, breaking on spaces.
func wrapText(line string, width int) []string {
	if width <= 0 {
		width = 1
	}
	if utf8.RuneCountInString(line) <= width {
		return []string{line}
	}

	var lines []string
	current := ""
	for _, w := range strings.Fields(line) {
		switch {
		case current == "":
			current = w
		case utf8.RuneCountInString(current)+1+utf8.RuneCountInString(w) > width:
			lines = append(lines, current)
			current = w
		default:
			current += " " + w
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}
