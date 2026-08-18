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
	"github.com/kordloom/whodar/internal/util"
)

// API bases for the two Jira deployments. Cloud speaks v3 and returns rich text
// as a node tree; Server and Data Center speak v2 and return it as a string.
const (
	apiBaseCloud  = "/rest/api/3"
	apiBaseServer = "/rest/api/2"
)

// Client calls the Jira REST API for either a Cloud or a Server/Data Center
// deployment. The two differ in API version, authentication, and how they name
// users, all resolved at construction.
type Client struct {
	// baseURL is the site root, for example https://acme.atlassian.net.
	baseURL string
	// auth is the Authorization header value, or empty for an anonymous
	// connection to a public Server/Data Center site.
	auth string
	// searchPath is the version-appropriate issue search endpoint.
	searchPath string
	// pingPath is the endpoint used to verify reachability and credentials.
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

// WithProgress sets a callback invoked after each page of a paging call with
// the running item count, so a long search can show movement.
func WithProgress(p util.Progress) Option {
	return func(c *Client) { c.progress = p }
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
		searchPath: apiBaseCloud + "/search",
		pingPath:   apiBaseCloud + "/myself",
		http:       &http.Client{Timeout: apiTimeout},
		maxRetries: 3,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// NewServer returns a Client for a self-hosted Jira Server or Data Center site,
// which speaks the v2 API. token is a personal access token sent as a bearer,
// or empty for a public site that allows anonymous read, such as an open-source
// project's issue tracker. It panics on an empty baseURL.
func NewServer(baseURL, token string, opts ...Option) *Client {
	if baseURL == "" {
		panic("jira: NewServer requires a baseURL")
	}
	auth := ""
	if token != "" {
		auth = "Bearer " + token
	}
	c := &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		auth:       auth,
		searchPath: apiBaseServer + "/search",
		pingPath:   apiBaseServer + "/serverInfo",
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
	// AccountID is the stable account identifier on Jira Cloud.
	AccountID string `json:"accountId"`
	// Name is the username on Jira Server and Data Center, which have no
	// account id. It is the stable identity there.
	Name string `json:"name"`
	// Key is the user key on older Server and Data Center versions, used when
	// Name is absent.
	Key string `json:"key"`
	// DisplayName is the user's display name.
	DisplayName string `json:"displayName"`
	// EmailAddress is the user's email, present when the site exposes it. Many
	// Server and Data Center sites hide it for privacy.
	EmailAddress string `json:"emailAddress"`
}

// Identity returns the stable per-site identifier for the user, preferring the
// account id used by Cloud and falling back to the username or key used by
// Server and Data Center. It is empty only for a user with none of these.
func (u User) Identity() string {
	switch {
	case u.AccountID != "":
		return u.AccountID
	case u.Name != "":
		return u.Name
	default:
		return u.Key
	}
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
		// Description is the issue description as Jira sends it, which on Cloud
		// is a rich-text node tree rather than a string. Use Description to read
		// it as words.
		Description json.RawMessage `json:"description"`
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

// Description returns the issue description as plain words, empty when the
// issue has none. It is what an issue says beyond its title, and is the text
// worth searching when someone asks how something was settled.
func (i Issue) Description() string { return adfText(i.Fields.Description) }

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

// searchFields are the issue fields Search asks for. Jira returns only the
// fields named here, so every field any caller reads must appear: omitting
// resolutiondate or status silently makes Resolved report false for every
// issue, and no issue ever becomes an episode.
const searchFields = "summary,assignee,reporter,components,labels,project," +
	"issuetype,updated,resolutiondate,status,description"

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
			"fields":     {searchFields},
		}
		var resp searchResponse
		if err := c.get(ctx, c.searchPath, params, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Issues...)
		c.progress.Report(len(all))
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

// Ping verifies the site is reachable and, when authenticated, that the
// credentials work. It uses the current-user endpoint for an authenticated
// connection and the public server-info endpoint for an anonymous one. It
// returns nil on success, a *httputil.StatusError for a non-200 status such as
// 401 for bad credentials, or the transport error when the site is unreachable.
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
		if c.auth != "" {
			req.Header.Set("Authorization", c.auth)
		}
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
		return fmt.Errorf("jira %s: %w: %w", path, ErrStatus, &httputil.StatusError{Code: resp.StatusCode})
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("jira %s: decode: %w", path, err)
	}
	return nil
}
