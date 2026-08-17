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
)

// searchPath is the Confluence Cloud content search endpoint.
const searchPath = "/wiki/rest/api/content/search"

// Client calls the Confluence Cloud REST API.
type Client struct {
	// baseURL is the site root, for example https://acme.atlassian.net.
	baseURL string
	// auth is the Basic authorization header value.
	auth string
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
	// AccountID is the stable account identifier.
	AccountID string `json:"accountId"`
	// DisplayName is the user's display name.
	DisplayName string `json:"displayName"`
	// Email is the user's email, present when visible to the token.
	Email string `json:"email"`
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
		if err := c.get(ctx, searchPath, params, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Results...)
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

// currentUserPath is the Confluence Cloud current-user endpoint used to
// validate credentials.
const currentUserPath = "/wiki/rest/api/user/current"

// Ping verifies the credentials with the current-user endpoint, the cheapest
// read-only call that proves the site, email, and token line up. It returns nil
// when they are accepted, a *httputil.StatusError for a non-200 status such as
// 401 for bad credentials, or the transport error when the site is unreachable.
func (c *Client) Ping(ctx context.Context) error {
	endpoint := c.baseURL + currentUserPath
	resp, _, err := httputil.Do(ctx, c.http, c.maxRetries, nil, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Authorization", c.auth)
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
		req.Header.Set("Authorization", c.auth)
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
		return fmt.Errorf("confluence %s: %w %d", path, ErrStatus, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("confluence %s: decode: %w", path, err)
	}
	return nil
}
