package graph

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeDoer serves canned bodies in order, one per call.
type fakeDoer struct {
	bodies []string
	calls  int
}

func (f *fakeDoer) Do(*http.Request) (*http.Response, error) {
	body := f.bodies[f.calls]
	f.calls++
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}, nil
}

// TestUsersPaginatesAndMapsManager confirms Users follows the nextLink and reads
// the expanded manager email, with the sign-in name as an email fallback.
func TestUsersPaginatesAndMapsManager(t *testing.T) {
	t.Parallel()
	page1 := `{"value":[
	  {"id":"1","displayName":"Alice","mail":"alice@x.com","jobTitle":"Eng","department":"Payments","manager":{"mail":"boss@x.com"}}
	],"@odata.nextLink":"https://graph.microsoft.com/v1.0/users?page=2"}`
	page2 := `{"value":[{"id":"2","displayName":"Bob","userPrincipalName":"bob@x.com"}]}`
	c := New("tok", WithHTTPClient(&fakeDoer{bodies: []string{page1, page2}}))

	users, err := c.Users(context.Background())
	if err != nil {
		t.Fatalf("Users: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %d, want 2", len(users))
	}
	if got := users[0].Email(); got != "alice@x.com" {
		t.Errorf("user0 email = %q, want alice@x.com", got)
	}
	if got := users[0].ManagerEmail(); got != "boss@x.com" {
		t.Errorf("user0 manager = %q, want boss@x.com", got)
	}
	if got := users[1].Email(); got != "bob@x.com" {
		t.Errorf("user1 email = %q, want bob@x.com (UPN fallback)", got)
	}
	if got := users[1].ManagerEmail(); got != "" {
		t.Errorf("user1 manager = %q, want empty", got)
	}
}
