package index

import (
	"strings"

	"github.com/kordloom/whodar/internal/connector"
	"github.com/kordloom/whodar/internal/model"
	"github.com/kordloom/whodar/internal/util"
)

// ForgetResult reports what purging a person removed.
type ForgetResult struct {
	// Records is how many stored source records were dropped.
	Records int
	// Mentions is how many references to the person were removed from other
	// records, such as channel member lists.
	Mentions int
}

// Forget removes a person from the index: every source record that is theirs,
// and every reference to them in other records, then rebuilds. The next save
// persists an index with no trace of them; a person removed here can only
// return by re-indexing a source that still contains them.
func (ix *Index) Forget(p *model.Person) ForgetResult {
	ids := forgetIDs(p)
	var res ForgetResult
	for name, recs := range ix.sources {
		kept := make([]connector.Record, 0, len(recs))
		for _, rec := range recs {
			if recordIsPerson(rec, ids) {
				res.Records++
				continue
			}
			if n := stripMentions(&rec, ids); n > 0 {
				res.Mentions += n
			}
			kept = append(kept, rec)
		}
		ix.sources[name] = kept
		ix.sourceCounts[name] = len(kept)
	}
	// The alias table would otherwise keep the one fact being erased: that
	// these identifiers were one person.
	gone := make(map[model.ID]bool, len(ids))
	for id := range ids {
		gone[model.ID(id)] = true
	}
	ix.identityResolver().Forget(gone)
	ix.rebuild()
	return res
}

// forgetIDs collects every identifier the person is known by, lowercased.
func forgetIDs(p *model.Person) map[string]bool {
	ids := make(map[string]bool)
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			ids[s] = true
		}
	}
	add(string(p.ID))
	add(p.Email)
	add(util.NormalizeEmail(p.Email))
	for _, id := range p.Identities {
		add(string(id))
	}
	return ids
}

// recordIsPerson reports whether a stored record belongs to the person.
func recordIsPerson(rec connector.Record, ids map[string]bool) bool {
	if rec.Kind != connector.KindPerson {
		return false
	}
	if ids[strings.ToLower(rec.PersonID)] || ids[util.NormalizeEmail(rec.Email)] {
		return true
	}
	for _, alt := range rec.AltIDs {
		if ids[strings.ToLower(alt)] {
			return true
		}
	}
	return false
}

// stripMentions removes references to the person from a record that is not
// theirs, and reports how many were removed.
func stripMentions(rec *connector.Record, ids map[string]bool) int {
	removed := 0
	if len(rec.Members) > 0 {
		kept := make([]string, 0, len(rec.Members))
		for _, m := range rec.Members {
			if ids[strings.ToLower(m)] {
				removed++
				continue
			}
			kept = append(kept, m)
		}
		rec.Members = kept
	}
	if ids[strings.ToLower(rec.Manager)] || ids[util.NormalizeEmail(rec.Manager)] {
		rec.Manager = ""
		removed++
	}
	return removed
}
