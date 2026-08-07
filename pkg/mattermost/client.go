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
