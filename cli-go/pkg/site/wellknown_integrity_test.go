package site

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Writers that do not drop what they cannot read.
//
// The golden fixture carries EVERY member the struct declares, the three it
// deliberately does not (public_key_history, public_key_messages, witnesses),
// an invented future member, and unmodelled members nested inside avatar and a
// bundle entry. It is written in the canonical on-disk form (sorted members,
// two-space indent, trailing newline), so an untouched round-trip must
// reproduce it byte for byte.

const everyMemberFixture = "every_member_wellknown.json"

// unmodelledOnPurpose are the members D1 says must NOT be declared on the
// struct. The fixture must carry them, or the table below proves nothing.
var unmodelledOnPurpose = []string{"public_key_history", "public_key_messages", "witnesses"}

func seedEveryMemberSite(t *testing.T) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	golden := loadTestdata(t, everyMemberFixture)
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), golden, 0644); err != nil {
		t.Fatal(err)
	}
	return dir, golden
}

func topLevelMembers(t *testing.T, data []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("document does not parse: %v", err)
	}
	return m
}

// The fixture is only a guard if it keeps up with the struct: a field declared
// tomorrow and absent from the fixture would be a member no writer is tested on.
func TestTheGoldenFixtureCarriesEveryDeclaredAndEveryUnmodelledMember(t *testing.T) {
	members := topLevelMembers(t, loadTestdata(t, everyMemberFixture))
	exact, _ := declaredMembers(reflect.TypeOf(wellKnownFields{}))
	for name := range exact {
		if _, ok := members[name]; !ok {
			t.Errorf("the fixture lacks declared member %q — add it to testdata/%s", name, everyMemberFixture)
		}
	}
	for _, name := range unmodelledOnPurpose {
		if exact[name] {
			t.Errorf("%q is declared on WellKnown — D1 says the fix is Extra, not declaring today's keys", name)
		}
		if _, ok := members[name]; !ok {
			t.Errorf("the fixture lacks unmodelled member %q", name)
		}
	}
	undeclared := 0
	for name := range members {
		if !exact[name] {
			undeclared++
		}
	}
	if undeclared <= len(unmodelledOnPurpose) {
		t.Error("the fixture needs at least one member nobody has heard of yet, or the test only proves today's three")
	}
}

// ⭐ Q2 of the plan, as a test: an untouched Load → Save reproduces the bytes of
// a file already in the canonical form, so Judge's snapshot hash does not move.
func TestAnUntouchedRoundTripIsByteIdentical(t *testing.T) {
	dir, golden := seedEveryMemberSite(t)
	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveWellKnown(dir, wk); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if !bytes.Equal(after, golden) {
		t.Fatalf("an untouched struct round-trip changed the file:\n--- before\n%s\n--- after\n%s", golden, after)
	}

	// And the raw writer emits the same bytes, so switching between them never
	// re-hashes a tenant either.
	raw, err := LoadWellKnownRaw(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveWellKnownRaw(dir, raw); err != nil {
		t.Fatal(err)
	}
	afterRaw, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if !bytes.Equal(afterRaw, golden) {
		t.Fatalf("the raw writer and the struct writer disagree on bytes:\n%s", afterRaw)
	}
}

// ⛔ THE TABLE. Every Go writer of .well-known/polis in this package, run over
// the golden fixture. Each names the members it MEANS to change; every other
// member — including ones nobody has invented yet — must come out byte-identical.
func TestEveryWellKnownWriterPreservesEveryMember(t *testing.T) {
	newKey, err := func() (string, error) {
		_, pub, err := signing.GenerateKeypair()
		return strings.TrimSpace(string(pub)), err
	}()
	if err != nil {
		t.Fatal(err)
	}
	priv, _, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	type writer struct {
		changes []string
		write   func(dir string) error
	}
	writers := map[string]writer{
		"SaveWellKnown(struct round-trip)": {[]string{"author_name"}, func(d string) error {
			wk, err := LoadWellKnown(d)
			if err != nil {
				return err
			}
			wk.AuthorName = "Alice Q."
			return SaveWellKnown(d, wk)
		}},
		"SetAuthorName": {[]string{"author_name"}, func(d string) error {
			_, err := SetAuthorName(d, "Alice Q.")
			return err
		}},
		"SetAvatar": {[]string{"avatar"}, func(d string) error {
			_, err := SetAvatar(d, &AvatarConfig{BG: "#112233", FG: "#ffffff"})
			return err
		}},
		"SetLicensePointer": {[]string{"license"}, func(d string) error {
			return SetLicensePointer(d, "/content/pub.polis.core/license/v2.json")
		}},
		"SetActorRegistryPointer": {[]string{"actor_registry"}, func(d string) error {
			return SetActorRegistryPointer(d, "/content/pub.polis.core/actor/v2.json")
		}},
		"SetOperatorPointer":        {[]string{"operator"}, func(d string) error { return SetOperatorPointer(d, "ops.example.com") }},
		"SetAgentsPointer":          {[]string{"agents"}, func(d string) error { return SetAgentsPointer(d, "/attestations/v2.json") }},
		"StripBundleActiveFields":   {nil, func(d string) error { return StripBundleActiveFields(d) }},
		"MigratePrivateBundlesPath": {nil, func(d string) error { return MigratePrivateBundlesPath(d) }},
		"MigrateAuthorField":        {nil, func(d string) error { return MigrateAuthorField(d) }},
		"WriteGenesisKeyHistory": {nil, func(d string) error {
			_, err := WriteGenesisKeyHistory(d)
			return err
		}},
		"RecordKeyRotation": {[]string{"public_key", "public_key_history"}, func(d string) error {
			return RecordKeyRotation(d, newKey, "sig", "2026-09-16T12:00:00Z")
		}},
		"ProvisionAndPublishMessagesKey": {[]string{"public_key_messages"}, func(d string) error {
			return ProvisionAndPublishMessagesKey(d, priv)
		}},
		"MigrateActiveThemeToRegistry": {nil, func(d string) error {
			raw, err := LoadWellKnownRaw(d)
			if err != nil {
				return err
			}
			raw["active_theme"] = "vice"
			if err := SaveWellKnownRaw(d, raw); err != nil {
				return err
			}
			return MigrateActiveThemeToRegistry(d)
		}},
	}

	golden := topLevelMembers(t, loadTestdata(t, everyMemberFixture))
	names := make([]string, 0, len(writers))
	for name := range writers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w := writers[name]
		t.Run(name, func(t *testing.T) {
			dir, _ := seedEveryMemberSite(t)
			if err := w.write(dir); err != nil {
				t.Fatalf("writer failed: %v", err)
			}
			after, err := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
			if err != nil {
				t.Fatal(err)
			}
			got := topLevelMembers(t, after)
			intended := map[string]bool{}
			for _, c := range w.changes {
				intended[c] = true
			}
			for member, want := range golden {
				if intended[member] {
					continue
				}
				have, ok := got[member]
				if !ok {
					t.Errorf("%s dropped %q", name, member)
					continue
				}
				if !bytes.Equal(compactJSON(t, have), compactJSON(t, want)) {
					t.Errorf("%s altered %q:\n want %s\n  got %s", name, member, want, have)
				}
			}
			for _, c := range w.changes {
				if bytes.Equal(compactJSON(t, got[c]), compactJSON(t, golden[c])) {
					t.Errorf("%s was meant to change %q and did not — the case proves nothing", name, c)
				}
			}
		})
	}
}

func compactJSON(t *testing.T, v json.RawMessage) []byte {
	t.Helper()
	if v == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, v); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The rotation fixture: a site that HAS rotated. A Load → mutate → Save must
// leave the chain byte-identical — this is the member nobody could ever restore.
func TestARotatedChainSurvivesAStructRoundTripByteIdentically(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	fixture := loadTestdata(t, "bash_rotated_wellknown.json")
	if err := os.WriteFile(filepath.Join(dir, ".well-known", "polis"), fixture, 0644); err != nil {
		t.Fatal(err)
	}
	before := topLevelMembers(t, fixture)["public_key_history"]
	var chain KeyHistoryBlock
	if err := json.Unmarshal(before, &chain); err != nil || len(chain.History) == 0 {
		t.Fatalf("the fixture must carry a chain that records a rotation (err=%v)", err)
	}

	wk, err := LoadWellKnown(dir)
	if err != nil {
		t.Fatal(err)
	}
	wk.AuthorName = "renamed"
	wk.SiteTitle = "a new title"
	if err := SaveWellKnown(dir, wk); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis"))
	if !bytes.Equal(compactJSON(t, topLevelMembers(t, after)["public_key_history"]), compactJSON(t, before)) {
		t.Fatal("a struct round-trip changed a rotated site's key history")
	}
}

// Nested unmodelled members survive too, and a cleared omitempty field is not
// resurrected from Extra.
func TestExtraDoesNotResurrectAClearedDeclaredField(t *testing.T) {
	wk := &WellKnown{
		PublicKey: "k",
		License:   "",
		Extra:     map[string]json.RawMessage{"license": json.RawMessage(`"/stale"`), "future": json.RawMessage(`1`)},
	}
	out, err := json.Marshal(wk)
	if err != nil {
		t.Fatal(err)
	}
	m := topLevelMembers(t, out)
	if _, ok := m["license"]; ok {
		t.Fatalf("a cleared License came back from Extra: %s", out)
	}
	if string(m["future"]) != "1" {
		t.Fatalf("an unmodelled member was lost: %s", out)
	}
}

func TestACaseFoldedMemberIsNotWrittenTwice(t *testing.T) {
	var wk WellKnown
	if err := json.Unmarshal([]byte(`{"Author_Name":"A","public_key":"k"}`), &wk); err != nil {
		t.Fatal(err)
	}
	if wk.AuthorName != "A" || len(wk.Extra) != 0 {
		t.Fatalf("encoding/json reads Author_Name into AuthorName; it must not also be kept as extra: %+v", wk)
	}
}

// ---------- D5: validate before the write ----------

func TestTheWriterRefusesToDropOrAlterProtectedMembers(t *testing.T) {
	for _, member := range []string{"public_key", "public_key_history", "public_key_messages"} {
		t.Run("drop "+member, func(t *testing.T) {
			dir, golden := seedEveryMemberSite(t)
			raw, _ := LoadWellKnownRaw(dir)
			delete(raw, member)
			err := SaveWellKnownRaw(dir, raw)
			var refusal *IdentityChangeError
			if !errors.As(err, &refusal) || len(refusal.Dropped) != 1 || refusal.Dropped[0] != member {
				t.Fatalf("dropping %s: want a refusal naming it, got %v", member, err)
			}
			if after, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis")); !bytes.Equal(after, golden) {
				t.Fatal("a refused write still changed the file")
			}
			if err := SaveWellKnownRaw(dir, raw, IdentityChange(member)); err != nil {
				t.Fatalf("a write that names the change must proceed: %v", err)
			}
		})
		t.Run("alter "+member, func(t *testing.T) {
			dir, _ := seedEveryMemberSite(t)
			raw, _ := LoadWellKnownRaw(dir)
			raw[member] = "something else"
			var refusal *IdentityChangeError
			if err := SaveWellKnownRaw(dir, raw); !errors.As(err, &refusal) || len(refusal.Altered) != 1 {
				t.Fatalf("altering %s: want a refusal, got %v", member, err)
			}
		})
	}
}

// The case Extra cannot help: a caller builds a document from scratch over an
// existing site. The struct writer refuses it.
func TestAFreshStructOverAnExistingSiteIsRefused(t *testing.T) {
	dir, _ := seedEveryMemberSite(t)
	wk, _ := LoadWellKnown(dir)
	fresh := &WellKnown{Version: wk.Version, PublicKey: wk.PublicKey, AuthorName: "x", Created: wk.Created}
	var refusal *IdentityChangeError
	if err := SaveWellKnown(dir, fresh); !errors.As(err, &refusal) {
		t.Fatalf("a from-scratch document dropped the chain and the messages key and was written: %v", err)
	}
	if !strings.Contains(refusal.Error(), "public_key_history") || !strings.Contains(refusal.Error(), "public_key_messages") {
		t.Fatalf("the refusal must name what would be lost: %v", refusal)
	}
}

func TestAddingAProtectedMemberNeedsNoIntent(t *testing.T) {
	dir := t.TempDir()
	if err := SaveWellKnownRaw(dir, map[string]interface{}{"public_key": "k", "created": "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteGenesisKeyHistory(dir); err != nil {
		t.Fatalf("genesis is an addition and must not need intent: %v", err)
	}
}

func TestAnUnparseableExistingFileIsNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".well-known", "polis")
	_ = os.MkdirAll(filepath.Dir(path), 0755)
	_ = os.WriteFile(path, []byte(`{"public_key": "k",`), 0644)
	if err := SaveWellKnown(dir, &WellKnown{PublicKey: "k"}); err == nil {
		t.Fatal("a half-edited identity document was overwritten without anyone knowing what it held")
	}
}

// ---------- D4 ----------

func TestSetAuthorNameValidates(t *testing.T) {
	dir, golden := seedEveryMemberSite(t)
	if _, err := SetAuthorName(dir, strings.Repeat("x", MaxAuthorNameLen+1)); err == nil {
		t.Fatal("an over-long display name was accepted")
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".well-known", "polis")); !bytes.Equal(after, golden) {
		t.Fatal("a rejected name still wrote the file")
	}
	name, err := SetAuthorName(dir, "  Alice  ")
	if err != nil || name != "Alice" {
		t.Fatalf("SetAuthorName = %q, %v", name, err)
	}
}

func TestSetAvatarValidates(t *testing.T) {
	dir, _ := seedEveryMemberSite(t)
	if _, err := SetAvatar(dir, &AvatarConfig{BG: "red", FG: "#ffffff"}); err == nil {
		t.Fatal("a non-hex colour was accepted")
	}
	if _, err := SetAvatar(dir, &AvatarConfig{BG: "#000000", FG: "#ffffff", Pattern: "plaid"}); err == nil {
		t.Fatal("an unknown pattern was accepted")
	}
}

// The site-owned migration: registry adopted, legacy field stripped through the
// guarded writer, everything else kept.
func TestMigrateActiveThemeToRegistry(t *testing.T) {
	dir := t.TempDir()
	if err := SaveWellKnownRaw(dir, map[string]interface{}{
		"public_key": "ssh-ed25519 AAAA", "active_theme": "vice", "author_name": "alice",
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // idempotent
		if err := MigrateActiveThemeToRegistry(dir); err != nil {
			t.Fatalf("migrate #%d: %v", i+1, err)
		}
	}
	reg, err := bundle.LoadRegistry(dir)
	if err != nil || reg.ActiveTheme != "pub.polis.themes.vice" {
		t.Fatalf("registry = %+v, %v", reg, err)
	}
	raw, _ := LoadWellKnownRaw(dir)
	if _, ok := raw["active_theme"]; ok {
		t.Error("active_theme not stripped from well-known")
	}
	if raw["author_name"] != "alice" || raw["public_key"] != "ssh-ed25519 AAAA" {
		t.Errorf("other members not kept: %v", raw)
	}

	// Nothing to migrate, and no document at all, are both no-ops.
	empty := t.TempDir()
	if err := MigrateActiveThemeToRegistry(empty); err != nil {
		t.Fatalf("no well-known: %v", err)
	}
	if _, err := bundle.LoadRegistry(empty); err == nil {
		t.Error("a registry was created with nothing to migrate")
	}
}
