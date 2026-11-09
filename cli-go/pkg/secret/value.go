// Package secret is the one seam every secret a polis instance holds is read
// through: what it is called, which modes require it, what shape it must have,
// and what may never happen to it.
//
// ⛔ IT IS NOT A SECRET MANAGER AND HOLDS NO STORE. The platform keeps the
// values (Fly secrets on the custodied deployment). A remote instance has no
// platform secret at all: its identity key lives in its own private store.
// This package governs the code reaching for them.
//
// ⭐ TWO KINDS, AND TREATING THEM ALIKE IS THE TRAP:
//
//   - a REFERENCE secret is used in place and never written down. An API key, a
//     database URL. Rotating it means changing one value; the worst case of
//     getting it wrong is a failed call.
//   - a PROJECTED secret is written to disk and then used from there, and it
//     CARRIES IDENTITY. ⭐ That is the whole difference. A projected secret IS
//     the site: rotation signs the handover with the old key BEFORE destroying
//     it, and a key-history chain is append-only and never rebuilt, so a
//     projection that overwrites destroys the only artifact that could prove
//     the handover happened.
//
// The projected discipline is a strict superset of the reference one. ⛔ Do not
// "simplify" by applying the superset everywhere: never-overwrite and the
// stale check are meaningless for an API key, where there is nothing on disk to
// overwrite and no published counterpart to compare against. Demanding them
// yields either dead code or a rule bent until it no longer says anything.
package secret

import (
	"errors"
	"fmt"
	"io"
)

// redacted is what every formatting verb renders. ⚠️ It is deliberately not the
// empty string: a log line reading `key=` looks like a bug in the logger, while
// this one says a value was present and withheld on purpose.
const redacted = "[redacted]"

// Value holds a secret and refuses to render it.
//
// ⭐⭐ THIS TYPE EXISTS BECAUSE "NEVER LOGGED" WAS THE ONE PROPERTY WITH NO
// IMPLEMENTATION. Nothing in this codebase redacted anything; the only reason
// no secret had leaked was that nobody had yet written the line that would leak
// one. A careless `log.Printf("%v", v)` or a value dropped into an event
// payload now prints the constant above, so the mistake is not available.
//
// ⛔ THE FIELD IS UNEXPORTED AND THE ONLY WAY OUT IS Expose(). That is not
// ceremony: it makes every point where a secret becomes an ordinary string
// greppable, reviewable, and countable by a test.
//
// ⚠️ WHAT IT CANNOT DO. It cannot stop Expose() being called and the result
// logged, and it cannot survive a reflective walk that reads unexported fields
// (encoding/gob, some deep-copy helpers, a debugger). It removes the easy
// mistake, not every mistake.
type Value struct {
	// ⛔ INSIDE A CLOSURE, deliberately. fmt cannot call methods on a value
	// held in an UNEXPORTED struct field, so it printed a plain string field's
	// contents — {name:n dsn:{v:hunter2}} — whatever String said. A pointer was
	// not enough either: a mismatched verb's "%!s(*box=&{…})" form dereferences
	// it. A function has no contents to print; every verb shows an address.
	get func() string
}

// New wraps a raw value. ⭐ Callers should rarely need this — Resolve returns
// Values already — but tests and adapters do.
func New(v string) Value { return Value{get: func() string { return v }} }

// Expose returns the underlying secret.
//
// ⛔ CALL IT AT THE POINT OF USE AND DO NOT STORE THE RESULT. Every call site is
// a place a secret becomes a loggable string, and `TestSecretExposureIsCounted`
// fails the build when a new one appears without being added to the census, so
// that each is a decision somebody made rather than one that accumulated.
func (s Value) Expose() string {
	if s.get == nil {
		return ""
	}
	return s.get()
}

// IsZero reports whether the value is empty. ⭐ Useful for "was this supplied?"
// without exposing anything.
func (s Value) IsZero() bool { return s.Expose() == "" }

// String implements fmt.Stringer and redacts. Covers %v, %s and %q.
func (s Value) String() string { return redacted }

// Format redacts under EVERY verb. ⛔ String and GoString cover only the verbs
// that call them; %d, %t, %e, %c, %U, %b, %o and %p printed the raw value.
func (s Value) Format(f fmt.State, verb rune) { _, _ = io.WriteString(f, redacted) }

// GoString implements fmt.GoStringer and redacts. Covers %#v, which otherwise
// prints the struct and its unexported field.
func (s Value) GoString() string { return redacted }

// MarshalJSON redacts, so a Value inside a logged event or an API response
// cannot carry the secret.
//
// ⛔ It never errors: a marshaller that fails on a secret turns a careless log
// line into a broken response, which invites someone to "fix" it by reaching
// for Expose().
func (s Value) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// MarshalText redacts. ⚠️ Needed as well as MarshalJSON: encoding/json prefers
// MarshalJSON, but several loggers and url.Values go through TextMarshaler.
func (s Value) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// UnmarshalJSON always fails.
//
// ⛔ A secret ARRIVES FROM THE PLATFORM, never from a document we parsed. If a
// Value could be unmarshalled, a request body or a config file could set one,
// which is an injection point with no legitimate use.
func (s *Value) UnmarshalJSON([]byte) error {
	return errors.New("secret.Value cannot be unmarshalled: secrets come from the platform, not from parsed documents")
}
