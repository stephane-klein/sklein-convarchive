package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/stephane-klein/sklein-convarchive/pkg/archive"
)

// Source is the archive source identifier of Claude.ai conversations.
const Source = "claude"

// ExportOptions configures how a Claude.ai export file is normalized.
type ExportOptions struct {
	// Owner is the identity attributed to human messages.
	Owner string
	// Loc is the timezone used to format entry timestamps.
	Loc *time.Location
}

type exportConversation struct {
	UUID         string        `json:"uuid"`
	Name         string        `json:"name"`
	Summary      string        `json:"summary"`
	CreatedAt    string        `json:"created_at"`
	Model        string        `json:"model"`
	Platform     string        `json:"platform"`
	ChatMessages []chatMessage `json:"chat_messages"`
}

type chatMessage struct {
	UUID              string         `json:"uuid"`
	Text              string         `json:"text"`
	Content           []contentBlock `json:"content"`
	Sender            string         `json:"sender"`
	Index             int            `json:"index"`
	CreatedAt         string         `json:"created_at"`
	ParentMessageUUID string         `json:"parent_message_uuid"`
	Attachments       []attachment   `json:"attachments"`
}

type contentBlock struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Name    string `json:"name"`
	Input   any    `json:"input"`
	Message string `json:"message"`
	IsError *bool  `json:"is_error"`
}

type attachment struct {
	ID               string `json:"id"`
	FileName         string `json:"file_name"`
	FileType         string `json:"file_type"`
	ExtractedContent string `json:"extracted_content"`
}

// Parse decodes a Claude.ai export file produced by the
// claude-chatgpt-backup-extension Firefox extension (an array of conversations
// with a chat_messages list) and returns one Thread per conversation.
func Parse(r io.Reader, opts ExportOptions) ([]*archive.Thread, error) {
	var convs []exportConversation
	if err := json.NewDecoder(r).Decode(&convs); err != nil {
		return nil, fmt.Errorf("failed to decode Claude export: %w", err)
	}
	loc := opts.Loc
	if loc == nil {
		loc = time.UTC
	}

	threads := make([]*archive.Thread, 0, len(convs))
	for i := range convs {
		conv := &convs[i]

		created, err := time.Parse(time.RFC3339, conv.CreatedAt)
		if err != nil {
			// Fall back to the first message's creation time when the
			// conversation-level timestamp is missing.
			for _, msg := range conv.ChatMessages {
				if t, err := time.Parse(time.RFC3339, msg.CreatedAt); err == nil {
					created = t
					break
				}
			}
		}

		thread := &archive.Thread{
			Source:      Source,
			DisplayName: conversationName(conv),
			CreatedAt:   created,
		}
		for j := range conv.ChatMessages {
			entry, err := normalizeMessage(conv, &conv.ChatMessages[j], opts, loc)
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
	if conv.Name != "" {
		return conv.Name
	}
	if conv.Summary != "" {
		return conv.Summary
	}
	return "Untitled conversation"
}

func normalizeMessage(conv *exportConversation, msg *chatMessage, opts ExportOptions, loc *time.Location) (*archive.Entry, error) {
	ts, err := time.Parse(time.RFC3339, msg.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("invalid message created_at %q: %w", msg.CreatedAt, err)
	}

	author := "assistant"
	switch msg.Sender {
	case "human":
		author = opts.Owner
	case "assistant":
		author = "assistant"
	default:
		if msg.Sender != "" {
			author = msg.Sender
		}
	}

	return &archive.Entry{
		Source:    Source,
		Timestamp: ts.In(loc).Format(time.RFC3339),
		Author:    author,
		Content:   renderContent(msg),
		Metadata: map[string]any{
			"conversation_uuid":   conv.UUID,
			"conversation_name":   conv.Name,
			"message_uuid":        msg.UUID,
			"sender":              msg.Sender,
			"model":               conv.Model,
			"platform":            conv.Platform,
			"parent_message_uuid": msg.ParentMessageUUID,
		},
		Raw: msg,
	}, nil
}

// renderContent renders a message as compact text: text blocks joined, tool
// calls and attachments as short annotations. The full structure stays in the
// entry Raw field.
func renderContent(msg *chatMessage) string {
	var parts []string
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case "tool_use":
			parts = append(parts, "[tool_use: "+block.Name+"] "+compactInput(block.Input))
		case "tool_result":
			s := "[tool_result: " + block.Name + "]"
			if block.IsError != nil && *block.IsError {
				s += " error"
			}
			parts = append(parts, s)
		case "token_budget":
			// no textual content
		}
	}
	for _, a := range msg.Attachments {
		parts = append(parts, "[attachment: "+a.FileName+"]")
	}
	if len(parts) == 0 && msg.Text != "" {
		parts = append(parts, msg.Text)
	}
	return strings.Join(parts, "\n\n")
}

func compactInput(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
