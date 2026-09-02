package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kordloom/whodar/internal/index"
	"github.com/kordloom/whodar/internal/resolve"
)

// summaryListed caps how many entries each summary section prints; the full
// data always sits beside it in the JSON files.
const summaryListed = 5

// assessSummary renders the executive summary a diligence reader opens first:
// the overall shape, the findings that matter, the questions the human team
// should put to management, and the actions the findings suggest. Every
// number in it is computed from the same data the JSON files carry; nothing
// is asserted that a reader cannot trace.
func assessSummary(
	ix *index.Index, findings []resolve.TopicRisk, ownership resolve.OwnershipReport,
	departures []resolve.DepartureImpact, spans []resolve.Span, places []resolve.Place,
) string {
	var b strings.Builder
	elevated := 0
	critical := 0
	for _, f := range findings {
		switch f.Level {
		case "critical":
			critical++
		case "elevated":
			elevated++
		}
	}

	fmt.Fprintf(&b, "# Knowledge continuity summary\n\n")
	fmt.Fprintf(&b, "Scope: %d people, %d subjects scored, sources: %s.\n\n",
		len(ix.Graph.People), len(findings), strings.Join(ix.SourceNames(), ", "))
	fmt.Fprintf(&b,
		"Observed concentration: %d subjects rest on a single person, %d on two.\n",
		critical, elevated)
	if ownership.Declared > 0 {
		fmt.Fprintf(&b,
			"Declared ownership: %d areas have an owner of record; the evidence agrees "+
				"in %d and points elsewhere in %d.\n", ownership.Declared, ownership.Held,
			len(ownership.Drift))
	}
	if len(spans) > 0 {
		fmt.Fprintf(&b,
			"Crossings: %d bodies of joined work rest on one person each.\n", len(spans))
	}
	b.WriteString("\nThe language throughout is deliberate: whodar measures observed work, " +
		"not competence. A finding says what the record shows and who it points to, " +
		"and every one traces to the evidence in the data files beside this summary.\n")

	if len(places) > 0 {
		b.WriteString("\n## The largest systems and who they rest on\n\n")
		for i, pl := range places {
			if i == summaryListed {
				fmt.Fprintf(&b, "- and %d more in systems.json\n", len(places)-i)
				break
			}
			names := make([]string, 0, len(pl.Holders))
			for _, h := range pl.Holders {
				names = append(names, h.Name)
			}
			fmt.Fprintf(&b, "- %s: %s\n", pl.Dir, strings.Join(names, ", "))
		}
	}

	if len(departures) > 0 {
		b.WriteString("\n## Where one person is the whole record\n\n")
		for i, d := range departures {
			if i == summaryListed {
				fmt.Fprintf(&b, "- and %d more in departures.json\n", len(departures)-i)
				break
			}
			line := fmt.Sprintf("- %s: ", d.Name)
			switch {
			case len(d.Sole) > 0 && len(d.Top) > 0:
				line += fmt.Sprintf("sole holder of %s; leads %s",
					listSome(d.Sole), listSome(d.Top))
			case len(d.Sole) > 0:
				line += "sole holder of " + listSome(d.Sole)
			default:
				line += "leads " + listSome(d.Top)
			}
			b.WriteString(line + "\n")
		}
	}

	if len(ownership.Drift) > 0 {
		b.WriteString("\n## Where the record and the declared owner disagree\n\n")
		drift := append([]resolve.OwnerDrift(nil), ownership.Drift...)
		sort.SliceStable(drift, func(i, j int) bool { return drift[i].Topic < drift[j].Topic })
		for i, d := range drift {
			if i == summaryListed {
				fmt.Fprintf(&b, "- and %d more in ownership.json\n", len(drift)-i)
				break
			}
			fmt.Fprintf(&b, "- %s: declared %s, observed work concentrates around %s (%s)\n",
				d.Topic, strings.Join(d.Declared, ", "), d.Actual, d.Why)
		}
	}

	questions := assessQuestions(ownership, departures, spans)
	if len(questions) > 0 {
		b.WriteString("\n## Questions for management\n\n")
		b.WriteString("Findings are the start of a conversation, not the end of one. " +
			"These are the questions the record raises; the answers belong to people.\n\n")
		for i, q := range questions {
			fmt.Fprintf(&b, "%d. %s\n", i+1, q)
		}
	}

	b.WriteString("\n## Suggested actions\n\n")
	if critical == 0 && len(ownership.Drift) == 0 && len(spans) == 0 {
		b.WriteString("Nothing here demands action. No significant subject rests on a " +
			"single person, and declared ownership matches the observed work. A report " +
			"that can say this is the same report worth believing when it cannot.\n")
	} else {
		if critical > 0 {
			fmt.Fprintf(&b, "- Confirm retention and transition plans for the people "+
				"named under sole holdings; %d subjects have no cover behind one person.\n", critical)
		}
		if len(ownership.Drift) > 0 {
			fmt.Fprintf(&b, "- Reconcile the %d drifted areas: either the declaration "+
				"or the staffing is out of date, and which one it is changes the fix.\n",
				len(ownership.Drift))
		}
		if len(spans) > 0 {
			fmt.Fprintf(&b, "- For each crossing, have a second person walk the joined "+
				"work end to end; the risk is not either area but knowing they belong together.\n")
		}
	}
	return b.String()
}

// assessQuestions derives the questions a diligence team should ask, each
// grounded in a specific finding rather than a template.
func assessQuestions(
	ownership resolve.OwnershipReport, departures []resolve.DepartureImpact,
	spans []resolve.Span,
) []string {
	var out []string
	for i, d := range ownership.Drift {
		if i == summaryListed {
			break
		}
		out = append(out, fmt.Sprintf(
			"The record names %s as owner of %s, but observed work concentrates around %s. "+
				"What is the working arrangement, and which of the two should a buyer rely on?",
			strings.Join(d.Declared, ", "), d.Topic, d.Actual))
	}
	for i, d := range departures {
		if i == 3 || len(d.Sole) == 0 {
			break
		}
		out = append(out, fmt.Sprintf(
			"%s is the only person with recorded work in %s. Is that reflected in "+
				"retention planning, and where would a successor start?",
			d.Name, listSome(d.Sole)))
	}
	for i, s := range spans {
		if i == 3 {
			break
		}
		out = append(out, fmt.Sprintf(
			"%s is the only person who has worked across %s together. Who else "+
				"understands how they fit?", s.Person, strings.Join(s.Topics, " and ")))
	}
	return out
}

// listSome renders a topic list capped for prose.
func listSome(topics []string) string {
	if len(topics) <= 4 {
		return strings.Join(topics, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(topics[:4], ", "), len(topics)-4)
}
