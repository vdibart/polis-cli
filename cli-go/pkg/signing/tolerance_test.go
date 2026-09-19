package signing_test

import (
	"reflect"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// The shared helper behind SIGNET epic 47. Six packages call it and none
// re-derives the rule, so everything the rule promises is asserted once, here.

type innerShape struct {
	URL   string `json:"url"`
	Added string `json:"added,omitempty"`
}

type midShape struct {
	Post    string       `json:"post"`
	Blessed []innerShape `json:"blessed"`
}

type outerShape struct {
	Version  string            `json:"version"`
	Comments []midShape        `json:"comments"`
	Subject  innerShape        `json:"subject"`
	Payload  map[string]string `json:"payload,omitempty"`
	Terms    *innerShape       `json:"terms,omitempty"`
	Skipped  string            `json:"-"`
	Untagged string
}

func TestUnrecognisedFieldsFindsAMemberAtEveryNestingLevel(t *testing.T) {
	// ⭐ D1: "recognised" is per nesting level, INCLUDING list entries. A rule
	// that only looked at the top-level object would miss exactly the field
	// this epic was raised for — epic 11's agent marker sits on a per-post
	// `blessed` entry, two levels down.
	raw := []byte(`{
	  "version": "polis-cli-go/test",
	  "endorsements": 3,
	  "comments": [
	    {"post": "https://a.example/p", "blessed": [
	      {"url": "https://b.example/c"},
	      {"url": "https://b.example/d", "agent": "rosie", "grant": "https://a.example/g"}
	    ], "sealed": true},
	    {"post": "https://a.example/q", "blessed": []}
	  ],
	  "subject": {"url": "https://a.example", "kind": "identity"},
	  "payload": {"result": "pass", "vantage": "edge"},
	  "terms": {"url": "https://a.example/l", "profile": "reserved"}
	}`)

	got := signing.UnrecognisedFields(raw, outerShape{})
	want := []string{
		"comments[0].blessed[1].agent",
		"comments[0].blessed[1].grant",
		"comments[0].sealed",
		"endorsements",
		"subject.kind",
		"terms.profile",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UnrecognisedFields =\n  %q\nwant\n  %q", got, want)
	}
}

func TestUnrecognisedFieldsTreatsMapKeysAsDataNotSchema(t *testing.T) {
	// ⚠️ The attestation `payload` is an open map by design — an unrecognised
	// predicate already loads rather than being rejected — so a payload key
	// this build has never seen is ordinary content, not a wider field set.
	// Reporting one would make every attestation carrying a new payload key
	// read as uncheckable.
	raw := []byte(`{"version":"v","comments":[],"subject":{"url":"u"},"payload":{"anything_at_all":"1","zzz":"2"}}`)
	if got := signing.UnrecognisedFields(raw, outerShape{}); len(got) != 0 {
		t.Errorf("map keys reported as unrecognised: %q", got)
	}
}

func TestUnrecognisedFieldsHonoursTheJSONTags(t *testing.T) {
	// A `json:"-"` field is NOT parsed, so a document carrying that member
	// really is saying something this build cannot read. An untagged field is
	// matched by its Go name, the way encoding/json does.
	raw := []byte(`{"version":"v","comments":[],"subject":{"url":"u"},"Skipped":"x","Untagged":"y"}`)
	got := signing.UnrecognisedFields(raw, outerShape{})
	want := []string{"Skipped"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("UnrecognisedFields = %q, want %q", got, want)
	}
}

func TestUnrecognisedFieldsMatchesTheWayEncodingJSONMatches(t *testing.T) {
	// ⛔ `encoding/json` falls back to a CASE-INSENSITIVE match, so a document
	// writing "Version" really is parsed into a field tagged `json:"version"`.
	// Reporting it as unrecognised would be a lie about what this build reads —
	// and on a tampered file it would downgrade `invalid` to `unknown` for no
	// reason at all. "Recognised" must mean "this build parses it", exactly.
	raw := []byte(`{"Version":"v","COMMENTS":[],"subject":{"URL":"u"}}`)
	if got := signing.UnrecognisedFields(raw, outerShape{}); len(got) != 0 {
		t.Errorf("case variants reported as unrecognised: %q", got)
	}

	// The fallback must not swallow a member that genuinely has no field.
	raw = []byte(`{"version":"v","VERSIONING":"x","comments":[],"subject":{"url":"u"}}`)
	got := signing.UnrecognisedFields(raw, outerShape{})
	if len(got) != 1 || got[0] != "VERSIONING" {
		t.Errorf("UnrecognisedFields = %q, want [VERSIONING]", got)
	}
}

func TestUnrecognisedFieldsIsSilentOnInputItCannotRead(t *testing.T) {
	// It answers "which members are new to me". A document that does not parse
	// has already failed somewhere that reports parse errors, and inventing a
	// finding here would turn one failure into two.
	for name, raw := range map[string]string{
		"not JSON":    `{{{`,
		"not object":  `[1,2,3]`,
		"empty":       ``,
		"null":        `null`,
		"type clash":  `{"version":"v","comments":"not-a-list"}`,
		"nested null": `{"version":"v","comments":null,"subject":null}`,
	} {
		if got := signing.UnrecognisedFields([]byte(raw), outerShape{}); len(got) != 0 {
			t.Errorf("%s: reported %q, want nothing", name, got)
		}
	}
}

func TestResolveIsTheWholeTable(t *testing.T) {
	// ⭐ The four rows of epic 47's table, in one place, because this is the one
	// place they are decided.
	extra := []string{"endorsements"}

	cases := []struct {
		name         string
		verified     bool
		unrecognised []string
		want         string
		explains     bool
	}{
		{"verifies, nothing unknown", true, nil, signing.StatusValid, false},
		{"verifies, unknown present", true, extra, signing.StatusValid, true},
		{"fails, unknown present", false, extra, signing.StatusUnknown, true},
		{"fails, nothing unknown", false, nil, signing.StatusInvalid, false},
	}
	for _, c := range cases {
		out := signing.Resolve(c.verified, c.unrecognised)
		if out.Status != c.want {
			t.Errorf("%s: status = %q, want %q", c.name, out.Status, c.want)
		}
		if got := out.Explain() != ""; got != c.explains {
			t.Errorf("%s: Explain()=%q, wanted an explanation: %v", c.name, out.Explain(), c.explains)
		}
	}
}

func TestResolveNeverReportsATamperedArtifactAsValid(t *testing.T) {
	// ⚠️ The accepted cost of the rule is that a tamperer can add a junk member
	// and turn `invalid` into `unknown`. What must NEVER happen is the next step
	// along: unknown is not valid, and no consumer may read it as acceptance.
	out := signing.Resolve(false, []string{"junk"})
	if out.Status == signing.StatusValid {
		t.Fatal("a signature that does not verify reported as valid")
	}
	if out.Status != signing.StatusUnknown {
		t.Fatalf("status = %q, want %q", out.Status, signing.StatusUnknown)
	}
}

func TestExplainSaysWhichFieldsAndWhy(t *testing.T) {
	// D3: the status vocabulary does not grow; the REASON lives in the
	// explanation. So the explanation has to actually carry it.
	unknown := signing.Resolve(false, []string{"endorsements", "comments[0].sealed"}).Explain()
	for _, want := range []string{"does not understand", "endorsements", "comments[0].sealed"} {
		if !contains(unknown, want) {
			t.Errorf("unknown explanation %q does not mention %q", unknown, want)
		}
	}

	// The `valid` row owes the reader the second half of the answer: the
	// signature verified, and these fields were not part of what it covered.
	valid := signing.Resolve(true, []string{"endorsements"}).Explain()
	for _, want := range []string{"verifies", "not covered", "endorsements"} {
		if !contains(valid, want) {
			t.Errorf("valid explanation %q does not mention %q", valid, want)
		}
	}

	if note := signing.UncoveredNote(nil); note != "" {
		t.Errorf("UncoveredNote(nil) = %q, want empty so a caller can append it unconditionally", note)
	}
	if note := signing.UncoveredNote([]string{"endorsements"}); !contains(note, "not covered by the signature") {
		t.Errorf("UncoveredNote = %q", note)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
