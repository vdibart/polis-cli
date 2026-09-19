package attestation

import (
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// VerifyRecordResolved is VerifyRecord that also reports WHICH key verified the
// record: nil for the site's current key, or the retired key-history entry whose
// window contains the record's `asserted` (Signet epics 31, 44).
//
// ⛔ WITHOUT THE HISTORY, A KEY ROTATION SILENTLY INVALIDATES EVERY RECORD THE
// SITE SIGNED BEFORE IT (Signet epic 11 review F1): a user agent's grant stops
// resolving as live, and the user cannot withdraw it either, because Withdraw
// checks its target through this same function.
//
// ⚠️ A retired-key pass still reports StatusValid — the status vocabulary has no
// fifth value, and epic 31 D4 keeps the distinction in WHICH key, not in the
// status. Callers that must say "verified by a retired key" read the entry.
func VerifyRecordResolved(siteDir string, r *Record) (SignatureStatus, *site.KeyHistoryEntry, error) {
	if r == nil || r.Signature == "" {
		return StatusUnsigned, nil, nil
	}
	keys, err := IdentityKeys(siteDir)
	if err != nil {
		return StatusUnknown, nil, err
	}
	return VerifyWithHistory(r, keys[0], trustedLocalChain(siteDir, r.Issuer, string(keys[0])))
}

// trustedLocalChain returns the site's published key history when it can be
// trusted to resolve retired keys, or nil (current key only).
//
// It applies sitecheck.ResolvingChain's two conditions, which this package
// cannot import: the chain verifies for the site's domain, and its head is the
// key the site publishes. A chain that fails either is not used — the result is
// then exactly the current-key answer, never a weaker pass.
func trustedLocalChain(siteDir, issuer, publicKey string) *site.KeyHistoryBlock {
	block, err := site.LoadKeyHistory(siteDir)
	if err != nil || block == nil || len(block.History) == 0 {
		return nil
	}
	domain := strings.TrimPrefix(strings.TrimPrefix(strings.TrimRight(issuer, "/"), "https://"), "http://")
	if i := strings.IndexByte(domain, '/'); i >= 0 {
		domain = domain[:i]
	}
	if domain == "" || site.VerifyChain(block, domain) != nil {
		return nil
	}
	if strings.TrimSpace(block.Current.Key) != strings.TrimSpace(publicKey) {
		return nil
	}
	return block
}
