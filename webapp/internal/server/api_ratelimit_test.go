package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ⭐ A runaway agent is stopped by its token's write budget; another
// connection keeps its own, and the owner's full key is not limited.
func TestAPIWritesHaveAPerTokenBudget(t *testing.T) {
	s, h := queryRoutes(t)
	agent, _ := mintVia(t, s, "looping agent", "notify")
	other, _ := mintVia(t, s, "other agent", "notify")
	owner, _ := mintVia(t, s, "owner", "full")

	post := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/seen", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	const budget = 200 // api.tokenWritesPerHour
	for i := 0; i < budget; i++ {
		if w := post(agent); w.Code == http.StatusTooManyRequests {
			t.Fatalf("limited after %d writes", i)
		}
	}
	w := post(agent)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Body.String(), "rate_limited") {
		t.Fatalf("write past the budget = %d %q %s", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
	if code := send(t, h, "GET", "/v1/seen", agent, ""); code == http.StatusTooManyRequests {
		t.Error("a read was refused by the write budget")
	}
	if w := post(other); w.Code == http.StatusTooManyRequests {
		t.Error("another connection shared the budget")
	}
	for i := 0; i <= budget; i++ {
		if w := post(owner); w.Code == http.StatusTooManyRequests {
			t.Fatalf("the owner's full key was limited after %d writes", i)
		}
	}
}
