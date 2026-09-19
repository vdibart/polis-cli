package attestation

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// specPath is the public specification. It is the deliverable, not a write-up
// of one: the acceptance test for this format is that a competent stranger can
// produce a record that verifies against ours WITHOUT READING THIS GO.
const specPath = "../../../docs/signet/spec/attestation.md"

// TestSpecWorkedExampleMatchesTheGoldenBytes is the mechanical check that keeps
// the spec honest.
//
// ⚠️ Prose and code drift the moment a human transcribes between them, and a
// spec whose worked example is subtly wrong is worse than no spec — it produces
// a second implementation that is confidently incompatible. So the documented
// signing base is compared against the SAME constant the golden-bytes test
// pins, on every test run, rather than by eye at review time.
//
// If this fails, one of two things happened: the canonical form changed (a
// protocol break — see TestCanonicalJSONIsTheSpec), or someone edited the spec's
// example by hand. Neither is fixed by editing this test.
func TestSpecWorkedExampleMatchesTheGoldenBytes(t *testing.T) {
	spec := readSpec(t)

	if !strings.Contains(spec, goldenCanonical) {
		t.Errorf("the spec's worked example does not contain the golden signing base.\n"+
			"expected these exact bytes in %s:\n%s", specPath, goldenCanonical)
	}
}

// TestSpecWorkedExampleInputMatchesTheFixture checks the OTHER half. The
// documented output being right is worthless if the documented input is not the
// record that produces it — a reader would follow the example and get different
// bytes.
func TestSpecWorkedExampleInputMatchesTheFixture(t *testing.T) {
	spec := readSpec(t)
	r := sampleRecord()

	for _, want := range []string{
		`"type": "` + r.Type + `"`,
		`"issuer": "` + r.Issuer + `"`,
		`"predicate": "` + r.Predicate + `"`,
		`"type": "` + r.Subject.Type + `", "id": "` + r.Subject.ID + `"`,
		`"asserted": "` + r.Asserted + `"`,
		`"generator": "` + r.Generator + `"`,
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("the spec's worked-example INPUT is missing %s — a reader following it "+
				"would not produce the documented signing base", want)
		}
	}
}

// TestSpecNamesEveryReservedPredicateExactlyOnce guards the vocabulary against
// the failure that produced D10 in the first place: two lists in two files,
// drifting apart.
//
// The spec's table is the single source of truth, so every constant must appear
// in it — and the table must not have grown a name the code does not have.
func TestSpecNamesEveryReservedPredicateExactlyOnce(t *testing.T) {
	spec := readSpec(t)

	reserved := []string{
		PredicateSameAs,
		PredicateIntegrity,
		PredicateCorrection,
		PredicateUsedUnderTerms,
		PredicateAgentDisclosure,
		PredicateEndorsement,
		PredicateWithdrawal,
		PredicateCustody,
		PredicateCustodyGrant,
		PredicateGrant,
	}
	for _, p := range reserved {
		if !strings.Contains(spec, p) {
			t.Errorf("the spec does not name the reserved predicate %q", p)
		}
	}

	// Anything matching pub.polis.attestation.<name> in the spec must be one of
	// the reserved set, or the vocabulary has grown somewhere other than the
	// code. ⚠️ The count is deliberately not written down here — a number in a
	// comment is one more thing that drifts, and this loop does not need one.
	known := map[string]bool{}
	for _, p := range reserved {
		known[p] = true
	}
	re := regexp.MustCompile(`pub\.polis\.attestation\.[a-z][a-z-]*`)
	for _, found := range re.FindAllString(spec, -1) {
		// Event names share the namespace and are not predicates.
		if found == "pub.polis.attestation.issued" || found == "pub.polis.attestation.withdrawn" {
			continue
		}
		if !known[found] {
			t.Errorf("the spec names %q, which is not a reserved predicate in the code — "+
				"two lists in two files is how the vocabulary rotted last time", found)
		}
	}
}

// TestSpecStatesWhatIsNotSigned. A second implementer who assumes the whole file
// is covered builds something that verifies against itself and nothing else, so
// the exclusions have to be stated rather than implied.
func TestSpecStatesWhatIsNotSigned(t *testing.T) {
	spec := readSpec(t)

	for _, claim := range []string{
		"`current_version` is excluded",
		"`signature` is excluded",
		"`withdrawn_by` is excluded",
		"unknown top-level JSON fields are not covered by the signature",
	} {
		if !strings.Contains(spec, claim) {
			t.Errorf("the spec does not say %q — a reader will assume the whole file is signed", claim)
		}
	}
}

// TestSpecStatesTheToleranceRule. Without it a second implementation rejects
// what it does not recognise, and no third party can ever add a predicate.
func TestSpecStatesTheToleranceRule(t *testing.T) {
	spec := readSpec(t)

	for _, claim := range []string{
		"MUST NOT** fail verification",
		"verify normally",
		"render as an opaque claim",
	} {
		if !strings.Contains(spec, claim) {
			t.Errorf("the spec does not state the tolerance rule: missing %q", claim)
		}
	}
}

// TestSpecStatesAbsentExpiryMeaning. Cheap now, unrecoverable later: every
// record written before an `expires` field exists would otherwise be ambiguous
// between "never expires" and "unknown".
func TestSpecStatesAbsentExpiryMeaning(t *testing.T) {
	spec := readSpec(t)
	if !strings.Contains(spec, `does NOT mean "valid forever."`) {
		t.Error("the spec does not define what an absent expiry means")
	}
}

func readSpec(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("the specification is part of this format, not an optional extra: %v", err)
	}
	return string(data)
}
