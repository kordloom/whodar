package index

import (
	"fmt"
	"testing"

	"github.com/kordloom/whodar/internal/connector"
)

// person builds a record for somebody with the given subjects.
func person(name, email string, topics ...string) connector.Record {
	return connector.Record{
		Kind: connector.KindPerson, Name: name, Email: email,
		Topics: topics, Source: "git",
	}
}

// TestOnePersonTwoAddressesIsOnePerson covers the commonest split identity in
// real history: somebody commits from a provider's noreply address and from
// their own, and nothing links the two records except the name they sign with
// and the work they do.
func TestOnePersonTwoAddressesIsOnePerson(t *testing.T) {
	t.Parallel()
	ix := New()
	ix.Build([]connector.Record{
		person("Josef Zweck", "24647999@users.noreply.github.com", "acaia", "coffee", "scales"),
		person("Josef Zweck", "josef@zweck.dev", "acaia", "coffee", "scales", "bluetooth"),
	})
	ix.AutoJoin()
	ix.Canonicalize()

	if got := len(ix.Graph.People); got != 1 {
		var who []string
		for id := range ix.Graph.People {
			who = append(who, string(id))
		}
		t.Fatalf("graph holds %d people %v, want the two records merged into one", got, who)
	}
}

// TestSameNameMergeStaysNarrow is the guard on the join above. Collapsing two
// people who merely share a name is a worse failure than leaving one person
// split, so every one of these has to be left alone.
func TestSameNameMergeStaysNarrow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Name    string
		Records []connector.Record
	}{{ // Test 0: A single-word name is a first name or a handle, far too common.
		Name: "single word name",
		Records: []connector.Record{
			person("Michael", "michael@a.com", "billing", "ledger", "invoices"),
			person("Michael", "michael@b.com", "billing", "ledger", "invoices"),
		},
	}, { // Test 1: A name held by three records is a common name, not a split one.
		Name: "three of the same name",
		Records: []connector.Record{
			person("John Smith", "js1@a.com", "billing", "ledger", "invoices"),
			person("John Smith", "js2@a.com", "billing", "ledger", "invoices"),
			person("John Smith", "js3@a.com", "billing", "ledger", "invoices"),
		},
	}, { // Test 2: A shared name with no shared work is not evidence of anything.
		Name: "no shared subjects",
		Records: []connector.Record{
			person("Jane Doe", "jane@a.com", "billing", "ledger"),
			person("Jane Doe", "jane@b.com", "kafka", "search"),
		},
	}, { // Test 3: Agreement on too few subjects is not enough.
		Name: "too few shared subjects",
		Records: []connector.Record{
			person("Ada Byron", "ada@a.com", "billing", "kafka"),
			person("Ada Byron", "ada@b.com", "billing", "search"),
		},
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d %s", testNum, test.Name), func(t *testing.T) {
			t.Parallel()
			ix := New()
			ix.Build(test.Records)
			ix.AutoJoin()
			ix.Canonicalize()
			if got := len(ix.Graph.People); got != len(test.Records) {
				t.Errorf("graph holds %d people, want all %d left apart: %s",
					got, len(test.Records), test.Name)
			}
		})
	}
}
