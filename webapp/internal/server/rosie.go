package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/agent"
	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/blessing"
	"github.com/vdibart/polis-cli/cli-go/pkg/cache"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
	"github.com/vdibart/polis-cli/cli-go/pkg/verify"
)

// Rosie, inside the user's own server sync (Signet epic 11 D5, D10, D12, D13).
//
// ⭐ She is a BEHAVIOUR here, not a process: the same sync that has always
// auto-decided blessing requests, now deciding only under the user's live
// grant, and marking every decision with it. The same code runs on localhost,
// `serve` and every hosted tenant.
//
// ⛔ ONE SIGNING CALL. Every auto-decision — from the stream or from the
// catch-up pass — goes through rosieDecide, which takes the live grant as a
// required argument and never falls back to an unmarked signature.
//
// ⭐ SINCE EPIC 45 THIS IS THE ONLY PLACE A BLESSING IS AUTO-DECIDED. The
// discovery service records requests and decides nothing (45 D1), so
// rosieEvaluate also does what the DS used to: it fetches and verifies the
// comment (D5) and resolves `thread-blessed` from the owner's own blessing list
// (D4). Both steps live here, the one function the stream and the catch-up pass
// share.

// The catch-up pass's bound (D13).
//
// ⚠️ 30 DAYS: a request older than that has sat in the owner's inbox long enough
// that deciding it now would surprise them more than leaving it. 200 DECISIONS:
// one pass must not become a burst of signed requests against the DS rate
// limit. Both are reported on the event.
//
// ⚠️ The cap bounds ONE pass, not the catch-up: a pass that stops at the cap is
// not recorded, so the next cycle continues (review F2). A variable only so a
// test can lower it.
const rosieCatchUpWindow = 30 * 24 * time.Hour

var rosieCatchUpMax = 200

// rosieVerifyContent fetches and verifies a comment before Rosie decides it
// (epic 45 D5). Nil means verify.VerifyContent; a variable only so tests can
// serve comments without the network.
var rosieVerifyContent func(url string) (*verify.VerificationResult, error)

// LogSecurityEvent logs on `source: security`.
func (s *Server) LogSecurityEvent(event string, fields map[string]interface{}) {
	if s.Logger != nil {
		s.Logger.EventWithSource("security", event, fields)
	}
}

// rosieLiveGrant is the gate: Rosie's live grant for blessing, or nil. Nil
// while the hosted operator switch is off, whatever the user's records say.
func (s *Server) rosieLiveGrant() *attestation.LiveGrant {
	if s.RosiePaused {
		return nil
	}
	g, err := agent.LiveRosieGrant(s.DataDir)
	if err != nil {
		s.LogWarn("rosie: could not read grants: %v", err)
		return nil
	}
	return g
}

// blessingEvalContext builds the policy context for a blessing decision.
// ThreadBlessedDomains is per request, filled by rosieEvaluate.
func (s *Server) blessingEvalContext(store *stream.Store, myDomain string) policy.EvalContext {
	followedDomains := make(map[string]bool)
	if fl, err := following.Load(following.DefaultPath(s.DataDir)); err == nil {
		for _, entry := range fl.All() {
			if d := discovery.ExtractDomainFromURL(entry.URL); d != "" {
				followedDomains[d] = true
			}
		}
	}
	var followerState stream.FollowerState
	_ = store.LoadState("pub.polis.follow", &followerState)
	followerDomains := make(map[string]bool, len(followerState.Followers))
	for _, f := range followerState.Followers {
		followerDomains[f] = true
	}
	return policy.EvalContext{
		MyDomain:         myDomain,
		FollowingDomains: followedDomains,
		FollowerDomains:  followerDomains,
	}
}

// threadBlessedDomains answers `thread-blessed` for one thread from the site
// owner's OWN blessing list (epic 45 D4): the domains of every comment the owner
// has blessed under rootPost.
//
// ⭐ It holds what the discovery service used to count. blessed.json carries
// the owner's own grants (signed), Rosie's (signed and marked), and every grant
// the DS made in the past, because the sync cached those without checking who
// decided them.
//
// ⚠️ What it cannot see, and the DS could: a blessing someone ELSE granted on
// this thread (a reply to another commenter's comment, blessed by that
// commenter), and a blessed comment the owner's site never managed to fetch.
// No client creates the first, and no production thread has one (45 Q1).
//
// Nil for an empty rootPost: no thread, and `thread-blessed` matches nobody —
// as the DS, which asked nothing without a root post.
func threadBlessedDomains(siteDir, rootPost string) map[string]bool {
	if rootPost == "" {
		return nil
	}
	bc, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		return nil
	}
	want := blessedPostKey(rootPost)
	out := make(map[string]bool)
	for _, pc := range bc.Comments {
		if blessedPostKey(pc.Post) != want {
			continue
		}
		for _, c := range pc.Blessed {
			if d := strings.ToLower(discovery.ExtractDomainFromURL(c.URL)); d != "" {
				out[d] = true
			}
		}
	}
	return out
}

// blessedListHas reports whether the owner's blessing list names commentURL. A
// missing list names nothing.
func blessedListHas(siteDir, commentURL string) bool {
	bc, err := metadata.LoadBlessedComments(siteDir)
	if err != nil {
		return false
	}
	for _, pc := range bc.Comments {
		for _, c := range pc.Blessed {
			if c.URL == commentURL {
				return true
			}
		}
	}
	return false
}

// blessedPostKey is the form blessed.json keys a post under, from either a post
// URL or an existing key: "posts/…" for a legacy /posts/ URL, the .md URL
// otherwise (the writers' extractPostPath).
func blessedPostKey(u string) string {
	return extractPostPathFromURL(polisurl.NormalizeToMD(u))
}

// rosieRequest is one blessing request Rosie may decide.
type rosieRequest struct {
	CommentURL string
	// CommentVersion is the version the REQUEST named. ⛔ It is never what Rosie
	// blesses: rosieEvaluate replaces it with the version of the bytes she
	// fetched and verified (epic 45 D5).
	CommentVersion string
	InReplyTo      string
	RootPost       string
	TargetDomain   string
	Actor          string
}

// rosieVerified is a comment Rosie fetched and verified before deciding it.
type rosieVerified struct {
	fetched cache.FetchOutcome
	store   cache.Store
	desc    cache.Descriptor
}

// rosieVerifyComment fetches the comment from its author's site and verifies its
// signature (epic 45 D5), reusing the blessed cache's own fetch-and-verify so the
// decision and the cache can never disagree about what verified.
//
// ⛔ ONLY A VALID SIGNATURE PROCEEDS. A fetch failure, an invalid or missing
// signature, or a key that could not be checked leaves the request pending for
// the owner, and says why. The cache stores a comment with a missing signature
// for availability; Rosie does not bless one.
func (s *Server) rosieVerifyComment(req rosieRequest) (*rosieVerified, bool) {
	store := cache.Store{DataDir: s.DataDir, DSDomain: extractDomainFromURL(s.DiscoveryURL)}
	desc := cache.NewBlessedDescriptor(cache.DescriptorConfig{VerifyContent: rosieVerifyContent})
	out := desc.Fetch(store, req.CommentURL, cache.IngestOptions{})

	reason := ""
	switch {
	case out.Verdict == "unsupported_key":
		reason = "not_a_comment"
	case out.Err != nil:
		reason = "fetch_failed"
	case out.Verdict == "valid" && out.Sidecar.Pin == "":
		reason = "no_version"
	case out.Verdict == "valid":
	case out.Verdict == "":
		reason = "signature_unknown"
	default:
		reason = "signature_" + out.Verdict
	}
	if reason != "" {
		fields := map[string]interface{}{
			"agent":             agent.Rosie,
			"comment_url":       req.CommentURL,
			"reason":            reason,
			"verdict":           out.Verdict,
			"requested_version": req.CommentVersion,
		}
		if out.Err != nil {
			fields["error"] = out.Err.Error()
		}
		s.LogEvent("pub.polis.agent.decision_skipped", fields)
		return nil, false
	}
	return &rosieVerified{fetched: out, store: store, desc: desc}, true
}

// rosieEvaluate applies the user's rules to one request and, when they decide
// it, signs the decision. Reports whether a decision was signed.
//
// The order is epic 46 The design §8: (the live grant, checked by the caller) →
// fetch and verify → the rules, with `thread-blessed` resolved locally → sign.
//
// ⚠️ The rules still decide: no match and `review` leave the request for the
// owner, exactly as before this epic.
func (s *Server) rosieEvaluate(live *attestation.LiveGrant, policies []policy.Policy, ctx policy.EvalContext, req rosieRequest, client *discovery.Client, hc *hooks.HookConfig) bool {
	verified, ok := s.rosieVerifyComment(req)
	if !ok {
		return false
	}
	if req.CommentVersion != "" && req.CommentVersion != verified.fetched.Sidecar.Pin {
		s.LogEvent("pub.polis.agent.version_differs", map[string]interface{}{
			"agent":             agent.Rosie,
			"comment_url":       req.CommentURL,
			"requested_version": req.CommentVersion,
			"verified_version":  verified.fetched.Sidecar.Pin,
		})
	}
	req.CommentVersion = verified.fetched.Sidecar.Pin

	ctx.ThreadBlessedDomains = threadBlessedDomains(s.DataDir, req.RootPost)
	threadBlessed := ctx.ThreadBlessedDomains[strings.ToLower(req.Actor)]

	pEvt := policy.Event{
		Type:         "pub.polis.comment.blessing.requested",
		ActorDomain:  req.Actor,
		TargetDomain: req.TargetDomain,
		TargetPath:   req.InReplyTo,
	}
	evalResult := policy.EvaluateWithLog(policies, pEvt, ctx)
	if !evalResult.Matched {
		return false // no match -> manual review
	}

	// Unified decision event — fires for every matched blessing evaluation so
	// Axiom can aggregate decision distributions. Action-specific events
	// (auto_grant / auto_deny / review_queued) fire for the outcome taken.
	s.LogEvent("pub.polis.policy.decision", map[string]interface{}{
		"decision":     string(evalResult.Decision),
		"content_type": pEvt.Type,
		"actor_domain": req.Actor,
		"rule_matched": evalResult.Rule,
		"layer":        "tenant-inbound",
	})

	if evalResult.Decision == policy.Review {
		// Explicit pending — leave for human review, no action.
		s.LogEvent("pub.polis.policy.review_queued", map[string]interface{}{
			"comment_url": req.CommentURL,
			"actor":       req.Actor,
			"policy_rule": evalResult.Rule,
		})
		return false
	}
	return s.rosieDecide(live, evalResult.Decision, req, rosieFacts{
		Rule:          evalResult.Rule,
		ThreadBlessed: threadBlessed,
		Verified:      verified,
	}, client, hc)
}

// rosieFacts is what rosieEvaluate learned on the way to a decision.
type rosieFacts struct {
	Rule          string
	ThreadBlessed bool
	Verified      *rosieVerified
}

// rosieDecide is THE signing call for an auto-decision.
//
// ⛔ The live grant is a REQUIRED argument. Reaching here without one is a bug
// in this server — every caller resolves a grant first and stops when there is
// none — so it is refused before anything is signed and reported on the
// security stream. It is flat zero in a healthy fleet.
func (s *Server) rosieDecide(live *attestation.LiveGrant, decision policy.Decision, req rosieRequest, facts rosieFacts, client *discovery.Client, hc *hooks.HookConfig) bool {
	if !live.Valid() {
		s.refuseAgentAct(req, decision)
		return false
	}
	fields := map[string]interface{}{
		"comment_url":             req.CommentURL,
		"comment_version":         req.CommentVersion,
		"actor":                   req.Actor,
		"policy_rule":             facts.Rule,
		"rule":                    facts.Rule,
		"thread_blessed_resolved": facts.ThreadBlessed,
		"agent":                   live.Agent(),
		"grant_url":               live.URL(),
	}
	switch decision {
	case policy.Bless, policy.Allow, policy.Emit:
		// Bless is the new grammar; Allow/Emit retained for legacy rules.
		_, err := blessing.GrantAsAgent(s.DataDir, &blessing.IncomingRequest{
			CommentURL: req.CommentURL, CommentVersion: req.CommentVersion, InReplyTo: req.InReplyTo,
		}, client, hc, s.PrivateKey, live)
		if errors.Is(err, agent.ErrNoLiveGrant) {
			s.refuseAgentAct(req, decision)
			return false
		}
		if err != nil {
			s.LogWarn("rosie auto-grant failed for %s: %v", req.CommentURL, err)
			return false
		}
		// The bytes she verified are the bytes she blessed: cache them now, so
		// the comment renders (and the thread remembers it) without a second
		// fetch that could fail.
		if v := facts.Verified; v != nil {
			if err := v.desc.Store(v.store, v.fetched.Sidecar, v.fetched.Body); err != nil {
				s.LogWarn("rosie: blessed %s but could not cache it: %v", req.CommentURL, err)
			}
		}
		s.LogEvent("pub.polis.comment.blessing.auto_grant", fields)
		return true
	case policy.Deny:
		_, err := blessing.DenyAsAgent(req.CommentURL, req.InReplyTo, client, s.PrivateKey, live)
		if errors.Is(err, agent.ErrNoLiveGrant) {
			s.refuseAgentAct(req, decision)
			return false
		}
		if err != nil {
			s.LogWarn("rosie auto-deny failed for %s: %v", req.CommentURL, err)
			return false
		}
		s.LogEvent("pub.polis.comment.blessing.auto_deny", fields)
		return true
	}
	return false
}

func (s *Server) refuseAgentAct(req rosieRequest, decision policy.Decision) {
	s.LogSecurityEvent("pub.polis.security.agent_act_without_grant", map[string]interface{}{
		"agent":       agent.Rosie,
		"comment_url": req.CommentURL,
		"decision":    string(decision),
	})
}

// rosieCatchUp is the catch-up pass (D13): once per live grant, decide the
// blessing requests still pending, exactly as a new one would be decided.
//
// ⭐ "PENDING" IS READ FROM THE DISCOVERY SERVICE, not local stream state. The DS
// row is the current status — a request the owner approved by hand since it
// arrived is no longer pending there.
//
// The pass is recorded only when it completes, so a DS that could not be reached
// is retried on the next cycle.
func (s *Server) rosieCatchUp() {
	live := s.rosieLiveGrant()
	if !live.Valid() || agent.CatchUpDone(s.DataDir, live.URL()) {
		return
	}
	if s.DiscoveryURL == "" || s.PrivateKey == nil || !s.IsRegisteredWithDS() {
		return
	}
	myDomain := extractDomainFromURL(s.GetBaseURL())
	if myDomain == "" {
		return
	}

	client := s.NewReadDSClient(nil)
	client.RequestID = generateBackgroundRequestID("rosie-catch-up")
	fields := map[string]interface{}{
		"agent":     live.Agent(),
		"grant_url": live.URL(),
		"window":    rosieCatchUpWindow.String(),
		"max":       rosieCatchUpMax,
	}

	pending, err := blessing.FetchPendingRequests(client, myDomain)
	if err != nil {
		fields["error"] = err.Error()
		s.LogEvent("pub.polis.agent.catch_up", fields)
		return
	}

	privPath, pubPath := policy.DefaultPaths(s.DataDir)
	policies, _ := policy.LoadPolicies(privPath, pubPath)
	store := stream.NewStore(s.DataDir, s.GetDiscoveryDomain(), "pub.polis.core")
	ctx := s.blessingEvalContext(store, myDomain)
	hc := s.getHookConfig()
	cutoff := time.Now().Add(-rosieCatchUpWindow)

	decided, leftPending, outsideWindow, capped := 0, 0, 0, 0
	for _, p := range pending {
		created, perr := time.Parse(time.RFC3339, p.CreatedAt)
		if perr != nil || created.Before(cutoff) {
			// Outside the window, or undated: leave it for the owner rather than
			// guess its age.
			outsideWindow++
			leftPending++
			continue
		}
		if len(policies) == 0 {
			leftPending++
			continue
		}
		if decided >= rosieCatchUpMax {
			// Left only because this pass is full — the next cycle continues.
			capped++
			leftPending++
			continue
		}
		req := rosieRequest{
			CommentURL: p.CommentURL, CommentVersion: p.CommentVersion, InReplyTo: p.InReplyTo,
			RootPost: p.RootPost, TargetDomain: myDomain, Actor: p.Author,
		}
		if s.rosieEvaluate(live, policies, ctx, req, client, hc) {
			decided++
		} else {
			leftPending++
		}
	}

	// ⛔ Recorded only when nothing was left because of the cap. Requests left
	// outside the window or held by a review rule are the owner's; requests left
	// because the pass was full are still Rosie's to decide (review F2).
	fields["complete"] = capped == 0
	if capped == 0 {
		if err := agent.RecordCatchUp(s.DataDir, live.URL()); err != nil {
			s.LogWarn("rosie: catch-up ran but could not be recorded: %v", err)
		}
	}
	fields["capped"] = capped
	fields["pending_seen"] = len(pending)
	fields["decided"] = decided
	fields["left_pending"] = leftPending
	fields["outside_window"] = outsideWindow
	s.LogEvent("pub.polis.agent.catch_up", fields)
}

// rosieConfig is the grant-writing configuration for this site.
func (s *Server) rosieConfig() agent.Config {
	cfg := agent.Config{SiteDir: s.DataDir, BaseURL: s.GetBaseURL(), PrivateKey: s.PrivateKey}
	if s.DiscoveryURL != "" {
		cfg.Discovery = &attestation.DiscoveryConfig{
			DiscoveryURL: s.DiscoveryURL,
			DiscoveryKey: s.DiscoveryKey,
			BaseURL:      s.GetBaseURL(),
			DataDir:      s.DataDir,
			HTTPClient:   s.SharedHTTPClient,
		}
	}
	return cfg
}

// rosieSettings describes Rosie for Settings → Rosie. Always carries the text,
// so the screen renders the ONE source string (D11).
func (s *Server) rosieSettings() map[string]interface{} {
	out := map[string]interface{}{
		"text":        agent.RosieText,
		"not_started": s.RosiePaused,
		"on":          false,
		"never":       true,
		"history":     []agent.GrantView{},
	}
	st, err := agent.ReadState(s.DataDir)
	if err != nil {
		out["error"] = "could not read Rosie's records"
		return out
	}
	out["on"] = st.On
	out["never"] = st.Never
	out["history"] = st.History
	if st.Live != nil {
		out["live"] = st.Live
	}
	if st.OffSince != "" {
		out["off_since"] = st.OffSince
	}
	return out
}

// handleRosieSettings switches Rosie on or off: the user's one switch (D4).
//
// ⚠️ It works while the hosted operator switch is off (D12): the choice is
// saved and registered, and she follows it once she starts.
func (s *Server) handleRosieSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"rosie": s.rosieSettings()})
		return
	case http.MethodPost:
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		On *bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.On == nil {
		http.Error(w, `Invalid request body: expected {"on": true|false}`, http.StatusBadRequest)
		return
	}
	if s.PrivateKey == nil {
		http.Error(w, "This site has no key to sign with", http.StatusServiceUnavailable)
		return
	}
	cfg := s.rosieConfig()

	if *req.On {
		written, already, err := agent.Enable(cfg)
		if err != nil {
			s.LogError("Failed to switch Rosie on: %v", err)
			http.Error(w, "Failed to switch Rosie on", http.StatusInternalServerError)
			return
		}
		if !already {
			s.logGrantWritten("pub.polis.agent.grant_issued", written, "settings")
			if !s.RosiePaused {
				// Her catch-up pass runs in the next sync cycle, which serialises
				// it with every other decision this server makes.
				s.TriggerSync()
			}
		}
	} else {
		written, err := agent.Disable(cfg)
		for _, wr := range written {
			s.logGrantWritten("pub.polis.agent.grant_withdrawn", wr, "settings")
		}
		if err != nil {
			s.LogError("Failed to switch Rosie off: %v", err)
			http.Error(w, "Failed to switch Rosie off", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"rosie":  s.rosieSettings(),
	})
}

func (s *Server) logGrantWritten(event string, wr *agent.Written, trigger string) {
	if wr == nil {
		return
	}
	fields := map[string]interface{}{
		"agent":      agent.Rosie,
		"grant_url":  wr.URL,
		"behaviours": wr.Behaviours,
		"basis":      wr.Basis,
		"registered": wr.Registered,
		"trigger":    trigger,
	}
	if wr.RegisterErr != nil {
		fields["register_error"] = wr.RegisterErr.Error()
	}
	if wr.ProjectionErr != nil {
		fields["projection_error"] = wr.ProjectionErr.Error()
	}
	s.LogEvent(event, fields)
}
