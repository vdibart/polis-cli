package sitecheck

import (
	"strings"
	"testing"
)

// A witness that binds `version` covers whatever that version hashes, and the
// two signing families hash different things: a post or comment's version is
// its body alone, a JSON record's is the whole signed record. The sentence a
// person reads must say which, or an attestation reads as only partly covered.
func TestWitnessDetailSaysWhatTheVersionCovers(t *testing.T) {
	w := &WitnessResult{State: WitnessWitnessed, EarliestWitnessedAt: "2026-09-13T12:00:01.500Z", EarliestBinds: "version"}

	markdown := w.DescribeFor(VersionHashesBody)
	if !strings.Contains(markdown, "the body, not the frontmatter") {
		t.Errorf("a post or comment's witness covers the body, not the frontmatter: %q", markdown)
	}
	if w.Describe() != markdown {
		t.Errorf("Describe is the post-and-comment wording: %q", w.Describe())
	}

	record := w.DescribeFor(VersionHashesRecord)
	if !strings.Contains(record, "the whole signed record") {
		t.Errorf("an attestation's witness covers the whole signed record: %q", record)
	}
	if strings.Contains(record, "frontmatter") || strings.Contains(record, "body") {
		t.Errorf("a JSON record has no body or frontmatter to speak of: %q", record)
	}

	// A witness that binds the whole artifact says so whatever the family.
	whole := &WitnessResult{State: WitnessWitnessed, EarliestWitnessedAt: "2026-09-13T12:00:01.500Z", EarliestBinds: "artifact"}
	for _, s := range []VersionScope{VersionHashesBody, VersionHashesRecord} {
		if d := whole.DescribeFor(s); !strings.Contains(d, "the whole signed artifact") {
			t.Errorf("%s: an artifact-hash witness covers the whole signed artifact: %q", s, d)
		}
	}
}
