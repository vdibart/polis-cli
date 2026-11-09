package storage

import (
	"fmt"
	"strings"
)

// The environment that selects a site's storage backend. ⭐ PROVIDER-NEUTRAL
// NAMES: a backend is a kind plus the SOURCE, the private store that holds the
// whole site and is the truth. Which provider's identifier that is is the
// kind's business.
const (
	EnvStorageKind   = "POLIS_STORAGE"
	EnvStorageSource = "POLIS_STORAGE_SOURCE"

	// EnvStorageServed named a second, PUBLIC store that a site's public
	// files were copied into and served from. ⛔ Removed: on Vercel the
	// public surface is the site's deployment, built from the source. It is
	// still read, only to refuse a boot that sets it, so a leftover setting
	// is named rather than silently ignored.
	EnvStorageServed = "POLIS_STORAGE_SERVED"

	// What a request-delivered credential must say to be accepted: the OIDC
	// issuer, an audience it must name, and the exact subject (which names
	// the deployment). ⛔ Required for a backend that takes its credential
	// from requests: an unverified one could be forged by any caller.
	EnvStorageTokenIssuer   = "POLIS_STORAGE_TOKEN_ISSUER"
	EnvStorageTokenAudience = "POLIS_STORAGE_TOKEN_AUDIENCE"
	EnvStorageTokenSubject  = "POLIS_STORAGE_TOKEN_SUBJECT"
)

// Backend kinds.
const (
	BackendPOSIX      = "posix"
	BackendVercelBlob = "vercel-blob"
)

// BackendSpec says which backend a site uses.
type BackendSpec struct {
	Kind   string
	Source string

	// TokenIssuer, TokenAudience and TokenSubject are what a
	// request-delivered credential must carry (see EnvStorageTokenIssuer).
	TokenIssuer, TokenAudience, TokenSubject string
}

// BackendFromEnv reads the spec.
//
// ⭐ AN UNSET KIND IS POSIX, deliberately, and it is the one default here:
// every existing deployment — a laptop, polis.pub — stores on a filesystem
// and sets nothing. ⛔ ANYTHING ELSE MUST BE COMPLETE. A partial spec refuses
// rather than falling back to a filesystem, because on a platform with a
// read-only or ephemeral disk that fallback is silent data loss.
func BackendFromEnv(getenv func(string) string) (BackendSpec, error) {
	spec := BackendSpec{
		Kind:   strings.TrimSpace(getenv(EnvStorageKind)),
		Source: strings.TrimSpace(getenv(EnvStorageSource)),

		TokenIssuer:   strings.TrimSpace(getenv(EnvStorageTokenIssuer)),
		TokenAudience: strings.TrimSpace(getenv(EnvStorageTokenAudience)),
		TokenSubject:  strings.TrimSpace(getenv(EnvStorageTokenSubject)),
	}
	if strings.TrimSpace(getenv(EnvStorageServed)) != "" {
		return spec, fmt.Errorf("storage: %s is no longer read: a site's public files are served from its deployment, "+
			"built from %s; unset it", EnvStorageServed, EnvStorageSource)
	}
	switch spec.Kind {
	case "", BackendPOSIX:
		if spec.Source != "" {
			return spec, fmt.Errorf("storage: %s is set but %s is not — say which backend it belongs to",
				EnvStorageSource, EnvStorageKind)
		}
		spec.Kind = BackendPOSIX
		return spec, nil
	case BackendVercelBlob:
		var missing []string
		if spec.Source == "" {
			missing = append(missing, EnvStorageSource)
		}
		for _, f := range []struct{ env, v string }{
			{EnvStorageTokenIssuer, spec.TokenIssuer},
			{EnvStorageTokenAudience, spec.TokenAudience},
			{EnvStorageTokenSubject, spec.TokenSubject},
		} {
			if f.v == "" {
				missing = append(missing, f.env)
			}
		}
		if len(missing) > 0 {
			return spec, fmt.Errorf("storage: %s=%s needs %s", EnvStorageKind, spec.Kind, strings.Join(missing, " and "))
		}
		// ⛔ Refused at boot, not at the first token: the issuer's keys are
		// fetched from this URL, and over plain http anyone on the path could
		// supply them. Named by variable; the value is configuration, not a
		// secret, but the rule is to name the problem, never echo the input.
		if !isHTTPSURL(spec.TokenIssuer) {
			return spec, fmt.Errorf("storage: %s must be an absolute https URL", EnvStorageTokenIssuer)
		}
		return spec, nil
	default:
		return spec, fmt.Errorf("storage: unknown %s %q", EnvStorageKind, spec.Kind)
	}
}

// Backend is an opened site backend.
type Backend struct {
	// Store is the site's store.
	Store Store

	// FromRequest is how a request delivers the credential this backend
	// needs, or nil when it needs none. ⭐ The caller hands it ALL the values of
	// a request header on every request; WHICH header is the backend's
	// knowledge. ⛔ An error means the request must be refused: more than one
	// value of the credential header, or a credential that did not verify.
	FromRequest func(values func(string) []string) error

	// FromEnvironment reads a credential the platform put in the environment,
	// for work outside a request (a laptop after `vercel env pull`), or is nil.
	// ⛔ SEPARATE FROM FromRequest ON PURPOSE: a request's headers are chosen
	// by whoever sent it, so a request must never be read with the
	// environment's names. ⭐ An error says why an offered credential was
	// refused; none is returned when the environment offers nothing.
	FromEnvironment func(getenv func(string) string) error

	// EnvironmentCredentialErr is why OpenBackend's FromEnvironment refused the
	// credential the environment offered, or nil. ⛔ NEVER FATAL, AND NEVER
	// SILENT: the backend still opens (a request can deliver a credential
	// later), and the caller logs this by cause. A laptop with a wrong subject
	// used to get "no credential yet" with nothing to say why.
	EnvironmentCredentialErr error

	// StartsOnRequest reports that the store is unusable until a request has
	// delivered a credential, so site work must wait for the first request.
	StartsOnRequest bool
}

// OpenBackend opens the backend a spec names, for the site rooted at dir.
//
// ⭐ getenv is how a backend finds a credential the platform puts in the
// environment for work outside a request (a laptop after `vercel env pull`).
// It is passed in, never read here, so the caller decides what this sees.
func OpenBackend(spec BackendSpec, dir string, getenv func(string) string) (Backend, error) {
	switch spec.Kind {
	case "", BackendPOSIX:
		return Backend{Store: NewSite(dir)}, nil
	case BackendVercelBlob:
		b, err := openVercelBlobBackend(spec, VercelBlobConfig{})
		if err != nil {
			return b, err
		}
		b.offerEnvironment(getenv)
		return b, nil
	default:
		return Backend{}, fmt.Errorf("storage: unknown backend %q", spec.Kind)
	}
}

// offerEnvironment hands the backend the credential the environment holds, if
// it reads one, and records why it was refused.
func (b *Backend) offerEnvironment(getenv func(string) string) {
	if getenv == nil || b.FromEnvironment == nil {
		return
	}
	b.EnvironmentCredentialErr = b.FromEnvironment(getenv)
}

// normaliseStoreID is a store id as the Blob client addresses it: no leading
// "store_", and case-insensitive.
func normaliseStoreID(id string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(id), "store_"))
}

// StoreHostLabel returns the label a store id contributes to a Blob hostname
// (normalised, lowercase), or false when id is not a store id. ⛔ Anything that
// builds a Blob URL from a configured id goes through this or isStoreID.
func StoreHostLabel(id string) (string, bool) {
	if !isStoreID(id) {
		return "", false
	}
	return normaliseStoreID(id), true
}

// isStoreID reports whether id, with any leading "store_", is a store id in
// the normalised form once lowercased: letters and digits only, nothing to
// trim.
//
// ⛔ THE ID BECOMES A HOSTNAME. The read host is built from it, so an id
// carrying a dot, a slash, an "@" or a "#" would send private reads, and the
// bearer credential with them, to a host of the id's choosing.
func isStoreID(id string) bool {
	n := normaliseStoreID(id)
	if n == "" || n != strings.ToLower(strings.TrimPrefix(id, "store_")) {
		return false
	}
	for _, c := range n {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}
