package connector

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kordloom/whodar/internal/pagerduty"
	"github.com/kordloom/whodar/internal/util"
)

// PagerDutyOptions configures the PagerDuty connector.
type PagerDutyOptions struct {
	// Log receives progress lines; nil discards them.
	Log io.Writer
}

// withDefaults fills the log writer when unset.
func (o PagerDutyOptions) withDefaults() PagerDutyOptions {
	if o.Log == nil {
		o.Log = io.Discard
	}
	return o
}

// PagerDuty is a Source that ingests services and on-call assignments, giving
// each on-call person the topics of the services they answer for.
type PagerDuty struct {
	// client calls the PagerDuty API.
	client *pagerduty.Client
	// opts holds the resolved options.
	opts PagerDutyOptions
}

// NewPagerDuty returns a PagerDuty connector authenticating with token.
func NewPagerDuty(token string, opts PagerDutyOptions) *PagerDuty {
	return &PagerDuty{client: pagerduty.New(token), opts: opts.withDefaults()}
}

// NewPagerDutyWithClient returns a PagerDuty connector using a preconfigured
// client. Tests use it to inject a client pointed at a mock server.
func NewPagerDutyWithClient(client *pagerduty.Client, opts PagerDutyOptions) *PagerDuty {
	if client == nil {
		panic("connector: NewPagerDutyWithClient requires a non-nil client")
	}
	return &PagerDuty{client: client, opts: opts.withDefaults()}
}

// Ping verifies the token with a cheap read-only call, so a wizard can confirm
// credentials before committing to a full index.
func (p *PagerDuty) Ping(ctx context.Context) error {
	return p.client.Ping(ctx)
}

// Fetch reads services and on-call assignments, returning one record per person
// weighted by the topics of the services they are on call for.
func (p *PagerDuty) Fetch(ctx context.Context) ([]Record, error) {
	services, err := p.client.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("pagerduty services: %w", err)
	}
	policyTopics := make(map[string][]string)
	for _, s := range services {
		tokens := append(titleTokens(s.Name), titleTokens(s.Description)...)
		policyTopics[s.EscalationPolicy.ID] = append(policyTopics[s.EscalationPolicy.ID], tokens...)
	}

	oncalls, err := p.client.OnCalls(ctx)
	if err != nil {
		return nil, fmt.Errorf("pagerduty oncalls: %w", err)
	}
	fmt.Fprintf(p.opts.Log, "pagerduty: %d services, %d on-call assignments\n", len(services), len(oncalls))

	counts := make(map[string]map[string]int)
	users := make(map[string]pagerduty.User)
	bump := func(u pagerduty.User, tokens []string) {
		key := pagerdutyUserKey(u)
		if key == "" {
			return
		}
		m := counts[key]
		if m == nil {
			m = make(map[string]int)
			counts[key] = m
		}
		for _, t := range tokens {
			if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
				m[t]++
			}
		}
		users[key] = u
	}

	for _, oc := range oncalls {
		bump(oc.User, policyTopics[oc.EscalationPolicy.ID])
	}

	records := make([]Record, 0, len(counts))
	for key, m := range counts {
		records = append(records, pagerdutyPersonRecord(users[key], expandTopics(m)))
	}
	return records, nil
}

// pagerdutyUserKey returns a stable key for a user, preferring email.
func pagerdutyUserKey(u pagerduty.User) string {
	if u.Email != "" {
		return strings.ToLower(u.Email)
	}
	if u.ID != "" {
		return "pagerduty:" + u.ID
	}
	return ""
}

// pagerdutyPersonRecord builds a person record. An email lets the person join
// other sources; otherwise the user id keys the record.
func pagerdutyPersonRecord(u pagerduty.User, topics []string) Record {
	rec := Record{Kind: KindPerson, Source: "pagerduty", Weight: 1, Topics: topics, Name: u.Name}
	if u.Email != "" {
		rec.Email = util.NormalizeEmail(u.Email)
	} else {
		rec.PersonID = "pagerduty:" + u.ID
	}
	if rec.Name == "" {
		if rec.Email != "" {
			rec.Name = rec.Email
		} else {
			rec.Name = rec.PersonID
		}
	}
	return rec
}
