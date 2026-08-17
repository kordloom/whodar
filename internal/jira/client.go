// Package jira is a minimal Jira Cloud client scoped to what whodar ingests:
// issues and the people on them. Credentials are held only in memory, never
// serialized, and never logged.
package jira

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

// searchPath is the Jira Cloud issue search endpoint.
const searchPath = "/rest/api/3/search"

// Client calls the Jira Cloud REST API.
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
func New(baseURL, email, token string, opts ...Option) *Client {
	if baseURL == "" || email == "" || token == "" {
		panic("jira: New requires baseURL, email, and token")
	}
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		auth:       "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+token)),
		http:       &http.Client{Timeout: apiTimeout},
		maxRetries: 3,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// User is the subset of a Jira user whodar reads.
type User struct {
	// AccountID is the stable account identifier.
	AccountID string `json:"accountId"`
	// DisplayName is the user's display name.
	DisplayName string `json:"displayName"`
	// EmailAddress is the user's email, present when visible to the token.
	EmailAddress string `json:"emailAddress"`
}

// Component is an issue component.
type Component struct {
	// Name is the component name.
	Name string `json:"name"`
}

// Issue is the subset of an issue whodar reads.
type Issue struct {
	// Key is the issue key, for example PROJ-12.
	Key string `json:"key"`
	// Fields holds the issue fields.
	Fields struct {
		// Summary is the issue title.
		Summary string `json:"summary"`
		// Assignee is the assigned user, if any.
		Assignee *User `json:"assignee"`
		// Reporter is the reporting user, if any.
		Reporter *User `json:"reporter"`
		// Components are the issue components.
		Components []Component `json:"components"`
		// Labels are the issue labels.
		Labels []string `json:"labels"`
		// Project is the issue's project.
		Project struct {
			// Key is the project key.
			Key string `json:"key"`
			// Name is the project name.
			Name string `json:"name"`
		} `json:"project"`
		// IssueType is the issue type.
		IssueType struct {
			// Name is the issue type name.
			Name string `json:"name"`
		} `json:"issuetype"`
		// Updated is the last update time in Jira's ISO 8601 format.
		Updated string `json:"updated"`
		// ResolutionDate is when the issue was resolved, empty when it is
		// still open.
		ResolutionDate string `json:"resolutiondate"`
		// Status is the issue's workflow status.
		Status struct {
			// Name is the status name, such as Done.
			Name string `json:"name"`
			// Category groups statuses; its key is "done" once the issue is
			// finished, whatever the workflow calls it.
			Category struct {
				// Key is the category key.
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"status"`
	} `json:"fields"`
}

// Resolved reports whether the issue reached a finished state, which is what
// makes it a record of how something was settled rather than work in flight.
func (i Issue) Resolved() bool {
	return i.Fields.ResolutionDate != "" || i.Fields.Status.Category.Key == "done"
}

// BaseURL returns the site root, so a caller can build a link to an issue.
func (c *Client) BaseURL() string { return c.baseURL }

// searchResponse decodes the issue search endpoint.
type searchResponse struct {
	// Issues is the page of issues.
	Issues []Issue `json:"issues"`
	// StartAt is the offset of this page.
	StartAt int `json:"startAt"`
	// Total is the total matching count.
	Total int `json:"total"`
}

// Search returns up to max issues matching jql, paginating in pages of 100. A
// non-positive max returns all matches.
func (c *Client) Search(ctx context.Context, jql string, max int) ([]Issue, error) {
	var all []Issue
	for startAt := 0; ; {
		page := 100
		if max > 0 && max-len(all) < page {
			page = max - len(all)
		}
		if page <= 0 {
			break
		}
		params := url.Values{
			"jql":        {jql},
			"startAt":    {strconv.Itoa(startAt)},
			"maxResults": {strconv.Itoa(page)},
			"fields":     {"summary,assignee,reporter,components,labels,project,issuetype,updated"},
		}
		var resp searchResponse
		if err := c.get(ctx, searchPath, params, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Issues...)
		startAt += len(resp.Issues)
		if len(resp.Issues) == 0 || startAt >= resp.Total {
			break
		}
		if max > 0 && len(all) >= max {
			break
		}
	}
	return all, nil
}

// myselfPath is the Jira Cloud current-user endpoint used to validate credentials.
const myselfPath = "/rest/api/3/myself"

// Ping verifies the credentials with the current-user endpoint, the cheapest
// read-only call that proves the site, email, and token line up. It returns nil
// when they are accepted, a *httputil.StatusError for a non-200 status such as
// 401 for bad credentials, or the transport error when the site is unreachable.
func (c *Client) Ping(ctx context.Context) error {
	endpoint := c.baseURL + myselfPath
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
		return fmt.Errorf("jira ping: %w", ErrRateLimited)
	}
	if err != nil {
		return fmt.Errorf("jira ping: %w", err)
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
		return fmt.Errorf("jira %s: %w", path, ErrRateLimited)
	}
	if err != nil {
		return fmt.Errorf("jira %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jira %s: %w %d", path, ErrStatus, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("jira %s: decode: %w", path, err)
	}
	return nil
}
