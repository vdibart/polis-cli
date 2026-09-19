package sitecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// SIGNET epic 47 — the tolerance rule where `polis validate` reports it.
//
// ⛔ THE FAILURE THIS PINS IS A REPORTING ONE, NOT A VERIFICATION ONE. The
// licence check returns a two-state `CheckStatus`, so `unknown` has to ride in
// on `OK: true` — and `OK: true` renders as a PASS unless the report knows
// better. "Could not check" must never look like "checked and fine", and the
// remote form already files it as not-applicable, so a pass here would also
// make local and remote disagree about one site.

// licenceSignedOverWiderBytes writes a licence whose signature covers a member
// this build does not declare — a newer AIPREF or RSL key, which is the
// expected case for this type rather than an exotic one.
func writeWideLicence(t *testing.T, b *siteBuilder) {
	t.Helper()
	b.write(filepath.Join(".well-known", "polis"), mustJSON(map[string]interface{}{
		"version": "2.0", "public_key": string(b.pub),
		"license": "/content/pub.polis.core/license/license.json",
	}))

	canonical := `{"type":"pub.polis.license","terms":{"v":"pub.polis.license.v1","profile":"pub.polis.license.reserved/1","train-ai":"n","search":"y","asserted":"2026-08-27T14:02:00Z","train-genai":"n"},"created":"2026-08-27T14:02:00Z","updated":"2026-08-27T14:02:00Z","generator":"polis-cli-go/newer"}`
	sig, err := signing.SignContent([]byte(canonical), b.priv)
	if err != nil {
		t.Fatalf("SignContent: %v", err)
	}
	doc := map[string]interface{}{
		"type": "pub.polis.license",
		"terms": map[string]interface{}{
			"v": "pub.polis.license.v1", "profile": "pub.polis.license.reserved/1",
			"train-ai": "n", "search": "y", "asserted": "2026-08-27T14:02:00Z",
			"train-genai": "n",
		},
		"created": "2026-08-27T14:02:00Z", "updated": "2026-08-27T14:02:00Z",
		"generator": "polis-cli-go/newer", "signature": sig,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	b.write(filepath.Join("content", "pub.polis.core", "license", "license.json"), raw)
}

func TestLicenceSignedOverAWiderFieldSetIsUncheckableNotAltered(t *testing.T) {
	b := newSite(t)
	writeWideLicence(t, b)

	got := LicenseIntegrity(b.dir, b.pub)
	if !got.OK {
		t.Fatalf("reported a failure: %q — a licence using vocabulary this build has not learned is not evidence the terms were altered", got.Message)
	}
	if !strings.HasPrefix(got.Message, LicenseUncheckable) {
		t.Fatalf("message = %q, want the %q sentinel so the report can file it as not-applicable", got.Message, LicenseUncheckable)
	}
	if !strings.Contains(got.Message, "terms.train-genai") {
		t.Errorf("message does not name the field: %q", got.Message)
	}
	// ⛔ And it must not read as the headline tampering case, which is what this
	// path said before the rule.
	if strings.Contains(got.Message, "may have been altered") {
		t.Errorf("an unrecognised field reported as possible tampering: %q", got.Message)
	}
}

func TestAnIntactLicenceWithAnUncoveredFieldSaysSo(t *testing.T) {
	// Row 2 of the table: the member sits OUTSIDE the signing base, so the
	// signature verifies — and the reader is owed the other half of the answer.
	b := newSite(t)
	b.write(filepath.Join(".well-known", "polis"), mustJSON(map[string]interface{}{
		"version": "2.0", "public_key": string(b.pub),
		"license": "/content/pub.polis.core/license/license.json",
	}))

	terms, err := license.ProfileTerms(license.ProfileReserved, "https://alice.example", "2026-08-27T14:02:00Z")
	if err != nil {
		t.Fatalf("ProfileTerms: %v", err)
	}
	f := &license.File{
		Type: license.TypeName, Terms: terms,
		Created: "2026-08-27T14:02:00Z", Updated: "2026-08-27T14:02:00Z",
		Generator: "polis-cli-go/test",
	}
	path := filepath.Join(b.dir, "content", "pub.polis.core", "license", "license.json")
	if err := license.SignAndWrite(f, path, b.priv); err != nil {
		t.Fatalf("SignAndWrite: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["jurisdiction"] = "US-NY"
	wide, _ := json.Marshal(doc)
	b.write(filepath.Join("content", "pub.polis.core", "license", "license.json"), wide)

	got := LicenseIntegrity(b.dir, b.pub)
	if !got.OK {
		t.Fatalf("reported a failure: %q — a member outside the signing base does not disturb the signature", got.Message)
	}
	if strings.HasPrefix(got.Message, LicenseUncheckable) {
		t.Fatalf("reported uncheckable: %q — the signature verified", got.Message)
	}
	if !strings.Contains(got.Message, "not covered by the signature") || !strings.Contains(got.Message, "jurisdiction") {
		t.Errorf("message = %q — a passing check still owes the reader the field the signature does NOT cover", got.Message)
	}
}
