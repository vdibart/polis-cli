package sitecheck

import (
	"strings"
	"testing"
)

// policy.LoadPolicies skips a line it cannot parse, so a typo makes the rule
// vanish. validate says so, naming the file and line; nothing else changes.
func TestParsePolicyLinesReportsANonJSONLine(t *testing.T) {
	body := strings.Join([]string{
		`{"version":"polis-cli-go/test","generator":"x"}`,
		``,
		`{"policy":"bless pub.polis.comment from following"}`,
		`{"policy":"deny pub.polis.comment from *"`, // missing brace
		`   `,
	}, "\n")
	w := ParsePolicyLines(body, "policies/rules.jsonl")
	if len(w) != 1 {
		t.Fatalf("warnings = %+v, want exactly one for the broken line", w)
	}
	if w[0].File != "policies/rules.jsonl" || w[0].Line != 4 || !strings.Contains(w[0].Error, "not valid JSON") {
		t.Errorf("warning = %+v, want policies/rules.jsonl line 4, not valid JSON", w[0])
	}
}
