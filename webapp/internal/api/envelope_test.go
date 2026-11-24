package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeEnvelope(t *testing.T, b []byte) (code, message string, inner map[string]any) {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("not JSON: %q", b)
	}
	inner, _ = env["error"].(map[string]any)
	if env["status"] != "error" || inner == nil {
		t.Fatalf("not the envelope: %s", b)
	}
	code, _ = inner["code"].(string)
	message, _ = inner["message"].(string)
	return code, message, inner
}

func TestEveryErrorShapeBecomesTheEnvelope(t *testing.T) {
	// Plain text from a forwarded browser-app handler.
	code, msg, _ := decodeEnvelope(t, envelopeFor(http.StatusBadRequest, []byte("Invalid JSON\n")))
	if code != "invalid_request" || msg != "Invalid JSON" {
		t.Errorf("plain text: %s %q", code, msg)
	}
	// A host route's {"error": msg}, with what else it said kept inside error.
	code, msg, inner := decodeEnvelope(t, envelopeFor(http.StatusBadRequest, []byte(`{"error":"refused","reasons":["a","b"]}`)))
	if code != "invalid_request" || msg != "refused" || len(inner["reasons"].([]any)) != 2 {
		t.Errorf("{error}: %s %q %v", code, msg, inner)
	}
	// Already the envelope: unchanged, its own code kept.
	orig := `{"status":"error","error":{"code":"insufficient_scope","message":"no"}}`
	if got := string(envelopeFor(http.StatusForbidden, []byte(orig))); got != orig {
		t.Errorf("an envelope was rewritten: %s", got)
	}
	// No body at all: the status's text.
	code, msg, _ = decodeEnvelope(t, envelopeFor(http.StatusTooManyRequests, nil))
	if code != "rate_limited" || msg != "Too Many Requests" {
		t.Errorf("empty: %s %q", code, msg)
	}
	for status, want := range map[int]string{401: "unauthorized", 404: "not_found", 409: "conflict", 413: "too_large", 500: "internal_error", 502: "internal_error", 503: "unavailable"} {
		if got := codeForStatus(status); got != want {
			t.Errorf("codeForStatus(%d) = %s, want %s", status, got, want)
		}
	}
}

func TestTheEnvelopeKeepsHeadersAndPassesSuccessThrough(t *testing.T) {
	h := enveloped(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ok" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"fine":true}`))
			return
		}
		w.Header().Set("Retry-After", "30")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if w.Code != http.StatusOK || w.Body.String() != `{"fine":true}` {
		t.Errorf("success = %d %s, want it untouched", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/slow", nil))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "30" || w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("error = %d, headers %v", w.Code, w.Header())
	}
	if code, msg, _ := decodeEnvelope(t, w.Body.Bytes()); code != "rate_limited" || msg != "slow down" {
		t.Errorf("error envelope = %s %q", code, msg)
	}
}
