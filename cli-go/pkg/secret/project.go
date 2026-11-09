package secret

import (
	"errors"
	"fmt"
	"io/fs"
)

// Projecting a secret that carries identity.
//
// ⭐⭐ WHY THIS IS IN THE SEAM AND NOT ONLY IN pkg/actor. The discipline below
// was written once, for the operator's actor keys, and it is correct, and a
// second projected secret must reuse it rather than reimplement it — two copies
// of reasoning that took a live incident to get right is how they drift.
// ⚠️ The remote identity key was expected to be that second one and is not: it
// is generated into the instance's own private store (site.EnsureIdentityKey),
// never written from a platform secret.
//
// ⛔ THE DEPENDENCIES ARE INJECTED, NOT IMPORTED. Real key parsing lives in
// pkg/signing and reading what a site publishes lives in pkg/site, and this
// package must not pull either in: it is imported by the boot path of every
// binary, and a seam that drags half the tree behind it stops being one.
// Callers supply the three functions; pkg/actor is the reference caller.

// Outcome says what a projection did.
type Outcome string

const (
	// Present means a key file was already there and nothing was written.
	Present Outcome = "present"

	// Written means the secret was projected onto disk.
	Written Outcome = "written"
)

// Projection is one projected secret and the operations it needs.
type Projection struct {
	// Value is the secret, from Resolve or from the environment.
	Value Value

	// KeyPath is where the private key belongs.
	KeyPath string

	// Absent reports whether KeyPath is genuinely not there.
	//
	// ⛔ IT MUST DISTINGUISH "NOT THERE" FROM "I COULD NOT LOOK". A permission
	// error read as absence makes this overwrite a key it could not see, which
	// is the one outcome the whole function exists to prevent.
	Absent func(path string) (bool, error)

	// ValidateKey parses the secret as a private key, or errors.
	ValidateKey func(key []byte) error

	// PublicKeyFor derives the public half, for the stale check.
	PublicKeyFor func(key []byte) ([]byte, error)

	// PublishedKey returns the public key the site publishes, for the stale
	// check. ⭐ Returning an empty string with no error means "this site
	// publishes nothing yet", which is a normal first-provisioning state and
	// not a failure.
	PublishedKey func() (string, error)

	// SameKey compares two public keys. ⚠️ It must ignore the trailing
	// comment, which is free-form and differs between tools.
	SameKey func(a, b string) bool

	// Write persists the private key and its public half.
	//
	// ⛔ THE PRIVATE KEY'S MODE IS LOAD-BEARING: 0600 under a 0700 directory.
	// Patrol checks key-file permissions every sweep, so a projection writing
	// 0644 turns the fleet red — and would be right to.
	Write func(priv, pub []byte) error
}

// Project writes a projected secret onto disk, if and only if it is absent and
// matches what the site publishes.
//
// ⛔ THE FOUR RULES, each of which has a reason that cost something to learn:
//
//  1. NEVER OVERWRITE. Rotation signs the handover with the OLD key before
//     destroying it, and a key-history chain is append-only and never rebuilt.
//     A projection that overwrites destroys the only artifact that could prove
//     the handover happened.
//  2. NEVER GENERATE. An absent secret is a configuration error. Generating a
//     keypair silently replaces an identity, and everything published under
//     the old one stops verifying against what the site serves.
//  3. ⚠️ CHECK FOR STALENESS HERE, because nothing downstream can. A secret not
//     updated after a rotation holds a RETIRED key, and the key-history chain
//     does NOT catch it: the chain-head check compares two fields inside the
//     same published document, neither of which is the key file, so both stay
//     correct while the projected private key is wrong. What would eventually
//     catch it is a signature failing to verify — i.e. after the instance has
//     already published under a key nobody accepts.
//  4. ABSENT OR STALE IS FATAL. Returning an error rather than logging is
//     deliberate: not booting is recoverable, publishing under the wrong key
//     is not.
func Project(p Projection) (Outcome, error) {
	if p.KeyPath == "" {
		return "", errors.New("projection has no key path")
	}
	for name, fn := range map[string]bool{
		"Absent":       p.Absent == nil,
		"ValidateKey":  p.ValidateKey == nil,
		"PublicKeyFor": p.PublicKeyFor == nil,
		"SameKey":      p.SameKey == nil,
		"Write":        p.Write == nil,
	} {
		if fn {
			// ⛔ A missing dependency must not degrade to skipping a check. A
			// nil SameKey silently disabling the stale check is exactly the
			// class of failure this function guards against.
			return "", fmt.Errorf("projection of %s is missing its %s function", p.KeyPath, name)
		}
	}

	absent, err := p.Absent(p.KeyPath)
	if err != nil {
		return "", fmt.Errorf("cannot tell whether %s exists, so refusing to project over it: %w", p.KeyPath, err)
	}
	if !absent {
		// Rule 1.
		return Present, nil
	}

	raw := p.Value.Expose()
	if raw == "" {
		// Rule 2.
		return "", fmt.Errorf("the secret for %s is absent or empty and the key file does not exist: "+
			"refusing to generate a new identity — restore the secret", p.KeyPath)
	}

	priv := []byte(raw)
	if raw[len(raw)-1] != '\n' {
		// ⚠️ A secret store round-trips values without a trailing newline more
		// often than not, and the OpenSSH PEM parser wants one.
		priv = []byte(raw + "\n")
	}
	if err := p.ValidateKey(priv); err != nil {
		// ⛔ Does not quote the value: it is a private key.
		return "", fmt.Errorf("the secret for %s is not a valid private key: %w", p.KeyPath, err)
	}

	pub, err := p.PublicKeyFor(priv)
	if err != nil {
		return "", fmt.Errorf("derive public key for %s: %w", p.KeyPath, err)
	}

	// Rule 3.
	if p.PublishedKey != nil {
		published, err := p.PublishedKey()
		// ⭐ An error or an empty value means "nothing to compare against",
		// which is the normal first-provisioning state. The check tightens as
		// soon as the site publishes something, and never blocks a new site.
		if err == nil && published != "" && !p.SameKey(published, string(pub)) {
			return "", fmt.Errorf("the secret for %s does not match the key this site publishes: "+
				"the secret is stale (a rotation was recorded and the secret was not updated) "+
				"or it belongs to another site. Refusing to project a retired identity", p.KeyPath)
		}
	}

	if err := p.Write(priv, pub); err != nil {
		return "", fmt.Errorf("write projected key %s: %w", p.KeyPath, err)
	}
	return Written, nil
}

// AbsentFromStat adapts a stat-style lookup to Projection.Absent.
//
// ⚠️ IT TREATS ONLY fs.ErrNotExist AS ABSENCE. Every other error — a
// permission problem, a broken mount — is returned, because a caller that read
// those as "not there" would overwrite a key it could not see.
func AbsentFromStat(statErr error) (bool, error) {
	if statErr == nil {
		return false, nil
	}
	if errors.Is(statErr, fs.ErrNotExist) {
		return true, nil
	}
	return false, statErr
}
