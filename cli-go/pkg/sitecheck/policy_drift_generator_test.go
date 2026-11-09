package sitecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
)

// A default-policy file written by an older build differs from today's default
// only in its generator. That is not drift: comparing it would flag every such
// tenant on each release, and Medic and Tailor would rewrite their files.
func TestPolicyContentDriftIgnoresTheGenerator(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "policies"), 0755)
	write := func(content string) {
		os.WriteFile(filepath.Join(dir, "policies", "rules.jsonl"), []byte(content), 0644)
	}
	def := policy.DefaultPublicPolicyContent()
	header := strings.SplitN(def, "\n", 2)

	// Same rules, an older build's generator: not drift.
	write(`{"version":2,"generator":"polis-cli-go/0.63.0"}` + "\n" + header[1])
	if d := PolicyContentDrift(dir); len(d) != 0 {
		t.Errorf("an older generator alone was reported as drift: %v", d)
	}
	// A bash-written header is the same case.
	write(`{"version":2,"generator":"polis-cli/0.70.0"}` + "\n" + header[1])
	if d := PolicyContentDrift(dir); len(d) != 0 {
		t.Errorf("a bash generator alone was reported as drift: %v", d)
	}
	// The format version still counts: a v1 header is drift.
	write(`{"version":1,"generator":"polis-cli-go/0.63.0"}` + "\n" + header[1])
	if d := PolicyContentDrift(dir); len(d) != 1 {
		t.Errorf("a v1 header was not reported as drift: %v", d)
	}
	// And any rule change is drift.
	write(header[0] + "\n" + strings.Replace(header[1], "review pub.polis.comment from all", "deny pub.polis.comment from all", 1))
	if d := PolicyContentDrift(dir); len(d) != 1 {
		t.Errorf("a changed rule was not reported as drift: %v", d)
	}
}
