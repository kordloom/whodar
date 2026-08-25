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
	// Why says how the owner of record stands in relation to their own area:
	// whether they have done no recorded work at all, none in this area, or
	// some but less than whoever leads it. The three are different problems and
	// call for different things to be done about them.
	Why string `json:"why"`
	// ActualID is the actual expert's canonical id.
	ActualID string `json:"actualId"`
}

// How an owner of record stands in relation to the area they own.
const (
	standingSilent   = "owner has no recorded work"
	standingUnworked = "owner works elsewhere, not here"
	standingTrailing = "owner works here but leads less"
)

// ownerStanding says which of the three an area's owners are in. Weight a
// source of record assigned does not count as work, or every owner would look
// active in everything they own the moment a CODEOWNERS file is indexed.
func ownerStanding(ix *index.Index, owners map[model.ID]bool, topic model.ID) string {
	anyWork, workedHere := false, false
	for oid := range owners {
		p := ix.Graph.People[ix.CanonicalID(oid)]
		if p == nil {
			continue
		}
		for tid, w := range p.Topics {
			if w-p.Stated[tid] <= 0 {
				continue
			}
			anyWork = true
			if tid == topic {
				workedHere = true
			}
		}
	}
	switch {
	case !anyWork:
		return standingSilent
	case !workedHere:
		return standingUnworked
	default:
		return standingTrailing
	}
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
	topic := model.ID(tr.Topic)
	best, bestScore := tr.Experts[0], -1.0
	for _, e := range tr.Experts {
		p := ix.Graph.People[ix.CanonicalID(model.ID(e.ID))]
		if p == nil {
			continue
		}
		// Only work counts. A person a source of record assigned to an area has
		// weight in it without having touched it, and their profile is narrow
		// precisely because declaring is all they have done, so scoring them on
		// it would hand them the area over whoever actually does it.
		here := p.Topics[topic] - p.Stated[topic]
		if here <= 0 {
			continue
		}
		var total float64
		for tid, w := range p.Topics {
			if work := w - p.Stated[tid]; work > 0 {
				total += work
			}
		}
		if total <= 0 {
			continue
		}
		if score := here / math.Sqrt(total); score > bestScore {
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
	// Silent counts drifted areas whose owner of record has no recorded work
	// anywhere. Read it carefully: they may have left, but a source of record
	// names people by handle and an activity source names them by address, so
	// an owner whose handle was never linked to their commits looks exactly the
	// same. It is the bucket to check an alias file against before believing.
	Silent int `json:"silent"`
	// Unworked counts drifted areas whose owner is active elsewhere but has
	// never worked in the area they own. Ownership there is paper only.
	Unworked int `json:"unworked"`
	// Trailing counts drifted areas whose owner does work in them, but less
	// than whoever now leads.
	Trailing int `json:"trailing"`
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
		why := ownerStanding(ix, owners, model.ID(tr.Topic))
		switch why {
		case standingSilent:
			report.Silent++
		case standingUnworked:
			report.Unworked++
		default:
			report.Trailing++
		}
		report.Drift = append(report.Drift, OwnerDrift{
			Topic: tr.Topic, Declared: names, Actual: actual.Name, ActualID: actual.ID, Why: why,
		})
	}
	sort.Slice(report.Drift, func(i, j int) bool { return report.Drift[i].Topic < report.Drift[j].Topic })
	return report
}
