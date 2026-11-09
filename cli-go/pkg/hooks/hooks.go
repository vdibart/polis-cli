// Package hooks provides post-action automation for polis events.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

const (
	// HookTimeout is the maximum execution time for a hook script.
	HookTimeout = 30 * time.Second
)

// HookEvent represents the type of event that triggers a hook.
type HookEvent string

const (
	// EventPostPublish is triggered when a new post is published.
	EventPostPublish HookEvent = "post-publish"
	// EventPostRepublish is triggered when an existing post is updated.
	EventPostRepublish HookEvent = "post-republish"
	// EventPostComment is triggered when a comment becomes blessed.
	// This includes explicit grant, blessing sync, and auto-bless on beseech.
	// The hook typically re-renders the post page to include the new comment.
	EventPostComment HookEvent = "post-comment"
)

// HookConfig contains paths to hook scripts.
type HookConfig struct {
	PostPublish   string `json:"post-publish,omitempty"`
	PostRepublish string `json:"post-republish,omitempty"`
	PostComment   string `json:"post-comment,omitempty"`

	// Extra holds members this build does not model — a hook another build
	// knows — so saving the webapp config keeps them (extra.go).
	Extra map[string]json.RawMessage `json:"-"`
}

// HookPayload contains data passed to hook scripts.
type HookPayload struct {
	Event         HookEvent `json:"event"`
	Path          string    `json:"path"`
	Title         string    `json:"title"`
	Version       string    `json:"version"`
	Timestamp     string    `json:"timestamp"`
	CommitMessage string    `json:"commit_message"`
}

// HookResult contains the result of running a hook.
type HookResult struct {
	Executed   bool   `json:"executed"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// RunHook executes a hook script if configured or discovered by convention.
// Checks explicit config first, then falls back to .polis/webapp/hooks/{event}.sh.
// Returns nil error if no hook is found (not an error condition).
func RunHook(site storage.Site, config *HookConfig, payload *HookPayload) (*HookResult, error) {
	// Get hook path from explicit config
	var hookPath string
	if config != nil {
		switch payload.Event {
		case EventPostPublish:
			hookPath = config.PostPublish
		case EventPostRepublish:
			hookPath = config.PostRepublish
		case EventPostComment:
			hookPath = config.PostComment
		}
	}

	// Auto-discover from conventional location if not explicitly configured
	if hookPath == "" {
		conventional := filepath.Join(".polis", "webapp", "hooks", string(payload.Event)+".sh")
		fullPath := site.Path(conventional)
		if present, _ := storage.PathExists(fullPath); present {
			hookPath = conventional
		}
	}

	if hookPath == "" {
		return &HookResult{Executed: false}, nil
	}

	// Resolve relative paths from site root
	if !filepath.IsAbs(hookPath) {
		hookPath = site.Path(hookPath)
	}

	// Path containment: hook must resolve within the site root
	absHook, err := filepath.Abs(hookPath)
	if err != nil {
		return nil, fmt.Errorf("resolve hook path: %w", err)
	}
	absSite, err := filepath.Abs(site.Dir())
	if err != nil {
		return nil, fmt.Errorf("resolve site dir: %w", err)
	}
	if !within(absHook, absSite) {
		return nil, fmt.Errorf("hook path %s is outside site directory", hookPath)
	}

	// The check above compares names, and a symlink's name is inside the site
	// while the file it points at need not be. Resolve both sides and check
	// again: the site too, or a site reached through a link would refuse all of
	// its own hooks. A link that stays inside the site is allowed.
	// NOT the storage interface: filepath.EvalSymlinks RESOLVES links, and the
	// interface cannot represent one — Stat follows and Info.Mode is Perm() only.
	realSite, err := filepath.EvalSymlinks(absSite)
	if err != nil {
		return nil, fmt.Errorf("resolve site dir: %w", err)
	}
	// NOT the storage interface: filepath.EvalSymlinks RESOLVES links, and the
	// interface cannot represent one — Stat follows and Info.Mode is Perm() only.
	realHook, err := filepath.EvalSymlinks(absHook)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("hook not found: %s", hookPath)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve hook path: %w", err)
	}
	if !within(realHook, realSite) {
		return nil, fmt.Errorf("hook path %s is outside site directory", hookPath)
	}
	// NOT the storage interface: asks whether a legacy directory is THERE, and an
	// EMPTY one still counts — which Exists cannot say.
	info, err := os.Stat(realHook)
	if err != nil {
		return nil, fmt.Errorf("hook not found: %s", hookPath)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("hook path %s is not a regular file", hookPath)
	}
	// Run the resolved path, so what executes is the file that was checked and
	// not whatever the link names by then.
	hookPath = realHook

	// Build environment variables
	configDir := site.Path(".polis")
	env := os.Environ()
	env = append(env,
		"POLIS_EVENT="+string(payload.Event),
		"POLIS_PATH="+payload.Path,
		"POLIS_TITLE="+payload.Title,
		"POLIS_VERSION="+payload.Version,
		"POLIS_TIMESTAMP="+payload.Timestamp,
		"POLIS_SITE_DIR="+site.Dir(),
		"POLIS_CONFIG_DIR="+configDir,
		"POLIS_COMMIT_MESSAGE="+payload.CommitMessage,
	)

	// Execute hook with timeout
	ctx, cancel := context.WithTimeout(context.Background(), HookTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, hookPath)
	cmd.Env = env
	cmd.Dir = site.Dir()            // Run in site directory
	cmd.WaitDelay = 3 * time.Second // Force-close pipes if child processes linger after timeout

	// Pass JSON payload to stdin
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal hook payload: %w", err)
	}
	cmd.Stdin = bytes.NewReader(jsonPayload)

	hookStart := time.Now()
	output, err := cmd.CombinedOutput()
	durationMs := time.Since(hookStart).Milliseconds()
	if err != nil {
		return &HookResult{
			Executed:   true,
			Output:     string(output),
			Error:      err.Error(),
			DurationMs: durationMs,
		}, fmt.Errorf("hook failed: %w\nOutput: %s", err, output)
	}

	return &HookResult{
		Executed:   true,
		Output:     string(output),
		DurationMs: durationMs,
	}, nil
}

// within reports whether path is strictly inside dir. Both must be absolute
// and clean.
func within(path, dir string) bool {
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// GenerateCommitMessage generates a git commit message for the given event.
func GenerateCommitMessage(event HookEvent, title string) string {
	switch event {
	case EventPostPublish:
		return fmt.Sprintf("Publish: %s", title)
	case EventPostRepublish:
		return fmt.Sprintf("Update: %s", title)
	case EventPostComment:
		return fmt.Sprintf("Comment blessed: %s", title)
	default:
		return fmt.Sprintf("Polis: %s", title)
	}
}

// GetHookPathWithDiscovery returns the hook path for a given event,
// checking explicit config first, then auto-discovering from
// .polis/webapp/hooks/{event}.sh under the site root. Returns empty string if none.
func GetHookPathWithDiscovery(config *HookConfig, event HookEvent, site storage.Site) string {
	var hookPath string
	if config != nil {
		switch event {
		case EventPostPublish:
			hookPath = config.PostPublish
		case EventPostRepublish:
			hookPath = config.PostRepublish
		case EventPostComment:
			hookPath = config.PostComment
		}
	}

	if hookPath == "" && site.Valid() {
		conventional := filepath.Join(".polis", "webapp", "hooks", string(event)+".sh")
		fullPath := site.Path(conventional)
		if present, _ := storage.PathExists(fullPath); present {
			hookPath = conventional
		}
	}

	return hookPath
}
