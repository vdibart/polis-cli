package actor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func keypair(t *testing.T) (priv, pub []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return priv, pub
}

func TestSignAndVerify_RoundTrip(t *testing.T) {
	opPriv, opPub := keypair(t)
	r := vectorRegistry()
	r.Signature = ""

	if err := Sign(r, opPriv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	status, err := Verify(r, opPub)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if status != StatusValid {
		t.Errorf("status = %q, want valid", status)
	}
}

// TestVerify_ReportsFourStatesSeparately — collapsing any two makes a judgment
// the protocol is not allowed to make for a consumer. An unsigned registry is a
// fact; a registry we could not check is not the same as one that failed.
func TestVerify_ReportsFourStatesSeparately(t *testing.T) {
	opPriv, opPub := keypair(t)
	_, otherPub := keypair(t)

	unsigned := vectorRegistry()
	unsigned.Signature = ""
	if s, _ := Verify(unsigned, opPub); s != StatusUnsigned {
		t.Errorf("no signature: got %q, want unsigned", s)
	}

	signed := vectorRegistry()
	signed.Signature = ""
	Sign(signed, opPriv)

	if s, _ := Verify(signed, nil); s != StatusUnknown {
		t.Errorf("no key available: got %q, want unknown", s)
	}
	if s, _ := Verify(signed, otherPub); s == StatusValid {
		t.Error("a signature from the wrong key must not verify")
	}
}

// TestSignatureCoversEveryClaim walks the fields a reader relies on and
// asserts each one is inside the signature. A field outside it can be edited by
// anyone with write access while the file still verifies — which is the whole
// failure the registry exists to prevent.
func TestSignatureCoversEveryClaim(t *testing.T) {
	opPriv, opPub := keypair(t)

	tamper := map[string]func(*Registry){
		"operator":         func(r *Registry) { r.Operator = "evil.example" },
		"asserted":         func(r *Registry) { r.Asserted = "2020-01-01T00:00:00Z" },
		"schema version":   func(r *Registry) { r.V = "pub.polis.actor-registry.v99" },
		"generator":        func(r *Registry) { r.Generator = "someone-else/1.0" },
		"actor domain":     func(r *Registry) { r.Actors[0].Domain = "lookalike.example" },
		"authority":        func(r *Registry) { r.Actors[0].Authority = AuthorityUser },
		"expected actions": func(r *Registry) { r.Actors[0].ExpectedActions = []string{"anything"} },
		// The one most likely to be left outside by a well-meaning
		// implementation, so that an actor can countersign without the operator
		// re-signing. Stripping it downgrades "the actor agrees" to "we say so"
		// and nothing would notice.
		"countersignature": func(r *Registry) { r.Actors[0].Countersignature = "" },
		"a new actor":      func(r *Registry) { r.Actors = append(r.Actors, Entry{Domain: "sneak.example"}) },
	}

	for name, mutate := range tamper {
		t.Run(name, func(t *testing.T) {
			r := vectorRegistry()
			r.Signature = ""
			if err := Sign(r, opPriv); err != nil {
				t.Fatalf("Sign: %v", err)
			}
			mutate(r)
			status, _ := Verify(r, opPub)
			if status == StatusValid {
				t.Errorf("editing %s left the signature verifying — the field is outside the signing base", name)
			}
		})
	}
}

func TestCountersign_RoundTrip(t *testing.T) {
	judgePriv, judgePub := keypair(t)
	r := vectorRegistry()
	r.Actors[0].Countersignature = ""

	sig, err := Countersign(r, &r.Actors[0], judgePriv)
	if err != nil {
		t.Fatalf("Countersign: %v", err)
	}
	r.Actors[0].Countersignature = sig

	status, err := VerifyCountersignature(r, &r.Actors[0], judgePub)
	if err != nil {
		t.Fatalf("VerifyCountersignature: %v", err)
	}
	if status != StatusValid {
		t.Errorf("status = %q, want valid", status)
	}
}

// TestCountersignature_IsNotReplayableAcrossOperators is the reason `operator`
// is inside the entry signing base.
//
// Without it, a hostile operator lists the same actor in ITS registry, pastes
// our countersignature across, and it verifies — so the file now says the actor
// agreed to be that operator's actor. "The actor agrees it is listed" is
// meaningless without "listed BY WHOM".
func TestCountersignature_IsNotReplayableAcrossOperators(t *testing.T) {
	judgePriv, judgePub := keypair(t)

	ours := vectorRegistry()
	ours.Actors[0].Countersignature = ""
	sig, err := Countersign(ours, &ours.Actors[0], judgePriv)
	if err != nil {
		t.Fatalf("Countersign: %v", err)
	}
	ours.Actors[0].Countersignature = sig

	// A hostile operator copies the entry verbatim, including the signature.
	theirs := &Registry{
		V:        SchemaVersion,
		Operator: "evil.example",
		Actors:   []Entry{ours.Actors[0]},
		Asserted: ours.Asserted,
	}
	status, _ := VerifyCountersignature(theirs, &theirs.Actors[0], judgePub)
	if status == StatusValid {
		t.Fatal("a countersignature verified in another operator's registry — it is replayable")
	}

	// Sanity: it still verifies where it belongs.
	if s, _ := VerifyCountersignature(ours, &ours.Actors[0], judgePub); s != StatusValid {
		t.Errorf("the countersignature stopped verifying in its own registry: %q", s)
	}
}

// TestCountersignature_WideningBreaksIt is the asymmetry the countersignature
// exists for: an operator cannot unilaterally widen what it claims an actor
// does. Narrowing and removal need no consent, because a decommissioned or
// compromised actor cannot sign and you must always be able to disown one —
// but that is a policy about which operations are ALLOWED, not a property of
// the bytes, so only the widening half is testable here.
func TestCountersignature_WideningBreaksIt(t *testing.T) {
	judgePriv, judgePub := keypair(t)
	r := vectorRegistry()
	r.Actors[0].Countersignature = ""
	sig, _ := Countersign(r, &r.Actors[0], judgePriv)
	r.Actors[0].Countersignature = sig

	r.Actors[0].ExpectedActions = append(r.Actors[0].ExpectedActions, "pub.polis.comment.published")
	if s, _ := VerifyCountersignature(r, &r.Actors[0], judgePub); s == StatusValid {
		t.Error("widening expected_actions left the countersignature verifying")
	}
}

func TestVerifyCountersignature_AbsentIsUnsignedNotInvalid(t *testing.T) {
	_, judgePub := keypair(t)
	r := vectorRegistry()
	r.Actors[0].Countersignature = ""
	s, err := VerifyCountersignature(r, &r.Actors[0], judgePub)
	if err != nil {
		t.Fatalf("VerifyCountersignature: %v", err)
	}
	if s != StatusUnsigned {
		t.Errorf("status = %q, want unsigned — an operator-signed-only entry is a weaker claim, not a broken one", s)
	}
}

// TestCanonicalJSON_HTMLEscaping pins Go's encoding/json HTML escaping into the
// registry's signing base, as the other five JSON types already do.
//
// `&`, `<` and `>` are not JSON metacharacters and essentially no serialiser
// outside Go escapes them. Go does, by default, and the escaping is INSIDE the
// bytes that were signed. A second implementation that writes them raw produces
// a different digest and concludes our signature is invalid.
//
// ⛔ Do not "fix" this to unescaped output.
func TestCanonicalJSON_HTMLEscaping(t *testing.T) {
	r := &Registry{
		V:        SchemaVersion,
		Operator: "polis.polis.pub",
		Actors: []Entry{{
			Domain:          "judge.polis.pub",
			Authority:       AuthorityOperator,
			ExpectedActions: []string{"com.example.did?a=1&b=2", "com.example.<x>"},
		}},
		Asserted:  "2026-09-05T14:02:00Z",
		Generator: "polis-cli-go/0.67.0",
	}
	got, err := CanonicalJSON(r)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	for _, want := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("expected %s in the signing base, got: %s", want, got)
		}
	}
	if strings.ContainsAny(string(got), "<>&") {
		t.Errorf("a raw <, > or & leaked into the signing base: %s", got)
	}

	// The entry base must escape identically, or a countersignature made by one
	// implementation would not verify for the other.
	entry, _ := EntryCanonicalJSON(r, &r.Actors[0])
	if !strings.Contains(string(entry), `\u0026`) || strings.ContainsAny(string(entry), "<>&") {
		t.Errorf("entry base does not escape identically: %s", entry)
	}
}

func TestSignAndWrite_RoundTripsThroughDisk(t *testing.T) {
	opPriv, opPub := keypair(t)
	dir := t.TempDir()
	path := DefaultPath(dir)

	r := vectorRegistry()
	r.Signature = ""
	if err := SignAndWrite(r, path, opPriv); err != nil {
		t.Fatalf("SignAndWrite: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// 0644: a registry a stranger cannot fetch proves nothing.
	if info.Mode().Perm() != 0644 {
		t.Errorf("mode = %v, want 0644 — this is public served content", info.Mode().Perm())
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	status, err := Verify(loaded, opPub)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if status != StatusValid {
		t.Errorf("a registry did not survive a disk round trip: %q", status)
	}
	if loaded.Find("judge.polis.pub") == nil {
		t.Error("Find did not locate the entry that was written")
	}
	if loaded.Find("nobody.example") != nil {
		t.Error("Find returned an entry for a domain that is not listed")
	}
}

func TestDefaultPath(t *testing.T) {
	got := DefaultPath("/data/tenants/polis")
	want := filepath.Join("/data/tenants/polis", "content", "pub.polis.core", "actor", "registry.json")
	if got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
}

// TestGeneratorIsRead is the version-propagation guard.
//
// pkg/following carried the package Version var and both assigners for five
// months with NOTHING READING IT, so every file's generator froze at whichever
// CLI created it. pkg/license had the reader and no assigner, so every
// published licence said "polis-cli-go/dev" INSIDE its signature. Both were
// silent. This asserts the resulting VALUE lands in the signed bytes, not
// merely that the field exists.
func TestGeneratorIsRead(t *testing.T) {
	old := Version
	defer func() { Version = old }()
	Version = "9.9.9"

	if GetGenerator() != "polis-cli-go/9.9.9" {
		t.Fatalf("GetGenerator() = %q", GetGenerator())
	}

	r := &Registry{
		V:         SchemaVersion,
		Operator:  "polis.polis.pub",
		Asserted:  "2026-09-05T14:02:00Z",
		Generator: GetGenerator(),
	}
	canonical, err := CanonicalJSON(r)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if !strings.Contains(string(canonical), `"generator":"polis-cli-go/9.9.9"`) {
		t.Errorf("the generator did not reach the signed bytes: %s", canonical)
	}
}
