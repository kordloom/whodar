package resolve

import (
	"math"
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

// actualOwner picks the person an area has really moved to. The largest share
// of a subject is not the answer on its own: every organization has a few
// people who touch everything, and by raw share they out-hold the declared
// owner of almost every area at once. Saying ownership drifted to them in a
// thousand places is not a finding about ownership, it is a finding about them.
//
// Weight is discounted by the square root of everything that person does, so
// somebody has to have done a lot of the work AND have it be a real part of
// what they do. That keeps the prolific in contention where they genuinely
// lead, without handing them every area in the company.
func actualOwner(ix *index.Index, tr TopicRisk) RiskExpert {
	best, bestScore := tr.Experts[0], -1.0
	for _, e := range tr.Experts {
		p := ix.Graph.People[ix.CanonicalID(model.ID(e.ID))]
		if p == nil {
			continue
		}
		var total float64
		for _, w := range p.Topics {
			total += w
		}
		if total <= 0 {
			continue
		}
		if score := e.Share * tr.Weight / math.Sqrt(total); score > bestScore {
			best, bestScore = e, score
		}
	}
	return best
}

// OwnershipReport is what a source of record says about ownership set against
// what the work says: the areas that moved, and the count they moved out of.
// The count is the finding. A list of drifted areas invites the reading that
// these are the exceptions, and in every organization measured so far they are
// not: most declared ownership does not match who does the work.
type OwnershipReport struct {
	// Declared is how many areas have an owner of record at all.
	Declared int `json:"declared"`
	// Held is how many of those the owner of record still leads.
	Held int `json:"held"`
	// Drift are the areas whose strongest expert is somebody else, by topic.
	Drift []OwnerDrift `json:"drift"`
}

// Drifted is how many declared areas have moved to somebody else.
func (r OwnershipReport) Drifted() int { return len(r.Drift) }

// Share is the fraction of declared ownership that no longer matches who does
// the work, zero to one. It is zero when nothing was declared.
func (r OwnershipReport) Share() float64 {
	if r.Declared == 0 {
		return 0
	}
	return float64(len(r.Drift)) / float64(r.Declared)
}

// Ownership compares declared ownership, from a source of record such as
// CODEOWNERS, against who actually has the expertise, so a reader can see where
// ownership on paper has drifted from where the work happens. It is
// deterministic over the graph, no model required.
//
// It can only speak for what was indexed: an owner whose work lives somewhere
// whodar was not pointed at looks like an owner who does nothing, so the share
// it reports is an upper bound on drift rather than a measurement of it.
func Ownership(ix *index.Index) OwnershipReport {
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
	report := OwnershipReport{}
	for _, tr := range Risk(ix, 0) {
		owners := declared[model.ID(tr.Topic)]
		if len(owners) == 0 || len(tr.Experts) == 0 {
			continue
		}
		report.Declared++
		actual := actualOwner(ix, tr)
		if owners[ix.CanonicalID(model.ID(actual.ID))] {
			report.Held++
			continue // the declared owner is the one doing the work: no drift
		}
		names := make([]string, 0, len(owners))
		for oid := range owners {
			names = append(names, personName(ix, oid))
		}
		sort.Strings(names)
		report.Drift = append(report.Drift, OwnerDrift{
			Topic: tr.Topic, Declared: names, Actual: actual.Name, ActualID: actual.ID,
		})
	}
	sort.Slice(report.Drift, func(i, j int) bool { return report.Drift[i].Topic < report.Drift[j].Topic })
	return report
}
