package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client is a minimal HTTP client for the Mattermost v4 API.
// It targets the stable /api/v4 endpoints that have been compatible
// across Mattermost server versions since 4.x.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type AuthConfig struct {
	// Token is a Mattermost personal access token.
	Token string
	// Username/Password fall back to the login endpoint when Token is empty.
	Username string
	Password string
	// MFAToken is the TOTP code required when the account has MFA enabled.
	MFAToken string
}

// NewClient creates a Mattermost API client. The serverURL is the base URL
// of the Mattermost server, e.g. https://chat.example.com.
func NewClient(serverURL string) *Client {
	return &Client{
		baseURL: serverURL,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// Authenticate configures the auth credentials. If a token is provided it is
// used directly as a Bearer token. Otherwise it logs in with username/password
// (and optional MFA token) and captures the session token.
func (c *Client) Authenticate(ctx context.Context, cfg AuthConfig) error {
	if cfg.Token != "" {
		c.token = cfg.Token
		return nil
	}
	if cfg.Username == "" || cfg.Password == "" {
		return fmt.Errorf("authentication requires either a token or username/password")
	}

	body := map[string]string{
		"login_id": cfg.Username,
		"password": cfg.Password,
	}
	if cfg.MFAToken != "" {
		body["token"] = cfg.MFAToken
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to encode login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v4/users/login", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("login failed with status %d: %s", resp.StatusCode, string(b))
	}

	c.token = resp.Header.Get("Token")
	if c.token == "" {
		return fmt.Errorf("login succeeded but no token returned in response header")
	}
	return nil
}

// ServerURL returns the base URL of the Mattermost server.
func (c *Client) ServerURL() string {
	return c.baseURL
}

// GetMe returns the current authenticated user.
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.doGet(ctx, "/api/v4/users/me", &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetTeamsForUser lists the teams the given user belongs to.
func (c *Client) GetTeamsForUser(ctx context.Context, userID string) ([]Team, error) {
	var teams []Team
	if err := c.doGet(ctx, "/api/v4/users/"+userID+"/teams", &teams); err != nil {
		return nil, err
	}
	return teams, nil
}

// GetPostsForChannel lists posts of a channel, with pagination.
func (c *Client) GetPostsForChannel(ctx context.Context, channelID string, page, perPage int) (*PostList, error) {
	var list PostList
	endpoint := "/api/v4/channels/" + channelID + "/posts?page=" + itoa(page) + "&per_page=" + itoa(perPage)
	if err := c.doGet(ctx, endpoint, &list); err != nil {
		return nil, err
	}
	return &list, nil
}

// GetOldestPost returns the oldest post of a channel, or nil when the channel
// has no messages. The posts endpoint pages newest first, so the oldest post
// is found by binary-searching the page number for the last non-empty page:
// O(log P) requests instead of P.
func (c *Client) GetOldestPost(ctx context.Context, channelID string, perPage int) (*Post, error) {
	last, err := c.firstPageFrom(ctx, channelID, 0, perPage)
	if err != nil {
		return nil, err
	}
	if last < 0 {
		return nil, nil
	}

	list, err := c.GetPostsForChannel(ctx, channelID, last, perPage)
	if err != nil {
		return nil, err
	}
	if len(list.Order) == 0 {
		return nil, nil
	}
	return list.Posts[list.Order[len(list.Order)-1]], nil
}

// firstPageFrom returns the largest page index whose newest post has
// CreateAt >= fromMs, or -1 when no such page exists (empty channel, or the
// channel's newest post is older than fromMs). Pages are indexed newest first,
// so this is the page where ascending iteration should start: with fromMs = 0
// it is the last non-empty page, and with fromMs = startMs it is the page
// containing the first post of the period.
func (c *Client) firstPageFrom(ctx context.Context, channelID string, fromMs int64, perPage int) (int, error) {
	// Page 0 must satisfy the predicate, otherwise there is nothing to iterate.
	page0, err := c.GetPostsForChannel(ctx, channelID, 0, perPage)
	if err != nil {
		return -1, err
	}
	if newest := newestPost(page0); newest == nil || newest.CreateAt < fromMs {
		return -1, nil
	}

	// Double the page bound until the predicate turns false or a page is empty.
	lo, hi := 0, 1
	for {
		list, err := c.GetPostsForChannel(ctx, channelID, hi, perPage)
		if err != nil {
			return -1, err
		}
		if newest := newestPost(list); newest == nil || newest.CreateAt < fromMs {
			break
		}
		lo = hi
		hi *= 2
		if hi > 1<<20 {
			return -1, fmt.Errorf("channel %q has too many posts to bound", channelID)
		}
	}

	// Binary search (lo, hi] for the largest page still satisfying the predicate.
	last := lo
	for lo <= hi {
		mid := (lo + hi) / 2
		list, err := c.GetPostsForChannel(ctx, channelID, mid, perPage)
		if err != nil {
			return -1, err
		}
		if newest := newestPost(list); newest != nil && newest.CreateAt >= fromMs {
			last = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return last, nil
}

// PostsAscending visits the posts of a channel in chronological order (oldest
// first). It starts at the first post with CreateAt >= fromMs (the oldest post
// when fromMs is 0) and stops after the first post with CreateAt > untilMs
// (when untilMs != 0). fn is called once per post; pageDone is called after
// each fetched page and may abort the traversal by returning an error.
//
// The ascending order comes from iterating the newest-first pages from the
// last non-empty page down to page 0 and reversing each page's order, so the
// traversal is gapless — unlike the `after` cursor, whose CreateAt comparison
// silently drops posts sharing the anchor's exact millisecond.
func (c *Client) PostsAscending(ctx context.Context, channelID string, fromMs, untilMs int64, perPage int, pageDone func() error, fn func(*Post) error) error {
	first, err := c.firstPageFrom(ctx, channelID, fromMs, perPage)
	if err != nil {
		return err
	}
	if first < 0 {
		return nil
	}

	for page := first; page >= 0; page-- {
		list, err := c.GetPostsForChannel(ctx, channelID, page, perPage)
		if err != nil {
			return err
		}

		done := false
		for i := len(list.Order) - 1; i >= 0; i-- {
			post := list.Posts[list.Order[i]]
			if post == nil {
				continue
			}
			if post.CreateAt < fromMs {
				continue
			}
			if untilMs != 0 && post.CreateAt > untilMs {
				done = true
				break
			}
			if err := fn(post); err != nil {
				return err
			}
		}

		if pageDone != nil {
			if err := pageDone(); err != nil {
				return err
			}
		}
		if done {
			return nil
		}
	}
	return nil
}

// newestPost returns the first post of a page's order (the newest one), or nil
// when the page is empty.
func newestPost(list *PostList) *Post {
	if list == nil || len(list.Order) == 0 {
		return nil
	}
	return list.Posts[list.Order[0]]
}

// GetUsersByIds resolves user IDs to usernames in a single batch request.
func (c *Client) GetUsersByIds(ctx context.Context, userIDs []string) ([]User, error) {
	data, err := json.Marshal(userIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to encode user ids: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v4/users/ids", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to build users/ids request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("users/ids request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("users/ids failed with status %d: %s", resp.StatusCode, string(b))
	}

	var users []User
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return nil, fmt.Errorf("failed to decode users/ids response: %w", err)
	}
	return users, nil
}

// GetChannelsForUser lists all channels the user is a member of,
// including public, private, direct message, and group message channels.
func (c *Client) GetChannelsForUser(ctx context.Context, userID string) ([]Channel, error) {
	var channels []Channel
	if err := c.doGet(ctx, "/api/v4/users/"+userID+"/channels", &channels); err != nil {
		return nil, err
	}
	return channels, nil
}

// GetChannelByName resolves a channel by team ID and channel name.
func (c *Client) GetChannelByName(ctx context.Context, teamID, name string) (*Channel, error) {
	var channel Channel
	endpoint := "/api/v4/teams/" + teamID + "/channels/name/" + name
	if err := c.doGet(ctx, endpoint, &channel); err != nil {
		return nil, err
	}
	return &channel, nil
}

// GetChannel returns a single channel by ID.
func (c *Client) GetChannel(ctx context.Context, channelID string) (*Channel, error) {
	var channel Channel
	endpoint := "/api/v4/channels/" + channelID
	if err := c.doGet(ctx, endpoint, &channel); err != nil {
		return nil, err
	}
	return &channel, nil
}

// GetChannelMembers lists the members of a channel, with pagination.
func (c *Client) GetChannelMembers(ctx context.Context, channelID string, page, perPage int) ([]ChannelMember, error) {
	var members []ChannelMember
	endpoint := "/api/v4/channels/" + channelID + "/members?page=" + itoa(page) + "&per_page=" + itoa(perPage)
	if err := c.doGet(ctx, endpoint, &members); err != nil {
		return nil, err
	}
	return members, nil
}

// GetUsersByGroupChannelIds resolves the participants of direct message and
// group message channels, keyed by channel ID.
func (c *Client) GetUsersByGroupChannelIds(ctx context.Context, channelIDs []string) (map[string][]User, error) {
	data, err := json.Marshal(channelIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to encode channel ids: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v4/users/group_channels", bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to build group_channels request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("group_channels request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("group_channels failed with status %d: %s", resp.StatusCode, string(b))
	}

	var usersByChannel map[string][]User
	if err := json.NewDecoder(resp.Body).Decode(&usersByChannel); err != nil {
		return nil, fmt.Errorf("failed to decode group_channels response: %w", err)
	}
	return usersByChannel, nil
}

func (c *Client) doGet(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s failed: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("GET %s failed with status %d: %s", endpoint, resp.StatusCode, string(b))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode response of %s: %w", endpoint, err)
	}
	return nil
}

func itoa(n int) string {
	return url.QueryEscape(fmt.Sprintf("%d", n))
}

// Team mirrors the stable subset of a Mattermost team.
type Team struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// Channel mirrors the stable subset of a Mattermost channel.
type Channel struct {
	Id          string `json:"id"`
	TeamId      string `json:"team_id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// ChannelMember mirrors the stable subset of a Mattermost channel member.
type ChannelMember struct {
	UserId string `json:"user_id"`
}

// Post mirrors the stable subset of a Mattermost post.
type Post struct {
	Id        string `json:"id"`
	CreateAt  int64  `json:"create_at"`
	UpdateAt  int64  `json:"update_at"`
	DeleteAt  int64  `json:"delete_at"`
	UserId    string `json:"user_id"`
	ChannelId string `json:"channel_id"`
	RootId    string `json:"root_id"`
	Message   string `json:"message"`
	Type      string `json:"type"`
}

// PostList is the paginated posts response.
type PostList struct {
	Order []string         `json:"order"`
	Posts map[string]*Post `json:"posts"`
}

// User mirrors the stable subset of a Mattermost user.
type User struct {
	Id       string `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Email    string `json:"email"`
}
