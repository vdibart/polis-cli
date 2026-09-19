package site

import (
	"fmt"
	"time"
)

// historyTimestampLayout is the one form every key-history timestamp is written
// in, which is what makes EntryAt's string comparison a time comparison.
const historyTimestampLayout = "2006-01-02T15:04:05Z"

// VerifyWithHistory is THE rule for resolving a signature to the key that made
// it (SIGNET epic 31 D1–D4), for every signing family.
//
// verify reports whether the artifact's signed bytes verify against a key — each
// type's own predicate, so nothing here rebuilds a signing base. The current key
// is tried first (D2); only if it fails is the key the chain resolves for the
// artifact's CLAIMED signing time tried, and only inside that key's window.
//
// claimed is the artifact's own signing time and field names it for messages
// (`published:` for markdown, `asserted` for an attestation, `updated` for a tag
// file or licence).
//
// Returns ok, and the retired entry that verified it — nil when the current key
// did. chain must already be trusted (sitecheck.ResolvingChain); nil means
// current key only.
//
// ⛔ Epic 44 moved this here from pkg/sitecheck so the JSON family could reach
// it: pkg/attestation and pkg/tag cannot import sitecheck, which imports them.
// It is the same walk, moved, not a second one — sitecheck's
// VerifySignatureWithHistory is now a wrapper.
func VerifyWithHistory(verify func(key []byte) (bool, error), pubKey []byte, chain *KeyHistoryBlock, claimed, field string) (bool, *KeyHistoryEntry, error) {
	// D2: the common path, unchanged.
	valid, verr := verify(pubKey)
	if verr == nil && valid {
		return true, nil, nil
	}
	if chain == nil || len(chain.History) == 0 {
		return false, nil, verr
	}

	if claimed == "" {
		return false, nil, fmt.Errorf("does not verify against the current key, and the artifact claims no signing time (`%s`), so the key history cannot say which earlier key to try", field)
	}
	// The artifact's claim is compared as a string against the history's, so it
	// must be in the history's form. Parsing it here is safe — it is the
	// artifact's field, never a history string that went into a signature.
	if t, perr := time.Parse(historyTimestampLayout, claimed); perr != nil || t.Format(historyTimestampLayout) != claimed {
		return false, nil, fmt.Errorf("does not verify against the current key, and the claimed signing time %q is not in the key history's timestamp form (%s), so it cannot be placed in the history", claimed, historyTimestampLayout)
	}

	entry, ok := chain.EntryAt(claimed)
	if !ok {
		return false, nil, fmt.Errorf("does not verify against the current key, and the claimed signing time %s falls outside every key's window in the site's published history", claimed)
	}
	if entry.ValidUntil == "" {
		// A retired key's window has an end, and a signature claiming a time after
		// it is not resolved to that key — which is how the window is enforced
		// even though the claim inside it cannot be checked.
		return false, nil, fmt.Errorf("does not verify against the current key, and the claimed signing time %s falls inside the current key's window, so no retired key applies", claimed)
	}

	ok, rerr := verify([]byte(entry.Key))
	if rerr != nil || !ok {
		return false, nil, fmt.Errorf("does not verify against the current key, nor against epoch %d's key, which the site's published history resolves for the claimed signing time %s", entry.Epoch, claimed)
	}
	return true, &entry, nil
}
