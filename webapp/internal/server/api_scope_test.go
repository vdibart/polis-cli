package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// mint makes the token an agent would hold: for scopes, a personal token
// presenting a signed grant for them (as POST /api/keys makes one); for none,
// the owner's own full key.
func mint(t *testing.T, s *Server, scopes ...string) string {
	t.Helper()
	if len(scopes) == 0 {
		token, _, err := ops.MintAPIKey(s.Site(), "owner", nil)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	name := "test token"
	issued, err := connection.Issue(s.connectionConfig(), name, name, scopes)
	if err != nil {
		t.Fatalf("issue a grant for %v: %v", scopes, err)
	}
	token, _, err := ops.MintPersonalToken(s.Site(), name, scopes, issued.URL, issued.ID)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func send(t *testing.T, h http.Handler, method, path, token, body string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

// ⭐ A token acts only within its scopes: each operation needs the scope its
// declaration names, the owner's own full token may do everything, and
// messages need that token whatever scopes another carries.
func TestTokensActOnlyWithinTheirScopes(t *testing.T) {
	s, h := queryRoutes(t)
	full := mint(t, s)
	publish := mint(t, s, "publish")
	read := mint(t, s, "read")
	appearance := mint(t, s, "appearance")
	// ⭐ Most scopes an agent can hold (tag is not grantable; follow is, and has its own tests).
	everything := mint(t, s, "read", "publish", "comment", "bless", "appearance", "profile", "notify", "status")
	post := `{"markdown":"# Scoped\n\nbody"}`

	for _, tc := range []struct {
		name, method, path, token, body string
		want                            int
	}{
		{"publish may publish", "POST", "/v1/content/post", publish, post, http.StatusCreated},
		{"read may not publish", "POST", "/v1/content/post", read, post, http.StatusForbidden},
		{"appearance may not publish", "POST", "/v1/content/post", appearance, post, http.StatusForbidden},
		{"full may publish", "POST", "/v1/content/post", full, post, http.StatusCreated},
		{"read may list follows", "GET", "/v1/content/follow", read, "", http.StatusOK},
		{"publish may not list follows", "GET", "/v1/content/follow", publish, "", http.StatusForbidden},
		{"no scope reaches messages", "GET", "/v1/content/dm", everything, "", http.StatusForbidden},
		{"full reaches messages", "GET", "/v1/content/dm", full, "", http.StatusOK},
		{"public needs nothing", "GET", "/v1/content/post", "", "", http.StatusOK},
		{"read may read the owner's posts", "GET", "/v1/query?pql=all+posts+from+me", read, "", http.StatusOK},
		{"read may not read messages", "GET", "/v1/query?pql=all+messages+from+my+mutuals", read, "", http.StatusForbidden},
		{"full may read messages", "GET", "/v1/query?pql=all+messages+from+my+mutuals", full, "", http.StatusOK},
		{"read may not count messages", "GET", "/v1/query?pql=all+posts+from+me&counts=posts,messages", read, "", http.StatusForbidden},
		{"read may count drafts", "GET", "/v1/query?pql=all+posts+from+me&counts=posts,drafts", read, "", http.StatusOK},
		{"full may count messages", "GET", "/v1/query?pql=all+messages+from+my+mutuals&counts=messages", full, "", http.StatusOK},
	} {
		if got := send(t, h, tc.method, tc.path, tc.token, tc.body); got != tc.want {
			t.Errorf("%s: %s %s = %d, want %d", tc.name, tc.method, tc.path, got, tc.want)
		}
	}
}
