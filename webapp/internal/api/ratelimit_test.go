package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWindowCounter(t *testing.T) {
	c := newWindowCounter(3, time.Hour)
	now := time.Now()
	for i := 0; i < 3; i++ {
		if ok, _, _ := c.take("a", now); !ok {
			t.Fatalf("call %d refused", i+1)
		}
	}
	ok, retry, first := c.take("a", now.Add(time.Minute))
	if ok || !first || retry < 3500 || retry > 3540 {
		t.Fatalf("fourth = %v retry %d first %v", ok, retry, first)
	}
	if _, _, first := c.take("a", now.Add(2*time.Minute)); first {
		t.Error("a second refusal in the window was reported as the first")
	}
	if ok, _, _ := c.take("b", now); !ok {
		t.Error("another key shared the budget")
	}
	if ok, _, _ := c.take("a", now.Add(time.Hour)); !ok {
		t.Error("the window did not reset")
	}
}

// ⭐ Requests without a token are limited per address, the refusal is
// logged once a window, and token-bearing and site-signed requests are not
// counted.
func TestEdgeLimitsTokenlessCallsPerAddress(t *testing.T) {
	saved := addressCalls
	addressCalls = newWindowCounter(2, time.Hour)
	t.Cleanup(func() { addressCalls = saved })

	var events []map[string]interface{}
	h := &handlers{securityLogger: func(e string, f map[string]interface{}) {
		if e == RateLimitedEvent {
			events = append(events, f)
		}
	}}
	ok := h.edge(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	call := func(addr string, hdr map[string]string) int {
		req := httptest.NewRequest(http.MethodGet, "/v1/site", nil)
		req.RemoteAddr = addr + ":5555"
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		ok(w, req)
		if w.Code == http.StatusTooManyRequests && w.Header().Get("Retry-After") == "" {
			t.Error("429 without Retry-After")
		}
		return w.Code
	}
	call("198.51.100.1", nil)
	call("198.51.100.1", nil)
	if c := call("198.51.100.1", nil); c != http.StatusTooManyRequests {
		t.Fatalf("third tokenless call = %d", c)
	}
	call("198.51.100.1", nil)
	if len(events) != 1 || events[0]["limit"] != "address" {
		t.Errorf("events = %v", events)
	}
	if c := call("198.51.100.1", map[string]string{"Authorization": "Bearer x"}); c != http.StatusOK {
		t.Errorf("a token-bearing call was limited by address: %d", c)
	}
	if c := call("198.51.100.1", map[string]string{"X-Polis-Signature": "sig"}); c != http.StatusOK {
		t.Errorf("a site-signed call was limited by address: %d", c)
	}
	if c := call("198.51.100.2", nil); c != http.StatusOK {
		t.Errorf("another address shared the budget: %d", c)
	}

	// Behind a proxy the host names the client.
	h.host = &Host{ClientAddr: func(*http.Request) string { return "203.0.113.9" }}
	call("198.51.100.3", nil)
	call("198.51.100.4", nil)
	if c := call("198.51.100.5", nil); c != http.StatusTooManyRequests {
		t.Errorf("the host's client address was not the key: %d", c)
	}
}

// Deletes have a smaller budget of their own; reads are never counted.
func TestTokenBudgetDeletesAndReads(t *testing.T) {
	savedW, savedD := tokenWrites, tokenDeletes
	tokenWrites, tokenDeletes = newWindowCounter(5, time.Hour), newWindowCounter(1, time.Hour)
	t.Cleanup(func() { tokenWrites, tokenDeletes = savedW, savedD })
	var events []map[string]interface{}
	h := &handlers{securityLogger: func(e string, f map[string]interface{}) { events = append(events, f) }}
	try := func(method string) bool {
		return h.tokenBudget(httptest.NewRecorder(), httptest.NewRequest(method, "/v1/content/post/x", nil), "site:a", "k1")
	}
	for i := 0; i < 10; i++ {
		if !try(http.MethodGet) {
			t.Fatal("a read was counted")
		}
	}
	if !try(http.MethodDelete) || try(http.MethodDelete) {
		t.Fatal("the delete budget of 1 is not enforced")
	}
	if !try(http.MethodPost) {
		t.Error("a refused delete used the write budget up")
	}
	if !h.tokenBudget(httptest.NewRecorder(), httptest.NewRequest(http.MethodDelete, "/x", nil), "site:b", "k1") {
		t.Error("the same key id on another site shared the budget")
	}
	if len(events) != 1 || events[0]["limit"] != "token_deletes" || events[0]["key_id"] != "k1" {
		t.Errorf("events = %v", events)
	}
}
