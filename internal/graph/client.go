package graph

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

const (
	// defaultBaseURL is the public Microsoft Graph root. A sovereign or national
	// cloud tenant overrides it with WithBaseURL.
	defaultBaseURL = "https://graph.microsoft.com"
	// apiTimeout bounds one HTTP exchange so a hung server cannot stall a run.
	apiTimeout = 60 * time.Second
)

// User is a directory user with the reporting line, the manager reference no
// code crawl can supply.
type User struct {
	// ID is the stable directory object id.
	ID string `json:"id"`
	// DisplayName is the person's name.
	DisplayName string `json:"displayName"`
	// Mail is the primary email, when set.
	Mail string `json:"mail"`
	// UserPrincipalName is the sign-in name, used as email when Mail is empty.
	UserPrincipalName string `json:"userPrincipalName"`
	// JobTitle is the person's title.
	JobTitle string `json:"jobTitle"`
	// Department is the person's department, used as the team.
	Department string `json:"department"`
	// Manager is the expanded manager reference, nil for an org root.
	Manager *struct {
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	} `json:"manager"`
}

// Email returns the user's best email: the primary mail, else the sign-in name.
func (u User) Email() string {
	if u.Mail != "" {
		return u.Mail
	}
	return u.UserPrincipalName
}

// ManagerEmail returns the manager's best email, or empty for an org root.
func (u User) ManagerEmail() string {
	if u.Manager == nil {
		return ""
	}
	if u.Manager.Mail != "" {
		return u.Manager.Mail
	}
	return u.Manager.UserPrincipalName
}

// Client reads users from Microsoft Graph.
type Client struct {
	// token is the bearer token; it is never serialized or logged.
	token string
	// baseURL is the Graph root, overridable for a sovereign cloud or tests.
	baseURL string
	// http performs requests.
	http httputil.Doer
	// maxRetries bounds retries on HTTP 429.
	maxRetries int
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP doer, for tests.
func WithHTTPClient(d httputil.Doer) Option {
	return func(c *Client) {
		if d != nil {
			c.http = d
		}
	}
}

// WithBaseURL overrides the Graph root, for a sovereign or national cloud.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		if u != "" {
			c.baseURL = strings.TrimRight(u, "/")
		}
	}
}

// New returns a Client for token. It panics on an empty token; callers validate
// token presence before constructing the client.
func New(token string, opts ...Option) *Client {
	if token == "" {
		panic("graph: New requires a non-empty token")
	}
	c := &Client{token: token, baseURL: defaultBaseURL, http: &http.Client{Timeout: apiTimeout}, maxRetries: 3}
	for _, o := range opts {
		o(c)
	}
	return c
}

// page is one page of a Graph collection response.
type page struct {
	// Value holds the users on this page.
	Value []User `json:"value"`
	// NextLink is the URL of the next page, empty on the last.
	NextLink string `json:"@odata.nextLink"`
}

// Users lists every user with their manager, following pagination.
func (c *Client) Users(ctx context.Context) ([]User, error) {
	params := url.Values{}
	params.Set("$select", "id,displayName,mail,userPrincipalName,jobTitle,department")
	params.Set("$expand", "manager($select=mail,userPrincipalName)")
	params.Set("$top", "100")
	next := c.baseURL + "/v1.0/users?" + params.Encode()

	var users []User
	for next != "" {
		var p page
		if err := c.getURL(ctx, next, &p); err != nil {
			return nil, err
		}
		users = append(users, p.Value...)
		next = p.NextLink
	}
	return users, nil
}

// Ping reads a single user to confirm the token and endpoint work.
func (c *Client) Ping(ctx context.Context) error {
	var p page
	return c.getURL(ctx, c.baseURL+"/v1.0/users?$top=1", &p)
}

// getURL performs one GET against a full URL and decodes the body into out. It
// takes a full URL so a Graph nextLink is followed as-is.
func (c *Client) getURL(ctx context.Context, endpoint string, out any) error {
	resp, body, err := httputil.Do(ctx, c.http, c.maxRetries, nil, httputil.Get(ctx, endpoint, "Authorization", "Bearer "+c.token, "Accept", "application/json"))
	if errors.Is(err, httputil.ErrRateLimited) {
		return fmt.Errorf("graph: %w", ErrRateLimited)
	}
	if err != nil {
		return fmt.Errorf("graph: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("graph: %w: %w", ErrStatus, &httputil.StatusError{Code: resp.StatusCode})
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("graph: decode: %w", err)
	}
	return nil
}
