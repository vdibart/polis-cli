package sitecheck

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Agent acts: does every act a user agent made on this site add up? Judge
// runs it on hosted tenants (item 21) and `polis validate` on any local site.
//
// For each marked post, comment and blessing:
//  1. GRANT. The marker cites a grant on this site that verifies (through
//     the key history) and names the same agent.
//  2. LIVE. The act was signed after the grant was asserted, and before any
//     withdrawal of it.
//  3. SCOPE. The grant allowed the act: a post needs publish, a comment
//     comment, a blessing bless (or the rosie/1 behaviour set).
//  4. RECORDED. A marked post or comment has its write in the site's act log,
//     for that grant, within minutes of the signed time.
//
// ⚠️ WHAT IT CANNOT SEE: an agent holding the owner's full key (its acts are
// unmarked, so they look like the owner's own); a write made after a
// switch-off but signed with an earlier time (signed times are the signer's
// claim); and anyone editing the act log, which is private and unsigned, so
// check 4 is evidence, never proof. ⛔ REPORT, NEVER HEAL: every artifact here
// was signed with the owner's key.

// Agent-act failure kinds.
const (
	AgentActGrant    = "grant"
	AgentActNotLive  = "not_live"
	AgentActScope    = "scope"
	AgentActNoRecord = "no_record"
)

// AgentActFailure is one act that does not add up.
type AgentActFailure struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Grant   string `json:"grant,omitempty"`
	Message string `json:"message"`
}

// AgentActsResult is what the check found.
type AgentActsResult struct {
	// Checked counts marked acts examined.
	Checked int `json:"checked"`
	// Unchecked are acts that could not be decided, with why. Never failures.
	Unchecked []string          `json:"unchecked,omitempty"`
	Failures  []AgentActFailure `json:"failures,omitempty"`
}

// actRecordWindow is how far after an act's signed time its act-log entry may
// be (the log is written when the response is sent), and actRecordSkew how
// far before.
const (
	actRecordWindow = 10 * time.Minute
	actRecordSkew   = time.Minute
)

type markedAct struct {
	path, agent, grant, kind, signedAt string
}

// AgentActs examines every marked act on site.
func AgentActs(site storage.Site) AgentActsResult {
	var res AgentActsResult
	acts := collectMarkedActs(site, &res)
	if len(acts) == 0 {
		return res
	}
	grants, err := attestation.Grants(site, "")
	if err != nil {
		res.Unchecked = append(res.Unchecked, "the site's grants could not be read: "+err.Error())
		return res
	}
	byURL := map[string]attestation.GrantEntry{}
	for _, g := range grants {
		byURL[g.URL] = g
	}
	// ⭐ The connections' acts only: the owner's share the file but have their
	// own bound, so they neither fill this record nor count toward it.
	log, _ := connection.AgentActs(site)
	logFull := len(log) >= connection.MaxActs
	var logStart time.Time
	if len(log) > 0 {
		logStart, _ = parseActTime(log[0].Time)
	}

	for _, a := range acts {
		res.Checked++
		fail := func(kind, msg string) {
			res.Failures = append(res.Failures, AgentActFailure{Path: a.path, Kind: kind, Grant: a.grant, Message: msg})
		}
		g, ok := byURL[a.grant]
		if !ok {
			fail(AgentActGrant, fmt.Sprintf("the %s is marked as %q's under a grant this site does not hold", a.kind, a.agent))
			continue
		}
		if g.Signature != attestation.StatusValid {
			fail(AgentActGrant, fmt.Sprintf("the grant this %s cites does not verify (%s)", a.kind, g.Signature))
			continue
		}
		if g.Agent != a.agent {
			fail(AgentActGrant, fmt.Sprintf("the %s names %q but its grant names %q", a.kind, a.agent, g.Agent))
			continue
		}
		if !grantAllows(g, a.kind) {
			fail(AgentActScope, fmt.Sprintf("the grant (%s/%d, scopes %q) does not allow a %s", g.Set, g.Version, g.Record.Payload[attestation.GrantKeyScopes], a.kind))
			continue
		}
		at, err := parseActTime(a.signedAt)
		if err != nil {
			res.Unchecked = append(res.Unchecked, a.path+": its signed time cannot be read, so its grant's standing then is unknown")
			continue
		}
		if asserted, err := parseActTime(g.Record.Asserted); err == nil && at.Before(asserted.Add(-actRecordSkew)) {
			fail(AgentActNotLive, fmt.Sprintf("the %s was signed at %s, before its grant was made (%s)", a.kind, a.signedAt, g.Record.Asserted))
			continue
		}
		if g.Withdrawn {
			w, err := parseActTime(g.WithdrawnAt)
			if err != nil {
				res.Unchecked = append(res.Unchecked, a.path+": its grant is withdrawn and the withdrawal's time is not on the site")
				continue
			}
			if at.After(w) {
				fail(AgentActNotLive, fmt.Sprintf("the %s was signed at %s, after its grant was withdrawn (%s)", a.kind, a.signedAt, g.WithdrawnAt))
				continue
			}
		}
		// Check 4 is for API connections: Rosie's blessings are made in the
		// site's own sync, not through the API, so they are not in the log.
		if a.kind == "blessing" || g.Set != connection.Set {
			continue
		}
		if logFull && at.Before(logStart) {
			res.Unchecked = append(res.Unchecked, a.path+": older than the act log, which keeps only the newest acts")
			continue
		}
		if !actLogged(log, a, at) {
			fail(AgentActNoRecord, fmt.Sprintf("no write of this %s by this grant is in the site's act log", a.kind))
		}
	}
	return res
}

func grantAllows(g attestation.GrantEntry, kind string) bool {
	switch g.Set {
	case "rosie":
		return kind == "blessing"
	case connection.Set:
		need := map[string]string{"post": "publish", "comment": "comment", "blessing": "bless"}[kind]
		for _, s := range strings.Fields(g.Record.Payload[attestation.GrantKeyScopes]) {
			if s == need {
				return true
			}
		}
	}
	return false
}

// actLogged reports whether the act log holds a successful write of a's kind,
// under a's grant, close to a's signed time.
func actLogged(log []connection.Act, a markedAct, at time.Time) bool {
	for _, e := range log {
		if e.Grant != a.grant || e.Status < 200 || e.Status > 299 || actKind(e) != a.kind {
			continue
		}
		t, err := parseActTime(e.Time)
		if err != nil {
			continue
		}
		if !t.Before(at.Add(-actRecordSkew)) && !t.After(at.Add(actRecordWindow)) {
			return true
		}
	}
	return false
}

// actKind is the kind of artifact an act-log write made, from its path:
// /v1/content/<type>[/…] with a POST, PUT or PATCH.
func actKind(e connection.Act) string {
	switch e.Method {
	case "POST", "PUT", "PATCH":
	default:
		return ""
	}
	parts := strings.Split(strings.Trim(e.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "content" {
		return ""
	}
	switch parts[2] {
	case "post", "pub.polis.post":
		if len(parts) > 4 { // an action on a post, not the post itself
			return ""
		}
		return "post"
	case "comment", "pub.polis.comment":
		if len(parts) > 4 {
			return ""
		}
		return "comment"
	}
	return ""
}

func parseActTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, strings.Trim(strings.TrimSpace(s), `"'`))
}

// collectMarkedActs lists the site's marked posts, comments and blessings.
func collectMarkedActs(site storage.Site, res *AgentActsResult) []markedAct {
	var out []markedAct
	ctx := context.Background()
	for _, tree := range []struct{ prefix, kind string }{
		{"content/pub.polis.core/post", "post"},
		{"content/pub.polis.core/comment", "comment"},
	} {
		entries, err := site.Store().List(ctx, tree.prefix, storage.Recursive())
		if err != nil && len(entries) == 0 {
			continue // no such tree: nothing published of this kind
		}
		for _, e := range entries {
			if e.IsDir || strings.Contains(e.Key, "/.versions/") || path.Ext(e.Key) != ".md" {
				continue
			}
			data, err := site.Store().Get(ctx, e.Key)
			if err != nil {
				res.Unchecked = append(res.Unchecked, e.Key+": could not be read")
				continue
			}
			fm, _, err := ParseFrontmatter(string(data))
			if err != nil || (fm.Agent == "" && fm.Grant == "") {
				continue
			}
			at := fm.Updated
			if at == "" {
				at = fm.Published
			}
			out = append(out, markedAct{path: e.Key, agent: fm.Agent, grant: fm.Grant, kind: tree.kind, signedAt: at})
		}
	}
	if bc, err := metadata.LoadBlessedComments(site); err == nil && bc != nil {
		for _, pc := range bc.Comments {
			for _, b := range pc.Blessed {
				if b.Agent == "" && b.Grant == "" {
					continue
				}
				out = append(out, markedAct{path: "blessed: " + b.URL, agent: b.Agent, grant: b.Grant, kind: "blessing", signedAt: b.BlessedAt})
			}
		}
	}
	return out
}

// checkAgentActsLocal is the check `polis validate` reports.
func checkAgentActsLocal(r *Report, siteDir string) {
	res := AgentActs(storage.At(siteDir))
	if res.Checked == 0 && len(res.Unchecked) == 0 {
		r.na("agents.acts", FamilyAgents, "no act on this site is marked as an agent's")
		return
	}
	detail := fmt.Sprintf("%s examined", plural(res.Checked, "marked act"))
	if len(res.Failures) > 0 {
		var findings []string
		for _, f := range res.Failures {
			findings = append(findings, f.Path+": "+f.Message)
		}
		r.fail("agents.acts", FamilyAgents, detail, res.Checked, findings...)
		return
	}
	if len(res.Unchecked) > 0 {
		r.passNoting("agents.acts", FamilyAgents, detail, res.Checked, res.Unchecked...)
		return
	}
	r.pass("agents.acts", FamilyAgents, detail, res.Checked)
}
