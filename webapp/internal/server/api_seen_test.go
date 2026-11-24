package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func seenOf(t *testing.T, h http.Handler, token string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/seen", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /v1/seen = %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Seen map[string]any `json:"seen"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Seen
}

// ⭐ One write marks a section seen up to a time, under the notify scope; it
// never moves back; reading content never clears anything by itself.
func TestSeenIsMarkedOnlyByTheSeenWrite(t *testing.T) {
	s, h := queryRoutes(t)
	notify := mint(t, s, "notify")
	publish := mint(t, s, "publish")
	read := mint(t, s, "read")

	if got := seenOf(t, h, notify); len(got) != 0 {
		t.Fatalf("new site: %v", got)
	}
	// An agent reading the owner's content clears nothing.
	_ = send(t, h, "GET", "/v1/query?pql=all+posts+from+my+network", read, "")
	if got := seenOf(t, h, notify); len(got) != 0 {
		t.Errorf("a read changed the seen times: %v", got)
	}

	mark := `{"section":"gather","up_to":"2026-11-13T10:00:00Z"}`
	if code := send(t, h, "POST", "/v1/seen", notify, mark); code != http.StatusOK {
		t.Fatalf("mark with notify = %d", code)
	}
	if got := seenOf(t, h, notify); got["gather"] != "2026-11-13T10:00:00Z" {
		t.Errorf("gather = %v", got["gather"])
	}
	older := `{"section":"gather","up_to":"2026-11-13T09:00:00Z"}`
	_ = send(t, h, "POST", "/v1/seen", notify, older)
	if got := seenOf(t, h, notify); got["gather"] != "2026-11-13T10:00:00Z" {
		t.Errorf("moved backwards to %v", got["gather"])
	}

	for _, tc := range []struct {
		name, token, body string
		want              int
	}{
		{"no token", "", mark, http.StatusUnauthorized},
		{"publish may not mark", publish, mark, http.StatusForbidden},
		{"read may not mark", read, mark, http.StatusForbidden},
		{"unknown section", notify, `{"section":"settings","up_to":"2026-11-13T10:00:00Z"}`, http.StatusBadRequest},
		{"not a time", notify, `{"section":"write","up_to":"yesterday"}`, http.StatusBadRequest},
	} {
		if got := send(t, h, "POST", "/v1/seen", tc.token, tc.body); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
	if !strings.Contains(fmtDescription(t, h), `"seen.mark"`) {
		t.Error("the description does not list seen.mark")
	}
}

func fmtDescription(t *testing.T, h http.Handler) string {
	t.Helper()
	w := get(t, h, "/v1/openapi.json", "")
	return w.Body.String()
}

// ⭐ new_for marks each item new against the section's seen time, only_new
// keeps the new ones, and neither clears anything. It needs read.
func TestQueryMarksItemsNewAgainstTheSeenTime(t *testing.T) {
	s, h := queryRoutes(t)
	full := mint(t, s)
	notify := mint(t, s, "notify")
	publishThroughAPI(t, h, full, 2)

	items := func(path string) []map[string]any {
		t.Helper()
		w := get(t, h, path, full)
		if w.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, w.Code, w.Body.String())
		}
		var env struct {
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return env.Items
	}
	const q = "/v1/query?pql=all+posts+from+me&new_for=write"

	for _, it := range items(q) {
		if it["new"] != true {
			t.Errorf("never-marked section: item new = %v, want true", it["new"])
		}
	}
	// Seen up to now: nothing published before it is new.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if code := send(t, h, "POST", "/v1/seen", notify, `{"section":"write","up_to":"`+now+`"}`); code != http.StatusOK {
		t.Fatalf("mark = %d", code)
	}
	got := items(q)
	if len(got) != 2 {
		t.Fatalf("items = %d, want 2", len(got))
	}
	for _, it := range got {
		if it["new"] != false {
			t.Errorf("after marking: item new = %v, want false", it["new"])
		}
	}
	if n := len(items(q + "&only_new=1")); n != 0 {
		t.Errorf("only_new kept %d, want 0", n)
	}
	// The reads changed nothing.
	if seenOf(t, h, notify)["write"] == nil {
		t.Error("seen time lost")
	}
	if code := send(t, h, "GET", "/v1/query?pql=all+posts+by+date&new_for=gather", "", ""); code != http.StatusUnauthorized {
		t.Errorf("new_for without a token = %d, want 401", code)
	}
	if code := send(t, h, "GET", "/v1/query?pql=all+posts+from+me&new_for=settings", full, ""); code != http.StatusBadRequest {
		t.Errorf("unknown section = %d, want 400", code)
	}
}
