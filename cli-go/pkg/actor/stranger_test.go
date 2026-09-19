package actor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitecheck"
)

// strangerWorld is a tiny network: an operator, the actor it lists, and
// whatever else a test adds, served through a map-backed Fetcher.
type strangerWorld struct {
	t       *testing.T
	files   map[string][]byte
	keys    map[string][]byte // domain → private key
	pubs    map[string]string // domain → public key
	reg     *Registry
	regPath string
	retired map[string][]byte // domain → the key a rotation retired
}

func newStrangerWorld(t *testing.T) *strangerWorld {
	w := &strangerWorld{
		t:       t,
		files:   map[string][]byte{},
		keys:    map[string][]byte{},
		pubs:    map[string]string{},
		regPath: "/content/pub.polis.core/actor/registry.json",
	}
	w.site("op.example", "")
	w.site("judge.example", "op.example")
	w.reg = &Registry{
		V:        SchemaVersion,
		Operator: "op.example",
		Actors: []Entry{{
			Domain:          "judge.example",
			Authority:       AuthorityOperator,
			ExpectedActions: []string{attestation.PredicateIntegrity},
		}},
		Asserted:  "2026-09-13T00:00:00Z",
		Generator: "polis-cli-go/test",
	}
	w.countersign("judge.example")
	w.publishRegistry()
	return w
}

func (w *strangerWorld) site(domain, operator string) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		w.t.Fatal(err)
	}
	w.keys[domain] = priv
	w.pubs[domain] = strings.TrimSpace(string(pub))
	w.writeWellKnown(domain, operator)
}

func (w *strangerWorld) writeWellKnown(domain, operator string) {
	wk := map[string]string{"public_key": w.pubs[domain]}
	if operator != "" {
		wk["operator"] = operator
	}
	if domain == "op.example" {
		wk["actor_registry"] = w.regPath
	}
	body, _ := json.Marshal(wk)
	w.files["https://"+domain+"/.well-known/polis"] = body
}

func (w *strangerWorld) countersign(domain string) {
	e := w.reg.Find(domain)
	sig, err := Countersign(w.reg, e, w.keys[domain])
	if err != nil {
		w.t.Fatal(err)
	}
	e.Countersignature = sig
}

func (w *strangerWorld) publishRegistry() {
	if err := Sign(w.reg, w.keys["op.example"]); err != nil {
		w.t.Fatal(err)
	}
	body, _ := json.Marshal(w.reg)
	w.files["https://op.example"+w.regPath] = body
}

// action signs an attestation as domain.
func (w *strangerWorld) action(domain, predicate string) *attestation.Record {
	r := &attestation.Record{
		Type:      attestation.TypeName,
		Issuer:    "https://" + domain,
		Predicate: predicate,
		Subject:   attestation.Subject{Type: attestation.SubjectIdentity, ID: "https://someone.example"},
		Asserted:  "2026-09-13T01:00:00Z",
		Generator: "polis-cli-go/test",
	}
	canonical, err := attestation.CanonicalJSON(r)
	if err != nil {
		w.t.Fatal(err)
	}
	r.Version = attestation.ContentVersion(canonical)
	if r.Signature, err = signing.SignContent(canonical, w.keys[domain]); err != nil {
		w.t.Fatal(err)
	}
	return r
}

func (w *strangerWorld) fetch(u string) ([]byte, error) {
	if b, ok := w.files[u]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("HTTP 404 from %s", u)
}

func kinds(c *StrangerCheck) []string {
	out := []string{}
	for _, f := range c.Findings {
		out = append(out, f.Kind)
	}
	return out
}

func outcomes(c *StrangerCheck) string {
	var parts []string
	for _, s := range c.Steps {
		parts = append(parts, fmt.Sprintf("%d:%s", s.Step, s.Outcome))
	}
	return strings.Join(parts, " ")
}

func assertKinds(t *testing.T, c *StrangerCheck, want ...string) {
	t.Helper()
	got := kinds(c)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("findings = %v, want %v\nsteps: %s", got, want, outcomes(c))
	}
}

func assertSteps(t *testing.T, c *StrangerCheck, want string) {
	t.Helper()
	if got := outcomes(c); got != want {
		t.Errorf("steps = %s\n want %s", got, want)
	}
}

// A listed, countersigned actor doing what it said it would: all six steps
// pass and there is nothing to report.
func TestStrangerCheck_ListedActorDoingItsExpectedAction(t *testing.T) {
	w := newStrangerWorld(t)
	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c)
	assertSteps(t, c, "1:passed 2:passed 3:passed 4:passed 5:passed 6:passed")
	if c.Operator != "op.example" || c.OperatorSource != "claimed" {
		t.Errorf("operator = %q (%s)", c.Operator, c.OperatorSource)
	}
}

// ⭐ The canary shape: a listed actor signing something off its list. Steps 1–5
// all pass — the signature is real and the actor is genuinely listed — and the
// one finding is a DEVIATION.
func TestStrangerCheck_OffListActionIsADeviation(t *testing.T) {
	w := newStrangerWorld(t)
	c := CheckAction(w.action("judge.example", attestation.PredicateEndorsement), "x", "", w.fetch)
	assertKinds(t, c, FindingDeviation)
	assertSteps(t, c, "1:passed 2:passed 3:passed 4:passed 5:passed 6:failed")
}

// A lookalike that claims the operator: step 4 is conclusive and step 6 never
// runs — a domain nobody lists has no expected actions to deviate from.
func TestStrangerCheck_UnlistedSignerClaimingTheOperatorIsALookalike(t *testing.T) {
	w := newStrangerWorld(t)
	w.site("judge-example.example", "op.example")
	c := CheckAction(w.action("judge-example.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c, FindingLookalike)
	assertSteps(t, c, "1:passed 2:passed 3:passed 4:failed 5:not_reached 6:not_reached")
}

// ⛔ custody.md §10: a lookalike and a deviation are DIFFERENT findings. The two
// tests above prove each alone; this proves they never collapse into one kind.
func TestStrangerCheck_LookalikeAndDeviationAreDifferentKinds(t *testing.T) {
	if FindingLookalike == FindingDeviation {
		t.Fatal("lookalike and deviation share a kind")
	}
	w := newStrangerWorld(t)
	w.site("judge-example.example", "op.example")
	look := CheckAction(w.action("judge-example.example", attestation.PredicateEndorsement), "x", "", w.fetch)
	dev := CheckAction(w.action("judge.example", attestation.PredicateEndorsement), "x", "", w.fetch)
	if strings.Join(kinds(look), ",") == strings.Join(kinds(dev), ",") {
		t.Errorf("an unlisted signer and a listed actor off its list reported the same findings: %v", kinds(look))
	}
}

// A lookalike that simply omits the operator claim is caught when the stranger
// names the operator they are asking about.
func TestStrangerCheck_GivenOperatorCatchesASilentLookalike(t *testing.T) {
	w := newStrangerWorld(t)
	w.site("judge-example.example", "")

	silent := CheckAction(w.action("judge-example.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, silent)
	assertSteps(t, silent, "1:passed 2:not_applicable 3:not_reached 4:not_reached 5:not_reached 6:not_reached")

	asked := CheckAction(w.action("judge-example.example", attestation.PredicateIntegrity), "x", "op.example", w.fetch)
	assertKinds(t, asked, FindingLookalike)
	if asked.OperatorSource != "given" {
		t.Errorf("operator_source = %q, want given", asked.OperatorSource)
	}
}

// An operator signing as itself: its site publishes a registry and (correctly,
// per custody.md §6) no `operator` claim. The check consults its own registry
// rather than stopping, and a self-entry off its own list is still a deviation.
func TestStrangerCheck_OperatorSigningAsItselfUsesItsOwnRegistry(t *testing.T) {
	w := newStrangerWorld(t)
	w.reg.Actors = append(w.reg.Actors, Entry{
		Domain: "op.example", Authority: AuthorityOperator,
		ExpectedActions: []string{attestation.PredicateAgentDisclosure},
	})
	w.publishRegistry()

	c := CheckAction(w.action("op.example", attestation.PredicateAgentDisclosure), "x", "", w.fetch)
	assertKinds(t, c)
	assertSteps(t, c, "1:passed 2:passed 3:passed 4:passed 5:weaker 6:passed")
	if c.OperatorSource != "self" || c.Operator != "op.example" {
		t.Errorf("operator = %q (%s), want op.example (self)", c.Operator, c.OperatorSource)
	}

	off := CheckAction(w.action("op.example", attestation.PredicateEndorsement), "x", "", w.fetch)
	assertKinds(t, off, FindingDeviation)
}

// No countersignature is a weaker claim, never a finding.
func TestStrangerCheck_AbsentCountersignatureIsWeakerNotAFinding(t *testing.T) {
	w := newStrangerWorld(t)
	w.reg.Actors[0].Countersignature = ""
	w.publishRegistry()
	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c)
	assertSteps(t, c, "1:passed 2:passed 3:passed 4:passed 5:weaker 6:passed")
}

// ⭐ The asymmetry the countersignature exists for: the operator widens the
// entry after the actor consented, re-signs the file, and the countersignature
// stops verifying. Step 6 still runs — against the widened list.
func TestStrangerCheck_WidenedEntryBreaksTheCountersignature(t *testing.T) {
	w := newStrangerWorld(t)
	w.reg.Actors[0].ExpectedActions = append(w.reg.Actors[0].ExpectedActions, attestation.PredicateEndorsement)
	w.publishRegistry()
	c := CheckAction(w.action("judge.example", attestation.PredicateEndorsement), "x", "", w.fetch)
	assertKinds(t, c, FindingCountersignatureInvalid)
	assertSteps(t, c, "1:passed 2:passed 3:passed 4:passed 5:failed 6:passed")
}

// custody.md §10: a countersignature lifted from another operator's registry
// must not verify, because the entry base binds the operator.
func TestStrangerCheck_CountersignatureLiftedFromAnotherOperatorFails(t *testing.T) {
	w := newStrangerWorld(t)
	lifted := w.reg.Actors[0].Countersignature

	w.site("rogue.example", "")
	w.files["https://rogue.example/.well-known/polis"], _ = json.Marshal(map[string]string{
		"public_key": w.pubs["rogue.example"], "actor_registry": w.regPath,
	})
	rogue := &Registry{
		V: SchemaVersion, Operator: "rogue.example",
		Actors: []Entry{{
			Domain: "judge.example", Authority: AuthorityOperator,
			ExpectedActions:  []string{attestation.PredicateIntegrity},
			Countersignature: lifted,
		}},
		Asserted: "2026-09-13T00:00:00Z", Generator: "polis-cli-go/test",
	}
	if err := Sign(rogue, w.keys["rogue.example"]); err != nil {
		t.Fatal(err)
	}
	w.files["https://rogue.example"+w.regPath], _ = json.Marshal(rogue)

	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "rogue.example", w.fetch)
	assertKinds(t, c, FindingCountersignatureInvalid)
}

// A registry that does not verify is not the operator's statement: stop at 3,
// and in particular never report a lookalike on the strength of it.
func TestStrangerCheck_TamperedRegistryCountsForNothing(t *testing.T) {
	w := newStrangerWorld(t)
	var tampered Registry
	_ = json.Unmarshal(w.files["https://op.example"+w.regPath], &tampered)
	tampered.Actors = nil
	w.files["https://op.example"+w.regPath], _ = json.Marshal(tampered)

	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c, FindingRegistryUntrusted)
	assertSteps(t, c, "1:passed 2:passed 3:failed 4:not_reached 5:not_reached 6:not_reached")
}

// A registry correctly signed by one operator but served from another origin
// says nothing about the origin serving it.
func TestStrangerCheck_RegistryNamingAnotherOperatorIsUntrusted(t *testing.T) {
	w := newStrangerWorld(t)
	w.reg.Operator = "elsewhere.example"
	w.publishRegistry()
	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c, FindingRegistryUntrusted)
}

// An operator with no registry pointer corroborates nothing.
func TestStrangerCheck_OperatorWithoutARegistryIsUncorroborated(t *testing.T) {
	w := newStrangerWorld(t)
	w.files["https://op.example/.well-known/polis"], _ = json.Marshal(map[string]string{"public_key": w.pubs["op.example"]})
	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c, FindingUncorroborated)
}

// ⛔ A pointer that leaves the operator's origin is refused: step 3 would then
// verify a file the operator does not serve.
func TestStrangerCheck_PointerOffTheOperatorsOriginIsRefused(t *testing.T) {
	w := newStrangerWorld(t)
	w.files["https://op.example/.well-known/polis"], _ = json.Marshal(map[string]string{
		"public_key": w.pubs["op.example"], "actor_registry": "https://elsewhere.example" + w.regPath,
	})
	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c, FindingUncorroborated)
}

// Step 1 stops the check: an action that does not verify against the key its
// signer publishes is not from that signer, so nothing about the signer applies.
func TestStrangerCheck_ForgedActionStopsAtStepOne(t *testing.T) {
	w := newStrangerWorld(t)
	r := w.action("judge.example", attestation.PredicateEndorsement)
	r.Subject.ID = "https://someone-else.example" // alter after signing
	c := CheckAction(r, "x", "", w.fetch)
	assertKinds(t, c, FindingSignatureInvalid)
	assertSteps(t, c, "1:failed 2:not_reached 3:not_reached 4:not_reached 5:not_reached 6:not_reached")

	r.Signature = ""
	assertKinds(t, CheckAction(r, "x", "", w.fetch), FindingUnsigned)
}

// Could not look is not looked-and-found: an unreachable operator yields no
// finding at all.
func TestStrangerCheck_UnreachableOperatorIsUnknownNotAFinding(t *testing.T) {
	w := newStrangerWorld(t)
	delete(w.files, "https://op.example/.well-known/polis")
	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c)
	assertSteps(t, c, "1:passed 2:unknown 3:not_reached 4:not_reached 5:not_reached 6:not_reached")
}

// Findings is [] rather than null, so a script can range over it unconditionally.
func TestStrangerCheck_FindingsMarshalsAsAnEmptyList(t *testing.T) {
	w := newStrangerWorld(t)
	body, _ := json.Marshal(CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch))
	if !strings.Contains(string(body), `"findings":[]`) {
		t.Errorf("findings did not marshal as []: %s", body)
	}
}

// rotate moves domain onto a fresh key through site.RecordKeyRotation — the
// seam every rotation path calls — and republishes its .well-known/polis with
// the resulting history. The old key stays usable for signing old actions.
func (w *strangerWorld) rotate(domain, operator, rotatedAt string) (oldKey []byte) {
	w.t.Helper()
	dir := w.t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		w.t.Fatal(err)
	}
	doc := map[string]string{"public_key": w.pubs[domain], "created": "2026-03-03T05:35:09Z"}
	if operator != "" {
		doc["operator"] = operator
	}
	if domain == "op.example" {
		doc["actor_registry"] = w.regPath
	}
	wk, _ := json.Marshal(doc)
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), wk, 0644); err != nil {
		w.t.Fatal(err)
	}
	if _, err := site.WriteGenesisKeyHistory(dir); err != nil {
		w.t.Fatal(err)
	}
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		w.t.Fatal(err)
	}
	newPub := strings.TrimSpace(string(pub))
	canonical, err := discovery.MakeKeyRotationCanonicalJSON(domain, w.pubs[domain], newPub, rotatedAt)
	if err != nil {
		w.t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, w.keys[domain])
	if err != nil {
		w.t.Fatal(err)
	}
	if err := site.RecordKeyRotation(dir, newPub, sig, rotatedAt); err != nil {
		w.t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		w.t.Fatal(err)
	}
	w.files["https://"+domain+"/.well-known/polis"] = body
	oldKey = w.keys[domain]
	if w.retired == nil {
		w.retired = map[string][]byte{}
	}
	w.retired[domain] = oldKey
	w.keys[domain], w.pubs[domain] = priv, newPub
	return oldKey
}

// Close-out F4c: an action signed BEFORE its signer rotated still verifies at
// step 1, through the signer's own published key history. Before this, step 1
// checked the signer's CURRENT key only, so every record a rotated actor ever
// signed read as a forgery here — permanently, for every stranger.
func TestStrangerCheck_ActionSignedBeforeTheSignerRotatedStillVerifies(t *testing.T) {
	w := newStrangerWorld(t)
	oldKey := w.keys["judge.example"]
	r := w.action("judge.example", attestation.PredicateIntegrity) // asserted 2026-09-13
	w.rotate("judge.example", "op.example", "2026-09-15T10:22:03Z")
	// The actor re-agrees to its entry under its new key, as it would.
	w.countersign("judge.example")
	w.publishRegistry()

	c := CheckAction(r, "x", "", w.fetch)
	assertKinds(t, c)
	assertSteps(t, c, "1:passed 2:passed 3:passed 4:passed 5:passed 6:passed")
	if !strings.Contains(c.Steps[0].Detail, "RETIRED") {
		t.Errorf("step 1 passed on a retired key and does not say so: %q", c.Steps[0].Detail)
	}

	// The retired key's window closed at the rotation: an action it signs
	// claiming a later time is still a forgery.
	late := w.action("judge.example", attestation.PredicateIntegrity)
	late.Asserted = "2026-09-16T00:00:00Z"
	canonical, _ := attestation.CanonicalJSON(late)
	late.Version = attestation.ContentVersion(canonical)
	late.Signature, _ = signing.SignContent(canonical, oldKey)
	assertKinds(t, CheckAction(late, "x", "", w.fetch), FindingSignatureInvalid)
}

// SIGNET epic 47 at step 1: an action that does not verify while carrying a
// member this build does not model is "could not check", never a forgery —
// the signature may cover a field set this build cannot rebuild.
func TestStrangerCheck_UnverifiableActionWithAnUnknownFieldIsUnknownNotAFinding(t *testing.T) {
	w := newStrangerWorld(t)
	r := w.action("judge.example", attestation.PredicateIntegrity)
	raw, _ := json.Marshal(r)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["from_the_future"] = "x"             // e.g. a newer writer signed a field we lack...
	m["asserted"] = "2026-09-13T02:00:00Z" // ...so the rebuilt bytes do not verify
	raw, _ = json.Marshal(m)
	parsed, err := attestation.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := CheckAction(parsed, "x", "", w.fetch)
	assertKinds(t, c)
	assertSteps(t, c, "1:unknown 2:not_reached 3:not_reached 4:not_reached 5:not_reached 6:not_reached")
}

// E4 (round 2), step 3: a registry the operator signed BEFORE it rotated is
// still the operator's statement. Before this, step 3 checked the operator's
// CURRENT key only and emitted registry_untrusted — accusing a correct site.
func TestStrangerCheck_RegistrySignedBeforeTheOperatorRotatedStillCounts(t *testing.T) {
	w := newStrangerWorld(t) // registry asserted 2026-09-13, signed by op's genesis key
	w.rotate("op.example", "", "2026-09-15T10:22:03Z")

	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c)
	assertSteps(t, c, "1:passed 2:passed 3:passed 4:passed 5:passed 6:passed")
	if d := c.Steps[2].Detail; !strings.Contains(d, "RETIRED") || !strings.Contains(d, "op.example") {
		t.Errorf("step 3 passed on a retired key and does not say so: %q", d)
	}

	// The retired key's window closed at the rotation: a registry it signs
	// claiming a later time is not the operator's statement.
	w.reg.Asserted = "2026-09-16T00:00:00Z"
	w.reg.Signature = ""
	canonical, _ := CanonicalJSON(w.reg)
	w.reg.Signature, _ = signing.SignContent(canonical, w.retired["op.example"])
	body, _ := json.Marshal(w.reg)
	w.files["https://op.example"+w.regPath] = body
	assertKinds(t, CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch), FindingRegistryUntrusted)
}

// E4 (round 2), step 5: a countersignature the actor made BEFORE it rotated
// is genuine consent under a key the actor really held. It is not a finding —
// before this it read as countersignature_invalid, "the operator changed this
// entry", an accusation against a correct operator. ⚠️ A countersignature
// carries no signing time, so nothing places it inside that key's window: it
// is reported WEAKER, never as a plain pass.
func TestStrangerCheck_CountersignatureMadeBeforeTheActorRotatedIsWeakerNotAFinding(t *testing.T) {
	w := newStrangerWorld(t) // judge countersigned with its genesis key
	w.rotate("judge.example", "op.example", "2026-09-15T10:22:03Z")
	w.publishRegistry() // the operator restates the list later; the entry is unchanged

	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c)
	assertSteps(t, c, "1:passed 2:passed 3:passed 4:passed 5:weaker 6:passed")
	if d := c.Steps[4].Detail; !strings.Contains(d, "RETIRED") || !strings.Contains(d, "no signing time") {
		t.Errorf("step 5 must say it matched a retired key and why that is weaker: %q", d)
	}

	// A countersignature by a key the actor NEVER held is still a finding.
	stranger, _, _ := signing.GenerateKeypair()
	e := w.reg.Find("judge.example")
	e.Countersignature, _ = Countersign(w.reg, e, stranger)
	w.publishRegistry()
	assertKinds(t, CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch), FindingCountersignatureInvalid)
}

// R2-4: step 5's two `weaker` cases are told apart by a member, not by prose.
// key_used is present when a RETIRED key verified the step and absent on a
// current-key pass and on a step that verified nothing.
func TestStrangerCheck_KeyUsedSeparatesTheTwoWeakerCases(t *testing.T) {
	// Current keys throughout: no step carries key_used, and the JSON has none.
	w := newStrangerWorld(t)
	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	for _, s := range c.Steps {
		if s.KeyUsed != nil {
			t.Errorf("step %d: key_used on a current-key pass: %+v", s.Step, s.KeyUsed)
		}
	}
	if raw, _ := json.Marshal(c); strings.Contains(string(raw), "key_used") {
		t.Errorf("key_used serialised on a current-key check: %s", raw)
	}

	// weaker case 1 — no countersignature: weaker, no key_used.
	w.reg.Find("judge.example").Countersignature = ""
	w.publishRegistry()
	c = CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	if s := c.Steps[4]; s.Outcome != StepWeaker || s.KeyUsed != nil {
		t.Fatalf("absent countersignature: %+v", s)
	}

	// weaker case 2 — countersigned under a key the actor has retired.
	w = newStrangerWorld(t)
	w.rotate("judge.example", "op.example", "2026-09-15T10:22:03Z")
	w.publishRegistry()
	c = CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	s := c.Steps[4]
	if s.Outcome != StepWeaker || s.KeyUsed == nil || s.KeyUsed.Source != sitecheck.KeyRetired ||
		s.KeyUsed.Epoch == nil || *s.KeyUsed.Epoch != 0 || s.KeyUsed.ClaimedSigningTime != "" {
		t.Fatalf("retired countersignature: %+v key_used=%+v", s, s.KeyUsed)
	}
	if c.Steps[0].KeyUsed != nil {
		t.Errorf("step 1 verified by the CURRENT key carries key_used: %+v", c.Steps[0].KeyUsed)
	}

	// Step 3 — a retired operator key carries it too, with the claimed time.
	w = newStrangerWorld(t)
	w.rotate("op.example", "", "2026-09-15T10:22:03Z")
	c = CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	if u := c.Steps[2].KeyUsed; u == nil || u.Source != sitecheck.KeyRetired || u.ClaimedSigningTime != "2026-09-13T00:00:00Z" {
		t.Fatalf("step 3 retired pass: key_used=%+v", u)
	}

	// Step 1 — an action signed before its signer rotated.
	w = newStrangerWorld(t)
	r := w.action("judge.example", attestation.PredicateIntegrity)
	w.rotate("judge.example", "op.example", "2026-09-15T10:22:03Z")
	w.countersign("judge.example")
	w.publishRegistry()
	c = CheckAction(r, "x", "", w.fetch)
	if u := c.Steps[0].KeyUsed; u == nil || u.Source != sitecheck.KeyRetired {
		t.Fatalf("step 1 retired pass: key_used=%+v", u)
	}
}

// R2-1: a rotated actor whose OWN key history cannot be used is not the
// operator's tampering. The finding stands (nothing verified), but its text
// carries the chain note instead of accusing the operator alone.
func TestStrangerCheck_UnusableActorChainIsSaidAtStepFive(t *testing.T) {
	w := newStrangerWorld(t)
	w.rotate("judge.example", "op.example", "2026-09-15T10:22:03Z")
	var wk map[string]any
	_ = json.Unmarshal(w.files["https://judge.example/.well-known/polis"], &wk)
	cur := wk["public_key_history"].(map[string]any)["current"].(map[string]any)
	sig := cur["transition_sig"].(string)
	cur["transition_sig"] = sig[:len(sig)-8] + "AAAAAAA=" // break the handover
	w.files["https://judge.example/.well-known/polis"], _ = json.Marshal(wk)
	w.publishRegistry()

	c := CheckAction(w.action("judge.example", attestation.PredicateIntegrity), "x", "", w.fetch)
	assertKinds(t, c, FindingCountersignatureInvalid)
	if d := c.Steps[4].Detail; !strings.Contains(d, "could not be used to resolve retired keys") {
		t.Errorf("step 5 blames the operator without saying the actor's chain was unusable: %q", d)
	}
}

// R2-2: a `valid` signature over a document carrying members this build does
// not model MUST say they are not covered (epic 47 row 2) — at steps 1, 3
// and 5 alike.
func TestStrangerCheck_ValidWithUnrecognisedFieldsSaysSo(t *testing.T) {
	w := newStrangerWorld(t)
	// Registry: an extra top-level member, and one inside the actor's entry.
	// Neither is in any signing base this build rebuilds, so both signatures
	// still verify over the fields it knows.
	var reg map[string]any
	_ = json.Unmarshal(w.files["https://op.example"+w.regPath], &reg)
	reg["from_the_future"] = "x"
	reg["actors"].([]any)[0].(map[string]any)["entry_future"] = "y"
	w.files["https://op.example"+w.regPath], _ = json.Marshal(reg)
	// Action: an extra member, parsed the way the CLI parses it.
	raw, _ := json.Marshal(w.action("judge.example", attestation.PredicateIntegrity))
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	m["action_future"] = "z"
	raw, _ = json.Marshal(m)
	rec, err := attestation.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}

	c := CheckAction(rec, "x", "", w.fetch)
	assertSteps(t, c, "1:passed 2:passed 3:passed 4:passed 5:passed 6:passed")
	for i, field := range map[int]string{0: "action_future", 2: "from_the_future", 4: "entry_future"} {
		if d := c.Steps[i].Detail; !strings.Contains(d, field) || !strings.Contains(d, "not covered by the signature") {
			t.Errorf("step %d passed without saying %s is not covered: %q", i+1, field, d)
		}
	}
}
