// Package slack is a minimal Slack Web API client scoped to what whodar needs
// to ingest: workspace users, channels, and message history. The token is held
// only in memory, never serialized, and never logged.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/httputil"
)

// defaultBaseURL is the Slack Web API root.
const defaultBaseURL = "https://slack.com/api"

// Client calls the Slack Web API.
type Client struct {
	// token is the bearer token; it is never serialized or logged.
	token string
	// baseURL is the API root, overridable for tests.
	baseURL string
	// http performs requests.
	http httputil.Doer
	// maxRetries bounds retries on HTTP 429.
	maxRetries int
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP doer.
func WithHTTPClient(d httputil.Doer) Option {
	return func(c *Client) {
		if d != nil {
			c.http = d
		}
	}
}

// WithBaseURL overrides the API base URL.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		if u != "" {
			c.baseURL = u
		}
	}
}

// apiTimeout bounds one HTTP exchange so a hung server cannot stall a run.
const apiTimeout = 60 * time.Second

// New returns a Client for token. It panics on an empty token; callers validate
// token presence before constructing the client.
func New(token string, opts ...Option) *Client {
	if token == "" {
		panic("slack: New requires a non-empty token")
	}
	c := &Client{token: token, baseURL: defaultBaseURL, http: &http.Client{Timeout: apiTimeout}, maxRetries: 3}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Profile holds the subset of a Slack user profile whodar uses.
type Profile struct {
	// RealName is the user's display name.
	RealName string `json:"real_name"`
	// Email is the user's email, present only with the users:read.email scope.
	Email string `json:"email"`
	// Title is the user's profile title.
	Title string `json:"title"`
}

// User is a Slack workspace member.
type User struct {
	// ID is the Slack user ID.
	ID string `json:"id"`
	// Name is the Slack handle.
	Name string `json:"name"`
	// Deleted reports whether the account is deactivated.
	Deleted bool `json:"deleted"`
	// IsBot reports whether the account is a bot.
	IsBot bool `json:"is_bot"`
	// Profile holds name, email, and title.
	Profile Profile `json:"profile"`
}

// value wraps a Slack topic or purpose object.
type value struct {
	// Value is the text content.
	Value string `json:"value"`
}

// Channel is a Slack conversation.
type Channel struct {
	// ID is the channel ID.
	ID string `json:"id"`
	// Name is the channel name without the leading hash.
	Name string `json:"name"`
	// IsPrivate reports whether the channel is private.
	IsPrivate bool `json:"is_private"`
	// IsArchived reports whether the channel is archived.
	IsArchived bool `json:"is_archived"`
	// NumMembers is the member count.
	NumMembers int `json:"num_members"`
	// Topic is the channel topic.
	Topic value `json:"topic"`
	// Purpose is the channel purpose.
	Purpose value `json:"purpose"`
}

// Message is a Slack message. The thread fields arrive on the parent of a
// thread in conversations.history, so a thread's shape is known without
// calling conversations.replies.
type Message struct {
	// Type is the message type, normally "message".
	Type string `json:"type"`
	// Subtype distinguishes system messages from user messages.
	Subtype string `json:"subtype"`
	// User is the author's Slack user ID.
	User string `json:"user"`
	// BotID is set when a bot authored the message.
	BotID string `json:"bot_id"`
	// Text is the message body.
	Text string `json:"text"`
	// TS is the message timestamp.
	TS string `json:"ts"`
	// ThreadTS is the parent timestamp of the thread this message belongs to,
	// equal to TS on the parent itself and empty when the message is not
	// threaded.
	ThreadTS string `json:"thread_ts"`
	// ReplyCount is the number of replies on a thread parent.
	ReplyCount int `json:"reply_count"`
	// ReplyUsers lists up to five user IDs that replied in the thread.
	ReplyUsers []string `json:"reply_users"`
	// LatestReply is the timestamp of the most recent reply in the thread.
	LatestReply string `json:"latest_reply"`
}

// Permalink builds the archive URL for a message from data every history page
// already carries, so no chat.getPermalink call is needed. workspaceURL is the
// url reported by auth.test. It returns an empty string when workspaceURL or
// ts is missing. When threadTS names a different message, the link points at
// the reply inside its thread.
func Permalink(workspaceURL, channelID, ts, threadTS string) string {
	if workspaceURL == "" || channelID == "" || ts == "" {
		return ""
	}
	link := strings.TrimSuffix(workspaceURL, "/") +
		"/archives/" + channelID + "/p" + strings.ReplaceAll(ts, ".", "")
	if threadTS != "" && threadTS != ts {
		link += "?thread_ts=" + threadTS + "&cid=" + channelID
	}
	return link
}

// apiMeta is the common envelope of every Slack Web API response.
type apiMeta struct {
	// OK reports whether the call succeeded.
	OK bool `json:"ok"`
	// Error holds the error code when OK is false.
	Error string `json:"error"`
	// Metadata carries the pagination cursor.
	Metadata struct {
		// NextCursor is the cursor for the next page, empty when done.
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// response is implemented by every decoded Slack response via embedded apiMeta.
type response interface {
	// ok reports whether the call succeeded.
	ok() bool
	// errMsg returns the Slack error code.
	errMsg() string
	// cursor returns the next-page cursor.
	cursor() string
}

// ok reports whether the call succeeded.
func (a apiMeta) ok() bool { return a.OK }

// errMsg returns the Slack error code.
func (a apiMeta) errMsg() string { return a.Error }

// cursor returns the next-page cursor.
func (a apiMeta) cursor() string { return a.Metadata.NextCursor }

// usersListResp decodes users.list.
type usersListResp struct {
	apiMeta
	// Members is the page of users.
	Members []User `json:"members"`
}

// channelsListResp decodes conversations.list.
type channelsListResp struct {
	apiMeta
	// Channels is the page of channels.
	Channels []Channel `json:"channels"`
}

// historyResp decodes conversations.history.
type historyResp struct {
	apiMeta
	// Messages is the page of messages.
	Messages []Message `json:"messages"`
	// HasMore reports whether more pages remain.
	HasMore bool `json:"has_more"`
}

// Users returns all active, non-bot users in the workspace.
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var all []User
	cursor := ""
	for {
		params := url.Values{"limit": {"200"}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		var resp usersListResp
		if err := c.do(ctx, "users.list", params, &resp); err != nil {
			return nil, err
		}
		for _, u := range resp.Members {
			if u.Deleted || u.IsBot {
				continue
			}
			all = append(all, u)
		}
		cursor = resp.cursor()
		if cursor == "" {
			return all, nil
		}
	}
}

// Channels lists conversations of the given comma-separated types (for example
// "public_channel" or "public_channel,private_channel"), excluding archived
// channels.
func (c *Client) Channels(ctx context.Context, types string) ([]Channel, error) {
	var all []Channel
	cursor := ""
	for {
		params := url.Values{
			"limit":            {"200"},
			"exclude_archived": {"true"},
			"types":            {types},
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		var resp channelsListResp
		if err := c.do(ctx, "conversations.list", params, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Channels...)
		cursor = resp.cursor()
		if cursor == "" {
			return all, nil
		}
	}
}

// History returns up to limit messages from channelID newer than oldest, a
// Slack timestamp string ("" means no lower bound). It stops at limit messages
// or when Slack reports no more pages.
func (c *Client) History(ctx context.Context, channelID, oldest string, limit int) ([]Message, error) {
	var all []Message
	cursor := ""
	for limit <= 0 || len(all) < limit {
		params := url.Values{
			"channel": {channelID},
			"limit":   {"200"},
		}
		if oldest != "" {
			params.Set("oldest", oldest)
		}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		var resp historyResp
		if err := c.do(ctx, "conversations.history", params, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Messages...)
		cursor = resp.cursor()
		if cursor == "" || !resp.HasMore {
			break
		}
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

// do calls a Slack Web API method with form params and decodes the result into
// out. It retries on HTTP 429 up to maxRetries, honoring Retry-After.
func (c *Client) do(ctx context.Context, method string, params url.Values, out response) error {
	endpoint := c.baseURL + "/" + method
	resp, body, err := httputil.Do(ctx, c.http, c.maxRetries, nil, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(
			ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return req, nil
	})
	if errors.Is(err, httputil.ErrRateLimited) {
		return fmt.Errorf("slack %s: %w", method, ErrRateLimited)
	}
	if err != nil {
		return fmt.Errorf("slack %s: %w", method, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack %s: unexpected status %d", method, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("slack %s: decode: %w", method, err)
	}
	if !out.ok() {
		return fmt.Errorf("slack %s: %w: %s", method, ErrAPI, out.errMsg())
	}
	return nil
}
