package index

import (
	"strings"
	"sync"
)

// synonymGroups are expressions that name the same thing. When a query uses any
// member of a group, the search also tries the others, at a discount, so asking
// about "time off" finds the person the index knows for "vacation". Groups are
// deliberately conservative: a wrong synonym silently hands answers to the wrong
// person, so a term only belongs here when the equivalence holds anywhere a
// workplace would use it. Multi-word members are detected as adjacent words in
// the question; only single words can be added to a search, since the index
// posts single tokens.
var synonymGroups = [][]string{
	// The everyday questions every company has.
	{"vacation", "pto", "ooo", "time off", "days off", "out of office"},
	{"payroll", "paycheck", "salary", "compensation", "pay stub"},
	{"benefits", "insurance", "medical", "dental", "health plan"},
	{"expenses", "reimbursement", "receipts", "expense report"},
	{"laptop", "computer", "workstation", "hardware"},
	{"onboarding", "orientation", "new hire", "first day"},
	{"hiring", "recruiting", "interviews", "candidates"},
	{"office", "facilities", "parking", "desks"},
	{"contracts", "nda", "legal", "agreement"},
	// The engineering vocabulary that splits by tool and abbreviation.
	{"kubernetes", "k8s", "kube"},
	{"terraform", "iac"},
	{"postgres", "postgresql", "database"},
	{"authentication", "auth", "login", "sso", "sign in"},
	{"deploys", "deployment", "rollout", "shipping"},
	{"incident", "outage", "oncall", "paging", "on call"},
	{"monitoring", "observability", "metrics", "alerting"},
	{"billing", "invoices", "payments"},
}

// expansionPenalty scales what a synonym contributes. A synonym is the right
// idea in different words, which is close to what was asked but never quite it,
// so it must not outrank a person who matches the words themselves.
const expansionPenalty = 0.8

// expTerm is one extra term a query grows: the word to search, the original
// query words it stands in for, and the expression that triggered it, for the
// reason line.
type expTerm struct {
	// term is the word added to the search.
	term string
	// covers are the original query terms this expansion answers for, so a hit
	// through a synonym still counts as covering what was asked.
	covers []string
	// asked is the expression the person actually used.
	asked string
}

// synonym lookup tables, built once from the groups.
var (
	synOnce sync.Once
	// wordGroup maps a single word's stem to its group.
	wordGroup map[string]int
	// phraseGroup maps a two-word phrase, as joined stems, to its group.
	phraseGroup map[string]int
	// groupSingles lists each group's single-word members, the only ones that
	// can be searched.
	groupSingles [][]string
)

// buildSynonyms indexes the groups by stem for lookup during a query.
func buildSynonyms() {
	wordGroup = make(map[string]int)
	phraseGroup = make(map[string]int)
	groupSingles = make([][]string, len(synonymGroups))
	for g, group := range synonymGroups {
		for _, member := range group {
			parts := strings.Fields(member)
			switch len(parts) {
			case 1:
				wordGroup[stem(parts[0])] = g
				groupSingles[g] = append(groupSingles[g], parts[0])
			case 2:
				phraseGroup[stem(parts[0])+" "+stem(parts[1])] = g
			}
		}
	}
}

// expandTerms returns the synonyms a question brings in: every single-word
// member of every group the question touches, minus the words already asked.
// Ordered tokens are scanned so a two-word expression is recognized as the
// adjacent pair the person typed, not as two unrelated words.
func expandTerms(ordered []string) []expTerm {
	synOnce.Do(buildSynonyms)
	type trigger struct {
		covers []string
		asked  string
	}
	triggered := make(map[int]trigger)
	for i := 0; i+1 < len(ordered); i++ {
		key := stem(ordered[i]) + " " + stem(ordered[i+1])
		if g, ok := phraseGroup[key]; ok {
			if _, seen := triggered[g]; !seen {
				triggered[g] = trigger{
					covers: []string{ordered[i], ordered[i+1]},
					asked:  ordered[i] + " " + ordered[i+1],
				}
			}
		}
	}
	for _, tok := range ordered {
		if g, ok := wordGroup[stem(tok)]; ok {
			if _, seen := triggered[g]; !seen {
				triggered[g] = trigger{covers: []string{tok}, asked: tok}
			}
		}
	}
	if len(triggered) == 0 {
		return nil
	}

	present := make(map[string]bool, len(ordered))
	for _, tok := range ordered {
		present[stem(tok)] = true
	}
	var out []expTerm
	for g, trig := range triggered {
		for _, w := range groupSingles[g] {
			if present[stem(w)] {
				continue
			}
			present[stem(w)] = true
			out = append(out, expTerm{term: w, covers: trig.covers, asked: trig.asked})
		}
	}
	return out
}
