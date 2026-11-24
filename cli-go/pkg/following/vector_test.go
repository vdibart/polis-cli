package following

import (
	"os"
	"strings"
	"testing"
)

// TestCanonicalJSON_Vector locks a follow file's canonical bytes, in full,
// against the exact vector published in docs/signet/spec/signing-base.md —
// Signet epic 08, D2.
//
// ⭐ THE VECTOR IS THE SPEC AND THIS TEST IS WHAT KEEPS IT TRUE. The published
// field order is Go's STRUCT DECLARATION order, which is invisible from the
// artifact: an independent reimplementer writes canonical JSON by sorting keys,
// as RFC 8785 and every other canonical-JSON convention do, produces different
// bytes, and concludes our signatures are invalid. Documenting the order is the
// whole reimplementability fix; a golden vector is what stops the document from
// quietly going stale the next time a field is added.
//
// ⛔ If this test fails, the signing base changed and every existing a follow file
// signature just stopped verifying. Do not update the golden string to match
// the code — work out what moved and put it back.
const goldenFollowing = `{"version":"polis-cli-go/0.67.0","following":[{"url":"https://bob.example","added_at":"2026-08-28T09:15:00Z","site_title":"Bob's Notes","author_name":"Bob"}]}`

const goldenFollowingEmpty = `{"version":"polis-cli-go/0.67.0","following":[]}`

func TestCanonicalJSON_Vector(t *testing.T) {
	f := &FollowingFile{
		Version: "polis-cli-go/0.67.0",
		Following: []FollowingEntry{{
			URL:        "https://bob.example",
			AddedAt:    "2026-08-28T09:15:00Z",
			SiteTitle:  "Bob's Notes",
			AuthorName: "Bob",
		}},
	}

	got, err := canonicalJSON(f)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	if string(got) != goldenFollowing {
		t.Errorf("canonical bytes moved:\n got: %s\nwant: %s", got, goldenFollowing)
	}

	// "I follow nobody" has ONE byte sequence, not two. A nil slice marshals as
	// null and an empty one as [] — the pin that decides which is in the
	// signing base, so it is in the spec and it is in the vector.
	empty := &FollowingFile{Version: "polis-cli-go/0.67.0"}
	got, err = canonicalJSON(empty)
	if err != nil {
		t.Fatalf("canonicalJSON (empty): %v", err)
	}
	if string(got) != goldenFollowingEmpty {
		t.Errorf("empty-roster canonical bytes moved:\n got: %s\nwant: %s", got, goldenFollowingEmpty)
	}
}

// signingBaseSpec is the public specification of the signing base. It is a
// DELIVERABLE, not a write-up of one: the acceptance test for this format is
// that a competent stranger can produce bytes that verify against ours without
// reading any Go.
const signingBaseSpec = "../../../docs/signet/spec/signing-base.md"

// TestSpecPublishesTheVector is the mechanical check that keeps the spec honest.
//
// ⚠️ Prose and code drift the moment a human transcribes between them, and a
// spec whose worked example is subtly wrong is worse than no spec — it produces
// a second implementation that is confidently incompatible. So the documented
// bytes are compared against the SAME constant the golden test pins, on every
// test run, rather than by eye at review time.
func TestSpecPublishesTheVector(t *testing.T) {
	assertSpecPublishes(t, goldenFollowing)
	assertSpecPublishes(t, goldenFollowingEmpty)
}

func assertSpecPublishes(t *testing.T, vector string) {
	t.Helper()
	data, err := os.ReadFile(signingBaseSpec)
	if err != nil {
		t.Fatalf("the specification is part of this format, not an optional extra: %v", err)
	}
	if !strings.Contains(string(data), vector) {
		t.Errorf("%s does not publish these exact bytes for a follow file:\n%s",
			signingBaseSpec, vector)
	}
}

// goldenFollowingMarked is an entry a user agent added: only that entry
// carries the marker, after the existing fields; the unmarked entry beside it
// is byte-identical to goldenFollowing's.
const goldenFollowingMarked = `{"version":"polis-cli-go/0.67.0","following":[{"url":"https://bob.example","added_at":"2026-08-28T09:15:00Z","site_title":"Bob's Notes","author_name":"Bob"},{"url":"https://carol.example","added_at":"2026-11-23T12:00:00Z","agent":"Claude Desktop","grant":"https://alice.example/content/pub.polis.core/attestation/20261123T120000Z-0123456789abcdef.json"}]}`

// ⭐ The agent marker on a follow entry is omitted when absent, so an entry the
// owner made signs exactly as before, and a marked entry's bytes are pinned
// and published.
func TestCanonicalJSON_MarkedVector(t *testing.T) {
	f := &FollowingFile{
		Version: "polis-cli-go/0.67.0",
		Following: []FollowingEntry{{
			URL: "https://bob.example", AddedAt: "2026-08-28T09:15:00Z", SiteTitle: "Bob's Notes", AuthorName: "Bob",
		}, {
			URL: "https://carol.example", AddedAt: "2026-11-23T12:00:00Z",
			Agent: "Claude Desktop", Grant: "https://alice.example/content/pub.polis.core/attestation/20261123T120000Z-0123456789abcdef.json",
		}},
	}
	got, err := canonicalJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != goldenFollowingMarked {
		t.Errorf("marked canonical bytes moved:\n got: %s\nwant: %s", got, goldenFollowingMarked)
	}
	assertSpecPublishes(t, goldenFollowingMarked)
}

// ⭐ AddMarked marks only the entry it adds, and leaves an entry already
// followed as it was.
func TestAddMarkedMarksOnlyTheNewEntry(t *testing.T) {
	f := &FollowingFile{}
	f.Add("https://bob.example")
	if !f.AddMarked("https://carol.example", "Claude Desktop", "https://alice.example/g.json") {
		t.Fatal("carol not added")
	}
	if f.AddMarked("https://bob.example", "Claude Desktop", "https://alice.example/g.json") {
		t.Error("bob added twice")
	}
	if b := f.Get("https://bob.example"); b.Agent != "" || b.Grant != "" {
		t.Errorf("the owner's entry gained a marker: %+v", b)
	}
	if c := f.Get("https://carol.example"); c.Agent != "Claude Desktop" || c.Grant != "https://alice.example/g.json" {
		t.Errorf("the agent's entry is not marked: %+v", c)
	}
}
