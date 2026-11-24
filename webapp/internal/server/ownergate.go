package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
)

// The owner gate: every API route is private unless it says otherwise.
//
// ⛔⛔ DEFAULT-DENY, AND THE ARGUMENT FOR IT IS A MISTAKE MADE WHILE PLANNING
// THIS. The server registers over a hundred routes. Asked to sort them by hand,
// a careful reader with the whole list in front of them classified
// /api/download-site, /api/content/, /api/automations, /api/hooks/generate,
// /api/feed and /api/profiles as "plausibly public". ⛔ Every one is an owner
// surface, and /api/download-site serves the entire site INCLUDING .polis/keys.
//
// ⭐ That is not an argument for being more careful. It is evidence that a
// per-handler allow model is the wrong shape: it has to be right sixty times
// and is wrong forever the first time somebody adds a route and forgets. So the
// default flips — a new route is PRIVATE until someone names it — and
// TestEveryAPIRouteIsClassified fails the build until they do.
//
// ⚠️ THIS GATE CHANGES NOTHING ON ANY DEPLOYMENT THAT EXISTS TODAY, by
// construction, and that is deliberate rather than lucky:
//
//	local on loopback  → the loopback provider answers, exactly as the old
//	                     bind-comment reasoning did
//	custodied          → the operator provider answers, because the operator's
//	                     front door already authenticated the session upstream
//	remote             → ⭐ nothing is enrolled, so privileged routes are
//	                     refused. This is the mode the gate exists for, and the
//	                     mode that does not ship yet.

// publicAPIRoutes are the API paths a stranger may reach.
//
// ⛔ ADDING A LINE HERE IS A SECURITY DECISION. Before you do: does this
// endpoint read or write anything the owner would not publish? Does its error
// text reveal structure — a path, a missing file, a validation failure? ⭐ "The
// SPA needs it before login" is a reason to TRIM the response, not to open the
// route.
var publicAPIRoutes = map[string]string{
	// path : why it is public
	"/api/capabilities": "a third party reads it to decide how to talk to this site; " +
		"behind the gate it returns 401, which reads as 'no API at all' and is the " +
		"opposite of what the descriptor says",

	RecoveryChallengePath: "the recovery door. ⛔ PUBLIC BY NECESSITY — you cannot authenticate " +
		"to reach the thing that lets you authenticate, and a locked-out owner is refused by " +
		"everything else. It mints a nonce and nothing more; rate-limited in the handler",
	RecoveryVerifyPath: "the recovery door. ⛔ PUBLIC BY NECESSITY, same reason. It accepts only a " +
		"signature over a challenge this instance just issued, so there is no secret to guess",

	SignInBeginPath: "the sign-in ceremony. ⛔ PUBLIC BY NECESSITY — somebody not signed in is " +
		"exactly who needs it, and it refuses a caller with nothing enrolled rather than " +
		"handing out a usable ceremony. Rate-limited",
	SignInCompletePath: "the sign-in ceremony, same reason. It accepts only an assertion over a " +
		"challenge this instance just issued",

	// ⭐ These two already serve public content to anonymous visitors on hosted
	// tenants — the operator releases them before its own session check, so
	// gating them here would break the v4 shape's public pages (the only
	// caller left: v4 is unsupported but still renders).
	"/api/v1/stream/focus-comment": "public comment content, already served to anonymous visitors",
	"/api/v1/stream/body":          "a post's body for the stream to show in place: the owner may ask for any post; anyone else only this site's or a followed site's, so it is no one's fetching proxy",
}

// publicAPIPrefixes are path prefixes a stranger may reach.
//
// ⚠️ A PREFIX IS BLUNTER THAN A PATH and each one is a standing commitment that
// everything beneath it is safe. Prefer exact paths; justify every prefix.
var publicAPIPrefixes = map[string]string{
	// ⛔ A PREFIX, AND IT MUST BE MATCHED AS ONE. It used to sit in the exact
	// map above, so /api/enrol/passkey/begin never matched and the gate refused
	// it 403 before recovery's enrolment session was ever looked at: a fresh
	// remote owner could never enrol. Tests that called the handler directly
	// bypassed the gate and could not see it.
	EnrolPathPrefix: "⛔ NOT public in effect — handleEnrol refuses first unless the caller holds " +
		"an enrolment session (from recovery) or is already the owner (enrolAuthorised). It is " +
		"passed through the gate because a fresh owner has no other way to prove anything yet",
	"/pql/": "scope-gated at its own layer — first-person scopes (me, my-*, dm, drafts) " +
		"are owner-private there, and the operator applies the same split upstream. " +
		"⛔ This entry delegates to that check; it does not replace it",
}

// isPublicAPIRoute reports whether a path is on the public list.
func isPublicAPIRoute(path string) bool {
	if _, ok := publicAPIRoutes[path]; ok {
		return true
	}
	for prefix := range publicAPIPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// guarded reports whether a path is subject to the gate at all.
//
// ⭐ Only /api/ and /pql/ are. The rendered site, .well-known, robots.txt,
// rsl.xml, the licence pages and static assets are published to everyone by
// design — that is what a polis site IS — and routing them through an owner
// check would be a category error, not extra safety.
func guarded(path string) bool {
	return strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/pql/")
}

// instance describes this deployment for the seam.
//
// ⛔ Loopback comes from the LISTENER, recorded at startup, never from the
// request. See owner.Instance.Loopback: a header, RemoteAddr or forwarded-for
// can each be forged, and deriving it per request turns loopback trust from a
// safety property into a universal bypass.
func (s *Server) instance() owner.Instance {
	m := s.Mode
	if m == "" {
		m = mode.Local
	}
	return owner.Instance{Mode: m, SiteID: s.DataDir, Loopback: s.BoundToLoopback, Dev: s.devServer,
		BaseURL: s.GetBaseURL(), Store: s.ownerStore(), Cache: s.ownerCache()}
}

// ownerGateMiddleware refuses a privileged request that cannot be shown to come
// from the owner.
func (s *Server) ownerGateMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ⛔ An owner-private /pql/ read is NEVER public, whatever the prefix
		// table says, and is never served to a cross-site page — loopback trust
		// included, because a page the owner has open can reach the listener.
		ownerPQL := strings.HasPrefix(r.URL.Path, "/pql/") && PQLRequiresOwner(r)
		if ownerPQL && crossSiteRequest(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"owner-private content is not served to another site's page"}`))
			return
		}
		// ⛔ A state-changing request from another site's page is refused on
		// every guarded route, public ones included, before anyone asks who
		// the caller is: the owner's own cookie rides along on a same-site
		// page's POST, and a text/plain POST needs no preflight. Here rather
		// than in front of Handler(), so it holds whatever wraps it.
		// ⛔ The widget-token routes are spared ONLY where an operator runs the
		// instance: hosted's edge checks the widget token before them. Nothing
		// here does, so elsewhere they would sign as the owner on the cookie.
		widgetRoute := CrossOriginAPIRoute(r.URL.Path) && capability.For(s.instance().Mode).HasOperator
		if guarded(r.URL.Path) && CrossSiteWrite(r) && !widgetRoute {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"cross-site request refused"}`))
			return
		}
		if !guarded(r.URL.Path) || (!ownerPQL && isPublicAPIRoute(r.URL.Path)) {
			next.ServeHTTP(w, r)
			return
		}

		ok, err := owner.IsOwner(s.instance(), r)
		if ok {
			next.ServeHTTP(w, r)
			return
		}

		// ⭐ The two refusals are DIFFERENT FACTS and the remedies differ, so
		// they are not collapsed: one needs credentials, the other needs the
		// owner to enrol a provider after proving ownership with the recovery key. A caller that cannot tell
		// them apart cannot say anything useful to the person reading it.
		if err == owner.ErrNoProvider {
			// ⚠️ 403, not 401: there is no credential to supply yet, so
			// challenging for one would send the owner looking for a login
			// that does not exist.
			writeOwnerRefusal(w, s.instance(), http.StatusForbidden, RefusalNoProvider,
				"no owner authentication is set up on this instance",
				"prove ownership with the recovery key at /api/recover/challenge, then enrol a provider")
			return
		}
		writeOwnerRefusal(w, s.instance(), http.StatusUnauthorized, RefusalNotSignedIn, "unauthorized", "")
	})
}

// The gate's refusal reasons. ⭐ MACHINE-READABLE, because the SPA picks a
// screen from them — setup for the first, sign-in for the second — and the
// error text is prose that may be reworded. ⛔ Renaming one is an API change.
const (
	RefusalNoProvider  = "no-provider"
	RefusalNotSignedIn = "not-signed-in"
)

// wayIn is the public projection of one provider on a refusal: a way in this
// INSTANCE supports, never whether the owner has set it up.
type wayIn struct {
	Name       string `json:"name"`
	Kind       string `json:"kind,omitempty"`
	Enrollable bool   `json:"enrollable"`
}

// writeOwnerRefusal answers a refused request with the reason and the ways in.
//
// ⭐ The ways in are what signed-out setup and sign-in screens render from: the
// signed-out app cannot read /api/providers, and must not hardcode a provider.
// ⛔ A GENERIC SIGN-IN, NOT A LIST OF WHAT THE OWNER SET UP. Each entry is a
// way in the instance SUPPORTS (available here: decided by the instance's mode
// and configuration, the same for every site configured alike) with its
// ceremony kind and whether it has an enrolment step — all facts about the
// software, none about the owner. ⛔ Never add which ways are enrolled: that
// told any visitor which kinds of sign-in this site's owner chose, a
// fingerprint and a hint for targeting. A screen offers every way listed, and
// the ceremony itself (the browser's own passkey prompt) is where the owner's
// choice shows. ⚠️ Whether ANYTHING is enrolled still shows, as the 403/401
// split: it picks setup or sign-in, and setup needs the recovery key anyway.
func writeOwnerRefusal(w http.ResponseWriter, inst owner.Instance, status int, reason, message, remedy string) {
	ways := []wayIn{}
	for _, st := range owner.Statuses(inst) {
		if !st.Available {
			continue
		}
		ways = append(ways, wayIn{Name: st.Name, Kind: st.Kind, Enrollable: st.Enrollable})
	}
	body := map[string]interface{}{"error": message, "reason": reason, "ways_in": ways}
	if remedy != "" {
		body["remedy"] = remedy
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
