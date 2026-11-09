package owner

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// Recovery: the instance's root of authority.
//
// ⭐⭐⭐ IT IS NOT "BREAK-GLASS" AND THE NAME MATTERS. This is what authorises
// the FIRST enrolment on a fresh instance and the next one after access is
// lost — two moments, one mechanism. Calling it break-glass suggests an
// emergency side door; it is the front door's hinge.
//
// ⛔ THE ENV VAR HOLDS A PUBLIC KEY, AND THAT IS THE WHOLE DESIGN. An earlier
// draft put a shared secret there, which meant the platform hosting the
// instance could read it and become the owner — silently, attributable to
// nobody. With a public key there is nothing to read: a hostile platform must
// OVERWRITE the key and REDEPLOY, which lands in the deployment history.
// ⚠️ The takeover is not prevented — whoever controls a deployment can replace
// the running code, on any platform — but every path to it moves to the LOUD
// side. That is the honest size of the claim.
//
// ⭐ IT ALSO CLOSES TRUST-ON-FIRST-USE. Because the key is set before first
// boot, only the holder of the private key can claim a fresh instance. Without
// it, whoever reaches the deployment first enrols themselves.
//
// ⭐⭐ AND THE MODES COLLAPSE. In client-signed the owner holds the IDENTITY
// key; in server-signed they hold a RECOVERY-ONLY key. Both prove the same way,
// so there is one sentence to implement and to document: prove you hold the key
// by signing a challenge.
//
// ⚠️ POLIS_RECOVERY_KEY IS CONFIG, NOT A SECRET, and deliberately does NOT go
// through pkg/secret. A public key may appear in logs, env listings and support
// tickets without harm; routing it through a seam built to refuse absence and
// redact values would say something false about what it is.

const (
	// RecoveryKeyEnv names the environment variable holding the owner's
	// recovery PUBLIC key, in OpenSSH form ("ssh-ed25519 AAAA...").
	RecoveryKeyEnv = "POLIS_RECOVERY_KEY"

	// challengeTTL bounds how long an issued challenge stays valid.
	//
	// ⭐ Long enough to switch to a terminal, run a signing command and paste
	// the result back; short enough that a challenge captured from a screen
	// share is useless by the time anyone acts on it.
	challengeTTL = 10 * time.Minute
)

// ErrNoRecoveryKey means the instance has no recovery key configured.
//
// ⭐ Distinct from a failed proof, because the remedies differ: this one needs
// POLIS_RECOVERY_KEY set and a redeploy, not a different signature.
var ErrNoRecoveryKey = errors.New("owner: no recovery key is configured for this instance")

// ErrChallengeUnknown means the nonce was never issued, has expired, or has
// already been used.
//
// ⚠️ THE THREE ARE DELIBERATELY ONE ERROR. Distinguishing them tells an
// attacker which nonces existed, and no honest caller needs to know: in every
// case the remedy is to request a fresh challenge.
var ErrChallengeUnknown = errors.New("owner: challenge is unknown, expired or already used")

// ErrBadSignature means the signature did not verify against the configured
// recovery key.
var ErrBadSignature = errors.New("owner: signature does not match the recovery key")

// Challenge is a one-time proof request.
type Challenge struct {
	// Nonce is the opaque identifier the caller returns with its signature.
	Nonce string `json:"nonce"`

	// Domain is the instance this challenge belongs to.
	Domain string `json:"domain"`

	// Message is the exact text to sign. ⭐ Returned explicitly rather than
	// described, so no caller has to reconstruct it and get it wrong.
	Message string `json:"message"`

	// Expires is when it stops being accepted.
	// Namespace is the SSHSIG namespace the signature must be made under, and
	// SignWith the command that makes one. ⭐ Stated in the challenge, so an
	// owner never has to guess it.
	Namespace string `json:"namespace"`
	SignWith  string `json:"sign_with"`

	Expires time.Time `json:"expires"`
}

// challengeMessage builds the bytes a signature must cover.
//
// ⛔ THE DOMAIN IS INSIDE THE SIGNED BYTES, AND THAT IS LOAD-BEARING. Without
// it, a signature captured from one instance — a staging copy, an old
// deployment, a site the owner also runs — verifies against ANOTHER instance
// holding the same recovery key. ⚠️ Owners WILL reuse a key across their own
// sites, so this is a likely accident rather than an exotic attack.
//
// ⭐ The prefix makes the bytes self-describing, so a signature made here can
// never be mistaken for a signature over a post.
// RecoveryNamespace is the SSHSIG namespace a recovery proof is signed under.
//
// ⛔ NOT "file", the namespace every content signature uses. A content signer
// (the render preview once signed any markdown it was given) could otherwise be
// asked to sign a challenge message and hand back a valid recovery proof —
// especially where an owner reused their identity key as the recovery key.
// Sign with: ssh-keygen -Y sign -n polis-recovery -f <recovery key> <file>
const RecoveryNamespace = "polis-recovery"

func challengeMessage(domain, nonce string) string {
	return "pub.polis.recovery.challenge.v1\n" + domain + "\n" + nonce + "\n"
}

// Recovery issues and verifies proofs of ownership.
//
// ⭐⭐ IT HOLDS CONFIGURATION AND NOTHING ELSE. A challenge is a sealed token —
// its id, this instance's domain and its expiry, MAC'd under the site's key —
// so ANY process sharing the store can check one this instance issued, and
// issuing one writes nothing. Before, the live nonces sat in a map inside this
// struct: on a serverless platform the challenge and the proof land on
// different processes, and recovery failed at random.
//
// ⛔ SINGLE USE IS ENFORCED BY THE STORE, after the signature verifies: a
// marker named by the challenge's id is written with IfAbsent, so a proof
// replayed against any process loses. ⚠️ A FAILED attempt consumes nothing.
// That reverses the old rule ("consumed on every attempt"), deliberately: a
// failed attempt proves nothing, so it may not cost a store operation, and
// leaving the challenge live gives an attacker nothing — the signature is
// checked against one fixed public key, so retrying the same challenge with
// other keys learns no more than trying fresh challenges would. The rate limit
// bounds attempts either way.
//
// ⚠️ It is safe for concurrent use.
type Recovery struct {
	mu sync.Mutex

	// publicKey is the configured recovery key, in OpenSSH form.
	publicKey []byte

	// domain is this instance's own domain, bound into every challenge.
	domain string

	// now is overridable in tests.
	now func() time.Time
}

type challengeToken struct {
	Site   string `json:"site"`
	Domain string `json:"domain"`
	ID     string `json:"id"`
	Exp    int64  `json:"exp"`
}

// NewRecovery builds a Recovery from configuration.
//
// ⛔ IT VALIDATES THE KEY AT CONSTRUCTION, NOT AT USE. A malformed
// POLIS_RECOVERY_KEY must be a startup failure the operator sees while
// deploying — not a surprise discovered by someone already locked out, at the
// moment they can least afford it.
//
// ⭐ An EMPTY value is not an error here: `local` and `custodied` need no
// recovery key, and refusing to construct would make every caller special-case
// them. Issue and Verify return ErrNoRecoveryKey instead.
func NewRecovery(publicKeySSH, domain string) (*Recovery, error) {
	r := &Recovery{
		domain: strings.TrimSpace(domain),
		now:    time.Now,
	}
	key := strings.TrimSpace(publicKeySSH)
	if key == "" {
		return r, nil
	}
	if _, err := signing.ParsePublicKey([]byte(key)); err != nil {
		return nil, fmt.Errorf("%s is not a valid OpenSSH public key: %w", RecoveryKeyEnv, err)
	}

	// ⛔⛔ FAIL CLOSED ON AN UNKNOWN DOMAIN, AND THIS WAS A REAL BUG CAUGHT BY
	// RUNNING THE SERVER RATHER THAN BY A TEST.
	//
	// The domain is bound into every challenge so a signature made for one
	// instance cannot be replayed at another holding the same recovery key —
	// and owners WILL reuse a key across their own sites. With an empty
	// domain, every such instance issues the SAME message, so the binding
	// silently stops existing and replay works everywhere.
	//
	// ⚠️ The unit tests did not catch it because they pass a domain
	// explicitly. A fresh site simply has no base URL configured yet.
	//
	// ⭐ Refusing at construction makes it a BOOT failure the operator sees
	// while deploying, with a remedy, rather than a silent weakening nobody
	// ever notices.
	if r.domain == "" {
		return nil, fmt.Errorf("%s is set but this instance does not know its own domain: "+
			"set POLIS_BASE_URL. Without it a recovery signature made for one site would be "+
			"replayable at every other site sharing the same recovery key", RecoveryKeyEnv)
	}

	r.publicKey = []byte(key)
	return r, nil
}

// Configured reports whether a recovery key is set.
func (r *Recovery) Configured() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.publicKey) > 0
}

// Issue mints a one-time challenge for this site.
//
// ⭐ It writes nothing: the challenge is sealed under the site's key, which the
// server loaded when it started. A flood of requests for challenges therefore
// costs CPU, which the rate limit bounds, and no store operation at all.
func (r *Recovery) Issue(inst Instance) (Challenge, error) {
	r.mu.Lock()
	configured := len(r.publicKey) > 0
	domain := r.domain
	now := r.now()
	r.mu.Unlock()
	if !configured {
		return Challenge{}, ErrNoRecoveryKey
	}

	id, err := randomID()
	if err != nil {
		return Challenge{}, fmt.Errorf("generating challenge: %w", err)
	}
	expires := now.Add(challengeTTL)
	nonce, err := seal(inst, purposeRecovery, challengeToken{
		Site: siteKey(inst), Domain: domain, ID: id, Exp: expires.Unix(),
	})
	if err != nil {
		return Challenge{}, fmt.Errorf("sealing challenge: %w", err)
	}

	return Challenge{
		Nonce:     nonce,
		Domain:    domain,
		Message:   challengeMessage(domain, nonce),
		Namespace: RecoveryNamespace,
		SignWith:  "ssh-keygen -Y sign -n " + RecoveryNamespace + " -f <recovery key> <file holding the message>",
		Expires:   expires,
	}, nil
}

// Verify checks a signature over a challenge this site issued, and spends it.
//
// ⭐ ORDER: the nonce's MAC and expiry (no store), then the signature (no
// store), and only then the single-use marker. A request that has proved
// nothing never reaches the store.
func (r *Recovery) Verify(inst Instance, nonce, signature string) error {
	r.mu.Lock()
	if len(r.publicKey) == 0 {
		r.mu.Unlock()
		return ErrNoRecoveryKey
	}
	key := append([]byte(nil), r.publicKey...)
	domain := r.domain
	now := r.now()
	r.mu.Unlock()

	var c challengeToken
	if err := unseal(inst, purposeRecovery, nonce, &c); err != nil {
		return ErrChallengeUnknown
	}
	expires := time.Unix(c.Exp, 0)
	if c.Site != siteKey(inst) || c.Domain != domain || !now.Before(expires) {
		return ErrChallengeUnknown
	}

	message := []byte(challengeMessage(domain, nonce))
	valid, err := signing.VerifySignatureNS(message, key, pastedArmor(signature), RecoveryNamespace)
	if err != nil {
		// ⚠️ A malformed signature is reported as a bad signature rather than
		// as an internal error: the caller supplied it, and the distinction
		// would only tell them how their input was wrong.
		return fmt.Errorf("%w: %v", ErrBadSignature, err)
	}
	if !valid {
		return ErrBadSignature
	}
	// ⛔ Spent across every process sharing the store. A replay is reported
	// exactly as an unknown nonce, so a caller cannot tell which existed.
	if err := Consume(inst, c.ID, expires); err != nil {
		if errors.Is(err, ErrCeremony) {
			return ErrChallengeUnknown
		}
		return err
	}
	return nil
}

// pastedArmor undoes what copying a signature does to it: spaces at the
// start or end of its lines (a chat, an indented terminal block) and blank
// lines. ⭐ Armour carries no meaning in either, and the armour parser needs
// "-----BEGIN" at the start of a line, so an indented paste was refused as a
// bad signature.
//
// ⛔ Here only, for a signature a person pasted. Content verification shares
// the parser and keeps it strict.
func pastedArmor(signature string) string {
	var lines []string
	for _, line := range strings.Split(signature, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
