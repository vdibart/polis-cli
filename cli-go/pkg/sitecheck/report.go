package sitecheck

import (
	"fmt"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// The five check families `polis validate` reports under. They are the
// families the command reference has always promised; before epic 20 only two
// of them existed in code.
const (
	FamilyContent  = "signed-content-integrity"
	FamilyIndex    = "index-consistency"
	FamilyPolicy   = "policy-parseability"
	FamilyIdentity = "key-handle-alignment"
	FamilyBundle   = "bundle-registry-health"
)

// Outcome is what happened to one check.
//
// ⛔ FOUR VALUES, AND NOT-APPLICABLE IS NEVER FOLDED INTO PASSED.
// A clean report must mean "I checked and it was fine" — never "I did not
// check". The whole reason this command needed rebuilding is that it printed
// no errors while verifying no signatures, and a user reasonably concluded
// their signatures were checked.
type Outcome string

const (
	// OutcomePassed — the check ran and found nothing wrong.
	OutcomePassed Outcome = "passed"
	// OutcomeFailed — the check ran and found something wrong.
	OutcomeFailed Outcome = "failed"
	// OutcomeWarning — the check ran and found something the reader should look
	// at, which is NOT a defect in the site and does NOT fail the run.
	//
	// ⭐ It exists for one shape of finding: something true about the site's
	// SURROUNDINGS rather than about the site. The public robots.txt is the
	// case — an intermediary can add directives to the file that speaks for the
	// author, and the author usually cannot edit a managed robots.txt and often
	// should not want to. A hard failure there would be an alert whose subject
	// cannot act on it; silence would be worse.
	//
	// ⛔ So it must NEVER become the soft landing for a real defect. If the site
	// itself is wrong and its owner can fix it, that is OutcomeFailed.
	OutcomeWarning Outcome = "warning"
	// OutcomeNotApplicable — the check did NOT run, and Reason says why.
	OutcomeNotApplicable Outcome = "not_applicable"
)

// Check is one answered (or unanswerable) question about a site.
type Check struct {
	ID      string  `json:"id"`
	Family  string  `json:"family"`
	Outcome Outcome `json:"outcome"`
	// Detail describes what was found. Present on passed checks too, because
	// "12 verified, 3 unsigned" and "15 verified" are different facts.
	Detail string `json:"detail,omitempty"`
	// Reason is REQUIRED whenever Outcome is not_applicable: what was missing,
	// and therefore what this report does not cover.
	Reason string `json:"reason,omitempty"`
	// Findings enumerates individual problems, when there are individual
	// problems to enumerate.
	Findings []string `json:"findings,omitempty"`
	// Examined counts the artifacts actually inspected.
	Examined int `json:"examined"`
}

// Totals is the census. One number per outcome, never one figure for
// "problems": that would hide the not-applicable count, which is the thing a
// reader most needs, and it would let a warning read as a failure.
type Totals struct {
	Passed        int `json:"passed"`
	Failed        int `json:"failed"`
	Warning       int `json:"warning"`
	NotApplicable int `json:"not_applicable"`
}

// Report is a full validation run.
type Report struct {
	// Target is the directory or URL that was checked.
	Target string `json:"target"`
	// Form is "local" (a directory on this machine) or "remote" (fetched over
	// public HTTP, storing nothing).
	Form string `json:"form"`
	// Scope is "site" or "artifact". An artifact-scoped result says something
	// about ONE file and nothing about the site around it.
	Scope string `json:"scope"`
	// Note carries the scope caveat in prose, so a result cannot be read as a
	// broader claim than it is.
	Note   string  `json:"note,omitempty"`
	Checks []Check `json:"checks"`
	Totals Totals  `json:"totals"`
	// Errors holds a fatal problem that stopped the run before any check could
	// be attempted (an unreadable directory, an unreachable host).
	FatalError string `json:"fatal_error,omitempty"`

	// dsKeys is where witness signatures get their discovery-service keys
	// (SIGNET epic 32 D8). Carried on the run so the checks that need it do not
	// all grow a parameter; never serialized.
	dsKeys DSKeyLookup
}

const (
	FormLocal  = "local"
	FormRemote = "remote"

	ScopeSite     = "site"
	ScopeArtifact = "artifact"
)

// OK reports whether nothing FAILED. It is deliberately not "everything
// passed": a report with not-applicable checks is a legitimate clean result for
// a clone or a remote site, and the not-applicable list is how the reader
// learns what it does not cover. Warnings do not affect it either, for the
// reason given on OutcomeWarning — they describe the site's surroundings, and
// exiting non-zero on one would break CI over something CI cannot fix.
func (r *Report) OK() bool {
	return r.FatalError == "" && r.Totals.Failed == 0
}

func (r *Report) add(c Check) {
	r.Checks = append(r.Checks, c)
	switch c.Outcome {
	case OutcomePassed:
		r.Totals.Passed++
	case OutcomeFailed:
		r.Totals.Failed++
	case OutcomeWarning:
		r.Totals.Warning++
	case OutcomeNotApplicable:
		r.Totals.NotApplicable++
	}
}

func (r *Report) pass(id, family, detail string, examined int) {
	r.add(Check{ID: id, Family: family, Outcome: OutcomePassed, Detail: detail, Examined: examined})
}

func (r *Report) fail(id, family, detail string, examined int, findings ...string) {
	r.add(Check{ID: id, Family: family, Outcome: OutcomeFailed, Detail: detail, Examined: examined, Findings: findings})
}

// passNoting records a passed check that still carries lines a reader must
// see. Its one use is a retired-key verification (SIGNET epic 31 D4): it passed,
// and it is a weaker claim than a current-key pass, so it must not be silent.
func (r *Report) passNoting(id, family, detail string, examined int, notes ...string) {
	r.add(Check{ID: id, Family: family, Outcome: OutcomePassed, Detail: detail, Examined: examined, Findings: notes})
}

// warn records a check that ran and found something worth reading, without
// failing the run. See OutcomeWarning for the one shape of finding it is for.
func (r *Report) warn(id, family, detail string, examined int, findings ...string) {
	r.add(Check{ID: id, Family: family, Outcome: OutcomeWarning, Detail: detail, Examined: examined, Findings: findings})
}

// na records a check that did not run. reason is mandatory by construction —
// there is no way to record a not-applicable check without saying why.
func (r *Report) na(id, family, reason string) {
	r.add(Check{ID: id, Family: family, Outcome: OutcomeNotApplicable, Reason: reason})
}

// fromStatus records a CheckStatus produced by one of the actor predicates.
// The actors' statuses carry no message when they pass, because a fleet sweep
// only reports exceptions; a person reading one site wants to be told what was
// looked at, so an empty message becomes an explicit "checked, nothing wrong".
func (r *Report) fromStatus(id, family string, st CheckStatus) {
	msg := st.Message
	if st.OK {
		if msg == "" {
			msg = "checked, nothing wrong"
		}
		r.pass(id, family, msg, 1)
		return
	}
	r.fail(id, family, msg, 1)
}

// signatureOutcome maps a verifier's signature status onto a check outcome.
//
// ⭐ The two rules that keep this honest:
//   - UNSIGNED IS PASSED. Most artifacts on most sites carry no signature;
//     reading absence as failure would tell nearly every self-hoster their site
//     is broken. Only present-and-failing is a finding.
//   - UNKNOWN IS NOT-APPLICABLE, never passed. "Could not check" is exactly the
//     thing that must never look like "checked and fine".
//
// unrecognised is the fetched document's unrecognised-member list. On the
// `valid` row it is the SECOND half of the answer and must not be dropped: the
// signature verified over the fields this build knows, and these were not among
// them (SIGNET epic 47, row 2 of the table). On the `unknown` row the detail
// already carries the reason.
func (r *Report) signatureOutcome(id, family, artifact string, status string, detail string, unrecognised []string) {
	switch status {
	case "valid":
		msg := artifact + ": signature verifies"
		if note := signing.UncoveredNote(unrecognised); note != "" {
			msg += " — " + note
		}
		r.pass(id, family, msg, 1)
	case "unsigned":
		r.pass(id, family, artifact+": unsigned — nothing to verify, which is a legal state", 1)
	case "invalid":
		r.fail(id, family, artifact+": signature does NOT verify", 1, detail)
	default:
		r.na(id, family, fmt.Sprintf("%s: could not be checked — %s", artifact, detail))
	}
}
