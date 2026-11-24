package policy

import (
	"strings"
	"testing"
)

// Policy rules are read from every site's published policies/rules.jsonl, so a
// DS or a sender's pre-flight check parses rules a stranger wrote. Parse must
// never panic, and a rule it accepts must say the same thing when written back
// out in the grammar's own order.
func FuzzPolicyRuleParse(f *testing.F) {
	for _, line := range strings.Split(DefaultPublicPolicyContent(), "\n") {
		if i := strings.Index(line, `"policy":"`); i >= 0 {
			f.Add(strings.TrimSuffix(line[i+len(`"policy":"`):], `"}`))
		}
	}
	for _, s := range []string{
		"deny all from all at stalker.polis.pub",
		"emit pub.polis.comment.blessing from self on posts/",
		"allow pub.polis.dm from following at alice.example on x",
		"review pub.polis.comment from none",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, rule string) {
		_ = Parses([]byte(rule))
		for _, mode := range []ParseMode{TenantMode} {
			r, err := ParseInMode(rule, mode)
			if err != nil {
				continue
			}
			out := r.Action + " " + r.Type + " from " + r.Source
			if r.Domain != "" {
				out += " at " + r.Domain
			}
			if r.Target != "" {
				out += " on " + r.Target
			}
			again, err := ParseInMode(out, mode)
			if err != nil {
				t.Fatalf("%q parses, but its canonical form %q does not: %v", rule, out, err)
			}
			if *again != *r {
				t.Fatalf("%q -> %+v, but %q -> %+v", rule, r, out, again)
			}
		}
	})
}
