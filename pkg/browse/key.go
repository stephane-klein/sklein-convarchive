package browse

import (
	"fmt"
	"strings"
)

// Layers of the archive layout.
const (
	LayerJSONL    = "jsonl"
	LayerMarkdown = "markdown"
)

// Object describes a single archived object, derived from its S3 key.
type Object struct {
	Layer        string // "jsonl" or "markdown"
	Source       string // e.g. "mattermost"
	Team         string // team slug, or pseudo-team ("-", "direct-messages", ...)
	Conversation string // channel slug or DM interlocutor slug
	Year         string
	Month        string // "2018-03"
	Compressed   bool   // carries the ".zst" suffix
	Encrypted    bool   // carries the ".age" suffix
	Key          string // full object key
}

// ParseObjectKey parses an object key of the layout
//
//	<layer>/<source>/<team>/<conversation>/<year>/<month>.<ext>[.zst][.age]
//
// or of the AI export thread layout, which has no conversation segment:
//
//	<layer>/<source>/<account>/<year>/<datetime>.<ext>[.zst][.age]
//
// where <ext> is ".jsonl" for the jsonl layer and ".md" for the markdown layer.
// The ".age" and ".zst" suffixes are optional and set the corresponding flags.
// The Conversation field is empty for the thread layout.
func ParseObjectKey(key string) (*Object, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 6 && len(parts) != 5 {
		return nil, fmt.Errorf("invalid object key %q: expected 5 or 6 path segments", key)
	}

	layer := parts[0]
	if layer != LayerJSONL && layer != LayerMarkdown {
		return nil, fmt.Errorf("invalid object key %q: unsupported layer %q", key, layer)
	}

	month, compressed, encrypted, err := parseFilename(layer, parts[len(parts)-1])
	if err != nil {
		return nil, fmt.Errorf("invalid object key %q: %w", key, err)
	}

	obj := &Object{
		Layer:      layer,
		Source:     parts[1],
		Team:       parts[2],
		Year:       parts[3],
		Month:      month,
		Compressed: compressed,
		Encrypted:  encrypted,
		Key:        key,
	}
	if len(parts) == 6 {
		obj.Conversation = parts[3]
		obj.Year = parts[4]
	}
	return obj, nil
}

// parseFilename strips the optional ".age" and ".zst" suffixes and the layer
// extension, returning the month ("2018-03") and the detected flags.
func parseFilename(layer, filename string) (month string, compressed, encrypted bool, err error) {
	name := filename
	if strings.HasSuffix(name, ".age") {
		encrypted = true
		name = strings.TrimSuffix(name, ".age")
	}
	if strings.HasSuffix(name, ".zst") {
		compressed = true
		name = strings.TrimSuffix(name, ".zst")
	}

	ext := ".jsonl"
	if layer == LayerMarkdown {
		ext = ".md"
	}
	if !strings.HasSuffix(name, ext) {
		return "", false, false, fmt.Errorf("unexpected filename %q", filename)
	}
	return strings.TrimSuffix(name, ext), compressed, encrypted, nil
}
