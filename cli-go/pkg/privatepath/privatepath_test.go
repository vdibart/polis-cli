package privatepath

import (
	"go/parser"
	"go/token"
	"net/url"
	"path"
	"strings"
	"testing"
)

func TestDeniedRefusesListedNamesInAnySegment(t *testing.T) {
	for _, p := range []string{
		".polis/keys/id_ed25519", "/.polis/keys/id_ed25519", ".env", ".env.local", ".env.example",
		".git/config", "content/.git/config", "content/.git/HEAD", "site/themes/x/.git/HEAD",
		".ssh/id_ed25519", "site/.ssh/id_ed25519", "site/id_rsa", "id_rsa.pub", "id_ecdsa",
		"posts/.env", ".DS_Store", "posts/.DS_Store", "x.swp", "index.html~", "a.bak", "a.orig",
		"server.pem", "tls/server.key", "cert.p12", ".htpasswd", ".npmrc", ".netrc", ".aws/credentials",
		".gnupg/pubring.kbx", ".hg/hgrc", ".svn/entries", ".hidden/index.html", "posts/.secret/x.html",
		// case-insensitive
		".GIT/config", "content/.Git/HEAD", ".POLIS/keys/id_ed25519", "SITE/ID_RSA", "x.PEM",
		// an exception never exempts a listed name beneath it
		"content/p/.versions/.git/config", "content/p/.versions/id_rsa",
		// .well-known is an exception only at the root
		"content/.well-known/polis",
	} {
		if !Denied(p) {
			t.Errorf("Denied(%q) = false, want true", p)
		}
	}
}

func TestDeniedServesPublicPaths(t *testing.T) {
	for _, p := range []string{
		"", "/", "index.html", ".well-known/polis", ".well-known/did.json", ".WELL-KNOWN/polis",
		"robots.txt", "rsl.xml", "sitemap.xml", "stream.js", "posts/20260101/hello.html",
		"content/pub.polis.core/post/20260101/hello.md",
		"content/pub.polis.core/post/20260101/.versions/hello.md",
		"content/pub.polis.core/license/license.json", "license/index.html",
		"posts/id-rsa-explained.html", "posts/keys.html",
	} {
		if Denied(p) {
			t.Errorf("Denied(%q) = true, want false", p)
		}
	}
}

func TestEveryEntryIsStated(t *testing.T) {
	exceptions := map[string]bool{}
	for _, e := range Entries {
		if e.Pattern == "" || e.Reason == "" || (e.Kind != PolisCreated && e.Kind != Conventional) {
			t.Errorf("entry %+v needs a pattern, a kind and a reason", e)
		}
		if e.Exception {
			exceptions[e.Pattern] = true
			if len(e.Probe) != 0 {
				t.Errorf("exception %q carries probe paths; the probe never asks for a public name", e.Pattern)
			}
			continue
		}
		if len(e.Probe) == 0 {
			t.Errorf("denial %q has no probe path, so nothing checks it from outside", e.Pattern)
		}
		for _, p := range e.Probe {
			if !Denied(p) {
				t.Errorf("denial %q probes %q, which Denied does not refuse", e.Pattern, p)
			}
		}
	}
	for _, want := range []string{".well-known", ".versions"} {
		if !exceptions[want] {
			t.Errorf("%s must be a stated exception", want)
		}
	}
}

// Every name the ticket's inventory names must be on the list by name, not
// only through the hidden-name rule, so the probe asks for each of them.
func TestTheListNamesEveryKnownSecret(t *testing.T) {
	named := map[string]bool{}
	for _, e := range Entries {
		if !e.Exception && e.Pattern != hiddenPrefix {
			named[e.Pattern] = true
		}
	}
	for _, want := range []string{".polis", ".env", ".env.", ".git", ".hg", ".svn", ".ssh", ".gnupg", ".aws",
		".npmrc", ".netrc", ".htpasswd", ".DS_Store", "id_rsa", "id_ed25519", "id_ecdsa",
		".pem", ".key", ".p12", ".swp", "~", ".bak", ".orig"} {
		if !named[want] {
			t.Errorf("%q is not named on the list", want)
		}
	}
}

func TestProbeTargetsCoverTheKeyAndItsSpellings(t *testing.T) {
	targets := ProbeTargets()
	have := map[string]bool{}
	for _, p := range targets {
		have[p] = true
	}
	for _, want := range []string{KeyPath, "/%2Epolis/keys/id_ed25519", "/.POLIS/keys/id_ed25519",
		"/content/.git/config", "/content/.git/HEAD", "/site/.ssh/id_ed25519", "/site/id_rsa"} {
		if !have[want] {
			t.Errorf("ProbeTargets lacks %q", want)
		}
	}
	// Every target, once decoded and cleaned the way a server would, is denied.
	for _, p := range targets {
		dec, err := url.PathUnescape(p)
		if err != nil {
			t.Fatalf("target %q does not decode: %v", p, err)
		}
		if !Denied(path.Clean(dec)) {
			t.Errorf("target %q (cleaned %q) is not denied", p, path.Clean(dec))
		}
	}
}

// The storage layer imports this package, so it must import nothing from polis.
func TestImportsNothingFromPolis(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "privatepath.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range f.Imports {
		if strings.Contains(imp.Path.Value, "polis-cli") {
			t.Errorf("privatepath imports %s; it must stay a leaf", imp.Path.Value)
		}
	}
}

// ⛔ A backslash is a separator on Windows, so a backslash-joined path must not
// hide a private segment — and the exceptions must still match.
func TestABackslashJoinedPathIsCheckedSegmentBySegment(t *testing.T) {
	for _, p := range []string{`content\.git\config`, `site\.ssh\id_ed25519`, `a\.env`} {
		if !Denied(p) {
			t.Errorf("Denied(%q) = false; a backslash-joined private segment must be denied", p)
		}
	}
	if Denied(`.well-known\polis`) {
		t.Error(`Denied(".well-known\polis") = true; the exception must hold for a backslash form too`)
	}
}
