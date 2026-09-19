package sitecheck

import (
	"fmt"
	"sort"
)

// The two forms of `polis validate` — SIGNET epic 44 D6.
//
// `polis validate <dir>` and `polis validate <url>` are supposed to be the same
// predicate reaching the same site two ways. They legitimately see different
// things — only a directory shows phantoms and private state, only the wire
// shows what the edge serves — so "the same results" is the WRONG assertion.
//
// ⛔ The assertion is two parts:
//
//  1. a check BOTH forms run gives the same site the SAME VERDICT;
//  2. a check only ONE form runs is declared here with its reason, and the
//     other form reports NOT CHECKED — never PASS.
//
// ⭐ Epic 44's F1 was part 2's failure at the level of one check: remote
// index.consistency returned a green tick over entry types it never fetched.
//
// ⚠️ A TABLE AND ONE COMPARISON, NOT A FRAMEWORK (M2). It says runs / does not
// run, and why — nothing else.

// formReach is where one check runs.
type formReach struct {
	local, remote bool
	// why is the reason a check runs in one form only. Required exactly when
	// one of local and remote is false.
	why string
}

// formParity declares every check a SITE run of either form can emit, keyed by
// check ID. ⛔ A check missing from this table fails CompareForms — so nobody
// can add a check to one form without saying what the other one does.
var formParity = map[string]formReach{
	"identity.well_known":          {local: true, remote: true},
	"identity.did_document":        {local: true, remote: true},
	"identity.key_history":         {local: true, remote: true},
	"identity.key_history_head":    {local: true, remote: true},
	"identity.key_history_witness": {local: true, remote: true},
	"identity.messages_key":        {local: true, remote: true},
	"identity.key_files":           {local: true, why: "private keys are never served over HTTP"},
	"identity.key_perms":           {local: true, why: "file permissions are never served over HTTP"},
	"identity.key_match":           {local: true, why: "the key file the published key is compared with is never served over HTTP"},

	"bundle.declared": {local: true, remote: true},
	"bundle.json":     {local: true, remote: true},
	"bundle.registry": {local: true, why: "the bundle registry is private state, never served over HTTP"},

	// Both forms check every indexed entry; only a directory can also see a
	// phantom (HTTP does not list directories), and the remote detail says so.
	"index.consistency": {local: true, remote: true},

	// Both forms parse the public rules; only a directory has the private ones,
	// and both details say which were read.
	"policy.syntax": {local: true, remote: true},

	"content.posts":        {local: true, remote: true},
	"content.comments":     {local: true, remote: true},
	"content.following":    {local: true, remote: true},
	"content.blessed":      {local: true, remote: true},
	"content.license":      {local: true, remote: true},
	"content.attestations": {local: true, remote: true},
	"content.tags":         {local: true, remote: true},
	"content.witnesses":    {local: true, remote: true},

	"content.license_robots": {remote: true, why: onlyOnTheWire},
	"content.license_rsl":    {remote: true, why: onlyOnTheWire},
}

// CompareForms reports every way a local and a remote run of the SAME site
// break the two-part assertion above. Empty means they agree.
//
// It compares OUTCOMES, never details: the two forms phrase things differently
// on purpose (a remote detail names what HTTP cannot see).
func CompareForms(local, remote *Report) []string {
	var out []string
	lc, rc := checksByID(local), checksByID(remote)

	seen := map[string]bool{}
	for id := range lc {
		seen[id] = true
	}
	for id := range rc {
		seen[id] = true
	}
	for id := range formParity {
		seen[id] = true
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		reach, declared := formParity[id]
		l, inLocal := lc[id]
		r, inRemote := rc[id]
		switch {
		case !declared:
			out = append(out, fmt.Sprintf("%s: not declared in formParity — say whether each form runs it, and why not", id))
		case !inLocal || !inRemote:
			out = append(out, fmt.Sprintf("%s: emitted by local=%v remote=%v — every site check must be reported by BOTH forms, as NOT CHECKED where it cannot run", id, inLocal, inRemote))
		case reach.local && reach.remote:
			if l.Outcome != r.Outcome {
				out = append(out, fmt.Sprintf("%s: local says %s, remote says %s — the same site must get the same verdict\n    local:  %s\n    remote: %s", id, l.Outcome, r.Outcome, l.Detail+l.Reason, r.Detail+r.Reason))
			}
		case !reach.remote && r.Outcome != OutcomeNotApplicable:
			out = append(out, fmt.Sprintf("%s: remote says %s for a check it cannot run (%s) — it must say NOT CHECKED", id, r.Outcome, reach.why))
		case !reach.local && l.Outcome != OutcomeNotApplicable:
			out = append(out, fmt.Sprintf("%s: local says %s for a check it cannot run (%s) — it must say NOT CHECKED", id, l.Outcome, reach.why))
		}
	}
	return out
}

func checksByID(r *Report) map[string]Check {
	out := map[string]Check{}
	if r == nil {
		return out
	}
	for _, c := range r.Checks {
		out[c.ID] = c
	}
	return out
}
