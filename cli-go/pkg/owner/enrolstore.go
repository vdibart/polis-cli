package owner

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Where a provider keeps what it needs to recognise the owner later.
//
// ⛔⛔ THE STATE HANGS OFF THE INSTANCE, NOT OFF THE PROVIDER, AND THAT IS
// FORCED BY THE HOSTED PROCESS. The registry is process-global and compile-time
// — which is what keeps `Available()` honest — but polis-hosted runs MANY
// TENANTS IN ONE PROCESS. A provider holding a store in its own struct would
// therefore share one site's enrolment with every other tenant: the worst
// possible bug in an auth system, and one that would only appear in production.
//
// ⭐ So providers are STATELESS, and every per-site fact arrives through the
// Instance they are handed. A provider that wants to remember something writes
// it here, keyed by its own name.
//
// ⭐⭐ THE BLOB IS OPAQUE, WHICH IS D7's REQUIREMENT AND NOT A SHRUG. A passkey
// credential, a password hash and an OAuth subject are all "the thing that
// provider needs to recognise the owner later". Typing this record would mean
// the seam knowing what a passkey is, and the next provider would need the seam
// to change — which is exactly what must not happen, because two providers are
// already deferred.

// enrolmentPrefix is where enrolment records live inside a site.
//
// ⚠️ Under .polis, so a site store created with storage.NewSite keeps it out of
// a world-readable directory. ⛔ It must never be served: these records are
// not secrets in the sense a private key is, but a credential ID is a
// fingerprint and publishing one tells a stranger which authenticators the
// owner carries.
const enrolmentPrefix = ".polis/owner/enrolments"

// ErrNotEnrolled means the provider has no record for this site.
var ErrNotEnrolled = errors.New("owner: provider is not enrolled on this site")

// enrolmentKey builds the storage key for one provider's record.
//
// ⛔ The provider name is validated rather than trusted. It reaches this
// function from a URL path on the enrol routes, and a name containing a slash
// or "        .." would address a file outside the prefix — reading or writing
// arbitrary site content through an endpoint whose whole purpose is to be
// reachable before you are authenticated.
func enrolmentKey(provider string) (string, error) {
	if provider == "" {
		return "", errors.New("owner: empty provider name")
	}
	if provider != path.Clean(provider) ||
		strings.ContainsAny(provider, `/\`) ||
		strings.Contains(provider, "..") ||
		strings.HasPrefix(provider, ".") {
		return "", fmt.Errorf("owner: %q is not a usable provider name", provider)
	}
	for _, r := range provider {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			// ⭐ Deliberately narrow: lower-case, digits and hyphen. Provider
			// names are chosen by us at compile time, so there is no cost to
			// refusing everything else, and it closes the category rather than
			// the instance.
			return "", fmt.Errorf("owner: %q is not a usable provider name", provider)
		}
	}
	return enrolmentPrefix + "/" + provider + ".json", nil
}

// LoadEnrolment returns a provider's record for this site.
//
// ⭐ Served from the instance's cache when it has one, for up to cacheTTL: the
// gate and the sign-in begin read this for every request, including requests
// that have proved nothing, and those must not each cost a store operation.
// ⚠️ A record saved by ANOTHER process is seen here within cacheTTL. A caller
// about to act on a verified proof reads FRESH (LoadEnrolmentFresh).
func LoadEnrolment(inst Instance, provider string) ([]byte, error) {
	return loadEnrolment(inst, provider, false)
}

// LoadEnrolmentFresh reads a provider's record from the store, bypassing the
// cache. ⭐ For the moment after a proof verified: a credential removed on
// another process a few seconds ago must not open a session here.
func LoadEnrolmentFresh(inst Instance, provider string) ([]byte, error) {
	return loadEnrolment(inst, provider, true)
}

func loadEnrolment(inst Instance, provider string, fresh bool) ([]byte, error) {
	if inst.Store == nil {
		return nil, ErrNotEnrolled
	}
	key, err := enrolmentKey(provider)
	if err != nil {
		return nil, err
	}
	ck := siteKey(inst) + "\x00" + provider
	c := inst.Cache
	if c != nil && !fresh {
		c.mu.Lock()
		hit, ok := c.enrol[ck]
		ttl, now := c.ttl, c.now()
		c.mu.Unlock()
		if ok && now.Sub(hit.at) < ttl {
			return hit.blob, hit.err
		}
	}
	blob, err := inst.Store.Get(context.Background(), key)
	if errors.Is(err, storage.ErrNotFound) {
		blob, err = nil, ErrNotEnrolled
	} else if err != nil {
		// ⛔ A read failure is NOT "not enrolled". Reporting it as absence
		// would make an unreadable disk look like a site with no providers,
		// which the seam answers by refusing everything — correct — but with a
		// message telling the owner to enrol, which would be a lie.
		// ⚠️ And it is not cached: the next request asks again.
		return nil, fmt.Errorf("reading %s enrolment: %w", provider, err)
	}
	if c != nil {
		c.mu.Lock()
		c.enrol[ck] = cachedBlob{blob: blob, err: err, at: c.now()}
		c.mu.Unlock()
	}
	return blob, err
}

func cacheEnrolment(inst Instance, provider string, blob []byte, err error) {
	if c := inst.Cache; c != nil {
		c.mu.Lock()
		c.enrol[siteKey(inst)+"\x00"+provider] = cachedBlob{blob: blob, err: err, at: c.now()}
		c.mu.Unlock()
	}
}

// SaveEnrolment writes a provider's record.
func SaveEnrolment(inst Instance, provider string, blob []byte) error {
	if inst.Store == nil {
		return errors.New("owner: this instance has no store, so enrolment cannot be saved")
	}
	key, err := enrolmentKey(provider)
	if err != nil {
		return err
	}
	// ⛔ 0600. A credential ID is a fingerprint of what the owner carries, and
	// on a shared host a world-readable one is an inventory.
	if err := inst.Store.Put(context.Background(), key, blob, storage.WithMode(0o600)); err != nil {
		return err
	}
	cacheEnrolment(inst, provider, blob, nil)
	return nil
}

// DeleteEnrolment removes a provider's record.
//
// ⭐ Absent is SUCCESS, not an error: removal is idempotent, and a UI that
// double-submits must not show a failure for reaching the state the owner
// asked for.
func DeleteEnrolment(inst Instance, provider string) error {
	if inst.Store == nil {
		return nil
	}
	key, err := enrolmentKey(provider)
	if err != nil {
		return err
	}
	if err := inst.Store.Delete(context.Background(), key); err != nil &&
		!errors.Is(err, storage.ErrNotFound) {
		return fmt.Errorf("removing %s enrolment: %w", provider, err)
	}
	cacheEnrolment(inst, provider, nil, ErrNotEnrolled)
	// ⛔ A removed way to sign in ends every session it opened. Before, sessions
	// opened with it stayed live, and re-enrolling a new authenticator revived
	// a stolen one. ⭐ A generation bump, so every process honours it: the
	// counter outlives the record, and a later enrolment starts above it.
	return EndProviderSessions(inst, provider)
}

// IsEnrolledIn reports whether a provider has a record for this site.
//
// ⭐ The helper a stateful provider's Enrolled() calls, so none of them
// reimplement the lookup. ⚠️ A read error reads as NOT enrolled here, which is
// the safe direction: it withholds a provider rather than offering one that
// cannot work.
func IsEnrolledIn(inst Instance, provider string) bool {
	blob, err := LoadEnrolment(inst, provider)
	return err == nil && len(blob) > 0
}

// Enroller is implemented by a provider the owner has to set up.
//
// ⭐⭐ IT IS A SEPARATE, OPTIONAL INTERFACE ON PURPOSE. Loopback and operator
// are real providers with nothing to enrol — their premise IS the deployment —
// so forcing enrolment methods onto every Provider would mean two stubs that
// exist only to return "not applicable", and a reader would reasonably wonder
// what they do.
//
// ⛔ TWO STEPS, NOT ONE, AND NOT THREE. "Begin, then complete" is the shape a
// passkey's challenge/response, a password compare and an OAuth round trip all
// share. A provider needing more state should carry it in its own payload
// rather than adding a step here — the moment the seam grows a third verb for
// one provider, the next provider needs a fourth.
//
// ⚠️ The payloads are OPAQUE BYTES in both directions. The seam marshals
// nothing and inspects nothing: a provider that needed the seam to understand
// its wire format would be the seam knowing what a passkey is.
//
// ⭐⭐ AND SO IS THE STATE BETWEEN THE STEPS. A provider keeps NOTHING between
// begin and complete: on a serverless platform the two land on different
// processes. BeginEnrol returns its state as opaque bytes, the seam seals them
// into a short-lived cookie bound to this provider's completion, and hands them
// back to CompleteEnrol. ⛔ CompleteEnrol VERIFIES and returns the record to
// keep; the seam spends the ceremony and only then saves the record, so a
// replay racing the first completion loses before anything is written.
type Enroller interface {
	Provider

	// BeginEnrol starts a ceremony and returns what the client needs next,
	// plus the provider's own state for the completion.
	BeginEnrol(inst Instance, request []byte) (out any, state []byte, err error)

	// CompleteEnrol checks the client's answer against state and returns what
	// the client is told and the record to keep (an opaque blob, saved under
	// this provider's name by the seam).
	//
	// ⛔ It must NOT save the record itself: the seam saves it after spending
	// the ceremony, which is what makes completion single-use across processes.
	CompleteEnrol(inst Instance, request, state []byte) (out any, record []byte, err error)
}
