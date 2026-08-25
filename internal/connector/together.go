package connector

import (
	"sort"
	"strings"

	"github.com/kordloom/whodar/internal/util"
)

// Bounds on what counts as two subjects being worked on together. They are
// shared by every source so a tie means the same thing wherever it came from.
const (
	// maxTogether is the most subjects one piece of work may touch and still
	// say anything about them. A rename swept across two hundred areas does not
	// make those areas one body of knowledge, and neither does an issue tagged
	// with everything.
	maxTogether = 18
	// minTogether is how many separate pieces of work must have paired two
	// subjects before the pairing is more than a coincidence.
	minTogether = 3
	// minSubjectItems is how often a subject must appear at all before its ties
	// mean anything.
	minSubjectItems = 5
	// ubiquitousShare is the share of work above which a subject is the
	// scaffolding everything touches rather than a subject of its own, and so
	// is tied to everything by construction.
	ubiquitousShare = 0.35
	// ubiquitousFloor is the amount of history below which that share means
	// nothing. In a young project a real subject is genuinely touched by half
	// of it, and calling that scaffolding would leave nothing tied to anything.
	ubiquitousFloor = 200
	// maxLinks bounds how many ties one subject keeps, strongest first.
	maxLinks = 12
	// minLinkWeight only guards against arithmetic noise. It is deliberately
	// almost zero: how much of the time two subjects move as one thing is a
	// tiny number whenever both are also worked on alone, which is normal, and
	// a floor set by eye cuts the real ties along with the noise. What makes a
	// tie trustworthy is minTogether, several separate pieces of work, and what
	// bounds the result is maxLinks keeping only the strongest.
	minLinkWeight = 0.001
)

// subjectPair is two subject names in a fixed order, so a pairing is counted
// once however the two were encountered.
type subjectPair struct {
	// A is the alphabetically first subject.
	A string
	// B is the other one.
	B string
}

// pairOf orders two subjects into a pair.
func pairOf(a, b string) subjectPair {
	if a > b {
		a, b = b, a
	}
	return subjectPair{A: a, B: b}
}

// togetherIndex accumulates which subjects are worked on together, and who has
// worked across each pairing. It is the one thing a source can say about a
// subject that does not run through the people who hold it: two areas changed
// in the same commit, or fixed in the same issue, are related whoever did it.
//
// Every source feeds the same accumulator so a tie carries the same meaning
// wherever it came from. A source with units of work that name several subjects
// at once, which is most of them, calls note; git pairs across paths itself and
// calls begin and pair, because two spellings of one path are not two subjects
// meeting.
type togetherIndex struct {
	// items is how many pieces of work have been read, the denominator for
	// deciding which subjects are scaffolding.
	items int
	// seen counts the pieces of work each subject appeared in.
	seen map[string]int
	// pairs counts the pieces of work in which two subjects appeared at once.
	pairs map[subjectPair]int
	// spanned records who has worked across each pairing, which is what tells a
	// connection everybody makes from one only ever made by a single person.
	spanned map[subjectPair]map[string]bool
}

// newTogether returns an empty accumulator.
func newTogether() *togetherIndex {
	return &togetherIndex{
		seen:    make(map[string]int),
		pairs:   make(map[subjectPair]int),
		spanned: make(map[subjectPair]map[string]bool),
	}
}

// begin records one piece of work and the distinct subjects it named. It
// reports whether those subjects are worth pairing: fewer than two say nothing
// about each other, and too many say nothing at all.
func (t *togetherIndex) begin(subjects map[string]bool) bool {
	t.items++
	for s := range subjects {
		t.seen[s]++
	}
	return len(subjects) >= 2 && len(subjects) <= maxTogether
}

// pair records that one person worked across two subjects in one piece of work.
func (t *togetherIndex) pair(a, b, who string) {
	p := pairOf(a, b)
	t.pairs[p]++
	by := t.spanned[p]
	if by == nil {
		by = make(map[string]bool)
		t.spanned[p] = by
	}
	if who != "" {
		by[who] = true
	}
}

// note records one piece of work that named several subjects at once, such as
// an issue, a page, or a thread. Everything it named met everything else it
// named, which is what makes those sources simpler than a repository: there are
// no directories above the work to mistake for the work itself.
func (t *togetherIndex) note(subjects []string, who string) {
	// Normalized here rather than by each caller: a source that hands over
	// "Matter" and one that hands over "matter" mean the same subject, and
	// counting them apart would split a pairing below the floor that makes it
	// trustworthy.
	distinct := make(map[string]bool, len(subjects))
	for _, s := range subjects {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			distinct[s] = true
		}
	}
	if !t.begin(distinct) {
		return
	}
	names := make([]string, 0, len(distinct))
	for s := range distinct {
		names = append(names, s)
	}
	sort.Strings(names)
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if util.SameFamily(names[i], names[j]) {
				continue
			}
			t.pair(names[i], names[j], who)
		}
	}
}

// records renders the accumulated ties as one record per subject, naming what
// it is worked on alongside and how many people have ever worked across the two.
func (t *togetherIndex) records(source string) []Record {
	if t.items == 0 {
		return nil
	}
	ceiling := float64(t.items) * ubiquitousShare
	if t.items < ubiquitousFloor {
		ceiling = float64(t.items)
	}
	links := make(map[string][]TopicLink)
	for p, n := range t.pairs {
		if n < minTogether {
			continue
		}
		na, nb := t.seen[p.A], t.seen[p.B]
		if na < minSubjectItems || nb < minSubjectItems {
			continue
		}
		if float64(na) > ceiling || float64(nb) > ceiling {
			continue
		}
		// How much of the time the two move as one thing.
		weight := float64(n) / float64(na+nb-n)
		if weight < minLinkWeight {
			continue
		}
		by := t.spanned[p]
		sole := ""
		if len(by) == 1 {
			for who := range by {
				sole = who
			}
		}
		links[p.A] = append(links[p.A],
			TopicLink{To: p.B, Weight: weight, Witnesses: len(by), Sole: sole})
		links[p.B] = append(links[p.B],
			TopicLink{To: p.A, Weight: weight, Witnesses: len(by), Sole: sole})
	}
	names := make([]string, 0, len(links))
	for name := range links {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]Record, 0, len(names))
	for _, name := range names {
		ties := links[name]
		sort.Slice(ties, func(i, j int) bool {
			if ties[i].Weight != ties[j].Weight {
				return ties[i].Weight > ties[j].Weight
			}
			return ties[i].To < ties[j].To
		})
		if len(ties) > maxLinks {
			ties = ties[:maxLinks]
		}
		out = append(out, Record{Kind: KindTopic, Source: source, Name: name, Links: ties})
	}
	return out
}

// absorb folds another accumulator into this one, which is how the workers of a
// parallel walk combine what each of them saw without sharing a map.
func (t *togetherIndex) absorb(o *togetherIndex) {
	if o == nil {
		return
	}
	t.items += o.items
	for s, n := range o.seen {
		t.seen[s] += n
	}
	for p, n := range o.pairs {
		t.pairs[p] += n
	}
	for p, by := range o.spanned {
		into := t.spanned[p]
		if into == nil {
			into = make(map[string]bool)
			t.spanned[p] = into
		}
		for who := range by {
			into[who] = true
		}
	}
}
