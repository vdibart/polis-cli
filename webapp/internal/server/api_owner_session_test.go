package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
)

// ownerReq sends a /v1 request as a browser on the site's own page would,
// with the given extra headers.
func ownerReq(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Code
}

// ⭐ THE OWNER'S INTERFACE REACHES /v1 ON THE OWNER'S OWN SIGN-IN (v5 spec 1.2),
// with no token: on polis.pub the front door's owner-session mark, the
// owner-only operations included (saving a draft, reading messages).
// ⛔ A request from another site's page never is the owner, whatever it
// carries, reads included; and without the mark, nothing.
func TestTheOwnersSignInReachesTheAPIOnPolisPub(t *testing.T) {
	s, h := queryRoutes(t)
	s.Mode = capability.ForHosted()
	mark := map[string]string{OwnerSessionHeader: "1", "Sec-Fetch-Site": "same-origin"}
	const read = "/v1/query?pql=all+drafts+from+me"
	const save = "/v1/content/post/actions/save_draft"
	const messages = "/v1/query?pql=all+messages+from+all+polis"

	if code := ownerReq(t, h, "GET", read, "", mark); code != http.StatusOK {
		t.Errorf("owner reads drafts = %d, want 200", code)
	}
	if code := ownerReq(t, h, "POST", save, `{"markdown":"from the owner's app"}`, mark); code != http.StatusOK {
		t.Errorf("owner saves a draft (owner-only) = %d, want 200", code)
	}
	if code := ownerReq(t, h, "GET", messages, "", mark); code == http.StatusUnauthorized || code == http.StatusForbidden {
		t.Errorf("owner reads messages (the full scope) = %d, want it allowed", code)
	}
	if code := ownerReq(t, h, "POST", "/v1/seen", `{"section":"write","up_to":"2026-11-17T00:00:00Z"}`, mark); code != http.StatusOK {
		t.Errorf("owner marks a section seen = %d, want 200", code)
	}

	// Without the mark: nobody.
	if code := ownerReq(t, h, "GET", read, "", map[string]string{"Sec-Fetch-Site": "same-origin"}); code != http.StatusUnauthorized {
		t.Errorf("no mark = %d, want 401", code)
	}
	// ⛔ Another site's page, with the owner's sign-in riding along: refused,
	// reads and writes, by either header.
	for name, hdr := range map[string]map[string]string{
		"another tenant's origin": {OwnerSessionHeader: "1", "Origin": "https://evil.polis.pub"},
		"same-site fetch":         {OwnerSessionHeader: "1", "Sec-Fetch-Site": "same-site"},
		"cross-site fetch":        {OwnerSessionHeader: "1", "Sec-Fetch-Site": "cross-site"},
		"a null origin":           {OwnerSessionHeader: "1", "Origin": "null"},
	} {
		if code := ownerReq(t, h, "GET", read, "", hdr); code != http.StatusUnauthorized {
			t.Errorf("%s reads drafts = %d, want 401", name, code)
		}
		if code := ownerReq(t, h, "POST", save, `{"markdown":"forged"}`, hdr); code != http.StatusUnauthorized {
			t.Errorf("%s saves a draft = %d, want 401", name, code)
		}
	}
	// ⭐ A token is judged alone, even with the owner signed in: an agent in the
	// owner's browser is still the agent.
	withToken := map[string]string{OwnerSessionHeader: "1", "Sec-Fetch-Site": "same-origin", "Authorization": "Bearer " + mint(t, s, "read")}
	if code := ownerReq(t, h, "POST", save, `{"markdown":"agent"}`, withToken); code != http.StatusForbidden {
		t.Errorf("a read token with the owner signed in saves a draft = %d, want 403", code)
	}
	if n := len(draftsOf(t, s)); n != 1 {
		t.Errorf("drafts = %d, want only the owner's one", n)
	}
}

// ⭐ On the owner's laptop, loopback is the owner's sign-in, as it is for the
// app's own routes; another local page is refused.
func TestTheOwnersSignInReachesTheAPIOnALaptop(t *testing.T) {
	s, h := queryRoutes(t)
	s.BoundToLoopback = true
	const read = "/v1/query?pql=all+drafts+from+me"
	if code := ownerReq(t, h, "GET", read, "", map[string]string{"Sec-Fetch-Site": "same-origin"}); code != http.StatusOK {
		t.Errorf("owner on loopback = %d, want 200", code)
	}
	if code := ownerReq(t, h, "POST", "/v1/content/post/actions/save_draft", `{"markdown":"x"}`, map[string]string{"Origin": "http://localhost:9999"}); code != http.StatusUnauthorized {
		t.Errorf("another local page saves a draft = %d, want 401", code)
	}
}
