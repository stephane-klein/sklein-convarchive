package archive

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// ConversationMeta carries the context of a conversation needed to render the
// Markdown title and the object storage path.
type ConversationMeta struct {
	Source       string
	TeamName     string
	Type         string   // O (public), P (private), D (direct), G (group)
	DisplayName  string   // channel display name, or "@user, @user" for D/G
	ChannelName  string   // technical channel name (fallback for display)
	Participants []string // usernames without '@' (for the title), D/G only
}

// MarkdownBuffer accumulates entries grouped by conversation and month,
// producing a human-readable Markdown file per month in an IRC-like format.
type MarkdownBuffer struct {
	months map[string]*monthGroup
}

type monthGroup struct {
	meta    ConversationMeta
	entries []*Entry
}

// NewMarkdownBuffer creates an empty markdown buffer.
func NewMarkdownBuffer() *MarkdownBuffer {
	return &MarkdownBuffer{months: make(map[string]*monthGroup)}
}

// Add appends an entry to the buffer of the conversation+month derived from
// the entry timestamp.
func (b *MarkdownBuffer) Add(entry *Entry, meta ConversationMeta) error {
	ts, err := time.Parse(time.RFC3339, entry.Timestamp)
	if err != nil {
		return fmt.Errorf("failed to parse entry timestamp %q: %w", entry.Timestamp, err)
	}
	month := ts.Format("2006-01")
	key := MonthKey(meta.TeamName, meta.DisplayName, month)

	group, ok := b.months[key]
	if !ok {
		group = &monthGroup{meta: meta}
		b.months[key] = group
	}
	group.entries = append(group.entries, entry)
	return nil
}

// Keys returns the sorted list of conversation+month keys.
func (b *MarkdownBuffer) Keys() []string {
	keys := make([]string, 0, len(b.months))
	for k := range b.months {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// LineCount returns the number of buffered entries for a key.
func (b *MarkdownBuffer) LineCount(key string) int {
	if g, ok := b.months[key]; ok {
		return len(g.entries)
	}
	return 0
}

// Render produces the Markdown content for a conversation+month key.
func (b *MarkdownBuffer) Render(key string) ([]byte, error) {
	group, ok := b.months[key]
	if !ok || len(group.entries) == 0 {
		return nil, nil
	}

	entries := sortedEntries(group.entries)
	month := monthFromEntries(entries)

	var buf bytes.Buffer
	renderEntries(&buf, entries, month+" - "+conversationTitle(group.meta))
	return buf.Bytes(), nil
}

// renderEntries writes the IRC-like body shared by the monthly and the
// per-thread Markdown renderers: a title, then the entries grouped by day,
// with blank lines between author turns.
func renderEntries(buf *bytes.Buffer, entries []*Entry, title string) {
	usernameWidth := maxUsernameWidth(entries)

	fmt.Fprintf(buf, "# %s\n\n", title)

	// Group the entries into threads: each root followed by its replies
	// (chronologically), then orphan replies.
	units := buildThreadUnits(entries)

	prevAuthor := ""
	var prevTime time.Time
	currentDay := ""
	first := true
	for _, u := range units {
		day := dayOf(u.entry)

		if day != currentDay {
			if !first {
				buf.WriteString("\n")
			}
			fmt.Fprintf(buf, "## %s\n\n", day)
			currentDay = day
			prevAuthor = ""
			prevTime = time.Time{}
		}

		if u.placeholder {
			// Orphan reply: its root is outside the archived period.
			buf.WriteString("- [racine non archivée]\n")
			prevAuthor = ""
			prevTime = time.Time{}
			first = false
			continue
		}

		ts := parseTime(u.entry.Timestamp)
		hour := ts.Format("15:04")

		// Blank line when the author changes or the gap exceeds 5 minutes.
		if prevAuthor != "" && (u.entry.Author != prevAuthor || gap(prevTime, ts) > 5*time.Minute) {
			buf.WriteString("\n")
		}

		lines := renderMessageLines(u, hour, usernameWidth)
		for _, line := range lines {
			buf.WriteString(line)
			buf.WriteString("\n")
		}

		prevAuthor = u.entry.Author
		prevTime = ts
		first = false
	}
}

// FlushKey renders and uploads a single conversation+month as one Markdown
// object and frees its buffered entries. It is a no-op when the key has no
// entries.
func (b *MarkdownBuffer) FlushKey(ctx context.Context, uploader ObjectPutter, key string) error {
	group, ok := b.months[key]
	if !ok || len(group.entries) == 0 {
		return nil
	}

	teamName, displayName, month, err := ParseMonthKey(key)
	if err != nil {
		return err
	}

	objKey, err := MarkdownObjectKey(teamName, displayName, month)
	if err != nil {
		return err
	}

	content, err := b.Render(key)
	if err != nil {
		return err
	}

	if err := uploader.Put(ctx, objKey, content, "text/markdown"); err != nil {
		return err
	}

	delete(b.months, key)
	return nil
}

// Flush renders and uploads each buffered conversation+month as a single
// Markdown object in the layout
// markdown/mattermost/<team>/<display>/<year>/<month>.md.
func (b *MarkdownBuffer) Flush(ctx context.Context, uploader ObjectPutter) error {
	for _, key := range b.Keys() {
		if err := b.FlushKey(ctx, uploader, key); err != nil {
			return err
		}
	}
	return nil
}

// conversationTitle builds the file title description from the meta.
func conversationTitle(meta ConversationMeta) string {
	switch meta.Type {
	case "D":
		names := sortedCopy(meta.Participants)
		if len(names) == 0 {
			return "Message direct"
		}
		return "Message direct entre " + strings.Join(names, " et ")
	case "G":
		names := sortedCopy(meta.Participants)
		if len(names) == 0 {
			return "Message de groupe"
		}
		return "Message de groupe " + strings.Join(names, ", ")
	default:
		display := meta.DisplayName
		if display == "" {
			display = meta.ChannelName
		}
		if meta.Source == "claude" || meta.Source == "chatgpt" {
			// AI conversation exports have no channel notion; the title is the
			// conversation name itself.
			if display == "" {
				return "Conversation"
			}
			return display
		}
		return "Canal " + display
	}
}

// threadUnit is a root message together with its replies, or a placeholder for
// an orphan reply.
type threadUnit struct {
	entry       *Entry
	reply       bool
	placeholder bool
}

// buildThreadUnits groups entries into threads: each root followed by its
// replies (sorted chronologically). Replies whose root is absent from the
// entries become placeholder units. Units are ordered by their root's first
// appearance (chronological).
func buildThreadUnits(entries []*Entry) []threadUnit {
	postID := func(e *Entry) string {
		if v, ok := e.Metadata["post_id"].(string); ok {
			return v
		}
		return ""
	}

	rootByID := map[string]*Entry{}
	repliesByRoot := map[string][]*Entry{}
	var orderedRoots []*Entry

	for _, e := range entries {
		if postID(e) == e.ThreadID {
			rootByID[e.ThreadID] = e
			orderedRoots = append(orderedRoots, e)
		} else {
			repliesByRoot[e.ThreadID] = append(repliesByRoot[e.ThreadID], e)
		}
	}

	var units []threadUnit
	emittedReplies := map[*Entry]bool{}

	for _, root := range orderedRoots {
		units = append(units, threadUnit{entry: root})
		replies := sortedEntries(repliesByRoot[root.ThreadID])
		for _, r := range replies {
			units = append(units, threadUnit{entry: r, reply: true})
			emittedReplies[r] = true
		}
	}

	// Orphan replies: their root is not present in the archived entries.
	var orphans []*Entry
	for rootID, replies := range repliesByRoot {
		if _, ok := rootByID[rootID]; ok {
			continue
		}
		orphans = append(orphans, replies...)
	}
	for _, o := range sortedEntries(orphans) {
		if emittedReplies[o] {
			continue
		}
		units = append(units, threadUnit{entry: o, reply: true, placeholder: true})
	}

	return units
}

// renderMessageLines renders a message as a bullet item, wrapping text at
// wrapWidth and aligning continuation lines under the message column.
func renderMessageLines(u threadUnit, hour string, usernameWidth int) []string {
	const wrapWidth = 100

	bullet := "- "
	if u.reply {
		bullet = "  - "
	}

	username := u.entry.Author
	userCol := padRight(username, usernameWidth)

	prefix := bullet + hour + " " + userCol + ": "
	contentCol := utf8.RuneCountInString(prefix)

	content := u.entry.Content
	if content == "" {
		return []string{strings.TrimRight(prefix, " ")}
	}

	wrapped := wrapContent(content, wrapWidth-contentCol)

	var lines []string
	lines = append(lines, prefix+wrapped[0])
	for _, w := range wrapped[1:] {
		lines = append(lines, strings.Repeat(" ", contentCol)+w)
	}
	return lines
}

// wrapContent wraps plain text at the given width while preserving code blocks
// (lines inside ``` fences) without wrapping.
func wrapContent(content string, width int) []string {
	if width <= 0 {
		width = 1
	}

	var out []string
	inCode := false
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
			out = append(out, line)
			continue
		}
		if inCode {
			out = append(out, line)
			continue
		}
		out = append(out, wrapText(line, width)...)
	}
	return out
}

// wrapText wraps a single line of text at the given width, breaking on spaces.
func wrapText(line string, width int) []string {
	if utf8.RuneCountInString(line) <= width {
		return []string{line}
	}

	var words []string
	for _, w := range strings.Split(line, " ") {
		words = append(words, w)
	}

	var lines []string
	current := ""
	for _, w := range words {
		if current == "" {
			current = w
			continue
		}
		if utf8.RuneCountInString(current)+1+utf8.RuneCountInString(w) > width {
			lines = append(lines, current)
			current = w
			continue
		}
		current += " " + w
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func sortedEntries(entries []*Entry) []*Entry {
	out := make([]*Entry, len(entries))
	copy(out, entries)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp < out[j].Timestamp
	})
	return out
}

func maxUsernameWidth(entries []*Entry) int {
	maxW := 0
	for _, e := range entries {
		if w := utf8.RuneCountInString(e.Author); w > maxW {
			maxW = w
		}
	}
	return maxW
}

func sortedCopy(items []string) []string {
	out := make([]string, len(items))
	copy(out, items)
	sort.Strings(out)
	return out
}

func padRight(s string, width int) string {
	if n := width - utf8.RuneCountInString(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

func dayOf(e *Entry) string {
	ts, err := time.Parse(time.RFC3339, e.Timestamp)
	if err != nil {
		return ""
	}
	return ts.Format("2006-01-02")
}

func parseTime(ts string) time.Time {
	t, _ := time.Parse(time.RFC3339, ts)
	return t
}

func gap(a, b time.Time) time.Duration {
	if b.Before(a) {
		return a.Sub(b)
	}
	return b.Sub(a)
}

func monthFromEntries(entries []*Entry) string {
	ts, err := time.Parse(time.RFC3339, entries[0].Timestamp)
	if err != nil {
		return ""
	}
	return ts.Format("2006-01")
}

// MonthKey builds the buffer key of a conversation+month: team|display|month.
func MonthKey(teamName, displayName, month string) string {
	return teamName + "|" + displayName + "|" + month
}

func ParseMonthKey(key string) (teamName, displayName, month string, err error) {
	parts := strings.Split(key, "|")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("invalid month key %q", key)
	}
	return parts[0], parts[1], parts[2], nil
}

// slugify converts a string into a safe path segment: lowercase, spaces and
// runs of non-alphanumeric characters replaced by a single '-'.
func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else {
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := b.String()
	return strings.Trim(out, "-")
}

// conversationObjectPath computes the object storage path prefix shared by
// the JSONL and Markdown layers for a conversation+month:
// mattermost/<team>/<display-slug>/<year>/<month>
func conversationObjectPath(teamName, displayName, month string) (string, error) {
	ts, err := time.Parse("2006-01", month)
	if err != nil {
		return "", fmt.Errorf("invalid month %q: %w", month, err)
	}

	team := "-"
	if teamName != "" {
		team = slugify(teamName)
	}
	display := "-"
	if displayName != "" {
		display = slugify(displayName)
	}

	return fmt.Sprintf("mattermost/%s/%s/%d/%s",
		team, display, ts.Year(), month), nil
}

// MarkdownObjectKey computes the object storage key for a conversation+month:
// markdown/mattermost/<team>/<display-slug>/<year>/<month>.md
func MarkdownObjectKey(teamName, displayName, month string) (string, error) {
	path, err := conversationObjectPath(teamName, displayName, month)
	if err != nil {
		return "", err
	}
	return "markdown/" + path + ".md", nil
}
