// Package server provides the HTTP server implementation for the Polis webapp.
package server

import (
	"encoding/json"
	"fmt"
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
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/webapp/internal/api"
	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
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
}

// SSEEvent is a server-sent event pushed to connected clients.
type SSEEvent struct {
	Event string // event type (e.g., "counts")
	Data  string // JSON payload
}

// DefaultLogRetentionDays is the default number of days to keep log files.
const DefaultLogRetentionDays = 7

// Server holds the application state
type Server struct {
	DataDir          string
	CLIThemesDir     string // Path to CLI themes directory (fallback for theme snippets)
	CLIVersion       string // CLI version for metadata files (set by bundled binary or from version.txt)
	Config           *Config
	PrivateKey       []byte
	PublicKey        []byte
	Logger           *Logger
	LogLevel         int // From .env LOG_LEVEL (default 1)
	LogRetentionDays int // From .env LOG_RETENTION_DAYS (default 7)
	BaseURL          string // From POLIS_BASE_URL env var (runtime config, not stored in .well-known/polis)
	DiscoveryURL     string // From .env / env var DISCOVERY_SERVICE_URL (not stored in webapp/config.json)
	DiscoveryKey     string // From .env / env var DISCOVERY_SERVICE_KEY (not stored in webapp/config.json)

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
	syncTrigger    chan struct{} // non-blocking channel to trigger on-demand sync
	AuthorKeyCache *discovery.AuthorKeyCache // cached author public keys for signature verification

	// Policy check cache: key is "recipient:myDomain", value is cached result with expiry
	policyCache   map[string]*policyCacheEntry
	policyCacheMu sync.Mutex

	// Cross-tenant comment-count cache: post URL → count + expiry.
	// Populated by populateCrossTenantCommentCounts in handlers_stream.go.
	// commentCountFlight collapses concurrent stream requests that share
	// overlapping URL sets into a single DS roundtrip (singleflight).
	commentCountCache   map[string]*commentCountCacheEntry
	commentCountCacheMu sync.Mutex
	commentCountFlight  commentCountSingleflight

	// SSE client registry
	sseClients map[chan SSEEvent]struct{}
	sseMu      sync.Mutex

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
	// Capacity-planning Step-6-Scaling-Concerns row.
	mutualsCacheMu     sync.Mutex
	mutualsCacheValue  map[string]bool
	mutualsCacheAt     time.Time
	mutualsOutboundMt  time.Time // mtime of following.json when cached
	mutualsInboundMt   time.Time // mtime of pub.polis.follow.json when cached

	syncDone chan struct{} // closed by StopSync() to stop the background goroutine
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
	disabledEvents map[string]bool // category filter (LOG_EVENTS_DISABLED)
}

// NewLogger creates a new logger with the given level and logs directory
func NewLogger(level int, logsDir string) *Logger {
	return &Logger{
		level:   level,
		logsDir: logsDir,
	}
}

// SetRetentionDays sets the number of days to keep log files for pruning.
func (l *Logger) SetRetentionDays(days int) {
	if l != nil {
		l.retentionDays = days
	}
}

// getLogFile returns the log file for today, creating it if necessary
func (l *Logger) getLogFile() (*os.File, error) {
	if l.level == LogLevelOff {
		return nil, nil
	}

	today := time.Now().Format("2006-01-02")
	logPath := filepath.Join(l.logsDir, today+".log")

	// Create logs directory if it doesn't exist (.polis/logs — restricted)
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
		l.file.Close()
	}

	// Open new log file
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
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
			fmt.Fprintln(os.Stdout, string(data))
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
func (l *Logger) Event(event string, fields map[string]interface{}) {
	if l == nil || l.level < LogLevelBasic {
		return
	}

	// Check category filter: category is the segment after "pub.polis."
	if len(l.disabledEvents) > 0 {
		category := eventCategory(event)
		if category != "" && l.disabledEvents[category] {
			return
		}
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
			"source": "webapp",
			"action": event,
		}
		for k, v := range fields {
			obj[k] = v
		}
		data, err := json.Marshal(obj)
		if err == nil {
			fmt.Fprintln(os.Stdout, string(data))
		}
	}
}

// eventCategory extracts the category from a pub.polis.* event name.
// e.g., "pub.polis.post.publish" → "post", "pub.polis.comment.blessing.grant" → "comment"
func eventCategory(event string) string {
	const prefix = "pub.polis."
	if !strings.HasPrefix(event, prefix) {
		return ""
	}
	rest := event[len(prefix):]
	if idx := strings.Index(rest, "."); idx > 0 {
		return rest[:idx]
	}
	return rest
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
			fmt.Fprintln(os.Stdout, string(data))
		}
	}
}

// Close closes the log file
func (l *Logger) Close() {
	if l != nil && l.file != nil {
		l.file.Close()
	}
}

// PruneLogs deletes log files older than retentionDays.
// Short-circuits if less than 24h since last prune. Nil-safe.
func (l *Logger) PruneLogs() {
	if l == nil || l.retentionDays <= 0 {
		return
	}

	l.mu.Lock()
	if !l.lastPruneAt.IsZero() && time.Since(l.lastPruneAt) < 24*time.Hour {
		l.mu.Unlock()
		return
	}
	l.lastPruneAt = time.Now()
	l.mu.Unlock()

	entries, err := os.ReadDir(l.logsDir)
	if err != nil {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -l.retentionDays)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			os.Remove(filepath.Join(l.logsDir, e.Name()))
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
		s.Logger.Event(event, fields)
	}
}

// NewDSClient creates a discovery client with request ID propagation and timing.
// Use for handlers that have an http.Request context.
// IsRegisteredWithDS checks whether this site has a local registration marker
// for the configured discovery service. Pure filesystem check — no network call.
func (s *Server) IsRegisteredWithDS() bool {
	return discovery.IsRegisteredLocally(s.DataDir, s.DiscoveryURL)
}

func (s *Server) NewDSClient(r *http.Request) *discovery.Client {
	var client *discovery.Client
	if s.SharedHTTPClient != nil {
		client = discovery.NewClientWithHTTP(s.DiscoveryURL, s.DiscoveryKey, s.SharedHTTPClient)
	} else {
		client = discovery.NewClient(s.DiscoveryURL, s.DiscoveryKey)
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
		client = discovery.NewAuthenticatedClientWithHTTP(s.DiscoveryURL, s.DiscoveryKey, domain, s.PrivateKey, s.SharedHTTPClient)
	} else {
		client = discovery.NewAuthenticatedClient(s.DiscoveryURL, s.DiscoveryKey, domain, s.PrivateKey)
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
		DiscoveryKey: s.DiscoveryKey,
		BaseURL:      s.BaseURL,
		Generator:    "polis-cli-go/" + s.CLIVersion,
	}
}

// loadOrDefaultBundle loads the site's bundle.json if present, otherwise returns
// the default pub.polis.core bundle.
func (s *Server) loadOrDefaultBundle() *bundle.Bundle {
	bundlePath := filepath.Join(s.DataDir, "content", "pub.polis.core", "bundle.json")
	if b, err := bundle.LoadBundle(bundlePath); err == nil {
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
	resolver := resolve.New(s.DataDir, bundles)

	return ops.NewEngine(ops.EngineConfig{
		Resolver:     resolver,
		Bundles:      bundles,
		PrivateKey:   s.PrivateKey,
		PublicKey:    s.PublicKey,
		BaseURL:      s.GetBaseURL(),
		DiscoveryURL: s.DiscoveryURL,
		DiscoveryKey: s.DiscoveryKey,
		CLIThemesDir: s.CLIThemesDir,
		OnContentChanged: func() {
			if err := s.RenderSite(); err != nil {
				s.LogWarn("post-write render failed: %v", err)
			}
		},
	})
}

// RenderSite renders all pages after publish/republish operations.
// This ensures HTML files are updated and hooks can act on the complete output.
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
		DataDir:           s.DataDir,
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
	shape, err := bundle.GetActiveShapeName(s.DataDir)
	if err != nil || shape != "v4" {
		// v3 or unknown → existing full-corpus path. Preserves byte-identical
		// output for blog-shape tenants.
		return s.RenderSite()
	}

	baseURL := s.GetBaseURL()
	coreBundle := s.loadOrDefaultBundle()
	postsSource, _ := coreBundle.ContentDir("pub.polis.post")
	postsMountDir, _ := coreBundle.MountDir("pub.polis.post")
	commentsSource, _ := coreBundle.ContentDir("pub.polis.comment")
	commentsMountDir, _ := coreBundle.MountDir("pub.polis.comment")

	renderer, err := render.NewPageRenderer(render.PageConfig{
		DataDir:           s.DataDir,
		CLIThemesDir:      s.CLIThemesDir,
		BaseURL:           baseURL,
		PostsSourceDir:    postsSource,
		PostsMountDir:     postsMountDir,
		CommentsSourceDir: commentsSource,
		CommentsMountDir:  commentsMountDir,
	})
	if err != nil {
		s.LogError("v4 incremental renderer init failed: %v", err)
		return fmt.Errorf("create renderer: %w", err)
	}
	if err := renderer.PublishStream(postSourceRelPath); err != nil {
		s.LogError("v4 PublishStream cascade failed: %v", err)
		return fmt.Errorf("v4 cascade: %w", err)
	}
	s.LogEvent("pub.polis.site.render", map[string]interface{}{
		"mode": "v4-incremental",
		"path": postSourceRelPath,
	})
	return nil
}

// LoadConfig loads the webapp configuration from webapp/config.json
func (s *Server) LoadConfig() {
	configPath := filepath.Join(s.DataDir, ".polis", "webapp", "config.json")
	data, err := os.ReadFile(configPath)
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
	configPath := filepath.Join(s.DataDir, ".polis", "webapp", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return err
	}
	// Clear deprecated fields before saving (don't persist them)
	savedSubdomain := s.Config.Subdomain
	s.Config.Subdomain = ""
	data, err := json.MarshalIndent(s.Config, "", "  ")
	s.Config.Subdomain = savedSubdomain // Restore in memory for runtime use
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0644)
}

// LoadKeys loads the private and public keys from the keys directory
func (s *Server) LoadKeys() {
	privPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519")
	pubPath := filepath.Join(s.DataDir, ".polis", "keys", "id_ed25519.pub")

	priv, err := os.ReadFile(privPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[warning] Could not read private key %s: %v", privPath, err)
		}
		return
	}
	pub, err := os.ReadFile(pubPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[warning] Could not read public key %s: %v", pubPath, err)
		}
		return
	}
	s.PrivateKey = priv
	s.PublicKey = pub
}

// LoadEnv reads the .env file and applies discovery service settings.
// This matches the bash CLI behavior where DISCOVERY_SERVICE_URL and DISCOVERY_SERVICE_KEY
// are read from .env file rather than being stored in config.json.
//
// Search order:
// 1. Data directory .env (where the polis site data lives)
// 2. Current working directory .env (user's polis site)
// 3. ~/.polis/.env (fallback for multi-site setups)
func (s *Server) LoadEnv() {
	var envPath string
	var data []byte
	var err error

	// First, try the data directory (where webapp/config.json and keys live)
	envPath = filepath.Join(s.DataDir, ".env")
	data, err = os.ReadFile(envPath)
	if err == nil {
		log.Printf("[i] Loaded .env from data directory: %s", envPath)
	}

	// If not found, try current working directory
	if err != nil {
		cwd, cwdErr := os.Getwd()
		if cwdErr == nil {
			envPath = filepath.Join(cwd, ".env")
			data, err = os.ReadFile(envPath)
			if err == nil {
				log.Printf("[i] Loaded .env from current directory: %s", envPath)
			}
		}
	}

	// If still not found, try ~/.polis/.env as fallback
	if err != nil {
		homeDir, homeErr := os.UserHomeDir()
		if homeErr == nil {
			envPath = filepath.Join(homeDir, ".polis", ".env")
			data, err = os.ReadFile(envPath)
			if err == nil {
				log.Printf("[i] Loaded .env from fallback: %s", envPath)
			}
		}
	}

	// If still not found, that's fine - just return
	if err != nil {
		return
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

	// Apply discovery service settings from .env (single source of truth, like CLI)
	if url := env["DISCOVERY_SERVICE_URL"]; url != "" {
		s.DiscoveryURL = url
	}
	if key := env["DISCOVERY_SERVICE_KEY"]; key != "" {
		s.DiscoveryKey = key
	}

	// Store POLIS_BASE_URL for runtime use (matches bash CLI behavior)
	// This is the authoritative source for base_url - not stored in .well-known/polis
	if baseURL := env["POLIS_BASE_URL"]; baseURL != "" {
		s.BaseURL = strings.TrimSuffix(baseURL, "/")
	}

	// Log level and retention from .env
	if v := env["LOG_LEVEL"]; v != "" {
		if level, err := strconv.Atoi(v); err == nil && level >= 0 {
			s.LogLevel = level
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
	wk, err := site.LoadWellKnown(s.DataDir)
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
	wk, err := site.LoadWellKnown(s.DataDir)
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
	listener.Close()
	return port, nil
}

// OpenBrowser opens the default browser to the given URL.
func OpenBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		fmt.Printf("[i] Please open %s in your browser\n", url)
		return
	}
	if err := cmd.Start(); err != nil {
		fmt.Printf("[i] Please open %s in your browser\n", url)
	}
}

// FindCLIThemesDir locates the cli/themes directory for fallback theme snippets.
// It searches upward from the given directory to find the repo root.
func FindCLIThemesDir(startDir string) string {
	// Start from startDir and search upward
	dir := startDir
	for i := 0; i < 10; i++ { // Max 10 levels up
		themesPath := filepath.Join(dir, "cli-bash", "themes")
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
		site.Version = s.CLIVersion
		tag.Version = s.CLIVersion
	}

	// Migrate .polis/drafts -> .polis/bundles/pub.polis.core/posts/drafts if needed
	s.migrateDraftsDir()

	// Validate the site first - only load keys/config if valid
	validation := site.Validate(s.DataDir)
	if validation.Status == site.StatusValid {
		// Load existing config if present
		s.LoadConfig()
		s.LoadKeys()
	}

	// Load .env file for discovery service settings (overrides webapp/config.json)
	s.LoadEnv()

	// Apply default discovery URL if not set by config or .env (matches CLI behavior)
	s.ApplyDiscoveryDefaults()

	// Propagate discovery config to packages that register with discovery.
	// This ensures publish and comment packages handle registration internally.
	publish.DiscoveryURL = s.DiscoveryURL
	publish.DiscoveryKey = s.DiscoveryKey
	publish.BaseURL = s.BaseURL
	comment.DiscoveryURL = s.DiscoveryURL
	comment.DiscoveryKey = s.DiscoveryKey
	comment.BaseURL = s.BaseURL
	stream.DiscoveryURL = s.DiscoveryURL
	stream.DiscoveryKey = s.DiscoveryKey
	stream.BaseURL = s.BaseURL
	stream.DataDir = s.DataDir
	tag.DataDir = s.DataDir
	following.DataDir = s.DataDir
	following.DiscoveryURL = s.DiscoveryURL

	// One-time migration: consolidate author → author_name in .well-known/polis
	site.MigrateAuthorField(s.DataDir)

	// Apply log defaults and write back to .env so settings are visible
	if s.LogLevel == 0 {
		s.LogLevel = LogLevelBasic
	}
	if s.LogRetentionDays == 0 {
		s.LogRetentionDays = DefaultLogRetentionDays
	}

	envPath := filepath.Join(s.DataDir, ".env")
	s.writeEnvFile(envPath, map[string]string{
		"LOG_LEVEL":          strconv.Itoa(s.LogLevel),
		"LOG_RETENTION_DAYS": strconv.Itoa(s.LogRetentionDays),
	})

	// Create logger (disk always; JSON stdout when LOG_FORMAT=json)
	logsDir := filepath.Join(s.DataDir, ".polis", "logs")
	s.Logger = NewLogger(s.LogLevel, logsDir)
	s.Logger.retentionDays = s.LogRetentionDays

	// Enable JSON stdout output when LOG_FORMAT=json (for Axiom/Vector ingestion)
	if os.Getenv("LOG_FORMAT") == "json" {
		s.Logger.jsonOutput = true
	}

	// Parse LOG_EVENTS_DISABLED for category-level filtering (e.g., "feed,site")
	if disabled := os.Getenv("LOG_EVENTS_DISABLED"); disabled != "" {
		s.Logger.disabledEvents = make(map[string]bool)
		for _, cat := range strings.Split(disabled, ",") {
			cat = strings.TrimSpace(cat)
			if cat != "" {
				s.Logger.disabledEvents[cat] = true
			}
		}
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
	// See operational-hardening.md R6-1 for the tracked fix.
	// s.AuthorKeyCache = discovery.NewAuthorKeyCache(0, 0)
}

// migrateDraftsDir migrates .polis/drafts to .polis/bundles/pub.polis.core/posts/drafts if needed.
func (s *Server) migrateDraftsDir() {
	oldPath := filepath.Join(s.DataDir, ".polis", "drafts")
	newPath := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")

	// Only migrate if old path exists and new path doesn't
	oldInfo, oldErr := os.Stat(oldPath)
	_, newErr := os.Stat(newPath)

	if oldErr == nil && oldInfo.IsDir() && os.IsNotExist(newErr) {
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
// Used after user-initiated mutations (publish, bless, follow) to push
// updated counts to SSE clients within milliseconds.
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
	s.sseClients = make(map[chan SSEEvent]struct{})

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
				// Only sync when browser tabs are connected — idle cached
				// tenants skip the DS query entirely. On-demand sync via
				// syncTrigger (SSE connect, wake endpoint) still fires
				// immediately when a client arrives.
				if s.hasSSEClients() {
					s.runUnifiedSync()
				}
				if s.Logger != nil {
					s.Logger.PruneLogs()
				}
			case <-s.syncTrigger:
				s.runUnifiedSync()
			}
		}
	}()
}

// hasSSEClients returns true if any browser tabs are connected via SSE.
func (s *Server) hasSSEClients() bool {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	return len(s.sseClients) > 0
}

// addSSEClient registers a client channel for SSE events.
func (s *Server) addSSEClient(ch chan SSEEvent) {
	s.sseMu.Lock()
	wasEmpty := len(s.sseClients) == 0
	s.sseClients[ch] = struct{}{}
	count := len(s.sseClients)
	s.sseMu.Unlock()
	s.LogEvent("sse.client.connected", map[string]interface{}{
		"active_clients": count,
	})
	if wasEmpty {
		s.TriggerSync()
	}
}

// removeSSEClient unregisters a client channel.
func (s *Server) removeSSEClient(ch chan SSEEvent) {
	s.sseMu.Lock()
	delete(s.sseClients, ch)
	count := len(s.sseClients)
	s.sseMu.Unlock()
	close(ch)
	s.LogEvent("sse.client.disconnected", map[string]interface{}{
		"active_clients": count,
	})
}

// broadcastSSE sends an event to all connected SSE clients.
func (s *Server) broadcastSSE(evt SSEEvent) {
	s.sseMu.Lock()
	defer s.sseMu.Unlock()
	for ch := range s.sseClients {
		select {
		case ch <- evt:
		default:
			// Client channel full, skip (they'll get the next one)
		}
	}
}

// broadcastCounts computes all badge counts and pushes them to SSE clients.
func (s *Server) broadcastCounts(syncResult SyncResult) {
	if s.sseClients == nil {
		return
	}
	s.sseMu.Lock()
	clientCount := len(s.sseClients)
	s.sseMu.Unlock()
	if clientCount == 0 {
		return
	}

	counts := s.computeAllCounts()
	data, err := json.Marshal(counts)
	if err != nil {
		return
	}
	s.broadcastSSE(SSEEvent{Event: "counts", Data: string(data)})
}

// CountsPayload contains all badge counts for the frontend.
type CountsPayload struct {
	Posts            int `json:"posts"`
	Drafts           int `json:"drafts"`
	MyPending        int `json:"my_pending"`
	MyBlessed        int `json:"my_blessed"`
	MyDenied         int `json:"my_denied"`
	MyCommentDrafts  int `json:"my_comment_drafts"`
	IncomingPending  int `json:"incoming_pending"`
	IncomingBlessed  int `json:"incoming_blessed"`
	FeedUnread       int    `json:"feed_unread"`
	HasNewFeed       bool   `json:"has_new_feed"`
	// HasNewBlessingInbox / HasNewDM follow the same "new since last view"
	// pattern as HasNewFeed — each clears when the user activates the
	// corresponding filter surface (see /api/comment/blessing/viewed and
	// /api/dm/viewed).
	HasNewBlessingInbox bool `json:"has_new_blessing_inbox"`
	HasNewDM            bool `json:"has_new_dm"`
	Following        int `json:"following"`
	Followers        int `json:"followers"`
	NotificationsUnread int `json:"notifications_unread"`
	BlessingRequests int `json:"blessing_requests"`
	DMUnread         int `json:"dm_unread"`
}

// computeAllCounts reads all badge counts from local state/filesystem.
// No DS queries — everything comes from cached state.
func (s *Server) computeAllCounts() CountsPayload {
	counts := CountsPayload{}

	// Posts — read from index.jsonl (handles date-based subdirectories)
	indexPath := filepath.Join(s.DataDir, "content", "pub.polis.core", "index.jsonl")
	if data, err := os.ReadFile(indexPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			// Skip comment entries in index.jsonl
			if !strings.Contains(line, "pub.polis.core/comment/") {
				counts.Posts++
			}
		}
	}

	// Drafts
	draftsDir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts")
	if entries, err := os.ReadDir(draftsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				counts.Drafts++
			}
		}
	}

	// My comments by status (pending/denied are flat in .polis/bundles/pub.polis.core/comments/<status>/)
	for _, status := range []struct {
		dir  string
		dest *int
	}{
		{"pending", &counts.MyPending},
		{"denied", &counts.MyDenied},
	} {
		dir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "comments", status.dir)
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
					*status.dest++
				}
			}
		}
	}

	// My blessed comments live in content/pub.polis.core/comment/YYYYMMDD/ (date-based subdirs)
	blessedDir := filepath.Join(s.DataDir, "content", "pub.polis.core", "comment")
	if dateDirs, err := os.ReadDir(blessedDir); err == nil {
		for _, dd := range dateDirs {
			if !dd.IsDir() {
				continue
			}
			subDir := filepath.Join(blessedDir, dd.Name())
			if files, err := os.ReadDir(subDir); err == nil {
				for _, f := range files {
					if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
						counts.MyBlessed++
					}
				}
			}
		}
	}

	// Comment drafts
	commentDraftsDir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "comments", "drafts")
	if entries, err := os.ReadDir(commentDraftsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				counts.MyCommentDrafts++
			}
		}
	}

	// Incoming blessed comments (on our posts)
	blessedIndex := filepath.Join(s.DataDir, "content", "pub.polis.core", "comment", "blessed.json")
	if data, err := os.ReadFile(blessedIndex); err == nil {
		var idx map[string]interface{}
		if json.Unmarshal(data, &idx) == nil {
			if posts, ok := idx["posts"].(map[string]interface{}); ok {
				for _, v := range posts {
					if comments, ok := v.([]interface{}); ok {
						counts.IncomingBlessed += len(comments)
					}
				}
			}
		}
	}

	// Following
	followingPath := following.DefaultPath(s.DataDir)
	if f, err := following.Load(followingPath); err == nil {
		counts.Following = f.Count()
	}

	// Feed unread count
	cm := s.feedCacheForScope("network")
	if items, err := cm.List(); err == nil {
		for _, item := range items {
			if item.ReadAt == "" {
				counts.FeedUnread++
			}
		}
	}

	// Followers (from cached state)
	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")
	var followerState stream.FollowerState
	if store.LoadState("pub.polis.follow", &followerState) == nil {
		counts.Followers = followerState.Count
	}

	// Incoming pending blessing requests — read from DS-cached blessing state
	var blessingState stream.BlessingState
	if store.LoadState("pub.polis.comment.blessing", &blessingState) == nil {
		for _, b := range blessingState.Blessings {
			if b.Status == "pending" {
				counts.BlessingRequests++
				counts.IncomingPending++
			}
		}
	}

	// DM unread count
	if s.PrivateKey != nil {
		if dmStore, err := s.dmStore(); err == nil {
			if idx, err := dmStore.LoadIndex(); err == nil {
				for _, c := range idx.Conversations {
					counts.DMUnread += c.UnreadCount
				}
			}
		}
	}

	// has-new flags: each surface uses a "viewed" cursor that the SPA
	// advances when the corresponding filter view becomes active. The dot
	// fires when sync has written something past that cursor.
	//
	// Cold-start: when a viewed cursor is empty (fresh tenant, or a tenant
	// that predates this code), seed it to the current high-water mark and
	// keep the flag false. Avoids lighting all three dots on first load
	// when the user has had no opportunity to acknowledge anything.
	syncPos, _ := store.GetCursor("pub.polis.sync")

	feedViewedPos, _ := store.GetCursor("pub.polis.feed.viewed")
	if feedViewedPos == "" || feedViewedPos == "0" {
		if syncPos != "" {
			_ = store.SetCursor("pub.polis.feed.viewed", syncPos)
		}
		counts.HasNewFeed = false
	} else {
		counts.HasNewFeed = syncPos != "" && syncPos != feedViewedPos
	}

	// Blessing inbox: dot fires when sync has advanced past the inbox-viewed
	// cursor AND there's at least one currently-pending blessing request
	// (no point flagging "new since last view" when nothing's actionable).
	blessingViewedPos, _ := store.GetCursor("pub.polis.comment.blessing.viewed")
	if blessingViewedPos == "" || blessingViewedPos == "0" {
		if syncPos != "" {
			_ = store.SetCursor("pub.polis.comment.blessing.viewed", syncPos)
		}
		counts.HasNewBlessingInbox = false
	} else {
		counts.HasNewBlessingInbox = syncPos != "" &&
			syncPos != blessingViewedPos &&
			counts.IncomingPending > 0
	}

	// DM surface: cursor is an RFC3339 timestamp (DMs are local-only).
	// Fast-path on the conversation summary — per-conversation UnreadCount
	// already excludes outgoing messages (see dm/store.go updateIndex),
	// so any conversation with UnreadCount > 0 AND LastMessageAt > viewed
	// is a "new since last view" hit. No per-message scan needed.
	dmViewedPos, _ := store.GetCursor("pub.polis.dm.viewed")
	if dmViewedPos == "" || dmViewedPos == "0" {
		_ = store.SetCursor("pub.polis.dm.viewed", time.Now().UTC().Format(time.RFC3339Nano))
		counts.HasNewDM = false
	} else if s.PrivateKey != nil {
		if dmStore, err := s.dmStore(); err == nil {
			if idx, err := dmStore.LoadIndex(); err == nil {
				for _, c := range idx.Conversations {
					if c.UnreadCount > 0 && c.LastMessageAt > dmViewedPos {
						counts.HasNewDM = true
						break
					}
				}
			}
		}
	}

	return counts
}

// dmStore creates a DM store from the server's data dir and private key.
func (s *Server) dmStore() (*dm.Store, error) {
	if s.PrivateKey == nil {
		return nil, fmt.Errorf("no private key configured")
	}
	privKey, err := signing.ParsePrivateKey(s.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	defer signing.ZeroKey(privKey)
	return dm.NewStore(s.DataDir, privKey.Seed())
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
	pendingDir := filepath.Join(s.DataDir, ".polis", "bundles", "pub.polis.core", "comments", "pending")
	entries, err := os.ReadDir(pendingDir)
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

	result, err := comment.SyncPendingComments(s.DataDir, baseURL, client, hc)
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
	followingPath := following.DefaultPath(s.DataDir)
	f, err := following.Load(followingPath)
	if err != nil {
		log.Printf("[feed-sync] skip: following load error: %v", err)
		// Load error could be transient (file lock, partial write) —
		// don't mark checked, let the next cycle retry.
		return
	}
	if f.Count() == 0 {
		log.Printf("[feed-sync] skip: following list is empty (path=%s)", followingPath)
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

// syncFeedScoped syncs a non-network feed scope that needs its own DS query.
// Currently only "global" qualifies — it queries DS with no actor filter + created_after=24h.
// "followers" and "me" are runtime-filtered over the network cache and don't need separate sync.
func (s *Server) syncFeedScoped(scope string) {
	// scope=="" / "network" / runtime-filtered are not handled here (see
	// syncFeed). Other invalid invocations short-circuit without touching
	// the cache.
	if scope == "" || scope == "network" || runtimeFilteredScopes[scope] {
		return
	}

	cm := s.feedCacheForScope(scope)

	// Structural early-returns mark the cache as checked — see syncFeed
	// for the rationale.
	if s.DiscoveryURL == "" {
		_ = cm.MarkChecked()
		return
	}
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		_ = cm.MarkChecked()
		return
	}
	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		_ = cm.MarkChecked()
		return
	}

	cursor, _ := cm.GetCursor()

	client := s.NewDSClient(nil)
	client.RequestID = generateBackgroundRequestID("feed-sync-" + scope)
	typeFilter := "pub.polis.post.published,pub.polis.post.republished,pub.polis.comment.published,pub.polis.comment.republished,pub.polis.comment.blessing.granted,pub.polis.comment.blessing.requested,pub.polis.follow.announced,pub.polis.site.registered"

	var actorFilter string
	followedDomains := make(map[string]bool)

	switch scope {
	case "global":
		client.CreatedAfter = time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
		// No actor filter — query all of polis
	}

	log.Printf("[feed-sync-%s] querying stream: cursor=%q actors=%q", scope, cursor, actorFilter)

	result, err := client.StreamQuery(cursor, 1000, typeFilter, actorFilter, "")
	if err != nil {
		log.Printf("[feed-sync-%s] stream query failed: %v", scope, err)
		return
	}

	log.Printf("[feed-sync-%s] stream returned %d events", scope, len(result.Events))

	handler := &feed.FeedHandler{
		MyDomain:        myDomain,
		FollowedDomains: followedDomains,
		IncludeSelf:     scope == "me",
	}

	items := handler.Process(result.Events)
	// R21: same fix as syncFeed (network) — pruning losses no longer
	// freeze the cursor. See the syncFeed comment for rationale.
	mergeFailed := false
	if len(items) > 0 {
		mr, err := cm.MergeItemsResult(items)
		if err != nil {
			log.Printf("[feed-sync-%s] merge failed: %v", scope, err)
			mergeFailed = true
		} else {
			log.Printf("[feed-sync-%s] merged %d new items, %d retained", scope, mr.Added, mr.Retained)
			if mr.Added > 0 && mr.Retained < mr.Added {
				log.Printf("[feed-sync-%s] %d items pruned on insert (advancing cursor anyway)", scope, mr.Added-mr.Retained)
			}
		}
	}

	if result.Cursor != "" {
		if mergeFailed {
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

// Handler returns an http.Handler for this Server's API routes.
// Used by the hosted service to serve tenant requests without starting a
// standalone HTTP listener.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	SetupRoutes(mux, s)

	// Setup v1 content API routes (needed for DM delivery, content queries, etc.)
	if apiEngine, err := s.newContentEngine(); err == nil {
		api.SetupRoutes(mux, apiEngine, s.DataDir, func(event string, fields map[string]interface{}) {
			s.LogEvent(event, fields)
		})
	}

	return mux
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
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		// Create just the data directory (not the full structure)
		if err := os.MkdirAll(dataDir, 0755); err != nil {
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

	// Initialize server
	server := NewServer(dataDir, cliThemesDir)
	server.EnableHooks = true // Localhost mode: hooks are safe to run
	if len(opts) > 0 && opts[0].CLIVersion != "" {
		server.CLIVersion = opts[0].CLIVersion
	}
	server.Initialize()
	defer server.Close()

	// Start background sync (notifications + feed). Mirror mode skips —
	// the data dir is a clone, not the operator's tenant; no DS announce
	// or auto-register applies.
	if dataMode != DataModeMirror {
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
	mux := http.NewServeMux()
	if surface == SurfaceReader {
		SetupReaderRoutes(mux, server)
		mux.Handle("/", tenantStaticHandler(server))
	} else {
		SetupRoutes(mux, server)

		// Setup v1 content API routes (alongside existing /api/ routes)
		if apiEngine, err := server.newContentEngine(); err != nil {
			log.Printf("[warning] v1 API not available: %v", err)
		} else {
			api.SetupRoutes(mux, apiEngine, dataDir, func(event string, fields map[string]interface{}) {
				server.LogEvent(event, fields)
			})
			log.Printf("[info] v1 content API enabled")
		}

		// Bundle reference assets (step-06/6.a). Serves the v4 stream shape's
		// stream.js / stream.css / snippet HTML to the SPA homepage so it can
		// hydrate against the canonical controller without duplicating files
		// in webapp/internal/webui/www. Path shape:
		//   /bundle-assets/pub.polis.core/shapes/v4/stream.js
		//   /bundle-assets/pub.polis.core/shapes/v4/stream.css
		//   /bundle-assets/pub.polis.core/shapes/v4/snippets/stream-filter.html
		mux.Handle("/bundle-assets/", http.StripPrefix("/bundle-assets/", http.FileServer(http.FS(bundle.ReferencePayloadFS()))))

		// Static files from embedded filesystem with SPA fallback
		mux.Handle("/", spaHandler(webFS, dataDir))
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

	// Wrap mux with middleware: security headers (outermost) → request logging → mux
	handler := securityHeadersMiddleware(requestLoggingMiddleware(server.Logger, mux))

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal("Server error:", err)
	}
}

// spaHandler serves static files from the embedded filesystem, falling back
// to index.html for unknown paths (SPA deep-link support). For index.html
// itself (and the SPA fallback path), substitutes a {{theme_attr}}
// placeholder with the tenant's active theme attribute so first paint
// lands in the user's selected theme rather than the sols-flavored default
// (R23 #4 follow-up — the inline-script localStorage path covered repeat
// visits but not first-load).
func spaHandler(fsys fs.FS, dataDir string) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	serveTemplatedIndex := func(w http.ResponseWriter, r *http.Request) {
		// Sub-rooted at www/ in cmd/server/main.go via fs.Sub, so paths
		// here are relative to that root (just "index.html", not the
		// full embed-relative path).
		raw, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		var attr string
		if name, _ := bundle.GetActiveThemeName(dataDir); name != "" {
			// Bare attribute injection — the value is constrained by
			// SetActiveThemeName to ParseFQN-validated tokens (alnum +
			// `-`/`_`), so direct interpolation is safe.
			attr = ` data-site-theme="` + name + `"`
		}
		out := strings.Replace(string(raw), "{{theme_attr}}", attr, 1)
		// R18-21: per-response CSP nonce. Substituted into the importmap
		// script tag's nonce= attribute; matches the admin-SPA CSP set
		// below. ReplaceAll because the placeholder appears in both an
		// HTML comment (descriptive text) AND the actual nonce attribute
		// — only substituting the first occurrence leaves the real
		// attribute as the literal placeholder string, which doesn't
		// match the CSP header and gets the importmap blocked.
		nonce := GenerateCSPNonce()
		out = strings.ReplaceAll(out, "{{csp_nonce}}", nonce)
		// R18-21: override the default tenant-content CSP from
		// securityHeadersMiddleware with the admin-SPA variant that
		// permits this nonce + the Milkdown esm.sh CDN.
		w.Header().Set("Content-Security-Policy", CSPAdminSPA(nonce))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(out))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" || path == "index.html" {
			serveTemplatedIndex(w, r)
			return
		}
		// If the file exists in the embedded FS, serve it directly
		if _, err := fs.Stat(fsys, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve templated index.html for deep-link paths
		serveTemplatedIndex(w, r)
	})
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

// validFeedScopes lists the accepted scope values for feed queries.
//
// step-06/6.f.2 review item #6 — known parallel of the scope=my bug
// fixed in /api/v1/stream/items at a3de221. The "me" scope here also
// routes through feedCacheForScope("me") → returns the network cache
// (per runtimeFilteredScopes) which self-skips own events — so /api/
// feed?scope=me returns 0 items in production despite local data
// existing on disk. The fix mirror would be to route through
// loadOwnContentAsFeedItems for scope=me, but /api/feed has a
// different response shape than /api/v1/stream/items and the legacy
// SPA views still using it would need verification.
//
// Deferred for now (low priority) since the v4 stream endpoint is the
// new canonical surface; legacy /api/feed is on the deprecation path
// (6.l excerpt-fetch + 6.m route cleanup). Revisit if a non-migrated
// view surfaces a regression.
var validFeedScopes = map[string]bool{
	"network":   true,
	"followers": true,
	"global":    true,
	"me":        true,
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
		cm = feed.NewCacheManager(s.DataDir, discoveryDomain)
	} else {
		cm = feed.NewScopedCacheManager(s.DataDir, discoveryDomain, key)
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
		store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")
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
