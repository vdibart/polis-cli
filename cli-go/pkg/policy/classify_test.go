package policy

import (
	"strings"
	"testing"
)

func TestClassifyCurrentDefaultWhateverTheGenerator(t *testing.T) {
	for _, f := range []File{PublicFile, PrivateFile} {
		if c := Classify([]byte(f.DefaultContent()), f); c != Current {
			t.Errorf("%s default classifies as %s, want current", f, c)
		}
		other := strings.Replace(f.DefaultContent(), GetGenerator(), "polis-cli-go/0.1.0", 1)
		if c := Classify([]byte(other), f); c != Current {
			t.Errorf("%s default from another build classifies as %s, want current", f, c)
		}
		// Whitespace and a missing trailing newline are not an edit.
		loose := "\n  " + strings.ReplaceAll(strings.TrimSpace(f.DefaultContent()), "\n", "\n\n ") + "  "
		if c := Classify([]byte(loose), f); c != Current {
			t.Errorf("%s default with loose whitespace classifies as %s, want current", f, c)
		}
	}
}

// Every past default is recognised as one, for its own file only, and none of
// them is the current default (which would make it unreachable in the list).
func TestEveryPastDefaultClassifiesAsPastDefault(t *testing.T) {
	seen := map[string]bool{}
	for _, pd := range PastDefaults {
		if pd.Origin == "" {
			t.Errorf("past default without an origin: %q", pd.Content)
		}
		if c := Classify([]byte(pd.Content), pd.File); c != PastDefault {
			t.Errorf("%s past default (%s) classifies as %s", pd.File, pd.Origin, c)
		}
		key := string(pd.File) + "\x00" + ContentKey(pd.Content)
		if seen[key] {
			t.Errorf("%s past default listed twice (%s)", pd.File, pd.Origin)
		}
		seen[key] = true
		if ContentKey(pd.Content) == ContentKey(pd.File.DefaultContent()) {
			t.Errorf("%s past default (%s) is the current default", pd.File, pd.Origin)
		}
	}
}

// A file built by the recipes in the policy docs is the site's own, and so is
// a default with one rule removed or switched off: nothing about a file made
// only of default rules makes it a default.
func TestClassifyAuthored(t *testing.T) {
	pub := DefaultPublicPolicyContent()
	priv := DefaultPrivatePolicyContent()
	cases := []struct {
		name    string
		f       File
		content string
	}{
		{"block a domain", PrivateFile, priv + `{"active":true,"policy":"deny all from all at spammer.example"}` + "\n"},
		{"deny all comments outright", PublicFile, strings.Replace(pub,
			`{"active":true,"policy":"review pub.polis.comment from all"}`,
			`{"active":true,"policy":"deny pub.polis.comment from all"}`+"\n"+`{"active":true,"policy":"review pub.polis.comment from all"}`, 1)},
		{"accept DMs from everyone", PublicFile, strings.Replace(strings.Replace(pub,
			`{"active":true,"policy":"allow pub.polis.dm from following"}`+"\n", "", 1),
			`{"active":true,"policy":"deny pub.polis.dm from all"}`, `{"active":true,"policy":"allow pub.polis.dm from all"}`, 1)},
		{"rule switched off", PublicFile, strings.Replace(pub,
			`{"active":true,"policy":"bless pub.polis.comment from thread-blessed"}`,
			`{"active":false,"policy":"bless pub.polis.comment from thread-blessed"}`, 1)},
		{"rule removed", PublicFile, strings.Replace(pub, `{"active":true,"policy":"deny all from all"}`+"\n", "", 1)},
		{"v1 header with the owner's rules", PrivateFile, `{"version":1,"generator":"polis-cli-go/0.61.0"}` + "\n" +
			`{"active":true,"policy":"deny all from all at spammer.example"}` + "\n"},
		{"empty file", PrivateFile, ""},
		// A past default in the other file's slot is not a default for this one.
		{"public default in the private file", PrivateFile, pub},
	}
	for _, c := range cases {
		if got := Classify([]byte(c.content), c.f); got != Authored {
			t.Errorf("%s: classifies as %s, want authored", c.name, got)
		}
	}
}

func TestClassifyUnparseable(t *testing.T) {
	pub := DefaultPublicPolicyContent()
	cases := map[string]string{
		"truncated line":   pub + `{"active":true,"policy":"deny all from`,
		"not JSON":         "deny all from all\n",
		"rule not parsing": pub + `{"active":true,"policy":"deny everything from everyone"}` + "\n",
		"active not bool":  pub + `{"active":"yes","policy":"deny all from all at x.example"}` + "\n",
		"policy not text":  pub + `{"active":true,"policy":7}` + "\n",
	}
	for name, content := range cases {
		if got := Classify([]byte(content), PublicFile); got != Unparseable {
			t.Errorf("%s: classifies as %s, want unparseable", name, got)
		}
	}
}

func TestHeaderVersion(t *testing.T) {
	cases := map[string]int{
		DefaultPublicPolicyContent():                          2,
		`{"version":1,"generator":"polis-cli/0.57.0"}` + "\n": 1,
		`{"active":true,"policy":"deny all from all"}` + "\n": 0,
		"": 0,
	}
	for content, want := range cases {
		if got := HeaderVersion([]byte(content)); got != want {
			t.Errorf("HeaderVersion(%q) = %d, want %d", content, got, want)
		}
	}
}
