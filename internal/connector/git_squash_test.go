package connector

import (
	"fmt"
	"testing"
	"time"
)

// TestUnshareSquashMailboxes pins the shared-mailbox repair measured on
// esphome: one noreply address authored commits under three names, and each
// commit moves to its author name's own mailbox, while everything without a
// safe destination stays put.
func TestUnshareSquashMailboxes(t *testing.T) {
	t.Parallel()
	shared := "154711427+swoboda1337@users.noreply.github.com"
	job := func(name, email string) commitJob {
		return commitJob{Email: email, Name: name, When: time.Now()}
	}
	tests := []struct {
		Name      string
		In        []commitJob
		WantEmail []string
	}{{
		// Test 0: Commits under the shared mailbox move to each author's own
		// address; the owner's own commits stay.
		Name: "tangle repaired",
		In: []commitJob{
			job("Jonathan Swoboda", shared),
			job("J. Nick Koston", shared),
			job("Kevin Ahrendt", shared),
			job("J. Nick Koston", "nick@koston.org"),
			job("Kevin Ahrendt", "kevin@openhome.org"),
		},
		WantEmail: []string{shared, "nick@koston.org", "kevin@openhome.org", "nick@koston.org", "kevin@openhome.org"},
	}, {
		// Test 1: A noreply mailbox with one name is one person, untouched.
		Name: "own noreply kept",
		In: []commitJob{
			job("Jesse Hills", "3060199+jesserockz@users.noreply.github.com"),
			job("Jesse Hills", "3060199+jesserockz@users.noreply.github.com"),
		},
		WantEmail: []string{
			"3060199+jesserockz@users.noreply.github.com",
			"3060199+jesserockz@users.noreply.github.com",
		},
	}, {
		// Test 2: A person with two own mailboxes gets their most active one.
		Name: "primary wins",
		In: []commitJob{
			job("J. Nick Koston", shared),
			job("Someone Else", shared),
			job("J. Nick Koston", "nick@koston.org"),
			job("J. Nick Koston", "nick@home-assistant.io"),
			job("J. Nick Koston", "nick@home-assistant.io"),
		},
		WantEmail: []string{"nick@home-assistant.io", shared,
			"nick@koston.org", "nick@home-assistant.io", "nick@home-assistant.io"},
	}, {
		// Test 3: A single-word name never moves, and a name with no unique
		// mailbox in the window stays where it was.
		Name: "no safe destination",
		In: []commitJob{
			job("Nick", shared),
			job("Grace Kim", shared),
			job("Someone Else", shared),
		},
		WantEmail: []string{shared, shared, shared},
	}, {
		// Test 4: A real shared corporate mailbox is not a noreply, untouched
		// even with two names on it.
		Name: "real mailbox untouched",
		In: []commitJob{
			job("Ana Ruiz", "team@corp.com"),
			job("Bo Chen", "team@corp.com"),
		},
		WantEmail: []string{"team@corp.com", "team@corp.com"},
	}}
	for testNum, test := range tests {
		t.Run(fmt.Sprintf("test %d %s", testNum, test.Name), func(t *testing.T) {
			t.Parallel()
			got := unshareSquashMailboxes(append([]commitJob(nil), test.In...))
			for i, want := range test.WantEmail {
				if got[i].Email != want {
					t.Errorf("commit %d (%s): email = %q, want %q", i, test.In[i].Name, got[i].Email, want)
				}
			}
		})
	}
}
