package actor

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// custodyWorld is a tenant, her operator and a discovery service, served
// through a map-backed Fetcher — the whole check runs with no network.
type custodyWorld struct {
	t      *testing.T
	files  map[string][]byte
	keys   map[string][]byte
	pubs   map[string]string
	index  map[string][]string
	events []map[string]interface{}
}

const (
	cwTenant   = "alice.example"
	cwOperator = "op.example"
	cwDS       = "https://ds.example"
)

func newCustodyWorld(t *testing.T) *custodyWorld {
	w := &custodyWorld{
		t:     t,
		files: map[string][]byte{},
		keys:  map[string][]byte{},
		pubs:  map[string]string{},
		index: map[string][]string{},
	}
	w.site(cwTenant)
	w.site(cwOperator)
	w.site("bob.example")
	w.serveEvents()
	return w
}

func (w *custodyWorld) site(domain string) {
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		w.t.Fatal(err)
	}
	w.keys[domain] = priv
	w.pubs[domain] = strings.TrimSpace(string(pub))
	body, _ := json.Marshal(map[string]interface{}{
		"public_key": w.pubs[domain],
		"bundles": map[string]interface{}{
			"pub.polis.core": map[string]string{"path": "content/pub.polis.core/bundle.json"},
		},
	})
	w.files["https://"+domain+"/.well-known/polis"] = body
	w.writeIndex(domain)
}

func (w *custodyWorld) writeIndex(domain string) {
	w.files["https://"+domain+"/content/pub.polis.core/index.jsonl"] = []byte(strings.Join(w.index[domain], "\n") + "\n")
}

// publish signs r with the key of the domain its issuer names, serves it at its
// content URL and lists it in that site's index.
func (w *custodyWorld) publish(r *attestation.Record) string {
	domain := bareDomain(r.Issuer)
	r.Type = attestation.TypeName
	r.Generator = "polis-cli-go/test"
	canonical, err := attestation.CanonicalJSON(r)
	if err != nil {
		w.t.Fatal(err)
	}
	r.Version = attestation.ContentVersion(canonical)
	if r.Signature, err = signing.SignContent(canonical, w.keys[domain]); err != nil {
		w.t.Fatal(err)
	}
	id := attestation.ID(r)
	u := attestation.RecordURL(r.Issuer, id)
	w.serve(u, r)
	line, _ := json.Marshal(map[string]string{
		"type": "attestation", "title": r.Predicate,
		"path": "content/pub.polis.core/attestation/" + id + ".json",
	})
	w.index[domain] = append(w.index[domain], string(line))
	w.writeIndex(domain)
	return u
}

// serve (re)writes a record's bytes WITHOUT re-signing — for a pointer added
// afterwards, or a tamper.
func (w *custodyWorld) serve(u string, r *attestation.Record) {
	body, _ := json.Marshal(r)
	w.files[u] = body
}

func (w *custodyWorld) grant(asserted, basis string) (*attestation.Record, string) {
	r := &attestation.Record{
		Issuer:    "https://" + cwTenant,
		Predicate: attestation.PredicateCustodyGrant,
		Subject:   attestation.Subject{Type: attestation.SubjectIdentity, ID: "https://" + cwOperator},
		Payload:   map[string]string{attestation.CustodyKeyScope: attestation.CustodyScopeCustodial, attestation.CustodyKeyBasis: basis},
		Asserted:  asserted,
	}
	return r, w.publish(r)
}

func (w *custodyWorld) declaration(subject, asserted string) (*attestation.Record, string) {
	r := &attestation.Record{
		Issuer:    "https://" + cwOperator,
		Predicate: attestation.PredicateCustody,
		Subject:   attestation.Subject{Type: attestation.SubjectIdentity, ID: "https://" + subject},
		Payload: map[string]string{
			attestation.CustodyKeyHolds:       attestation.CustodyHoldsIdentityKey,
			attestation.CustodyKeyAttribution: attestation.CustodyAttributionAsTenant,
		},
		Asserted: asserted,
	}
	return r, w.publish(r)
}

func (w *custodyWorld) withdraw(target *attestation.Record, targetURL, asserted string) string {
	return w.publish(&attestation.Record{
		Issuer:    target.Issuer,
		Predicate: attestation.PredicateWithdrawal,
		Subject:   attestation.Subject{Type: attestation.SubjectURI, ID: targetURL, Version: target.Version},
		Asserted:  asserted,
	})
}

func (w *custodyWorld) serveEvents(events ...map[string]interface{}) {
	w.events = events
	body, _ := json.Marshal(map[string]interface{}{"events": events, "cursor": fmt.Sprint(len(events)), "has_more": false})
	w.files[cwDS+"/v1/stream?actor="+cwTenant+"&since=0&limit=1000"] = body
}

func event(id int, signedBy, authority string) map[string]interface{} {
	e := map[string]interface{}{
		"id": id, "type": "pub.polis.post.published", "created_at": "2026-09-14T00:00:00Z",
		"actor": cwTenant, "signature": "sig", "payload": map[string]interface{}{},
	}
	if authority != "" {
		e["signed_by"], e["principal"], e["authority"] = signedBy, cwTenant, authority
	}
	return e
}

func (w *custodyWorld) fetch(u string) ([]byte, error) {
	if b, ok := w.files[u]; ok {
		return b, nil
	}
	return nil, fmt.Errorf("HTTP 404 from %s", u)
}

func TestCustodyCheckReadsBothRecordsAndTheEvents(t *testing.T) {
	w := newCustodyWorld(t)
	w.grant("2026-09-14T01:00:00Z", attestation.CustodyBasisHostingTerms)
	w.declaration(cwTenant, "2026-09-14T00:00:00Z")
	w.declaration("bob.example", "2026-09-14T00:00:01Z") // about someone else — not hers
	w.serveEvents(
		event(1, cwTenant, "self"),
		event(2, cwTenant, "self"),
		event(3, "", ""), // stored before the service recorded authority
		event(4, "bob.example", "none"),
	)

	c := CheckCustody(cwTenant, "", cwDS, w.fetch)

	if c.Operator != cwOperator || c.OperatorSource != "granted" {
		t.Fatalf("operator = %q (%s), want %s from the grant", c.Operator, c.OperatorSource, cwOperator)
	}
	if len(c.Grants) != 1 || c.Grants[0].Signature != "valid" || c.Grants[0].Withdrawal != WithdrawalNotWithdrawn {
		t.Fatalf("grants = %+v", c.Grants)
	}
	if len(c.Declarations) != 1 || c.Declarations[0].Signature != "valid" ||
		c.Declarations[0].Payload[attestation.CustodyKeyAttribution] != attestation.CustodyAttributionAsTenant {
		t.Fatalf("declarations = %+v, want exactly the one about %s", c.Declarations, cwTenant)
	}
	ev := c.Events
	if ev == nil || ev.Examined != 4 || ev.SignedWithYourKey != 2 || ev.NotRecorded != 1 || ev.Authority["none"] != 1 {
		t.Fatalf("events = %+v", ev)
	}
	if len(ev.SignedByOther) != 1 || ev.SignedByOther[0].SignedBy != "bob.example" {
		t.Fatalf("signed by other = %+v", ev.SignedByOther)
	}
	if len(c.Findings) != 0 {
		t.Fatalf("findings = %+v, want none", c.Findings)
	}
	if !strings.Contains(c.Limit, "does not reduce it") || !strings.Contains(c.Limit, "cannot FORGE") {
		t.Fatalf("the limit is not stated: %q", c.Limit)
	}
}

func TestCustodyCheckFollowsTheForwardReference(t *testing.T) {
	w := newCustodyWorld(t)
	g, gURL := w.grant("2026-09-14T01:00:00Z", attestation.CustodyBasisUserSigned)
	wURL := w.withdraw(g, gURL, "2026-09-14T02:00:00Z")
	g.WithdrawnBy = wURL
	w.serve(gURL, g)

	c := CheckCustody(cwTenant, cwOperator, "", w.fetch)

	if len(c.Grants) != 1 || c.Grants[0].Withdrawal != WithdrawalWithdrawn || c.Grants[0].WithdrawnBy != wURL {
		t.Fatalf("grants = %+v, want the grant withdrawn by %s", c.Grants, wURL)
	}
	if c.Grants[0].Signature != "valid" {
		t.Fatalf("a withdrawn grant must still verify: %+v", c.Grants[0])
	}
}

// ⚠️ A pointer's ABSENCE proves nothing — whoever can write the file can strip
// it — so the issuer's own withdrawals are read too.
func TestCustodyCheckFindsAWithdrawalWhenThePointerIsStripped(t *testing.T) {
	w := newCustodyWorld(t)
	g, gURL := w.grant("2026-09-14T01:00:00Z", attestation.CustodyBasisUserSigned)
	w.withdraw(g, gURL, "2026-09-14T02:00:00Z")

	c := CheckCustody(cwTenant, cwOperator, "", w.fetch)
	if len(c.Grants) != 1 || c.Grants[0].Withdrawal != WithdrawalWithdrawn {
		t.Fatalf("grants = %+v, want withdrawn with no pointer", c.Grants)
	}
	// A withdrawn grant does not name an operator.
	if CheckCustody(cwTenant, "", "", w.fetch).Operator != "" {
		t.Fatal("a withdrawn grant was used to pick the operator")
	}
}

// ⭐ DISCOVERY, NOT PROOF: a pointer to something that does not verify is
// discarded, never read as a retraction.
func TestCustodyCheckDiscardsAPointerThatDoesNotVerify(t *testing.T) {
	w := newCustodyWorld(t)
	g, gURL := w.grant("2026-09-14T01:00:00Z", attestation.CustodyBasisUserSigned)

	// A "withdrawal" of her grant signed by someone else, pointed at from her file.
	forged := &attestation.Record{
		Issuer:    "https://bob.example",
		Predicate: attestation.PredicateWithdrawal,
		Subject:   attestation.Subject{Type: attestation.SubjectURI, ID: gURL, Version: g.Version},
		Asserted:  "2026-09-14T02:00:00Z",
	}
	forgedURL := w.publish(forged)
	g.WithdrawnBy = forgedURL
	w.serve(gURL, g)

	c := CheckCustody(cwTenant, cwOperator, "", w.fetch)
	if len(c.Grants) != 1 || c.Grants[0].Withdrawal != WithdrawalNotWithdrawn {
		t.Fatalf("grants = %+v, want not_withdrawn — the pointer leads to bob's signature", c.Grants)
	}
	if !strings.Contains(strings.Join(c.Notes, "\n"), "pointer was discarded") {
		t.Fatalf("notes = %v, want the discarded pointer said out loud", c.Notes)
	}
}

func TestCustodyCheckReportsATamperedDeclaration(t *testing.T) {
	w := newCustodyWorld(t)
	d, dURL := w.declaration(cwTenant, "2026-09-14T00:00:00Z")
	d.Payload[attestation.CustodyKeyAttribution] = attestation.CustodyAttributionCoSigned // after signing
	w.serve(dURL, d)

	c := CheckCustody(cwTenant, cwOperator, "", w.fetch)
	if len(c.Declarations) != 1 || c.Declarations[0].Signature != "invalid" {
		t.Fatalf("declarations = %+v, want invalid", c.Declarations)
	}
	if len(c.Findings) != 1 || c.Findings[0].Kind != FindingDeclarationSignatureInvalid {
		t.Fatalf("findings = %+v", c.Findings)
	}
}

// ⛔ Absence is never a finding: a self-hosted site looks exactly like this.
func TestCustodyCheckWithNothingDeclaredIsNotAFinding(t *testing.T) {
	w := newCustodyWorld(t)
	c := CheckCustody(cwTenant, "", "", w.fetch)
	if len(c.Findings) != 0 || c.Operator != "" || len(c.Grants) != 0 || len(c.Declarations) != 0 {
		t.Fatalf("check = %+v, want an empty, finding-free result", c)
	}
	if !strings.Contains(strings.Join(c.Notes, "\n"), "self-hosted site has nothing to declare") {
		t.Fatalf("notes = %v, want the absence explained", c.Notes)
	}

	// The same with a named operator that declares nothing about her.
	c = CheckCustody(cwTenant, cwOperator, "", w.fetch)
	if len(c.Findings) != 0 || !strings.Contains(strings.Join(c.Notes, "\n"), "not a finding") {
		t.Fatalf("findings %+v notes %v", c.Findings, c.Notes)
	}
}

// Could not look is not a finding either.
func TestCustodyCheckWhenTheDiscoveryServiceIsUnreachable(t *testing.T) {
	w := newCustodyWorld(t)
	c := CheckCustody(cwTenant, "", "https://down.example", w.fetch)
	if c.Events == nil || c.Events.Error == "" || len(c.Findings) != 0 {
		t.Fatalf("events %+v findings %+v", c.Events, c.Findings)
	}
}

// Close-out F16a (epic 46): an act a user agent signed with the tenant's key
// under a grant is NOT "signed by you". The marker the check reads is the one
// the discovery service copied from the signed act into the event payload
// (`agent`, `grant`, `grant_state` on pub.polis.comment.blessing.*).
func TestCustodyCheckSplitsAgentActsFromYours(t *testing.T) {
	w := newCustodyWorld(t)
	const grantURL = "https://" + cwTenant + "/content/pub.polis.core/attestation/g.json"
	marked := event(2, cwTenant, "self")
	marked["type"] = "pub.polis.comment.blessing.granted"
	marked["payload"] = map[string]interface{}{"agent": "rosie", "grant": grantURL, "grant_state": "active"}
	w.serveEvents(event(1, cwTenant, "self"), marked, event(3, "bob.example", "none"))

	ev := CheckCustody(cwTenant, "", cwDS, w.fetch).Events
	if ev == nil || ev.SignedWithYourKey != 2 || ev.SignedByYou != 1 || ev.SignedByAgent != 1 {
		t.Fatalf("events = %+v, want 2 with your key: 1 by you, 1 by an agent", ev)
	}
	if len(ev.AgentActs) != 1 {
		t.Fatalf("agent acts = %+v", ev.AgentActs)
	}
	a := ev.AgentActs[0]
	if a.ID != "2" || a.Agent != "rosie" || a.Grant != grantURL || a.GrantState != "active" || a.Type != "pub.polis.comment.blessing.granted" {
		t.Fatalf("agent act = %+v", a)
	}
	// A half marker is not a marker: both fields or neither (epic 46).
	half := event(4, cwTenant, "self")
	half["payload"] = map[string]interface{}{"agent": "rosie"}
	w.serveEvents(half)
	if ev := CheckCustody(cwTenant, "", cwDS, w.fetch).Events; ev.SignedByAgent != 0 || ev.SignedByYou != 1 {
		t.Fatalf("half marker counted as an agent act: %+v", ev)
	}
}
