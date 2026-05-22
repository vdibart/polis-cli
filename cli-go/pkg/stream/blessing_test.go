package stream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestBlessingHandler_EventTypes(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	types := h.EventTypes()

	if len(types) != 3 {
		t.Fatalf("EventTypes() len = %d, want 3", len(types))
	}

	expected := map[string]bool{
		"pub.polis.comment.blessing.requested": true,
		"pub.polis.comment.blessing.granted":   true,
		"pub.polis.comment.blessing.denied":    true,
	}
	for _, typ := range types {
		if !expected[typ] {
			t.Errorf("unexpected event type: %q", typ)
		}
	}
}

func TestBlessingHandler_ProcessRequested_WithTargetDomain(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.requested",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 1 {
		t.Fatalf("expected 1 blessing, got %d", len(bs.Blessings))
	}
	if bs.Blessings[0].Status != "pending" {
		t.Errorf("status = %q, want %q", bs.Blessings[0].Status, "pending")
	}
	if bs.Blessings[0].SourceURL != "https://alice.com/comments/1.md" {
		t.Errorf("source_url = %q, want %q", bs.Blessings[0].SourceURL, "https://alice.com/comments/1.md")
	}
}

func TestBlessingHandler_ProcessRequested_FallbackToTargetURL(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	// No target_domain — should fall back to extracting from target_url
	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.requested",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"source_url": "https://alice.com/comments/1.md",
				"target_url": "https://bob.com/posts/1.md",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 1 {
		t.Fatalf("expected 1 blessing (via fallback), got %d", len(bs.Blessings))
	}
	if bs.Blessings[0].Status != "pending" {
		t.Errorf("status = %q, want %q", bs.Blessings[0].Status, "pending")
	}
}

func TestBlessingHandler_ProcessGranted(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 1 {
		t.Fatalf("expected 1 blessing, got %d", len(bs.Blessings))
	}
	if bs.Blessings[0].Status != "granted" {
		t.Errorf("status = %q, want %q", bs.Blessings[0].Status, "granted")
	}
	if bs.Granted != 1 {
		t.Errorf("granted = %d, want 1", bs.Granted)
	}
}

func TestBlessingHandler_ProcessDenied(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.denied",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 1 {
		t.Fatalf("expected 1 blessing, got %d", len(bs.Blessings))
	}
	if bs.Blessings[0].Status != "denied" {
		t.Errorf("status = %q, want %q", bs.Blessings[0].Status, "denied")
	}
	if bs.Denied != 1 {
		t.Errorf("denied = %d, want 1", bs.Denied)
	}
}

func TestBlessingHandler_IgnoresOtherDomains(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.requested",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://charlie.com/posts/1.md",
				"target_domain": "charlie.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 0 {
		t.Errorf("expected 0 blessings for other domain, got %d", len(bs.Blessings))
	}
}

func TestBlessingHandler_FullLifecycle(t *testing.T) {
	h := &BlessingHandler{MyDomain: "bob.com"}
	state := h.NewState()

	// Step 1: Request
	events1 := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.requested",
			Actor: "alice.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:00:00Z",
		},
	}

	result1, err := h.Process(events1, state)
	if err != nil {
		t.Fatalf("Process batch 1: %v", err)
	}

	bs1 := result1.(*BlessingState)
	if len(bs1.Blessings) != 1 || bs1.Blessings[0].Status != "pending" {
		t.Fatalf("after request: expected 1 pending blessing, got %d with status %q",
			len(bs1.Blessings), bs1.Blessings[0].Status)
	}

	// Step 2: Grant
	events2 := []discovery.StreamEvent{
		{
			ID:    json.Number("2"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
			},
			Timestamp: "2026-02-10T10:01:00Z",
		},
	}

	result2, err := h.Process(events2, bs1)
	if err != nil {
		t.Fatalf("Process batch 2: %v", err)
	}

	bs2 := result2.(*BlessingState)
	if len(bs2.Blessings) != 1 {
		t.Fatalf("after grant: expected 1 blessing, got %d", len(bs2.Blessings))
	}
	if bs2.Blessings[0].Status != "granted" {
		t.Errorf("after grant: status = %q, want %q", bs2.Blessings[0].Status, "granted")
	}
	if bs2.Granted != 1 {
		t.Errorf("after grant: granted = %d, want 1", bs2.Granted)
	}
}

// TestBlessingHandler_AutoblessNoAttestation_Dropped — R20-C-F4
// (2026-05-18). Pre-fix, autobless events without ds_attestation
// were "processed without warning" as a legacy concession. An
// attacker could emit a fake autobless event with no attestation
// and the local cache would accept it. New posture: drop the event.
func TestBlessingHandler_AutoblessNoAttestation_Dropped(t *testing.T) {
	// DSKeyCache wired so the handler's "no cache configured" path
	// doesn't fire — this test is specifically about empty attestation.
	cache := discovery.NewDSKeyCache("http://localhost:1", time.Hour)
	h := &BlessingHandler{MyDomain: "bob.com", DSKeyCache: cache}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"source_url":    "https://alice.com/comments/1.md",
				"target_url":    "https://bob.com/posts/1.md",
				"target_domain": "bob.com",
				"auto_blessed":  true,
				"policy_rule":   "emit pub.polis.comment.blessing from following",
				"policy_source": "user-published",
				// No ds_attestation — must be dropped.
			},
			Timestamp: "2026-03-23T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 0 {
		t.Errorf("R20-C-F4 regression: autobless event without attestation produced %d blessings; expected 0", len(bs.Blessings))
	}
}

// TestBlessingHandler_AutoblessCommentURLFields — DS auto-blessing
// events use comment_url/in_reply_to instead of source_url/target_url.
// Regression test: these events must not be silently skipped.
//
// Post-R20-C-F4 (2026-05-18): the event now requires a valid
// ds_attestation. Stand up a fake DS that signs the canonical with a
// known key; the handler verifies and processes the entry.
func TestBlessingHandler_AutoblessCommentURLFields(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": string(pubSSH),
			"key_id":     "ds-primary",
		})
	}))
	defer server.Close()
	cache := discovery.NewDSKeyCache(server.URL, time.Hour)

	commentURL := "https://vdibart.polis.pub/comments/20260323/ongoing-list-20260323.md"
	inReplyTo := "https://david.polis.pub/posts/20260323/ongoing-list-of-ideas.md"
	policyRule := "emit pub.polis.comment.blessing from following"
	policySource := "user-published"
	dsKeyID := "ds-primary"
	canonical := discovery.BuildCanonicalJSON(map[string]interface{}{
		"type":          "pub.polis.comment.blessing",
		"action":        "autobless",
		"comment_url":   commentURL,
		"target_url":    inReplyTo,
		"policy_rule":   policyRule,
		"policy_source": policySource,
		"ds_key_id":     dsKeyID,
	})
	attestation, err := signing.SignContent([]byte(canonical), privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	h := &BlessingHandler{MyDomain: "david.polis.pub", DSKeyCache: cache}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "david.polis.pub",
			Payload: map[string]interface{}{
				"comment_url":    commentURL,
				"in_reply_to":    inReplyTo,
				"target_domain":  "david.polis.pub",
				"source_domain":  "vdibart.polis.pub",
				"auto_blessed":   true,
				"bless_reason":   "policy-match",
				"policy_rule":    policyRule,
				"policy_source":  policySource,
				"ds_attestation": attestation,
				"ds_key_id":      dsKeyID,
			},
			Timestamp: "2026-03-23T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 1 {
		t.Fatalf("expected 1 blessing, got %d", len(bs.Blessings))
	}
	if bs.Blessings[0].Status != "granted" {
		t.Errorf("status = %q, want %q", bs.Blessings[0].Status, "granted")
	}
	if bs.Blessings[0].SourceURL != commentURL {
		t.Errorf("source_url = %q, want comment_url value", bs.Blessings[0].SourceURL)
	}
	if bs.Blessings[0].TargetURL != inReplyTo {
		t.Errorf("target_url = %q, want in_reply_to value", bs.Blessings[0].TargetURL)
	}
	if bs.Granted != 1 {
		t.Errorf("granted = %d, want 1", bs.Granted)
	}
}

func TestBlessingHandler_AutoblessWithValidAttestation_Processes(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": string(pubSSH),
			"key_id":     "ds-primary",
		})
	}))
	defer server.Close()

	cache := discovery.NewDSKeyCache(server.URL, time.Hour)

	commentURL := "https://alice.com/comments/1.md"
	targetURL := "https://bob.com/posts/1.md"
	policyRule := "emit pub.polis.comment.blessing from following"
	policySource := "user-published"
	dsKeyID := "ds-primary"

	canonical := discovery.BuildCanonicalJSON(map[string]interface{}{
		"type":          "pub.polis.comment.blessing",
		"action":        "autobless",
		"comment_url":   commentURL,
		"target_url":    targetURL,
		"policy_rule":   policyRule,
		"policy_source": policySource,
		"ds_key_id":     dsKeyID,
	})
	attestation, err := signing.SignContent([]byte(canonical), privPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	h := &BlessingHandler{MyDomain: "bob.com", DSKeyCache: cache}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"source_url":     commentURL,
				"target_url":     targetURL,
				"comment_url":    commentURL,
				"in_reply_to":    targetURL,
				"target_domain":  "bob.com",
				"auto_blessed":   true,
				"policy_rule":    policyRule,
				"policy_source":  policySource,
				"ds_attestation": attestation,
				"ds_key_id":      dsKeyID,
			},
			Timestamp: "2026-03-23T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	bs := result.(*BlessingState)
	if len(bs.Blessings) != 1 {
		t.Fatalf("expected 1 blessing, got %d", len(bs.Blessings))
	}
	if bs.Blessings[0].Status != "granted" {
		t.Errorf("status = %q, want %q", bs.Blessings[0].Status, "granted")
	}
}

// TestBlessingHandler_AutoblessWithInvalidAttestation_Drops — R20-C-F4
// (2026-05-18). Pre-fix invalid attestations were "warn-only" — the
// event got processed anyway. Now they're dropped.
func TestBlessingHandler_AutoblessWithInvalidAttestation_Drops(t *testing.T) {
	_, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	// Sign with a different key
	otherPrivPEM, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("keygen2: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"public_key": string(pubSSH),
			"key_id":     "ds-primary",
		})
	}))
	defer server.Close()

	cache := discovery.NewDSKeyCache(server.URL, time.Hour)

	// Sign with wrong key
	canonical := discovery.BuildCanonicalJSON(map[string]interface{}{
		"type":          "pub.polis.comment.blessing",
		"action":        "autobless",
		"comment_url":   "https://alice.com/comments/1.md",
		"target_url":    "https://bob.com/posts/1.md",
		"policy_rule":   "emit pub.polis.comment.blessing from following",
		"policy_source": "user-published",
		"ds_key_id":     "ds-primary",
	})
	badAttestation, err := signing.SignContent([]byte(canonical), otherPrivPEM)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	h := &BlessingHandler{MyDomain: "bob.com", DSKeyCache: cache}
	state := h.NewState()

	events := []discovery.StreamEvent{
		{
			ID:    json.Number("1"),
			Type:  "pub.polis.comment.blessing.granted",
			Actor: "bob.com",
			Payload: map[string]interface{}{
				"source_url":     "https://alice.com/comments/1.md",
				"target_url":     "https://bob.com/posts/1.md",
				"comment_url":    "https://alice.com/comments/1.md",
				"in_reply_to":    "https://bob.com/posts/1.md",
				"target_domain":  "bob.com",
				"auto_blessed":   true,
				"policy_rule":    "emit pub.polis.comment.blessing from following",
				"policy_source":  "user-published",
				"ds_attestation": badAttestation,
				"ds_key_id":      "ds-primary",
			},
			Timestamp: "2026-03-23T10:00:00Z",
		},
	}

	result, err := h.Process(events, state)
	if err != nil {
		t.Fatalf("Process should not fail on bad attestation: %v", err)
	}

	// R20-C-F4: event must be DROPPED on invalid attestation. The
	// pre-fix "warn-only" posture admitted a forgery vector — an
	// attacker emitting an autobless event with a self-signed
	// attestation would have it accepted as granted.
	bs := result.(*BlessingState)
	if len(bs.Blessings) != 0 {
		t.Errorf("R20-C-F4 regression: invalid attestation produced %d blessings; expected 0", len(bs.Blessings))
	}
}
