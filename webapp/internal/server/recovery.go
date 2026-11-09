package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
)

// The recovery HTTP surface: two public routes and a cookie.
//
// ⛔⛔ BOTH ROUTES ARE PERMANENTLY PUBLIC, AND THAT IS NOT A WEAKNESS. You
// cannot authenticate to reach the thing that lets you authenticate — and a
// locked-out owner gets a 401 from everything else, because the instance cannot
// tell "a provider is enrolled" from "the owner can still use it".
//
// ⭐ They are safe because of what they accept: a challenge this instance just
// issued, and a signature over it made with a key only the owner holds. There
// is no secret here to guess, and nothing to read.
//
// ⚠️ THE RATE LIMIT IS THE ONE THING STANDING BETWEEN A PUBLIC ENDPOINT AND A
// FREE RESOURCE. Issue seals a challenge; verify does a signature check.
// Neither is expensive alone, and both are reachable by anyone. ⭐ Neither
// touches the store until a signature has verified: the challenge is a sealed
// token any process sharing the store can check, so a flood costs CPU, which
// the limit bounds, and never a store operation.

const (
	// RecoveryChallengePath mints a challenge.
	RecoveryChallengePath = "/api/recover/challenge"

	// RecoveryVerifyPath exchanges a signature for an enrolment session.
	RecoveryVerifyPath = "/api/recover/verify"

	// enrolmentCookie carries the enrolment session.
	//
	// ⛔ __Host- prefix: the browser then refuses it unless it is Secure,
	// path=/ and has NO Domain attribute — so a sibling subdomain cannot set
	// it. ⚠️ On plain-HTTP localhost browsers reject the prefix, which is why
	// it is only set when the request arrived over TLS.
	enrolmentCookie      = "__Host-polis-enrol"
	enrolmentCookiePlain = "polis-enrol"
)

// handleRecoveryChallenge issues a one-time challenge for the owner to sign.
func (s *Server) handleRecoveryChallenge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeRecoveryError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}
	if s.Recovery == nil || !s.Recovery.Configured() {
		// ⭐ A distinct answer, because the remedy differs from a failed proof:
		// this one needs POLIS_RECOVERY_KEY set and a redeploy.
		writeRecoveryError(w, http.StatusNotFound, "this instance has no recovery key configured",
			"set "+owner.RecoveryKeyEnv+" to your recovery public key and redeploy")
		return
	}
	if !s.recoveryLimiter().allow(s.clientKey(r)) {
		writeRecoveryError(w, http.StatusTooManyRequests, "too many recovery attempts", "wait a minute and try again")
		return
	}

	// ⭐ Issuing writes nothing: the challenge is sealed under the site's key.
	challenge, err := s.Recovery.Issue(s.instance())
	if err != nil {
		writeRecoveryError(w, http.StatusServiceUnavailable, err.Error(), "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(challenge)
}

// handleRecoveryVerify exchanges a signature for an enrolment session.
func (s *Server) handleRecoveryVerify(w http.ResponseWriter, r *http.Request) {
	if refuseCrossSite(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeRecoveryError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}
	if s.Recovery == nil || !s.Recovery.Configured() {
		writeRecoveryError(w, http.StatusNotFound, "this instance has no recovery key configured",
			"set "+owner.RecoveryKeyEnv+" to your recovery public key and redeploy")
		return
	}
	if !s.recoveryLimiter().allow(s.clientKey(r)) {
		writeRecoveryError(w, http.StatusTooManyRequests, "too many recovery attempts", "wait a minute and try again")
		return
	}

	var req struct {
		Nonce     string `json:"nonce"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeRecoveryError(w, http.StatusBadRequest, "invalid request", "")
		return
	}

	inst := s.instance()
	if err := s.Recovery.Verify(inst, req.Nonce, req.Signature); err != nil {
		// ⛔ ONE STATUS AND ONE MESSAGE FOR EVERY FAILURE. Separating "unknown
		// nonce" from "bad signature" would tell a caller which nonces
		// existed. ⚠️ The remedy text is identical in both cases anyway: get a
		// fresh challenge and sign it again.
		if errors.Is(err, owner.ErrNoRecoveryKey) {
			writeRecoveryError(w, http.StatusNotFound, "this instance has no recovery key configured", "")
			return
		}
		writeRecoveryError(w, http.StatusUnauthorized, "proof rejected",
			"request a fresh challenge and sign it with your recovery key")
		return
	}

	token, expires, err := owner.OpenEnrolment(inst)
	if err != nil {
		writeRecoveryError(w, http.StatusServiceUnavailable, "could not open an enrolment session", "")
		return
	}
	setOwnerCookie(w, r, enrolmentCookie, enrolmentCookiePlain, token, expires)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	// ⚠️ The token is NOT echoed in the body. A cookie the browser handles
	// cannot be pasted into a chat window by a confused owner.
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"enrolment_until": expires,
		"next":            "enrol a sign-in provider",
	})
}

// enrolmentToken reads the session token from the request, if present.
func enrolmentToken(r *http.Request) string {
	for _, name := range cookieNames(r, enrolmentCookie, enrolmentCookiePlain) {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// writeRecoveryError answers with a stable shape.
//
// ⭐ Every refusal carries a REMEDY where one exists. An owner meeting this
// endpoint is, by definition, locked out and anxious; "unauthorized" alone
// tells them nothing they can act on.
func writeRecoveryError(w http.ResponseWriter, status int, message, remedy string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	body := map[string]string{"error": message}
	if remedy != "" {
		body["remedy"] = remedy
	}
	_ = json.NewEncoder(w).Encode(body)
}

// clientKey identifies a caller for rate limiting.
//
// ⚠️ IT IS FOR COUNTING, NOT FOR TRUST. A forwarded-for header is attacker
// controlled, so this must never gate a decision — only a quota. ⭐ Using it
// anyway is right: behind a proxy every request shares one RemoteAddr, and
// limiting on that would let one visitor lock out everyone else.
func (s *Server) clientKey(r *http.Request) string {
	// ⛔⛔ THE CONNECTION'S ADDRESS, or the proxy-appended one only behind a
	// trusted proxy — the same rule the public-content limiter uses. This used
	// to trust the LEFTMOST X-Forwarded-For unconditionally, so one socket with
	// a fresh value per request walked past the limit, filled the outstanding
	// challenge pool, and locked the real owner out with 503s.
	return remoteIP(r, s.Proxy)
}

// recoveryLimiter returns the recovery routes' limiter.
func (s *Server) recoveryLimiter() *attemptLimiter {
	s.ownerAuth()
	return s.recoveryLimit
}

// signInLimiter returns the sign-in routes' limiter. ⭐ Separate from
// recovery's, so a flood at one door does not close the other.
func (s *Server) signInLimiter() *attemptLimiter {
	s.ownerAuth()
	return s.signInLimit
}

// ownerCache is this server's cache of owner-auth store reads.
func (s *Server) ownerCache() *owner.Cache {
	s.ownerAuth()
	return s.ownerCacheV
}

// ownerAuth builds the owner-auth cache and limiters on first use, so a
// zero-valued Server works in tests.
func (s *Server) ownerAuth() {
	s.ownerAuthOnce.Do(func() {
		s.ownerCacheV = owner.NewCache()
		s.signInLimit = newAttemptLimiter()
		s.recoveryLimit = newAttemptLimiter()
	})
}

// prepareOwnerAuth loads the session key into memory when the site starts, so
// a sign-in or recovery request that has proved nothing never has to touch the
// store. ⭐ Only on a remote instance: a laptop trusts loopback and a custodied
// tenant its operator, and neither needs a session key on disk. Anywhere else
// the key is still created the first time it is needed.
func (s *Server) prepareOwnerAuth() {
	if m := s.effectiveMode(); m != mode.RemoteServerSigned && m != mode.RemoteClientSigned {
		return
	}
	if err := owner.PrepareSessions(s.instance()); err != nil {
		// ⚠️ Not fatal: the first sign-in tries again. Named, never the bytes.
		log.Printf("[warning] owner sessions not prepared: %v", err)
	}
}
