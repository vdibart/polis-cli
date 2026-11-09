package discovery

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// ⛔ THIS IS THE TEST R23-2 EXISTS BECAUSE OF.
//
// The obvious way to add freshness to a stream event is a sibling field, next
// to `type` and `payload` — which is what every other signed write on the
// discovery service does. It was written that way first, and it would have
// taken the whole fleet's feeds down:
//
// VerifyAuthorSignature re-derives the signed bytes from a STORED event. A
// stored event carries `created_at`, which is the discovery service's clock at
// insert, NOT the string the client signed. A sibling field is verified once at
// POST time and then discarded, so no consumer can ever rebuild the canonical.
// Every event published in the new form would fail verification everywhere, and
// FilterVerifiedEvents drops a failure SILENTLY — on every sync cycle, in
// webapp/internal/server/sync.go.
//
// Producer and verifier would each have been correct alone. Nothing else here
// would have caught it. So the timestamp lives INSIDE `payload`, which is
// stored and returned verbatim, and this test proves the round trip.

// storedAsTheServiceWouldReturnIt marshals what a publisher sent and reads it
// back as a consumer receives it — through the JSON boundary, with the
// service's own `created_at` attached and NOTHING carried over from the request
// except the fields the service stores.
func storedAsTheServiceWouldReturnIt(t *testing.T, eventType, actor string, payload map[string]interface{}, sig string) StreamEvent {
	t.Helper()
	wire, err := json.Marshal(map[string]interface{}{
		"id":      1,
		"type":    eventType,
		"actor":   actor,
		"payload": payload,
		// ⚠️ The service's clock, deliberately different from anything signed.
		"created_at": time.Now().Add(3 * time.Second).UTC().Format(time.RFC3339),
		"signature":  sig,
	})
	if err != nil {
		t.Fatalf("marshal wire event: %v", err)
	}
	var got StreamEvent
	if err := json.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal wire event: %v", err)
	}
	return got
}

func TestVerifyAuthorSignatureSurvivesTheTimestamp(t *testing.T) {
	privPEM, pubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}

	cases := []struct {
		name    string
		payload map[string]interface{}
	}{
		{
			// The new form. This is the case that a sibling field would break.
			name: "new form — a timestamp inside the payload",
			payload: map[string]interface{}{
				"timestamp": time.Now().UTC().Format(time.RFC3339),
				"url":       "https://alice.example/posts/a.md",
			},
		},
		{
			// The old form. Every event already on the network looks like this,
			// and they must keep verifying for ever.
			name:    "old form — no timestamp at all",
			payload: map[string]interface{}{"url": "https://alice.example/posts/a.md"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			canonical, err := MakeStreamCanonicalJSON("pub.polis.follow.announced", c.payload)
			if err != nil {
				t.Fatalf("MakeStreamCanonicalJSON: %v", err)
			}
			sig, err := signing.SignContent(canonical, privPEM)
			if err != nil {
				t.Fatalf("SignContent: %v", err)
			}

			event := storedAsTheServiceWouldReturnIt(t, "pub.polis.follow.announced", "alice.example", c.payload, sig)

			cache := NewAuthorKeyCache(time.Hour, time.Minute)
			cache.entries["alice.example"] = &authorKeyEntry{
				publicKey: string(pubSSH),
				fetchedAt: time.Now(),
			}

			if err := VerifyAuthorSignature(cache, event); err != nil {
				t.Fatalf("a published event does not verify after a round trip: %v\n"+
					"Every consumer runs this on every sync cycle and DROPS what fails, "+
					"silently. If the signed canonical gained a field the stored event "+
					"does not carry, this is where it shows up.", err)
			}
		})
	}
}

// TestVerifyAuthorSignatureStillCatchesTampering — the round trip must not have
// been made to pass by weakening the check.
func TestVerifyAuthorSignatureStillCatchesTampering(t *testing.T) {
	privPEM, pubSSH, _ := signing.GenerateKeypair()
	payload := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"url":       "https://alice.example/posts/a.md",
	}
	canonical, _ := MakeStreamCanonicalJSON("pub.polis.follow.announced", payload)
	sig, _ := signing.SignContent(canonical, privPEM)

	cache := NewAuthorKeyCache(time.Hour, time.Minute)
	cache.entries["alice.example"] = &authorKeyEntry{publicKey: string(pubSSH), fetchedAt: time.Now()}

	t.Run("an edited payload field", func(t *testing.T) {
		tampered := map[string]interface{}{"timestamp": payload["timestamp"], "url": "https://evil.example/x.md"}
		event := storedAsTheServiceWouldReturnIt(t, "pub.polis.follow.announced", "alice.example", tampered, sig)
		if err := VerifyAuthorSignature(cache, event); err == nil {
			t.Error("an edited payload verified")
		}
	})

	t.Run("a back-dated timestamp", func(t *testing.T) {
		// ⭐ The point of putting it inside the payload: the timestamp is now
		// covered by the signature, so it cannot be rewritten in transit.
		tampered := map[string]interface{}{"timestamp": "2020-01-01T00:00:00Z", "url": payload["url"]}
		event := storedAsTheServiceWouldReturnIt(t, "pub.polis.follow.announced", "alice.example", tampered, sig)
		if err := VerifyAuthorSignature(cache, event); err == nil {
			t.Error("a rewritten timestamp verified — it is not covered by the signature")
		}
	})
}
