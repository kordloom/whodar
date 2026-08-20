package resolve

import (
	"sort"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
)

// OwnerDrift is a topic whose declared owner is not the person doing the work.
type OwnerDrift struct {
	// Topic is the area of ownership.
	Topic string `json:"topic"`
	// Declared are the owners of record, by name, such as from CODEOWNERS.
	Declared []string `json:"declared"`
	// Actual is the strongest actual expert by activity, by name.
	Actual string `json:"actual"`
	// ActualID is the actual expert's canonical id.
	ActualID string `json:"actualId"`
}

// OwnershipDrift finds topics whose declared owner, from a source of record such
// as CODEOWNERS, is not the strongest actual expert by activity, so a reader can
// see where ownership on paper has drifted from where the work happens. It is
// deterministic over the graph, no model required.
func OwnershipDrift(ix *index.Index) []OwnerDrift {
	declared := make(map[model.ID]map[model.ID]bool)
	for id, p := range ix.Graph.People {
		canon := ix.CanonicalID(id)
		for _, t := range p.Owns {
			m := declared[t]
			if m == nil {
				m = make(map[model.ID]bool)
				declared[t] = m
			}
			m[canon] = true
		}
	}
	var out []OwnerDrift
	for _, tr := range Risk(ix, 0) {
		owners := declared[model.ID(tr.Topic)]
		if len(owners) == 0 || len(tr.Experts) == 0 {
			continue
		}
		if owners[ix.CanonicalID(model.ID(tr.Experts[0].ID))] {
			continue // the declared owner is the one doing the work: no drift
		}
		names := make([]string, 0, len(owners))
		for oid := range owners {
			names = append(names, personName(ix, oid))
		}
		sort.Strings(names)
		out = append(out, OwnerDrift{
			Topic: tr.Topic, Declared: names, Actual: tr.Experts[0].Name, ActualID: tr.Experts[0].ID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Topic < out[j].Topic })
	return out
}
