package simorg

import (
	"context"
	"strings"
	"testing"

	"github.com/kordloom/whodar/internal/recall"
)

// TestRecallPipeline runs the whole recall path over the simulated workspace:
// conversations are collected from Slack, kept with their content, and found
// again by the person who took part in them.
func TestRecallPipeline(t *testing.T) {
	t.Parallel()
	ix, err := BuildIndex(t.TempDir())
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	store, err := BuildEpisodes()
	if err != nil {
		t.Fatalf("BuildEpisodes: %v", err)
	}
	if store.Len() == 0 {
		t.Fatal("no conversations were remembered")
	}

	res := recall.New(store, ix)
	ans := res.Resolve(context.Background(), recall.Query{
		Text: "certificate renewal failing", Person: "angela@corp.com", Explain: true,
	})
	if len(ans.Episodes) == 0 {
		t.Fatalf("no conversation found; scope = %+v", ans.Scope)
	}
	ep := ans.Episodes[0]
	if ep.Place != "infra" {
		t.Errorf("place = %q, want infra", ep.Place)
	}
	if !strings.HasPrefix(ep.Permalink, "https://corp.slack.com/archives/C3/p") {
		t.Errorf("permalink = %q, want a link into the workspace", ep.Permalink)
	}

	var named []string
	for _, p := range ep.People {
		named = append(named, p.Name)
	}
	if len(named) == 0 {
		t.Fatal("nobody was named as having helped")
	}
	for _, want := range []string{"Carol Lee", "Grace Kim"} {
		if !strings.Contains(strings.Join(named, ","), want) {
			t.Errorf("people = %v, want %s among them", named, want)
		}
	}
	for _, p := range ep.People {
		if p.Name == "Angela Malone" {
			t.Error("the asker was listed as someone who helped her")
		}
	}

	if ep.Solution == nil {
		t.Fatal("the kept conversation was not returned")
	}
	joined := ""
	for _, n := range ep.Solution.Notes {
		joined += n.Author + ": " + n.Text + "\n"
	}
	if !strings.Contains(joined, "certbot") || !strings.Contains(joined, "wildcard") {
		t.Errorf("conversation = %q, want the messages that solved it", joined)
	}

	// Someone who was not in the conversation must not reach it.
	other := res.Resolve(context.Background(), recall.Query{
		Text: "certificate renewal failing", Person: "dan@corp.com", Explain: true,
	})
	for _, e := range other.Episodes {
		if e.Place == "infra" && e.Solution != nil {
			t.Error("a conversation reached someone who was not in it")
		}
	}
}
