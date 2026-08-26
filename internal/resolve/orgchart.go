package resolve

import (
	"sort"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/model"
)

// Seat is one person in the organization chart, with what whodar knows about
// them and everyone reporting to them.
//
// A chart drawn from a directory alone says who reports to whom and nothing
// else, which is the part every company already has. What makes this one worth
// drawing is the second half of each seat: what the person actually works on,
// what rests on them alone, and what they have stopped touching. Read down a
// branch and the shape of a team's knowledge is visible, including the places it
// has quietly narrowed to one person.
type Seat struct {
	// ID is the person's canonical identifier.
	ID string `json:"id"`
	// Name is their display name.
	Name string `json:"name"`
	// Title is their job title, when a source of record gave one.
	Title string `json:"title,omitempty"`
	// Team is the team they belong to, when known.
	Team string `json:"team,omitempty"`
	// Knows are the subjects they hold, strongest first.
	Knows []string `json:"knows,omitempty"`
	// Alone are the subjects nobody else holds at all. Those leave with them.
	Alone []string `json:"alone,omitempty"`
	// Quiet are subjects they still know best but have stopped working on,
	// which is a sharper warning than concentration by itself.
	Quiet []string `json:"quiet,omitempty"`
	// Reports are the people who report to them, by name.
	Reports []Seat `json:"reports,omitempty"`
}

// Size counts this seat and everyone beneath it.
func (s Seat) Size() int {
	n := 1
	for _, r := range s.Reports {
		n += r.Size()
	}
	return n
}

// Chart is the organization as whodar sees it.
type Chart struct {
	// Roots are the people nobody reports to, each carrying their branch.
	Roots []Seat `json:"roots"`
	// People is how many are in the chart altogether.
	People int `json:"people"`
	// Unplaced is how many people the chart could not seat, because no source
	// of record said who they report to. They are not in Roots: a chart that
	// silently promotes everyone with no manager to the top reads as an
	// organization of a hundred chief executives.
	Unplaced int `json:"unplaced"`
}

// knownPerSeat is how many subjects a seat carries. A chart is read at a
// glance, and a list long enough to need scrolling defeats that.
const knownPerSeat = 6

// OrgChart assembles the organization from the management chain a source of
// record gave, and hangs what whodar learned on each person.
//
// Only people a source placed appear. Inferring a chain from who works with
// whom was considered and left out: the whole value of the chart is that the
// structure is stated and the knowledge is measured, so mixing the two would
// leave a reader unable to tell which half they were looking at.
func OrgChart(ix *index.Index) Chart {
	if ix == nil || len(ix.Graph.People) == 0 {
		return Chart{}
	}
	holders := topicHolders(ix)

	// Everyone who reports to each manager, by canonical id.
	reports := make(map[model.ID][]model.ID, len(ix.Graph.People))
	var roots []model.ID
	for id, p := range ix.Graph.People {
		if id != ix.CanonicalID(id) {
			continue
		}
		mgr := ix.CanonicalID(model.ID(p.ManagerID))
		switch {
		case p.ManagerID == "" || mgr == id:
			roots = append(roots, id)
		default:
			reports[mgr] = append(reports[mgr], id)
		}
	}

	// A root with nobody under it was never placed by anyone; it is a person
	// the directory does not describe, not the head of the company.
	var heads []model.ID
	unplaced := 0
	for _, id := range roots {
		if len(reports[id]) == 0 {
			unplaced++
			continue
		}
		heads = append(heads, id)
	}
	sort.Slice(heads, func(i, j int) bool { return heads[i] < heads[j] })

	seen := make(map[model.ID]bool, len(ix.Graph.People))
	chart := Chart{Unplaced: unplaced}
	for _, id := range heads {
		seat := buildSeat(ix, id, reports, holders, seen)
		chart.Roots = append(chart.Roots, seat)
		chart.People += seat.Size()
	}
	return chart
}

// buildSeat fills one person's seat and everyone below them. The seen set stops
// a cycle in the stated chain from recursing forever, which a hand-maintained
// directory will eventually contain.
func buildSeat(ix *index.Index, id model.ID, reports map[model.ID][]model.ID,
	holders map[string]map[model.ID]bool, seen map[model.ID]bool,
) Seat {
	seen[id] = true
	p := ix.Graph.People[id]
	seat := Seat{ID: string(id)}
	if p == nil {
		return seat
	}
	seat.Name, seat.Title = p.Name, p.Title
	if t := ix.Graph.Teams[model.ID(p.TeamID)]; t != nil {
		seat.Team = t.Name
	}
	// Only stated subjects, and no falling back to mined words when there are
	// none. A profile answers about one person and owes them an answer; a chart
	// is scanned, and filling two hundred seats with words lifted from chat
	// hides the handful of seats that carry something.
	stated := make(map[model.ID]float64, len(p.Topics))
	for id, w := range p.Topics {
		if ix.Graph.Topics[id].Salient() {
			stated[id] = w
		}
	}
	seat.Knows = topTopics(stated, knownPerSeat)

	for _, name := range seat.Knows {
		if len(holders[name]) == 1 {
			seat.Alone = append(seat.Alone, name)
		}
		// Present in Topics and absent from Recent means the work stopped.
		tid := model.ID(name)
		if _, still := p.Recent[tid]; !still && p.Topics[tid] > 0 {
			seat.Quiet = append(seat.Quiet, name)
		}
	}

	kids := append([]model.ID(nil), reports[id]...)
	sort.Slice(kids, func(i, j int) bool {
		a, b := ix.Graph.People[kids[i]], ix.Graph.People[kids[j]]
		switch {
		case a == nil || b == nil:
			return kids[i] < kids[j]
		case a.Name != b.Name:
			return a.Name < b.Name
		default:
			return kids[i] < kids[j]
		}
	})
	for _, kid := range kids {
		if seen[kid] {
			continue
		}
		seat.Reports = append(seat.Reports, buildSeat(ix, kid, reports, holders, seen))
	}
	return seat
}
