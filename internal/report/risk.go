// Package report renders whodar findings as a self-contained HTML document.
// The point of the format is that it travels: a manager can forward the file
// to somebody who has never installed whodar, and it still explains itself.
package report

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"sort"
	"time"

	"github.com/kordloom/whodar/internal/resolve"
)

//go:embed risk.html
var files embed.FS

// Brief is a knowledge-risk report: the finding, plus everything a reader
// needs to judge it without whodar in front of them.
type Brief struct {
	// Generated is when the report was written.
	Generated time.Time
	// People is how many people the index holds.
	People int
	// Scored is how many subjects were scored, which is not the same as how
	// many are listed: the table may show only the worst of them.
	Scored int
	// Sources names the connectors the index was built from, which is what
	// tells a reader how much of the company the finding actually saw.
	Sources []string
	// Risks are the subjects listed, most concentrated first.
	Risks []resolve.TopicRisk
	// Totals counts every scored subject, not only the listed ones, so the
	// headline figures do not shrink when the table is capped.
	Totals Counts
	// Exposed are the people the risks trace back to, most exposed first.
	Exposed []Exposure
	// Regions are the connected bodies of work that rest on one person:
	// subjects changed together where the same person leads every one. They are
	// the largest findings in the report, because whoever picks one up has to
	// learn the whole of it rather than a subject at a time.
	Regions []resolve.Region
}

// Exposure is one person and the knowledge that would leave with them.
type Exposure struct {
	// Name is the person's display name.
	Name string
	// ID is the person's canonical identifier.
	ID string
	// Sole are the topics no one else covers at all.
	Sole []string
	// Leading are the topics they lead while others still contribute.
	Leading []string
}

// Exposures turns per-topic risk into per-person risk, which is the form the
// question actually gets asked in: not "which topics are concentrated" but
// "who can we not afford to lose". A topic with a single expert is theirs
// alone; a topic they merely lead still has cover behind them.
func Exposures(risks []resolve.TopicRisk) []Exposure {
	byID := make(map[string]*Exposure)
	at := func(e resolve.RiskExpert) *Exposure {
		if ex := byID[e.ID]; ex != nil {
			return ex
		}
		ex := &Exposure{Name: e.Name, ID: e.ID}
		byID[e.ID] = ex
		return ex
	}
	for _, r := range risks {
		if len(r.Experts) == 0 {
			continue
		}
		top := r.Experts[0]
		if len(r.Experts) == 1 {
			ex := at(top)
			ex.Sole = append(ex.Sole, r.Topic)
			continue
		}
		if r.Level != "ok" {
			ex := at(top)
			ex.Leading = append(ex.Leading, r.Topic)
		}
	}
	out := make([]Exposure, 0, len(byID))
	for _, ex := range byID {
		sort.Strings(ex.Sole)
		sort.Strings(ex.Leading)
		out = append(out, *ex)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Sole) != len(out[j].Sole) {
			return len(out[i].Sole) > len(out[j].Sole)
		}
		if len(out[i].Leading) != len(out[j].Leading) {
			return len(out[i].Leading) > len(out[j].Leading)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Counts summarizes a brief for its headline figures.
type Counts struct {
	// Critical is how many topics scored critical.
	Critical int
	// Elevated is how many topics scored elevated.
	Elevated int
	// SinglePoint is how many topics rest on exactly one person.
	SinglePoint int
	// Exposed is how many people solely hold at least one topic.
	Exposed int
}

// Count tallies the headline figures over every scored subject. It is taken
// before any cap on the table, so capping what is listed never quietly shrinks
// the numbers the report leads with.
func Count(risks []resolve.TopicRisk, exposed []Exposure) Counts {
	var c Counts
	for _, r := range risks {
		switch r.Level {
		case "critical":
			c.Critical++
		case "elevated":
			c.Elevated++
		}
		if len(r.Experts) == 1 {
			c.SinglePoint++
		}
	}
	for _, ex := range exposed {
		if len(ex.Sole) > 0 {
			c.Exposed++
		}
	}
	return c
}

// funcs are the small formatting helpers the template needs. Everything else
// is done in Go, so the template stays a document rather than a program.
var funcs = template.FuncMap{
	"pct": func(f float64) string { return fmt.Sprintf("%.0f%%", f*100) },
	"join": func(parts []string) string {
		out := ""
		for i, p := range parts {
			if i > 0 {
				out += ", "
			}
			out += p
		}
		return out
	},
	"date": func(t time.Time) string { return t.Format("2 January 2006, 15:04 MST") },
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	},
}

// WriteRisk renders the brief as one self-contained HTML file. Nothing is
// fetched at open time, so the report reads the same offline, from an email
// attachment, or years later.
func WriteRisk(w io.Writer, b Brief) error {
	tmpl, err := template.New("risk.html").Funcs(funcs).ParseFS(files, "risk.html")
	if err != nil {
		return fmt.Errorf("report: parse template: %w", err)
	}
	if err := tmpl.Execute(w, b); err != nil {
		return fmt.Errorf("report: render: %w", err)
	}
	return nil
}
