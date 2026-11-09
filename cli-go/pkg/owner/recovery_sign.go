package owner

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// The owner's side of recovery: signing a challenge with the key they hold.
//
// ⭐ THE SAME CODE FOR EITHER KEY. Server-signed, the owner holds a
// recovery-only key; client-signed, they hold the identity key. Both prove
// the same way, so nothing here asks which one it was given.

// RecoveryPublicKeyKey is where a branched site carries the owner's recovery
// PUBLIC key, for the deploy step to set as POLIS_RECOVERY_KEY before the
// instance first starts.
//
// ⭐ It is a public key, so the file is harmless wherever the site goes,
// including into the instance's store. ⛔ The PRIVATE half is never written
// inside a site.
const RecoveryPublicKeyKey = ".polis/deploy/recovery.pub"

// challengePrefix is the first line of every challenge message.
const challengePrefix = "pub.polis.recovery.challenge.v1"

// ErrNotAChallenge means the input is not a recovery challenge message.
//
// ⛔ Refusing it is the point: without the check, a recovery signer is a
// general signer under the recovery namespace, and anything it signs could be
// presented as a proof.
var ErrNotAChallenge = errors.New("owner: not a " + challengePrefix + " challenge message")

// ParseChallenge reads a challenge the owner pasted: the message itself (what
// the setup screen shows under "Copy message only"), or the challenge JSON the
// instance returns, which carries the message. It returns the domain and nonce.
//
// ⚠️ Line endings and trailing whitespace are forgiven, because a terminal
// paste mangles both; the signature is always made over the canonical message
// rebuilt from the parts, which is the exact text the instance verifies.
func ParseChallenge(input []byte) (domain, nonce string, err error) {
	text := strings.TrimSpace(string(input))
	if strings.HasPrefix(text, "{") {
		var ch struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(text), &ch) != nil || ch.Message == "" {
			return "", "", ErrNotAChallenge
		}
		text = strings.TrimSpace(ch.Message)
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) != 3 {
		return "", "", ErrNotAChallenge
	}
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	if lines[0] != challengePrefix {
		return "", "", ErrNotAChallenge
	}
	domain, nonce = lines[1], lines[2]
	if domain == "" || nonce == "" || strings.ContainsAny(domain+nonce, " \t") {
		return "", "", ErrNotAChallenge
	}
	return domain, nonce, nil
}

// SignChallenge signs a pasted challenge under RecoveryNamespace with the
// owner's private key (OpenSSH PEM), and returns the armoured signature the
// owner pastes back.
//
// ⛔ NEVER the "file" namespace: a content signature can never pass as a
// recovery proof, and this signature can never pass as content.
func SignChallenge(input, privateKeyPEM []byte) (string, error) {
	domain, nonce, err := ParseChallenge(input)
	if err != nil {
		return "", err
	}
	sig, err := signing.SignContentNS([]byte(challengeMessage(domain, nonce)), privateKeyPEM, RecoveryNamespace)
	if err != nil {
		// ⛔ Names the problem, never the key.
		return "", fmt.Errorf("owner: the key does not parse as an OpenSSH Ed25519 private key")
	}
	return sig, nil
}
