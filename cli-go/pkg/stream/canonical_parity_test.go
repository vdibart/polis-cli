package stream

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// R23-2. The Go CLI and the bash CLI are independent publishers, and each
// rebuilds the signed canonical by hand — bash in `jq`, under a CRITICAL
// comment pointing at Go's struct. They do not have to agree for either to
// verify, because the discovery service rebuilds the canonical from whatever a
// client sent it. But when they DISAGREE it is always because one of them
// drifted, and nothing else would notice.
//
// ⚠️ Two ordering rules, and they are different on purpose:
//   - the TOP level is Go struct declaration order — {type, payload} — because
//     Go marshals a struct in declaration order and never sorts it;
//   - the PAYLOAD is a Go map, which encoding/json DOES sort, which is why the
//     bash side runs `jq -S` over it.
//
// Getting either backwards produces bytes that still verify for the publisher
// that made them and differ from the other implementation for ever.

func TestGoAndBashBuildTheSameStreamCanonical(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed; this test compares against the bash implementation")
	}

	cases := []struct {
		name    string
		payload map[string]interface{}
	}{
		{"keys already in order", map[string]interface{}{
			"timestamp": "2026-09-20T12:00:00Z", "url": "https://alice.example/p.md"}},
		{"keys needing a sort", map[string]interface{}{
			"url": "https://alice.example/p.md", "title": "A post",
			"timestamp": "2026-09-20T12:00:00Z"}},
		{"nested object", map[string]interface{}{
			"timestamp": "2026-09-20T12:00:00Z",
			"target":    map[string]interface{}{"url": "https://bob.example/", "handle": "bob"}}},
		{"only a timestamp", map[string]interface{}{"timestamp": "2026-09-20T12:00:00Z"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := discovery.MakeStreamCanonicalJSON("pub.polis.follow.announced", c.payload)
			if err != nil {
				t.Fatalf("MakeStreamCanonicalJSON: %v", err)
			}
			want := bashStreamCanonical(t, "pub.polis.follow.announced", c.payload)
			if string(got) != want {
				t.Errorf("canonical bytes differ between implementations:\n go:   %s\n bash: %s", got, want)
			}
		})
	}
}

// bashStreamCanonical runs the same jq pipeline the bash CLI uses in
// _emit_stream_event. Kept as the literal expressions from that function so a
// change there shows up here as a failure.
func bashStreamCanonical(t *testing.T, eventType string, payload map[string]interface{}) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	sorted := runJQ(t, string(raw), "-S", "-c", ".")
	canonical := runJQ(t, "", "-n", "-c",
		"--arg", "type", eventType,
		"--argjson", "payload", sorted,
		"{type: $type, payload: $payload}")
	return canonical
}

func runJQ(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("jq", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("jq %v: %v", args, err)
	}
	return strings.TrimRight(string(out), "\n")
}

// TestEventTimestampDoesNotMutateTheCallersMap — these payload maps are built by
// callers that reuse and re-read them. Stamping one in place would leak a stale
// timestamp into the next publish, and the signature over it would be perfectly
// valid.
func TestEventTimestampDoesNotMutateTheCallersMap(t *testing.T) {
	original := map[string]interface{}{"url": "https://alice.example/p.md"}
	got := withEventTimestamp(original, "2026-09-20T12:00:00Z")

	if _, leaked := original["timestamp"]; leaked {
		t.Error("withEventTimestamp mutated the caller's map")
	}
	if got["timestamp"] != "2026-09-20T12:00:00Z" {
		t.Errorf("timestamp = %v, want the one passed in", got["timestamp"])
	}
	if got["url"] != original["url"] {
		t.Error("the rest of the payload did not survive")
	}
}

// TestEventTimestampKeepsAnExistingOne — an event type whose own schema carries
// a timestamp must not have it overwritten with the wall clock. That would be a
// lie with a valid signature over it.
func TestEventTimestampKeepsAnExistingOne(t *testing.T) {
	original := map[string]interface{}{"timestamp": "2020-01-01T00:00:00Z"}
	got := withEventTimestamp(original, "2026-09-20T12:00:00Z")
	if got["timestamp"] != "2020-01-01T00:00:00Z" {
		t.Errorf("overwrote the event's own timestamp with %v", got["timestamp"])
	}
}
