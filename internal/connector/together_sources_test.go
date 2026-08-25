package connector

import (
	"testing"
)

// TestTogetherPairsWhatOnePieceOfWorkNames checks a source whose unit of work
// names several subjects at once ties them to each other. Most sources are that
// shape: an issue carries labels, a page carries labels, and everything one of
// them names was worked on alongside everything else it named.
func TestTogetherPairsWhatOnePieceOfWorkNames(t *testing.T) {
	t.Parallel()
	ties := newTogether()
	// Enough separate pieces of work for a pairing to be more than coincidence,
	// and enough of each subject for its ties to mean anything.
	for range minSubjectItems {
		ties.note([]string{"billing", "ledger"}, "ada@x.com")
		ties.note([]string{"search", "indexing"}, "bo@x.com")
	}
	// Padding so no subject is a majority of the work and gets set aside as
	// scaffolding.
	for i := range 40 {
		ties.note([]string{"filler", "other"}, "cy@x.com")
		_ = i
	}
	recs := ties.records("jira")
	if len(recs) == 0 {
		t.Fatal("nothing was tied to anything")
	}
	find := func(from, to string) (TopicLink, bool) {
		for _, r := range recs {
			if r.Kind != KindTopic || r.Name != from {
				continue
			}
			for _, l := range r.Links {
				if l.To == to {
					return l, true
				}
			}
		}
		return TopicLink{}, false
	}
	link, ok := find("billing", "ledger")
	if !ok {
		t.Fatalf("billing was not tied to ledger: %+v", recs)
	}
	if link.Witnesses != 1 || link.Sole != "ada@x.com" {
		t.Errorf("billing to ledger: %d people, sole %q; want the one who did that work",
			link.Witnesses, link.Sole)
	}
	if _, ok := find("billing", "search"); ok {
		t.Error("billing was tied to search, which no piece of work ever named alongside it")
	}
	if recs[0].Source != "jira" {
		t.Errorf("source = %q, want the source that observed it", recs[0].Source)
	}
}

// TestTogetherIgnoresWorkThatNamesEverything checks a piece of work touching a
// great many subjects ties none of them. A sweeping rename, or an issue tagged
// with the whole taxonomy, says nothing about what belongs with what.
func TestTogetherIgnoresWorkThatNamesEverything(t *testing.T) {
	t.Parallel()
	ties := newTogether()
	sweeping := make([]string, 0, maxTogether+5)
	for i := range maxTogether + 5 {
		sweeping = append(sweeping, string(rune('a'+i%26))+"subject")
	}
	for range 10 {
		ties.note(sweeping, "ada@x.com")
	}
	if recs := ties.records("jira"); len(recs) != 0 {
		t.Errorf("records = %+v, want nothing tied by work that named everything", recs)
	}
}

// TestTogetherTreatsOneSubjectAsOneSubject checks that the same subject spelled
// differently by two sources is counted once. A pairing split across spellings
// falls below the floor that makes it trustworthy and vanishes silently.
func TestTogetherTreatsOneSubjectAsOneSubject(t *testing.T) {
	t.Parallel()
	ties := newTogether()
	for i := range minSubjectItems {
		if i%2 == 0 {
			ties.note([]string{"Billing", " Ledger "}, "ada@x.com")
			continue
		}
		ties.note([]string{"billing", "ledger"}, "ada@x.com")
	}
	for range 40 {
		ties.note([]string{"filler", "other"}, "cy@x.com")
	}
	found := false
	for _, r := range ties.records("github") {
		if r.Name != "billing" {
			continue
		}
		for _, l := range r.Links {
			if l.To == "ledger" {
				found = true
			}
		}
	}
	if !found {
		t.Error("billing and ledger were counted as four subjects, so their pairing disappeared")
	}
}
