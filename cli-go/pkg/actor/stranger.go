package actor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
)

// The stranger's check — docs/signet/spec/custody.md §7, as code.
//
// Given one signed action, a stranger with nothing but HTTPS asks: is the key
// that signed this one of an operator's listed actors, did that actor agree to
// how it is listed, and is this the kind of thing the operator said it would do?
//
// # ⛔ It is the opposite of the Guard, and the difference is deliberate
//
// A Guard (guard.go) is how an ACTOR reads its own operator's registry, and it
// NEVER fetches — a network dependency inside a sweep is disqualifying. This is
// how everyone ELSE reads it, and fetching over HTTPS is the whole point: the
// check must need nothing the operator controls except what it publishes. The
// fetcher is passed in so the logic is testable without a network, and so this
// package does not grow an HTTP client.
//
// # ⛔ THREE DIFFERENT FINDINGS, NEVER ONE VERDICT (custody.md §10)
//
//   - LOOKALIKE (step 4) — the operator does not list the signer. Binary and
//     conclusive.
//   - COUNTERSIGNATURE (step 5) — the actor's consent to its entry does not
//     verify: the operator has changed the entry since the actor agreed to it.
//     An ABSENT countersignature is a weaker claim and is NOT a finding.
//   - DEVIATION (step 6) — a listed actor did something outside its expected
//     actions. ⚠️ EVIDENCE, not a violation the protocol blocked. Nothing here
//     enforces the registry, and nothing here may be described as though it did.
//
// There is deliberately no "ok" or "trusted" field. Law 2: the check states what
// it found; whether that is acceptable belongs to whoever is asking.

// Fetcher returns the body of an HTTPS URL, or an error when it could not be
// fetched. A failed fetch is "could not look", never "looked and found nothing".
type Fetcher func(url string) ([]byte, error)

// StepOutcome is what happened at one step of the check.
type StepOutcome string

const (
	// StepPassed — the step ran and found what the claim says.
	StepPassed StepOutcome = "passed"
	// StepFailed — the step ran and found something that contradicts the claim.
	// Every failed step carries a Finding.
	StepFailed StepOutcome = "failed"
	// StepWeaker — step 5 only, and TWO cases: no countersignature, or one that
	// verifies only against a RETIRED key of the actor (a countersignature signs
	// no time, so no key window can be checked). A weaker claim, not a broken
	// one, and never a finding. Step.KeyUsed tells the two apart: absent for the
	// first, present for the second.
	StepWeaker StepOutcome = "weaker"
	// StepUnknown — the step could not be checked (a fetch failed, a key was not
	// published). Never a finding: having failed to look is not having looked.
	StepUnknown StepOutcome = "unknown"
	// StepNotApplicable — step 2 only: the signer names no operator and none was
	// given, so there is no registry to consult. Not a finding either — nearly
	// every site on the network runs no actors.
	StepNotApplicable StepOutcome = "not_applicable"
	// StepNotReached — an earlier step stopped the check, as §7 says it must.
	StepNotReached StepOutcome = "not_reached"
)

// Finding kinds. Each is a different fact and a reader must be able to tell them
// apart without parsing prose.
const (
	FindingUnsigned                = "unsigned"                 // step 1 — nothing binds the action to the signer
	FindingSignatureInvalid        = "signature_invalid"        // step 1 — not from the domain it names
	FindingUncorroborated          = "uncorroborated"           // step 2 — the operator publishes no registry
	FindingRegistryUntrusted       = "registry_untrusted"       // step 3 — nothing in the file counts
	FindingLookalike               = "lookalike"                // step 4
	FindingCountersignatureInvalid = "countersignature_invalid" // step 5
	FindingDeviation               = "deviation"                // step 6
)

// Step is one of §7's six steps.
type Step struct {
	Step    int         `json:"step"`
	Name    string      `json:"name"`
	Outcome StepOutcome `json:"outcome"`
	Detail  string      `json:"detail,omitempty"`
	// KeyUsed is set when a RETIRED key verified this step's signature — step
	// 1's action, step 3's registry, step 5's countersignature — and ABSENT for
	// a current-key pass and for every step that verified nothing. It sits
	// BESIDE Detail, which says the same thing in prose, so a consumer never
	// has to string-match "RETIRED" to tell step 5's two `weaker` cases apart.
	// ⚠️ At step 5 it carries no claimed_signing_time: a countersignature
	// signs none, which is why that case is weaker rather than passed.
	KeyUsed *sitecheck.KeyUsed `json:"key_used,omitempty"`
}

// Finding is one thing a reader should look at.
type Finding struct {
	Kind   string `json:"kind"`
	Step   int    `json:"step"`
	Detail string `json:"detail"`
}

// ActionRef says what was checked.
type ActionRef struct {
	// Source is the URL or file the action was read from.
	Source string `json:"source"`
	// Type is the action type compared at step 6. For an attestation it is the
	// predicate — the registry's expected_actions name predicates and event types.
	Type string `json:"type"`
	// Signer is the domain the action names as its signer.
	Signer string `json:"signer"`
}

// StrangerCheck is the full result.
type StrangerCheck struct {
	Action ActionRef `json:"action"`
	// Operator is the operator the check consulted, when there was one.
	Operator string `json:"operator,omitempty"`
	// OperatorSource is "claimed" (from the signer's own .well-known/polis),
	// "given" (passed by the person running the check), or "self" (the signer
	// publishes its own registry, so it is an operator checking against itself).
	// ⛔ A claimed operator is a CLAIM — step 4 is what corroborates it.
	OperatorSource string `json:"operator_source,omitempty"`
	// RegistryURL is where step 3 read the registry from.
	RegistryURL string    `json:"registry_url,omitempty"`
	Steps       []Step    `json:"steps"`
	Findings    []Finding `json:"findings"`
}

var stepNames = [7]string{"",
	"signature",
	"operator",
	"registry",
	"entry",
	"countersignature",
	"expected_action",
}

// CheckAction runs custody.md §7 over one attestation record.
//
// givenOperator is optional. Without it the check follows the signer's own
// `operator` claim; with it, the check asks that specific operator — which is how
// a stranger catches a lookalike that simply omits the claim.
func CheckAction(rec *attestation.Record, source, givenOperator string, fetch Fetcher) *StrangerCheck {
	c := &StrangerCheck{Findings: []Finding{}}
	c.Action.Source = source
	if rec != nil {
		c.Action.Type = rec.Predicate
	}

	// ---- step 1: the signer's key, and the action's signature against it
	signer, err := domainOf(recIssuer(rec))
	if err != nil {
		return c.stop(1, StepFailed, FindingSignatureInvalid,
			"the action names no usable signer: "+err.Error())
	}
	c.Action.Signer = signer

	// Every document is read at most once per check: step 1's verifier and
	// step 2's operator claim come from the same .well-known/polis.
	fetch = memoFetch(fetch)
	signerWK, err := fetchWellKnown(fetch, signer)
	if err != nil {
		return c.stop(1, StepUnknown, "", "could not read the signer's key: "+err.Error())
	}
	if signerWK.PublicKey == "" {
		return c.stop(1, StepUnknown, "", "https://"+signer+"/.well-known/polis publishes no public_key")
	}
	// ⭐ The foreign-issuer verifier (close-out F4c): the signer's current key,
	// then the key its own published history resolves for the action's
	// `asserted` — so an action signed before the signer rotated still verifies.
	switch v := sitecheck.VerifyIssuedAttestation(rec, sitecheck.IssuerFetch(fetch)); v.Status {
	case attestation.StatusValid:
		detail := "signature verifies against the key published at https://" + signer
		if d := v.KeyUsed.Describe(); d != "" {
			detail = "signature " + d + " (signer: https://" + signer + ")"
		}
		c.stepKey(1, StepPassed, withUncovered(detail, rec.UnrecognisedFields()), v.KeyUsed)
	case attestation.StatusUnsigned:
		return c.stop(1, StepFailed, FindingUnsigned,
			"the action carries no signature, so nothing binds it to "+signer)
	case attestation.StatusUnknown:
		detail := "could not check the signature against https://" + signer
		if v.Err != nil {
			detail += ": " + v.Err.Error()
		}
		return c.stop(1, StepUnknown, "", detail)
	default:
		detail := "the signature does NOT verify against the key published at https://" + signer +
			" — the action is not from this domain, whatever it says"
		if v.Err != nil {
			detail += " (" + v.Err.Error() + ")"
		}
		if v.ChainNote != "" {
			detail += " — " + v.ChainNote
		}
		return c.stop(1, StepFailed, FindingSignatureInvalid, detail)
	}

	// ---- step 2: the operator, and its registry pointer
	operator := strings.ToLower(strings.TrimSpace(givenOperator))
	claimed := strings.ToLower(strings.TrimSpace(signerWK.Operator))
	switch {
	case operator != "":
		c.OperatorSource = "given"
	case claimed != "":
		operator = claimed
		c.OperatorSource = "claimed"
	case signerWK.ActorRegistry != "":
		// ⚠️ A signer that publishes its OWN registry is an operator, and an
		// operator lists itself when it signs things (polis.polis.pub does). Its
		// own `operator` field is defined for ACTOR sites only (custody.md §6), so
		// it is absent here — and reading that absence as "names no operator"
		// would make every operator's own actions uncheckable. custody.md is
		// silent on this case; epic 33 escalates it.
		operator = signer
		c.OperatorSource = "self"
	default:
		return c.stop(2, StepNotApplicable, "",
			signer+" names no operator, so this action is not presented as anyone's actor. "+
				"To ask whether a specific operator lists it, pass --operator <domain>")
	}
	c.Operator = operator
	claimNote := ""
	switch {
	case c.OperatorSource == "given" && claimed == "":
		claimNote = " (" + signer + " itself names no operator)"
	case c.OperatorSource == "given" && claimed != operator:
		claimNote = " (" + signer + " claims " + claimed + " instead)"
	}

	opWK, err := fetchWellKnown(fetch, operator)
	if err != nil {
		return c.stop(2, StepUnknown, "", "could not read the operator's identity document: "+err.Error())
	}
	if opWK.ActorRegistry == "" {
		return c.stop(2, StepFailed, FindingUncorroborated,
			operator+" publishes no actor_registry pointer, so it lists no actors and nothing corroborates "+
				signer+" as one of them"+claimNote)
	}
	registryURL, err := resolvePointer(operator, opWK.ActorRegistry)
	if err != nil {
		return c.stop(2, StepFailed, FindingUncorroborated, err.Error())
	}
	c.RegistryURL = registryURL
	c.step(2, StepPassed, operator+" publishes a registry at "+registryURL+claimNote)

	// ---- step 3: the registry, verified against the operator's key — current
	// first, then the key the operator's own published history resolves for the
	// registry's `asserted` (E4): a registry signed before the operator rotated
	// is still the operator's statement.
	if opWK.PublicKey == "" {
		return c.stop(3, StepUnknown, "", "https://"+operator+"/.well-known/polis publishes no public_key")
	}
	body, err := fetch(registryURL)
	if err != nil {
		return c.stop(3, StepUnknown, "", "could not fetch the registry: "+err.Error())
	}
	// ⭐ Parse, never json.Unmarshal: it keeps the members this build does not
	// model, so a newer registry reads "could not check", not "forged".
	reg, err := Parse(body)
	if err != nil {
		return c.stop(3, StepFailed, FindingRegistryUntrusted, "the registry does not parse: "+err.Error())
	}
	opKeys := identityKeys(fetch, operator, opWK.PublicKey)
	status, retired, verr := verifyRegistryWithHistory(reg, opKeys)
	switch status {
	case StatusValid:
	case StatusUnsigned:
		return c.stop(3, StepFailed, FindingRegistryUntrusted,
			"the registry is unsigned, so it is not the operator's statement and nothing in it counts")
	case StatusUnknown:
		detail := "could not check the registry's signature against " + operator + "'s keys"
		if verr != nil {
			detail += " (" + verr.Error() + ")"
		}
		return c.stop(3, StepUnknown, "", detail)
	default:
		detail := "the registry does NOT verify against " + operator + "'s published key, so nothing in it counts"
		if verr != nil {
			detail += " (" + verr.Error() + ")"
		}
		if opKeys.ChainNote != "" {
			detail += " — " + opKeys.ChainNote
		}
		return c.stop(3, StepFailed, FindingRegistryUntrusted, detail)
	}
	if !strings.EqualFold(reg.Operator, operator) {
		return c.stop(3, StepFailed, FindingRegistryUntrusted,
			"the registry verifies but names "+reg.Operator+" as its operator while being served by "+operator)
	}
	used := sitecheck.KeyUsedFor(opKeys.Chain, retired, reg.Asserted)
	detail := "the registry verifies against " + operator + "'s published key"
	if d := used.Describe(); d != "" {
		detail = "the registry is " + operator + "'s statement: its signature " + d
	}
	c.stepKey(3, StepPassed, withUncovered(detail, reg.UnrecognisedFields()), used)

	// ---- step 4: is the signer listed?
	entry := reg.Find(signer)
	if entry == nil {
		return c.stop(4, StepFailed, FindingLookalike,
			operator+"'s registry does not list "+signer+" — a LOOKALIKE: the operator does not claim this domain")
	}
	c.step(4, StepPassed, operator+" lists "+signer+" ("+entry.Authority+" authority)")

	// ---- step 5: the actor's countersignature, against step 1's key — then,
	// if that fails, against the retired keys the actor's own trusted history
	// names (E4).
	signerKeys := identityKeys(fetch, signer, signerWK.PublicKey)
	switch status, verr := VerifyCountersignature(reg, entry, []byte(signerWK.PublicKey)); status {
	case StatusValid:
		c.step(5, StepPassed, withUncovered(signer+" countersigned this entry — it agrees to being listed, and to this scope",
			reg.entryUnrecognised(entry)))
	case StatusUnsigned:
		c.step(5, StepWeaker, "no countersignature — the entry says \"we say this is ours\", not \"and it agrees\". A weaker claim, not a broken one")
	case StatusUnknown:
		detail := "could not check the countersignature against " + signer + "'s key"
		if verr != nil {
			detail += " (" + verr.Error() + ")"
		}
		c.step(5, StepUnknown, detail)
	default:
		if old := retiredCountersigner(reg, entry, signerKeys); old != nil {
			// ⛔ WEAKER, NEVER A PLAIN PASS. A countersignature signs no time
			// (EntryCanonicalJSON excludes `asserted` on purpose), so nothing
			// places it inside the retired key's window — the one constraint
			// that stops a retired key speaking after it was retired (epic 31
			// D4). Weaker is exactly the standing of NO countersignature, so a
			// retired key compromised later buys a forger nothing.
			epoch := old.Epoch
			c.stepKey(5, StepWeaker, withUncovered(fmt.Sprintf("the countersignature verifies against a RETIRED key of %s (epoch %d, current %s → %s), "+
				"not the key it publishes now. A countersignature carries no signing time, so nothing shows it was made while that key was current: "+
				"%s agreed to this entry under a key it has since replaced — a weaker claim, not a broken one. A fresh countersignature restores the full claim",
				signer, old.Epoch, old.ValidFrom, old.ValidUntil, signer), reg.entryUnrecognised(entry)),
				&sitecheck.KeyUsed{Source: sitecheck.KeyRetired, Epoch: &epoch, ValidFrom: old.ValidFrom, ValidUntil: old.ValidUntil})
			break
		}
		detail := "the countersignature does NOT verify against " + signer + "'s key — the operator has changed this entry since the actor agreed to it"
		if verr != nil {
			detail += " (" + verr.Error() + ")"
		}
		// R2-1: a rotated actor whose own chain cannot be used must not read as
		// the operator's tampering — a failure after a rotation is never
		// indistinguishable from it (sitecheck/resolve.go).
		if signerKeys.ChainNote != "" {
			detail += " — " + signerKeys.ChainNote
		}
		c.fail(5, FindingCountersignatureInvalid, detail)
	}

	// ---- step 6: the action's type against expected_actions
	for _, a := range entry.ExpectedActions {
		if a == c.Action.Type {
			c.step(6, StepPassed, c.Action.Type+" is among "+signer+"'s expected actions")
			return c
		}
	}
	expected := "nothing"
	if len(entry.ExpectedActions) > 0 {
		expected = strings.Join(entry.ExpectedActions, ", ")
	}
	c.fail(6, FindingDeviation,
		c.Action.Type+" is NOT among "+signer+"'s expected actions ("+expected+") — a DEVIATION. "+
			"This is information to report and ask about, not a violation the protocol blocked")
	return c
}

// stepKey is step with the key that verified it, kept only when that key was
// RETIRED — a current-key pass carries no KeyUsed, so the member's presence is
// the fact.
func (c *StrangerCheck) stepKey(n int, o StepOutcome, detail string, used *sitecheck.KeyUsed) {
	c.step(n, o, detail)
	if used != nil && used.Source == sitecheck.KeyRetired {
		c.Steps[len(c.Steps)-1].KeyUsed = used
	}
}

// withUncovered appends the epic-47 row-2 sentence when a `valid` signature
// left members this build does not model uncovered — the MUST on
// UnrecognisedFields. Unchanged when there are none.
func withUncovered(detail string, unrecognised []string) string {
	if note := signing.UncoveredNote(unrecognised); note != "" {
		return detail + " — " + note
	}
	return detail
}

func (c *StrangerCheck) step(n int, o StepOutcome, detail string) {
	c.Steps = append(c.Steps, Step{Step: n, Name: stepNames[n], Outcome: o, Detail: detail})
}

func (c *StrangerCheck) fail(n int, kind, detail string) {
	c.step(n, StepFailed, detail)
	c.Findings = append(c.Findings, Finding{Kind: kind, Step: n, Detail: detail})
}

// stop records step n and marks every later step not reached — §7 says where
// the check must stop, and a reader should see which steps never ran.
func (c *StrangerCheck) stop(n int, o StepOutcome, kind, detail string) *StrangerCheck {
	if kind != "" {
		c.fail(n, kind, detail)
	} else {
		c.step(n, o, detail)
	}
	for i := n + 1; i <= 6; i++ {
		c.step(i, StepNotReached, "")
	}
	return c
}

type publishedIdentity struct {
	PublicKey     string `json:"public_key"`
	Operator      string `json:"operator"`
	ActorRegistry string `json:"actor_registry"`
}

// identityKeys reads a domain's current key and TRUSTED key history from its
// .well-known/polis — already fetched by an earlier step, so memoFetch serves
// the same bytes. The chain has passed sitecheck's trust rule (transitions
// verify, head is the published key) or is nil. Never nil itself: without a
// readable document it holds the key the caller already had.
func identityKeys(fetch Fetcher, domain, publicKey string) *sitecheck.IssuerKeys {
	fallback := &sitecheck.IssuerKeys{PublicKey: []byte(publicKey)}
	body, err := fetch("https://" + domain + "/.well-known/polis")
	if err != nil {
		return fallback
	}
	keys, err := sitecheck.IssuerKeysFromWellKnown("https://"+domain, body)
	if err != nil {
		return fallback
	}
	return keys
}

// verifyRegistryWithHistory is Verify for an operator that may have rotated:
// its current key, then the key its trusted history resolves for the
// registry's `asserted` — the registry's own claimed signing time, inside the
// operator's signature. retired is the history entry that verified it; nil for
// the current key. The epic-47 tolerance rule applies after the walk.
func verifyRegistryWithHistory(reg *Registry, keys *sitecheck.IssuerKeys) (SignatureStatus, *site.KeyHistoryEntry, error) {
	if reg.Signature == "" {
		return StatusUnsigned, nil, nil
	}
	if len(keys.PublicKey) == 0 {
		return StatusUnknown, nil, nil
	}
	canonical, err := CanonicalJSON(reg)
	if err != nil {
		return StatusUnknown, nil, fmt.Errorf("canonical JSON: %w", err)
	}
	ok, retired, verr := site.VerifyWithHistory(func(key []byte) (bool, error) {
		return signing.VerifySignature(canonical, key, reg.Signature)
	}, keys.PublicKey, keys.Chain, reg.Asserted, "asserted")
	switch out := signing.Resolve(ok, reg.UnrecognisedFields()); out.Status {
	case signing.StatusUnknown:
		return StatusUnknown, nil, errors.New(out.Explain())
	case signing.StatusInvalid:
		return StatusInvalid, nil, verr
	}
	return StatusValid, retired, nil
}

// retiredCountersigner returns the retired key in the actor's trusted history
// that verifies e's countersignature, newest first, or nil. Only keys the
// chain's verified transitions vouch for are tried — never an arbitrary key.
func retiredCountersigner(reg *Registry, e *Entry, keys *sitecheck.IssuerKeys) *site.KeyHistoryEntry {
	if keys == nil || keys.Chain == nil {
		return nil
	}
	for i := len(keys.Chain.History) - 1; i >= 0; i-- {
		h := keys.Chain.History[i]
		if st, _ := VerifyCountersignature(reg, e, []byte(h.Key)); st == StatusValid {
			return &h
		}
	}
	return nil
}

// memoFetch remembers each URL's result for the life of one check.
func memoFetch(fetch Fetcher) Fetcher {
	type result struct {
		body []byte
		err  error
	}
	seen := map[string]result{}
	return func(u string) ([]byte, error) {
		if r, ok := seen[u]; ok {
			return r.body, r.err
		}
		b, err := fetch(u)
		seen[u] = result{b, err}
		return b, err
	}
}

func fetchWellKnown(fetch Fetcher, domain string) (*publishedIdentity, error) {
	body, err := fetch("https://" + domain + "/.well-known/polis")
	if err != nil {
		return nil, err
	}
	var wk publishedIdentity
	if err := json.Unmarshal(body, &wk); err != nil {
		return nil, fmt.Errorf("https://%s/.well-known/polis does not parse: %v", domain, err)
	}
	return &wk, nil
}

func recIssuer(rec *attestation.Record) string {
	if rec == nil {
		return ""
	}
	return rec.Issuer
}

// domainOf returns the lower-cased host of an https base URL.
func domainOf(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("issuer %q is not a URL", base)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("issuer %q is not an https origin, so its key cannot be fetched with any assurance", base)
	}
	return strings.ToLower(u.Host), nil
}

// resolvePointer turns an actor_registry pointer into a URL on the operator's
// own origin. ⛔ A pointer that leaves the origin is refused: the registry must
// be served by the party whose key signs it, or step 3 proves nothing about who
// published it.
func resolvePointer(operator, pointer string) (string, error) {
	base := &url.URL{Scheme: "https", Host: operator, Path: "/"}
	ref, err := url.Parse(pointer)
	if err != nil {
		return "", fmt.Errorf("%s's actor_registry pointer %q is not a URL", operator, pointer)
	}
	u := base.ResolveReference(ref)
	if u.Scheme != "https" || !strings.EqualFold(u.Host, operator) {
		return "", fmt.Errorf("%s's actor_registry pointer %q leaves the operator's own https origin", operator, pointer)
	}
	return u.String(), nil
}
