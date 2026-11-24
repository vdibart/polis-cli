// Package server provides the HTTP server implementation for the Polis webapp.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/deploy"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/pql"
	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// DefaultDiscoveryServiceURL is the default discovery service URL matching the CLI
const DefaultDiscoveryServiceURL = "https://ds.polis.pub"

// Request body size limits
const (
	MaxPostBodySize    = 256 << 10 // 256KB - post markdown
	MaxCommentBodySize = 64 << 10  // 64KB - comment text
	MaxSnippetBodySize = 64 << 10  // 64KB - snippet/about content
	MaxHookBodySize    = 32 << 10  // 32KB - hook scripts
	MaxDefaultBodySize = 1 << 20   // 1MB - everything else
)

// limitBody wraps a handler with http.MaxBytesReader to cap request body size.
func limitBody(next http.HandlerFunc, maxBytes int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next(w, r)
	}
}

// isBodyTooLargeError returns true if the error is from http.MaxBytesReader.
func isBodyTooLargeError(err error) bool {
	// MaxBytesError was added in Go 1.19; also check the legacy string.
	if err == nil {
		return false
	}
	if _, ok := err.(*http.MaxBytesError); ok {
		return true
	}
	return err.Error() == "http: request body too large"
}

// handleBodyTooLarge checks for body-too-large error and logs a security event.
// Returns true if the error was a body-too-large error (and the response was written).
func (s *Server) handleBodyTooLarge(w http.ResponseWriter, r *http.Request, err error) bool {
	if !isBodyTooLargeError(err) {
		return false
	}
	s.LogEvent("pub.polis.security.body_too_large", map[string]interface{}{
		"path": r.URL.Path, "method": r.Method,
		"request_id": RequestIDFromContext(r.Context()),
	})
	http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
	return true
}

// Log levels
const (
	LogLevelOff     = 0 // No logging
	LogLevelBasic   = 1 // Basic logging (errors, warnings, important info)
	LogLevelVerbose = 2 // Verbose logging (all operations)
)

// Config holds the application configuration
// Note: SetupCode and Subdomain are deprecated but still parsed for backwards compatibility
type Config struct {
	SetupCode string `json:"setup_code,omitempty"` // Deprecated: ignored
	Subdomain string `json:"subdomain,omitempty"`  // Deprecated: derive from .well-known/polis
	SetupAt   string `json:"setup_at,omitempty"`   // Deprecated: derive from .well-known/polis

	// Hooks configuration
	Hooks *hooks.HookConfig `json:"hooks,omitempty"`

	// Show frontmatter in markdown pane (default true)
	ShowFrontmatter *bool `json:"show_frontmatter,omitempty"`

	// Setup wizard dismissed state (false = show wizard after init)
	SetupWizardDismissed bool `json:"setup_wizard_dismissed,omitempty"`

	// Hide read items in feed/activity views (default false)
	HideRead bool `json:"hide_read,omitempty"`

	// Webapp theme: "light" or "dark" (default "dark")
	WebappTheme string `json:"webapp_theme,omitempty"`

	// Editor right panel mode: "preview", "help", or "browse" (default "preview")
	EditorPanelMode string `json:"editor_panel_mode,omitempty"`

	// Extra holds members this build does not model, so SaveConfig writes them
	// back: config.json is unsigned, and another build's setting must survive
	// this one's next settings save (config_extra.go).
	Extra map[string]json.RawMessage `json:"-"`
}

// DefaultLogRetentionDays is the default number of days to keep log files.
const DefaultLogRetentionDays = 7

// Server holds the application state
type Server struct {
	// AgentTokens checks a sign-in token an agent presents to this site's API
	// (polis.pub's sign-in server sets it per tenant); nil accepts only the
	// site's own API keys.
	AgentTokens func(r *http.Request, token string) (api.TokenGrant, bool)
	// ConnectionOff is told when the owner switches a connection off, after
	// its grant is withdrawn, so the host can revoke the connection's sign-in
	// tokens at once. nil where the host issues none.
	ConnectionOff func(ctx context.Context, grantID string)
	// SwitchedOff reports whether the host's operator has switched a feature
	// off, right now (api/switches.go). Asked on every gated request, never
	// copied: a tenant server is cached, so a value set at load would freeze.
	// nil means nothing is ever off.
	SwitchedOff func(name string) bool
	// Notices tells the owner, by the host's mail, that an agent was
	// connected or a passkey added or removed (notices.go). nil on a
	// self-hosted instance: nothing is sent there.
	Notices Notices

	keyReload keyReloadState // key file as last loaded; see keyreload.go

	DataDir      string
	CLIThemesDir string // Path to CLI themes directory (fallback for theme snippets)
	CLIVersion   string // CLI version for metadata files (set by bundled binary or from version.txt)
	Config       *Config
	PrivateKey   []byte
	PublicKey    []byte
	Logger       *Logger
	// LogOutput, when set, is where the logger's JSON lines go instead of
	// os.Stdout; Initialize hands it to the logger it makes or keeps.
	LogOutput        io.Writer
	LogLevel         int    // From .env LOG_LEVEL (default 1)
	logLevelSet      bool   // LOG_LEVEL was present and valid, so 0 means off, not unset
	LogRetentionDays int    // From .env LOG_RETENTION_DAYS (default 7)
	BaseURL          string // From POLIS_BASE_URL env var (runtime config, not stored in .well-known/polis)
	DiscoveryURL     string // From .env / env var DISCOVERY_SERVICE_URL (not stored in webapp/config.json)

	// Site is this server's site: where it is, and what it is stored on.
	//
	// ⭐ IT SITS ALONGSIDE DataDir DURING THE MIGRATION, deliberately. There
	// are ~600 uses of s.DataDir, and they convert WITH THEIR CALLEES — a call
	// site changes only when the package it calls takes a Site. ⛔ So this is
	// not 600 edits; it is two representations coexisting while the packages
	// beneath move, which is why internal/server converts LAST.
	//
	// ⚠️ Built on demand so a zero-valued Server in a test still works.
	siteOnce sync.Once
	siteV    storage.Site

	// devServer is set by Run from `--dev`, and nowhere else. See owner.Instance.Dev.
	devServer bool
	// siteConfigLoaded says Initialize found a valid site and read its
	// configuration. EnsureSite reads it again when it was not.
	siteConfigLoaded bool

	// backend is the storage backend this server's site is on, when it is
	// not the local filesystem. Set once, by UseBackend, before Site is used.
	backend storage.Backend

	// deployQueue publishes the site by deploying it, on Vercel; nil elsewhere.
	deployQueue *deploy.Queue

	// syncOnRequestFn replaces syncFeed in sync on request, and
	// syncOnRequestBudget its time budget; tests set them.
	syncOnRequestFn     func()
	syncOnRequestBudget time.Duration
	// deployAPIBase overrides the platform API, for tests.
	deployAPIBase string
	// releaseFunctionFor replaces the release fetch, for tests.
	releaseFunctionFor func(ctx context.Context, version string) (provision.FunctionManifest, func(context.Context, provision.DeployFile) ([]byte, error), error)
	// runningBootstrapSHA replaces the digest of the running executable, for
	// tests. Nil reads the executable itself, on Vercel only.
	runningBootstrapSHA func() (string, error)
	// deployGetenv is the environment the deploy queue was built with.
	deployGetenv func(string) string

	// deployStore is the site's store as the deploy queue sees it, counting
	// and timing every call, for the request timing log; nil without a queue.
	deployStore *timedStore
	// deployLiveBase overrides where the live site is read before a deploy,
	// for tests; the base URL otherwise.
	deployLiveBase string

	// started guards the deferred start of a request-credentialed backend.
	startMu sync.Mutex
	started bool

	// Mode is how this instance is deployed, and the SOURCE the published
	// capability descriptor is derived from.
	//
	// ⭐ An empty value means local, which keeps `polis serve` on a laptop
	// configuration-free — the overwhelmingly common case.
	//
	// ⛔ NOTHING READS THIS TO BRANCH ON BEHAVIOUR. Branch on a capability
	// (capability.For) instead: a mode is a closed set every consumer must
	// know all of, and a fifth one breaks anyone who enumerated four.
	Mode mode.Mode

	// BoundToLoopback records whether this process's listener is bound to a
	// loopback address.
	//
	// ⛔ IT IS SET ONCE FROM THE LISTENER AND NEVER FROM A REQUEST. The owner
	// seam's loopback provider rests entirely on this being unforgeable: a
	// header, RemoteAddr or X-Forwarded-For can each be controlled by a
	// caller, and deriving this per request would turn owner-trust from a
	// safety property into a universal bypass.
	BoundToLoopback bool

	// Recovery proves ownership for the first enrolment and after access is
	// lost. ⭐ Nil, or configured with no key, on local and custodied — neither
	// needs it: loopback answers on one, the operator fixes logins on the other.
	Recovery *owner.Recovery

	ownerStoreOnce sync.Once
	ownerStoreV    storage.Store

	// ⭐ Owner auth keeps NO state of its own in the server: sessions,
	// ceremonies and recovery challenges are sealed tokens the client holds.
	// What is here is a cache of store reads and the per-process limiters.
	ownerAuthOnce sync.Once
	ownerCacheV   *owner.Cache
	signInLimit   *attemptLimiter
	recoveryLimit *attemptLimiter

	// mcpKeys caches the keys file for /mcp; mcpFail counts /mcp requests
	// that failed to authenticate, per address (mcp.go).
	mcpKeys     mcpKeyCache
	mcpFailOnce sync.Once
	mcpFail     *attemptLimiter
	// mcpToolsets caches each key's generated tools (mcp.go).
	mcpToolsets mcpToolsetCache

	// v1 is the handler mounted at /v1/, recorded where it is mounted so /mcp
	// can call the site's API in-process (mcp.go). nil until mounted.
	v1Mu sync.Mutex
	v1   http.Handler

	// SharedHTTPClient is a process-level HTTP client using a shared transport
	// for connection pooling. Set by the hosted entry point; nil in localhost mode
	// (falls back to per-call clients).
	SharedHTTPClient *http.Client

	// ContentCache is a shared LRU cache for remote content fetches.
	// Set by the hosted entry point; nil in localhost mode (no caching).
	ContentCache *remote.LRUCache

	// EnableHooks controls whether post-action hooks (post-publish, post-republish,
	// post-comment) are allowed to execute. Defaults to false (safe for hosted/
	// multi-tenant). Localhost server sets this to true during startup.
	EnableHooks bool

	// Unified sync infrastructure
	syncHandlers   []stream.SyncHandler
	syncTrigger    chan struct{}             // non-blocking channel to trigger on-demand sync
	lastSyncAsked  atomic.Int64              // unix nanos of the last sync run or requested; see syncIfStale
	AuthorKeyCache *discovery.AuthorKeyCache // cached author public keys for signature verification

	// Policy check cache: key is "recipient:myDomain", value is cached result with expiry
	policyCache   map[string]*policyCacheEntry
	policyCacheMu sync.Mutex

	// dmKeyringMu serializes DM keyring mutations (set-password, rewrap) for this tenant.
	// SaveCAS guards the on-disk revision but has a documented TOCTOU between its read-back
	// and write (see dm/cas.go); the contract requires the server to serialize the whole
	// load→mutate→SaveCAS sequence per tenant. Each tenant has its own *Server, so a
	// per-Server mutex is exactly per-tenant (DM-8).
	dmKeyringMu sync.Mutex

	// RestrictDMEgress enables the outbound DM SSRF guard (DM-4): when true, DM send /
	// recipient-key / protection-status reject recipient URLs that aren't https to a safe
	// public host (no IPs, loopback, .internal, ports), and connect only to public
	// addresses once resolved (dm.PublicOnly). Set true by the hosted multi-tenant entry
	// point (a hostile tenant must not drive the shared server to internal hosts). When
	// false the rule still applies in every mode but local, where the owner runs on their
	// own machine and may DM a local instance (allowPrivateDMRecipients).
	RestrictDMEgress bool

	// Proxy names the platform proxy standing in front of this server, which
	// decides the header the client IP may be read from (see peerIP). ProxyFly
	// is set only by the hosted multi-tenant entry point, behind Fly's proxy;
	// ProxyVercel only by the network handler, behind Vercel's. Left ProxyNone
	// in localhost and self-hosted mode, where nothing guarantees a proxy and a
	// client could set the headers itself to slip past a per-IP limit; the IP
	// is then RemoteAddr.
	Proxy ProxyKind

	// RosiePaused is the hosted operator switch POLIS_ROSIE, seen from one
	// tenant's server (Signet epic 11 D12). While true, Rosie resolves no grant,
	// makes no decision and sends no marked request — a marked request to a
	// discovery service that predates the marker fails with 401. Settings →
	// Rosie still saves the user's choice and says she has not started.
	//
	// Always false outside hosting: self-hosted and localhost have no switch.
	RosiePaused bool

	// APIPointerEnabled is the switch APIPointerEnv, read at boot (self-hosted)
	// or set by the hosting service: whether this site publishes the `api`
	// pointer in .well-known/polis (EnsureAPIPointer).
	APIPointerEnabled bool

	// Cross-tenant comment-count cache: post URL → count + expiry.
	// Populated by populateCrossTenantCommentCounts in handlers_stream.go.
	// commentCountFlight collapses concurrent stream requests that share
	// overlapping URL sets into a single DS roundtrip (singleflight).
	commentCountCache   map[string]*commentCountCacheEntry
	commentCountCacheMu sync.Mutex
	commentCountFlight  commentCountSingleflight

	// Focus-comment cache + singleflight: post URL → resolved single-latest-
	// comment response. Backs GET /api/v1/stream/focus-comment (the stream's
	// read-focus mode, which shows exactly one comment per post).
	focusCommentCache   map[string]*focusCommentCacheEntry
	focusCommentCacheMu sync.Mutex
	latestCommentFlight focusCommentSingleflight

	// Shared feed caches — one CacheManager per scope, lazily created.
	// All code paths (background sync, manual refresh, counts) use these
	// to avoid file-level TOCTOU races from concurrent CacheManager instances.
	feedCaches  map[string]*feed.CacheManager
	feedCacheMu sync.Mutex

	// Mutuals-set memoization. computeMutualsSet runs O(N+M) over
	// following.json + the FollowerState projection per call; for
	// scope=my-mutuals requests at high frequency this is wasteful.
	// Cached value stays valid as long as both source files'
	// mtimes are unchanged AND the entry is within mutualsCacheTTL.
	mutualsCacheMu    sync.Mutex
	mutualsCacheValue map[string]bool
	mutualsCacheAt    time.Time
	mutualsOutboundMt time.Time // mtime of following.json when cached
	mutualsInboundMt  time.Time // mtime of pub.polis.follow.json when cached

	syncDone chan struct{} // closed by StopSync() to stop the background goroutine
}

// Site returns this server's site handle: where the site is, and what it is
// stored on.
//
// ⭐ THE ONE PLACE THE WEBAPP DECIDES WHAT ITS SITE IS STORED ON. A blob-backed
// instance constructs it with storage.On here, and nothing downstream changes.
//
// ⚠️ Built on demand rather than in Initialize, because tests construct a
// Server as a literal and would otherwise get a zero-valued handle.
func (s *Server) Site() storage.Site {
	s.siteOnce.Do(func() {
		if s.backend.Store != nil {
			s.siteV = storage.On(s.DataDir, s.backend.Store)
			return
		}
		s.siteV = storage.At(s.DataDir)
	})
	return s.siteV
}

// checkModeForBackend refuses a mode that would grant the wrong trust for where
// the site is stored.
//
// ⛔ AN UNUSABLE MODE STOPS THE BOOT, ON EVERY BACKEND. ResolveSelfHosted's
// fallback to local was meant for a typo in a developer's shell, but the same
// typo on a VPS behind a reverse proxy granted loopback owner-trust to everyone
// the proxy forwarded, and /api/download-site served the private key. An unset
// mode still runs as local: that is the laptop, and it needs no configuration.
// ⛔ UNLESS a setting only a remote instance has is present (remoteSignals, from
// remoteOnlySignals): then an unset mode is a deployer who forgot POLIS_MODE, and
// running as local would hand loopback owner-trust to whatever their proxy
// forwards. --dev does not exempt it — a dev server with a recovery key set is
// exactly the confused case. An explicit POLIS_MODE=local is a choice and runs.
// ⛔ And local trust is for a site on this machine's disk: a local mode with its
// site in a remote store is a misconfiguration, never a laptop. A dev server is
// exempt from that second rule only, since --dev already chose its mode.
func checkModeForBackend(m mode.Mode, modeNote string, unset, dev bool, backendKind string, remoteSignals []string) error {
	if modeNote != "" {
		return fmt.Errorf("%s — refusing to start: %s must be a valid mode, or unset to run as local",
			modeNote, capability.ModeEnv)
	}
	if unset && len(remoteSignals) > 0 {
		return fmt.Errorf("%s is unset, but %s is set: that is an instance reached over the network, where local "+
			"would grant loopback owner-trust to whatever reaches it — refusing to start: set %s "+
			"(remote-server-signed or remote-client-signed), or %s=local to run locally on purpose",
			capability.ModeEnv, strings.Join(remoteSignals, " and "), capability.ModeEnv, capability.ModeEnv)
	}
	deployed := backendKind != "" && backendKind != storage.BackendPOSIX
	if deployed && m == mode.Local && !dev {
		return fmt.Errorf("local, but %s=%s: a local instance keeps its site on this machine; set %s",
			storage.EnvStorageKind, backendKind, capability.ModeEnv)
	}
	return nil
}

// vercelRuntimeEnv is set to "1" by Vercel's runtime on every deployment.
const vercelRuntimeEnv = "VERCEL"

// remoteOnlySignals names the settings present in getenv that only a remote
// instance has: a recovery key (recovery is for an owner who cannot reach the
// instance on loopback) and Vercel's runtime marker. A non-POSIX POLIS_STORAGE
// is not listed: checkModeForBackend already refuses local on one.
func remoteOnlySignals(getenv func(string) string) []string {
	var set []string
	for _, name := range []string{owner.RecoveryKeyEnv, vercelRuntimeEnv} {
		if strings.TrimSpace(getenv(name)) != "" {
			set = append(set, name)
		}
	}
	return set
}

// UseBackend puts this server's site on b.
//
// ⛔ IT MUST RUN BEFORE ANYTHING ASKS FOR THE SITE, and it refuses otherwise:
// a handle built on the filesystem first and a backend set afterwards would
// leave part of the server writing to a disk the instance does not keep.
func (s *Server) UseBackend(b storage.Backend) error {
	if b.Store == nil {
		return errors.New("server: backend has no store")
	}
	set := false
	s.siteOnce.Do(func() {
		s.backend = b
		s.siteV = storage.On(s.DataDir, b.Store)
		set = true
	})
	if !set {
		return errors.New("server: the site was already in use before its backend was set")
	}
	return nil
}

// StartsOnRequest reports whether this server's site work waits for the first
// request, because its backend has no credential until a request delivers one.
func (s *Server) StartsOnRequest() bool { return s.backend.StartsOnRequest }

// backendMiddleware hands every request's credential to the backend and, for a
// backend that starts on request, starts the server on the first request.
//
// ⭐ The backend decides WHICH header carries its credential; this only passes
// the request's headers along. ⛔ It must be OUTERMOST: no handler may touch
// the site before the credential is offered and the start has run.
func (s *Server) backendMiddleware(next http.Handler) http.Handler {
	if s.backend.FromRequest == nil && !s.backend.StartsOnRequest {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.backend.FromRequest != nil {
			// ⛔ Refused outright: more than one value of the credential
			// header, or a credential that does not verify. ⚠️ Logged by
			// cause only; the token is never written down.
			// ⭐ EXCEPT a credential that could not be CHECKED (the issuer's
			// keys could not be loaded, or name a key not fetched yet): it is
			// not adopted, and the request continues on whatever credential the
			// backend already holds. On a platform that sends the credential
			// with every request, refusing here refused every public read for
			// as long as the issuer was unreachable.
			if err := s.backend.FromRequest(r.Header.Values); err != nil {
				if !errors.Is(err, storage.ErrVerifierUnavailable) {
					log.Printf("[warning] request credential refused: %v", err)
					http.Error(w, "credential refused", http.StatusBadRequest)
					return
				}
				log.Printf("[warning] request credential not checked, continuing on the held one: %v", err)
			}
		}
		if s.backend.StartsOnRequest {
			if err := s.startOnFirstRequest(r.Context()); err != nil {
				// ⛔ Not fatal: the next request may carry what this one lacked.
				// ⚠️ Logged by cause, never with the credential.
				log.Printf("[warning] deferred start failed, will retry on the next request: %v", err)
				http.Error(w, "this instance is starting; try again shortly", http.StatusServiceUnavailable)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// startOnFirstRequest runs the site work a POSIX server does at boot —
// Initialize, then the identity-key bootstrap — once, on the first request
// that can. ⭐ It retries until it succeeds rather than giving up, because the
// usual failure is "no credential yet", which the next request fixes.
func (s *Server) startOnFirstRequest(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.started {
		return nil
	}
	// ⭐ Probe the store before Initialize, so a missing credential fails
	// cleanly here instead of half-way through Initialize's reads.
	if _, err := s.Site().Store().Exists(ctx, site.WellKnownKey); err != nil {
		return fmt.Errorf("storage not reachable yet: %w", err)
	}
	s.Initialize()
	// ⛔ RE-BIND RECOVERY to the domain the started instance serves. At boot a
	// deferred server could not read the site's .env, so recovery was bound to
	// whatever the boot-time environment said; its challenges must name the
	// domain Initialize just read.
	if key := os.Getenv(owner.RecoveryKeyEnv); key != "" {
		if rec, err := owner.NewRecovery(key, s.GetBaseURL()); err == nil {
			s.Recovery = rec
		}
	}
	if err := s.EnsureIdentityKey(ctx); err != nil {
		return fmt.Errorf("identity key: %w", err)
	}
	s.EnsureSite(ctx)
	s.EnsureAPIPointer()
	s.deployIfNeverDeployed(ctx)
	// ⭐ The session key into memory now, so the sign-in that may have woken
	// this process costs no store operation of its own.
	s.prepareOwnerAuth()
	s.started = true
	return nil
}

// mutualsCacheTTL caps how long the cached mutuals set is trusted even
// when both source mtimes appear unchanged. A backstop against silent
// corruption / clock skew / external file mutation that mtime might
// miss. 60s balances request-load amortization against the worst-case
// staleness window for a single user editing their follows.
const mutualsCacheTTL = 60 * time.Second

// policyCacheEntry holds a cached policy check result with expiry.
type policyCacheEntry struct {
	Status    string
	Reason    string
	FollowsUs bool
	ExpiresAt time.Time
}

// policyCheckCacheTTL is how long policy check results are cached.
const policyCheckCacheTTL = 5 * time.Minute

// commentCountCacheEntry holds a cross-tenant post comment count with expiry.
// Populated by populateCrossTenantCommentCounts after the DS roundtrip.
type commentCountCacheEntry struct {
	Total     int
	ExpiresAt time.Time
}

// commentCountCacheTTL is how long an authoritative DS-sourced count is
// trusted before we re-fetch. "Thoughtfulness not reactivity" framing —
// a slightly stale count (±1) is fine; what matters is the stream never
// blocks on the count fetch. 30s amortizes scroll/refresh thrash without
// the user noticing staleness.
const commentCountCacheTTL = 30 * time.Second

// commentCountSingleflight collapses concurrent stream-handler calls
// that fetch overlapping URL sets into a single in-flight DS request.
// Inline equivalent of golang.org/x/sync/singleflight — kept inline to
// avoid adding a dependency for one call site (matches the
// replyFetchGroupT pattern in cli-go/pkg/render/page.go).
type commentCountFlightCall struct {
	wg  sync.WaitGroup
	val map[string]int
	err error
}

type commentCountSingleflight struct {
	mu       sync.Mutex
	inflight map[string]*commentCountFlightCall
}

// Do executes fn() exactly once per `key` while one is in flight;
// concurrent callers with the same key block on wg and receive the
// same (val, err) pair the in-flight call returned. The key is the
// caller-built representation of the URL set (typically a sorted,
// joined string). Returns (nil, nil) if fn is nil (defensive).
func (g *commentCountSingleflight) Do(key string, fn func() (map[string]int, error)) (map[string]int, error) {
	g.mu.Lock()
	if g.inflight == nil {
		g.inflight = make(map[string]*commentCountFlightCall)
	}
	if c, present := g.inflight[key]; present {
		g.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &commentCountFlightCall{}
	c.wg.Add(1)
	g.inflight[key] = c
	g.mu.Unlock()

	if fn != nil {
		c.val, c.err = fn()
	}

	g.mu.Lock()
	delete(g.inflight, key)
	g.mu.Unlock()
	c.wg.Done()

	return c.val, c.err
}

// Logger handles logging to files organized by date
type Logger struct {
	level          int
	logsDir        string
	retentionDays  int
	lastPruneAt    time.Time
	file           *os.File
	mu             sync.Mutex
	jsonOutput     bool            // emit JSON to stdout when true (LOG_FORMAT=json)
	disabledEvents map[string]bool // LOG_EVENTS_DISABLED (see EventDisabled)
	// stdoutOnly: no log file at all, every line goes to stdout as JSON. For a
	// site whose storage is not this machine's disk (see NewStdoutLogger).
	stdoutOnly bool
	// out is where the JSON lines go; nil means os.Stdout. A host that
	// collects its tenants' lines (and a test that reads them) sets it with
	// SetOutput, never by replacing os.Stdout, which every goroutine shares.
	out io.Writer
}

// SetOutput sends the logger's JSON lines to w instead of os.Stdout (nil
// restores os.Stdout).
func (l *Logger) SetOutput(w io.Writer) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.out = w
}

// stdout is where a JSON line goes. The caller holds l.mu.
func (l *Logger) stdout() io.Writer {
	if l.out != nil {
		return l.out
	}
	return os.Stdout
}

// NewLogger creates a new logger with the given level and logs directory
func NewLogger(level int, logsDir string) *Logger {
	return &Logger{
		level:   level,
		logsDir: logsDir,
	}
}

// NewStdoutLogger creates a logger that writes nothing to disk: every line
// goes to stdout as JSON, which the platform captures.
//
// ⛔ For a site on a remote backend. Its local disk is not where the site
// lives and may not outlive the instance, and the rotating file is appended to
// per request, which an object store cannot take.
func NewStdoutLogger(level int) *Logger {
	return &Logger{level: level, jsonOutput: true, stdoutOnly: true}
}

// SetRetentionDays sets the number of days to keep log files for pruning.
func (l *Logger) SetRetentionDays(days int) {
	if l != nil {
		l.retentionDays = days
	}
}

// getLogFile returns the log file for today, creating it if necessary
func (l *Logger) getLogFile() (*os.File, error) {
	if l.level == LogLevelOff || l.stdoutOnly {
		return nil, nil
	}

	today := time.Now().Format("2006-01-02")
	logPath := filepath.Join(l.logsDir, today+".log")

	// Create logs directory if it doesn't exist (.polis/logs — restricted)
	// NOT the storage interface: this creates the log directory itself, which
	// may stay empty until something is logged, and a prefix store cannot
	// represent an empty directory.
	if err := os.MkdirAll(l.logsDir, 0700); err != nil {
		return nil, err
	}

	// Check if we need to open a new file (date changed)
	if l.file != nil {
		// Check if the current file is for today
		info, err := l.file.Stat()
		if err == nil && strings.HasPrefix(info.Name(), today) {
			return l.file, nil
		}
		// Close old file
		_ = l.file.Close() // yesterday's log file, being replaced
	}

	// Open new log file.
	//
	// NOT the storage interface, and ⭐ THIS STAYS ON PURPOSE. A rotating log
	// is appended to once per request; on a backend with no append that would
	// be a read-modify-write of the whole file per line — an object write per
	// log entry, growing without bound. ⭐ A deployment whose storage cannot
	// take this logs to STDOUT, which the platform captures; hosted already
	// ships stdout to its pipeline. A different answer, not a missing
	// conversion.
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600) //nolint:gosec // G304: the server's own log file under the site
	if err != nil {
		return nil, err
	}
	l.file = file
	return file, nil
}

// log writes a message to the log file and optionally emits JSON to stdout.
// DEBUG-level messages are excluded from stdout to keep volume manageable.
func (l *Logger) log(level int, prefix string, format string, args ...interface{}) {
	if l == nil || l.level < level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	message := fmt.Sprintf(format, args...)

	// Always write to disk
	file, err := l.getLogFile()
	if err == nil && file != nil {
		timestamp := now.Format("2006-01-02 15:04:05")
		fmt.Fprintf(file, "%s [%s] %s\n", timestamp, prefix, message)
	}

	// Emit JSON to stdout when enabled (skip DEBUG to keep volume manageable)
	if l.jsonOutput && prefix != "DEBUG" {
		obj := map[string]interface{}{
			"ts":     now.UTC().Format(time.RFC3339),
			"source": "webapp",
			"level":  strings.ToLower(prefix),
			"msg":    message,
		}
		data, err := json.Marshal(obj)
		if err == nil {
			fmt.Fprintln(l.stdout(), string(data))
		}
	}
}

// Info logs informational messages (level 1)
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LogLevelBasic, "INFO", format, args...)
}

// Warn logs warning messages (level 1)
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LogLevelBasic, "WARN", format, args...)
}

// Error logs error messages (level 1)
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LogLevelBasic, "ERROR", format, args...)
}

// Debug logs debug messages (level 2)
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LogLevelVerbose, "DEBUG", format, args...)
}

// Event logs a structured event to disk and optionally emits JSON to stdout.
// Event names use the pub.polis.* namespace for cross-system consistency.
//
// ⭐ `pub.polis.security.*` events go out on `source: security`, where the
// security dashboards look for them (R24-18); everything else on `webapp`.
func (l *Logger) Event(event string, fields map[string]interface{}) {
	source := "webapp"
	if strings.HasPrefix(event, "pub.polis.security.") {
		source = "security"
	}
	l.EventWithSource(source, event, fields)
}

// EventWithSource is Event with an explicit `source`. Use it only for a source
// the shared schema already defines — today `security`, for events an operator
// must see on the security stream (Signet epic 11's agent_act_without_grant).
func (l *Logger) EventWithSource(source, event string, fields map[string]interface{}) {
	if l == nil || l.level < LogLevelBasic {
		return
	}

	if EventDisabled(l.disabledEvents, event) {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	// Write to disk in plain text format
	file, err := l.getLogFile()
	if err == nil && file != nil {
		timestamp := now.Format("2006-01-02 15:04:05")
		var pairs []string
		for k, v := range fields {
			pairs = append(pairs, fmt.Sprintf("%s=%v", k, v))
		}
		sort.Strings(pairs)
		fmt.Fprintf(file, "%s [EVENT:%s] %s\n", timestamp, event, strings.Join(pairs, " "))
	}

	// Emit JSON to stdout when enabled. Schema is standardized across
	// the stack as of the v4 cutover:
	//   - source="webapp" for application events emitted by this Logger
	//     (hosted.go uses source="landlord" for hosted-service events;
	//     middleware.go's HTTP request log uses source="http"; DS uses
	//     source="api"/"admin"/... in its own log line).
	//   - action=<name> matches the DS schema. Older builds emitted
	//     "event": <name>; dashboards filtering on `action` now match
	//     across the entire stack.
	if l.jsonOutput {
		obj := map[string]interface{}{
			"ts":     now.UTC().Format(time.RFC3339),
			"source": source,
			"action": event,
		}
		for k, v := range fields {
			// ⛔ THE SCHEMA FIELDS ARE NOT THE CALLER'S TO SET. `action` is the
			// event name, `source` says which component logged it and `ts` when
			// — a caller field of any of those names used to overwrite the real
			// one (C19, R1-6), so a record could claim a source it did not come
			// from. The caller's value is kept under `context_*`.
			if schemaLogFields[k] {
				k = "context_" + k
			}
			obj[k] = v
		}
		data, err := json.Marshal(obj)
		if err == nil {
			fmt.Fprintln(l.stdout(), string(data))
		}
	}
}

// schemaLogFields are the base-schema keys a caller may not set. Hosted's
// logEvent stamps the same three after copying the caller's fields.
var schemaLogFields = map[string]bool{"action": true, "source": true, "ts": true}

// EventDisabled reports whether LOG_EVENTS_DISABLED silences event. An entry
// matches when it EQUALS the event name or is a DOTTED PREFIX of it at any
// depth: `judge` silences everything Judge emits, `judge.alert` only its
// alerts, `judge.sweep` only that one event. A pub.polis.* name is also
// matched with that prefix removed, so the bare categories this filter has
// always taken (`feed` for pub.polis.feed.refresh) keep working. Both loggers
// — this one and hosted's logEvent — use it, so one value means one thing.
func EventDisabled(disabled map[string]bool, event string) bool {
	if len(disabled) == 0 {
		return false
	}
	if prefixDisabled(disabled, event) {
		return true
	}
	const prefix = "pub.polis."
	return strings.HasPrefix(event, prefix) && prefixDisabled(disabled, event[len(prefix):])
}

// prefixDisabled tests name and each of its dotted prefixes, longest first.
func prefixDisabled(disabled map[string]bool, name string) bool {
	for name != "" {
		if disabled[name] {
			return true
		}
		dot := strings.LastIndex(name, ".")
		if dot < 0 {
			return false
		}
		name = name[:dot]
	}
	return false
}

// ParseDisabledEvents parses LOG_EVENTS_DISABLED: a comma-separated list of
// event names or dotted prefixes (see EventDisabled).
func ParseDisabledEvents(s string) map[string]bool {
	m := make(map[string]bool)
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry != "" {
			m[entry] = true
		}
	}
	return m
}

// Request logs an HTTP request as structured JSON (both disk and stdout).
// This is the per-request access log used for cross-boundary tracing.
func (l *Logger) Request(method, path string, status int, duration time.Duration, requestID string) {
	if l == nil || l.level < LogLevelBasic {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	durationMs := duration.Milliseconds()

	// Write to disk
	file, err := l.getLogFile()
	if err == nil && file != nil {
		timestamp := now.Format("2006-01-02 15:04:05")
		fmt.Fprintf(file, "%s [HTTP] %s %s %d %dms request_id=%s\n", timestamp, method, path, status, durationMs, requestID)
	}

	// Emit JSON to stdout when enabled
	if l.jsonOutput {
		obj := map[string]interface{}{
			"ts":          now.UTC().Format(time.RFC3339),
			"source":      "webapp",
			"request_id":  requestID,
			"method":      method,
			"path":        path,
			"status":      status,
			"duration_ms": durationMs,
		}
		data, err := json.Marshal(obj)
		if err == nil {
			fmt.Fprintln(l.stdout(), string(data))
		}
	}
}

// Close closes the log file
func (l *Logger) Close() {
	if l != nil && l.file != nil {
		_ = l.file.Close() // at shutdown; nothing left to log to
	}
}

// PruneLogs deletes log files older than retentionDays.
// Short-circuits if less than 24h since last prune. Nil-safe.
func (l *Logger) PruneLogs() {
	if l == nil || l.retentionDays <= 0 || l.stdoutOnly {
		return
	}

	l.mu.Lock()
	if !l.lastPruneAt.IsZero() && time.Since(l.lastPruneAt) < 24*time.Hour {
		l.mu.Unlock()
		return
	}
	l.lastPruneAt = time.Now()
	l.mu.Unlock()

	logStore := storage.NewPOSIX(l.logsDir)
	entries, err := logStore.List(context.Background(), "")
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -l.retentionDays)
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(e.Key, ".log") {
			continue
		}
		// ⚠️ Depends on ModTimes: log rotation is decided by age, so a
		// backend that cannot report one would never rotate.
		info, err := logStore.Stat(context.Background(), e.Key)
		if err != nil {
			continue
		}
		if info.ModTime.Before(cutoff) {
			_ = logStore.Delete(context.Background(), e.Key, storage.IgnoreMissing())
		}
	}
}

// Server logging helpers
func (s *Server) LogInfo(format string, args ...interface{}) {
	if s.Logger != nil {
		s.Logger.Info(format, args...)
	}
}

func (s *Server) LogWarn(format string, args ...interface{}) {
	if s.Logger != nil {
		s.Logger.Warn(format, args...)
	}
}

func (s *Server) LogError(format string, args ...interface{}) {
	if s.Logger != nil {
		s.Logger.Error(format, args...)
	}
}

func (s *Server) LogDebug(format string, args ...interface{}) {
	if s.Logger != nil {
		s.Logger.Debug(format, args...)
	}
}

// LogEvent logs a structured event. Nil-safe delegate to Logger.Event().
func (s *Server) LogEvent(event string, fields map[string]interface{}) {
	if s.Logger != nil {
		s.Logger.Event(event, withSiteField(fields, s.GetBaseURL()))
	}
}

// withSiteField adds "site", the site's domain, to an event's fields unless
// the event names one itself. ⭐ On polis.pub every tenant's server logs to
// the same stream, and without it a tenant's pub.polis.post.publish could be
// told apart from another's only by joining on the request log. The caller's
// map is not changed.
func withSiteField(fields map[string]interface{}, baseURL string) map[string]interface{} {
	domain := extractDomainFromURL(baseURL)
	if domain == "" {
		return fields
	}
	if _, ok := fields["site"]; ok {
		return fields
	}
	out := make(map[string]interface{}, len(fields)+1)
	for k, v := range fields {
		out[k] = v
	}
	out["site"] = domain
	return out
}

// NewDSClient creates a discovery client with request ID propagation and timing.
// Use for handlers that have an http.Request context.
// IsRegisteredWithDS checks whether this site has a local registration marker
// for the configured discovery service. Pure filesystem check — no network call.
func (s *Server) IsRegisteredWithDS() bool {
	return discovery.IsRegisteredLocally(s.Site(), s.DiscoveryURL)
}

func (s *Server) NewDSClient(r *http.Request) *discovery.Client {
	var client *discovery.Client
	if s.SharedHTTPClient != nil {
		client = discovery.NewClientWithHTTP(s.DiscoveryURL, s.SharedHTTPClient)
	} else {
		client = discovery.NewClient(s.DiscoveryURL)
	}
	if r != nil {
		client.RequestID = RequestIDFromContext(r.Context())
	}
	s.setDSLogger(client)
	return client
}

// NewAuthDSClient creates an authenticated discovery client with request ID propagation.
func (s *Server) NewAuthDSClient(r *http.Request, domain string) *discovery.Client {
	var client *discovery.Client
	if s.SharedHTTPClient != nil {
		client = discovery.NewAuthenticatedClientWithHTTP(s.DiscoveryURL, domain, s.PrivateKey, s.SharedHTTPClient)
	} else {
		client = discovery.NewAuthenticatedClient(s.DiscoveryURL, domain, s.PrivateKey)
	}
	if r != nil {
		client.RequestID = RequestIDFromContext(r.Context())
	}
	s.setDSLogger(client)
	return client
}

// NewReadDSClient returns a client for READ-only DS calls. The DS allows
// anonymous reads on stream/content endpoints, so an unregistered
// localhost tenant can still sync without dragging in auth verification
// (which would fail anyway — the DS does a live well-known fetch of the
// caller's domain on every authenticated request, and a localhost tenant
// is not publicly reachable).
//
// Behavior:
//   - If the tenant IS registered locally with the configured DS, returns
//     an authenticated client (auth headers attached). Defense-in-depth:
//     registered sites attest their domain on reads too.
//   - If NOT registered, returns an unauthenticated client. The bare
//     request still leaks "this tenant is interested in X" to the DS,
//     which is unavoidable when you need data from the DS.
//
// For WRITE calls (publish, register, follow/unfollow event emission),
// use NewAuthDSClient directly — those must always sign, and the DS
// rejection on an unregistered site is the right failure mode.
func (s *Server) NewReadDSClient(r *http.Request) *discovery.Client {
	if s.IsRegisteredWithDS() {
		myDomain := discovery.ExtractDomainFromURL(s.GetBaseURL())
		if myDomain != "" {
			return s.NewAuthDSClient(r, myDomain)
		}
	}
	return s.NewDSClient(r)
}

// setDSLogger configures DS roundtrip timing logging on a discovery client.
func (s *Server) setDSLogger(client *discovery.Client) {
	client.Logger = func(method, path string, durationMs int64, requestID string) {
		s.LogEvent("pub.polis.ds_roundtrip", map[string]interface{}{
			"method":      method,
			"path":        path,
			"duration_ms": durationMs,
			"request_id":  requestID,
		})
	}
}

// NewRemoteClient creates a remote content client using the shared HTTP client
// and content cache if available.
func (s *Server) NewRemoteClient() *remote.Client {
	rc := remote.NewClientWithHTTP(s.SharedHTTPClient)
	rc.Cache = s.ContentCache
	return rc
}

// GetBaseURL returns the site's base URL from POLIS_BASE_URL environment variable.
// This matches the bash CLI behavior - base_url is runtime config, not stored in .well-known/polis.
func (s *Server) GetBaseURL() string {
	// Return cached value from LoadEnv()
	return s.BaseURL
}

// DiscoveryConfig returns a per-instance discovery config for use with
// publish.PublishPost/RepublishPost. This avoids relying on package-level
// globals which are unsafe in multi-tenant (hosted) mode where each tenant
// has a different BaseURL.
func (s *Server) DiscoveryConfig() *publish.DiscoveryConfig {
	if s.DiscoveryURL == "" || s.BaseURL == "" {
		return nil
	}
	return &publish.DiscoveryConfig{
		DiscoveryURL: s.DiscoveryURL,
		BaseURL:      s.BaseURL,
		Generator:    "polis-cli-go/" + s.CLIVersion,
	}
}

// FollowConfig returns a per-instance follow config for
// following.FollowWithBlessing / UnfollowWithDenial. Like DiscoveryConfig(),
// this keeps the follow.announced/removed stream events off the multi-tenant-
// unsafe package globals: in the hosted process every tenant's Initialize()
// overwrites stream.BaseURL/DataDir, so an interactive follow that relied on
// globals would announce under the wrong (or empty) domain and get silently
// suppressed. Returns nil when DS isn't configured (single-tenant CLI fallback).
func (s *Server) FollowConfig() *following.FollowConfig {
	if s.DiscoveryURL == "" || s.BaseURL == "" {
		return nil
	}
	// No DataDir: following checks and announces from the site handle it is
	// given, which is s.Site() at every call.
	return &following.FollowConfig{
		DiscoveryURL: s.DiscoveryURL,
		BaseURL:      s.BaseURL,
	}
}

// loadOrDefaultBundle loads the site's bundle.json if present, otherwise returns
// the default pub.polis.core bundle.
func (s *Server) loadOrDefaultBundle() *bundle.Bundle {
	if b, err := bundle.LoadBundleAt(s.Site(), "content/pub.polis.core/bundle.json"); err == nil {
		return b
	}
	return bundle.DefaultCoreBundle()
}

// newContentEngine creates an ops.Engine for the v1 content API.
func (s *Server) newContentEngine() (*ops.Engine, error) {
	coreBundle := s.loadOrDefaultBundle()
	bundles := map[string]*bundle.Bundle{
		"pub.polis.core": coreBundle,
	}
	resolver := resolve.New(s.Site(), bundles)

	return ops.NewEngine(ops.EngineConfig{
		Resolver:   resolver,
		Bundles:    bundles,
		PrivateKey: s.PrivateKey,
		// ⭐ At call time: the engine is built during route setup, before a
		// deferred start loads the key, and it must also follow a rotation.
		Keys:         func() ([]byte, []byte) { return s.PrivateKey, s.PublicKey },
		PublicKey:    s.PublicKey,
		BaseURL:      s.GetBaseURL(),
		Capabilities: capability.For(s.effectiveMode()),
		DiscoveryURL: s.DiscoveryURL,
		CLIThemesDir: s.CLIThemesDir,
		EmitEvent:    s.LogEvent,
		// ⭐ What a write causes (render, the owner's hook, the draft it came
		// from) is the shared after-write module's, the same for every host.
		// The outbound address rule for messages (ops/dm_send.go): a private
		// recipient only on the owner's own machine.
		DMHTTPClient:             s.SharedHTTPClient,
		AllowPrivateDMRecipients: s.allowPrivateDMRecipients(),
		// Built per write, so a hook configured in settings since start applies.
		AfterWrite: func(ctx context.Context, w ops.Written) { s.afterWrite().Apply(ctx, w) },
	})
}

// PublishAsOwner publishes a post in the owner's own name through the action
// layer: the same operation /v1 serves (post.create), with its render and side
// effects, unmarked because no agent is acting. ⭐ For a caller inside the
// deployment that has no HTTP request to make, such as polis.pub's signup,
// which publishes the post a new owner wrote before confirming their email.
// It answers the post's content path.
func (s *Server) PublishAsOwner(ctx context.Context, markdown string) (string, error) {
	engine, err := s.newContentEngine()
	if err != nil {
		return "", err
	}
	res, err := engine.Dispatch(ctx, ops.ActionRequest{
		Action:      "create",
		ContentType: "pub.polis.post",
		Payload:     map[string]any{"markdown": markdown},
	})
	if err != nil {
		return "", err
	}
	path, _ := res.Data["path"].(string)
	return path, nil
}

// afterWrite is this server's after-write module. Hooks run only where the
// webapp has them on (EnableHooks; never on hosted).
func (s *Server) afterWrite() *ops.AfterWrite {
	return ops.NewAfterWrite(ops.AfterWriteConfig{
		Site:        s.Site(),
		NewRenderer: func() (ops.SiteRenderer, error) { return s.newPageRenderer() },
		Hooks:       s.getHookConfig(),
		HooksOn:     s.EnableHooks,
		Emit:        s.LogEvent,
		Warn:        func(format string, args ...any) { s.LogWarn(format, args...) },
	})
}

// newPageRenderer is a renderer for this site, reading through the render's
// read cache.
func (s *Server) newPageRenderer() (*render.PageRenderer, error) {
	coreBundle := s.loadOrDefaultBundle()
	postsSource, _ := coreBundle.ContentDir("pub.polis.post")
	postsMountDir, _ := coreBundle.MountDir("pub.polis.post")
	commentsSource, _ := coreBundle.ContentDir("pub.polis.comment")
	commentsMountDir, _ := coreBundle.MountDir("pub.polis.comment")
	return render.NewPageRenderer(render.PageConfig{
		Site:              s.renderSite(),
		CLIThemesDir:      s.CLIThemesDir,
		BaseURL:           s.GetBaseURL(),
		PostsSourceDir:    postsSource,
		PostsMountDir:     postsMountDir,
		CommentsSourceDir: commentsSource,
		CommentsMountDir:  commentsMountDir,
	})
}

// RenderSite renders all pages after publish/republish operations.
// This ensures HTML files are updated and hooks can act on the complete output.
// renderSite is the site as one render sees it: its reads remembered for
// the render (storage.ReadCache), its writes going straight through. ⭐ A
// render reads the same templates, snippets and theme files for every page;
// on a remote store each read is a round trip, and a three-post publish made
// 216 store calls. ⛔ A fresh one per render, never kept.
func (s *Server) renderSite() storage.Site {
	site := s.Site()
	return site.WithStore(storage.ReadCache(site.Store()))
}

func (s *Server) RenderSite() error {
	// Get site base URL from POLIS_BASE_URL env var (matches bash CLI behavior)
	baseURL := s.GetBaseURL()

	// Load bundle for source/mount path resolution
	coreBundle := s.loadOrDefaultBundle()
	postsSource, _ := coreBundle.ContentDir("pub.polis.post")
	postsMountDir, _ := coreBundle.MountDir("pub.polis.post")
	commentsSource, _ := coreBundle.ContentDir("pub.polis.comment")
	commentsMountDir, _ := coreBundle.MountDir("pub.polis.comment")

	// Create page renderer
	renderer, err := render.NewPageRenderer(render.PageConfig{
		Site:              s.renderSite(),
		CLIThemesDir:      s.CLIThemesDir,
		BaseURL:           baseURL,
		RenderMarkers:     false, // No markers needed for publish flow
		PostsSourceDir:    postsSource,
		PostsMountDir:     postsMountDir,
		CommentsSourceDir: commentsSource,
		CommentsMountDir:  commentsMountDir,
	})
	if err != nil {
		s.LogError("Failed to create renderer: %v", err)
		return fmt.Errorf("failed to create renderer: %w", err)
	}

	// Render all pages
	stats, err := renderer.RenderAll(true)
	if err != nil {
		s.LogError("Render failed: %v", err)
		return fmt.Errorf("render failed: %w", err)
	}

	s.LogEvent("pub.polis.site.render", map[string]interface{}{
		"mode":     ops.RenderModeFull,
		"posts":    stats.PostsRendered,
		"comments": stats.CommentsRendered,
	})
	return nil
}

// RenderSiteAfterPublish dispatches the post-publish render. For stream-shape tenants
// it uses the incremental cascade (renderer.PublishStream) — bounded work per
// publish instead of the full-corpus RenderAll. For blog-shape tenants it falls back
// to RenderSite (full corpus) — unchanged from prior behavior.
//
// postSourceRelPath is the canonical content path returned by
// publish.PublishPost / RepublishPost (e.g.
// "content/pub.polis.core/post/YYYYMMDD/slug.md").
func (s *Server) RenderSiteAfterPublish(postSourceRelPath string) error {
	// ⭐ The choice (incremental where the shape supports it, full otherwise,
	// full again if the shortcut fails) is the action layer's, shared with
	// every /v1 write: ops.RenderAfterChange.
	renderer, err := s.newPageRenderer()
	if err != nil {
		s.LogError("renderer init failed: %v", err)
		return fmt.Errorf("create renderer: %w", err)
	}
	mode, stats, err := ops.RenderAfterChange(s.Site(), renderer, postSourceRelPath)
	if err != nil {
		s.LogError("render after publish failed: %v", err)
		return fmt.Errorf("render: %w", err)
	}
	fields := map[string]interface{}{"mode": mode, "path": postSourceRelPath}
	if stats != nil {
		fields["posts"] = stats.PostsRendered
		fields["comments"] = stats.CommentsRendered
	}
	s.LogEvent("pub.polis.site.render", fields)
	return nil
}

// LoadConfig loads the webapp configuration from webapp/config.json
func (s *Server) LoadConfig() {
	configPath := siteKey(".polis", "webapp", "config.json")
	data, err := s.readKey(configPath)
	if err != nil {
		return // Config doesn't exist yet
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return
	}
	s.Config = &config
}

// getHookConfig returns the hook configuration if hooks are enabled, nil otherwise.
// This centralizes the EnableHooks guard so callsites don't need to check individually.
func (s *Server) getHookConfig() *hooks.HookConfig {
	if !s.EnableHooks {
		return nil
	}
	if s.Config != nil {
		return s.Config.Hooks
	}
	return nil
}

// SaveConfig saves the webapp configuration to webapp/config.json
func (s *Server) SaveConfig() error {
	// No MkdirAll: Put creates parent prefixes.
	// Clear deprecated fields before saving (don't persist them)
	savedSubdomain := s.Config.Subdomain
	s.Config.Subdomain = ""
	data, err := json.MarshalIndent(s.Config, "", "  ")
	s.Config.Subdomain = savedSubdomain // Restore in memory for runtime use
	if err != nil {
		return err
	}
	return s.Site().Store().Put(context.Background(), siteKey(".polis", "webapp", "config.json"), data)
}

// LoadKeys loads the private and public keys from the site's store.
//
// ⭐ Through the store, so an instance whose site lives in Blob loads the key
// it generated there. ⛔ Absent is silent (a site before init has none); any
// other failure is logged by KEY, never with the bytes.
func (s *Server) LoadKeys() {
	st := s.Site().Store()
	ctx := context.Background()
	priv, err := st.Get(ctx, site.PrivateKeyKey)
	if err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			log.Printf("[warning] Could not read private key %s: %v", site.PrivateKeyKey, err)
		}
		return
	}
	pub, err := st.Get(ctx, site.PublicKeyKey)
	if err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			log.Printf("[warning] Could not read public key %s: %v", site.PublicKeyKey, err)
		}
		return
	}
	s.PrivateKey = priv
	s.PublicKey = pub
	s.stampKeyFile()
}

// EnsureIdentityKey gives a server-signed instance its identity key, generating
// it into the site's own store on first boot, and loads it.
//
// ⭐ It does nothing for any other mode: a client-signed instance holds no
// key (the owner signs), `polis init` makes a local one, and the operator
// provisions a custodied tenant's. ⛔ Called once the Mode is known, because
// the mode is what decides.
func (s *Server) EnsureIdentityKey(ctx context.Context) error {
	if !site.NeedsIdentityKey(capability.For(s.effectiveMode())) {
		return nil
	}
	k, err := site.EnsureIdentityKey(ctx, s.Site())
	if err != nil {
		return err
	}
	if k.Created {
		log.Printf("[i] generated this instance's identity key at %s", site.PrivateKeyKey)
	}
	s.LoadKeys()
	return nil
}

// EnsureSite writes a server-signed instance's site around the identity key
// it already holds, the first time it starts with no .well-known/polis.
//
// ⭐ Only remote-server-signed: the key existed before any site did, so
// nothing else would ever publish it. A client-signed instance holds no key;
// `polis init` makes a local site, and the operator a custodied tenant's.
//
// ⛔ Nothing is stated on the owner's behalf (no licence, no registration),
// and a site with no domain is never written: with no base URL it logs the
// cause and the next start tries again.
//
// ⚠️ NOT FATAL. The instance still serves what it has, and a later start
// retries: the claim inside site.Init makes a retry, or a racing start, write
// nothing twice. A site whose claiming start stopped part way is FINISHED by a
// start that finds it after site.Init's resume window; one found sooner is
// logged as still being written.
func (s *Server) EnsureSite(ctx context.Context) {
	if s.effectiveMode() != mode.RemoteServerSigned {
		return
	}
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		log.Printf("[warning] site not initialised: POLIS_BASE_URL is not set, and a site needs its domain; it will be tried on the next start")
		return
	}
	res, err := site.Init(s.Site(), site.InitOptions{
		AdoptExistingKey: true,
		BaseURL:          baseURL,
		Generator:        "polis-cli-go/" + s.CLIVersion,
	})
	switch {
	case err == nil && res.Resumed:
		log.Printf("[warning] finished this instance's site, which an earlier start claimed and left part-written")
		s.LogEvent("pub.polis.site.init", map[string]interface{}{"adopted_key": true, "resumed": true})
		s.Initialize()
		s.LoadKeys()
		s.renderIfNoFrontPage(ctx)
	case err == nil:
		log.Printf("[i] initialised this instance's site around its identity key")
		s.LogEvent("pub.polis.site.init", map[string]interface{}{"adopted_key": true})
		// ⭐ The configuration read at start predates the site; read it again.
		s.Initialize()
		s.LoadKeys()
		s.renderIfNoFrontPage(ctx)
	case errors.Is(err, site.ErrInitInProgress):
		log.Printf("[i] this instance's site is still being written by another start; a start after the resume window finishes it if that one stopped")
	case errors.Is(err, site.ErrAlreadyInitialised):
		// ⭐ A start that lost the race to another ran Initialize before the
		// site existed, so its configuration was never read.
		if !s.siteConfigLoaded {
			s.Initialize()
			s.LoadKeys()
		}
		s.renderIfNoFrontPage(ctx)
	case errors.Is(err, site.ErrPartialSite):
		log.Printf("[warning] the site is only partly initialised and is served as it is; finishing it is not automatic: %v", err)
	default:
		log.Printf("[error] site not initialised; it will be tried on the next start: %v", err)
	}
}

// renderIfNoFrontPage renders a server-signed instance's site when it has no
// index.html, as hosted renders a tenant at signup.
//
// ⭐ On Vercel the front page is a static file of the deployment, so a site
// that was written but never rendered has no front page to deploy.
// ⚠️ Not fatal, and only while there is no front page: a site with one is never
// re-rendered here. Every rendered page is derived output, so two starts
// rendering at once write the same bytes.
func (s *Server) renderIfNoFrontPage(ctx context.Context) {
	if ok, err := s.Site().Store().Exists(ctx, "index.html"); err != nil || ok {
		return
	}
	if err := s.RenderSite(); err != nil {
		log.Printf("[warning] the site has no front page and rendering it failed; it will be tried on the next start: %v", err)
		return
	}
	log.Printf("[i] rendered this instance's site, which had no front page")
}

// effectiveMode is Mode with empty read as local, the documented default.
func (s *Server) effectiveMode() mode.Mode {
	if s.Mode == "" {
		return mode.Local
	}
	return s.Mode
}

// envKeys are the settings LoadEnv reads. ⚠️ A new one is added HERE, or it
// reads only from .env and the process environment is silently ignored for it.
var envKeys = []string{"POLIS_BASE_URL", "DISCOVERY_SERVICE_URL", "LOG_LEVEL", "LOG_RETENTION_DAYS"}

// LoadEnv reads the .env file and applies discovery service settings.
// This matches the bash CLI behavior where DISCOVERY_SERVICE_URL is read from
// the .env file rather than being stored in config.json.
//
// Search order:
// 1. Data directory .env (where the polis site data lives)
// 2. Current working directory .env (user's polis site)
// 3. ~/.polis/.env (fallback for multi-site setups)
func (s *Server) LoadEnv() {
	var envPath string
	var data []byte
	var err error

	// First, try the site (where webapp/config.json and keys live)
	data, err = s.readKey(".env")
	if err == nil {
		log.Printf("[i] Loaded .env from data directory: %s", filepath.Join(s.DataDir, ".env"))
	}

	// If not found, try current working directory
	if err != nil {
		cwd, cwdErr := os.Getwd()
		if cwdErr == nil {
			// NOT the storage interface: the working directory is not the
			// site.
			envPath = filepath.Join(cwd, ".env")
			data, err = os.ReadFile(envPath) //nolint:gosec // G304: .env in the working directory
			if err == nil {
				log.Printf("[i] Loaded .env from current directory: %s", envPath)
			}
		}
	}

	// If still not found, try ~/.polis/.env as fallback
	if err != nil {
		homeDir, homeErr := os.UserHomeDir()
		if homeErr == nil {
			// NOT the storage interface: the home directory is not the site.
			envPath = filepath.Join(homeDir, ".polis", ".env")
			data, err = os.ReadFile(envPath) //nolint:gosec // G304: .env in the user's home
			if err == nil {
				log.Printf("[i] Loaded .env from fallback: %s", envPath)
			}
		}
	}

	// ⛔ NOT FOUND IS NOT "NOTHING TO DO". The process environment still
	// applies below — on a platform (Vercel, Fly) there is never a .env file,
	// and returning here is how a remote instance used to boot with no base URL.
	if err != nil {
		data = nil
	}

	// Parse .env file (simple KEY=VALUE format, one per line)
	env := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Parse KEY=VALUE (handle quoted values)
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// Remove surrounding quotes if present
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		env[key] = value
	}

	// ⭐ THE PROCESS ENVIRONMENT WINS OVER .env — the same rule both CLIs
	// already follow (bash's load_env_file and the Go CLI's loadEnvFile each
	// set a key only if it is not already in the environment), and the way
	// every hosting platform expects to configure a process.
	//
	// ⛔⛔ EXCEPT WHERE THERE IS AN OPERATOR. One process then serves many
	// sites, so a process-wide value cannot be any one site's setting: a single
	// POLIS_BASE_URL in the operator's environment would re-point EVERY tenant
	// at one domain. There the site's own .env is the only source. ⚠️ This is
	// why hosted sets a tenant's Mode BEFORE Initialize.
	processWins := !capability.For(s.effectiveMode()).HasOperator
	for _, key := range envKeys {
		if !processWins {
			break
		}
		fromProcess := os.Getenv(key)
		fromFile, inFile := env[key]
		if fromProcess == "" {
			continue
		}
		// ⚠️ Both set and different is rare and confusing, so say so. ⛔ Name
		// the KEY, never the values: a .env is where credentials live.
		if inFile && fromFile != "" && fromFile != fromProcess {
			log.Printf("[i] %s is set in both the process environment and %s; the process environment wins", key, envPath)
		}
		env[key] = fromProcess
	}

	// Apply discovery service settings
	if url := env["DISCOVERY_SERVICE_URL"]; url != "" {
		s.DiscoveryURL = url
	}

	// Store POLIS_BASE_URL for runtime use.
	// This is the authoritative source for base_url - not stored in .well-known/polis
	if baseURL := env["POLIS_BASE_URL"]; baseURL != "" {
		s.BaseURL = strings.TrimSuffix(baseURL, "/")
	}

	// Log level and retention
	if v := env["LOG_LEVEL"]; v != "" {
		if level, err := strconv.Atoi(v); err == nil && level >= 0 {
			s.LogLevel = level
			s.logLevelSet = true
		}
	}
	if v := env["LOG_RETENTION_DAYS"]; v != "" {
		if days, err := strconv.Atoi(v); err == nil && days > 0 {
			s.LogRetentionDays = days
		}
	}
}

// ApplyDiscoveryDefaults sets the default discovery service URL if not configured.
// This ensures the webapp works out of the box without requiring .env configuration.
// The discovery service key is no longer needed — all auth uses Ed25519 signatures.
func (s *Server) ApplyDiscoveryDefaults() {
	if s.DiscoveryURL == "" {
		s.DiscoveryURL = DefaultDiscoveryServiceURL
	}
}

// GetAuthorEmail returns the author email from .well-known/polis.
// Deprecated: Use GetAuthorDomain instead. Email is private by default.
func (s *Server) GetAuthorEmail() string {
	wk, err := site.LoadWellKnown(s.Site())
	if err != nil {
		return ""
	}
	return wk.Email
}

// GetAuthorDomain returns the domain identity, extracted from POLIS_BASE_URL.
func (s *Server) GetAuthorDomain() string {
	if baseURL := s.GetBaseURL(); baseURL != "" {
		return polisurl.ExtractDomain(baseURL)
	}
	return ""
}

// GetSubdomain extracts subdomain from POLIS_BASE_URL env var
func (s *Server) GetSubdomain() string {
	// Get from POLIS_BASE_URL env var (authoritative source)
	if baseURL := s.GetBaseURL(); baseURL != "" {
		// Extract subdomain from URL like https://alice.polis.pub
		host := strings.TrimPrefix(baseURL, "https://")
		host = strings.TrimPrefix(host, "http://")
		if idx := strings.Index(host, "."); idx > 0 {
			return host[:idx]
		}
	}
	// Fallback to deprecated config field
	if s.Config != nil {
		return s.Config.Subdomain
	}
	return ""
}

// GetSiteTitle returns site_title from .well-known/polis, falling back to POLIS_BASE_URL if empty
func (s *Server) GetSiteTitle() string {
	wk, err := site.LoadWellKnown(s.Site())
	if err != nil {
		// No .well-known/polis file - try config subdomain
		if s.Config != nil && s.Config.Subdomain != "" {
			return "https://" + s.Config.Subdomain + ".polis.pub"
		}
		return ""
	}
	// 1. Try site_title from file
	if wk.SiteTitle != "" {
		return wk.SiteTitle
	}
	// 2. Fall back to POLIS_BASE_URL env var
	if s.BaseURL != "" {
		return s.BaseURL
	}
	return ""
}

// ResolveSymlink follows symlinks to get the real path.
func ResolveSymlink(path string) string {
	// NOT the storage interface: filepath.EvalSymlinks RESOLVES links, and the
	// interface cannot represent one — Stat follows and Info.Mode is Perm() only.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// Path doesn't exist yet or other error - return original
		return path
	}
	return resolved
}

// FindAvailablePort finds an available port on localhost.
func FindAvailablePort() (int, error) {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close() // a probe listener, closed to free the port
	return port, nil
}

// OpenBrowser opens the default browser to the given URL.
func OpenBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url) //nolint:gosec // G204: opens the local server's own URL in the owner's browser, on their machine
	case "linux":
		cmd = exec.Command("xdg-open", url) //nolint:gosec // G204: opens the local server's own URL in the owner's browser, on their machine
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url) //nolint:gosec // G204: opens the local server's own URL in the owner's browser, on their machine
	default:
		fmt.Printf("[i] Please open %s in your browser\n", url)
		return
	}
	if err := cmd.Start(); err != nil {
		fmt.Printf("[i] Please open %s in your browser\n", url)
	}
}

// NOT the storage interface: FindCLIThemesDir searches UPWARD from a directory
// to find a repo root, so by construction it looks outside the site.
//
// FindCLIThemesDir locates the cli/themes directory for fallback theme snippets.
// It searches upward from the given directory to find the repo root.
func FindCLIThemesDir(startDir string) string {
	// Start from startDir and search upward
	dir := startDir
	for i := 0; i < 10; i++ { // Max 10 levels up
		themesPath := filepath.Join(dir, "cli-bash", "themes")
		// NOT the storage interface: asks whether a legacy directory is THERE, and an
		// EMPTY one still counts — which Exists cannot say.
		if info, err := os.Stat(themesPath); err == nil && info.IsDir() {
			return themesPath
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // Reached root
		}
		dir = parent
	}

	// Fallback: try current working directory
	cwd, err := os.Getwd()
	if err == nil {
		dir = cwd
		for i := 0; i < 10; i++ {
			themesPath := filepath.Join(dir, "cli-bash", "themes")
			// NOT the storage interface: asks whether a legacy directory is THERE, and an
			// EMPTY one still counts — which Exists cannot say.
			if info, err := os.Stat(themesPath); err == nil && info.IsDir() {
				return themesPath
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	// Return empty if not found (will just use local themes only)
	return ""
}

// NewServer creates and initializes a new Server instance.
func NewServer(dataDir, cliThemesDir string) *Server {
	return &Server{
		DataDir:      dataDir,
		CLIThemesDir: cliThemesDir,
	}
}

// Initialize validates the site and loads configuration.
func (s *Server) Initialize() {
	// Propagate CLI version to packages that still use package-level globals
	// as fallback when no explicit generator parameter is passed.
	if s.CLIVersion != "" {
		publish.Version = s.CLIVersion
		comment.Version = s.CLIVersion
		metadata.Version = s.CLIVersion
		following.Version = s.CLIVersion
		policy.Version = s.CLIVersion
		site.Version = s.CLIVersion
		tag.Version = s.CLIVersion
		// Wired before the webapp issues an attestation, on purpose. A package
		// with a Version var, a GetGenerator reader and NO assigner writes
		// "polis-cli-go/dev" into a signed artifact and nothing catches it —
		// pkg/following did it for five months, and pkg/license is doing it in
		// production right now. The assignment is one line; the silence is not.
		attestation.Version = s.CLIVersion
		license.Version = s.CLIVersion // E2 — see cmd/root.go
	}

	// Migrate .polis/drafts -> .polis/bundles/pub.polis.core/posts/drafts if needed
	s.migrateDraftsDir()

	// Validate the site first - only load keys/config if valid
	validation := site.Validate(s.Site())
	if validation.Status == site.StatusValid {
		// Load existing config if present
		s.LoadConfig()
		s.LoadKeys()
		s.siteConfigLoaded = true
	}

	// Load .env file for discovery service settings (overrides webapp/config.json)
	s.LoadEnv()

	// Apply default discovery URL if not set by config or .env (matches CLI behavior)
	s.ApplyDiscoveryDefaults()

	// Propagate discovery config to packages that register with discovery.
	// This ensures publish and comment packages handle registration internally.
	publish.DiscoveryURL = s.DiscoveryURL
	publish.BaseURL = s.BaseURL
	comment.DiscoveryURL = s.DiscoveryURL
	comment.BaseURL = s.BaseURL
	stream.DiscoveryURL = s.DiscoveryURL
	stream.BaseURL = s.BaseURL
	// ⭐ The handle, not the directory: it carries the store the site is on.
	// following needs neither, because it is handed the site at every call.
	stream.DefaultSite = s.Site()
	tag.DefaultSite = s.Site()
	following.DiscoveryURL = s.DiscoveryURL

	// Render-time read-through for blessed-comment cache misses: populate the
	// isolated cache via cache.Ingest so a foreign comment is never served from a
	// public path. No-op when there is no DS configured.
	InstallBlessedCacheFiller(extractDomainFromURL(s.DiscoveryURL))

	// Author-avatar fetch failures are logged through this site's events, unless
	// the process (polis-hosted) already set its own sink.
	render.SetAvatarFetchLoggerIfUnset(func(event string, fields map[string]any) {
		s.LogEvent(event, fields)
	})

	// One-time migration: consolidate author → author_name in .well-known/polis
	_ = site.MigrateAuthorField(s.Site()) // best effort, and retried at every start

	// Apply log defaults and write back to .env so settings are visible
	// Only an ABSENT level takes the default. LOG_LEVEL=0 is LogLevelOff, a
	// real setting; defaulting it would turn logging back on and write 1 over
	// the user's 0 in .env.
	if !s.logLevelSet && s.LogLevel == 0 {
		s.LogLevel = LogLevelBasic
	}
	if s.LogRetentionDays == 0 {
		s.LogRetentionDays = DefaultLogRetentionDays
	}

	s.writeEnvFile(".env", map[string]string{
		"LOG_LEVEL":          strconv.Itoa(s.LogLevel),
		"LOG_RETENTION_DAYS": strconv.Itoa(s.LogRetentionDays),
	})

	// Create logger (disk always; JSON stdout when LOG_FORMAT=json)
	logsDir := filepath.Join(s.DataDir, ".polis", "logs")
	// ⭐ Reuse a logger made at boot rather than replacing it: a server that
	// starts on its first request builds its request middleware before
	// Initialize runs, and that middleware holds the logger it was given.
	// Replacing it here would leave every request unlogged, silently.
	if s.Logger == nil {
		s.Logger = NewLogger(s.LogLevel, logsDir)
	} else {
		s.Logger.level, s.Logger.logsDir = s.LogLevel, logsDir
	}
	s.Logger.retentionDays = s.LogRetentionDays
	if s.LogOutput != nil {
		s.Logger.SetOutput(s.LogOutput)
	}

	// Enable JSON stdout output when LOG_FORMAT=json (for Axiom/Vector ingestion)
	if os.Getenv("LOG_FORMAT") == "json" {
		s.Logger.jsonOutput = true
	}

	// LOG_EVENTS_DISABLED: event names or dotted prefixes (e.g., "feed,site")
	if disabled := os.Getenv("LOG_EVENTS_DISABLED"); disabled != "" {
		s.Logger.disabledEvents = ParseDisabledEvents(disabled)
	}

	s.Logger.PruneLogs()
	s.Logger.Info("Server starting with log level %d", s.LogLevel)
	s.Logger.Info("Data directory: %s", s.DataDir)

	// Author key cache intentionally NOT initialized.
	// When nil, FilterVerifiedEvents passes all events through, relying on
	// DS envelope signature verification instead. Per-event author signature
	// verification is not yet feasible because DS-emitted events carry the
	// author's content-registration signature, not a stream-event signature.
	// The verifier cannot reconstruct the original signed payload.
	// The fix is for the stream to carry the signed content, so the verifier
	// can rebuild what was signed; until then this stays off.
	// s.AuthorKeyCache = discovery.NewAuthorKeyCache(0, 0)
}

// migrateDraftsDir migrates .polis/drafts to .polis/bundles/pub.polis.core/posts/drafts if needed.
func (s *Server) migrateDraftsDir() {
	oldPath := filepath.Join(s.DataDir, ".polis", "drafts")
	newPath := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")

	// NOT the storage interface, all of it: this MOVES A DIRECTORY with
	// os.Rename. The interface has no rename — atomicity lives in atomicfile
	// and a prefix store has no directories to move — so a one-time layout
	// migration like this stays on the filesystem.
	oldInfo, oldErr := os.Stat(oldPath)
	_, newErr := os.Stat(newPath)

	if oldErr == nil && oldInfo.IsDir() && errors.Is(newErr, fs.ErrNotExist) {
		// Create parent directory (.polis/bundles — restricted)
		if err := os.MkdirAll(filepath.Dir(newPath), 0700); err != nil {
			log.Printf("[warning] Failed to create parent directory for drafts migration: %v", err)
			return
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			log.Printf("[warning] Failed to migrate drafts directory: %v", err)
		} else {
			log.Printf("[i] Migrated drafts: .polis/drafts -> .polis/bundles/pub.polis.core/posts/drafts")
		}
	}
}

// Close cleans up server resources.
func (s *Server) Close() {
	s.StopSync()
	if s.Logger != nil {
		s.Logger.Close()
	}
}

// StopSync stops the background sync goroutine. Safe to call multiple times.
func (s *Server) StopSync() {
	if s.syncDone != nil {
		select {
		case <-s.syncDone:
			// already closed
		default:
			close(s.syncDone)
		}
	}
}

// RegisterSyncHandler adds a handler to the unified sync loop.
func (s *Server) RegisterSyncHandler(h stream.SyncHandler) {
	s.syncHandlers = append(s.syncHandlers, h)
}

// TriggerSync requests an immediate sync cycle (non-blocking).
// Used after user-initiated mutations (publish, bless, follow) and by the
// discovery service's wake, so the site's caches catch up at once.
func (s *Server) TriggerSync() {
	select {
	case s.syncTrigger <- struct{}{}:
	default:
		// Already triggered, skip
	}
}

// StartBackgroundSync registers built-in handlers and starts the unified
// sync loop that processes ALL event types in a single coordinated cycle.
func (s *Server) StartBackgroundSync() {
	// Initialize infrastructure
	s.syncTrigger = make(chan struct{}, 1)
	s.syncDone = make(chan struct{})

	// Register handlers
	s.syncHandlers = nil
	s.RegisterSyncHandler(&feedSyncHandler{server: s})
	s.RegisterSyncHandler(&followSyncHandler{server: s})
	s.RegisterSyncHandler(&commentStatusSyncHandler{server: s})
	s.RegisterSyncHandler(&blessingSyncHandler{server: s})

	go func() {
		// Initial catch-up: run legacy comment sync for pre-existing pending comments
		s.syncCommentStatuses()

		// Initial unified sync
		s.runUnifiedSync()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-s.syncDone:
				return
			case <-ticker.C:
				// The ticker only prunes logs. A sync runs at start, when
				// asked (TriggerSync: a write, the DS's wake) and when the
				// owner reads a stale first-person view (syncIfStale).
				if s.Logger != nil {
					s.Logger.PruneLogs()
				}
			case <-s.syncTrigger:
				s.runUnifiedSync()
			}
		}
	}()
}

// allowPrivateDMRecipients is the outbound address rule for messages
// (ops/dm_send.go), for every send path on this server: a private recipient
// only in local mode, on the owner's own machine, and never where hosted
// restricts egress.
func (s *Server) allowPrivateDMRecipients() bool {
	return s.effectiveMode() == mode.Local && !s.RestrictDMEgress
}

// dmHTTPClient is the client a message route sends with: the shared one, its
// connections limited to public addresses unless the rule allows private ones.
func (s *Server) dmHTTPClient() *http.Client {
	if s.allowPrivateDMRecipients() {
		return s.SharedHTTPClient
	}
	return dm.PublicOnly(s.SharedHTTPClient)
}

// dmMailbox returns the tenant DM mailbox, the site's own domain (for convID↔peer
// mapping), and the epoch DEKs available without a password (bootstrap server_dek;
// password epochs stay locked until the SPA unlock).
func (s *Server) dmMailbox() (*dm.Mailbox, string, map[int][32]byte, error) {
	if s.PrivateKey == nil {
		return nil, "", nil, fmt.Errorf("no private key configured")
	}
	deks, err := dm.LoadAvailableDEKs(s.Site())
	if err != nil {
		return nil, "", nil, err
	}
	return dm.NewMailbox(s.Site()), dm.ExtractDomainFromURL(s.GetBaseURL()), deks, nil
}

// syncCommentStatuses checks pending comments against the discovery service
// and moves any that have been blessed or denied. Re-renders the site if
// any statuses changed so HTML and index.html stay current.
func (s *Server) syncCommentStatuses() {
	if s.DiscoveryURL == "" || s.PrivateKey == nil {
		return
	}
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		return
	}

	// Quick check: any pending comments at all?
	pendingDir := siteKey(".polis", "bundles", "pub.polis.core", "comments", "pending")
	entries := s.listKey(pendingDir)
	var err error
	if err != nil || len(entries) == 0 {
		return
	}

	// Read-only: scans for new blessing/denial status on the comments
	// we've published. NewReadDSClient skips auth when this tenant is
	// not registered with the DS — the DS allows anonymous reads on
	// the relationship-query endpoint.
	client := s.NewReadDSClient(nil)
	client.RequestID = generateBackgroundRequestID("comment-sync")

	hc := s.getHookConfig()

	result, err := comment.SyncPendingComments(s.Site(), baseURL, client, hc)
	if err != nil {
		s.LogDebug("background comment sync failed: %v", err)
		return
	}

	// Re-render if any statuses changed
	if len(result.Blessed) > 0 || len(result.Denied) > 0 {
		if err := s.RenderSite(); err != nil {
			log.Printf("[warning] background comment sync render failed: %v", err)
		}
		s.LogInfo("background comment sync: %d blessed, %d denied", len(result.Blessed), len(result.Denied))
	}
}

// cursorGreater compares two cursor position strings numerically.
// Stream cursors are stringified integers; string comparison fails for
// multi-digit values (e.g., "30" < "4" lexicographically).
func cursorGreater(a, b string) bool {
	ai, errA := strconv.Atoi(a)
	bi, errB := strconv.Atoi(b)
	if errA != nil || errB != nil {
		return a > b // fallback to string comparison
	}
	return ai > bi
}

// syncFeed queries the discovery stream for post and comment events
// from followed authors and merges them into the feed cache.
//
// Structural early-returns (no DS config, empty following, etc.) bump the
// cache's LastUpdated via cm.MarkChecked() so IsStale returns false. Without
// this, the frontend's auto-sync-on-stale loop would call /api/feed/refresh
// in a tight cycle: refresh skips → cache still stale → frontend retries.
// Transient errors after the StreamQuery call do NOT mark checked — we want
// the next cycle to retry.
func (s *Server) syncFeed() {
	cm := s.feedCacheForScope("network")

	if s.DiscoveryURL == "" {
		log.Printf("[feed-sync] skip: no discovery config (url=%q)", s.DiscoveryURL)
		_ = cm.MarkChecked()
		return
	}
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		log.Printf("[feed-sync] skip: empty BaseURL (dataDir=%s)", s.DataDir)
		_ = cm.MarkChecked()
		return
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		log.Printf("[feed-sync] skip: could not extract domain from %s", baseURL)
		_ = cm.MarkChecked()
		return
	}

	// Load following list to get followed domains
	f, err := following.LoadSite(s.Site())
	if err != nil {
		log.Printf("[feed-sync] skip: following load error: %v", err)
		// Load error could be transient (file lock, partial write) —
		// don't mark checked, let the next cycle retry.
		return
	}
	if f.Count() == 0 {
		log.Printf("[feed-sync] skip: following list is empty (site=%s)", s.DataDir)
		_ = cm.MarkChecked()
		return
	}

	var domains []string
	for _, entry := range f.All() {
		d := discovery.ExtractDomainFromURL(entry.URL)
		if d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		log.Printf("[feed-sync] skip: no valid domains extracted from %d following entries", f.Count())
		_ = cm.MarkChecked()
		return
	}

	// Load feed cursor (cm already obtained at top for early-return MarkChecked).
	cursor, _ := cm.GetCursor()

	// Query DS stream with actor filter for followed domains
	client := s.NewDSClient(nil)
	client.RequestID = generateBackgroundRequestID("feed-sync")
	typeFilter := "pub.polis.post.published,pub.polis.post.republished,pub.polis.comment.published,pub.polis.comment.republished,pub.polis.comment.blessing.granted,pub.polis.comment.blessing.requested,pub.polis.follow.announced,pub.polis.site.registered"
	actorFilter := discovery.JoinDomains(domains)

	log.Printf("[feed-sync] querying stream: myDomain=%s actors=%s cursor=%q dsURL=%s", myDomain, actorFilter, cursor, s.DiscoveryURL)

	result, err := client.StreamQuery(cursor, 1000, typeFilter, actorFilter, "")
	if err != nil {
		log.Printf("[feed-sync] stream query failed: %v", err)
		return
	}

	log.Printf("[feed-sync] stream returned %d events, cursor=%q, hasMore=%v", len(result.Events), result.Cursor, result.HasMore)

	// Transform events to feed items
	handler := &feed.FeedHandler{
		MyDomain:        myDomain,
		FollowedDomains: make(map[string]bool, len(domains)),
	}
	for _, d := range domains {
		handler.FollowedDomains[d] = true
	}

	items := handler.Process(result.Events)
	log.Printf("[feed-sync] processed %d events -> %d feed items", len(result.Events), len(items))

	// Merge into cache. R21: cursor advances regardless of how many of
	// the newly-added items survived pruning. Older intent (hold cursor
	// when pruning dropped new items, on the theory that older items
	// would age out and the next re-fetch would succeed) is permanently
	// broken: items past MaxAgeDays/MaxAnnouncementDays are dropped by
	// AGE, never admitted no matter how long we wait, so the cursor
	// freezes forever and no newer events ever load. Pruning is a
	// normal eviction signal — log how many were dropped so we can
	// see when a tenant's cap is too tight, but always move the cursor
	// forward.
	mergeFailed := false
	if len(items) > 0 {
		mr, err := cm.MergeItemsResult(items)
		if err != nil {
			log.Printf("[feed-sync] merge failed: %v", err)
			mergeFailed = true
		} else {
			log.Printf("[feed-sync] merged %d new items, %d retained after prune", mr.Added, mr.Retained)
			if mr.Added > 0 && mr.Retained < mr.Added {
				log.Printf("[feed-sync] %d items pruned on insert (advancing cursor anyway; tighten retention if this is unexpected)",
					mr.Added-mr.Retained)
			}
		}
	}

	// Advance the cursor unless the merge itself failed (disk error,
	// etc.). Pruning losses no longer block advancement.
	if result.Cursor != "" {
		if mergeFailed {
			// Touch LastUpdated for staleness tracking without
			// advancing position so the next cycle retries.
			_ = cm.SetCursor(cursor)
		} else {
			_ = cm.SetCursor(result.Cursor)
		}
	}
}

// backfillFeedForAuthor queries the DS stream from the beginning (since="0")
// for a single author's events and merges them into the feed cache.
// This ensures historical posts appear when following a new author.
// It does NOT update the global feed cursor.
func (s *Server) backfillFeedForAuthor(domain string) {
	if s.DiscoveryURL == "" {
		return
	}
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		return
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return
	}

	client := s.NewDSClient(nil)
	client.RequestID = generateBackgroundRequestID("feed-backfill")
	typeFilter := "pub.polis.post.published,pub.polis.post.republished,pub.polis.comment.published,pub.polis.comment.republished,pub.polis.comment.blessing.granted,pub.polis.comment.blessing.requested,pub.polis.follow.announced,pub.polis.site.registered"

	handler := &feed.FeedHandler{
		MyDomain:        myDomain,
		FollowedDomains: map[string]bool{domain: true},
	}

	cm := s.feedCacheForScope("network")

	// Page through all events from the beginning for this author
	cursor := "0"
	for {
		result, err := client.StreamQuery(cursor, 1000, typeFilter, domain, "")
		if err != nil {
			log.Printf("[feed-backfill] stream query failed for %s: %v", domain, err)
			return
		}

		log.Printf("[feed-backfill] %s: got %d events (cursor=%q hasMore=%v)", domain, len(result.Events), result.Cursor, result.HasMore)

		items := handler.Process(result.Events)
		if len(items) > 0 {
			newCount, err := cm.MergeItems(items)
			if err != nil {
				log.Printf("[feed-backfill] merge failed for %s: %v", domain, err)
				return
			}
			log.Printf("[feed-backfill] %s: merged %d new items", domain, newCount)
		}

		if !result.HasMore || result.Cursor == "" || result.Cursor == cursor {
			break
		}
		cursor = result.Cursor
	}
}

// newV1Handler is the site's API, /v1, on its own mux, recorded so /mcp can
// call it in-process.
func (s *Server) newV1Handler(engine *ops.Engine) http.Handler {
	m := http.NewServeMux()
	api.SetupRoutesWithHost(m, engine, s.Site(), s.apiHost(), func(event string, fields map[string]interface{}) {
		s.LogEvent(event, fields)
	})
	s.setV1Handler(m)
	return m
}

func (s *Server) setV1Handler(h http.Handler) {
	s.v1Mu.Lock()
	s.v1 = h
	s.v1Mu.Unlock()
}

func (s *Server) v1Handler() http.Handler {
	s.v1Mu.Lock()
	defer s.v1Mu.Unlock()
	return s.v1
}

// Handler returns an http.Handler for this Server's API routes.
// Used by the hosted service to serve tenant requests without starting a
// standalone HTTP listener.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	SetupRoutes(mux, s)

	// Setup v1 content API routes (needed for DM delivery, content queries, etc.)
	if apiEngine, err := s.newContentEngine(); err == nil {
		mux.Handle("/v1/", s.newV1Handler(apiEngine))
	}

	// ⚠️ NOT loopback: this handler is mounted by the hosted service and
	// reached over the network, so BoundToLoopback stays false and the
	// operator provider is what answers in custodied mode.
	// ⛔ Key reload outermost — see the note at the standalone listener: a
	// request refused by the gate must still have refreshed the keys, or a
	// provider that verifies against them reads a retired one.
	return s.keyReloadMiddleware(s.ownerGateMiddleware(mux))
}

// Two-axis flag values for polis-server invocation modes (step 5.a).
//
// DataMode selects how the data directory is interpreted:
//   - DataModeOwner ("owner", default): the operator's own tenant. Keys
//     present, write paths enabled, DS auto-register on startup.
//   - DataModeMirror ("mirror"): a clone/snapshot of another tenant. Keys
//     may be absent; auto-register / DS announce / publish disabled.
//
// Surface selects what's served:
//   - SurfaceEditor ("editor", default for --owner): admin SPA + editor
//     APIs + deep-link /_/... routes. Today's default behavior.
//   - SurfaceReader ("reader", default for --mirror): tenant-static at /,
//     read-only structured-query API at /api/v1/stream/items. No SPA, no
//     editor APIs, no /_/... routes.
//
// Error case: DataModeMirror + SurfaceEditor is invalid (the editor surface
// requires write paths that mirror data can't support). Run() rejects this
// combination at startup with a clear message.
const (
	DataModeOwner  = "owner"
	DataModeMirror = "mirror"
	SurfaceEditor  = "editor"
	SurfaceReader  = "reader"
)

// RunOptions contains optional configuration for the server.
type RunOptions struct {
	CLIVersion string // CLI version for metadata (empty = use package default)
	Port       int    // Specific port to bind to (0 = auto-pick an available port)
	DataMode   string // "owner" (default) or "mirror"; empty == DataModeOwner
	Surface    string // "editor" or "reader"; empty defaults from DataMode
	// DevMode (polis serve --dev) runs the server as remote-client-signed, a
	// reachable instance, so the capability descriptor turns the full messaging
	// UI (compose/send) on; loopback trust still holds (owner.Instance.Dev). By
	// default localhost DMs are read-only (DMs are a networked feature; a served
	// export is offline). Delivery to a real peer still needs a reachable
	// network. See docs/cli/user/command-reference.md (polis serve).
	DevMode bool
}

// resolveModes normalizes RunOptions's flag fields, applying defaults and
// returning an error for invalid combinations. Empty DataMode → "owner".
// Empty Surface → "editor" if DataMode is "owner", "reader" if "mirror".
// Any combination of "mirror" + "editor" is rejected.
func resolveModes(opts RunOptions) (dataMode, surface string, err error) {
	dataMode = opts.DataMode
	if dataMode == "" {
		dataMode = DataModeOwner
	}
	if dataMode != DataModeOwner && dataMode != DataModeMirror {
		return "", "", fmt.Errorf("invalid --data-mode %q (want %q or %q)", dataMode, DataModeOwner, DataModeMirror)
	}

	surface = opts.Surface
	if surface == "" {
		if dataMode == DataModeMirror {
			surface = SurfaceReader
		} else {
			surface = SurfaceEditor
		}
	}
	if surface != SurfaceEditor && surface != SurfaceReader {
		return "", "", fmt.Errorf("invalid --surface %q (want %q or %q)", surface, SurfaceEditor, SurfaceReader)
	}

	if dataMode == DataModeMirror && surface == SurfaceEditor {
		return "", "", fmt.Errorf("--mirror cannot run with --editor surface (signing keys, DS announce, and publish flows don't apply to mirror data); use --reader (the default for --mirror)")
	}

	return dataMode, surface, nil
}

// Run starts the HTTP server with the given embedded filesystem.
func Run(webFS fs.FS, dataDir string, opts ...RunOptions) {
	// Resolve flag-mode combination first so we fail fast on invalid combos
	// (e.g., --mirror --editor) before doing any filesystem work.
	var optsVal RunOptions
	if len(opts) > 0 {
		optsVal = opts[0]
	}
	dataMode, surface, err := resolveModes(optsVal)
	if err != nil {
		log.Fatalf("[error] invalid flag combination: %v", err)
	}

	// Resolve symlinks - if data/ is a symlink, follow it
	dataDir = ResolveSymlink(dataDir)

	// DON'T auto-create directories on startup - let the user choose init vs link
	// We only create the parent data dir if it doesn't exist (needed for symlink target)
	// NOT the storage interface: creating the store's own ROOT is a backend
	// concern, not a key operation.
	if _, err := os.Stat(dataDir); errors.Is(err, fs.ErrNotExist) {
		// Create just the data directory (not the full structure)
		if err := os.MkdirAll(dataDir, 0755); err != nil { //nolint:gosec // G301: the site root; .polis inside it is created 0700
			log.Printf("[warning] Failed to create data directory: %v", err)
		}
	}

	// Find executable directory for CLI themes
	execPath, err := os.Executable()
	if err != nil {
		log.Fatal("Failed to get executable path:", err)
	}
	execDir := filepath.Dir(execPath)

	// Find CLI themes directory (for fallback theme snippets)
	cliThemesDir := FindCLIThemesDir(execDir)

	// Developer mode (polis serve --dev): re-enable the localhost messaging UI.
	devMode := len(opts) > 0 && opts[0].DevMode

	// ⭐ The one construction, shared with the network handler: backend, mode,
	// configuration, identity key and recovery. ⛔ Every failure stops the boot.
	server, err := newInstance(instanceOptions{
		DataDir:      dataDir,
		CLIThemesDir: cliThemesDir,
		CLIVersion:   optsVal.CLIVersion,
		DevMode:      devMode,
		Getenv:       os.Getenv,
	})
	if err != nil {
		log.Fatalf("%v", err)
	}
	server.EnableHooks = true // Localhost mode: hooks are safe to run
	defer server.Close()

	// Start background sync (notifications + feed). Mirror mode skips —
	// the data dir is a clone, not the operator's tenant; no DS announce
	// or auto-register applies. ⚠️ Nor does a request-credentialed backend:
	// the loops need storage between requests, which it cannot give them, so
	// that work arrives as scheduled requests instead.
	if dataMode != DataModeMirror && !server.StartsOnRequest() {
		server.StartBackgroundSync()
	}

	// Resolve port: explicit RunOptions.Port wins; otherwise auto-pick.
	var port int
	if len(opts) > 0 && opts[0].Port > 0 {
		port = opts[0].Port
	} else {
		var err error
		port, err = FindAvailablePort()
		if err != nil {
			log.Fatal("Failed to find available port:", err)
		}
	}

	// Setup routes — conditional on surface (--editor vs --reader).
	//
	// --editor (default for --owner): admin SPA + full editor API surface.
	// --reader (default for --mirror): read-only public surface — only
	//   tenant-static content at / + the structured-query stream items
	//   endpoint at /api/v1/stream/items. No SPA, no editor APIs, no
	//   /_/... deep-link routes. Used by polis-server-as-discover for
	//   the local-stack scenario (step 5.e) and by --reader dogfood mode.
	var routes *http.ServeMux
	if surface == SurfaceReader {
		routes = http.NewServeMux()
		SetupReaderRoutes(routes, server)
		routes.Handle("/", tenantStaticHandler(server))
	} else {
		routes = editorRoutes(server, webFS)
	}

	// R18-18 (2026-05-18): bind is HARDCODED to `localhost` so the
	// localhost server can't accidentally be exposed on a non-loopback
	// interface. The "owner-trust by construction" model — under which
	// /api/content/ (drafts, etc.) and many other endpoints serve
	// without auth — assumes the only callers reach the loopback
	// interface, which on a single-user machine means the owner.
	//
	// Do NOT add a `--host` / `--bind` RunOptions field without also
	// gating sensitive endpoints behind auth. The handlers themselves
	// trust loopback unconditionally. If you need cross-machine access,
	// run the hosted multi-tenant binary instead (which has session
	// + magic-link auth) or front this with an SSH tunnel + your own
	// access controls.
	//
	// Regression test: TestRunBindIsLoopbackOnly in server_test.go
	// asserts this string literal stays "localhost:" prefixed.
	addr := fmt.Sprintf("localhost:%d", port)
	url := fmt.Sprintf("http://%s", addr)

	fmt.Printf("[i] Starting polis server...\n")
	fmt.Printf("[i] Listening on %s\n", url)
	fmt.Printf("[i] Data directory: %s\n", dataDir)
	fmt.Printf("[i] Mode: --%s --%s\n", dataMode, surface)

	// Open browser after a short delay
	go func() {
		time.Sleep(500 * time.Millisecond)
		OpenBrowser(url)
	}()

	// Every request first checks the key file (keyreload.go), then the
	// localhost guard and the rest of localhostModeHandler wrap it.
	// ⭐ The bind below is hardcoded to localhost, so this is true by
	// construction here — recorded from the listener, never inferred later.
	server.BoundToLoopback = true
	// ⭐ The shared middleware order (instanceHandler), inside the localhost
	// guard and the rest of localhostModeHandler.
	mux := server.instanceHandler(routes)
	if err := NewHTTPServer(addr, localhostModeHandler(server.Logger, mux)).ListenAndServe(); err != nil {
		log.Fatal("Server error:", err)
	}
}

// spaHandler serves the owner's app (the v5 app at /_/ and below, the
// instance's root redirecting there), the embedded files it loads, and the
// site's own generated files; 404 for anything else.
func spaHandler(fsys fs.FS, site storage.Site) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		// ⭐ Every site's owner app is the v5 app, whatever its shape: /_/
		// and below. The v4 app is retired.
		if isAppPath(path) {
			ServeV5Owner(w, r, fsys)
			return
		}
		// The instance's root is the owner's app, as it always was here.
		if path == "" || path == "index.html" {
			http.Redirect(w, r, "/_/", http.StatusFound)
			return
		}
		// If the file exists in the embedded FS, serve it directly
		if _, err := fs.Stat(fsys, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// The site's own generated files (robots.txt, rsl.xml, licence pages,
		// .well-known/) come from the data dir, 404 when absent — never the
		// shell with a 200 (epic 29 E2).
		if isSiteFilePath(site, r.URL.Path) {
			serveSiteFile(w, r, site)
			return
		}
		// ⛔ ANY OTHER PATH IS A 404, as on hosted. Serving the shell with a 200
		// for everything made a mistyped page, a retired URL and a private name
		// (/.env, /.polis/keys/…) all look served, to a crawler and to any check
		// that reads the status; on a remote instance every such path reaches
		// this handler, since the deployment's static files answer the rest.
		http.NotFound(w, r)
	})
}

// isAppPath reports whether a path (without its leading slash) is one of the
// owner app's own routes, which the SPA resolves in the browser.
func isAppPath(path string) bool {
	return path == "_" || strings.HasPrefix(path, "_/")
}

// GetDiscoveryDomain returns the discovery service hostname for use as
// the namespace key in .polis/ds/<domain>/.
func (s *Server) GetDiscoveryDomain() string {
	domain := extractDomainFromURL(s.DiscoveryURL)
	if domain == "" {
		return "default"
	}
	return domain
}

// runtimeFilteredScopes are feed scopes that are served as runtime filters
// over the network cache rather than materialized as separate JSONL files.
// Only "global" needs its own file because it contains data outside the
// unified sync boundary (posts from unfollowed domains).
var runtimeFilteredScopes = map[string]bool{
	"followers": true,
	"me":        true,
}

// feedCacheForScope returns a shared CacheManager for the given scope.
// All callers (background sync, manual refresh, counts) share the same
// instance per scope to avoid file-level TOCTOU races.
//
// Runtime-filtered scopes ("followers", "me") return the network cache
// manager — their data is a subset of the network feed, filtered at read
// time via FilterOptions.AuthorDomains.
func (s *Server) feedCacheForScope(scope string) *feed.CacheManager {
	// Normalize scope key
	key := scope
	if key == "" || key == "network" || runtimeFilteredScopes[key] {
		key = "network"
	}

	s.feedCacheMu.Lock()
	defer s.feedCacheMu.Unlock()

	if s.feedCaches == nil {
		s.feedCaches = make(map[string]*feed.CacheManager)
	}
	if cm, ok := s.feedCaches[key]; ok {
		return cm
	}

	discoveryDomain := s.GetDiscoveryDomain()
	var cm *feed.CacheManager
	if key == "network" {
		cm = feed.NewCacheManager(s.Site(), discoveryDomain)
	} else {
		cm = feed.NewScopedCacheManager(s.Site(), discoveryDomain, key)
	}
	s.feedCaches[key] = cm
	return cm
}

// feedFilterForScope returns FilterOptions with AuthorDomains set for
// runtime-filtered scopes (followers, me). For other scopes returns empty opts.
func (s *Server) feedFilterForScope(scope string) feed.FilterOptions {
	switch scope {
	case "followers":
		discoveryDomain := s.GetDiscoveryDomain()
		store := stream.NewStore(s.Site(), discoveryDomain, "pub.polis.core")
		var state stream.FollowerState
		_ = store.LoadState("pub.polis.follow", &state)
		m := make(map[string]bool, len(state.Followers))
		for _, d := range state.Followers {
			m[d] = true
		}
		return feed.FilterOptions{AuthorDomains: m}
	case "me":
		baseURL := s.GetBaseURL()
		myDomain := extractDomainFromURL(baseURL)
		if myDomain != "" {
			return feed.FilterOptions{AuthorDomains: map[string]bool{myDomain: true}}
		}
	}
	return feed.FilterOptions{}
}

// extractDomainFromURL extracts the hostname from a URL string.
func extractDomainFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// isPostIndexEntry reports whether an index.jsonl entry is a post.
//
// Keys on `type`, falling back to the source path for entries written before
// the field was reliably set. Everything else — comment, tag, attestation, a
// type this build has never heard of — is not a post.
func isPostIndexEntry(entryType, path string) bool {
	if entryType != "" {
		return entryType == "post"
	}
	return strings.HasPrefix(path, "content/pub.polis.core/post/") || strings.HasPrefix(path, "posts/")
}

// APIPointerEnv switches on the `api` pointer in .well-known/polis: "1" on,
// anything else off.
//
// ⭐ DARK UNTIL SWITCHED ON. The description itself is always served at
// /v1/openapi.json, but the pointer is what tells every crawler and agent
// that the API exists, and once published it is a promise (the developer
// reference states it), so it is published only where this is on. On
// polis.pub it is the operator's switch, a Fly secret.
const APIPointerEnv = "POLIS_API_POINTER"

// EnsureAPIPointer makes .well-known/polis's `api` member match what this
// site offers: present when the switch is on and the site has an API an
// outsider can reach, absent otherwise. It writes only when that changes
// (Patrol and Judge track the file).
//
// ⭐ A site that should carry the pointer and cannot (the switch is on, the
// site has a public API, and there is no base URL to name or the write
// failed) logs the same event with published false and failed true, so the
// operator's monitor for a missing pointer sees it as it sees a withdrawal.
func (s *Server) EnsureAPIPointer() {
	var p *site.APIPointer
	caps := capability.For(s.effectiveMode())
	if s.APIPointerEnabled {
		if desc, ver, mcp, ok := apidesc.Pointer(caps, s.GetBaseURL()); ok {
			p = &site.APIPointer{Description: desc, Version: ver, MCP: mcp}
		} else if caps.HasPublicApi {
			s.LogEvent("pub.polis.site.api_pointer", map[string]interface{}{"published": false, "failed": true, "reason": "no base URL"})
		}
	}
	changed, err := site.SetAPIPointer(s.Site(), p)
	if err != nil {
		if p != nil {
			s.LogWarn("api pointer: %v", err)
			s.LogEvent("pub.polis.site.api_pointer", map[string]interface{}{"published": false, "failed": true, "reason": "write failed"})
		}
		return
	}
	if changed {
		s.LogEvent("pub.polis.site.api_pointer", map[string]interface{}{"published": p != nil})
	}
}

// apiQuery is how /v1/query reaches PQL: the very handler /pql/ is served by
// (rate limit included), and the owner gate's own rule for which sentences are
// the owner's alone. The API checks the owner's token for those itself;
// /pql/ keeps its session-based gate.
func (s *Server) apiQuery() *api.Query {
	return &api.Query{
		Handler: http.HandlerFunc(publicContentMiddleware(sharedPublicContentLimiter, s.Proxy, s.handleStreamPQL)),
		Scope:   PQLScope,
	}
}

// apiHost is what this server serves to the API beyond the engine: PQL and
// the blessing decisions, each through the code its own routes use.
func (s *Server) apiHost() *api.Host {
	return &api.Host{
		Query:      s.apiQuery(),
		Blessing:   &api.Blessing{Bless: s.BlessComment, Deny: s.DenyComment, Unbless: s.UnblessComment},
		Following:  &api.Following{Follow: s.Follow, Unfollow: s.Unfollow},
		Routes:     withAnswers(append(append(append(s.siteRoutes(), s.messageRoutes()...), s.ownerRoutes()...), s.sourceRoutes()...)),
		Tokens:     s.AgentTokens,
		ClientAddr: func(r *http.Request) string { return remoteIP(r, s.Proxy) },
		// The owner's interface reaches /v1 on the owner's own sign-in.
		OwnerSession: s.apiOwnerSession,
		SwitchedOff:  s.SwitchedOff,
	}
}

// switchedOff asks the host whether a switch is off.
func (s *Server) switchedOff(name string) bool {
	return s.SwitchedOff != nil && s.SwitchedOff(name)
}

// PQLScope is the scope a /pql/<sentence> request needs through the API: none
// for public content, read for the owner's own private content, and the
// owner's full token for messages, which no agent scope reaches.
func PQLScope(r *http.Request) string {
	if !PQLRequiresOwner(r) {
		return ""
	}
	if f, err := pql.ParseURL(r.URL.Path, "@__tenant__"); err == nil && f != nil {
		if f.Type == "dms" {
			return apidesc.ScopeFull
		}
		// The owner's own activity: that things happened, the notify scope.
		if aboutMe(f) {
			return apidesc.ScopeNotify
		}
	}
	return apidesc.ScopeRead
}
