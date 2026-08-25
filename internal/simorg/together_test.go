package simorg

import (
	"testing"

	"github.com/kordloom/whodar/internal/resolve"
)

// TestGeneratedCompanyHasWorkThatCrossesAreas checks the sample company contains
// work spanning two subjects at once, and that whodar finds the connections.
//
// It is here because the generator once produced a company of sealed cells:
// every commit touched one area, every ticket named one label, and nothing was
// ever worked on alongside anything else. Everything passed. The connection
// graph was simply empty, and the only way that showed was by looking at it.
func TestGeneratedCompanyHasWorkThatCrossesAreas(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ix, err := BuildBigIndex(dir)
	if err != nil {
		t.Fatalf("BuildBigIndex: %v", err)
	}

	tied := 0
	for _, topic := range ix.Graph.Topics {
		if len(topic.Near) > 0 {
			tied++
		}
	}
	if tied == 0 {
		t.Fatal("no subject is worked on alongside any other, so the connection graph is empty")
	}

	spans := resolve.SoleSpans(ix, 0)
	if len(spans) == 0 {
		t.Fatal("no connection rests on a single person, which the generated company plants on purpose")
	}
	for _, s := range spans {
		if s.Person == "" {
			t.Errorf("%s with %s rests on one person but nobody is named", s.Topic, s.With)
		}
		if s.Experts < 2 {
			t.Errorf("%s with %s: %d experts; the finding is only interesting when both areas have some",
				s.Topic, s.With, s.Experts)
		}
	}

	// The same pair must not be reported twice under fragments of its own name.
	for i := range spans {
		for j := i + 1; j < len(spans); j++ {
			if sameSpanPair(spans[i], spans[j]) {
				t.Errorf("one connection reported twice: %s+%s and %s+%s",
					spans[i].Topic, spans[i].With, spans[j].Topic, spans[j].With)
			}
		}
	}
}

// sameSpanPair reports whether two findings name the same two areas.
func sameSpanPair(a, b resolve.Span) bool {
	return (a.Topic == b.Topic && a.With == b.With) || (a.Topic == b.With && a.With == b.Topic)
}
