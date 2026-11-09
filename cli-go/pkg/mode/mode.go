// Package mode names the four ways a polis instance can be deployed.
//
// ⭐ THIS PACKAGE HOLDS THE VALUE AND NOTHING ELSE. It deliberately contains no
// capability table, no secret table and no policy: those are DERIVED from a
// mode by the packages that need them, and each derivation belongs with its
// consumer. The mode is the source; everything else is a projection of it.
//
// ⛔ THERE IS EXACTLY ONE MODE TYPE IN THE CODEBASE AND THIS IS IT. The reason
// it lives alone in a package this small is that two consumers need it and
// neither should have to import the other:
//
//   - the secrets seam asks "does this mode require that secret?"
//   - the capability contract maps a mode to the five published capabilities
//
// Before this existed the signal was scattered — an injected global on the web
// side, an unrelated isDev flag, and nothing at all on a remote instance, which
// therefore concluded it was running on a laptop. Any new "is this hosted?"
// boolean is that bug coming back; add a mode check instead.
package mode

import "fmt"

// Mode is how an instance is deployed. ⚠️ The four values are not a spectrum
// and are not ordered; do not compare them with < or >.
type Mode string

const (
	// Local is one person's site on their own machine, served by `polis serve`.
	// The owner's identity key is already on disk and nothing is projected.
	Local Mode = "local"

	// RemoteClientSigned is a hosted-anywhere instance where ONLY THE OWNER
	// signs. The key lives on the owner's machine and ⛔ the server can never
	// sign with it.
	//
	// ⚠️ NOT "self-signed", and the distinction is worth the words: to any
	// reader that names a self-signed CERTIFICATE — signed by its own subject
	// instead of an authority — which carries a connotation of untrusted and
	// unvalidated. This is the opposite. It is the flavour where the instance
	// never holds the key at all.
	RemoteClientSigned Mode = "remote-client-signed"

	// RemoteServerSigned is a hosted-anywhere instance that signs unattended,
	// so it MUST hold the identity key — "custodied-for-one".
	//
	// ⭐ WHAT SEPARATES THE TWO REMOTE FLAVOURS IS NOT OWNERSHIP BUT EXPOSURE.
	// Both keys are the owner's, in the sense that matters: no one else's
	// software signs with them. The difference is that this one can act while
	// the owner is asleep, and the client-signed one cannot act without them.
	//
	// ⭐⭐ It is the only deployment where an instance holds a private key that
	// represents a person, and it holds it WITHOUT a platform secret: the key
	// is generated into the instance's own private store on first start
	// (site.EnsureIdentityKey), so nothing projects it from the environment.
	RemoteServerSigned Mode = "remote-server-signed"

	// Custodied is a multi-tenant operator such as polis.pub: many tenants,
	// accounts and sessions in Postgres, operator-held actor keys.
	//
	// ⚠️ Nearly every secret in the codebase today is custodied-only. That is a
	// fact about where the product has been deployed so far, not a default for
	// other modes to inherit.
	Custodied Mode = "custodied"
)

// All lists every mode, in a stable order. ⭐ Tables that must cover every mode
// range over this, so adding a fifth mode fails those tests rather than
// silently acquiring whatever the zero value does.
func All() []Mode {
	return []Mode{Local, RemoteClientSigned, RemoteServerSigned, Custodied}
}

// Valid reports whether m is one of the four.
func Valid(m Mode) bool {
	for _, k := range All() {
		if m == k {
			return true
		}
	}
	return false
}

// Parse turns a configured string into a Mode.
//
// ⛔ IT REFUSES AN EMPTY STRING RATHER THAN DEFAULTING. A missing mode is a
// configuration error, and the intuitive default — "assume local, it is the
// safest" — is the bug this package was made to remove: a remote instance with
// no mode set concluded it was a laptop and switched off the capabilities it
// actually had.
func Parse(s string) (Mode, error) {
	if s == "" {
		return "", fmt.Errorf("no instance mode is configured: expected one of %v", All())
	}
	m := Mode(s)
	if !Valid(m) {
		return "", fmt.Errorf("unknown instance mode %q: expected one of %v", s, All())
	}
	return m, nil
}

// IsRemote reports whether m is either remote flavour.
//
// ⚠️ Use this only where the two flavours genuinely behave alike. The thing
// that most often differs is whether the instance can sign unattended, and for
// that the flavours must be distinguished — collapsing them there is how a
// server-signing instance ends up without the key it needs.
func IsRemote(m Mode) bool {
	return m == RemoteClientSigned || m == RemoteServerSigned
}
