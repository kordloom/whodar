package connector

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/pagerduty"
	"github.com/kordloom/whodar/internal/util"
)

// defaultIncidentDays bounds how far back incidents are read.
const defaultIncidentDays = 365

// Episodes returns the resolved incidents seen by the most recent Fetch. It is
// empty unless PagerDutyOptions.Episodes was set.
func (p *PagerDuty) Episodes() []episode.Episode { return p.episodes }

// collectIncidents records resolved incidents: what broke, who settled it, and
// a link to the incident. An incident nobody was assigned to is skipped, since
// recall answers with people.
func (p *PagerDuty) collectIncidents(ctx context.Context) error {
	days := p.opts.IncidentDays
	if days <= 0 {
		days = defaultIncidentDays
	}
	since := time.Now().AddDate(0, 0, -days)
	incidents, err := p.client.Incidents(ctx, since, p.opts.MaxIncidents)
	if err != nil {
		return fmt.Errorf("pagerduty incidents: %w", err)
	}
	for _, in := range incidents {
		if ep, ok := incidentEpisode(in); ok {
			p.episodes = append(p.episodes, ep)
		}
	}
	return nil
}

// incidentEpisode builds an episode from a resolved incident.
func incidentEpisode(in pagerduty.Incident) (episode.Episode, bool) {
	if !in.Resolved() {
		return episode.Episode{}, false
	}
	people := in.People()
	participants := make([]model.ID, 0, len(people))
	for _, u := range people {
		if id := pagerDutyPersonID(u); id != "" {
			participants = append(participants, model.ID(id))
		}
	}
	if len(participants) == 0 {
		return episode.Episode{}, false
	}
	when := in.ResolvedAt
	if when.IsZero() {
		when = in.CreatedAt
	}
	id := in.ID
	if id == "" {
		id = strconv.Itoa(in.Number)
	}
	return episode.Episode{
		ID:           "pagerduty:" + id,
		Source:       "pagerduty",
		Kind:         episode.KindIncident,
		Place:        in.Service.Summary,
		Title:        in.Title,
		Participants: participants,
		Occurred:     when,
		Permalink:    in.HTMLURL,
		Body:         in.Title + " " + in.Service.Summary,
	}, true
}

// pagerDutyPersonID resolves a PagerDuty user to the identifier the index keys
// them by: their email when known, otherwise their PagerDuty id.
func pagerDutyPersonID(u pagerduty.User) string {
	if u.Email != "" {
		return util.NormalizeEmail(u.Email)
	}
	if u.ID == "" {
		return ""
	}
	return "pagerduty:" + u.ID
}
