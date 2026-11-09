package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
)

// The sign-in surface.
//
// ⛔ BOTH CEREMONY ROUTES ARE PUBLIC, because somebody who is not signed in is
// exactly who needs them. ⭐ That is safe for the same reason the recovery door
// is: they accept only an assertion over a challenge THIS INSTANCE just issued,
// and a caller with no enrolled credential gets a refusal rather than a usable
// ceremony.
//
// ⚠️ SIGN-OUT IS NOT PUBLIC. It needs a live session to end, so it sits behind
// the gate like anything else.

const (
	// SignInBeginPath issues an assertion challenge.
	SignInBeginPath = "/api/signin/begin"

	// SignInCompletePath verifies an assertion and opens a session.
	SignInCompletePath = "/api/signin/complete"

	// SignOutPath ends the current session.
	SignOutPath = "/api/signout"

	// signInCookie carries the signed-in session.
	//
	// ⛔ __Host- prefix on TLS: the browser then refuses it unless it is
	// Secure, path=/ and carries no Domain attribute, so a sibling subdomain
	// cannot set it. ⚠️ Browsers reject the prefix on plain-HTTP localhost,
	// which is why the plain name exists.
	signInCookie      = "__Host-polis-session"
	signInCookiePlain = "polis-session"

	// ceremonyCookie carries a begun ceremony (a sign-in or an enrolment)
	// between its begin and its completion.
	//
	// ⭐ It is what lets the two land on different processes: the ceremony's
	// state is sealed under the site's key and comes back with the client.
	// ⛔ Its purpose is inside the MAC, so a sign-in ceremony can only complete
	// a sign-in and an enrolment ceremony only its own provider's enrolment.
	ceremonyCookie      = "__Host-polis-ceremony"
	ceremonyCookiePlain = "polis-ceremony"

	// signInCeremony is the purpose a sign-in ceremony is sealed under.
	signInCeremony = "signin"
)

// signInToken reads the session token from a request.
// requestIsSecure reports a request that arrived over TLS, directly or through
// a proxy that said so.
func requestIsSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// cookieNames are the session cookie names a request may present. ⛔ Over TLS
// only the __Host- name: accepting the unprefixed one there undid the
// __Host- protection (a sibling subdomain can set a plain cookie, never a
// __Host- one).
func cookieNames(r *http.Request, prefixed, plain string) []string {
	if requestIsSecure(r) {
		return []string{prefixed}
	}
	return []string{prefixed, plain}
}

// refuseCrossSite answers 403 to a state-changing request from another site's
// page, and reports whether it did. ⛔ The same rule as the localhost guard,
// applied by the owner routes themselves, so it holds whatever wraps
// Server.Handler(): a deployment built on Handler() has no localhost guard in
// front of it.
func refuseCrossSite(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return false
	}
	if crossSiteRequest(r) {
		writeRecoveryError(w, http.StatusForbidden, "refused: a request from another site", "")
		return true
	}
	return false
}

// freshReauth is how recently an owner must have signed in to enrol or remove
// a way to sign in. ⭐ Those acts outlive the session, so a stolen 12-hour
// session must not be able to perform them.
const freshReauth = 10 * time.Minute

// freshOwner reports whether the owner in r may make a change that outlives the
// session.
//
// ⭐ Re-authentication matters only where the owner is recognised BY A
// SESSION: that is what can be stolen and replayed for twelve hours, so it
// must be recent. A provider that proves ownership on every request (loopback
// trust on a laptop, an operator's auth upstream) is fresh by definition.
func (s *Server) freshOwner(r *http.Request) bool {
	inst := s.instance()
	if tok := signInToken(r); owner.SignedIn(inst, tok) {
		return owner.SignedInWithin(inst, tok, freshReauth)
	}
	ok, _ := owner.IsOwner(inst, r)
	return ok
}

func signInToken(r *http.Request) string { return cookieValue(r, signInCookie, signInCookiePlain) }

func ceremonyToken(r *http.Request) string {
	return cookieValue(r, ceremonyCookie, ceremonyCookiePlain)
}

func cookieValue(r *http.Request, prefixed, plain string) string {
	for _, name := range cookieNames(r, prefixed, plain) {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

// setOwnerCookie sets an owner-auth cookie: a session, an enrolment session or
// a ceremony.
//
// ⛔ ALL OF THEM ARE __Host-, Secure, HttpOnly and SameSite=Strict over TLS.
// __Host- makes the browser refuse the cookie unless it is Secure, path=/ and
// has no Domain, so a sibling subdomain cannot plant one. ⭐ Strict costs the
// owner nothing: the SPA is a static page and every privileged call is a
// same-site fetch, which carries a Strict cookie; only the first navigation
// from another site's link arrives without it, and that page needs none.
// ⚠️ Browsers reject the prefix on plain-HTTP localhost, which is why the
// plain name exists — and a laptop is recognised by loopback, not a cookie.
func setOwnerCookie(w http.ResponseWriter, r *http.Request, prefixed, plain, value string, expires time.Time) {
	secure := requestIsSecure(r)
	name := plain
	if secure {
		name = prefixed
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true, // ⛔ never readable by script
		Secure:   secure,
		SameSite: http.SameSiteStrictMode, // ⛔ not sent on a cross-site request
	})
}

// clearOwnerCookie clears an owner-auth cookie under both names.
// ⛔ A __Host- cookie is only accepted — and so only cleared — with Secure.
// Clearing it without Secure was silently ignored by browsers.
func clearOwnerCookie(w http.ResponseWriter, prefixed, plain string) {
	http.SetCookie(w, &http.Cookie{Name: prefixed, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	http.SetCookie(w, &http.Cookie{Name: plain, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

// passkeyProvider finds the registered passkey provider.
//
// ⭐ Looked up by name through the registry rather than held in a field: the
// handler then has no privileged reference, and a deployment that did not
// register it gets a clean refusal instead of a nil dereference.
func passkeyProvider() *PasskeyProvider {
	for _, p := range owner.Registered() {
		if pk, ok := p.(*PasskeyProvider); ok {
			return pk
		}
	}
	return nil
}

func (s *Server) handleSignInBegin(w http.ResponseWriter, r *http.Request) {
	if refuseCrossSite(w, r) {
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		writeRecoveryError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}
	if !s.signInLimiter().allow(s.clientKey(r)) {
		writeRecoveryError(w, http.StatusTooManyRequests, "too many sign-in attempts", "wait a minute and try again")
		return
	}
	pk := passkeyProvider()
	if pk == nil {
		writeRecoveryError(w, http.StatusNotFound, "no sign-in provider is available here", "")
		return
	}
	inst := s.instance()
	options, state, err := pk.BeginSignIn(inst)
	if err != nil {
		// ⭐ A caller with nothing enrolled is told so plainly, and pointed at
		// the door that exists for exactly that case.
		writeRecoveryError(w, http.StatusNotFound, err.Error(),
			"if you have lost access, prove ownership at "+RecoveryChallengePath)
		return
	}
	// ⭐ The ceremony rides in a sealed cookie, never in this process: the
	// completion may be served by another one. Sealing costs no store operation.
	tok, expires, err := owner.BeginCeremony(inst, signInCeremony, "", state)
	if err != nil {
		writeRecoveryError(w, http.StatusServiceUnavailable, "could not begin a sign-in", "")
		return
	}
	setOwnerCookie(w, r, ceremonyCookie, ceremonyCookiePlain, tok, expires)
	writeJSON(w, options)
}

func (s *Server) handleSignInComplete(w http.ResponseWriter, r *http.Request) {
	if refuseCrossSite(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeRecoveryError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}
	if !s.signInLimiter().allow(s.clientKey(r)) {
		writeRecoveryError(w, http.StatusTooManyRequests, "too many sign-in attempts", "wait a minute and try again")
		return
	}
	pk := passkeyProvider()
	if pk == nil {
		writeRecoveryError(w, http.StatusNotFound, "no sign-in provider is available here", "")
		return
	}

	inst := s.instance()
	// ⭐ No store operation until the assertion verifies: the ceremony is
	// checked by its MAC, the assertion against the (cached) credential.
	ceremony, err := owner.OpenCeremony(inst, signInCeremony, ceremonyToken(r))
	if err != nil {
		writeRecoveryError(w, http.StatusUnauthorized, "sign-in failed",
			"start again at "+SignInBeginPath)
		return
	}
	body, _ := readLimited(r)
	out, err := pk.CompleteSignIn(inst, body, ceremony)
	if err != nil {
		writeRecoveryError(w, http.StatusUnauthorized, "sign-in failed",
			"start again at "+SignInBeginPath)
		return
	}
	result, ok := out.(signInResult)
	if !ok {
		writeRecoveryError(w, http.StatusInternalServerError, "sign-in produced no session", "")
		return
	}

	clearOwnerCookie(w, ceremonyCookie, ceremonyCookiePlain)
	setOwnerCookie(w, r, signInCookie, signInCookiePlain, result.Token, result.Expires)
	// ⚠️ The token is NOT echoed in the body: a cookie the browser handles
	// cannot be pasted into a chat window by a confused owner.
	writeJSON(w, map[string]interface{}{"signed_in_until": result.Expires})
}

func (s *Server) handleSignOut(w http.ResponseWriter, r *http.Request) {
	if refuseCrossSite(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeRecoveryError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}
	inst := s.instance()
	// ⭐ One session: clear the cookie AND remember its id until it would have
	// expired, so a copy of the cookie elsewhere is dead too. Everywhere: raise
	// the site's generation, which every process honours within the cache's
	// window. Both are one write, earned by a request the gate let through.
	var err error
	if strings.TrimSpace(r.URL.Query().Get("everywhere")) == "1" {
		err = owner.SignOutAll(inst)
	} else {
		err = owner.SignOut(inst, signInToken(r))
	}
	// ⭐ Clear the cookie whichever name it arrived under.
	clearOwnerCookie(w, signInCookie, signInCookiePlain)
	if err != nil {
		// ⚠️ The cookie is cleared either way, but the owner is told the
		// revocation did not land: a copy elsewhere may still work.
		s.LogWarn("sign-out could not be recorded: %v", err)
		writeRecoveryError(w, http.StatusServiceUnavailable, "signed out here, but the sign-out could not be recorded",
			"sign out again, or sign out everywhere")
		return
	}
	writeJSON(w, map[string]bool{"signed_out": true})
}
