package stream

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStderr runs fn while temporarily redirecting stderr to a pipe and
// returns whatever fn wrote there.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = orig })

	fn()
	w.Close()

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestLogSuppressedEmit_StructuredJSON(t *testing.T) {
	// The "not_registered_locally" reason is gated on POLIS_DIAGNOSE_SUPPRESSIONS;
	// this test exercises log shape, so opt in.
	t.Setenv("POLIS_DIAGNOSE_SUPPRESSIONS", "1")
	out := captureStderr(t, func() {
		LogSuppressedEmit(
			"pub.polis.follow.announced",
			"not_registered_locally",
			"alice.polis.pub",
			map[string]interface{}{"target_domain": "bob.polis.pub", "ds_url": "https://ds.polis.pub"},
		)
	})

	out = strings.TrimSpace(out)
	if out == "" {
		t.Fatal("expected stderr output")
	}

	var rec map[string]interface{}
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, out)
	}

	checks := map[string]string{
		"event":         "pub.polis.emit.suppressed",
		"event_type":    "pub.polis.follow.announced",
		"reason":        "not_registered_locally",
		"actor":         "alice.polis.pub",
		"source":        "cli",
		"target_domain": "bob.polis.pub",
		"ds_url":        "https://ds.polis.pub",
	}
	for k, want := range checks {
		got, ok := rec[k].(string)
		if !ok || got != want {
			t.Errorf("field %q = %v, want %q", k, rec[k], want)
		}
	}
	if _, ok := rec["ts"].(string); !ok {
		t.Error("missing or non-string ts field")
	}
}

func TestLogSuppressedEmit_OmitsEmptyActor(t *testing.T) {
	t.Setenv("POLIS_DIAGNOSE_SUPPRESSIONS", "1")
	out := captureStderr(t, func() {
		LogSuppressedEmit("pub.polis.follow.removed", "not_registered_locally", "", nil)
	})
	var rec map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, present := rec["actor"]; present {
		t.Errorf("actor should be omitted when empty; got %v", rec["actor"])
	}
}

// TestLogSuppressedEmit_NotRegisteredGatedByEnv exercises the local-vs-hosted
// gate: when POLIS_DIAGNOSE_SUPPRESSIONS is unset, "not_registered_locally"
// records do NOT fire. Self-hosted users on a fresh unregistered install
// shouldn't see DS-related log noise on every publish/comment/follow.
func TestLogSuppressedEmit_NotRegisteredGatedByEnv(t *testing.T) {
	// Ensure the env var is NOT set (Setenv with "" still sets the key to
	// empty, which our gate treats as unset since it checks != "1").
	t.Setenv("POLIS_DIAGNOSE_SUPPRESSIONS", "")

	out := captureStderr(t, func() {
		LogSuppressedEmit(
			"pub.polis.post.published",
			"not_registered_locally",
			"alice.polis.pub",
			map[string]interface{}{"ds_url": "https://ds.polis.pub"},
		)
	})
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no stderr output when POLIS_DIAGNOSE_SUPPRESSIONS unset; got: %s", out)
	}
}

// TestLogSuppressedEmit_OtherReasonsAlwaysLog confirms the gate is scoped to
// "not_registered_locally" only — other reasons (config_missing, etc.) still
// log unconditionally since those indicate a genuinely broken setup that's
// useful to surface in any environment.
func TestLogSuppressedEmit_OtherReasonsAlwaysLog(t *testing.T) {
	t.Setenv("POLIS_DIAGNOSE_SUPPRESSIONS", "")

	out := captureStderr(t, func() {
		LogSuppressedEmit(
			"pub.polis.post.published",
			"config_missing",
			"alice.polis.pub",
			map[string]interface{}{"missing": "DISCOVERY_SERVICE_URL"},
		)
	})
	out = strings.TrimSpace(out)
	if out == "" {
		t.Fatal("config_missing should always log, even when POLIS_DIAGNOSE_SUPPRESSIONS unset")
	}
	var rec map[string]interface{}
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if rec["reason"] != "config_missing" {
		t.Errorf("reason field = %v, want config_missing", rec["reason"])
	}
}

func TestLogSuppressedEmit_ContextDoesNotOverrideReserved(t *testing.T) {
	out := captureStderr(t, func() {
		// Caller maliciously passes "event" in context — should be namespaced.
		LogSuppressedEmit("pub.polis.tag.applied", "config_missing", "alice.polis.pub",
			map[string]interface{}{"event": "spoofed", "tag": "design"})
	})
	var rec map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if rec["event"] != "pub.polis.emit.suppressed" {
		t.Errorf("reserved event field clobbered: %v", rec["event"])
	}
	if rec["context_event"] != "spoofed" {
		t.Errorf("colliding context field should be namespaced under context_*; got %v", rec["context_event"])
	}
	if rec["tag"] != "design" {
		t.Errorf("non-colliding context field should pass through; got %v", rec["tag"])
	}
}
