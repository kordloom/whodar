// Package pagerduty is a minimal PagerDuty client scoped to what whodar
// ingests: services and who is on call for them. The token is held only in
// memory, never serialized, and never logged.
package pagerduty

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kordloom/whodar/internal/httputil"
)

// defaultBaseURL is the PagerDuty REST API root.
const defaultBaseURL = "https://api.pagerduty.com"

// Client calls the PagerDuty REST API.
type Client struct {
	// token is the API token; it is never serialized or logged.
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
		panic("pagerduty: New requires a non-empty token")
	}
	c := &Client{token: token, baseURL: defaultBaseURL, http: &http.Client{Timeout: apiTimeout}, maxRetries: 3}
	for _, o := range opts {
		o(c)
	}
	return c
}

// EscalationPolicyRef references an escalation policy.
type EscalationPolicyRef struct {
	// ID is the escalation policy id.
	ID string `json:"id"`
}

// Service is a monitored service.
type Service struct {
	// ID is the service id.
	ID string `json:"id"`
	// Name is the service name.
	Name string `json:"name"`
	// Description is the service description.
	Description string `json:"description"`
	// EscalationPolicy links the service to its escalation policy.
	EscalationPolicy EscalationPolicyRef `json:"escalation_policy"`
}

// User is the subset of a PagerDuty user whodar reads.
type User struct {
	// ID is the user id.
	ID string `json:"id"`
	// Name is the user's display name.
	Name string `json:"name"`
	// Email is the user's email.
	Email string `json:"email"`
}

// OnCall is a current on-call assignment.
type OnCall struct {
	// User is the on-call user.
	User User `json:"user"`
	// EscalationPolicy is the policy the user is on call for.
	EscalationPolicy EscalationPolicyRef `json:"escalation_policy"`
}

// servicesResponse decodes the services endpoint.
type servicesResponse struct {
	// Services is the page of services.
	Services []Service `json:"services"`
	// More reports whether more pages remain.
	More bool `json:"more"`
}

// oncallsResponse decodes the oncalls endpoint.
type oncallsResponse struct {
	// OnCalls is the page of on-call assignments.
	OnCalls []OnCall `json:"oncalls"`
	// More reports whether more pages remain.
	More bool `json:"more"`
}

// maxPages caps how many pages one listing walks so a server that always
// reports more, or returns non-empty pages without end, cannot loop forever.
const maxPages = 100

// Services returns all services with their escalation policy.
func (c *Client) Services(ctx context.Context) ([]Service, error) {
	var all []Service
	for page, offset := 0, 0; page < maxPages; page, offset = page+1, offset+100 {
		params := url.Values{"limit": {"100"}, "offset": {strconv.Itoa(offset)}}
		var resp servicesResponse
		if err := c.get(ctx, "/services", params, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Services...)
		if !resp.More || len(resp.Services) == 0 {
			break
		}
	}
	return all, nil
}

// OnCalls returns the current on-call assignments with the on-call users.
func (c *Client) OnCalls(ctx context.Context) ([]OnCall, error) {
	var all []OnCall
	for page, offset := 0, 0; page < maxPages; page, offset = page+1, offset+100 {
		params := url.Values{"limit": {"100"}, "offset": {strconv.Itoa(offset)}, "include[]": {"users"}}
		var resp oncallsResponse
		if err := c.get(ctx, "/oncalls", params, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.OnCalls...)
		if !resp.More || len(resp.OnCalls) == 0 {
			break
		}
	}
	return all, nil
}

// Ping verifies the token by requesting a single user, the cheapest read-only
// call that proves the token is valid. It returns nil when the token is
// accepted, a *httputil.StatusError for a non-200 status such as 401 for a bad
// token, or the transport error when the API is unreachable.
func (c *Client) Ping(ctx context.Context) error {
	endpoint := c.baseURL + "/users?limit=1"
	resp, _, err := httputil.Do(ctx, c.http, c.maxRetries, nil, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		req.Header.Set("Authorization", "Token token="+c.token)
		req.Header.Set("Accept", "application/vnd.pagerduty+json;version=2")
		return req, nil
	})
	if errors.Is(err, httputil.ErrRateLimited) {
		return fmt.Errorf("pagerduty ping: %w", ErrRateLimited)
	}
	if err != nil {
		return fmt.Errorf("pagerduty ping: %w", err)
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
		req.Header.Set("Authorization", "Token token="+c.token)
		req.Header.Set("Accept", "application/vnd.pagerduty+json;version=2")
		return req, nil
	})
	if errors.Is(err, httputil.ErrRateLimited) {
		return fmt.Errorf("pagerduty %s: %w", path, ErrRateLimited)
	}
	if err != nil {
		return fmt.Errorf("pagerduty %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pagerduty %s: %w %d", path, ErrStatus, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("pagerduty %s: decode: %w", path, err)
	}
	return nil
}
