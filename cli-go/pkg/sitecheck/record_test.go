package sitecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
)

// SIGNET epic 42 V2 — `polis validate <record-url>` failed on a valid record.
//
// RunArtifact ran ParseFrontmatter on every URL, so an attestation, tag or
// follow file read "not a signed polis artifact: no frontmatter found" even
// when it verified. Every RunArtifact test below failed that way on the
// unmodified tree.

// ⭐ A REAL RECORD, NOT A MODEL OF ONE. testdata/polis.polis.pub-agent-disclosure.json
// is the byte-for-byte record https://polis.polis.pub serves at
// /content/pub.polis.core/attestation/20260913T020718Z-411630ed319bf4db.json,
// and polis.polis.pub-public_key.txt is the public_key its .well-known/polis
// published, both captured 2026-09-13. A fixture written by the author of the
// verifier proves only that the two agree with each other.
func realAgentDisclosure(t *testing.T) (body, key []byte) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "polis.polis.pub-agent-disclosure.json"))
	if err != nil {
		t.Fatal(err)
	}
	key, err = os.ReadFile(filepath.Join("testdata", "polis.polis.pub-public_key.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return body, []byte(strings.TrimSpace(string(key)))
}

func TestTheLiveAgentDisclosureRecordVerifies(t *testing.T) {
	body, key := realAgentDisclosure(t)

	var p recordProbe
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	kind := recordKindFor(p)
	if kind == nil || kind.name != "attestation" {
		t.Fatalf("the live record was not recognised as an attestation: %+v", kind)
	}
	if got := strings.TrimRight(kind.signer(p), "/"); got != "https://polis.polis.pub" {
		t.Errorf("signer = %q, want the record's issuer", got)
	}

	res := kind.verify(body, key, nil)
	if res.status != "valid" {
		t.Fatalf("the live agent-disclosure record did not verify: %s (%v)", res.status, res.err)
	}
	rule, ok := IndexEntryRuleFor(kind.versionRule)
	if !ok {
		t.Fatal("attestation has no version rule")
	}
	version := res.version
	if match, _ := rule.VersionMatches(body, version); !match {
		t.Errorf("the live record's bytes do not match its own current_version %s", version)
	}

	// ⛔ And the check is not a rubber stamp: one changed value inside the
	// signed bytes fails both the signature and the version.
	tampered := []byte(strings.Replace(string(body), `"authority": "operator"`, `"authority": "tenant"`, 1))
	if string(tampered) == string(body) {
		t.Fatal("tamper did not apply")
	}
	if got := kind.verify(tampered, key, nil); got.status != "invalid" {
		t.Errorf("a tampered live record reported %q, want invalid", got.status)
	}
	if match, _ := rule.VersionMatches(tampered, version); match {
		t.Error("a tampered live record still matched its current_version")
	}
}

// signedAttestation returns a record signed by priv, claiming issuer.
func signedAttestation(t *testing.T, priv []byte, issuer string) string {
	t.Helper()
	r := &attestation.Record{
		Type:      attestation.TypeName,
		Issuer:    issuer,
		Predicate: "pub.polis.attestation.same-as",
		Subject:   attestation.Subject{Type: "identity", ID: "https://alice.other.example"},
		Asserted:  "2026-01-15T10:00:00Z",
		Generator: "polis-cli-go/test",
	}
	canonical, err := attestation.CanonicalJSON(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Version = attestation.ContentVersion(canonical)
	if r.Signature, err = signing.SignContent(canonical, priv); err != nil {
		t.Fatal(err)
	}
	return string(mustJSON(r))
}

func (s *remoteSite) put(path, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = body
}

const attestationPath = "/content/pub.polis.core/attestation/20260115T100000Z-0000000000000000.json"

func TestRunArtifactVerifiesAnAttestationRecord(t *testing.T) {
	site, priv := signedRemoteSite(t, 0)
	ts := site.serve(t)
	site.put(attestationPath, signedAttestation(t, priv, ts.URL))

	r := NewRemote().RunArtifact(ts.URL + attestationPath)
	if !r.OK() {
		t.Fatalf("a valid attestation must verify: %+v", r.Checks)
	}
	if c := checkByID(t, r, "content.artifact"); c.Outcome != OutcomePassed || !strings.Contains(c.Detail, "attestation signature verifies") {
		t.Errorf("content.artifact = %s %q", c.Outcome, c.Detail)
	}
	if c := checkByID(t, r, "content.hash"); c.Outcome != OutcomePassed {
		t.Errorf("content.hash = %s %q, want passed", c.Outcome, c.Detail)
	}
}

func TestRunArtifactFailsATamperedAttestation(t *testing.T) {
	site, priv := signedRemoteSite(t, 0)
	ts := site.serve(t)
	site.put(attestationPath, strings.Replace(signedAttestation(t, priv, ts.URL), "same-as", "integrity", 1))

	r := NewRemote().RunArtifact(ts.URL + attestationPath)
	if r.OK() {
		t.Fatal("a tampered attestation must not report OK")
	}
	if c := checkByID(t, r, "content.artifact"); c.Outcome != OutcomeFailed {
		t.Errorf("content.artifact = %s %q, want failed", c.Outcome, c.Detail)
	}
	if c := checkByID(t, r, "content.hash"); c.Outcome != OutcomeFailed {
		t.Errorf("content.hash = %s %q, want failed", c.Outcome, c.Detail)
	}
}

// spec/attestation.md §9: the key is the ISSUER's. A copy served from another
// host verifies against the issuer, never against whoever served it.
func TestRunArtifactVerifiesAnAttestationAgainstItsIssuerNotItsHost(t *testing.T) {
	issuer, issuerPriv := signedRemoteSite(t, 0)
	issuerTS := issuer.serve(t)
	host, _ := signedRemoteSite(t, 0) // a different key
	hostTS := host.serve(t)
	host.put(attestationPath, signedAttestation(t, issuerPriv, issuerTS.URL))

	r := NewRemote().RunArtifact(hostTS.URL + attestationPath)
	c := checkByID(t, r, "content.artifact")
	if c.Outcome != OutcomePassed || !strings.Contains(c.Detail, issuerTS.URL) {
		t.Errorf("content.artifact = %s %q, want passed against %s", c.Outcome, c.Detail, issuerTS.URL)
	}
}

func TestRunArtifactVerifiesATagFile(t *testing.T) {
	site, priv := signedRemoteSite(t, 0)
	ts := site.serve(t)
	tf := &tag.TagFile{
		Tag: "essays", Targets: []tag.TagTarget{{URI: ts.URL + "/content/pub.polis.core/post/20260101/p0.md"}},
		Created: "2026-01-15T10:00:00Z", Updated: "2026-01-15T10:00:00Z", Generator: "polis-cli-go/test",
	}
	canonical, _ := tag.CanonicalJSON(tf)
	tf.Version = "sha256:" + SHA256Hex(canonical)
	tf.Signature, _ = signing.SignContent(canonical, priv)
	site.put("/content/pub.polis.core/tag/essays.json", string(mustJSON(tf)))

	r := NewRemote().RunArtifact(ts.URL + "/content/pub.polis.core/tag/essays.json")
	if !r.OK() {
		t.Fatalf("a valid tag file must verify: %+v", r.Checks)
	}
	if c := checkByID(t, r, "content.hash"); c.Outcome != OutcomePassed {
		t.Errorf("content.hash = %s %q, want passed", c.Outcome, c.Detail)
	}
}

// An unsigned follow file is a legal state (Law 2) — recognised, and reported
// as unsigned rather than as "no frontmatter found".
func TestRunArtifactRecognisesAnUnsignedFollowFile(t *testing.T) {
	site, _ := signedRemoteSite(t, 0)
	ts := site.serve(t)
	site.put("/content/pub.polis.core/follow/following.json", `{"version":"polis-cli-go/test","following":[{"url":"https://bob.example"}]}`)

	r := NewRemote().RunArtifact(ts.URL + "/content/pub.polis.core/follow/following.json")
	c := checkByID(t, r, "content.artifact")
	if c.Outcome != OutcomePassed || !strings.Contains(c.Detail, "unsigned follow file") {
		t.Errorf("content.artifact = %s %q", c.Outcome, c.Detail)
	}
}

// A JSON document of no type this run knows is not reported as verified.
func TestRunArtifactFailsAnUnrecognisedJSONDocument(t *testing.T) {
	site, _ := signedRemoteSite(t, 0)
	ts := site.serve(t)
	site.put("/data.json", `{"hello":"world"}`)

	r := NewRemote().RunArtifact(ts.URL + "/data.json")
	if r.OK() {
		t.Fatal("an unrecognised JSON document must not report OK")
	}
	if c := checkByID(t, r, "content.artifact"); !strings.Contains(c.Detail, "no record type this run recognises") {
		t.Errorf("detail %q", c.Detail)
	}
}
