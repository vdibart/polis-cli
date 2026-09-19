package actor

import (
	"os"
	"strings"
	"testing"
)

// The registry's canonical bytes, locked in full against the vectors published
// in docs/signet/spec/signing-base.md §5.3 — Signet epic 08's format, epic 04's
// type.
//
// ⭐ THE VECTOR IS THE SPEC AND THIS TEST IS WHAT KEEPS IT TRUE. The field
// order is Go's STRUCT DECLARATION order, which is invisible from the published
// artifact: an independent reimplementer writes canonical JSON by sorting keys,
// as RFC 8785 and every other canonical-JSON convention do, produces different
// bytes, and concludes our signatures are invalid. Documenting the order is the
// reimplementability fix; a golden vector is what stops the document going
// stale the next time a field is added.
//
// ⛔ If these fail, the signing base changed and every registry signature and
// every countersignature just stopped verifying. Do not update the golden
// string to match the code — work out what moved and put it back.
const (
	goldenRegistry = `{"v":"pub.polis.actor-registry.v1","operator":"polis.polis.pub","actors":[{"domain":"judge.polis.pub","authority":"operator","expected_actions":["pub.polis.attestation.integrity"],"countersignature":"U1NIU0lHAAAAAWp1ZGdl"}],"asserted":"2026-09-05T14:02:00Z","generator":"polis-cli-go/0.67.0"}`

	goldenRegistryEntry = `{"v":"pub.polis.actor-registry.v1","operator":"polis.polis.pub","domain":"judge.polis.pub","authority":"operator","expected_actions":["pub.polis.attestation.integrity"]}`

	goldenRegistryEmpty = `{"v":"pub.polis.actor-registry.v1","operator":"polis.polis.pub","actors":[],"asserted":"2026-09-05T14:02:00Z","generator":"polis-cli-go/0.67.0"}`
)

func vectorRegistry() *Registry {
	return &Registry{
		V:        SchemaVersion,
		Operator: "polis.polis.pub",
		Actors: []Entry{
			{
				Domain:           "judge.polis.pub",
				Authority:        AuthorityOperator,
				ExpectedActions:  []string{"pub.polis.attestation.integrity"},
				Countersignature: "U1NIU0lHAAAAAWp1ZGdl",
			},
		},
		Asserted:  "2026-09-05T14:02:00Z",
		Generator: "polis-cli-go/0.67.0",
	}
}

func TestCanonicalJSON_Vector(t *testing.T) {
	got, err := CanonicalJSON(vectorRegistry())
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if string(got) != goldenRegistry {
		t.Errorf("canonical bytes moved:\n got: %s\nwant: %s", got, goldenRegistry)
	}
}

func TestEntryCanonicalJSON_Vector(t *testing.T) {
	r := vectorRegistry()
	got, err := EntryCanonicalJSON(r, &r.Actors[0])
	if err != nil {
		t.Fatalf("EntryCanonicalJSON: %v", err)
	}
	if string(got) != goldenRegistryEntry {
		t.Errorf("entry canonical bytes moved:\n got: %s\nwant: %s", got, goldenRegistryEntry)
	}
}

// TestCanonicalJSON_EmptyRegistryIsAList pins "we run no actors" to ONE byte
// sequence. A nil slice marshals as null and an empty one as [], and the two
// would be different signed claims about the same fact.
func TestCanonicalJSON_EmptyRegistryIsAList(t *testing.T) {
	r := &Registry{
		V:         SchemaVersion,
		Operator:  "polis.polis.pub",
		Asserted:  "2026-09-05T14:02:00Z",
		Generator: "polis-cli-go/0.67.0",
	}
	got, err := CanonicalJSON(r)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if string(got) != goldenRegistryEmpty {
		t.Errorf("empty registry bytes moved:\n got: %s\nwant: %s", got, goldenRegistryEmpty)
	}

	// The explicitly-empty slice must produce the identical bytes.
	r.Actors = []Entry{}
	again, _ := CanonicalJSON(r)
	if string(again) != string(got) {
		t.Errorf("nil and empty actor lists must serialise identically:\n%s\n%s", got, again)
	}
}

// TestEntryCanonicalJSON_EmptyActionsIsAList is the same pin one level down:
// "we expect this actor to do nothing" is a real, statable claim and it needs
// one byte sequence.
func TestEntryCanonicalJSON_EmptyActionsIsAList(t *testing.T) {
	r := &Registry{V: SchemaVersion, Operator: "polis.polis.pub"}
	nilActions := Entry{Domain: "x.polis.pub", Authority: AuthorityOperator}
	emptyActions := Entry{Domain: "x.polis.pub", Authority: AuthorityOperator, ExpectedActions: []string{}}

	a, err := EntryCanonicalJSON(r, &nilActions)
	if err != nil {
		t.Fatalf("EntryCanonicalJSON: %v", err)
	}
	b, _ := EntryCanonicalJSON(r, &emptyActions)
	if string(a) != string(b) {
		t.Errorf("nil and empty expected_actions must serialise identically:\n%s\n%s", a, b)
	}
	if !strings.Contains(string(a), `"expected_actions":[]`) {
		t.Errorf("empty expected_actions must be [], got: %s", a)
	}
}

// signingBaseSpec is the public specification of the signing base. It is a
// DELIVERABLE, not a write-up of one: the acceptance test for this format is
// that a competent stranger can produce bytes that verify against ours without
// reading any Go.
const signingBaseSpec = "../../../docs/signet/spec/signing-base.md"

// TestSpecPublishesTheVectors is the mechanical check that keeps the spec
// honest. Prose and code drift the moment a human transcribes between them, and
// a spec whose worked example is subtly wrong is worse than no spec — it
// produces a second implementation that is confidently incompatible.
func TestSpecPublishesTheVectors(t *testing.T) {
	data, err := os.ReadFile(signingBaseSpec)
	if err != nil {
		t.Fatalf("the specification is part of this format, not an optional extra: %v", err)
	}
	for _, vector := range []string{goldenRegistry, goldenRegistryEntry, goldenRegistryEmpty} {
		if !strings.Contains(string(data), vector) {
			t.Errorf("%s does not publish these exact bytes:\n%s", signingBaseSpec, vector)
		}
	}
}
