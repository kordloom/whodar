package index

import (
	"fmt"
	"testing"

	"github.com/kordloom/whodar/internal/connector"

	"github.com/kordloom/whodar/internal/model"
)

func TestFoldGroupName(t *testing.T) {
	t.Parallel()
	tests := []struct{ In, Want string }{
		{"store-admin", "store"},
		{"store-write", "store"},
		{"Store-Read", "store"},
		{"payments", "payments"},
		{"x-dev", "x"},
		{"-admin", "-admin"},
	}
	for _, test := range tests {
		t.Run(test.In, func(t *testing.T) {
			t.Parallel()
			if got := foldGroupName(test.In); got != test.Want {
				t.Errorf("foldGroupName(%q) = %q, want %q", test.In, got, test.Want)
			}
		})
	}
}

func TestIsOrgWide(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Size, Total int
		Want        bool
	}{
		{250, 300, true},  // over the absolute cap
		{201, 1000, true}, // over the absolute cap
		{50, 100, true},   // over 40% of a large org
		{30, 100, false},  // under 40%
		{5, 10, false},    // small org: fraction rule does not apply
	}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d", testNum), func(t *testing.T) {
			t.Parallel()
			if got := isOrgWide(test.Size, test.Total); got != test.Want {
				t.Errorf("isOrgWide(%d, %d) = %v, want %v", test.Size, test.Total, got, test.Want)
			}
		})
	}
}

// TestNearRanksBySharedGroupsAndTopics builds a small graph where one person
// shares a team, a channel, and a topic with the focal person and confirms they
// rank first, ahead of people who share only one signal.
func TestNearRanksBySharedGroupsAndTopics(t *testing.T) {
	t.Parallel()
	ix := New()
	g := ix.Graph
	g.Teams["payments"] = &model.Team{ID: "payments", Name: "payments"}
	g.Topics["t1"] = &model.Topic{ID: "t1", Name: "billing"}
	g.Topics["t2"] = &model.Topic{ID: "t2", Name: "kafka"}
	g.People["alice"] = &model.Person{ID: "alice", Name: "Alice", TeamID: "payments", Topics: map[model.ID]float64{"t1": 1, "t2": 1}}
	g.People["bob"] = &model.Person{ID: "bob", Name: "Bob", TeamID: "payments", Topics: map[model.ID]float64{"t1": 1}}
	g.People["carol"] = &model.Person{ID: "carol", Name: "Carol", Topics: map[model.ID]float64{}}
	g.People["dave"] = &model.Person{ID: "dave", Name: "Dave", Topics: map[model.ID]float64{"t2": 1}}
	g.Channels["billing"] = &model.Channel{ID: "billing", Name: "billing", Members: []model.ID{"alice", "bob", "carol"}}

	near := ix.Near("alice", 10)
	if len(near) != 3 {
		t.Fatalf("near count = %d, want 3 (%+v)", len(near), near)
	}
	if near[0].ID != "bob" {
		t.Errorf("nearest = %s, want bob", near[0].ID)
	}
	if len(near[0].Reasons) == 0 {
		t.Errorf("nearest has no reasons")
	}
	// Bob shares a team, a channel, and a topic, so he must outscore the rest.
	if !(near[0].Score > near[1].Score) {
		t.Errorf("bob score %.3f not greater than next %.3f", near[0].Score, near[1].Score)
	}
	// The focal person is never in their own results.
	for _, a := range near {
		if a.ID == "alice" {
			t.Errorf("focal person appeared in results")
		}
	}
}

// TestNearIsNotTheSamePeopleForEveryone checks proximity is measured against
// how much each person knows rather than as a raw total. Every organization has
// a few people who touch all of it, and by raw overlap they are near everybody:
// whoever you asked about, the answer came back as the same handful of names.
func TestNearIsNotTheSamePeopleForEveryone(t *testing.T) {
	t.Parallel()
	recs := []connector.Record{
		// Two pairs who genuinely work alongside each other.
		{Kind: connector.KindPerson, Name: "Radio One", Email: "r1@x.com",
			Topics: []string{"zigbee", "zigbee", "thread"}, Source: "git"},
		{Kind: connector.KindPerson, Name: "Radio Two", Email: "r2@x.com",
			Topics: []string{"zigbee", "zigbee", "thread"}, Source: "git"},
		{Kind: connector.KindPerson, Name: "Storage One", Email: "s1@x.com",
			Topics: []string{"recorder", "recorder", "history"}, Source: "git"},
		{Kind: connector.KindPerson, Name: "Storage Two", Email: "s2@x.com",
			Topics: []string{"recorder", "recorder", "history"}, Source: "git"},
	}
	// And somebody who touches everything, including both pairs.
	sweep := []string{"zigbee", "thread", "recorder", "history"}
	for i := range 60 {
		sweep = append(sweep, fmt.Sprintf("area%d", i), fmt.Sprintf("area%d", i))
	}
	recs = append(recs, connector.Record{
		Kind: connector.KindPerson, Name: "Sweeper", Email: "sweep@x.com",
		Topics: sweep, Source: "git",
	})

	ix := New()
	ix.Build(recs)
	ix.Canonicalize()

	nearest := func(who model.ID) string {
		got := ix.Near(who, 1)
		if len(got) == 0 {
			t.Fatalf("%s has nobody near them", who)
		}
		return got[0].Name
	}
	if got := nearest("r1@x.com"); got != "Radio Two" {
		t.Errorf("nearest to Radio One = %q, want their counterpart rather than the busiest person", got)
	}
	if got := nearest("s1@x.com"); got != "Storage Two" {
		t.Errorf("nearest to Storage One = %q, want their counterpart rather than the busiest person", got)
	}
}
