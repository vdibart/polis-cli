package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/agent"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/verify"
)

// Signet epic 11 — Rosie in the user's own server sync.

// fakeDS records every relationship write and answers pending queries.
type fakeDS struct {
	srv     *httptest.Server
	mu      sync.Mutex
	writes  []discovery.RelationshipUpdateRequest
	pending string // JSON body for GET /v1/relationships
}

func newFakeDS(t *testing.T) *fakeDS {
	t.Helper()
	d := &fakeDS{pending: `{"records":[]}`}
	// A real DS signs its query responses and every client verifies them, so the
	// fake does too.
	dsPriv, dsPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if r.URL.Path == "/.well-known/polis" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"public_key": string(dsPub), "key_id": "test-ds"})
			return
		}
		if r.URL.Path == "/v1/relationships" && r.Method == http.MethodGet {
			var body map[string]interface{}
			if err := json.Unmarshal([]byte(d.pending), &body); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// Like the real DS: a decided request is no longer pending.
			decided := map[string]bool{}
			for _, wr := range d.writes {
				decided[wr.SourceURL] = true
			}
			if recs, ok := body["records"].([]interface{}); ok {
				kept := []interface{}{}
				for _, rec := range recs {
					if m, ok := rec.(map[string]interface{}); ok && decided[fmt.Sprint(m["source_url"])] {
						continue
					}
					kept = append(kept, rec)
				}
				body["records"] = kept
			}
			sig, err := signing.SignContent([]byte(discovery.BuildCanonicalJSON(body)), dsPriv)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			body["ds_signature"] = sig
			body["ds_key_id"] = "test-ds"
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(body)
			return
		}
		if r.URL.Path == "/v1/relationships" && r.Method == http.MethodPost {
			var req discovery.RelationshipUpdateRequest
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &req)
			d.writes = append(d.writes, req)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *fakeDS) writeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.writes)
}

func (d *fakeDS) lastWrite() discovery.RelationshipUpdateRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.writes[len(d.writes)-1]
}

// rosieServer is a server configured the way a hosted tenant's is — key in
// memory, discovery configured, the hosted egress guard on — with a policy that
// makes a decision for every comment.
func rosieServer(t *testing.T, rule string) (*Server, *fakeDS) {
	t.Helper()
	s := newConfiguredServer(t)
	s.RestrictDMEgress = true // the hosted entry point's setting
	ds := newFakeDS(t)
	s.DiscoveryURL = ds.srv.URL
	s.Logger = NewLogger(LogLevelBasic, t.TempDir())
	s.Logger.jsonOutput = true

	_, pub := policy.DefaultPaths(s.DataDir)
	if err := os.MkdirAll(filepath.Dir(pub), 0755); err != nil {
		t.Fatal(err)
	}
	content := `{"version":2,"generator":"test"}` + "\n" + `{"active":true,"policy":"` + rule + `"}` + "\n"
	if err := os.WriteFile(pub, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	serveVerifiedComments(t, rosieVerifiedVersion)
	return s, ds
}

// rosieVerifiedVersion is the version of the bytes Rosie fetches and verifies —
// deliberately NOT any version a request names (epic 45 D5).
const rosieVerifiedVersion = "sha256:9999999999999999999999999999999999999999999999999999999999999999"

// serveVerifiedComments makes every comment Rosie fetches answer as validly
// signed at version, without the network (epic 45 D5).
func serveVerifiedComments(t *testing.T, version string) {
	t.Helper()
	serveComments(t, func(url string) (*verify.VerificationResult, error) {
		return &verify.VerificationResult{
			URL: url, CurrentVersion: version, Body: "a comment",
			Signature: verify.SignatureResult{Status: "valid"},
		}, nil
	})
}

func serveComments(t *testing.T, fn func(url string) (*verify.VerificationResult, error)) {
	t.Helper()
	orig := rosieVerifyContent
	rosieVerifyContent = fn
	t.Cleanup(func() { rosieVerifyContent = orig })
}

func switchRosieOnByDefault(t *testing.T, s *Server) {
	t.Helper()
	if outcome, _, err := agent.IssueDefault(s.rosieConfig()); err != nil || outcome != agent.DefaultIssued {
		t.Fatalf("IssueDefault = %s, %v", outcome, err)
	}
}

const (
	rosieCommentURL = "https://bob.polis.pub/content/pub.polis.core/comment/20260915/re.md"
	rosiePostURL    = "https://test-site.polis.pub/posts/20260915/hello.md"
)

func requestedEvent() discovery.StreamEvent {
	return discovery.StreamEvent{
		Type:  "pub.polis.comment.blessing.requested",
		Actor: "bob.polis.pub",
		Payload: map[string]interface{}{
			"target_domain":   "test-site.polis.pub",
			"comment_url":     rosieCommentURL,
			"in_reply_to":     rosiePostURL,
			"comment_version": "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		},
	}
}

// captureEvents runs fn with stdout captured and returns the JSON log lines.
func captureEvents(t *testing.T, fn func()) []map[string]interface{} {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()
	fn()
	os.Stdout = orig
	w.Close()
	out := <-done

	var events []map[string]interface{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]interface{}
		if json.Unmarshal(sc.Bytes(), &m) == nil && m["action"] != nil {
			events = append(events, m)
		}
	}
	return events
}

func findEvent(events []map[string]interface{}, action string) map[string]interface{} {
	for _, e := range events {
		if e["action"] == action {
			return e
		}
	}
	return nil
}

func TestRosieWithNoLiveGrantMakesNoAutoDecision(t *testing.T) {
	s, ds := rosieServer(t, "bless pub.polis.comment from all")
	events := captureEvents(t, func() {
		(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{requestedEvent()})
	})
	if n := ds.writeCount(); n != 0 {
		t.Fatalf("%d relationship requests sent with no live grant", n)
	}
	if findEvent(events, "pub.polis.comment.blessing.auto_grant") != nil {
		t.Fatal("auto_grant emitted with no live grant")
	}
	// No grant is an ordinary state, not a refusal: nothing reached the signer.
	if findEvent(events, "pub.polis.security.agent_act_without_grant") != nil {
		t.Fatal("the security event fired for an ordinary no-grant state")
	}
}

func TestRosieAutoGrantIsSignedWithTheUsersKeyAndMarkedInBothPlaces(t *testing.T) {
	s, ds := rosieServer(t, "bless pub.polis.comment from all")
	switchRosieOnByDefault(t, s)
	live, _ := agent.LiveRosieGrant(s.DataDir)

	events := captureEvents(t, func() {
		(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{requestedEvent()})
	})

	if ds.writeCount() != 1 {
		t.Fatalf("relationship writes = %d, want 1", ds.writeCount())
	}
	req := ds.lastWrite()
	if req.Action != "grant" || req.Agent != agent.Rosie || req.Grant != live.URL() {
		t.Fatalf("relationship request = %+v", req)
	}

	bc, err := metadata.LoadBlessedComments(s.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	var entry *metadata.BlessedComment
	for _, pc := range bc.Comments {
		for i := range pc.Blessed {
			if pc.Blessed[i].URL == rosieCommentURL {
				entry = &pc.Blessed[i]
			}
		}
	}
	if entry == nil || entry.Agent != agent.Rosie || entry.Grant != live.URL() {
		t.Fatalf("blessed.json entry = %+v", entry)
	}
	if st, verr := metadata.VerifyBlessedSite(s.DataDir); st != metadata.StatusValid {
		t.Fatalf("blessed.json status = %s (%v), want valid", st, verr)
	}

	ev := findEvent(events, "pub.polis.comment.blessing.auto_grant")
	if ev == nil || ev["agent"] != agent.Rosie || ev["grant_url"] != live.URL() {
		t.Fatalf("auto_grant event = %v", ev)
	}
}

func TestRosieAutoDenyIsMarked(t *testing.T) {
	s, ds := rosieServer(t, "deny pub.polis.comment from all")
	switchRosieOnByDefault(t, s)
	live, _ := agent.LiveRosieGrant(s.DataDir)
	events := captureEvents(t, func() {
		(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{requestedEvent()})
	})
	if ds.writeCount() != 1 || ds.lastWrite().Action != "deny" || ds.lastWrite().Grant != live.URL() {
		t.Fatalf("deny request = %+v (writes %d)", ds.writes, ds.writeCount())
	}
	if ev := findEvent(events, "pub.polis.comment.blessing.auto_deny"); ev == nil || ev["grant_url"] != live.URL() {
		t.Fatalf("auto_deny event = %v", ev)
	}
}

func TestRosieReviewRuleLeavesTheRequestWaiting(t *testing.T) {
	s, ds := rosieServer(t, "review pub.polis.comment from all")
	switchRosieOnByDefault(t, s)
	(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{requestedEvent()})
	if ds.writeCount() != 0 {
		t.Fatal("a review rule was decided")
	}
}

// ⛔ THE TRAP, on the hosted path where the key is in memory: reaching the
// signing call without a live grant refuses, reports on the security stream,
// and signs nothing — no fallback to an unmarked signature.
func TestReachingTheSigningCallWithoutAGrantSignsNothing(t *testing.T) {
	s, ds := rosieServer(t, "bless pub.polis.comment from all")
	if s.PrivateKey == nil || !s.RestrictDMEgress {
		t.Fatal("fixture is not the hosted shape")
	}
	before, _ := os.ReadFile(metadata.BlessedPath(s.DataDir))
	req := rosieRequest{CommentURL: rosieCommentURL, InReplyTo: rosiePostURL, TargetDomain: "test-site.polis.pub", Actor: "bob.polis.pub"}
	client := discovery.NewClient(ds.srv.URL, "")

	for _, decision := range []policy.Decision{policy.Bless, policy.Deny} {
		var signed bool
		events := captureEvents(t, func() {
			signed = s.rosieDecide(nil, decision, req, rosieFacts{Rule: "test"}, client, nil)
		})
		if signed {
			t.Fatalf("%s: rosieDecide signed with no grant", decision)
		}
		ev := findEvent(events, "pub.polis.security.agent_act_without_grant")
		if ev == nil || ev["source"] != "security" {
			t.Fatalf("%s: security event = %v, want one on source security", decision, ev)
		}
	}
	if ds.writeCount() != 0 {
		t.Fatalf("%d relationship requests sent", ds.writeCount())
	}
	if after, _ := os.ReadFile(metadata.BlessedPath(s.DataDir)); string(after) != string(before) {
		t.Fatal("blessed.json changed")
	}
}

// D12: with the operator switch off, a live grant still sends no marked request.
func TestRosiePausedSendsNoMarkedRequest(t *testing.T) {
	s, ds := rosieServer(t, "bless pub.polis.comment from all")
	switchRosieOnByDefault(t, s)
	s.RosiePaused = true
	(&blessingSyncHandler{server: s}).Process([]discovery.StreamEvent{requestedEvent()})
	s.rosieCatchUp()
	if ds.writeCount() != 0 {
		t.Fatalf("%d relationship requests sent while paused", ds.writeCount())
	}
}

func postRosie(t *testing.T, s *Server, on bool) map[string]interface{} {
	t.Helper()
	body := `{"on":false}`
	if on {
		body = `{"on":true}`
	}
	rec := httptest.NewRecorder()
	s.handleRosieSettings(rec, httptest.NewRequest(http.MethodPost, "/api/settings/rosie", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST on=%v: %d %s", on, rec.Code, rec.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp["rosie"].(map[string]interface{})
}

// D12: Settings → Rosie still saves the user's choice while she is paused.
func TestSettingsSavesTheChoiceWhileRosieIsPaused(t *testing.T) {
	s, _ := rosieServer(t, "bless pub.polis.comment from all")
	s.RosiePaused = true

	var st map[string]interface{}
	events := captureEvents(t, func() { st = postRosie(t, s, true) })
	if st["on"] != true || st["not_started"] != true {
		t.Fatalf("after switching on while paused: %v", st)
	}
	if ev := findEvent(events, "pub.polis.agent.grant_issued"); ev == nil || ev["basis"] != "user-signed" {
		t.Fatalf("grant_issued = %v", ev)
	}

	events = captureEvents(t, func() { st = postRosie(t, s, false) })
	if st["on"] != false {
		t.Fatalf("after switching off while paused: %v", st)
	}
	if findEvent(events, "pub.polis.agent.grant_withdrawn") == nil {
		t.Fatal("no grant_withdrawn event")
	}
}

func TestSwitchingRosieOffSurvivesARestart(t *testing.T) {
	s, ds := rosieServer(t, "bless pub.polis.comment from all")
	switchRosieOnByDefault(t, s)
	postRosie(t, s, false)

	// A new server on the same directory — a reboot.
	s2, _ := rosieServer(t, "bless pub.polis.comment from all")
	s2.DataDir, s2.PrivateKey, s2.PublicKey, s2.BaseURL = s.DataDir, s.PrivateKey, s.PublicKey, s.BaseURL
	s2.DiscoveryURL = ds.srv.URL
	if outcome, _, _ := agent.IssueDefault(s2.rosieConfig()); outcome != agent.DefaultWithdrawn {
		t.Fatalf("default pass after restart = %s", outcome)
	}
	(&blessingSyncHandler{server: s2}).Process([]discovery.StreamEvent{requestedEvent()})
	if ds.writeCount() != 0 {
		t.Fatal("Rosie decided after the user switched her off and the server restarted")
	}
}

// Rosie's own blessing.granted echo must leave the list she signed signed and
// marked (46 The design §7).
func TestRosiesOwnEchoLeavesTheListSignedAndMarked(t *testing.T) {
	s, _ := rosieServer(t, "bless pub.polis.comment from all")
	switchRosieOnByDefault(t, s)
	h := &blessingSyncHandler{server: s}
	h.Process([]discovery.StreamEvent{requestedEvent()})
	before, _ := os.ReadFile(metadata.BlessedPath(s.DataDir))

	h.Process([]discovery.StreamEvent{{
		Type:  "pub.polis.comment.blessing.granted",
		Actor: "test-site.polis.pub",
		Payload: map[string]interface{}{
			"target_domain": "test-site.polis.pub",
			"source_url":    rosieCommentURL,
			"target_url":    rosiePostURL,
		},
	}})
	after, _ := os.ReadFile(metadata.BlessedPath(s.DataDir))
	if string(before) != string(after) {
		t.Fatalf("the echo rewrote blessed.json:\nbefore: %s\nafter:  %s", before, after)
	}
	if st, _ := metadata.VerifyBlessedSite(s.DataDir); st != metadata.StatusValid {
		t.Fatalf("list status after the echo = %s", st)
	}
}

// D11: Settings renders the ONE source text.
func TestSettingsServesTheOneRosieText(t *testing.T) {
	s, _ := rosieServer(t, "bless pub.polis.comment from all")
	rec := httptest.NewRecorder()
	s.handleSettings(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
	var resp struct {
		Rosie struct {
			Text agent.Text `json:"text"`
		} `json:"rosie"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Rosie.Text != agent.RosieText {
		t.Fatalf("settings text = %+v, want agent.RosieText", resp.Rosie.Text)
	}
}
