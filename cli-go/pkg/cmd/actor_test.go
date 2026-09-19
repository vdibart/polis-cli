package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/actor"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func TestParseActorRegisterFlags(t *testing.T) {
	f, err := parseActorRegisterFlags([]string{
		"judge.polis.pub", "--authority", "operator",
		"--expects", "pub.polis.attestation.integrity",
		"--expects", "pub.polis.attestation.withdrawal",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if f.domain != "judge.polis.pub" || f.authority != actor.AuthorityOperator {
		t.Errorf("got %+v", f)
	}
	if len(f.expects) != 2 {
		t.Errorf("expects = %v", f.expects)
	}
}

// TestParseActorRegisterFlags_AuthorityIsTheTwoPublishedWords — the values are
// the already-published authority rule's vocabulary. ⛔ "handle" was the
// intuitive third word and it is exactly the drift to refuse: a third name for
// a concept that already has two published ones.
func TestParseActorRegisterFlags_AuthorityIsTheTwoPublishedWords(t *testing.T) {
	for _, bad := range []string{"handle", "tenant", "site", "admin"} {
		if _, err := parseActorRegisterFlags([]string{"x.example", "--authority", bad}); err == nil {
			t.Errorf("--authority %q was accepted", bad)
		}
	}
	for _, good := range []string{actor.AuthorityOperator, actor.AuthorityUser} {
		if _, err := parseActorRegisterFlags([]string{"x.example", "--authority", good}); err != nil {
			t.Errorf("--authority %q was rejected: %v", good, err)
		}
	}
}

// TestParseActorRegisterFlags_RequiresAuthority — there is no sensible default.
// Guessing `operator` for an actor that exercises a tenant's authority would
// publish a false claim about the most important field in the entry.
func TestParseActorRegisterFlags_RequiresAuthority(t *testing.T) {
	if _, err := parseActorRegisterFlags([]string{"x.example"}); err == nil {
		t.Fatal("--authority was not required")
	}
}

// TestParseActorRegisterFlags_RejectsAURL — an entry names a DOMAIN so that it
// survives a key rotation; a URL here would usually still "work" and then
// silently fail to match anything the guard indexes.
func TestParseActorRegisterFlags_RejectsAURL(t *testing.T) {
	for _, bad := range []string{"https://judge.polis.pub", "judge.polis.pub/actors"} {
		if _, err := parseActorRegisterFlags([]string{bad, "--authority", "operator"}); err == nil {
			t.Errorf("%q was accepted as a domain", bad)
		}
	}
}

// TestParseActorRegisterFlags_ExpectedActionsMustBeQualified — an unqualified
// action type is unmatchable against anything on the wire, so a registry
// carrying one would look like a statement and check nothing.
func TestParseActorRegisterFlags_ExpectedActionsMustBeQualified(t *testing.T) {
	if _, err := parseActorRegisterFlags([]string{
		"x.example", "--authority", "operator", "--expects", "integrity",
	}); err == nil {
		t.Fatal("an unqualified expected action was accepted")
	}
}

func TestUpsertReplacesAndPreservesOrder(t *testing.T) {
	entries := []actor.Entry{
		{Domain: "a.example", Authority: actor.AuthorityOperator},
		{Domain: "b.example", Authority: actor.AuthorityOperator},
	}
	entries = upsert(entries, actor.Entry{Domain: "a.example", Authority: actor.AuthorityUser})
	if len(entries) != 2 {
		t.Fatalf("upsert appended instead of replacing: %+v", entries)
	}
	if entries[0].Domain != "a.example" || entries[0].Authority != actor.AuthorityUser {
		t.Errorf("entry not replaced in place: %+v", entries)
	}
	entries = upsert(entries, actor.Entry{Domain: "c.example"})
	if len(entries) != 3 || entries[2].Domain != "c.example" {
		t.Errorf("append did not go to the end: %+v", entries)
	}
}

func announceFixture(t *testing.T) (dir string, priv []byte) {
	t.Helper()
	dir = t.TempDir()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, ".well-known"), 0755)
	wk, _ := json.Marshal(map[string]string{
		"version": "2.0", "public_key": strings.TrimSpace(string(pub)),
		"author_name": "op", "created": "2026-09-12T00:00:00Z",
	})
	os.WriteFile(filepath.Join(dir, ".well-known", "polis"), wk, 0644)
	return dir, priv
}

func TestCheckAnnounceable(t *testing.T) {
	dir, priv := announceFixture(t)

	if err := checkAnnounceable(dir, "judge.polis.pub"); err == nil {
		t.Fatal("announcing with no registry was allowed")
	}

	r := &actor.Registry{
		V: actor.SchemaVersion, Operator: "polis.polis.pub", Asserted: "2026-09-12T00:00:00Z",
		Actors: []actor.Entry{{Domain: "judge.polis.pub", Authority: actor.AuthorityOperator}},
	}
	if err := actor.StateRegistry(dir, r, priv); err != nil {
		t.Fatal(err)
	}

	if err := checkAnnounceable(dir, "judge.polis.pub"); err != nil {
		t.Errorf("a listed actor in a valid registry was refused: %v", err)
	}
	if err := checkAnnounceable(dir, "lookalike.example"); err == nil {
		t.Error("an actor the registry does not list was announceable")
	}

	// Tamper after signing: the file no longer backs what would be announced.
	loaded, _ := actor.Load(actor.RegistryPath(dir))
	loaded.Actors[0].ExpectedActions = []string{"pub.polis.anything"}
	data, _ := json.MarshalIndent(loaded, "", "  ")
	os.WriteFile(actor.RegistryPath(dir), data, 0644)
	if err := checkAnnounceable(dir, "judge.polis.pub"); err == nil {
		t.Error("an invalid registry was announceable — the event cannot be withdrawn")
	}
}
