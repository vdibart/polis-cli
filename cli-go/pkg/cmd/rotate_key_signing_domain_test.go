package cmd

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// `polis rotate-key` records the domain the handover was signed under on the
// new entry, so the chain still verifies when it is read at another domain.
func TestRotateKeyRecordsTheSigningDomain(t *testing.T) {
	dir, s := initRotationSite(t)
	runRotateKeyJSON(t, dir, silentDS(t))

	block, err := site.LoadKeyHistory(s)
	if err != nil || block == nil {
		t.Fatalf("load key history: %v", err)
	}
	if got := block.Current.SigningDomain; got != "alice.polis.pub" {
		t.Errorf("signing_domain = %q, want alice.polis.pub", got)
	}
	if err := site.VerifyChain(block, "elsewhere.example"); err != nil {
		t.Errorf("the chain must verify when read at another domain: %v", err)
	}
}
