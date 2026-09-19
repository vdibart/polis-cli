package attestation

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func integrityRecord(payload map[string]string) *Record {
	return &Record{
		Issuer:    "https://judge.example",
		Predicate: PredicateIntegrity,
		Subject:   Subject{Type: SubjectIdentity, ID: "https://alice.example"},
		Payload:   payload,
	}
}

func wellFormedIntegrityPayload() map[string]string {
	return map[string]string{
		IntegrityKeyResult:   IntegrityNotVerified,
		IntegrityKeyVantage:  "operator fleet, loopback",
		IntegrityKeyObserved: "2026-09-14T12:00:00Z",
	}
}

func TestIssue_IntegrityObservationWellFormed(t *testing.T) {
	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, result := range []string{IntegrityVerified, IntegrityNotVerified} {
		p := wellFormedIntegrityPayload()
		p[IntegrityKeyResult] = result
		if _, err := Issue(dir, integrityRecord(p), priv); err != nil {
			t.Errorf("result=%s refused: %v", result, err)
		}
	}
}

// ⛔ Spec §4.1: `result` has NO DEFAULT, and vantage and observed are required.
// A record missing any of them must be refused rather than written — least of
// all written as though it said "verified".
func TestIssue_IntegrityObservationRefusesAnIncompleteClaim(t *testing.T) {
	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(p map[string]string) *Record{
		"no result":      func(p map[string]string) *Record { delete(p, IntegrityKeyResult); return integrityRecord(p) },
		"unknown result": func(p map[string]string) *Record { p[IntegrityKeyResult] = "pass"; return integrityRecord(p) },
		"no vantage":     func(p map[string]string) *Record { delete(p, IntegrityKeyVantage); return integrityRecord(p) },
		"no observed":    func(p map[string]string) *Record { delete(p, IntegrityKeyObserved); return integrityRecord(p) },
		"observed not UTC": func(p map[string]string) *Record {
			p[IntegrityKeyObserved] = "2026-09-14T12:00:00+02:00"
			return integrityRecord(p)
		},
		"no payload at all": func(map[string]string) *Record { return integrityRecord(nil) },
		"uri subject": func(p map[string]string) *Record {
			r := integrityRecord(p)
			r.Subject = Subject{Type: SubjectURI, ID: "https://alice.example/post.md"}
			return r
		},
	}
	for name, build := range cases {
		dir := t.TempDir()
		_, err := Issue(dir, build(wellFormedIntegrityPayload()), priv)
		if err == nil {
			t.Errorf("%s: Issue accepted it", name)
			continue
		}
		if recs, _ := List(dir); len(recs) != 0 {
			t.Errorf("%s: refused, but %d record(s) were written", name, len(recs))
		}
	}
}

// The rule is a WRITER rule. A reader still loads and verifies an integrity
// record that lacks the payload — tolerance (§7) is not suspended for it.
func TestIntegrityObservation_ReaderStillVerifiesAMalformedRecord(t *testing.T) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	r := integrityRecord(nil)
	r.Type = TypeName
	r.Asserted = "2026-09-14T12:00:00Z"
	r.Generator = GetGenerator()
	if err := signAndStamp(r, priv); err != nil {
		t.Fatal(err)
	}
	if status, err := Verify(r, pub); status != StatusValid {
		t.Errorf("status = %s (%v); a reader must still verify a record it cannot interpret", status, err)
	}
}

func TestIntegrityObservation_HelpNamesTheRequiredKeys(t *testing.T) {
	err := checkIntegrityObservation(integrityRecord(nil))
	if err == nil || !strings.Contains(err.Error(), "no default") {
		t.Errorf("error = %v; it must say there is no default", err)
	}
}
