package discovery

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Signet epic 11 — the agent marker in the relationship request.

func TestUnmarkedRelationshipCanonicalBytesAreUnchanged(t *testing.T) {
	got, err := marshalCanonical(relationshipCanonicalPayload{
		Type: "pub.polis.comment.blessing", SourceURL: "https://bob.example/c.md",
		TargetURL: "https://alice.example/p.md", Action: "grant", Timestamp: "2026-09-15T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Byte-identical to the DS's five-field JSON.stringify.
	want := `{"type":"pub.polis.comment.blessing","source_url":"https://bob.example/c.md","target_url":"https://alice.example/p.md","action":"grant","timestamp":"2026-09-15T12:00:00Z"}`
	if string(got) != want {
		t.Errorf("unmarked canonical bytes moved:\n got: %s\nwant: %s", got, want)
	}
}

func TestMarkedRelationshipCanonicalBytesAppendAgentThenGrant(t *testing.T) {
	got, err := marshalCanonical(relationshipCanonicalPayload{
		Type: "pub.polis.comment.blessing", SourceURL: "https://bob.example/c.md",
		TargetURL: "https://alice.example/p.md", Action: "deny", Timestamp: "2026-09-15T12:00:00Z",
		Agent: "rosie", Grant: "https://alice.example/content/pub.polis.core/attestation/x.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"pub.polis.comment.blessing","source_url":"https://bob.example/c.md","target_url":"https://alice.example/p.md","action":"deny","timestamp":"2026-09-15T12:00:00Z","agent":"rosie","grant":"https://alice.example/content/pub.polis.core/attestation/x.json"}`
	if string(got) != want {
		t.Errorf("marked canonical bytes:\n got: %s\nwant: %s", got, want)
	}
}

// The marker must be INSIDE the signature: the request's signature verifies
// over the marked canonical bytes and not over the unmarked ones.
func TestMarkedRelationshipRequestSignsTheMarker(t *testing.T) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	var body RelationshipUpdateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	marker := AgentMarker{Agent: "rosie", Grant: "https://alice.example/content/pub.polis.core/attestation/x.json"}
	if err := c.UpdateRelationshipMarked("pub.polis.comment.blessing", "https://bob.example/c.md", "https://alice.example/p.md", "grant", marker, priv); err != nil {
		t.Fatal(err)
	}
	if body.Agent != marker.Agent || body.Grant != marker.Grant {
		t.Fatalf("request body lacks the marker: %+v", body)
	}
	marked, _ := marshalCanonical(relationshipCanonicalPayload{
		Type: body.Type, SourceURL: body.SourceURL, TargetURL: body.TargetURL, Action: body.Action,
		Timestamp: body.Timestamp, Agent: body.Agent, Grant: body.Grant,
	})
	if ok, _ := signing.VerifySignature(marked, pub, body.Signature); !ok {
		t.Fatal("signature does not cover the marked payload")
	}
	unmarked, _ := marshalCanonical(relationshipCanonicalPayload{
		Type: body.Type, SourceURL: body.SourceURL, TargetURL: body.TargetURL, Action: body.Action, Timestamp: body.Timestamp,
	})
	if ok, _ := signing.VerifySignature(unmarked, pub, body.Signature); ok {
		t.Fatal("signature also verifies without the marker — it is outside the signed bytes")
	}
}

func TestUnmarkedRelationshipRequestCarriesNoMarkerKeys(t *testing.T) {
	priv, _, _ := signing.GenerateKeypair()
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		raw = string(b)
	}))
	defer srv.Close()
	if err := NewClient(srv.URL, "").UpdateRelationship("pub.polis.comment.blessing", "https://bob.example/c.md", "https://alice.example/p.md", "grant", priv); err != nil {
		t.Fatal(err)
	}
	var keys map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		t.Fatal(err)
	}
	_, hasAgent := keys["agent"]
	_, hasGrant := keys["grant"]
	if hasAgent || hasGrant || strings.Contains(raw, `"agent":""`) {
		t.Fatalf("an unmarked request carries marker keys: %s", raw)
	}
}

// TestRelationshipSignatureMatchesTheSharedContractFixture pins the client to the
// fixture the DS verifies (Signet epic 45 D9): the Go client builds exactly the
// fixture's canonical bytes from each request, and the fixture's signatures —
// made by this package's signer — verify over them. The DS side is
// discovery-service/core/handlers/relationships.test.ts.
func TestRelationshipSignatureMatchesTheSharedContractFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "discovery-service", "core", "contract-fixtures", "relationship-signature.json"))
	if err != nil {
		skipWithoutDS(t)
		t.Fatalf("read fixture: %v", err)
	}
	var fixture struct {
		PublicKey string `json:"public_key"`
		Cases     []struct {
			Name      string                    `json:"name"`
			Canonical string                    `json:"canonical"`
			Request   RelationshipUpdateRequest `json:"request"`
			Signature string                    `json:"signature"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(fixture.Cases) != 2 {
		t.Fatalf("fixture has %d cases; expected the five-field vector and delegation.md §5.1", len(fixture.Cases))
	}
	for _, c := range fixture.Cases {
		r := c.Request
		got, err := marshalCanonical(relationshipCanonicalPayload{
			Type: r.Type, SourceURL: r.SourceURL, TargetURL: r.TargetURL, Action: r.Action,
			Timestamp: r.Timestamp, Agent: r.Agent, Grant: r.Grant,
		})
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.Canonical {
			t.Errorf("%s:\n got  %s\n want %s", c.Name, got, c.Canonical)
		}
		if ok, _ := signing.VerifySignature(got, []byte(fixture.PublicKey), c.Signature); !ok {
			t.Errorf("%s: the fixture signature does not verify over the client's bytes", c.Name)
		}
	}
}

func TestAHalfMarkerIsRefused(t *testing.T) {
	priv, _, _ := signing.GenerateKeypair()
	c := NewClient("http://127.0.0.1:1", "")
	if err := c.UpdateRelationshipMarked("pub.polis.comment.blessing", "a", "b", "grant", AgentMarker{Agent: "rosie"}, priv); err == nil {
		t.Fatal("a marker with an agent and no grant was sent")
	}
}
