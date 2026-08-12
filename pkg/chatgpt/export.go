package chatgpt

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/stephane-klein/sklein-convarchive/pkg/archive"
)

// Source is the archive source identifier of ChatGPT conversations.
const Source = "chatgpt"

// ExportOptions configures how a ChatGPT export file is normalized.
type ExportOptions struct {
	// Owner is the identity attributed to user messages.
	Owner string
	// Loc is the timezone used to format entry timestamps.
	Loc *time.Location
}

type exportConversation struct {
	Title            string                 `json:"title"`
	CreateTime       float64                `json:"create_time"`
	ConversationID   string                 `json:"conversation_id"`
	DefaultModelSlug string                 `json:"default_model_slug"`
	Mapping          map[string]mappingNode `json:"mapping"`
}

type mappingNode struct {
	ID       string   `json:"id"`
	Message  *message `json:"message"`
	Parent   *string  `json:"parent"`
	Children []string `json:"children"`
}

type message struct {
	ID         string         `json:"id"`
	Author     *author        `json:"author"`
	CreateTime *float64       `json:"create_time"`
	Content    *content       `json:"content"`
	Metadata   map[string]any `json:"metadata"`
}

type author struct {
	Role string  `json:"role"`
	Name *string `json:"name"`
}

type content struct {
	ContentType string            `json:"content_type"`
	Parts       []json.RawMessage `json:"parts"`
	Language    string            `json:"language"`
	Text        string            `json:"text"`
	Name        string            `json:"name"`
}

// Parse decodes a ChatGPT export file produced by the
// claude-chatgpt-backup-extension Firefox extension (an array of conversations
// with a mapping tree) and returns one Thread per conversation. Every message
// node of the tree is kept — including regenerated branches — except the shell
// root and the untimestamped system messages; the final ordering is
// chronological (the archive layer sorts by timestamp).
func Parse(r io.Reader, opts ExportOptions) ([]*archive.Thread, error) {
	var convs []exportConversation
	if err := json.NewDecoder(r).Decode(&convs); err != nil {
		return nil, fmt.Errorf("failed to decode ChatGPT export: %w", err)
	}
	loc := opts.Loc
	if loc == nil {
		loc = time.UTC
	}

	threads := make([]*archive.Thread, 0, len(convs))
	for i := range convs {
		conv := &convs[i]
		thread := &archive.Thread{
			Source:      Source,
			DisplayName: conversationName(conv),
			CreatedAt:   unixFloat(conv.CreateTime),
		}
		for _, msg := range traversalOrder(conv.Mapping) {
			if msg.CreateTime == nil || msg.Author == nil || msg.Author.Role == "" {
				continue
			}
			entry, err := normalizeMessage(conv, msg, opts, loc)
			if err != nil {
				return nil, err
			}
			thread.Entries = append(thread.Entries, entry)
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

func conversationName(conv *exportConversation) string {
	if conv.Title != "" {
		return conv.Title
	}
	return "Untitled conversation"
}

// traversalOrder walks the mapping tree from the root node in depth-first
// order, following the children in their listed order. This preserves the
// reading order for messages sharing a timestamp.
func traversalOrder(mapping map[string]mappingNode) []*message {
	var order []*message

	var visit func(id string)
	visit = func(id string) {
		node, ok := mapping[id]
		if !ok {
			return
		}
		if node.Message != nil {
			order = append(order, node.Message)
		}
		for _, child := range node.Children {
			visit(child)
		}
	}

	for _, node := range mapping {
		if node.Parent == nil {
			visit(node.ID)
			break
		}
	}
	return order
}

func normalizeMessage(conv *exportConversation, msg *message, opts ExportOptions, loc *time.Location) (*archive.Entry, error) {
	ts := unixFloat(*msg.CreateTime)
	role := ""
	if msg.Author != nil {
		role = msg.Author.Role
	}

	modelSlug := ""
	if v, ok := msg.Metadata["model_slug"].(string); ok {
		modelSlug = v
	}

	return &archive.Entry{
		Source:    Source,
		Timestamp: ts.In(loc).Format(time.RFC3339),
		Author:    authorLabel(msg, opts.Owner),
		Content:   renderContent(msg.Content),
		Metadata: map[string]any{
			"conversation_id": conv.ConversationID,
			"title":           conv.Title,
			"message_id":      msg.ID,
			"role":            role,
			"model_slug":      modelSlug,
		},
		Raw: msg,
	}, nil
}

func authorLabel(msg *message, owner string) string {
	if msg.Author == nil {
		return ""
	}
	switch msg.Author.Role {
	case "user":
		return owner
	case "assistant":
		return "assistant"
	case "system":
		return "system"
	case "tool":
		if msg.Author.Name != nil && *msg.Author.Name != "" {
			return *msg.Author.Name
		}
		return "tool"
	}
	if msg.Author.Role != "" {
		return msg.Author.Role
	}
	return "unknown"
}

// renderContent renders a message as compact text: text parts joined, code in
// fences, tool and image artifacts as short annotations. The full structure
// stays in the entry Raw field.
func renderContent(c *content) string {
	if c == nil {
		return ""
	}
	switch c.ContentType {
	case "text":
		return joinParts(c.Parts)
	case "multimodal_text":
		return joinParts(c.Parts)
	case "code":
		return "```" + c.Language + "\n" + c.Text + "\n```"
	case "execution_output":
		if c.Text != "" {
			return c.Text
		}
		return joinParts(c.Parts)
	case "reasoning_recap":
		if c.Text != "" {
			return "[reasoning] " + c.Text
		}
		return "[reasoning]"
	case "thoughts":
		return "[reasoning]"
	case "tether_browsing_display":
		return "[web browser]"
	case "tether_quote":
		return "[web page]"
	case "system_error":
		if c.Name != "" {
			return "[error: " + c.Name + "]"
		}
		return "[error]"
	default:
		// model_editable_context and unknown types carry no conversation text.
		return joinParts(c.Parts)
	}
}

func joinParts(parts []json.RawMessage) string {
	var out []string
	for _, p := range parts {
		var s string
		if err := json.Unmarshal(p, &s); err == nil {
			if s != "" {
				out = append(out, s)
			}
			continue
		}
		var obj struct {
			ContentType  string `json:"content_type"`
			AssetPointer string `json:"asset_pointer"`
		}
		if err := json.Unmarshal(p, &obj); err == nil && obj.ContentType == "image_asset_pointer" {
			out = append(out, "[image: "+obj.AssetPointer+"]")
		}
	}
	return strings.Join(out, "\n")
}

func unixFloat(f float64) time.Time {
	sec := int64(f)
	nsec := int64((f - float64(sec)) * 1e9)
	return time.Unix(sec, nsec)
}
