package ops

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
)

// minimalEnv builds a HandlerEnv with just a Resolver pointing at a
// temp dir — enough to keep ExecutableHandler from panicking on
// env.Resolver.SiteDir().
func minimalEnv(t *testing.T) HandlerEnv {
	t.Helper()
	r := resolve.New(t.TempDir(), map[string]*bundle.Bundle{})
	return HandlerEnv{Resolver: r}
}

// Failure-injection tests for the non-builtin Handler types. The Engine
// tests cover the happy path through BuiltinCoreHandler; these cover
// the other two paths (ExecutableHandler, HTTPHandler) which previously
// had no isolated coverage of their failure surfaces.

// ============================================================================
// ExecutableHandler
// ============================================================================

func TestExecutableHandler_NonexistentBinary(t *testing.T) {
	h := NewExecutableHandler("/this/path/definitely/does/not/exist")
	_, err := h.Handle(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "pub.example.thing",
	}, minimalEnv(t))
	if err == nil {
		t.Fatal("expected error for missing binary, got nil")
	}
}

func TestExecutableHandler_BinaryExitsNonZero(t *testing.T) {
	// /bin/false exits with code 1 and no output — surfaces as an
	// ExitError that the handler should wrap with the exit code.
	h := NewExecutableHandler("/bin/false")
	_, err := h.Handle(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "pub.example.thing",
	}, minimalEnv(t))
	if err == nil {
		t.Fatal("expected error from non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), "exited with code") && !strings.Contains(err.Error(), "execution failed") {
		t.Errorf("expected exit-code error message, got: %v", err)
	}
}

func TestExecutableHandler_BinaryOutputsNonJSON(t *testing.T) {
	// /bin/echo emits a string with no JSON structure — unmarshal must
	// surface a parse error rather than returning a zero-value result.
	h := NewExecutableHandler("/bin/echo")
	_, err := h.Handle(context.Background(), ActionRequest{
		Action:      "list",
		ContentType: "pub.example.thing",
	}, minimalEnv(t))
	if err == nil {
		t.Fatal("expected unmarshal error from non-JSON output")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected unmarshal error, got: %v", err)
	}
}

func TestExecutableHandler_ContextCancellation(t *testing.T) {
	// /bin/sleep blocks; ctx cancellation should propagate and abort.
	h := NewExecutableHandler("/bin/sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := h.Handle(ctx, ActionRequest{
		Action:      "noop",
		ContentType: "pub.example.thing",
		Payload:     map[string]any{"_arg": "10"}, // not used by /bin/sleep; binary will be cancelled
	}, minimalEnv(t))
	if err == nil {
		t.Fatal("expected error on context cancellation")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("handler did not abort on context cancel — took %v", time.Since(start))
	}
}

// ============================================================================
// HTTPHandler
// ============================================================================

func TestHTTPHandler_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo back a well-formed ActionResult
		json.NewEncoder(w).Encode(ActionResult{
			Status: "success",
			Data:   map[string]any{"ok": true},
		})
	}))
	defer server.Close()

	h := NewHTTPHandler(server.URL)
	got, err := h.Handle(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.example.thing",
	}, HandlerEnv{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Status != "success" {
		t.Errorf("expected status=success, got %q", got.Status)
	}
}

func TestHTTPHandler_Unreachable(t *testing.T) {
	h := NewHTTPHandler("http://127.0.0.1:1") // port 1, nothing listens
	_, err := h.Handle(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.example.thing",
	}, HandlerEnv{})
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
}

func TestHTTPHandler_Returns4xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid payload"}`))
	}))
	defer server.Close()

	h := NewHTTPHandler(server.URL)
	_, err := h.Handle(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.example.thing",
	}, HandlerEnv{})
	if err == nil {
		t.Fatal("expected error for 4xx response")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "invalid payload") {
		t.Errorf("expected error containing status+body, got: %v", err)
	}
}

func TestHTTPHandler_Returns5xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"db down"}`))
	}))
	defer server.Close()

	h := NewHTTPHandler(server.URL)
	_, err := h.Handle(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.example.thing",
	}, HandlerEnv{})
	if err == nil {
		t.Fatal("expected error for 5xx response")
	}
}

func TestHTTPHandler_MalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 200 OK but body is not JSON
		w.Write([]byte("<html>not json</html>"))
	}))
	defer server.Close()

	h := NewHTTPHandler(server.URL)
	_, err := h.Handle(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.example.thing",
	}, HandlerEnv{})
	if err == nil {
		t.Fatal("expected unmarshal error for non-JSON response")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected unmarshal error, got: %v", err)
	}
}

func TestHTTPHandler_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow response, longer than ctx timeout
		time.Sleep(500 * time.Millisecond)
		json.NewEncoder(w).Encode(ActionResult{Status: "success"})
	}))
	defer server.Close()

	h := NewHTTPHandler(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := h.Handle(ctx, ActionRequest{
		Action:      "create",
		ContentType: "pub.example.thing",
	}, HandlerEnv{})
	if err == nil {
		t.Fatal("expected error on context cancellation")
	}
	if time.Since(start) > 300*time.Millisecond {
		t.Errorf("handler did not respect context — took %v", time.Since(start))
	}
}

func TestHTTPHandler_SendsJSONBody(t *testing.T) {
	var receivedBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("expected Content-Type=application/json, got %q", got)
		}
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		receivedBody = buf[:n]
		json.NewEncoder(w).Encode(ActionResult{Status: "success"})
	}))
	defer server.Close()

	h := NewHTTPHandler(server.URL)
	_, _ = h.Handle(context.Background(), ActionRequest{
		Action:      "create",
		ContentType: "pub.example.thing",
		Payload:     map[string]any{"key": "value"},
	}, HandlerEnv{})

	var parsed ActionRequest
	if err := json.Unmarshal(receivedBody, &parsed); err != nil {
		t.Fatalf("server received non-JSON body: %v\n%s", err, receivedBody)
	}
	if parsed.Action != "create" || parsed.ContentType != "pub.example.thing" {
		t.Errorf("unexpected request payload: %+v", parsed)
	}
	if v, _ := parsed.Payload["key"].(string); v != "value" {
		t.Errorf("payload not forwarded, got: %+v", parsed.Payload)
	}
}
