package index

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/kordloom/whodar/internal/model"
)

// Embedder turns text into a vector. The llm package's Ollama client satisfies
// it.
type Embedder interface {
	// Embed returns the embedding vector for text.
	Embed(ctx context.Context, text string) ([]float32, error)
}

// HasEmbeddings reports whether the index holds vectors to search semantically.
func (ix *Index) HasEmbeddings() bool {
	return len(ix.personVecs) > 0 || len(ix.channelVecs) > 0
}

// Embed fills per-person and per-channel vectors by embedding a text
// representation of each entity. An error from e aborts and is returned.
func (ix *Index) Embed(ctx context.Context, e Embedder) error {
	pv := make(map[model.ID][]float32, len(ix.Graph.People))
	for id, p := range ix.Graph.People {
		vec, err := e.Embed(ctx, personEmbedText(p, ix.texts[id]))
		if err != nil {
			return fmt.Errorf("index: embed person %s: %w", id, err)
		}
		pv[id] = vec
	}
	cv := make(map[model.ID][]float32, len(ix.Graph.Channels))
	for id, ch := range ix.Graph.Channels {
		vec, err := e.Embed(ctx, channelEmbedText(ch, ix.channelTexts[id]))
		if err != nil {
			return fmt.Errorf("index: embed channel %s: %w", id, err)
		}
		cv[id] = vec
	}
	ix.personVecs = pv
	ix.channelVecs = cv
	return nil
}

// SemanticPeople ranks people by cosine similarity to the query vector. The
// similarity doubles as the confidence, clamped at zero.
func (ix *Index) SemanticPeople(query []float32, limit int) []model.Match {
	ranked := rankByCosine(ix.personVecs, query, limit)
	out := make([]model.Match, 0, len(ranked))
	for _, r := range ranked {
		p := ix.Graph.People[r.id]
		if p == nil {
			continue
		}
		var team *model.Team
		if p.TeamID != "" {
			team = ix.Graph.Teams[p.TeamID]
		}
		out = append(out, model.Match{
			Person:     p,
			Team:       team,
			Score:      r.score,
			Confidence: max(0, r.score),
			Reasons:    []string{"semantic match"},
		})
	}
	return out
}

// SemanticChannels ranks channels by cosine similarity to the query vector and
// attaches the members most similar to the query.
func (ix *Index) SemanticChannels(query []float32, limit int) []model.ChannelMatch {
	ranked := rankByCosine(ix.channelVecs, query, limit)
	memberScores := cosineScores(ix.personVecs, query)
	out := make([]model.ChannelMatch, 0, len(ranked))
	for _, r := range ranked {
		ch := ix.Graph.Channels[r.id]
		if ch == nil {
			continue
		}
		out = append(out, model.ChannelMatch{
			Channel:    ch,
			Score:      r.score,
			Confidence: max(0, r.score),
			Reasons:    []string{"semantic match"},
			TopMembers: ix.topMembers(ch, memberScores, 3),
		})
	}
	return out
}

// scoredID pairs an entity id with its similarity score.
type scoredID struct {
	// id is the entity id.
	id model.ID
	// score is the cosine similarity.
	score float64
}

// rankByCosine scores every vector against query and returns the top entities,
// or all of them when limit is non-positive.
func rankByCosine(vecs map[model.ID][]float32, query []float32, limit int) []scoredID {
	ranked := make([]scoredID, 0, len(vecs))
	for id, vec := range vecs {
		ranked = append(ranked, scoredID{id: id, score: cosine(query, vec)})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].id < ranked[j].id
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}

// cosineScores returns the cosine similarity of each vector to query.
func cosineScores(vecs map[model.ID][]float32, query []float32) map[model.ID]float64 {
	out := make(map[model.ID]float64, len(vecs))
	for id, vec := range vecs {
		out[id] = cosine(query, vec)
	}
	return out
}

// cosine returns the cosine similarity of a and b, or 0 for empty or mismatched
// vectors.
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// personEmbedText is the text representation of a person used for embedding.
func personEmbedText(p *model.Person, pt *personText) string {
	parts := []string{p.Name, p.Title}
	if pt != nil {
		parts = append(parts, pt.Teams...)
		parts = append(parts, pt.Topics...)
		parts = append(parts, pt.Text)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// channelEmbedText is the text representation of a channel used for embedding.
func channelEmbedText(ch *model.Channel, ct *channelText) string {
	parts := []string{ch.Name, ch.Topic}
	if ct != nil {
		parts = append(parts, ct.Topics...)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
