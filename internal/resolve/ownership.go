package resolve

import (
	"math"
	"sort"
	"strings"

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
	// DeclaredIDs are the owners of record by canonical id, in the same order
	// as Declared, so a reader can be taken to the person rather than shown a
	// name they then have to go and look up.
	DeclaredIDs []string `json:"declaredIds,omitempty"`
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

// isTeam reports whether an owner of record is a team rather than a person. A
// CODEOWNERS entry may name @org/team, which keeps its slash through to the id.
//
// A team cannot be displaced, because a team does not commit. Measured on
// prometheus/prometheus, whose CODEOWNERS assigns almost everything to
// @prometheus/default-maintainers: every area it owns looked as though the work
// had moved away, because the owner of record could never have done any. Both
// verifiable findings there were wrong for this one reason. Whodar cannot
// enumerate a team's membership from a CODEOWNERS file, so the honest answer is
// that these areas cannot be judged, not that they have drifted.
func isTeam(id model.ID) bool {
	s := string(id)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[i+1:]
	}
	return strings.Contains(s, "/")
}

// allTeams reports whether every owner of record for an area is a team.
func allTeams(owners map[model.ID]bool) bool {
	for oid := range owners {
		if !isTeam(oid) {
			return false
		}
	}
	return len(owners) > 0
}

// driftMargin is how far a challenger must be ahead of the owner of record, in
// the area itself, before the work is said to have moved.
//
// One is not a tuned number, it is what the claim means: nobody has taken an
// area off its owner while doing less of the work in it than they do. Anything
// below one is not a weak finding, it is a contradiction.
//
// Demanding more was tried and is worse. At two, five of seven findings checked
// by hand and confirmed correct were suppressed, including an area where the
// challenger had eleven changes to the owner's five. The ratios of confirmed
// findings run 0.93, 1.40, 1.61, 2.01, 2.32 against 0.11, 0.14, 0.25, 0.40 for
// confirmed bogus ones, so no threshold separates them cleanly and one is where
// the reasoning, rather than the data, puts the line.
const driftMargin = 1.0

// minDriftEvidence is how much work in the area a challenger needs before the
// claim is worth making at all. Below it the finding is a coin toss between two
// people with a change each.
//
// Checked against every drift finding on home-assistant/core, scored from raw
// git rather than from whodar: is the named person also the top focused
// committer of that component and its tests?
//
//	none  72% correct of 48 findings
//	5     80% of 40
//	10    86% of 23
//	14    90% of 20
//	20    100% of 12
//
// Precision is bought with findings, and this end of the curve is deliberate.
// Higher is tempting, and two things argue against it: a hundred percent of
// twelve is a small sample rather than a strong guarantee, and this is an
// absolute quantity of work tuned on a repository with a hundred and fifteen
// thousand commits, so a large floor could report nothing at all on a small one
// and has not been tested there. Raise it once there is a second repository to
// check against.
const minDriftEvidence = 5.0

// workIn is how much real work somebody has done in one area. Weight a source
// of record assigned does not count, or an owner would look active in an area
// the moment their name was written next to it.
func workIn(ix *index.Index, who model.ID, topic model.ID) float64 {
	p := ix.Graph.People[ix.CanonicalID(who)]
	if p == nil {
		return 0
	}
	// Work done inside the area, not a file elsewhere that carries its name.
	// Home Assistant names a platform file after the platform it implements, so
	// editing voip/assist_satellite.py is real work on assist-satellite and says
	// nothing about who owns the integration called assist_satellite. Counting
	// it handed that integration to somebody with no change in it at all, over
	// the eight-commit maintainer.
	//
	// A source that reports no paths has no Direct at all, and falling back to
	// the whole weight keeps ownership working for it rather than reporting
	// that nobody owns anything.
	if len(p.Direct) > 0 {
		return p.Direct[topic]
	}
	if w := p.Topics[topic] - p.Stated[topic]; w > 0 {
		return w
	}
	return 0
}

// displaced reports whether challenger has taken an area off its owners of
// record, and is the whole of what drift means.
//
// It is a comparison between the incumbent and the challenger, not a contest
// the whole company enters. Ranking everybody and calling the winner the owner
// reported drift wherever somebody happened to edge ahead: on
// home-assistant/core it fired on areas where the owner and the challenger had
// one change each, which is a coin toss rather than a finding. An earlier
// attempt at a fix applied a floor to every candidate, which threw out the
// incumbent along with the noise and guaranteed drift whenever an owner's own
// work was modest. It won 16 areas and lost 35.
//
// An owner who has never worked in the area at all is not handled here. That is
// paper ownership, which ownerStanding reports separately and which needs no
// margin: there is nothing to displace.
func displaced(ix *index.Index, owners map[model.ID]bool, challenger model.ID, topic model.ID) bool {
	var most float64
	for oid := range owners {
		if w := workIn(ix, oid, topic); w > most {
			most = w
		}
	}
	if most <= 0 {
		return true
	}
	mine := workIn(ix, challenger, topic)
	// Enough to be worth saying. Below this the finding is a coin toss between
	// two people with a change each, and reporting it as ownership having moved
	// is a claim the evidence cannot carry.
	if mine < minDriftEvidence {
		return false
	}
	return mine >= most*driftMargin
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
	// KNOWN WEAK, and the fix that looked obvious is worse. Checked by hand
	// against home-assistant/core, ten of sixteen drift findings were
	// questionable: ecovacs named a contributor with one commit over the
	// maintainer with twenty-seven. Two causes compound. Weight counts
	// file-touches, so one commit over twelve files reads like sustained work.
	// And dividing by everything a person does cannot tell a prolific person
	// who genuinely owns this area from one who swept through it.
	//
	// Requiring a candidate to hold at least half the area's work against
	// whoever holds most was measured on the same 405 areas: it won 16 and lost
	// 35. Do not reapply it. A real fix means counting units of work per
	// subject rather than file-touches, so a single wide change stops looking
	// like a history of them.
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
	report := OwnershipReport{}
	for _, a := range OwnedAreas(ix) {
		report.Declared++
		switch a.Standing {
		case StandingHeld:
			report.Held++
			continue // the declared owner is the one doing the work: no drift
		case standingSilent:
			report.Silent++
		case standingUnworked:
			report.Unworked++
		default:
			report.Trailing++
		}
		report.Drift = append(report.Drift, OwnerDrift{
			Topic: a.Topic, Declared: a.Declared, DeclaredIDs: a.DeclaredIDs,
			Actual: a.Actual, ActualID: a.ActualID, Why: a.Standing,
		})
	}
	sort.Slice(report.Drift, func(i, j int) bool { return report.Drift[i].Topic < report.Drift[j].Topic })
	return report
}

// StandingHeld marks an area whose owner of record is also the person doing the
// work. The other three standings say why an area is not held.
const StandingHeld = "owner leads their own area"

// OwnedArea is one declared area set against who actually does the work there.
type OwnedArea struct {
	// Topic is the area a source of record assigned an owner.
	Topic string `json:"topic"`
	// Standing is StandingHeld, or which of the three ways the owner of record
	// is not the person leading it.
	Standing string `json:"standing"`
	// Declared names the owners of record, alphabetically, and DeclaredIDs
	// their canonical ids in the same order.
	Declared    []string `json:"declared,omitempty"`
	DeclaredIDs []string `json:"declaredIds,omitempty"`
	// Actual is the person the work says leads it, and ActualID their id.
	Actual   string `json:"actual,omitempty"`
	ActualID string `json:"actualId,omitempty"`
}

// Answerable reports whether this area can fairly be used to judge ranking. An
// owner with no recorded work in the area could not be named first by any
// ranking, so scoring those measures how much was indexed rather than how well
// it ranks.
func (a OwnedArea) Answerable() bool {
	return a.Standing == StandingHeld || a.Standing == standingTrailing
}

// OwnedAreas returns every area with an owner of record, sorted by area, set
// against who the work says leads it.
//
// Ownership summarizes this into counts. It is exported separately because a
// count cannot answer whether two indexes did better or worse on the SAME
// areas, and comparing overall scores across indexes that answer different
// questions has been wrong every time it was tried.
func OwnedAreas(ix *index.Index) []OwnedArea {
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
	var out []OwnedArea
	for _, tr := range Risk(ix, 0) {
		owners := declared[model.ID(tr.Topic)]
		if len(owners) == 0 || len(tr.Experts) == 0 {
			continue
		}
		// An area owned only by a team cannot be scored either way: there is no
		// person to have held it and none to have lost it.
		if allTeams(owners) {
			continue
		}
		actual := actualOwner(ix, tr)
		area := OwnedArea{Topic: tr.Topic, Actual: actual.Name, ActualID: actual.ID}
		switch {
		case owners[ix.CanonicalID(model.ID(actual.ID))]:
			area.Standing = StandingHeld
		case !displaced(ix, owners, model.ID(actual.ID), model.ID(tr.Topic)):
			// Somebody else ranks higher, but not by enough to say the job has
			// changed hands. The owner of record still holds the area.
			area.Standing = StandingHeld
		default:
			area.Standing = ownerStanding(ix, owners, model.ID(tr.Topic))
		}
		type owner struct{ Name, ID string }
		list := make([]owner, 0, len(owners))
		for oid := range owners {
			list = append(list, owner{Name: personName(ix, oid), ID: string(oid)})
		}
		// Sorted by name, and the ids carried along, so the two slices stay in
		// step. Sorting the names alone and the ids separately would pair each
		// person with somebody else's identifier.
		sort.Slice(list, func(i, j int) bool {
			if list[i].Name != list[j].Name {
				return list[i].Name < list[j].Name
			}
			return list[i].ID < list[j].ID
		})
		for _, o := range list {
			area.Declared = append(area.Declared, o.Name)
			area.DeclaredIDs = append(area.DeclaredIDs, o.ID)
		}
		out = append(out, area)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Topic < out[j].Topic })
	return out
}
