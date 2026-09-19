package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
)

func captureAttestUsage(t *testing.T) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	printAttestUsage()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

func TestParseAttestIssueFlags(t *testing.T) {
	f, err := parseAttestIssueFlags([]string{
		"--predicate", attestation.PredicateCorrection,
		"--subject", "https://site.example/posts/x.md",
		"--subject-type", "uri",
		"--subject-version", "sha256:" + strings.Repeat("9", 64),
		"--payload", "note=the figure was revised",
		"--payload", "source=https://source.example",
		"--asserted", "2026-08-28T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.predicate != attestation.PredicateCorrection {
		t.Errorf("predicate = %q", f.predicate)
	}
	if f.subject != "https://site.example/posts/x.md" {
		t.Errorf("subject = %q", f.subject)
	}
	if f.subjectType != "uri" {
		t.Errorf("subject type = %q", f.subjectType)
	}
	if f.payload["note"] != "the figure was revised" {
		t.Errorf("payload note = %q — a value with spaces must survive", f.payload["note"])
	}
	if len(f.payload) != 2 {
		t.Errorf("payload has %d keys, want 2 — --payload must be repeatable", len(f.payload))
	}
	if f.asserted != "2026-08-28T00:00:00Z" {
		t.Errorf("asserted = %q", f.asserted)
	}
}

// TestParseAttestIssueFlags_PayloadValueMayContainEquals — a URL with a query
// string is an ordinary payload value, so only the FIRST "=" separates.
func TestParseAttestIssueFlags_PayloadValueMayContainEquals(t *testing.T) {
	f, err := parseAttestIssueFlags([]string{
		"--predicate", attestation.PredicateUsedUnderTerms,
		"--subject", "https://site.example/posts/x.md",
		"--payload", "terms=https://site.example/license?v=1",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.payload["terms"] != "https://site.example/license?v=1" {
		t.Errorf("payload terms = %q", f.payload["terms"])
	}
}

func TestParseAttestIssueFlags_Errors(t *testing.T) {
	cases := map[string][]string{
		"missing predicate": {"--subject", "https://a.example"},
		"missing subject":   {"--predicate", attestation.PredicateSameAs},
		"dangling value":    {"--predicate"},
		"unknown option":    {"--predicate", "a.b", "--subject", "https://a.example", "--nope"},
		"payload not k=v":   {"--predicate", "a.b", "--subject", "https://a.example", "--payload", "bare"},
	}
	for name, args := range cases {
		if _, err := parseAttestIssueFlags(args); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// TestAttestIssueDefaultsToURISubject — most subjects are works, and a person
// who forgets --subject-type should get the common case rather than a failure.
func TestAttestIssueDefaultsToURISubject(t *testing.T) {
	f, err := parseAttestIssueFlags([]string{
		"--predicate", attestation.PredicateCorrection,
		"--subject", "https://site.example/posts/x.md",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.subjectType != attestation.SubjectURI {
		t.Errorf("default subject type = %q, want %q", f.subjectType, attestation.SubjectURI)
	}
}

// TestAttestHelpNamesTheReservedPredicates — the reserved list is only useful
// if a user can find it, and the full form is what goes in a file.
func TestAttestHelpNamesTheReservedPredicates(t *testing.T) {
	out := captureAttestUsage(t)
	for _, p := range []string{
		attestation.PredicateSameAs,
		attestation.PredicateIntegrity,
		attestation.PredicateCorrection,
		attestation.PredicateUsedUnderTerms,
		attestation.PredicateAgentDisclosure,
		attestation.PredicateEndorsement,
	} {
		if !strings.Contains(out, p) {
			t.Errorf("attest help does not name %q", p)
		}
	}
	if strings.Contains(out, "attest delete") || strings.Contains(out, "attest remove") {
		t.Error("attest must not offer a delete — withdrawal is a signed record, never an rm")
	}
}
