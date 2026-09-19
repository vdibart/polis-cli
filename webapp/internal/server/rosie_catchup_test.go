package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/agent"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// Signet epic 11 D13 — the catch-up pass decides requests left pending before
// Rosie became live, within its bound, once per grant.
//
// Epic 45: every request names a real comment path, because Rosie now fetches
// and verifies the comment before deciding (D5), and carries its root_post, as
// the DS's pending rows do (D4).

// catchUpComment is a comment URL on bob's (or another author's) site.
func catchUpComment(author, id string) string {
	return fmt.Sprintf("https://%s/content/pub.polis.core/comment/20260915/%s.md", author, id)
}

func pendingRecord(id int, commentURL, actor string, created time.Time) string {
	return fmt.Sprintf(`{"id":%d,"type":"pub.polis.comment.blessing","source_url":%q,"target_url":%q,"actor":%q,"status":"pending","metadata":{"comment_version":"sha256:3333333333333333333333333333333333333333333333333333333333333333","root_post":%q},"created_at":%q}`,
		id, commentURL, rosiePostURL, actor, rosiePostURL, created.UTC().Format(time.RFC3339))
}

func registered(t *testing.T, s *Server) {
	t.Helper()
	if err := discovery.WriteRegistrationMarker(s.DataDir, s.DiscoveryURL, "test-site.polis.pub", "test"); err != nil {
		t.Fatal(err)
	}
}

func TestCatchUpDecidesRequestsLeftPendingWithinItsWindow(t *testing.T) {
	s, ds := rosieServer(t, "bless pub.polis.comment from all")
	registered(t, s)
	now := time.Now()
	recent := catchUpComment("bob.polis.pub", "recent")
	ds.pending = `{"records":[` +
		pendingRecord(1, recent, "bob.polis.pub", now.Add(-2*time.Hour)) + `,` +
		pendingRecord(2, catchUpComment("carol.polis.pub", "old"), "carol.polis.pub", now.Add(-45*24*time.Hour)) +
		`]}`

	// Requests arrived while no grant stood: nothing decided them.
	s.rosieCatchUp()
	if ds.writeCount() != 0 {
		t.Fatal("catch-up ran with no live grant")
	}

	switchRosieOnByDefault(t, s)
	live, _ := agent.LiveRosieGrant(s.DataDir)
	events := captureEvents(t, s.rosieCatchUp)

	if ds.writeCount() != 1 {
		t.Fatalf("catch-up decided %d requests, want 1 (the old one is outside the window); events: %v", ds.writeCount(), events)
	}
	w := ds.lastWrite()
	if w.SourceURL != recent || w.Agent != agent.Rosie || w.Grant != live.URL() {
		t.Fatalf("catch-up decision = %+v", w)
	}
	ev := findEvent(events, "pub.polis.agent.catch_up")
	if ev == nil || ev["decided"] != float64(1) || ev["left_pending"] != float64(1) ||
		ev["outside_window"] != float64(1) || ev["pending_seen"] != float64(2) || ev["grant_url"] != live.URL() {
		t.Fatalf("catch_up event = %v", ev)
	}

	// Once per grant: a second cycle decides nothing more.
	s.rosieCatchUp()
	if ds.writeCount() != 1 {
		t.Fatal("catch-up ran twice under the same grant")
	}
}

// Review F2: requests left because a pass hit the cap are decided by the next
// cycle, and only then is the catch-up recorded.
func TestCatchUpCapNeverStrandsRequests(t *testing.T) {
	orig := rosieCatchUpMax
	rosieCatchUpMax = 2
	t.Cleanup(func() { rosieCatchUpMax = orig })

	s, ds := rosieServer(t, "bless pub.polis.comment from all")
	registered(t, s)
	now := time.Now()
	ds.pending = `{"records":[` +
		pendingRecord(1, catchUpComment("bob.polis.pub", "1"), "bob.polis.pub", now) + `,` +
		pendingRecord(2, catchUpComment("bob.polis.pub", "2"), "bob.polis.pub", now) + `,` +
		pendingRecord(3, catchUpComment("bob.polis.pub", "3"), "bob.polis.pub", now) +
		`]}`
	switchRosieOnByDefault(t, s)
	live, _ := agent.LiveRosieGrant(s.DataDir)

	events := captureEvents(t, s.rosieCatchUp)
	if ds.writeCount() != 2 {
		t.Fatalf("first pass decided %d, want the cap (2)", ds.writeCount())
	}
	if agent.CatchUpDone(s.DataDir, live.URL()) {
		t.Fatal("a pass that stopped at the cap was recorded as done")
	}
	if ev := findEvent(events, "pub.polis.agent.catch_up"); ev == nil || ev["capped"] != float64(1) || ev["complete"] != false {
		t.Fatalf("first catch_up event = %v", ev)
	}

	s.rosieCatchUp()
	if ds.writeCount() != 3 {
		t.Fatalf("second pass brought decisions to %d, want 3", ds.writeCount())
	}
	if !agent.CatchUpDone(s.DataDir, live.URL()) {
		t.Fatal("catch-up not recorded once nothing was left by the cap")
	}

	s.rosieCatchUp()
	if ds.writeCount() != 3 {
		t.Fatal("a recorded catch-up ran again")
	}
}

func TestCatchUpLeavesARequestTheRulesHoldForReview(t *testing.T) {
	s, ds := rosieServer(t, "review pub.polis.comment from all")
	registered(t, s)
	ds.pending = `{"records":[` + pendingRecord(1, catchUpComment("bob.polis.pub", "1"), "bob.polis.pub", time.Now()) + `]}`
	switchRosieOnByDefault(t, s)
	s.rosieCatchUp()
	if ds.writeCount() != 0 {
		t.Fatal("catch-up decided a request a review rule holds")
	}
}

func TestCatchUpRunsAgainWhenTheUserSwitchesRosieBackOn(t *testing.T) {
	s, ds := rosieServer(t, "bless pub.polis.comment from all")
	registered(t, s)
	switchRosieOnByDefault(t, s)
	s.rosieCatchUp() // nothing pending yet
	postRosie(t, s, false)

	ds.pending = `{"records":[` + pendingRecord(1, catchUpComment("bob.polis.pub", "1"), "bob.polis.pub", time.Now()) + `]}`
	postRosie(t, s, true)
	s.rosieCatchUp()
	if ds.writeCount() != 1 {
		t.Fatalf("catch-up after switching back on decided %d, want 1", ds.writeCount())
	}
}
