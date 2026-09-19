package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/agent"
	"github.com/vdibart/polis-cli/cli-go/pkg/cache"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/verify"
)

// Signet epic 45 — what Rosie does now that the discovery service decides
// nothing: `thread-blessed` resolved from the owner's own records (D4), and the
// comment fetched and verified before any rule is applied (D5). Both live in
// rosieEvaluate, so each is asserted on the stream path AND the catch-up path.

// writeRules replaces the site's public rules.
func writeRules(t *testing.T, s *Server, rules []string) {
	t.Helper()
	_, pub := policy.DefaultPaths(s.DataDir)
	var b strings.Builder
	b.WriteString(`{"version":2,"generator":"test"}` + "\n")
	for _, r := range rules {
		fmt.Fprintf(&b, `{"active":true,"policy":%q}`+"\n", r)
	}
	if err := os.WriteFile(pub, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}
}

type parityComment struct {
	CommentURL string `json:"comment_url"`
	InReplyTo  string `json:"in_reply_to"`
	RootPost   string `json:"root_post"`
}

type parityCase struct {
	parityComment
	Name          string          `json:"name"`
	CommentAuthor string          `json:"comment_author"`
	PriorBlessed  []parityComment `json:"prior_blessed"`
	Expect        string          `json:"expect"`
}

type parityFixture struct {
	PostAuthor string       `json:"post_author"`
	Rules      []string     `json:"rules"`
	Cases      []parityCase `json:"cases"`
}

// loadThreadParity reads the fixture the DS test reads, with the post author's
// domain moved onto this test server's own.
func loadThreadParity(t *testing.T) parityFixture {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "discovery-service", "core", "contract-fixtures", "thread-blessed-parity.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if _, derr := os.Stat(filepath.Join(filepath.Dir(file), "..", "..", "..", "discovery-service")); os.IsNotExist(derr) {
			t.Skip("the shared contract fixtures live with the discovery service source, which is not in this repository")
		}
		t.Fatal(err)
	}
	var f parityFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	moved := strings.ReplaceAll(string(data), f.PostAuthor, "test-site.polis.pub")
	if err := json.Unmarshal([]byte(moved), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 4 {
		t.Fatalf("fixture has %d cases", len(f.Cases))
	}
	return f
}

// paritySite is a Rosie-on site holding the case's prior blessings, written the
// way every blessed.json writer keys them.
func paritySite(t *testing.T, f parityFixture, c parityCase) (*Server, *fakeDS) {
	t.Helper()
	s, ds := rosieServer(t, "review pub.polis.comment from all")
	writeRules(t, s, f.Rules)
	for _, p := range c.PriorBlessed {
		if err := metadata.AddBlessedComment(s.DataDir, extractPostPathFromURL(p.InReplyTo), metadata.BlessedComment{URL: p.CommentURL}); err != nil {
			t.Fatal(err)
		}
	}
	switchRosieOnByDefault(t, s)
	return s, ds
}

func requestedFor(c parityCase) discovery.StreamEvent {
	return discovery.StreamEvent{
		Type:  "pub.polis.comment.blessing.requested",
		Actor: c.CommentAuthor,
		Payload: map[string]interface{}{
			"target_domain":   "test-site.polis.pub",
			"comment_url":     c.CommentURL,
			"in_reply_to":     c.InReplyTo,
			"root_post":       c.RootPost,
			"comment_version": "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		},
	}
}

func assertParity(t *testing.T, c parityCase, ds *fakeDS, events []map[string]interface{}) {
	t.Helper()
	switch c.Expect {
	case "bless":
		if ds.writeCount() != 1 || ds.lastWrite().Action != "grant" || ds.lastWrite().SourceURL != c.CommentURL {
			t.Fatalf("%s: want a grant, got %d writes; events %v", c.Name, ds.writeCount(), events)
		}
		ev := findEvent(events, "pub.polis.comment.blessing.auto_grant")
		if ev == nil || ev["rule"] == "" || ev["rule"] == nil {
			t.Fatalf("%s: auto_grant = %v", c.Name, ev)
		}
		if strings.Contains(ev["rule"].(string), "thread-blessed") && ev["thread_blessed_resolved"] != true {
			t.Fatalf("%s: a thread-blessed grant reports thread_blessed_resolved = %v", c.Name, ev["thread_blessed_resolved"])
		}
	case "review":
		if ds.writeCount() != 0 {
			t.Fatalf("%s: want it left for review, got %d writes", c.Name, ds.writeCount())
		}
	default:
		t.Fatalf("%s: unknown expectation %q", c.Name, c.Expect)
	}
}

// D4 — the fixture that blessed under the DS's evaluation blesses under Rosie's.
func TestThreadBlessedParityOnTheStreamPath(t *testing.T) {
	f := loadThreadParity(t)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			s, ds := paritySite(t, f, c)
			events := captureEvents(t, func() {
				(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{requestedFor(c)})
			})
			assertParity(t, c, ds, events)
		})
	}
}

func TestThreadBlessedParityOnTheCatchUpPath(t *testing.T) {
	f := loadThreadParity(t)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			s, ds := paritySite(t, f, c)
			registered(t, s)
			ds.pending = fmt.Sprintf(`{"records":[{"id":1,"type":"pub.polis.comment.blessing","source_url":%q,"target_url":%q,"actor":%q,"status":"pending","metadata":{"root_post":%q},"created_at":%q}]}`,
				c.CommentURL, c.InReplyTo, c.CommentAuthor, c.RootPost, time.Now().UTC().Format(time.RFC3339))
			events := captureEvents(t, s.rosieCatchUp)
			assertParity(t, c, ds, events)
		})
	}
}

// A comment Rosie blesses is on the thread for the next one — her own record is
// what the next decision reads, with no discovery service involved.
func TestACommentRosieBlessedMakesItsAuthorThreadBlessed(t *testing.T) {
	s, ds := rosieServer(t, "review pub.polis.comment from all")
	writeRules(t, s, []string{
		"bless pub.polis.comment from following",
		"bless pub.polis.comment from thread-blessed",
		"review pub.polis.comment from all",
	})
	switchRosieOnByDefault(t, s)
	first := requestedEvent()
	first.Payload["root_post"] = rosiePostURL
	(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{first})
	if ds.writeCount() != 0 {
		t.Fatal("bob is not followed; his first comment must wait")
	}

	// The owner blesses it by hand; bob's next comment on the thread is Rosie's.
	if err := metadata.AddBlessedComment(s.DataDir, extractPostPathFromURL(rosiePostURL), metadata.BlessedComment{URL: rosieCommentURL}); err != nil {
		t.Fatal(err)
	}
	second := requestedEvent()
	second.Payload["comment_url"] = "https://bob.polis.pub/content/pub.polis.core/comment/20260915/second.md"
	second.Payload["root_post"] = rosiePostURL
	events := captureEvents(t, func() {
		(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{second})
	})
	if ds.writeCount() != 1 {
		t.Fatalf("bob's second comment was not blessed; events %v", events)
	}
	if ev := findEvent(events, "pub.polis.comment.blessing.auto_grant"); ev == nil || ev["thread_blessed_resolved"] != true {
		t.Fatalf("auto_grant = %v", ev)
	}
}

// ============================================================================
// D5 — fetch and verify before deciding; the version comes from the bytes
// ============================================================================

func TestAFetchOrVerifyFailureLeavesTheRequestPending(t *testing.T) {
	for _, tc := range []struct {
		name   string
		serve  func(string) (*verify.VerificationResult, error)
		reason string
	}{
		{"unreachable", func(string) (*verify.VerificationResult, error) { return nil, errors.New("connection refused") }, "fetch_failed"},
		{"invalid signature", func(u string) (*verify.VerificationResult, error) {
			return &verify.VerificationResult{URL: u, CurrentVersion: rosieVerifiedVersion, Signature: verify.SignatureResult{Status: "invalid"}}, nil
		}, "signature_invalid"},
		{"unsigned", func(u string) (*verify.VerificationResult, error) {
			return &verify.VerificationResult{URL: u, CurrentVersion: rosieVerifiedVersion, Signature: verify.SignatureResult{Status: "missing"}}, nil
		}, "signature_missing"},
		{"key could not be checked", func(u string) (*verify.VerificationResult, error) {
			return &verify.VerificationResult{URL: u, CurrentVersion: rosieVerifiedVersion, Signature: verify.SignatureResult{Status: "error"}}, nil
		}, "signature_error"},
	} {
		t.Run(tc.name+"/stream", func(t *testing.T) {
			s, ds := rosieServer(t, "bless pub.polis.comment from all")
			switchRosieOnByDefault(t, s)
			serveComments(t, tc.serve)
			events := captureEvents(t, func() {
				(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{requestedEvent()})
			})
			assertSkipped(t, ds, events, tc.reason)
		})
		t.Run(tc.name+"/catch-up", func(t *testing.T) {
			s, ds := rosieServer(t, "bless pub.polis.comment from all")
			registered(t, s)
			ds.pending = `{"records":[` + pendingRecord(1, rosieCommentURL, "bob.polis.pub", time.Now()) + `]}`
			switchRosieOnByDefault(t, s)
			serveComments(t, tc.serve)
			events := captureEvents(t, s.rosieCatchUp)
			assertSkipped(t, ds, events, tc.reason)
		})
	}
}

func assertSkipped(t *testing.T, ds *fakeDS, events []map[string]interface{}, reason string) {
	t.Helper()
	if ds.writeCount() != 0 {
		t.Fatalf("a comment that did not verify was decided (%d writes)", ds.writeCount())
	}
	ev := findEvent(events, "pub.polis.agent.decision_skipped")
	if ev == nil || ev["reason"] != reason || ev["agent"] != agent.Rosie || ev["comment_url"] != rosieCommentURL {
		t.Fatalf("decision_skipped = %v, want reason %s", ev, reason)
	}
	if findEvent(events, "pub.polis.policy.decision") != nil {
		t.Fatal("the rules were applied to a comment that did not verify")
	}
}

func TestTheBlessedVersionIsTheVerifiedBytesNeverTheRequested(t *testing.T) {
	check := func(t *testing.T, s *Server, ds *fakeDS, events []map[string]interface{}) {
		t.Helper()
		if ds.writeCount() != 1 {
			t.Fatalf("writes = %d; events %v", ds.writeCount(), events)
		}
		bc, err := metadata.LoadBlessedComments(s.DataDir)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		for _, pc := range bc.Comments {
			for _, e := range pc.Blessed {
				if e.URL == rosieCommentURL {
					got = e.Version
				}
			}
		}
		if got != rosieVerifiedVersion {
			t.Fatalf("blessed.json version = %q, want the verified %q", got, rosieVerifiedVersion)
		}
		if ev := findEvent(events, "pub.polis.comment.blessing.auto_grant"); ev == nil || ev["comment_version"] != rosieVerifiedVersion {
			t.Fatalf("auto_grant = %v", ev)
		}
		if ev := findEvent(events, "pub.polis.agent.version_differs"); ev == nil || ev["verified_version"] != rosieVerifiedVersion {
			t.Fatalf("version_differs = %v", ev)
		}
		// The bytes she verified are cached, pinned to that version.
		_, sc, ok := cache.FindBlessed(s.DataDir, rosieCommentURL)
		if !ok || sc.BlessedVersionHash != rosieVerifiedVersion {
			t.Fatalf("blessed cache = %v %+v", ok, sc)
		}
	}

	t.Run("stream", func(t *testing.T) {
		s, ds := rosieServer(t, "bless pub.polis.comment from all")
		switchRosieOnByDefault(t, s)
		events := captureEvents(t, func() {
			(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{requestedEvent()})
		})
		check(t, s, ds, events)
	})
	t.Run("catch-up", func(t *testing.T) {
		s, ds := rosieServer(t, "bless pub.polis.comment from all")
		registered(t, s)
		ds.pending = `{"records":[` + pendingRecord(1, rosieCommentURL, "bob.polis.pub", time.Now()) + `]}`
		switchRosieOnByDefault(t, s)
		events := captureEvents(t, s.rosieCatchUp)
		check(t, s, ds, events)
	})
}

// Review of E2, case 3: a withdrawn blessing leaves the owner's list even when
// the comment was never cached, so its author stops being thread-blessed. The
// un-granting reaches the owner's own sync; no discovery service is asked.
func TestAWithdrawnBlessingOfAnUncachedCommentEndsThreadBlessed(t *testing.T) {
	for _, evtType := range []string{"pub.polis.comment.blessing.denied", "pub.polis.comment.unpublished"} {
		t.Run(evtType, func(t *testing.T) {
			s, ds := rosieServer(t, "review pub.polis.comment from all")
			writeRules(t, s, []string{
				"bless pub.polis.comment from thread-blessed",
				"review pub.polis.comment from all",
			})
			switchRosieOnByDefault(t, s)
			if err := metadata.AddBlessedComment(s.DataDir, extractPostPathFromURL(rosiePostURL), metadata.BlessedComment{URL: rosieCommentURL}); err != nil {
				t.Fatal(err)
			}
			if _, _, ok := cache.FindBlessed(s.DataDir, rosieCommentURL); ok {
				t.Fatal("fixture: the comment must be uncached")
			}
			if !threadBlessedDomains(s.DataDir, rosiePostURL)["bob.polis.pub"] {
				t.Fatal("fixture: bob must start thread-blessed")
			}

			(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{{
				Type:  evtType,
				Actor: "test-site.polis.pub",
				Payload: map[string]interface{}{
					"source_url":    rosieCommentURL,
					"url":           rosieCommentURL,
					"target_url":    rosiePostURL,
					"target_domain": "test-site.polis.pub",
				},
			}})

			if blessedListHas(s.DataDir, rosieCommentURL) {
				t.Fatal("the withdrawn blessing is still in blessed.json")
			}
			if threadBlessedDomains(s.DataDir, rosiePostURL)["bob.polis.pub"] {
				t.Fatal("bob is still thread-blessed after the blessing was withdrawn")
			}
			// And the next comment from bob on the thread waits for the owner.
			next := requestedEvent()
			next.Payload["comment_url"] = "https://bob.polis.pub/content/pub.polis.core/comment/20260915/after.md"
			next.Payload["root_post"] = rosiePostURL
			(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{next})
			if ds.writeCount() != 0 {
				t.Fatalf("bob's next comment was decided (%d writes)", ds.writeCount())
			}
		})
	}
}

// A withdrawal for a comment the owner never blessed rewrites nothing — an
// unsigned rewrite would clear the list's signature.
func TestAWithdrawalForAnUnlistedCommentLeavesTheListUntouched(t *testing.T) {
	s, _ := rosieServer(t, "review pub.polis.comment from all")
	if err := metadata.AddBlessedCommentSigned(s.DataDir, extractPostPathFromURL(rosiePostURL), metadata.BlessedComment{URL: rosieCommentURL}, s.PrivateKey); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(metadata.BlessedPath(s.DataDir))
	(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{{
		Type: "pub.polis.comment.blessing.denied",
		Payload: map[string]interface{}{
			"source_url":    "https://carol.polis.pub/content/pub.polis.core/comment/20260915/never.md",
			"target_url":    rosiePostURL,
			"target_domain": "test-site.polis.pub",
		},
	}})
	if after, _ := os.ReadFile(metadata.BlessedPath(s.DataDir)); string(after) != string(before) {
		t.Fatal("blessed.json was rewritten for a comment it never listed")
	}
}
