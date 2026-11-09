package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

func recoveryKeyAt(t *testing.T, p string, mode os.FileMode) (priv, pub []byte) {
	t.Helper()
	priv, pub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, priv, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return priv, pub
}

const testChallenge = "pub.polis.recovery.challenge.v1\nhttps://alice-two.example.test\nabc.def\n"

// The default key is ~/.polis/recovery/<host>, the host read from the
// challenge, and the signature is made under the recovery namespace only.
func TestRecoverSignUsesTheDefaultKeyAndTheRecoveryNamespace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	_, pub := recoveryKeyAt(t, filepath.Join(home, ".polis", "recovery", "alice-two.example.test"), 0o600)

	// A terminal paste: CRLF, no trailing newline.
	pasted := strings.TrimSpace(strings.ReplaceAll(testChallenge, "\n", "\r\n"))
	res, err := recoverSign([]byte(pasted), "", "alice-two.example.test")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if res.Domain != "alice-two.example.test" {
		t.Errorf("domain = %q", res.Domain)
	}
	ok, err := signing.VerifySignatureNS([]byte(testChallenge), pub, res.Signature, owner.RecoveryNamespace)
	if err != nil || !ok {
		t.Errorf("the signature does not verify over the canonical message under %s: %v", owner.RecoveryNamespace, err)
	}
	if ok, _ := signing.VerifySignature([]byte(testChallenge), pub, res.Signature); ok {
		t.Error("⛔ the recovery signature verifies as a content signature")
	}

	// The challenge JSON works too.
	js := `{"nonce":"abc.def","domain":"https://alice-two.example.test","message":"` +
		strings.ReplaceAll(testChallenge, "\n", `\n`) + `"}`
	if _, err := recoverSign([]byte(js), "", "https://alice-two.example.test/"); err != nil {
		t.Errorf("the challenge JSON was refused: %v", err)
	}
}

func TestRecoverSignRefusals(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	good := filepath.Join(home, ".polis", "recovery", "alice-two.example.test")
	recoveryKeyAt(t, good, 0o600)
	loose := filepath.Join(t.TempDir(), "loose")
	recoveryKeyAt(t, loose, 0o644)

	cases := []struct {
		name, input, key, domain, text string
	}{
		{"arbitrary text", "sign me please\n", good, "", "not a recovery challenge"},
		{"a post", "---\ntitle: x\n---\nhello\n", good, "", "not a recovery challenge"},
		{"an extra line", testChallenge + "more\n", good, "", "not a recovery challenge"},
		{"a key others can read", testChallenge, loose, "alice-two.example.test", "readable by others"},
		{"no --domain: the site must come from the owner", testChallenge, good, "", "say which site you are signing in to"},
		{"a challenge for another site", testChallenge, good, "bob.example.test", "not bob.example.test"},
		{"no key at the default path", strings.Replace(testChallenge, "alice-two", "carol", 1), "", "carol.example.test", "no recovery key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := recoverSign([]byte(c.input), c.key, c.domain)
			if err == nil || !strings.Contains(err.Error(), c.text) {
				t.Fatalf("err = %v, want a refusal mentioning %q", err, c.text)
			}
			if strings.Contains(err.Error(), "PRIVATE KEY") {
				t.Errorf("the refusal prints a key: %v", err)
			}
		})
	}
}

// The challenge comes from a file, from stdin, or from "-" (stdin), and an
// empty stdin with no terminal names the forms that work instead of saying
// "paste".
func TestRecoverSignReadsTheChallengeFromAFileOrStdin(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "challenge.txt")
	if err := os.WriteFile(file, []byte(testChallenge), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, path, stdin string
		terminal          bool
	}{
		{"a file", file, "", false},
		{"stdin", "", testChallenge, false},
		{"dash is stdin", "-", testChallenge, false},
		{"the file wins over stdin", file, "ignored\n", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := readChallenge(c.path, strings.NewReader(c.stdin), c.terminal)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != testChallenge {
				t.Fatalf("read %q, want the challenge", got)
			}
		})
	}

	t.Run("empty stdin with no terminal names the file and pipe forms", func(t *testing.T) {
		_, err := readChallenge("", strings.NewReader(""), false)
		if err == nil {
			t.Fatal("an empty, non-terminal stdin was accepted")
		}
		for _, want := range []string{"no terminal", "--challenge <file>", "| polis recover sign"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
		if strings.Contains(err.Error(), "paste the message") {
			t.Errorf("error %q tells someone with no terminal to paste", err)
		}
	})
	t.Run("empty stdin at a terminal is left to the challenge check", func(t *testing.T) {
		if _, err := readChallenge("", strings.NewReader(""), true); err != nil {
			t.Fatalf("err = %v; a terminal user who pressed Ctrl-D early gets the usual refusal later", err)
		}
	})
	t.Run("an empty file is refused", func(t *testing.T) {
		if _, err := readChallenge(empty, strings.NewReader(""), false); err == nil || !strings.Contains(err.Error(), "is empty") {
			t.Fatalf("err = %v, want an empty-file refusal", err)
		}
	})
	t.Run("a missing file is refused", func(t *testing.T) {
		if _, err := readChallenge(filepath.Join(dir, "nope"), strings.NewReader(""), false); err == nil {
			t.Fatal("a missing file was accepted")
		}
	})
}
