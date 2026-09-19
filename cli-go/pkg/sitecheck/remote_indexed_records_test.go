package sitecheck

import "testing"

// Signet epic 37 D4. Remote form used to say "a polis site publishes no index of
// these" about tags and attestations — false for any site whose index lists
// them. It now reports the count the site publishes, stays NOT CHECKED because
// nothing verifies the entries yet (epic 05), and always carries the freshness
// limit so zero is never read as "none exist".
func TestRunSite_ReportsIndexedRecordCountsWithTheirFreshnessLimit(t *testing.T) {
	site, _ := signedRemoteSite(t, 1)
	site.files["/content/pub.polis.core/index.jsonl"] +=
		`{"type":"attestation","path":"content/pub.polis.core/attestation/20260104T000000Z-abcd.json","title":"pub.polis.attestation.agent-disclosure","published":"2026-01-04T00:00:00Z","current_version":"sha256:dddd"}` + "\n"
	ts := site.serve(t)

	r := NewRemote().RunSite(ts.URL)

	att := checkByID(t, r, "content.attestations")
	if att.Outcome != OutcomeNotApplicable {
		t.Errorf("content.attestations outcome = %s, want not_applicable — the entries are listed, not verified", att.Outcome)
	}
	for _, want := range []string{"lists 1 attestation", "only as fresh as"} {
		if !contains(att.Reason, want) {
			t.Errorf("content.attestations reason = %q, want it to contain %q", att.Reason, want)
		}
	}

	tags := checkByID(t, r, "content.tags")
	for _, want := range []string{"lists no tag files", "nothing indexed, never that none exist"} {
		if !contains(tags.Reason, want) {
			t.Errorf("content.tags reason = %q, want it to contain %q", tags.Reason, want)
		}
	}

	for _, c := range r.Checks {
		if contains(c.Reason, "publishes no index") || contains(c.Detail, "publishes no index") {
			t.Errorf("%s still claims the site publishes no index: %q", c.ID, c.Reason+c.Detail)
		}
	}
}
