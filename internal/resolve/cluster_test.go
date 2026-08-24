package resolve

import (
	"testing"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
)

// TestRelated checks that topics held by the same people are reported as
// related, that an unrelated topic is not, and that a smaller expert set reads
// as the narrower topic.
func TestRelated(t *testing.T) {
	t.Parallel()
	ix := index.New()
	g := ix.Graph
	topic := func(id string) {
		g.Topics[model.ID(id)] = &model.Topic{ID: model.ID(id), Name: id, Curated: true}
	}
	topic("billing")
	topic("billing-retries")
	topic("kafka")
	person := func(id string, topics ...string) {
		p := &model.Person{ID: model.ID(id), Topics: make(map[model.ID]float64)}
		for _, tp := range topics {
			p.Topics[model.ID(tp)] = 1
		}
		g.People[model.ID(id)] = p
	}
	// Everyone on retries also does billing; billing has one extra person, so
	// retries is the narrower topic. Nobody who does billing touches kafka.
	person("a@x.com", "billing", "billing-retries")
	person("b@x.com", "billing", "billing-retries")
	person("c@x.com", "billing")
	person("d@x.com", "kafka")

	rel := Related(ix, "billing", 0)
	if len(rel) != 1 {
		t.Fatalf("Related(billing) = %+v, want just billing-retries", rel)
	}
	if rel[0].Topic != "billing-retries" {
		t.Errorf("related topic = %q, want billing-retries", rel[0].Topic)
	}
	if !rel[0].Narrower {
		t.Errorf("billing-retries should read as narrower than billing: %+v", rel[0])
	}
	if got := rel[0].Overlap; got != 1 {
		t.Errorf("overlap = %v, want 1: every retries expert also does billing", got)
	}
	if rel := Related(ix, "kafka", 0); len(rel) != 0 {
		t.Errorf("Related(kafka) = %+v, want none: no shared experts", rel)
	}
	if rel := Related(ix, "nonexistent", 0); rel != nil {
		t.Errorf("Related(nonexistent) = %+v, want nil", rel)
	}
}
