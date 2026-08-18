// Package confluence is a minimal Confluence Cloud client scoped to what whodar
// ingests: pages and the people who wrote them. Credentials are held only in
// memory, never serialized, and never logged.
package confluence

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kordloom/whodar/internal/httputil"
	"github.com/kordloom/whodar/internal/util"
)

// API bases for the two Confluence deployments. Cloud serves the REST API under
// a /wiki context path; Server and Data Center serve it at the site root.
const (
	apiBaseCloud  = "/wiki/rest/api"
	apiBaseServer = "/rest/api"
)

// Client calls the Confluence REST API for either a Cloud or a Server/Data
// Center deployment, which differ in the API context path, authentication, and
// how they name users.
type Client struct {
	// baseURL is the site root, for example https://acme.atlassian.net.
	baseURL string
	// auth is the Authorization header value, or empty for an anonymous
	// connection to a public Server/Data Center site.
	auth string
	// searchPath is the deployment-appropriate content search endpoint.
	searchPath string
	// pingPath verifies reachability and, when authenticated, credentials.
	pingPath string
	// http performs requests.
	http httputil.Doer
	// maxRetries bounds retries on HTTP 429.
	maxRetries int
	// progress, when set, is called after each page with the running count.
	progress util.Progress
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

// WithProgress sets a callback invoked after each page with the running item
// count, so a long fetch can show movement.
func WithProgress(p util.Progress) Option {
	return func(c *Client) { c.progress = p }
}

// apiTimeout bounds one HTTP exchange so a hung server cannot stall a run.
const apiTimeout = 60 * time.Second

// New returns a Client for the site, authenticating with an email and API
// token. It panics if any argument is empty.
func New(siteURL, email, token string, opts ...Option) *Client {
	if siteURL == "" || email == "" || token == "" {
		panic("confluence: New requires siteURL, email, and token")
	}
	c := &Client{
		baseURL:    strings.TrimRight(siteURL, "/"),
		auth:       "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token)),
		searchPath: apiBaseCloud + "/content/search",
		pingPath:   apiBaseCloud + "/user/current",
		http:       &http.Client{Timeout: apiTimeout},
		maxRetries: 3,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NewServer returns a Client for a self-hosted Confluence Server or Data Center
// site, which serves the REST API at the site root rather than under /wiki.
// token is a personal access token sent as a bearer, or empty for a public
// site that allows anonymous read, such as an open-source project's wiki. It
// panics on an empty siteURL.
func NewServer(siteURL, token string, opts ...Option) *Client {
	if siteURL == "" {
		panic("confluence: NewServer requires a siteURL")
	}
	auth := ""
	if token != "" {
		auth = "Bearer " + token
	}
	c := &Client{
		baseURL:    strings.TrimRight(siteURL, "/"),
		auth:       auth,
		searchPath: apiBaseServer + "/content/search",
		pingPath:   apiBaseServer + "/space?limit=1",
		http:       &http.Client{Timeout: apiTimeout},
		maxRetries: 3,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// User is the subset of a Confluence user whodar reads.
type User struct {
	// AccountID is the stable account identifier on Confluence Cloud.
	AccountID string `json:"accountId"`
	// Username is the login on Confluence Server and Data Center, which have no
	// account id.
	Username string `json:"username"`
	// UserKey is the internal user key on Server and Data Center, used when
	// Username is absent.
	UserKey string `json:"userKey"`
	// DisplayName is the user's display name.
	DisplayName string `json:"displayName"`
	// Email is the user's email, present when the site exposes it. Server and
	// Data Center sites usually hide it.
	Email string `json:"email"`
}

// Identity returns the stable per-site identifier for the user, preferring the
// account id used by Cloud and falling back to the username or key used by
// Server and Data Center. It is empty only for a user with none of these.
func (u User) Identity() string {
	switch {
	case u.AccountID != "":
		return u.AccountID
	case u.Username != "":
		return u.Username
	default:
		return u.UserKey
	}
}

// label is a content label.
type label struct {
	// Name is the label text.
	Name string `json:"name"`
}

// Page is the subset of a content page whodar reads.
type Page struct {
	// Title is the page title.
	Title string `json:"title"`
	// Space is the page's space.
	Space struct {
		// Key is the space key.
		Key string `json:"key"`
		// Name is the space name.
		Name string `json:"name"`
	} `json:"space"`
	// Metadata holds the page labels.
	Metadata struct {
		// Labels are the page labels.
		Labels struct {
			// Results is the label list.
			Results []label `json:"results"`
		} `json:"labels"`
	} `json:"metadata"`
	// History holds the page creator and creation time.
	History struct {
		// CreatedBy is the page's creator.
		CreatedBy *User `json:"createdBy"`
		// CreatedAt is when the page was created.
		CreatedAt time.Time `json:"createdDate"`
	} `json:"history"`
	// Version holds the last editor and edit time.
	Version struct {
		// By is the last editor.
		By *User `json:"by"`
		// When is the last edit time.
		When time.Time `json:"when"`
	} `json:"version"`
}

// LabelNames returns the page's label names.
func (p Page) LabelNames() []string {
	out := make([]string, 0, len(p.Metadata.Labels.Results))
	for _, l := range p.Metadata.Labels.Results {
		out = append(out, l.Name)
	}
	return out
}

// Authors returns the distinct creator and last editor of the page.
func (p Page) Authors() []*User {
	var out []*User
	seen := make(map[string]bool)
	for _, u := range []*User{p.History.CreatedBy, p.Version.By} {
		if u != nil && u.AccountID != "" && !seen[u.AccountID] {
			seen[u.AccountID] = true
			out = append(out, u)
		}
	}
	return out
}

// searchResponse decodes the content search endpoint.
type searchResponse struct {
	// Results is the page of content.
	Results []Page `json:"results"`
	// Size is the number returned in this page.
	Size int `json:"size"`
	// Limit is the requested page size.
	Limit int `json:"limit"`
}

// Pages returns up to max pages matching cql, paginating in pages of 100. A
// non-positive max returns all matches.
func (c *Client) Pages(ctx context.Context, cql string, max int) ([]Page, error) {
	var all []Page
	for start := 0; ; {
		limit := 100
		if max > 0 && max-len(all) < limit {
			limit = max - len(all)
		}
		if limit <= 0 {
			break
		}
		params := url.Values{
			"cql":    {cql},
			"start":  {strconv.Itoa(start)},
			"limit":  {strconv.Itoa(limit)},
			"expand": {"space,metadata.labels,history,version"},
		}
		var resp searchResponse
		if err := c.get(ctx, c.searchPath, params, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Results...)
		c.progress.Report(len(all))
		start += resp.Size
		if resp.Size == 0 || resp.Size < limit {
			break
		}
		if max > 0 && len(all) >= max {
			break
		}
	}
	return all, nil
}

// Ping verifies the site is reachable and, when authenticated, that the
// credentials work. Cloud checks the current user; an anonymous Server
// connection checks a space listing, since the current-user endpoint needs
// auth. It returns nil on success, a *httputil.StatusError for a non-200
// status, or the transport error when the site is unreachable.
func (c *Client) Ping(ctx context.Context) error {
	endpoint := c.baseURL + c.pingPath
	resp, _, err := httputil.Do(ctx, c.http, c.maxRetries, nil, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		req.Header.Set("Accept", "application/json")
		return req, nil
	})
	if errors.Is(err, httputil.ErrRateLimited) {
		return fmt.Errorf("confluence ping: %w", ErrRateLimited)
	}
	if err != nil {
		return fmt.Errorf("confluence ping: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &httputil.StatusError{Code: resp.StatusCode}
	}
	return nil
}

// get performs a GET request and decodes the JSON body into out, retrying on
// HTTP 429 up to maxRetries.
func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	endpoint := c.baseURL + path + "?" + params.Encode()
	resp, body, err := httputil.Do(ctx, c.http, c.maxRetries, nil, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
		req.Header.Set("Accept", "application/json")
		return req, nil
	})
	if errors.Is(err, httputil.ErrRateLimited) {
		return fmt.Errorf("confluence %s: %w", path, ErrRateLimited)
	}
	if err != nil {
		return fmt.Errorf("confluence %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("confluence %s: %w: %w", path, ErrStatus, &httputil.StatusError{Code: resp.StatusCode})
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("confluence %s: decode: %w", path, err)
	}
	return nil
}
