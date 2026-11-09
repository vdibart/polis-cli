package secret

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// A projection whose dependencies all succeed, for tests to mutate.
func okProjection(t *testing.T) (*Projection, *[]byte) {
	t.Helper()
	var written []byte
	p := &Projection{
		Value:        New("-----BEGIN PRIVATE KEY-----body\n"),
		KeyPath:      "/site/.polis/keys/id_ed25519",
		Absent:       func(string) (bool, error) { return true, nil },
		ValidateKey:  func([]byte) error { return nil },
		PublicKeyFor: func([]byte) ([]byte, error) { return []byte("ssh-ed25519 AAAA host"), nil },
		PublishedKey: func() (string, error) { return "ssh-ed25519 AAAA other-comment", nil },
		SameKey:      func(a, b string) bool { return strings.Fields(a)[1] == strings.Fields(b)[1] },
		Write:        func(priv, pub []byte) error { written = priv; return nil },
	}
	return p, &written
}

// ⛔ RULE 1. Rotation signs the handover with the old key before destroying it,
// and a chain is append-only and never rebuilt, so overwriting destroys the
// only artifact that could prove the handover happened.
func TestProjectNeverOverwritesAnExistingKey(t *testing.T) {
	p, written := okProjection(t)
	p.Absent = func(string) (bool, error) { return false, nil }

	outcome, err := Project(*p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != Present {
		t.Errorf("outcome = %q, want %q", outcome, Present)
	}
	if len(*written) != 0 {
		t.Error("a key already on disk was overwritten")
	}
}

// ⛔ AND THE SHARPER VERSION: "I could not look" must never be read as "not
// there". A permission error read as absence overwrites a key it cannot see,
// which is the one outcome this function exists to prevent.
func TestProjectRefusesWhenItCannotTellWhetherTheKeyExists(t *testing.T) {
	p, written := okProjection(t)
	p.Absent = func(string) (bool, error) { return false, errors.New("permission denied") }

	if _, err := Project(*p); err == nil {
		t.Fatal("projected despite being unable to stat the key path")
	}
	if len(*written) != 0 {
		t.Error("wrote a key despite an unreadable path")
	}
}

// AbsentFromStat is the adapter that keeps the distinction above honest.
func TestAbsentFromStatSeparatesAbsenceFromFailure(t *testing.T) {
	if absent, err := AbsentFromStat(nil); absent || err != nil {
		t.Error("a successful stat should mean present")
	}
	if absent, err := AbsentFromStat(fs.ErrNotExist); !absent || err != nil {
		t.Error("ErrNotExist should mean absent")
	}
	// ⚠️ wrapped, as storage.ErrNotFound is
	if absent, err := AbsentFromStat(errors.New("boom")); absent || err == nil {
		t.Error("an unrelated error must not be read as absence")
	}
}

// ⛔ RULE 2. An absent secret is a configuration error. Generating silently
// replaces an identity, and everything published under the old key stops
// verifying against what the site serves.
func TestProjectNeverGenerates(t *testing.T) {
	p, written := okProjection(t)
	p.Value = New("")

	_, err := Project(*p)
	if err == nil {
		t.Fatal("an absent secret was accepted; it must refuse")
	}
	if !strings.Contains(err.Error(), "refusing to generate") {
		t.Errorf("the refusal should say it will not generate: %v", err)
	}
	if len(*written) != 0 {
		t.Error("wrote something despite having no secret")
	}
}

// ⛔ RULE 3, and the reason it is HERE. A secret not updated after a rotation
// holds a retired key, and the key-history chain cannot catch it: the
// chain-head check compares two fields inside the same published document,
// neither of which is the key file.
func TestProjectRefusesAStaleSecretBeforeWriting(t *testing.T) {
	p, written := okProjection(t)
	p.PublishedKey = func() (string, error) { return "ssh-ed25519 DIFFERENT host", nil }

	_, err := Project(*p)
	if err == nil {
		t.Fatal("a stale secret was projected")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("the refusal should name staleness: %v", err)
	}
	// ⭐ "BEFORE WRITING" is the half that matters: catching it afterwards
	// means the instance has already published under a key nobody accepts.
	if len(*written) != 0 {
		t.Error("wrote the stale key before checking it")
	}
}

// ⭐ A site that publishes nothing yet is the normal first-provisioning state,
// and must not be mistaken for a mismatch.
func TestProjectAllowsASiteThatPublishesNothingYet(t *testing.T) {
	for _, pub := range []func() (string, error){
		func() (string, error) { return "", nil },
		func() (string, error) { return "", errors.New("no well-known file") },
		nil,
	} {
		p, written := okProjection(t)
		p.PublishedKey = pub
		outcome, err := Project(*p)
		if err != nil {
			t.Fatalf("refused a site with nothing published: %v", err)
		}
		if outcome != Written || len(*written) == 0 {
			t.Error("should have projected the key")
		}
	}
}

// ⚠️ A secret store round-trips values without a trailing newline more often
// than not, and the OpenSSH PEM parser wants one.
func TestProjectAddsTheTrailingNewlineAPEMParserWants(t *testing.T) {
	p, written := okProjection(t)
	p.Value = New("-----BEGIN PRIVATE KEY-----body")

	if _, err := Project(*p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(*written); !strings.HasSuffix(got, "\n") {
		t.Errorf("projected key has no trailing newline: %q", got)
	}
}

// ⛔ A missing dependency must not degrade to skipping a check. A nil SameKey
// silently disabling the stale check is exactly the class of failure this
// guards against.
func TestProjectRefusesAnIncompleteProjection(t *testing.T) {
	for _, drop := range []string{"Absent", "ValidateKey", "PublicKeyFor", "SameKey", "Write"} {
		p, _ := okProjection(t)
		switch drop {
		case "Absent":
			p.Absent = nil
		case "ValidateKey":
			p.ValidateKey = nil
		case "PublicKeyFor":
			p.PublicKeyFor = nil
		case "SameKey":
			p.SameKey = nil
		case "Write":
			p.Write = nil
		}
		if _, err := Project(*p); err == nil {
			t.Errorf("projected with a nil %s; a missing dependency must refuse, not skip a check", drop)
		}
	}
	p, _ := okProjection(t)
	p.KeyPath = ""
	if _, err := Project(*p); err == nil {
		t.Error("projected with no key path")
	}
}

// ⛔ The refusal must not quote the key. It is a private key.
func TestProjectDoesNotEchoTheSecret(t *testing.T) {
	const leak = "LEAKME-private-key-body"
	p, _ := okProjection(t)
	p.Value = New(leak)
	p.ValidateKey = func([]byte) error { return errors.New("malformed PEM") }

	_, err := Project(*p)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), leak) {
		t.Errorf("the refusal echoed the private key: %v", err)
	}
}
