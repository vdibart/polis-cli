package sitecheck

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// SIGNET epic 32 — the witness axis in `polis validate` and the verifier.
//
// ⛔ EVERY TEST HERE MAKES NO NETWORK CALL. The discovery service's key is
// pinned (StaticDSKeys), which is the real claim of "verifies offline": the
// record is self-contained GIVEN the DS's key. Fetching that key is a separate
// step, and its failure is tested to degrade to "unverifiable", never to red.

const (
	testDS      = "https://ds.polis.pub"
	testPostRel = "content/pub.polis.core/post/20260913/witnessed.md"
	testPostURL = "https://alice.polis.pub/" + testPostRel
)

// signWitness signs a witness exactly as the DS does: the DS key over Canonical.
func signWitness(t *testing.T, w discovery.Witness, ds kp) discovery.Witness {
	t.Helper()
	canonical, err := w.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := signing.SignContent(canonical, ds.priv)
	if err != nil {
		t.Fatal(err)
	}
	w.Signature = sig
	return w
}

// contentWitnessFor builds the witness a DS would issue for these exact bytes.
func contentWitnessFor(t *testing.T, content, url string, site kp, ds kp, keyID, at string) discovery.Witness {
	t.Helper()
	fm, _, err := ParseFrontmatter(content)
	if err != nil {
		t.Fatal(err)
	}
	return signWitness(t, discovery.Witness{
		Action:       discovery.WitnessActionContent,
		Type:         "pub.polis.post",
		URL:          url,
		Version:      fm.CurrentVersion,
		Author:       "alice.polis.pub",
		Actor:        "alice.polis.pub",
		PublicKey:    site.pub,
		ArtifactHash: discovery.ArtifactHash(signing.MarkdownSigningBase(content, signing.TypePost)),
		DS:           testDS,
		DSKeyID:      keyID,
		WitnessedAt:  at,
	}, ds)
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// witnessedSite is a site with one signed post, witnessed by `ds` under keyID.
func witnessedSite(t *testing.T, ds kp, keyID string) (dir string, k kp, post string) {
	t.Helper()
	k = genKey(t)
	dir = khSite(t, k, true)
	post = signedAt(t, k.priv, "Seen by a witness.\n", "2026-09-13T12:00:00Z", false)
	writeFile(t, dir, testPostRel, post)
	w := contentWitnessFor(t, post, testPostURL, k, ds, keyID, "2026-09-13T12:00:01.500Z")
	if _, err := site.RecordWitness(dir, testPostURL, w); err != nil {
		t.Fatal(err)
	}
	return dir, k, post
}

func pinned(ds kp, keyID string) DSKeyLookup {
	return StaticDSKeys(map[string]string{testDS + "|" + keyID: ds.pub})
}

// ⭐ DONE-WHEN 1: a newly registered artifact carries a DS countersignature that
// verifies OFFLINE against the DS's published key.
func TestAWitnessedPostVerifiesOfflineAgainstThePinnedDSKey(t *testing.T) {
	ds := genKey(t)
	dir, _, _ := witnessedSite(t, ds, "ds-2026")

	r := RunLocalWith(dir, pinned(ds, "ds-2026"))
	if !r.OK() {
		t.Fatalf("report must be clean: %+v", r.Checks)
	}
	c := findCheck(t, r, "content.witnesses")
	if c.Outcome != OutcomePassed || !strings.Contains(c.Detail, "1 witnessed, 0 unwitnessed, 0 with a witness that could not be checked") {
		t.Fatalf("the post must be reported witnessed: %+v", c)
	}
}

// ⛔ DONE-WHEN 2 — D1, THE EPIC: an artifact with NO countersignature still
// verifies, and nothing about the witness axis fails.
func TestAnUncountersignedArtifactStillVerifies(t *testing.T) {
	k := genKey(t)
	dir := khSite(t, k, true)
	writeFile(t, dir, testPostRel, signedAt(t, k.priv, "Nobody saw this.\n", "2026-09-13T12:00:00Z", false))

	r := RunLocalWith(dir, nil)
	if !r.OK() {
		t.Fatalf("an uncountersigned site must validate clean: %+v", r.Checks)
	}
	if c := findCheck(t, r, "content.posts"); c.Outcome != OutcomePassed {
		t.Fatalf("the post must verify on its own signature: %+v", c)
	}
	w := findCheck(t, r, "content.witnesses")
	if w.Outcome != OutcomeNotApplicable || !strings.Contains(w.Reason, "weaker claim, not a defect") {
		t.Fatalf("a site with no witnesses is not-applicable and says the claim is weaker, not broken: %+v", w)
	}

	// A site that DOES publish witnesses, but not for this post: unwitnessed,
	// counted, still clean.
	other := contentWitnessFor(t, signedAt(t, k.priv, "Another post.\n", "2026-09-13T12:00:00Z", false),
		"https://alice.polis.pub/content/pub.polis.core/post/20260913/other.md", k, genKey(t), "ds-x", "2026-09-13T12:00:02.000Z")
	if _, err := site.RecordWitness(dir, other.URL, other); err != nil {
		t.Fatal(err)
	}
	r = RunLocalWith(dir, nil)
	if !r.OK() {
		t.Fatalf("still clean: %+v", r.Checks)
	}
	if c := findCheck(t, r, "content.witnesses"); c.Outcome != OutcomePassed || !strings.Contains(c.Detail, "0 witnessed, 1 unwitnessed") {
		t.Fatalf("an unwitnessed post is counted as unwitnessed, never failed: %+v", c)
	}
}

// DONE-WHEN 3: the result distinguishes witnessed from unwitnessed, in the API
// and in what a human sees.
func TestTheResultDistinguishesWitnessedFromUnwitnessed(t *testing.T) {
	ds := genKey(t)
	_, k, post := witnessedSite(t, ds, "ds-2026")
	w := contentWitnessFor(t, post, testPostURL, k, ds, "ds-2026", "2026-09-13T12:00:01.500Z")
	res := VerifyContent(post, []byte(k.pub), signing.TypePost)

	got := CheckContentWitnesses([]discovery.Witness{w}, res.ArtifactHash, res.CurrentVersion, pinned(ds, "ds-2026"))
	if got.State != WitnessWitnessed || got.EarliestWitnessedAt != "2026-09-13T12:00:01.500Z" || got.EarliestBinds != "artifact" {
		t.Fatalf("API: %+v", got)
	}
	if !strings.HasPrefix(got.Describe(), "witnessed:") || !strings.Contains(got.Describe(), "the whole signed artifact") {
		t.Errorf("human: %q", got.Describe())
	}

	none := CheckContentWitnesses(nil, res.ArtifactHash, res.CurrentVersion, pinned(ds, "ds-2026"))
	if none.State != WitnessUnwitnessed || !strings.HasPrefix(none.Describe(), "unwitnessed:") || !strings.Contains(none.Describe(), "still verifies") {
		t.Fatalf("unwitnessed must be a distinct state and say the artifact still verifies: %+v %q", none, none.Describe())
	}
}

// ⭐ DONE-WHEN 4 — D4: a countersignature still verifies after the DS rotates
// its key, because the record names the key it was made with.
func TestAWitnessStillVerifiesAfterTheDSRotatesItsKey(t *testing.T) {
	oldDS, newDS := genKey(t), genKey(t)
	k := genKey(t)
	post := signedAt(t, k.priv, "Witnessed before the DS rotated.\n", "2026-09-13T12:00:00Z", false)
	res := VerifyContent(post, []byte(k.pub), signing.TypePost)
	w := contentWitnessFor(t, post, testPostURL, k, oldDS, "ds-2025", "2026-01-01T00:00:00.000Z")

	both := StaticDSKeys(map[string]string{testDS + "|ds-2025": oldDS.pub, testDS + "|ds-2026": newDS.pub})
	if got := CheckContentWitnesses([]discovery.Witness{w}, res.ArtifactHash, res.CurrentVersion, both); got.State != WitnessWitnessed {
		t.Fatalf("a witness made under a retired DS key must verify against THAT key: %+v", got)
	}

	// Checked against the DS's CURRENT key instead, it does not — which is why
	// the record names its key and the verifier asks for that one.
	currentOnly := func(ds, keyID string) (string, error) { return newDS.pub, nil }
	if got := CheckContentWitnesses([]discovery.Witness{w}, res.ArtifactHash, res.CurrentVersion, currentOnly); got.State == WitnessWitnessed {
		t.Fatal("resolving by the current DS key would accept anything signed by it; the named key must be used")
	}
}

// ⛔ DONE-WHEN 6 — D6/E2: a tampered artifact whose countersignature is intact
// is not witnessed. Covers the frontmatter, not just the body: the case the
// body-only `version` would have missed.
func TestATamperedArtifactWhoseWitnessIsIntactIsNotWitnessed(t *testing.T) {
	ds := genKey(t)
	dir, k, post := witnessedSite(t, ds, "ds-2026")

	// The author (or a thief holding their key) rewrites `published:` over the
	// SAME body and re-signs. The signature is valid and `version` is unchanged.
	backdated := signedAt(t, k.priv, "Seen by a witness.\n", "2026-01-01T00:00:00Z", false)
	resOrig := VerifyContent(post, []byte(k.pub), signing.TypePost)
	resBack := VerifyContent(backdated, []byte(k.pub), signing.TypePost)
	if resBack.Signature != SigValid || resBack.CurrentVersion != resOrig.CurrentVersion {
		t.Fatalf("setup: the rewrite must verify and keep the body version: %+v", resBack)
	}
	writeFile(t, dir, testPostRel, backdated)

	r := RunLocalWith(dir, pinned(ds, "ds-2026"))
	if c := findCheck(t, r, "content.posts"); c.Outcome != OutcomePassed {
		t.Fatalf("setup: the re-signed post verifies on its signature: %+v", c)
	}
	if c := findCheck(t, r, "content.witnesses"); !strings.Contains(c.Detail, "0 witnessed, 1 unwitnessed") {
		t.Fatalf("the intact witness binds the ORIGINAL signed artifact and must not apply to the rewrite: %+v", c)
	}

	f, _ := site.LoadWitnesses(dir)
	got := CheckContentWitnesses(f.For(testPostURL), resBack.ArtifactHash, resBack.CurrentVersion, pinned(ds, "ds-2026"))
	if got.State != WitnessUnwitnessed || len(got.Checks) != 1 || got.Checks[0].Status != WitnessOtherBytes {
		t.Fatalf("the witness must be reported as covering other bytes: %+v", got)
	}

	// Body tampered, not re-signed: the signature fails (as before) and the
	// witness still does not apply.
	writeFile(t, dir, testPostRel, strings.Replace(post, "Seen by a witness.", "Seen by nobody.", 1))
	r = RunLocalWith(dir, pinned(ds, "ds-2026"))
	if c := findCheck(t, r, "content.posts"); c.Outcome != OutcomeFailed {
		t.Fatalf("a tampered body fails its own signature: %+v", c)
	}
	if c := findCheck(t, r, "content.witnesses"); c.Outcome == OutcomeFailed || !strings.Contains(c.Detail, "0 witnessed") {
		t.Fatalf("and the witness axis neither applies nor fails: %+v", c)
	}
}

// ⛔ D1/D8: an unreachable DS turns a witness "unverifiable" — never red.
func TestAnUnreachableDSNeverFailsTheWitnessAxis(t *testing.T) {
	ds := genKey(t)
	dir, _, _ := witnessedSite(t, ds, "ds-2026")
	down := func(string, string) (string, error) { return "", errors.New("connection refused") }

	r := RunLocalWith(dir, down)
	if !r.OK() {
		t.Fatalf("an unreachable DS must not fail the run: %+v", r.Checks)
	}
	c := findCheck(t, r, "content.witnesses")
	if c.Outcome != OutcomePassed || !strings.Contains(c.Detail, "1 with a witness that could not be checked") {
		t.Fatalf("expected the witness counted as unverifiable: %+v", c)
	}
	if len(c.Findings) == 0 || !strings.Contains(c.Findings[0], "connection refused") {
		t.Errorf("the reason must be carried: %+v", c.Findings)
	}

	if _, err := NewDSKeyLookup(nil, "")("http://ds.polis.pub", "ds-2026"); err == nil {
		t.Error("a plain-http DS key has no assurance and must not be fetched")
	}
}

// ⛔ E9 (ruled by oversight): a published witness that does not verify against
// its discovery service's key FAILS. D1 forbids REQUIRING a witness; it does not
// make a fraudulent one harmless — the site is asserting third-party testimony
// it does not have.
func TestAForgedWitnessFails(t *testing.T) {
	ds, forger := genKey(t), genKey(t)
	k := genKey(t)
	dir := khSite(t, k, true)
	post := signedAt(t, k.priv, "Forged testimony.\n", "2026-09-13T12:00:00Z", false)
	writeFile(t, dir, testPostRel, post)
	w := contentWitnessFor(t, post, testPostURL, k, forger, "ds-2026", "2020-01-01T00:00:00.000Z")
	if _, err := site.RecordWitness(dir, testPostURL, w); err != nil {
		t.Fatal(err)
	}
	r := RunLocalWith(dir, pinned(ds, "ds-2026"))
	if r.OK() {
		t.Fatalf("a forged witness must fail the run: %+v", r.Checks)
	}
	if c := findCheck(t, r, "content.posts"); c.Outcome != OutcomePassed {
		t.Fatalf("the post's own signature is judged separately and still passes: %+v", c)
	}
	c := findCheck(t, r, "content.witnesses")
	if c.Outcome != OutcomeFailed || !strings.Contains(c.Detail, "1 with a witness that does not verify") ||
		len(c.Findings) == 0 || !strings.Contains(c.Findings[0], "does NOT verify") {
		t.Fatalf("a forged witness must fail content.witnesses and name the record: %+v", c)
	}

	// The API must not call it "unwitnessed" — there IS a countersignature.
	res := VerifyContent(post, []byte(k.pub), signing.TypePost)
	got := CheckContentWitnesses([]discovery.Witness{w}, res.ArtifactHash, res.CurrentVersion, pinned(ds, "ds-2026"))
	if got.State != WitnessStateInvalid || strings.Contains(got.Describe(), "no discovery service countersignature") {
		t.Fatalf("an invalid witness is its own state and must never read as absent: %+v %q", got, got.Describe())
	}
}

// A genuine witness does not launder a forged one published beside it.
func TestAForgedWitnessBesideAValidOneStillFails(t *testing.T) {
	ds, forger := genKey(t), genKey(t)
	k := genKey(t)
	post := signedAt(t, k.priv, "One true, one false.\n", "2026-09-13T12:00:00Z", false)
	res := VerifyContent(post, []byte(k.pub), signing.TypePost)
	good := contentWitnessFor(t, post, testPostURL, k, ds, "ds-2026", "2026-09-13T12:00:01.000Z")
	bad := contentWitnessFor(t, post, testPostURL, k, forger, "ds-2026", "2020-01-01T00:00:00.000Z")
	got := CheckContentWitnesses([]discovery.Witness{good, bad}, res.ArtifactHash, res.CurrentVersion, pinned(ds, "ds-2026"))
	if got.State != WitnessStateInvalid || got.EarliestWitnessedAt != good.WitnessedAt {
		t.Fatalf("invalid takes precedence, and the genuine date is still carried: %+v", got)
	}
}

// ⚠️ The case the E9 fix must NOT catch: a witness of a superseded version is
// normal in a witness set and never fails.
func TestAWitnessOfOtherBytesNeverFails(t *testing.T) {
	ds, forger := genKey(t), genKey(t)
	dir, k, _ := witnessedSite(t, ds, "ds-2026")
	// Even a witness that would NOT verify is never checked when it covers other
	// bytes — it does not apply to this artifact at all.
	stale := signedAt(t, k.priv, "An earlier version.\n", "2026-09-12T12:00:00Z", false)
	old := contentWitnessFor(t, stale, testPostURL, k, forger, "ds-2026", "2026-09-12T12:00:01.000Z")
	if _, err := site.RecordWitness(dir, testPostURL, old); err != nil {
		t.Fatal(err)
	}
	r := RunLocalWith(dir, pinned(ds, "ds-2026"))
	if !r.OK() {
		t.Fatalf("a superseded version's witness must never fail the run: %+v", r.Checks)
	}
	if c := findCheck(t, r, "content.witnesses"); c.Outcome != OutcomePassed || !strings.Contains(c.Detail, "1 witnessed") {
		t.Fatalf("the current version is still witnessed: %+v", c)
	}
}

func TestAForgedRotationWitnessFailsTheKeyHistoryWitnessCheck(t *testing.T) {
	ds, forger := genKey(t), genKey(t)
	k0, k1 := genKey(t), genKey(t)
	dir := khSite(t, k0, false)
	ts := "2026-09-15T10:22:03Z"
	canonical, _ := discovery.MakeKeyRotationCanonicalJSON("alice.polis.pub", k0.pub, k1.pub, ts)
	sig, err := signing.SignContent(canonical, k0.priv)
	if err != nil {
		t.Fatal(err)
	}
	w := signWitness(t, discovery.Witness{
		Action: discovery.WitnessActionKeyRotation, Domain: "alice.polis.pub",
		OldKey: k0.pub, NewKey: k1.pub, Timestamp: ts, TransitionSig: sig,
		DS: testDS, DSKeyID: "ds-2026", WitnessedAt: "2026-09-15T10:22:03.417Z",
	}, forger)
	if err := site.RecordKeyRotation(dir, k1.pub, sig, ts, w); err != nil {
		t.Fatal(err)
	}
	if err := site.PublishDIDDocument(dir, "alice.polis.pub"); err != nil {
		t.Fatal(err)
	}
	r := RunLocalWith(dir, pinned(ds, "ds-2026"))
	c := findCheck(t, r, "identity.key_history_witness")
	if c.Outcome != OutcomeFailed || len(c.Findings) == 0 || !strings.Contains(c.Findings[0], "epoch 1") || !strings.Contains(c.Findings[0], "does NOT verify") {
		t.Fatalf("a forged rotation witness must fail and name the epoch: %+v", c)
	}
	if kh := findCheck(t, r, "identity.key_history"); kh.Outcome != OutcomePassed {
		t.Fatalf("the chain itself still verifies — the witness is judged separately: %+v", kh)
	}
}

// D2: a set, and a second DS's testimony is simply more of it.
func TestASecondDSIsMoreTestimonyNotAConflict(t *testing.T) {
	dsA, dsB := genKey(t), genKey(t)
	k := genKey(t)
	post := signedAt(t, k.priv, "Seen twice.\n", "2026-09-13T12:00:00Z", false)
	res := VerifyContent(post, []byte(k.pub), signing.TypePost)
	a := contentWitnessFor(t, post, testPostURL, k, dsA, "a-1", "2026-09-13T12:00:05.000Z")
	b := contentWitnessFor(t, post, testPostURL, k, dsB, "b-1", "2026-09-13T12:00:03.000Z")
	b = func() discovery.Witness { b.DS = "https://ds.example.org"; return signWitness(t, b, dsB) }()

	keys := StaticDSKeys(map[string]string{testDS + "|a-1": dsA.pub, "https://ds.example.org|b-1": dsB.pub})
	got := CheckContentWitnesses([]discovery.Witness{a, b}, res.ArtifactHash, res.CurrentVersion, keys)
	if got.State != WitnessWitnessed || got.EarliestWitnessedAt != "2026-09-13T12:00:03.000Z" || len(got.Checks) != 2 {
		t.Fatalf("two witnesses, earliest date wins: %+v", got)
	}
}

// ⭐ The backdating D3 could not bound: a retired key's signature, claiming a
// time inside its window, first witnessed after that window closed.
func TestAWitnessAfterTheRetiredKeysWindowIsReportedAgainstTheClaim(t *testing.T) {
	epoch := 0
	key := &KeyUsed{Source: KeyRetired, Epoch: &epoch, ClaimedSigningTime: beforeRot, ValidFrom: genesisAt, ValidUntil: rotatedAt}

	late := &WitnessResult{State: WitnessWitnessed, EarliestWitnessedAt: "2026-10-01T00:00:00.000Z"}
	if msg := late.ContradictsClaimedTime(key); !strings.Contains(msg, "AFTER the retired key") {
		t.Fatalf("a witness after the window must be reported: %q", msg)
	}
	early := &WitnessResult{State: WitnessWitnessed, EarliestWitnessedAt: "2026-05-01T09:00:01.000Z"}
	if msg := early.ContradictsClaimedTime(key); msg != "" {
		t.Fatalf("a witness inside the window corroborates and says nothing: %q", msg)
	}
	if msg := late.ContradictsClaimedTime(&KeyUsed{Source: KeyCurrent}); msg != "" {
		t.Fatalf("a current-key pass has no retired window to contradict: %q", msg)
	}
}

// ⭐ DONE-WHEN 5 + D8's free win: a rotation carries the DS's countersignature,
// and identity.key_history_witness is real for the first time.
func TestAWitnessedRotationFillsTheKeyHistoryWitnessCheck(t *testing.T) {
	ds := genKey(t)
	k0, k1 := genKey(t), genKey(t)
	dir := khSite(t, k0, false)
	ts := "2026-09-15T10:22:03Z"
	canonical, _ := discovery.MakeKeyRotationCanonicalJSON("alice.polis.pub", k0.pub, k1.pub, ts)
	sig, err := signing.SignContent(canonical, k0.priv)
	if err != nil {
		t.Fatal(err)
	}
	w := signWitness(t, discovery.Witness{
		Action: discovery.WitnessActionKeyRotation, Domain: "alice.polis.pub",
		OldKey: k0.pub, NewKey: k1.pub, Timestamp: ts, TransitionSig: sig,
		DS: testDS, DSKeyID: "ds-2026", WitnessedAt: "2026-09-15T10:22:03.417Z",
	}, ds)
	if err := site.RecordKeyRotation(dir, k1.pub, sig, ts, w); err != nil {
		t.Fatal(err)
	}
	if err := site.PublishDIDDocument(dir, "alice.polis.pub"); err != nil {
		t.Fatal(err)
	}

	c := findCheck(t, RunLocalWith(dir, pinned(ds, "ds-2026")), "identity.key_history_witness")
	if c.Outcome != OutcomePassed || !strings.Contains(c.Detail, "1 of 1 rotation(s)") {
		t.Fatalf("a witnessed rotation must pass the witness check: %+v", c)
	}
	if !strings.Contains(c.Detail, "cannot reveal one the chain omits") {
		t.Errorf("the pass must still say what a witness cannot show: %q", c.Detail)
	}
	if len(c.Findings) != 1 || !strings.Contains(c.Findings[0], "epoch 1: witnessed by "+testDS+" at 2026-09-15T10:22:03.417Z") {
		t.Errorf("the note must name the epoch, the witness and its date: %+v", c.Findings)
	}

	// Unreachable DS: not-applicable, never failed, and still names the gap.
	down := RunLocalWith(dir, func(string, string) (string, error) { return "", errors.New("timeout") })
	if c := findCheck(t, down, "identity.key_history_witness"); c.Outcome != OutcomeNotApplicable || !strings.Contains(c.Reason, "omitted") {
		t.Fatalf("an unverifiable rotation witness is not-applicable: %+v", c)
	}
}

func TestAnUnwitnessedRotationLeavesTheGapNamed(t *testing.T) {
	f := rotatedSite(t, "alice.polis.pub", true)
	c := findCheck(t, RunLocalWith(f.dir, nil), "identity.key_history_witness")
	if c.Outcome != OutcomeNotApplicable || !strings.Contains(c.Reason, "no rotation in the chain carries a witness") ||
		!strings.Contains(c.Reason, "omitted") || !strings.Contains(c.Reason, "backdated") {
		t.Fatalf("an unwitnessed rotation is not-applicable and still names what goes unchecked: %+v", c)
	}
}

func TestARotationWitnessFarFromValidFromIsReported(t *testing.T) {
	ds := genKey(t)
	k0, k1 := genKey(t), genKey(t)
	dir := khSite(t, k0, true)
	ts := "2026-09-15T10:22:03Z"
	canonical, _ := discovery.MakeKeyRotationCanonicalJSON("alice.polis.pub", k0.pub, k1.pub, ts)
	sig, _ := signing.SignContent(canonical, k0.priv)
	w := signWitness(t, discovery.Witness{
		Action: discovery.WitnessActionKeyRotation, Domain: "alice.polis.pub",
		OldKey: k0.pub, NewKey: k1.pub, Timestamp: ts, TransitionSig: sig,
		DS: testDS, DSKeyID: "ds-2026", WitnessedAt: "2026-09-16T10:22:03.000Z",
	}, ds)
	if err := site.RecordKeyRotation(dir, k1.pub, sig, ts, w); err != nil {
		t.Fatal(err)
	}
	site.PublishDIDDocument(dir, "alice.polis.pub")
	c := findCheck(t, RunLocalWith(dir, pinned(ds, "ds-2026")), "identity.key_history_witness")
	if c.Outcome != OutcomePassed || len(c.Findings) != 1 || !strings.Contains(c.Findings[0], "24h0m0s from the chain's valid_from") {
		t.Fatalf("a day between the witness and valid_from must be reported, not judged: %+v", c)
	}
}

// D8 over HTTP: the stranger's tool reads the published witness set.
func TestValidateRemoteReadsThePublishedWitnessSet(t *testing.T) {
	ds := genKey(t)
	dir, _, post := witnessedSite(t, ds, "ds-2026")
	wk, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wk), `"witnesses"`) {
		t.Fatalf("setup: the site must publish a witnesses pointer: %s", wk)
	}
	witnesses, err := os.ReadFile(filepath.Join(dir, strings.TrimPrefix(site.DefaultWitnessesPointer, "/")))
	if err != nil {
		t.Fatal(err)
	}
	srv := &remoteSite{files: map[string]string{
		"/.well-known/polis":                  string(wk),
		"/" + testPostRel:                     post,
		site.DefaultWitnessesPointer:          string(witnesses),
		"/content/pub.polis.core/index.jsonl": string(mustJSONLine(map[string]string{"type": "post", "path": testPostRel, "published": "2026-09-13T12:00:00Z"})) + "\n",
	}}
	ts := srv.serve(t)

	rr := NewRemote()
	rr.DSKeys = pinned(ds, "ds-2026")
	if c := checkByID(t, rr.RunSite(ts.URL), "content.witnesses"); c.Outcome != OutcomePassed || !strings.Contains(c.Detail, "1 witnessed") {
		t.Fatalf("remote site: %+v", c)
	}
	a := checkByID(t, rr.RunArtifact(ts.URL+"/"+testPostRel), "content.witness")
	if a.Outcome != OutcomePassed || !strings.HasPrefix(a.Detail, "witnessed:") {
		t.Fatalf("remote artifact: %+v", a)
	}

	// The same site with its witness file gone: not-applicable, never failed.
	delete(srv.files, site.DefaultWitnessesPointer)
	rr2 := NewRemote()
	rr2.DSKeys = pinned(ds, "ds-2026")
	rep := rr2.RunSite(ts.URL)
	if c := checkByID(t, rep, "content.witnesses"); c.Outcome != OutcomeNotApplicable || !strings.Contains(c.Reason, "does not serve") {
		t.Fatalf("a pointer that leads nowhere is not-applicable: %+v", c)
	}
	for _, c := range rep.Checks {
		if strings.Contains(c.ID, "witness") && c.Outcome == OutcomeFailed {
			t.Fatalf("no witness check may ever fail (D1): %+v", c)
		}
	}
}

// The witness file itself round-trips through JSON as the verifier reads it.
func TestTheWitnessFileIsTheWireShape(t *testing.T) {
	ds := genKey(t)
	dir, _, _ := witnessedSite(t, ds, "ds-2026")
	data, err := os.ReadFile(filepath.Join(dir, strings.TrimPrefix(site.DefaultWitnessesPointer, "/")))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]map[string][]map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("witness file must be {\"witnesses\": {url: [record]}}: %v\n%s", err, data)
	}
	rec := raw["witnesses"][testPostURL][0]
	for _, field := range []string{"action", "type", "url", "version", "artifact_hash", "public_key", "ds", "ds_key_id", "witnessed_at", "signature"} {
		if _, ok := rec[field]; !ok {
			t.Errorf("published record is missing %q: %v", field, rec)
		}
	}
}
