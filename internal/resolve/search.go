package resolve

import (
	"sort"
	"strings"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
)

// defaultSearchLimit caps a search that does not ask for a size.
const defaultSearchLimit = 20

// SearchResult is one match from a free-text search over indexed entities.
type SearchResult struct {
	// Kind is "person" or "channel".
	Kind string `json:"kind"`
	// ID is the entity's identifier: a person's canonical id or a channel name.
	ID string `json:"id"`
	// Name is the display name.
	Name string `json:"name"`
	// Email is the person's work email, when the match is a person.
	Email string `json:"email,omitempty"`
	// Title is the person's job title.
	Title string `json:"title,omitempty"`
	// Team is the person's team, or a channel's stated topic.
	Team string `json:"team,omitempty"`
	// Topics are the entity's topics, strongest first.
	Topics []string `json:"topics,omitempty"`
	// Score ranks the match; higher is a closer match.
	Score float64 `json:"score"`
	// Matched names the fields the query hit, such as "team" or "topic".
	Matched []string `json:"matched,omitempty"`
}

// Search finds people and channels whose name, email, title, team, or topics
// contain the query, ranked by how directly they match: an exact name beats a
// prefix beats a substring, and a name beats an email beats a title, team, or
// topic. It is a direct lookup, distinct from Resolve, which ranks by who knows
// a subject. A limit of zero or less returns a default cap.
func Search(ix *index.Index, query string, limit int) []SearchResult {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	var out []SearchResult
	for id, p := range ix.Graph.People {
		team := teamName(ix, p)
		score, matched := scorePerson(p, team, q)
		if score <= 0 {
			continue
		}
		out = append(out, SearchResult{
			Kind: "person", ID: string(id), Name: nameOr(p.Name, string(id)),
			Email: p.Email, Title: p.Title, Team: team, Topics: topTopics(p.Topics, 8),
			Score: score, Matched: matched,
		})
	}
	for _, ch := range ix.Graph.Channels {
		score, matched := scoreChannel(ch, q)
		if score <= 0 {
			continue
		}
		out = append(out, SearchResult{
			Kind: "channel", ID: ch.Name, Name: ch.Name, Team: ch.Topic,
			Score: score, Matched: matched,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// scorePerson scores how directly a person matches the lowercased query and
// lists the fields that hit. The score is the strongest single field; a name
// carries the most weight, then email, title, team, and topic.
func scorePerson(p *model.Person, team, q string) (float64, []string) {
	var best float64
	var matched []string
	hit := func(tier float64, field string) {
		if tier > best {
			best = tier
		}
		matched = append(matched, field)
	}
	nm := strings.ToLower(p.Name)
	switch {
	case nm == q:
		hit(100, "name")
	case strings.HasPrefix(nm, q):
		hit(60, "name")
	case strings.Contains(nm, q):
		hit(40, "name")
	}
	if p.Email != "" && strings.Contains(strings.ToLower(p.Email), q) {
		hit(30, "email")
	}
	if p.Title != "" && strings.Contains(strings.ToLower(p.Title), q) {
		hit(15, "title")
	}
	if team != "" && strings.Contains(strings.ToLower(team), q) {
		hit(12, "team")
	}
	for tid := range p.Topics {
		if strings.Contains(strings.ToLower(string(tid)), q) {
			hit(8, "topic")
			break
		}
	}
	return best, matched
}

// scoreChannel scores how directly a channel matches the query, on its name
// then its stated topic.
func scoreChannel(ch *model.Channel, q string) (float64, []string) {
	var best float64
	var matched []string
	hit := func(tier float64, field string) {
		if tier > best {
			best = tier
		}
		matched = append(matched, field)
	}
	nm := strings.ToLower(ch.Name)
	switch {
	case nm == q:
		hit(90, "channel")
	case strings.HasPrefix(nm, q):
		hit(55, "channel")
	case strings.Contains(nm, q):
		hit(38, "channel")
	}
	if ch.Topic != "" && strings.Contains(strings.ToLower(ch.Topic), q) {
		hit(10, "topic")
	}
	return best, matched
}

// teamName returns the display name of a person's team, or the empty string.
func teamName(ix *index.Index, p *model.Person) string {
	if p.TeamID == "" {
		return ""
	}
	if t := ix.Graph.Teams[p.TeamID]; t != nil {
		return t.Name
	}
	return ""
}

// nameOr returns name, or fallback when name is empty.
func nameOr(name, fallback string) string {
	if strings.TrimSpace(name) == "" {
		return fallback
	}
	return name
}
