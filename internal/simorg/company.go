package simorg

import (
	"context"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/kordloom/whodar/internal/confluence"
	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/episode"
	"github.com/kordloom/whodar/internal/github"
	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/jira"
	"github.com/kordloom/whodar/internal/pagerduty"
	"github.com/kordloom/whodar/internal/slack"
)

// BigSpec is the demo company's default shape: a few hundred people across many
// teams, cast from the show name pools, with enough history that recall, risk,
// and ownership all have something to find. It is the corpus behind demo.whodar.dev.
func BigSpec() Spec {
	return Spec{
		People:            220,
		Channels:          len(subjects),
		Topics:            len(subjects),
		ThreadsPerChannel: 8,
		ChatterPerChannel: 30,
		Seed:              7,
		GivenNames:        showGivenNames,
		FamilyNames:       showFamilyNames,
	}
}

// icTitles are the individual-contributor titles the demo spreads across the
// workforce, so the org chart reads like a real engineering org rather than one
// role repeated.
var icTitles = []string{
	"Software Engineer", "Senior Software Engineer", "Staff Engineer",
	"Site Reliability Engineer", "Security Engineer", "Platform Engineer",
	"Data Engineer", "Principal Engineer",
}

// company is a large synthesized organization for the public demo: people with a
// management chain and topic expertise, plus the Slack history that makes each
// owner the answer to their subject.
type company struct {
	spec     Spec
	people   []person
	owners   []owner
	channels []map[string]any
	history  map[string][]map[string]any
	threads  []thread
	messages int
}

// buildCompany synthesizes the demo organization deterministically from spec.
func buildCompany(spec Spec) *company {
	spec = spec.withDefaults()
	rng := rand.New(rand.NewSource(spec.Seed))
	people := buildBigPeople(spec)
	owners := buildBigOwners(people)
	channels := generateChannels(spec, owners)
	// The channel name is the topic slug; align each channel's topic and purpose
	// text to it so a channel does not also register its spaced phrase as separate
	// single-word topics.
	for _, ch := range channels {
		name, _ := ch["name"].(string)
		if tp, ok := ch["topic"].(map[string]any); ok {
			tp["value"] = name
		}
		if pp, ok := ch["purpose"].(map[string]any); ok {
			pp["value"] = name
		}
	}
	history, threads, count := generateHistory(spec, rng, people, owners, channels)
	c := &company{
		spec: spec, people: people, owners: owners,
		channels: channels, history: history, threads: threads, messages: count,
	}
	c.addSecondaryExperts(rng)
	return c
}

// addSecondaryExperts gives some subjects a second or third fluent voice, so the
// risk view shows a real spread of critical, elevated, and ok rather than every
// topic sitting at bus factor one.
func (c *company) addSecondaryExperts(rng *rand.Rand) {
	ts := 1_700_000_000
	for s := range c.owners {
		extra := s % 3 // 0 critical, 1 elevated, 2 ok
		channelID := c.owners[s].channel
		words := subjects[s].Words
		for k := 0; k < extra; k++ {
			idx := (s*17 + k*53 + 11) % len(c.people)
			if c.people[idx].email == c.owners[s].who.email {
				idx = (idx + 1) % len(c.people)
			}
			c.people[idx].topics = append(c.people[idx].topics, s)
			for range 5 {
				ts += 600
				c.history[channelID] = append(c.history[channelID],
					slackMessageAt(c.people[idx].id, sentence(rng, fillers.Owner, words), ts))
			}
		}
	}
}

// buildBigPeople casts the workforce and lays a four-level management chain over
// it: a VP, a few directors, one manager per team, and everyone else reporting to
// their team's manager.
func buildBigPeople(spec Spec) []person {
	given, family := spec.GivenNames, spec.FamilyNames
	if len(given) == 0 {
		given = showGivenNames
	}
	if len(family) == 0 {
		family = showFamilyNames
	}
	n := spec.People
	people := make([]person, n)
	for i := range n {
		g := given[i%len(given)]
		f := family[(i/len(given)+i)%len(family)]
		email := fmt.Sprintf("%s.%s%d@corp.com",
			strings.ToLower(asciiFold(g)), strings.ToLower(asciiFold(f)), i)
		people[i] = person{
			id:    fmt.Sprintf("U%04d", i),
			name:  g + " " + f,
			email: email,
			team:  teams[i%len(teams)],
			title: "Software Engineer",
		}
		// Most people have a GitHub login; a few have none and join by alias.
		if i%29 != 4 {
			people[i].github = strings.ToLower(asciiFold(g[:1]) + asciiFold(f) + fmt.Sprint(i))
		}
	}

	if n == 0 {
		return people
	}
	people[0].title = "VP of Engineering"
	var dirs []int
	for i := 1; i <= 3 && i < n; i++ {
		people[i].title = "Director of Engineering"
		people[i].manager = people[0].email
		dirs = append(dirs, i)
	}
	teamMgr := map[string]string{}
	di := 0
	for i := 4; i < n; i++ {
		if _, ok := teamMgr[people[i].team]; ok {
			continue
		}
		people[i].title = "Engineering Manager"
		if len(dirs) > 0 {
			people[i].manager = people[dirs[di%len(dirs)]].email
		} else {
			people[i].manager = people[0].email
		}
		teamMgr[people[i].team] = people[i].email
		di++
	}
	for i := 4; i < n; i++ {
		if people[i].title == "Engineering Manager" {
			continue
		}
		people[i].title = icTitles[i%len(icTitles)]
		if mgr, ok := teamMgr[people[i].team]; ok {
			people[i].manager = mgr
		} else {
			people[i].manager = people[0].email
		}
	}
	return people
}

// buildBigOwners makes one person the expert for each subject, spread across the
// company so ownership is not clustered on a single team.
func buildBigOwners(people []person) []owner {
	owners := make([]owner, 0, len(subjects))
	if len(people) == 0 {
		return owners
	}
	step := max(1, len(people)/len(subjects))
	for s := range subjects {
		idx := (s*step + 7) % len(people)
		people[idx].topics = append(people[idx].topics, s)
		owners = append(owners, owner{
			subject: s, who: people[idx], channel: fmt.Sprintf("C%03d", s),
		})
	}
	return owners
}

// orgCSV renders the company as the org-csv source reads it, with the management
// chain and topic expertise the small fixture leaves blank.
func (c *company) orgCSV() string {
	var b strings.Builder
	b.WriteString("name,email,title,team,org,manager,topics\n")
	for _, p := range c.people {
		names := make([]string, 0, len(p.topics))
		for _, t := range p.topics {
			names = append(names, topicSlug(t))
		}
		fmt.Fprintf(&b, "%s,%s,%s,%s,Engineering,%s,%s\n",
			p.name, p.email, p.title, p.team, p.manager, strings.Join(names, ";"))
	}
	return b.String()
}

// codeOwners maps a path per subject to a declared owner. Most declare the real
// expert; a planted fraction declare someone who no longer does the work, which
// is the ownership drift `whodar ownership` surfaces.
func (c *company) codeOwners() string {
	var b strings.Builder
	for s := range subjects {
		declared := c.owners[s].who.email
		if s%5 == 0 {
			declared = c.people[(s*13+3)%len(c.people)].email
		}
		path := strings.ReplaceAll(subjects[s].Topic, " ", "-") + "/"
		fmt.Fprintf(&b, "%s %s\n", path, declared)
	}
	return b.String()
}

// BuildBigIndex assembles the large demo company into a merged, canonicalized
// index under dir. It drives the org chart, expertise, and recall from one
// generated organization at scale.
func BuildBigIndex(dir string) (*index.Index, error) {
	ctx := context.Background()
	c := buildCompany(BigSpec())

	write := func(name, content string) (string, error) {
		p := filepath.Join(dir, name)
		return p, os.WriteFile(p, []byte(content), 0o600)
	}
	csvPath, err := write("org.csv", c.orgCSV())
	if err != nil {
		return nil, fmt.Errorf("simorg: %w", err)
	}
	ownersPath, err := write("CODEOWNERS", c.codeOwners())
	if err != nil {
		return nil, fmt.Errorf("simorg: %w", err)
	}

	repoDir := filepath.Join(dir, "repo")
	if err := c.buildGitRepo(repoDir); err != nil {
		return nil, err
	}

	slackSrv := c.slackServer()
	defer slackSrv.Close()
	githubSrv, repos := c.githubServer()
	defer githubSrv.Close()
	jiraSrv := c.jiraServer()
	defer jiraSrv.Close()
	confluenceSrv := c.confluenceServer()
	defer confluenceSrv.Close()
	pagerdutySrv := c.pagerdutyServer()
	defer pagerdutySrv.Close()

	sources := []struct {
		Name   string
		Source connector.Source
	}{
		{"org-csv", connector.NewOrgCSV(csvPath)},
		{"codeowners", connector.NewCodeOwners(ownersPath)},
		{"slack", connector.NewSlackWithClient(
			slack.New("xoxb-demo", slack.WithBaseURL(slackSrv.URL)), connector.SlackOptions{})},
		{"github", connector.NewGitHubWithClient(
			github.New("ghp-demo", github.WithBaseURL(githubSrv.URL)),
			connector.GitHubOptions{Repos: repos, ResolveEmails: true})},
		{"jira", connector.NewJiraWithClient(
			jira.New(jiraSrv.URL, "demo@corp.com", "token"), connector.JiraOptions{})},
		{"confluence", connector.NewConfluenceWithClient(
			confluence.New(confluenceSrv.URL, "demo@corp.com", "token"), connector.ConfluenceOptions{})},
		{"pagerduty", connector.NewPagerDutyWithClient(
			pagerduty.New("token", pagerduty.WithBaseURL(pagerdutySrv.URL)), connector.PagerDutyOptions{})},
		{"git", connector.NewGitHistory(connector.GitOptions{Paths: []string{repoDir}, SinceDays: 900})},
	}

	ix := index.New()
	for _, s := range sources {
		recs, err := s.Source.Fetch(ctx)
		if err != nil {
			return nil, fmt.Errorf("simorg: %s: %w", s.Name, err)
		}
		ix.Add(recs)
	}
	ix.AutoJoin()
	ix.Canonicalize()
	return ix, nil
}

// slackServer serves the company's generated workspace in Slack's wire format.
func (c *company) slackServer() *httptest.Server {
	return slackServerFor(c.people, c.channels, c.history, c.threads)
}

// BuildBigEpisodes collects the large demo company's recall material: the Slack
// conversations people worked through, resolved against ix so a person's work is
// findable. It mirrors BuildEpisodes for the big company.
func BuildBigEpisodes(ix *index.Index) (*episode.Store, error) {
	ctx := context.Background()
	c := buildCompany(BigSpec())
	slackSrv := c.slackServer()
	defer slackSrv.Close()
	githubSrv, repos := c.githubServer()
	defer githubSrv.Close()
	jiraSrv := c.jiraServer()
	defer jiraSrv.Close()
	pagerdutySrv := c.pagerdutyServer()
	defer pagerdutySrv.Close()

	sources := []struct {
		Name   string
		Source interface {
			connector.Source
			connector.EpisodeSource
		}
	}{
		{"slack", connector.NewSlackWithClient(
			slack.New("xoxb-demo", slack.WithBaseURL(slackSrv.URL)),
			connector.SlackOptions{Episodes: true, Archive: true})},
		{"github", connector.NewGitHubWithClient(
			github.New("ghp-demo", github.WithBaseURL(githubSrv.URL)),
			connector.GitHubOptions{Repos: repos, Episodes: true, ResolveEmails: true})},
		{"jira", connector.NewJiraWithClient(
			jira.New(jiraSrv.URL, "demo@corp.com", "token"), connector.JiraOptions{Episodes: true})},
		{"pagerduty", connector.NewPagerDutyWithClient(
			pagerduty.New("token", pagerduty.WithBaseURL(pagerdutySrv.URL)),
			connector.PagerDutyOptions{Episodes: true})},
	}

	store := episode.New()
	for _, s := range sources {
		if _, err := s.Source.Fetch(ctx); err != nil {
			return nil, fmt.Errorf("simorg: %s episodes: %w", s.Name, err)
		}
		eps := s.Source.Episodes()
		if ix != nil {
			ix.CanonicalizeEpisodes(eps)
		}
		for _, ep := range eps {
			store.Add(ep)
		}
	}
	return store, nil
}

// BigDemoPerson is who the large demo opens as and the question it opens with: a
// person who raised a problem the company solved, so their recall view is not empty.
func BigDemoPerson() (email, query string) {
	c := buildCompany(BigSpec())
	query = "who knows about " + subjects[0].Topic
	switch {
	case len(c.threads) > 0:
		return c.threads[0].asker.email, query
	case len(c.people) > 0:
		return c.people[0].email, query
	default:
		return "", query
	}
}
