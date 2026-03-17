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
	"github.com/vdibart/polis-cli/cli-go/pkg/notification"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
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

	// View mode: "list" or "browser"
	ViewMode string `json:"view_mode,omitempty"`

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

	// SSE client registry
	sseClients map[chan SSEEvent]struct{}
	sseMu      sync.Mutex

	syncDone chan struct{} // closed by StopSync() to stop the background goroutine
}

// policyCacheEntry holds a cached policy check result with expiry.
type policyCacheEntry struct {
	Status    string
	Reason    string
	FollowsUs bool
	ExpiresAt time.Time
}

// policyCheckCacheTTL is how long policy check results are cached.
const policyCheckCacheTTL = 5 * time.Minute

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
			"source": "localhost",
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

	// Emit JSON to stdout when enabled
	if l.jsonOutput {
		obj := map[string]interface{}{
			"ts":     now.UTC().Format(time.RFC3339),
			"source": "localhost",
			"event":  event,
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
func (s *Server) NewDSClient(r *http.Request) *discovery.Client {
	client := discovery.NewClient(s.DiscoveryURL, s.DiscoveryKey)
	if r != nil {
		client.RequestID = RequestIDFromContext(r.Context())
	}
	s.setDSLogger(client)
	return client
}

// NewAuthDSClient creates an authenticated discovery client with request ID propagation.
func (s *Server) NewAuthDSClient(r *http.Request, domain string) *discovery.Client {
	client := discovery.NewAuthenticatedClient(s.DiscoveryURL, s.DiscoveryKey, domain, s.PrivateKey)
	if r != nil {
		client.RequestID = RequestIDFromContext(r.Context())
	}
	s.setDSLogger(client)
	return client
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
	// Propagate CLI version to packages that embed it in metadata
	if s.CLIVersion != "" {
		publish.Version = s.CLIVersion
		comment.Version = s.CLIVersion
		metadata.Version = s.CLIVersion
		following.Version = s.CLIVersion
		feed.Version = s.CLIVersion
		site.Version = s.CLIVersion
		notification.Version = s.CLIVersion
		theme.Version = s.CLIVersion
	}

	// Migrate .polis/drafts -> .polis/content/pub.polis.core/posts/drafts if needed
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

// migrateDraftsDir migrates .polis/drafts to .polis/content/pub.polis.core/posts/drafts if needed.
func (s *Server) migrateDraftsDir() {
	oldPath := filepath.Join(s.DataDir, ".polis", "drafts")
	newPath := filepath.Join(s.DataDir, ".polis", "content", "pub.polis.core", "posts", "drafts")

	// Only migrate if old path exists and new path doesn't
	oldInfo, oldErr := os.Stat(oldPath)
	_, newErr := os.Stat(newPath)

	if oldErr == nil && oldInfo.IsDir() && os.IsNotExist(newErr) {
		// Create parent directory (.polis/content — restricted)
		if err := os.MkdirAll(filepath.Dir(newPath), 0700); err != nil {
			log.Printf("[warning] Failed to create parent directory for drafts migration: %v", err)
			return
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			log.Printf("[warning] Failed to migrate drafts directory: %v", err)
		} else {
			log.Printf("[i] Migrated drafts: .polis/drafts -> .polis/content/pub.polis.core/posts/drafts")
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
	s.RegisterSyncHandler(&notificationSyncHandler{server: s})
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
	Feed             int `json:"feed"`
	FeedUnread       int `json:"feed_unread"`
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
	draftsDir := filepath.Join(s.DataDir, ".polis", "content", "pub.polis.core", "posts", "drafts")
	if entries, err := os.ReadDir(draftsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				counts.Drafts++
			}
		}
	}

	// My comments by status (pending/denied are flat in .polis/content/pub.polis.core/comments/<status>/)
	for _, status := range []struct {
		dir  string
		dest *int
	}{
		{"pending", &counts.MyPending},
		{"denied", &counts.MyDenied},
	} {
		dir := filepath.Join(s.DataDir, ".polis", "content", "pub.polis.core", "comments", status.dir)
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
	commentDraftsDir := filepath.Join(s.DataDir, ".polis", "content", "pub.polis.core", "comments", "drafts")
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

	// Feed counts
	discoveryDomain := s.GetDiscoveryDomain()
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)
	if items, err := cm.List(); err == nil {
		counts.Feed = len(items)
		for _, item := range items {
			if item.ReadAt == "" {
				counts.FeedUnread++
			}
		}
	}

	// Followers (from cached state)
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")
	var followerState stream.FollowerState
	if store.LoadState("pub.polis.follow", &followerState) == nil {
		counts.Followers = followerState.Count
	}

	// Notification unread count
	mgr := notification.NewManager(s.DataDir, discoveryDomain)
	if unread, err := mgr.CountUnread(); err == nil {
		counts.NotificationsUnread = unread
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
	pendingDir := filepath.Join(s.DataDir, ".polis", "content", "pub.polis.core", "comments", "pending")
	entries, err := os.ReadDir(pendingDir)
	if err != nil || len(entries) == 0 {
		return
	}

	myDomain := discovery.ExtractDomainFromURL(baseURL)
	client := s.NewAuthDSClient(nil, myDomain)
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

// syncNotifications runs the notification projection: queries the stream
// with separate queries per relevance group, applies rules, and appends
// new entries to state.jsonl.
func (s *Server) syncNotifications() {
	if s.DiscoveryURL == "" {
		return
	}
	baseURL := s.GetBaseURL()
	if baseURL == "" || s.PrivateKey == nil {
		return
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		return
	}

	discoveryDomain := s.GetDiscoveryDomain()
	store := stream.NewStore(s.DataDir, discoveryDomain, "pub.polis.core")

	// Load notification config (rules + muted domains)
	var config stream.NotificationConfig
	_ = store.LoadConfig("notifications", &config)

	// Seed rules from defaults if empty, or merge any new default rules
	rules := config.Rules
	if len(rules) == 0 {
		rules = notification.DefaultRules()
		config.Rules = rules
		_ = store.SaveConfig("notifications", &config)
	} else {
		// Merge new default rules not present in saved config
		defaults := notification.DefaultRules()
		existingIDs := make(map[string]bool, len(rules))
		for _, r := range rules {
			existingIDs[r.ID] = true
		}
		added := false
		for _, d := range defaults {
			if !existingIDs[d.ID] {
				rules = append(rules, d)
				added = true
			}
		}
		if added {
			config.Rules = rules
			_ = store.SaveConfig("notifications", &config)
			// Reset cursor so newly added rules can process past events
			_ = store.SetCursor("pub.polis.notification", "0")
			s.LogInfo("notification sync: added new default rules, cursor reset")
		}
	}

	// Build muted domains set
	mutedDomains := make(map[string]bool, len(config.MutedDomains))
	for _, d := range config.MutedDomains {
		mutedDomains[d] = true
	}

	handler := &stream.NotificationHandler{
		MyDomain:     myDomain,
		Rules:        rules,
		MutedDomains: mutedDomains,
	}

	// Get shared cursor
	cursor, _ := store.GetCursor("pub.polis.notification")

	myDomainForAuth := discovery.ExtractDomainFromURL(s.GetBaseURL())
	client := s.NewAuthDSClient(nil, myDomainForAuth)
	client.RequestID = generateBackgroundRequestID("notification-sync")

	// Group rules by relevance for targeted server-side filtering
	groups := handler.RulesByRelevance()
	var allEntries []notification.StateEntry
	newCursor := cursor

	// Query 1: target_domain rules
	if targetRules := groups["target_domain"]; len(targetRules) > 0 {
		var types []string
		for _, r := range targetRules {
			types = append(types, r.EventType)
		}
		typeFilter := discovery.JoinDomains(types)
		result, err := client.StreamQuery(cursor, 1000, typeFilter, "", myDomain)
		if err != nil {
			s.LogDebug("notification sync: target_domain query failed: %v", err)
		} else {
			entries := handler.Process(result.Events)
			allEntries = append(allEntries, entries...)
			if cursorGreater(result.Cursor, newCursor) {
				newCursor = result.Cursor
			}
		}
	}

	// Query 2: source_domain rules
	if sourceRules := groups["source_domain"]; len(sourceRules) > 0 {
		var types []string
		for _, r := range sourceRules {
			types = append(types, r.EventType)
		}
		typeFilter := discovery.JoinDomains(types)
		result, err := client.StreamQuery(cursor, 1000, typeFilter, "", "", myDomain)
		if err != nil {
			s.LogDebug("notification sync: source_domain query failed: %v", err)
		} else {
			entries := handler.Process(result.Events)
			allEntries = append(allEntries, entries...)
			if cursorGreater(result.Cursor, newCursor) {
				newCursor = result.Cursor
			}
		}
	}

	// Query 3: followed_author rules (only if enabled)
	if authorRules := groups["followed_author"]; len(authorRules) > 0 {
		// Load following list
		followingPath := following.DefaultPath(s.DataDir)
		f, err := following.Load(followingPath)
		if err == nil {
			var domains []string
			for _, entry := range f.All() {
				d := discovery.ExtractDomainFromURL(entry.URL)
				if d != "" {
					domains = append(domains, d)
				}
			}
			if len(domains) > 0 {
				var types []string
				for _, r := range authorRules {
					types = append(types, r.EventType)
				}
				typeFilter := discovery.JoinDomains(types)
				actorFilter := discovery.JoinDomains(domains)
				result, err := client.StreamQuery(cursor, 1000, typeFilter, actorFilter, "")
				if err != nil {
					s.LogDebug("notification sync: followed_author query failed: %v", err)
				} else {
					entries := handler.Process(result.Events)
					allEntries = append(allEntries, entries...)
					if cursorGreater(result.Cursor, newCursor) {
						newCursor = result.Cursor
					}
				}
			}
		}
	}

	// Append new entries to state.jsonl
	if len(allEntries) > 0 {
		mgr := notification.NewManager(s.DataDir, discoveryDomain)
		added, err := mgr.Append(allEntries)
		if err != nil {
			s.LogError("notification sync: failed to append entries: %v", err)
		} else if added > 0 {
			s.LogInfo("notification sync: added %d new notifications", added)

			// Prune old notifications to prevent unbounded growth
			pruneCfg := notification.DefaultPruneConfig()
			if config.MaxItems > 0 {
				pruneCfg.MaxItems = config.MaxItems
			}
			if config.MaxAgeDays > 0 {
				pruneCfg.MaxAgeDays = config.MaxAgeDays
			}
			if pruned, err := mgr.Prune(pruneCfg); err != nil {
				s.LogError("notification sync: prune failed: %v", err)
			} else if pruned > 0 {
				s.LogInfo("notification sync: pruned %d old notifications", pruned)
			}
		}
	}

	// Update cursor
	if newCursor != cursor {
		_ = store.SetCursor("pub.polis.notification", newCursor)
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
func (s *Server) syncFeed() {
	if s.DiscoveryURL == "" {
		log.Printf("[feed-sync] skip: no discovery config (url=%q)", s.DiscoveryURL)
		return
	}
	baseURL := s.GetBaseURL()
	if baseURL == "" {
		log.Printf("[feed-sync] skip: empty BaseURL (dataDir=%s)", s.DataDir)
		return
	}

	myDomain := extractDomainFromURL(baseURL)
	if myDomain == "" {
		log.Printf("[feed-sync] skip: could not extract domain from %s", baseURL)
		return
	}

	discoveryDomain := s.GetDiscoveryDomain()

	// Load following list to get followed domains
	followingPath := following.DefaultPath(s.DataDir)
	f, err := following.Load(followingPath)
	if err != nil {
		log.Printf("[feed-sync] skip: following load error: %v", err)
		return
	}
	if f.Count() == 0 {
		log.Printf("[feed-sync] skip: following list is empty (path=%s)", followingPath)
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
		return
	}

	// Load feed cursor
	cm := feed.NewCacheManager(s.DataDir, discoveryDomain)
	cursor, _ := cm.GetCursor()

	// Query DS stream with actor filter for followed domains
	client := s.NewDSClient(nil)
	client.RequestID = generateBackgroundRequestID("feed-sync")
	typeFilter := "pub.polis.post.published,pub.polis.post.republished,pub.polis.comment.published,pub.polis.comment.republished"
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

	// Merge into cache
	if len(items) > 0 {
		newCount, err := cm.MergeItems(items)
		if err != nil {
			log.Printf("[feed-sync] merge failed: %v", err)
		} else {
			log.Printf("[feed-sync] merged %d new items (total cached: check JSONL)", newCount)
		}
	}

	// Update cursor (always set to refresh LastUpdated, even if position unchanged)
	if result.Cursor != "" {
		_ = cm.SetCursor(result.Cursor)
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

// RunOptions contains optional configuration for the server.
type RunOptions struct {
	CLIVersion string // CLI version for metadata (empty = use package default)
}

// Run starts the HTTP server with the given embedded filesystem.
func Run(webFS fs.FS, dataDir string, opts ...RunOptions) {
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

	// Start background sync (notifications + feed)
	server.StartBackgroundSync()

	// Find available port
	port, err := FindAvailablePort()
	if err != nil {
		log.Fatal("Failed to find available port:", err)
	}

	// Setup routes
	mux := http.NewServeMux()
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

	// Static files from embedded filesystem with SPA fallback
	mux.Handle("/", spaHandler(webFS))

	addr := fmt.Sprintf("localhost:%d", port)
	url := fmt.Sprintf("http://%s", addr)

	fmt.Printf("[i] Starting polis server...\n")
	fmt.Printf("[i] Listening on %s\n", url)
	fmt.Printf("[i] Data directory: %s\n", dataDir)

	// Open browser after a short delay
	go func() {
		time.Sleep(500 * time.Millisecond)
		OpenBrowser(url)
	}()

	// Wrap mux with request logging middleware for correlation IDs and access logs
	handler := requestLoggingMiddleware(server.Logger, mux)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal("Server error:", err)
	}
}

// spaHandler serves static files from the embedded filesystem, falling back
// to index.html for unknown paths (SPA deep-link support).
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		// If the file exists in the embedded FS, serve it directly
		if _, err := fs.Stat(fsys, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback: serve index.html for deep-link paths
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
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

// extractDomainFromURL extracts the hostname from a URL string.
func extractDomainFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
