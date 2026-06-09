package dm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// DMDir returns the tenant DM directory under siteDir
// (.polis/bundles/pub.polis.core/dm) — the single source of truth for where the
// keyring, conversations, and inbox live.
func DMDir(siteDir string) string {
	return filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "dm")
}

// IsProtected reports whether the site has secured its messages: true iff the keyring
// has at least one password epoch. A site with no keyring (not provisioned) or only the
// bootstrap epoch is unprotected — operator-readable by design. This is the single bit
// the protection_status endpoint exposes; it never reveals keys, counts, or contents.
func IsProtected(siteDir string) (bool, error) {
	kr, err := LoadKeyring(DMDir(siteDir))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("load keyring: %w", err)
	}
	for _, e := range kr.Epochs {
		if e.Kind == EpochKindPassword {
			return true, nil
		}
	}
	return false, nil
}

// ProtectionStatus is the signed answer to a protection_status query: the responder's
// domain, the single protected bit, a timestamp, and an identity-key signature so an
// operator in the path cannot spoof it. See PROTOCOL.md §4.
type ProtectionStatus struct {
	Domain    string `json:"domain"`
	Protected bool   `json:"protected"`
	At        string `json:"at"`  // RFC3339
	Sig       string `json:"sig"` // identity-key signature over canonicalProtectionStatus
}

// canonicalProtectionStatus is the frozen byte string signed/verified for a status
// answer — stable across implementations (PROTOCOL.md). Do not reorder or reformat.
func canonicalProtectionStatus(domain string, protected bool, at string) []byte {
	return []byte(fmt.Sprintf("pub.polis.dm.protection_status\ndomain=%s\nprotected=%t\nat=%s", domain, protected, at))
}

// SignProtectionStatus signs a protected bit with the site's identity private key (PEM).
func SignProtectionStatus(domain string, protected bool, at string, identityPrivPEM []byte) (*ProtectionStatus, error) {
	sig, err := signing.SignContent(canonicalProtectionStatus(domain, protected, at), identityPrivPEM)
	if err != nil {
		return nil, fmt.Errorf("sign protection status: %w", err)
	}
	return &ProtectionStatus{Domain: domain, Protected: protected, At: at, Sig: sig}, nil
}

// VerifyProtectionStatus checks a signed answer against the responder's identity public
// key (OpenSSH). Callers MUST verify before trusting the protected bit.
func VerifyProtectionStatus(ps ProtectionStatus, identityPubSSH []byte) (bool, error) {
	return signing.VerifySignature(canonicalProtectionStatus(ps.Domain, ps.Protected, ps.At), identityPubSSH, ps.Sig)
}

// ErrProtectionStatusForbidden is returned when the caller is outside the responder's
// follow relationship. Gate #3 ("Three independent gates"): protection-status exposure is
// minimal and front-checked against the follow file — independent of the inbound DM
// policy gate (#2) and the send-time mutual-follow UI gate (#1).
var ErrProtectionStatusForbidden = errors.New("caller not in follow relationship")

// BuildProtectionStatus front-checks the caller against the responder's following set,
// then returns a signed protected bit stamped with the current time. callerDomain must be
// a domain the responder follows; otherwise ErrProtectionStatusForbidden (the handler maps
// it to 403/404 so a non-follower learns nothing).
func BuildProtectionStatus(siteDir, responderDomain string, identityPrivPEM []byte, callerDomain string, followingDomains map[string]bool) (*ProtectionStatus, error) {
	if !followingDomains[strings.ToLower(callerDomain)] {
		return nil, ErrProtectionStatusForbidden
	}
	protected, err := IsProtected(siteDir)
	if err != nil {
		return nil, err
	}
	return SignProtectionStatus(responderDomain, protected, nowRFC3339(), identityPrivPEM)
}
