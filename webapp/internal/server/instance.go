package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"sync"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// instanceOptions is what building a self-hosted instance needs, whichever way
// it is then served: on a loopback listener (Run) or as a handler a platform
// calls over the network (NewNetworkHandler).
type instanceOptions struct {
	DataDir      string
	CLIThemesDir string
	CLIVersion   string
	DevMode      bool
	Getenv       func(string) string

	// Network: the instance is reached over the network with no loopback
	// listener and no localhost guard in front of it. ⛔ Local mode is refused
	// outright then, whatever the backend: local trust is loopback trust.
	Network bool

	// Backend, when set, is used instead of the one POLIS_STORAGE names. For
	// tests: a fake store needs no credentials or network.
	Backend *storage.Backend
}

// newInstance builds a self-hosted Server: where its site is stored, which mode
// it runs in, its configuration, its identity key and its recovery wiring.
//
// ⛔ THIS IS THE ONE CONSTRUCTION. Run and the network handler both call it;
// a second hand-written copy is how a check gets added to one path and not the
// other, and the mode check is exactly the kind of check that matters on both.
//
// ⭐ The mode is checked BEFORE anything reads or writes the site, so a refused
// boot leaves no trace in the store. Nothing it skips depends on the mode: the
// one mode-dependent rule in Initialize (whether the process environment wins
// over .env) is the same for every self-hosted mode, none of which has an
// operator.
func newInstance(o instanceOptions) (*Server, error) {
	getenv := o.Getenv
	s := NewServer(o.DataDir, o.CLIThemesDir)
	s.CLIVersion = o.CLIVersion

	// ⭐ WHERE THE SITE IS STORED, chosen before anything touches it. Unset is
	// the filesystem, as it always was; anything else must be complete, and a
	// partial spec stops the boot rather than falling back to a disk.
	spec, err := storage.BackendFromEnv(getenv)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	backendKind := spec.Kind
	switch {
	case o.Backend != nil:
		if err := s.UseBackend(s.withDeployQueue(*o.Backend, getenv)); err != nil {
			return nil, fmt.Errorf("storage: %w", err)
		}
		if backendKind == storage.BackendPOSIX {
			backendKind = "test"
		}
	case spec.Kind != storage.BackendPOSIX:
		backend, berr := storage.OpenBackend(spec, o.DataDir, getenv)
		if berr != nil {
			return nil, fmt.Errorf("storage: %w", berr)
		}
		if uerr := s.UseBackend(s.withDeployQueue(backend, getenv)); uerr != nil {
			return nil, fmt.Errorf("storage: %w", uerr)
		}
		log.Printf("[i] storage: %s", spec.Kind)
		// ⭐ Not fatal (a request can still deliver a credential), never silent.
		if backend.EnvironmentCredentialErr != nil {
			log.Printf("[warning] storage: the environment's credential was not used: %v", backend.EnvironmentCredentialErr)
		}
	case o.Network:
		// ⛔ A platform that calls a handler gives it no disk that survives the
		// call: a site written there is lost with the instance.
		return nil, fmt.Errorf("storage: %s is unset, and an instance served over the network keeps nothing on its local disk; "+
			"set %s to a remote backend", storage.EnvStorageKind, storage.EnvStorageKind)
	}

	// ⛔⛔ THIS IS WHERE --dev STOPS BEING A SECOND SOURCE OF TRUTH.
	//
	// It used to inject window.__POLIS_DEV, a flag the SPA checked ALONGSIDE
	// the hosted flag to decide one capability. Two globals answering "is this
	// instance reachable?" differently is the scattering the capability model
	// removes, and leaving it would make "derive, never set" true of four
	// capabilities and a lie about the fifth.
	//
	// ⭐ So --dev now says WHICH MODE to be, and the real derivation does the
	// rest. What the developer wanted — a reachable instance, so the messaging
	// UI is live — is exactly remote-client-signed: a public API, no operator,
	// and the browser signs.
	//
	// ⚠️ IT ALSO MEANS canRunHooks GOES FALSE IN DEV, which is honest for a
	// reachable instance and currently consumes nothing: no surface branches
	// on that capability yet. ⭐ A developer who wants a different shape sets
	// POLIS_MODE explicitly, which is the real model rather than a flag that
	// fakes one field of it.
	instanceMode, modeNote, modeUnset := capability.ResolveSelfHosted(getenv)
	if o.Network && modeNote == "" && instanceMode == mode.Local {
		// ⛔ First, and for unset too: on a network path there is no laptop for
		// local to mean, whatever the backend or the other settings say.
		return nil, fmt.Errorf("instance mode: an instance served over the network cannot run as local, which grants "+
			"loopback owner-trust; set %s to remote-server-signed or remote-client-signed", capability.ModeEnv)
	}
	if err := checkModeForBackend(instanceMode, modeNote, modeUnset, o.DevMode, backendKind, remoteOnlySignals(getenv)); err != nil {
		return nil, fmt.Errorf("instance mode: %w", err)
	}
	// ⛔ --dev grants loopback owner-trust only to the laptop it was made for:
	// a mode named explicitly in POLIS_MODE keeps its own trust rules, so
	// `POLIS_MODE=remote-server-signed polis serve --dev` is not owner-trusted
	// to anyone who reaches the port.
	s.devServer = o.DevMode && instanceMode == mode.Local && !o.Network
	if instanceMode == mode.Local && o.DevMode {
		instanceMode = mode.RemoteClientSigned
	}
	s.Mode = instanceMode

	// ⛔ A SITE ON A REMOTE BACKEND LOGS TO STDOUT. The rotating log file is
	// appended to per request, which a store without append cannot take, and a
	// platform's local disk does not survive the instance anyway; the platform
	// captures stdout.
	remote := backendKind != storage.BackendPOSIX
	if remote {
		s.Logger = NewStdoutLogger(LogLevelBasic)
	}

	if s.StartsOnRequest() {
		// ⭐ The backend has no credential until a request delivers one, so the
		// site work waits for the first request (backendMiddleware). The
		// environment is still read now: the recovery wiring below needs the
		// base URL, and reading it touches no store.
		s.LoadEnv()
		log.Printf("[i] storage needs a request-delivered credential: the site starts on the first request")
	} else {
		s.Initialize()
		// ⭐ A server-signed instance makes its own key on first boot, into its
		// own store: nothing secret is injected at creation. ⛔ Fatal on failure
		// — an instance that signs unattended with no key would fail every act,
		// later and more confusingly. A backend that starts on request runs this
		// in its deferred start.
		if err := s.EnsureIdentityKey(context.Background()); err != nil {
			return nil, fmt.Errorf("identity key: %w", err)
		}
		// ⭐ And the site around that key, the first time. Not fatal.
		s.EnsureSite(context.Background())
		// ⭐ And a store never deployed from (a seeded one) is deployed.
		s.deployIfNeverDeployed(context.Background())
		// ⭐ And the owner-session key, so an unproven sign-in never reads it.
		s.prepareOwnerAuth()
	}

	// ⚠️ A MALFORMED RECOVERY KEY MUST FAIL THE BOOT, not surprise somebody
	// who is already locked out at the moment they can least afford it.
	// ⭐ An ABSENT one is fine: local and custodied need none. The domain
	// comes from LoadEnv, which honours the process environment, so a platform
	// that configures by environment variables gets a bound challenge.
	recovery, err := owner.NewRecovery(getenv(owner.RecoveryKeyEnv), s.GetBaseURL())
	if err != nil {
		return nil, fmt.Errorf("recovery configuration: %w", err)
	}
	s.Recovery = recovery
	return s, nil
}

// editorRoutes is the owner's full surface: the editor API, the v1 content API,
// the bundle reference assets the SPA hydrates from, and the SPA itself.
func editorRoutes(s *Server, webFS fs.FS) *http.ServeMux {
	routes := http.NewServeMux()
	SetupRoutes(routes, s)

	// Setup v1 content API routes (alongside existing /api/ routes)
	mountV1(routes, s)

	// Bundle reference assets (step-06/6.a). Serves the v4 stream shape's
	// stream.js / stream.css / snippet HTML to the SPA homepage so it can
	// hydrate against the canonical controller without duplicating files
	// in webapp/internal/webui/www. Path shape:
	//   /bundle-assets/pub.polis.core/shapes/v4/stream.js
	//   /bundle-assets/pub.polis.core/shapes/v4/stream.css
	//   /bundle-assets/pub.polis.core/shapes/v4/snippets/stream-filter.html
	routes.Handle("/bundle-assets/", http.StripPrefix("/bundle-assets/", http.FileServer(http.FS(bundle.ReferencePayloadFS()))))

	// Static files from embedded filesystem with SPA fallback
	routes.Handle("/", spaHandler(webFS, s.Site()))
	return routes
}

// mountV1 puts the v1 content API on routes.
//
// ⭐ On a backend that starts on request, the engine is built on first use,
// AFTER the deferred start. Built at route setup it would read the site before
// any credential arrived, fall back to the default bundle, and capture a
// discovery URL Initialize had not yet set; and if building failed, /v1 was
// silently never mounted for the life of the instance. Until it can be built,
// /v1 answers 503, as the rest of a starting instance does.
func mountV1(routes *http.ServeMux, s *Server) {
	logEvent := func(event string, fields map[string]interface{}) { s.LogEvent(event, fields) }
	if !s.StartsOnRequest() {
		if apiEngine, err := s.newContentEngine(); err != nil {
			log.Printf("[warning] v1 API not available: %v", err)
		} else {
			api.SetupRoutes(routes, apiEngine, s.Site(), logEvent)
			log.Printf("[info] v1 content API enabled")
		}
		return
	}
	var mu sync.Mutex
	var inner http.Handler
	routes.Handle("/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if inner == nil {
			apiEngine, err := s.newContentEngine()
			if err != nil {
				mu.Unlock()
				log.Printf("[warning] v1 API not available yet: %v", err)
				http.Error(w, "this instance is starting; try again shortly", http.StatusServiceUnavailable)
				return
			}
			m := http.NewServeMux()
			api.SetupRoutes(m, apiEngine, s.Site(), logEvent)
			inner = m
		}
		h := inner
		mu.Unlock()
		h.ServeHTTP(w, r)
	}))
}

// instanceHandler is the middleware every self-hosted instance runs inside,
// whichever way it is served.
//
// ⛔ KEY RELOAD IS OUTERMOST OF THE THREE, AND THE ORDER IS LOAD-BEARING.
// Gating first means a refused request never reloads keys — so a provider that
// needs to verify something against the current key would read a retired one,
// and the reload would only happen after something was already allowed
// through. Reload, then decide, then handle.
//
// ⭐ The backend middleware wraps everything that can read the site: it offers
// the request's credential and, on a backend that starts on request, runs the
// deferred start before any handler. What sits outside it (security headers,
// request logging, and on a loopback listener the localhost guard) never reads
// the site.
func (s *Server) instanceHandler(routes http.Handler) http.Handler {
	return s.backendMiddleware(s.deployMiddleware(s.keyReloadMiddleware(s.ownerGateMiddleware(routes))))
}

// NewNetworkHandler builds a self-hosted instance served over the network by a
// platform that calls a handler (a serverless function), with no listener of
// its own.
//
// It differs from Run in exactly what a network path must not have: no
// loopback bind, no port, no browser, no localhost guard, and BoundToLoopback
// left false, so nothing grants loopback owner-trust. ⛔ Owner auth is the gate
// here. Security headers and request logging stay; the client address comes
// from the header the platform sets, never one a client could choose.
//
// ⛔ Errors name the cause and never a value: the environment holds secrets.
//
// version is the build's release version ("dev" when unstamped). ⭐ A remote
// instance on Vercel fetches its own release's function manifest by it, so an
// unstamped build can deploy only with POLIS_FUNCTION_MANIFEST set.
func NewNetworkHandler(webFS fs.FS, dataDir, version string, getenv func(string) string) (http.Handler, error) {
	_, h, err := newNetworkHandler(webFS, instanceOptions{DataDir: dataDir, CLIVersion: version, Getenv: getenv})
	return h, err
}

func newNetworkHandler(webFS fs.FS, o instanceOptions) (*Server, http.Handler, error) {
	if webFS == nil {
		return nil, nil, errors.New("server: no web UI filesystem")
	}
	o.Network = true
	s, err := newInstance(o)
	if err != nil {
		return nil, nil, err
	}
	// ⭐ Recorded from the construction, never inferred later: nothing here
	// listens on loopback.
	s.BoundToLoopback = false
	s.Proxy = ProxyVercel
	return s, securityHeadersMiddleware(requestLoggingMiddleware(s.Logger, s.instanceHandler(editorRoutes(s, webFS)))), nil
}
