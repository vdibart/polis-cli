package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// ⭐ Spec 3.9: every v5 place answers the shell, and every legacy address
// goes to its v5 place, so every address keeps working (D3).
func TestV5OwnerAddressesAndTheLegacyRedirects(t *testing.T) {
	for _, c := range []struct{ path, query, want string }{
		// The places: the shell answers them ("").
		{"/_/", "", ""},
		{"/_", "", ""},
		{"/_/write", "", ""},
		{"/_/gather", "", ""},
		{"/_/message", "", ""},
		{"/_/enhance", "", ""},
		{"/_/enhance/you", "", ""},
		{"/_/enhance/data", "", ""},
		{"/_/gather/pql/all+posts+from+my+network+by+date", "", ""},
		{"/_/write/pql/all+comments+from+all+polis+to+bless", "", ""},
		{"/_/write/pql/all+comments+on+https:%2F%2Fa.example%2Fposts%2F20261110%2Fx.md+to+bless", "", ""},
		{"/_/message/pql/all+messages+from+alice.polis.pub", "", ""},
		{"/_/write/pql/all+drafts+from+me", "", ""},
		{"/_/v5/frame.js", "v=1", ""},
		{"/_/kay", "", ""},
		// A sentence under the wrong section goes to the one that holds it.
		{"/_/gather/pql/all+posts+from+me", "", "/_/write/pql/all+posts+from+me"},
		{"/_/write/pql/all+activity+about+me", "", "/_/gather/pql/all+activity+about+me"},
		{"/_/gather/pql/all+messages+from+all+polis", "", "/_/message/pql/all+messages+from+all+polis"},
		// The legacy table.
		{"/_/pql/all+posts+from+my+network", "", "/_/gather/pql/all+posts+from+my+network"},
		{"/_/pql/all+comments+from+me", "", "/_/write/pql/all+comments+from+me"},
		{"/_/pql/all+comments+from+all+polis+to+bless", "", "/_/write/pql/all+comments+from+all+polis+to+bless"},
		{"/_/pql/all+activity+about+me", "", "/_/gather/pql/all+activity+about+me"},
		{"/_/pql/all+messages+from+alice.polis.pub", "", "/_/message/pql/all+messages+from+alice.polis.pub"},
		{"/_/settings", "", "/_/enhance"},
		{"/_/messages", "", "/_/message"},
		{"/_/messages/abc123", "", "/_/message"},
		{"/_/posts", "", "/_/write"},
		{"/_/posts/drafts/draft-1", "", "/_/write"},
		{"/_/feed", "", "/_/gather"},
		{"/_/following", "intent=follow&target=bob.polis.pub&t=abc", "/_/gather?intent=follow&target=bob.polis.pub&t=abc"},
		{"/_/posts/2026/03/hello.html", "", "/posts/2026/03/hello.html"},
		{"/_/comments/2026/03/hi.html", "", "/comments/2026/03/hi.html"},
		{"/_/social/followers", "", "/_/gather/pql/all+activity+about+me"},
		{"/_/comments", "filter=blessed", "/_/write/pql/all+comments+from+me"},
		// Retired v3 addresses, and anything else: the signed-in home.
		{"/_/blessings", "", "/_/"},
		{"/_/nothing-here", "", "/_/"},
		{"/_/enhance/nope", "", "/_/"},
		{"/_/write/extra", "", "/_/"},
	} {
		if got := V5OwnerPlace(c.path, c.query); got != c.want {
			t.Errorf("V5OwnerPlace(%q, %q) = %q, want %q", c.path, c.query, got, c.want)
		}
	}
}

func v5WebFS(t *testing.T) fs.FS {
	t.Helper()
	fsys, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		t.Fatal(err)
	}
	return fsys
}

// ⭐ A site switched to v5 gets the v5 owner app at /_/, its files and its
// addresses, with nothing to switch on; switched back to v4, the v4 app.
func TestEverySiteGetsTheV5OwnerApp(t *testing.T) {
	s := newConfiguredServer(t)
	fsys := v5WebFS(t)
	h := spaHandler(fsys, s.Site())
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	if err := bundle.SetActiveShapeName(s.Site(), V5ShapeName); err != nil {
		t.Fatal(err)
	}
	w := get("/_/gather")
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `class="ways"`) {
		t.Fatalf("/_/gather on v5 = %d, not the v5 shell:\n%.300s", w.Code, body)
	}
	if strings.Contains(body, "{{") {
		t.Errorf("the shell has an unfilled placeholder:\n%s", body)
	}
	if !strings.Contains(body, `href="/_/v5/frame.css?v=`) || !strings.Contains(body, `src="/_/v5/frame.js?v=`) {
		t.Errorf("the shell's files are not addressed with their versions:\n%s", body)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src") {
		t.Errorf("the shell has no CSP: %q", csp)
	}
	if w := get("/_/settings"); w.Code != http.StatusFound || w.Header().Get("Location") != "/_/enhance" {
		t.Errorf("/_/settings on v5 = %d → %q, want 302 → /_/enhance", w.Code, w.Header().Get("Location"))
	}
	if w := get("/_/v5/frame.js?v=1"); w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/javascript") {
		t.Errorf("/_/v5/frame.js = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w := get("/_/v5/index.html"); w.Code != http.StatusNotFound {
		t.Errorf("the shell's source as a file = %d, want 404", w.Code)
	}
	if w := get("/_/v5/../index.html"); w.Code == http.StatusOK && strings.Contains(w.Body.String(), "stream.js") {
		t.Error("a path out of v5/ reached today's app's files")
	}

	// The v5 app is every site's, whatever its shape: a site naming the
	// retired blog shape gets it too.
	if err := bundle.SetActiveShapeName(s.Site(), "pub.polis.shapes.v3"); err != nil {
		t.Fatal(err)
	}
	if w := get("/_/gather"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `class="ways"`) {
		t.Fatalf("/_/gather on a v3 site = %d, want the v5 app", w.Code)
	}
}
