package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrNoCredential is returned when a backend needs a credential and has none
// yet — typically before the first request has delivered one.
var ErrNoCredential = errors.New("storage: no credential available yet")

// CredentialSource supplies the bearer credential a backend presents.
//
// ⭐ PROVIDER-NEUTRAL. A backend asks for "the credential now" and does not
// know whether it is a fixed token or one the platform hands over with each
// request. ⛔ Implementations never log or wrap the value into an error.
type CredentialSource interface {
	Credential(ctx context.Context) (string, error)
}

// StaticCredential is a credential that does not change.
type StaticCredential string

func (c StaticCredential) Credential(context.Context) (string, error) {
	if c == "" {
		return "", ErrNoCredential
	}
	return string(c), nil
}

// RequestCredential is a credential the PLATFORM delivers with requests: one
// process-wide cell, refreshed from every request that carries one, valid
// until its own expiry.
//
// ⛔⛔ ONE CELL FOR THE WHOLE PROCESS IS CORRECT ONLY BECAUSE THE CREDENTIAL
// NAMES THE DEPLOYMENT, NOT THE CALLER. Every request to an instance carries
// an equivalent credential, so whichever one arrived last is as good as any.
// ⚠️ A credential that identified the VISITOR must never use this type: it
// would let one visitor's credential act for the next.
//
// ⭐ It avoids threading a request context through every store call (212 on
// the serving path use a background context), and it lets work that is not
// itself a request use the credential the last request delivered.
type RequestCredential struct {
	mu  sync.Mutex
	tok string
	exp time.Time

	// Verify, when set, must accept a token before the cell will hold it.
	// ⛔ A deployed backend always sets it: without it a forged header on one
	// request would replace the token every request's store calls present.
	Verify func(ctx context.Context, tok string) error

	// now is time.Now, replaceable in tests.
	now func() time.Time
}

// expiryMargin is how long before its stated expiry a credential stops being
// handed out, so a call started on it does not land after it lapses.
const expiryMargin = 2 * time.Minute

// Offer records a credential delivered with a request. ⭐ THE LATEST OFFER
// WINS, and an empty one is ignored.
//
// ⛔⛔ NOT "WHICHEVER EXPIRES LATER". The expiry is read WITHOUT verification,
// so preferring the later one would let a single forged token with a
// far-future expiry displace every genuine one and stick until restart — a
// one-request denial of service on all storage. Latest-wins means a forged
// offer is replaced by the next genuine request.
func (c *RequestCredential) Offer(tok string) error {
	tok = strings.TrimSpace(tok)
	if tok == "" {
		return nil
	}
	c.mu.Lock()
	same := tok == c.tok
	c.mu.Unlock()
	if same {
		return nil // already verified when it was first accepted
	}
	if c.Verify != nil {
		if err := c.Verify(context.Background(), tok); err != nil {
			// ⭐ Refused or unchecked, the held token is untouched. ⛔ The error
			// passes through unchanged, so a caller can tell a verifier that
			// was unavailable (ErrVerifierUnavailable) from a bad token.
			return err
		}
	}
	exp := jwtExpiry(tok)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tok, c.exp = tok, exp
	return nil
}

// Credential returns the held credential while it is valid.
func (c *RequestCredential) Credential(context.Context) (string, error) {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok == "" {
		return "", ErrNoCredential
	}
	// ⚠️ An expiry we could not read is not treated as "never": the platform
	// will refuse the call, and that refusal is the honest signal.
	if !c.exp.IsZero() && !now().Before(c.exp.Add(-expiryMargin)) {
		return "", ErrNoCredential
	}
	return c.tok, nil
}

// Held reports whether a credential has ever been offered.
func (c *RequestCredential) Held() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tok != ""
}

// jwtExpiry reads the `exp` claim of a JWT WITHOUT verifying it. ⭐ That is
// safe here and only here: this process is not trusting the token, it is
// forwarding it to the party that issued it, which verifies it. The expiry is
// read only to stop handing out a token that will be refused.
func jwtExpiry(tok string) time.Time {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}
