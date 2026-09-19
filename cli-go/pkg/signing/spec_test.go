package signing_test

import (
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// specPath is the public specification of the signing base. It is THE
// DELIVERABLE of Signet epic 08, not a write-up of one: the acceptance test for
// this format is that a competent stranger can produce bytes that verify
// against ours WITHOUT READING THIS GO.
const specPath = "../../../docs/signet/spec/signing-base.md"

// specPost is the worked example in §4.4 — deliberately carrying the two things
// a naive implementation gets wrong: a BODY line beginning `signature:` and an
// INDENTED one inside the licence block.
const specPost = `---
title: Hello, World
published: 2026-08-27T14:02:00Z
generator: polis-cli-go/0.67.0
current-version: sha256:2ef7bde608ce5404e97d5f042f95f89f1c232871d1e0e6f2d3f5a2b4c6d8e0f1
version-history:
  - sha256:2ef7bde608ce5404e97d5f042f95f89f1c232871d1e0e6f2d3f5a2b4c6d8e0f1 (2026-08-27T14:02:00Z)
license:
  v: pub.polis.license.v1
  profile: pub.polis.license.reserved/1
  train-ai: n
  search: y
  terms: https://alice.example/license
  asserted: 2026-08-27T14:02:00Z
signature: U1NIU0lHAAAAAQ...
---

A body line can begin ` + "`signature:`" + ` and still verify:

signature: not-the-frontmatter-one
`

// TestSpecWorkedExampleMatchesTheImplementation is the mechanical check that
// keeps the spec honest.
//
// ⚠️ Prose and code drift the moment a human transcribes between them, and a
// spec whose worked example is subtly wrong is worse than no spec — it produces
// a second implementation that is confidently incompatible. Both halves are
// checked: the documented INPUT must be the file that produces the documented
// OUTPUT, or a reader following the example gets different bytes.
func TestSpecWorkedExampleMatchesTheImplementation(t *testing.T) {
	spec := readSigningBaseSpec(t)

	if !strings.Contains(spec, specPost) {
		t.Errorf("%s does not contain the worked example's INPUT file verbatim", specPath)
	}
	base := signing.MarkdownSigningBase(specPost, signing.TypePost)
	if !strings.Contains(spec, base) {
		t.Errorf("%s does not publish the signing base this input actually produces:\n%s", specPath, base)
	}

	// The trap the example exists to demonstrate: the frontmatter signature is
	// gone, the body one and the indented one are not.
	if strings.Contains(base, "U1NIU0lHAAAAAQ...") {
		t.Error("the frontmatter signature survived the strip")
	}
	for _, keep := range []string{"signature: not-the-frontmatter-one", "  profile: pub.polis.license.reserved/1"} {
		if !strings.Contains(base, keep) {
			t.Errorf("the example's signing base dropped %q", keep)
		}
	}
}

// TestSpecStatesWhatIsNotSigned. A second implementer who assumes the whole file
// is covered builds something that verifies against itself and nothing else, so
// the exclusions have to be stated rather than implied.
func TestSpecStatesWhatIsNotSigned(t *testing.T) {
	spec := readSigningBaseSpec(t)
	for _, claim := range []string{
		"REBUILT from named fields, never hashed off the raw file",
		"It cannot cover itself.",
		"is **unsigned**",
	} {
		if !strings.Contains(spec, claim) {
			t.Errorf("the spec does not say %q — a reader will assume the whole file is signed", claim)
		}
	}
}

// TestSpecStatesTheFieldOrderRule. This is the finding the epic turns on: the
// order is a fixed sequence and is invisible from the artifact, so a
// reimplementer who sorts keys — as every canonical-JSON convention does —
// produces different bytes and concludes our signatures are invalid.
func TestSpecStatesTheFieldOrderRule(t *testing.T) {
	spec := readSigningBaseSpec(t)
	for _, claim := range []string{
		"RFC 8785",
		"not alphabetical",
		"Field order is the fixed sequence",
	} {
		if !strings.Contains(spec, claim) {
			t.Errorf("the spec does not state the field-order rule: missing %q", claim)
		}
	}
}

// TestSpecStatesTheEscapingRule — §5.2. It has never executed in production, so
// the document is the only thing standing between a reimplementer and a wrong
// digest the first time a terms URL carries a query string.
func TestSpecStatesTheEscapingRule(t *testing.T) {
	spec := readSigningBaseSpec(t)
	for _, claim := range []string{`<`, `>`, `&`} {
		if !strings.Contains(spec, claim) {
			t.Errorf("the spec does not name the escape %s", claim)
		}
	}
}

// TestSpecStatesTheThreeOutcomes. Collapsing unsigned into invalid turns an
// ordinary state into a fleet-wide alarm, and it is the mistake a verifier
// written from a partial spec makes first.
func TestSpecStatesTheThreeOutcomes(t *testing.T) {
	spec := readSigningBaseSpec(t)
	for _, claim := range []string{
		"`unsigned` is a fact, not a failure",
		"`invalid` is evidence, not a verdict",
		"nothing backfills a signature",
	} {
		if !strings.Contains(spec, claim) {
			t.Errorf("the spec does not state %q", claim)
		}
	}
}

// TestSpecNamesTheUnsignedFieldsPerType keeps the prose table and the rule table
// in pkg/signing from drifting apart — two lists in two files is how a
// vocabulary rots.
func TestSpecNamesTheUnsignedFieldsPerType(t *testing.T) {
	spec := readSigningBaseSpec(t)
	for _, typ := range []signing.ObjectType{signing.TypePost, signing.TypeComment} {
		if !strings.Contains(spec, string(typ)) {
			t.Errorf("the spec does not name the object type %q", typ)
		}
		for _, f := range signing.UnsignedFrontmatterFields(typ) {
			if !strings.Contains(spec, "`"+f+"`") {
				t.Errorf("the spec does not name %q as excluded from %s's signing base", f, typ)
			}
		}
	}
	// The asymmetry a reader must not miss.
	if !strings.Contains(spec, "a post's `author:` line, if one exists, is inside the\nsignature") {
		t.Error("the spec does not say a POST's author line is inside its signature")
	}
}

// TestSpecStatesTheVersionRule — v1 is unmarked, and an unknown future version
// is uncheckable rather than invalid (Law 2: the verifier does not hold the
// judgment).
func TestSpecStatesTheVersionRule(t *testing.T) {
	spec := readSigningBaseSpec(t)
	if !strings.Contains(spec, signing.BaseVersion) {
		t.Errorf("the spec does not name the version %q", signing.BaseVersion)
	}
	for _, claim := range []string{
		"No marker ⇒",
		"v1 is never retired",
	} {
		if !strings.Contains(spec, claim) {
			t.Errorf("the spec does not state %q", claim)
		}
	}
}

func readSigningBaseSpec(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("the specification is part of this format, not an optional extra: %v", err)
	}
	return string(data)
}
