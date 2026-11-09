package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
)

// The enrolment surface: begin, complete, list, remove.
//
// ⭐⭐⭐ NOTHING HERE KNOWS WHAT A PASSKEY IS, and that is the requirement, not
// a happy accident. Two providers are already deferred on non-design grounds —
// password on scope, Vercel on three endpoints somebody else controls — and
// each must be addable later WITHOUT a redesign. ⛔ One deferral is a special
// case to accommodate; two is a property this file has to have.
//
// So the shape is "begin, then complete" for a NAMED provider. A passkey's
// challenge/response, a password compare and an OAuth round trip are all that
// shape. ⚠️ If a provider ever needs a third step or a different verb, resist
// adding one here: give it a richer payload inside the same two calls.
//
// ⛔ REACHING THESE ROUTES NEEDS EITHER AN ENROLMENT SESSION OR OWNERSHIP, and
// both arms matter. The session is how a locked-out owner who proved themselves
// with the recovery key gets back in. Ownership is how an owner who is ALREADY
// in adds a second authenticator without touching the recovery key at all —
// the ordinary case, and the one that keeps the recovery key for emergencies
// rather than making it part of routine use.

const (
	// EnrolPathPrefix is the begin/complete surface: /api/enrol/{provider}/{step}.
	EnrolPathPrefix = "/api/enrol/"

	// ProvidersPath lists what can let the owner in, and removes one.
	ProvidersPath = "/api/providers"
)

// enrolAuthorised reports whether this request may enrol or remove a provider,
// and the recovery-proved enrolment session that authorised it, if any.
//
// ⭐ An enrolment session is a sealed token: any process recognises it, and its
// single use is checked against the store's markers, not a process map.
func (s *Server) enrolAuthorised(r *http.Request) (bool, *owner.EnrolmentSession) {
	inst := s.instance()
	if token := enrolmentToken(r); token != "" {
		if sess, err := owner.CheckEnrolment(inst, token); err == nil {
			return true, &sess // authorised, via a recovery-proved session
		}
	}
	ok, _ := owner.IsOwner(inst, r)
	return ok, nil
}

// handleProviders lists providers, or removes one.
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	if refuseCrossSite(w, r) {
		return
	}
	authorised, recoverySession := s.enrolAuthorised(r)
	if !authorised {
		writeRecoveryError(w, http.StatusUnauthorized, "not authorised", "")
		return
	}
	// ⛔ A recovery session's only act is enrolling (a recovery key proves ownership once, to set up a lasting way in). It
	// may not list or remove providers.
	if recoverySession != nil {
		if ok, _ := owner.IsOwner(s.instance(), r); !ok {
			writeRecoveryError(w, http.StatusForbidden, "a recovery session can only enrol a way to sign in", "")
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		inst := s.instance()
		statuses := owner.Statuses(inst)
		// ⭐ The list is the point of criterion #2. The WEAKEST enrolled
		// provider sets the floor, so an owner who cannot see what is enrolled
		// cannot reason about who can let someone in — and cannot notice
		// something they did not enrol.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"providers": statuses,
			// ⚠️ Surfaced so the UI can show D6's acknowledgement BEFORE the
			// owner removes their last way in, rather than discovering it
			// after.
			"enrolled_count":      countEnrolled(statuses),
			"recovery_configured": s.Recovery != nil && s.Recovery.Configured(),
		})

	case http.MethodDelete:
		if !s.freshOwner(r) {
			writeRecoveryError(w, http.StatusUnauthorized, "sign in again to remove a way to sign in",
				"removing one outlives your session, so it needs a sign-in from the last "+freshReauth.String())
			return
		}
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if name == "" {
			writeRecoveryError(w, http.StatusBadRequest, "no provider named", "pass ?name=<provider>")
			return
		}
		inst := s.instance()

		// ⛔ ONLY SOMETHING THE OWNER SET UP CAN BE REMOVED, and only if it is
		// set up. Operator trust and loopback are not enrolments — nothing is
		// stored to delete — and this used to answer 200 {"removed": …} for
		// them while changing nothing.
		var target owner.Provider
		for _, p := range owner.Registered() {
			if p.Name() == name {
				target = p
			}
		}
		if target == nil {
			writeRecoveryError(w, http.StatusNotFound, "no such way to sign in", "")
			return
		}
		if _, ok := target.(owner.Enroller); !ok {
			writeRecoveryError(w, http.StatusConflict, name+" cannot be removed here",
				"it is not something you set up: it comes from how this instance is run")
			return
		}
		if !owner.IsEnrolledIn(inst, name) {
			writeRecoveryError(w, http.StatusNotFound, name+" is not set up", "there is nothing to remove")
			return
		}

		// ⛔ D6: REMOVING THE LAST PROVIDER IS PERMITTED, because on a host the
		// owner controls lockout cannot happen — whoever holds the account can
		// set an env var and redeploy. ⚠️ But correct-and-surprising is still
		// bad, so it requires an explicit acknowledgement that names the
		// recovery path.
		if lastEnrolled(owner.Statuses(inst), name) && r.URL.Query().Get("acknowledge") != "recovery" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":       "this is the last way you can sign in",
				"consequence": "removing it signs you out and nothing else can let you back in",
				"remedy": "you would recover by signing a challenge with your recovery key, " +
					"which needs " + owner.RecoveryKeyEnv + " set on this instance",
				"confirm": "repeat with &acknowledge=recovery",
			})
			return
		}

		// ⭐ One credential, not the whole provider, when ?credential= names it.
		if cred := strings.TrimSpace(r.URL.Query().Get("credential")); cred != "" && name == passkeyName {
			pk := passkeyProvider()
			if pk == nil {
				writeRecoveryError(w, http.StatusNotFound, "no passkey provider", "")
				return
			}
			if err := pk.RemoveCredential(inst, cred); err != nil {
				writeRecoveryError(w, http.StatusBadRequest, err.Error(), "")
				return
			}
			writeJSON(w, map[string]interface{}{"removed": name, "credential": cred})
			return
		}
		if err := owner.DeleteEnrolment(inst, name); err != nil {
			writeRecoveryError(w, http.StatusBadRequest, err.Error(), "")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"removed": name})

	default:
		w.Header().Set("Allow", "GET, DELETE")
		writeRecoveryError(w, http.StatusMethodNotAllowed, "method not allowed", "")
	}
}

// handleEnrol routes /api/enrol/{provider}/{begin|complete}.
func (s *Server) handleEnrol(w http.ResponseWriter, r *http.Request) {
	if refuseCrossSite(w, r) {
		return
	}
	authorised, enrolSession := s.enrolAuthorised(r)
	viaSession := enrolSession != nil
	if !authorised {
		writeRecoveryError(w, http.StatusUnauthorized, "not authorised",
			"prove ownership at "+RecoveryChallengePath+" first")
		return
	}
	// ⛔ An owner enrolling through a session must have signed in recently: a
	// stolen session must not be able to add a passkey that outlives it.
	if !viaSession && !s.freshOwner(r) {
		writeRecoveryError(w, http.StatusUnauthorized, "sign in again to enrol a way to sign in",
			"enrolling outlives your session, so it needs a sign-in from the last "+freshReauth.String())
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeRecoveryError(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, EnrolPathPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" {
		writeRecoveryError(w, http.StatusNotFound, "no such enrolment step",
			"expected "+EnrolPathPrefix+"{provider}/begin or /complete")
		return
	}
	name, step := parts[0], parts[1]

	// ⭐ Resolved from the registry, so this handler never names a provider.
	var provider owner.Provider
	for _, p := range owner.Registered() {
		if p.Name() == name {
			provider = p
		}
	}
	if provider == nil {
		writeRecoveryError(w, http.StatusNotFound, "no such provider", "")
		return
	}
	enroller, ok := provider.(owner.Enroller)
	if !ok {
		// ⚠️ loopback and operator are real providers with nothing to enrol:
		// their premise is the deployment itself. Saying so plainly beats a
		// 404 that reads as "misspelled".
		writeRecoveryError(w, http.StatusBadRequest, "this provider cannot be enrolled",
			"it applies automatically where it is available")
		return
	}

	inst := s.instance()
	body, _ := readLimited(r)

	// ⭐ The ceremony is sealed for THIS provider's completion, and tied to the
	// enrolment session that authorised it, if one did. Its state rides in a
	// cookie, so begin and complete may be served by different processes.
	purpose := "enrol:" + name
	bound := ""
	if viaSession {
		bound = enrolSession.ID
	}

	switch step {
	case "begin":
		out, state, err := enroller.BeginEnrol(inst, body)
		if err != nil {
			writeRecoveryError(w, http.StatusBadRequest, err.Error(), "")
			return
		}
		tok, expires, err := owner.BeginCeremony(inst, purpose, bound, state)
		if err != nil {
			writeRecoveryError(w, http.StatusServiceUnavailable, "could not begin enrolment", "")
			return
		}
		setOwnerCookie(w, r, ceremonyCookie, ceremonyCookiePlain, tok, expires)
		writeJSON(w, out)

	case "complete":
		ceremony, err := owner.OpenCeremony(inst, purpose, ceremonyToken(r))
		if err != nil || ceremony.Bound != bound {
			// ⛔ A ceremony begun under one authorisation cannot be completed
			// under another: begun by a recovery session, it completes only
			// with that session; begun by a signed-in owner, only without one.
			writeRecoveryError(w, http.StatusBadRequest, owner.ErrCeremony.Error(), "")
			return
		}
		out, record, err := enroller.CompleteEnrol(inst, body, ceremony.State)
		if err != nil {
			writeRecoveryError(w, http.StatusBadRequest, err.Error(), "")
			return
		}
		// ⛔ VERIFIED, NOW SPEND, THEN SAVE. The provider has checked the
		// answer; the single-use marker is written next, so a replay racing
		// this completion on any process loses before anything is saved.
		// ⭐ A recovery-proved session is what is spent when one authorised
		// the enrolment ("using it consumes it"): the ceremony is tied to it,
		// so spending the session also spends the ceremony. Otherwise the
		// ceremony itself is spent.
		if viaSession {
			err = owner.ConsumeEnrolment(inst, *enrolSession)
		} else {
			err = owner.Consume(inst, ceremony.ID, ceremony.Expires)
		}
		if err != nil {
			writeRecoveryError(w, http.StatusBadRequest, "this enrolment was already completed",
				"begin again")
			return
		}
		if err := owner.SaveEnrolment(inst, name, record); err != nil {
			writeRecoveryError(w, http.StatusInternalServerError, "the enrolment could not be saved", "begin again")
			return
		}
		clearOwnerCookie(w, ceremonyCookie, ceremonyCookiePlain)
		if viaSession {
			clearOwnerCookie(w, enrolmentCookie, enrolmentCookiePlain)
		}
		writeJSON(w, out)

	default:
		writeRecoveryError(w, http.StatusNotFound, "no such enrolment step",
			"expected begin or complete")
	}
}

func countEnrolled(sts []owner.Status) int {
	n := 0
	for _, s := range sts {
		if s.Enrolled {
			n++
		}
	}
	return n
}

// lastEnrolled reports whether name is the only thing still letting the owner
// in.
//
// ⚠️ It counts ENROLLED providers, not registered ones: a provider that is
// available but not set up protects nobody, and counting it would suppress the
// warning exactly when it is needed.
func lastEnrolled(sts []owner.Status, name string) bool {
	enrolled := []string{}
	for _, s := range sts {
		if s.Enrolled {
			enrolled = append(enrolled, s.Name)
		}
	}
	return len(enrolled) == 1 && enrolled[0] == name
}

func readLimited(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	limited := http.MaxBytesReader(nil, r.Body, 256*1024)
	for {
		n, err := limited.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
