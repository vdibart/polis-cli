package following

import "testing"

// TestFollowsNeedingReannounce locks the net-based reconcile decision: a target
// is re-announced iff the user follows it locally AND its DS net (announced -
// removed) is below 1. This is the upstream guard for the "dangling removed"
// drift — a target announced then removed then re-followed nets to 0, so it must
// be re-announced to bring the DS back up to the local following.json. Mirrors
// the hosted Chaplain's reannounceTargets; keep the two in step.
func TestFollowsNeedingReannounce(t *testing.T) {
	entries := []FollowingEntry{
		{URL: "https://alice.polis.pub/"}, // net 1 — edge present, skip
		{URL: "https://bob.polis.pub/"},   // net 0 — never announced, re-announce
		{URL: "https://carol.polis.pub/"}, // net 0 — dangling removed, re-announce
		{URL: "https://dave.polis.pub/"},  // net 2 — skip
		{URL: "https://eve.polis.pub/"},   // net -1 — double removed, re-announce
		{URL: ""},                         // unparseable → skip
	}
	net := map[string]int{
		"alice.polis.pub": 1,
		"bob.polis.pub":   0,
		"carol.polis.pub": 0,
		"dave.polis.pub":  2,
		"eve.polis.pub":   -1,
	}
	got := map[string]bool{}
	for _, d := range followsNeedingReannounce(entries, net) {
		got[d] = true
	}
	for d, want := range map[string]bool{
		"alice.polis.pub": false,
		"bob.polis.pub":   true,
		"carol.polis.pub": true,
		"dave.polis.pub":  false,
		"eve.polis.pub":   true,
	} {
		if got[d] != want {
			t.Errorf("target %s (net=%d): reannounce=%v want %v", d, net[d], got[d], want)
		}
	}
	if len(got) != 3 {
		t.Errorf("expected exactly 3 re-announce targets, got %d: %v", len(got), got)
	}
}
