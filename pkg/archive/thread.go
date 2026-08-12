package archive

import (
	"bytes"
	"fmt"
	"time"
)

// Thread is a single conversation (thread) of an AI chat export, ready to be
// archived as one JSONL and one Markdown object, named after the thread
// creation datetime.
type Thread struct {
	Source      string
	DisplayName string
	CreatedAt   time.Time
	Entries     []*Entry
}

// threadObjectPath computes the object storage path prefix shared by the
// JSONL and Markdown layers of a thread:
// <source>/<account>/<year>/<datetime>
func threadObjectPath(source, account string, year int, datetime string) (string, error) {
	if source == "" {
		return "", fmt.Errorf("invalid empty source")
	}
	if datetime == "" {
		return "", fmt.Errorf("invalid empty thread datetime")
	}
	acc := "-"
	if account != "" {
		acc = slugify(account)
	}
	return fmt.Sprintf("%s/%s/%d/%s", source, acc, year, datetime), nil
}

// JSONLThreadObjectKey computes the object storage key of a thread in the
// import layout: jsonl/<source>/<account>/<year>/<datetime>.jsonl
func JSONLThreadObjectKey(source, account string, year int, datetime string) (string, error) {
	path, err := threadObjectPath(source, account, year, datetime)
	if err != nil {
		return "", err
	}
	return "jsonl/" + path + ".jsonl", nil
}

// MarkdownThreadObjectKey computes the object storage key of a thread in the
// import layout: markdown/<source>/<account>/<year>/<datetime>.md
func MarkdownThreadObjectKey(source, account string, year int, datetime string) (string, error) {
	path, err := threadObjectPath(source, account, year, datetime)
	if err != nil {
		return "", err
	}
	return "markdown/" + path + ".md", nil
}

// MarshalThreadJSONL marshals the thread entries as JSONL lines, in
// chronological order (oldest first).
func MarshalThreadJSONL(entries []*Entry) ([]byte, error) {
	sorted := sortedEntries(entries)
	var buf bytes.Buffer
	for _, e := range sorted {
		line, err := MarshalEntry(e)
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// RenderThreadMarkdown renders the Markdown content of a whole thread
// (conversation), grouped by day, without splitting it across months. The
// title carries the thread's first day and the conversation title.
func RenderThreadMarkdown(entries []*Entry, meta ConversationMeta) ([]byte, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	sorted := sortedEntries(entries)
	day := dayOf(sorted[0])
	var buf bytes.Buffer
	renderEntries(&buf, sorted, day+" - "+conversationTitle(meta))
	return buf.Bytes(), nil
}
