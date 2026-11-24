package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

func publishThroughAPI(t *testing.T, h http.Handler, token string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		body := fmt.Sprintf(`{"markdown":"# Counted %d\n\nbody %d"}`, i, i)
		req := httptest.NewRequest(http.MethodPost, "/v1/content/post", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("publish %d: %d %s", i, w.Code, w.Body.String())
		}
	}
}

func envelopeOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var env map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return env
}

// ⭐ counts=<types> adds how many items the same sentence finds for each
// type: the numbers a section's filters show. A count equals what reading
// every page of that sentence would return.
func TestPQLCountsCountWhatTheSentenceFinds(t *testing.T) {
	s, h := queryRoutes(t)
	full, _, err := ops.MintAPIKey(s.Site(), "owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	publishThroughAPI(t, h, full, 3)

	env := envelopeOf(t, get(t, h, "/v1/query?pql=all+posts+by+date&counts=posts,comments", ""))
	counts, _ := env["counts"].(map[string]any)
	items, _ := env["items"].([]any)
	if counts == nil || counts["posts"] != float64(3) || counts["comments"] != float64(0) {
		t.Fatalf("counts = %v, want posts 3 and comments 0", env["counts"])
	}
	if len(items) != 3 {
		t.Errorf("items = %d, want the 3 posts the count names", len(items))
	}
	// /pql/ carries the same block.
	env = envelopeOf(t, get(t, h, "/pql/all+posts+by+date?counts=posts", ""))
	if c, _ := env["counts"].(map[string]any); c["posts"] != float64(3) {
		t.Errorf("/pql/ counts = %v", env["counts"])
	}
	// Without counts= there is no block: it costs reads, so it is asked for.
	env = envelopeOf(t, get(t, h, "/v1/query?pql=all+posts+by+date", ""))
	if _, ok := env["counts"]; ok {
		t.Error("counts present without counts=")
	}
}

// ⛔ A count never widens what the request may see: a public sentence cannot
// count the owner's messages or drafts, and a word that is not a type is
// refused. The owner's own sentence may.
func TestPQLCountsNeverWidenWhatTheRequestMaySee(t *testing.T) {
	s, h := queryRoutes(t)
	for _, path := range []string{
		"/v1/query?pql=all+posts+by+date&counts=messages",
		"/v1/query?pql=all+posts+by+date&counts=drafts",
		"/pql/all+posts+by+date?counts=messages",
		"/v1/query?pql=all+posts+by+date&counts=secrets",
	} {
		if w := get(t, h, path, ""); w.Code != http.StatusBadRequest {
			t.Errorf("%s = %d %s, want 400", path, w.Code, w.Body.String())
		}
	}
	full, _, err := ops.MintAPIKey(s.Site(), "owner", nil)
	if err != nil {
		t.Fatal(err)
	}
	env := envelopeOf(t, get(t, h, "/v1/query?pql=all+posts+from+me&counts=posts,drafts", full))
	if c, _ := env["counts"].(map[string]any); c == nil || c["drafts"] == nil {
		t.Errorf("owner sentence counts = %v, want drafts counted", env["counts"])
	}
}
