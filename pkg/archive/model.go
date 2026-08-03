package archive

import (
	"time"

	"github.com/stephane-klein/sklein-convarchive/pkg/mattermost"
)

// Entry is the common normalized schema shared by all sources.
// Every source connector (Mattermost, Signal, OpenCode, ...) converges
// towards this homogeneous structure.
type Entry struct {
	Source    string         `json:"source"`
	Timestamp string         `json:"timestamp"`
	Author    string         `json:"author"`
	Content   string         `json:"content"`
	ThreadID  string         `json:"thread_id"`
	Metadata  map[string]any `json:"metadata"`
	Raw       any            `json:"raw,omitempty"`
}

// ChannelContext carries the Mattermost context needed to normalize a post.
type ChannelContext struct {
	TeamName    string
	ChannelName string
	ServerURL   string
}

// NormalizePost converts a Mattermost post into a normalized Entry.
// The raw post is preserved as-is for long-term fidelity. Timestamps are
// formatted in the given location (the archive then carries the local offset).
func NormalizePost(post *mattermost.Post, ctx ChannelContext, loc *time.Location) *Entry {
	threadID := post.RootId
	if threadID == "" {
		threadID = post.Id
	}

	format := func(ms int64) string {
		return time.UnixMilli(ms).In(loc).Format(time.RFC3339)
	}

	return &Entry{
		Source:    "mattermost",
		Timestamp: format(post.CreateAt),
		Author:    post.UserId,
		Content:   post.Message,
		ThreadID:  threadID,
		Metadata: map[string]any{
			"team":       ctx.TeamName,
			"channel":    ctx.ChannelName,
			"server_url": ctx.ServerURL,
			"channel_id": post.ChannelId,
			"post_id":    post.Id,
			"post_type":  post.Type,
			"update_at":  format(post.UpdateAt),
			"delete_at":  format(post.DeleteAt),
		},
		Raw: post,
	}
}
