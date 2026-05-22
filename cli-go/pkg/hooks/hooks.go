// Package hooks provides post-action automation for polis events.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
// Checks explicit config first, then falls back to .polis/hooks/{event}.sh.
// Returns nil error if no hook is found (not an error condition).
func RunHook(siteDir string, config *HookConfig, payload *HookPayload) (*HookResult, error) {
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
		fullPath := filepath.Join(siteDir, conventional)
		if _, err := os.Stat(fullPath); err == nil {
			hookPath = conventional
		}
	}

	if hookPath == "" {
		return &HookResult{Executed: false}, nil
	}

	// Resolve relative paths from site root
	if !filepath.IsAbs(hookPath) {
		hookPath = filepath.Join(siteDir, hookPath)
	}

	// Path containment: hook must resolve within siteDir
	absHook, err := filepath.Abs(hookPath)
	if err != nil {
		return nil, fmt.Errorf("resolve hook path: %w", err)
	}
	absSite, err := filepath.Abs(siteDir)
	if err != nil {
		return nil, fmt.Errorf("resolve site dir: %w", err)
	}
	if !strings.HasPrefix(absHook, absSite+string(filepath.Separator)) {
		return nil, fmt.Errorf("hook path %s is outside site directory", hookPath)
	}

	// Check if hook exists
	if _, err := os.Stat(hookPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("hook not found: %s", hookPath)
	}

	// Build environment variables
	configDir := filepath.Join(siteDir, ".polis")
	env := os.Environ()
	env = append(env,
		"POLIS_EVENT="+string(payload.Event),
		"POLIS_PATH="+payload.Path,
		"POLIS_TITLE="+payload.Title,
		"POLIS_VERSION="+payload.Version,
		"POLIS_TIMESTAMP="+payload.Timestamp,
		"POLIS_SITE_DIR="+siteDir,
		"POLIS_CONFIG_DIR="+configDir,
		"POLIS_COMMIT_MESSAGE="+payload.CommitMessage,
	)

	// Execute hook with timeout
	ctx, cancel := context.WithTimeout(context.Background(), HookTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, hookPath)
	cmd.Env = env
	cmd.Dir = siteDir // Run in site directory
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
// .polis/hooks/{event}.sh under siteDir. Returns empty string if none.
func GetHookPathWithDiscovery(config *HookConfig, event HookEvent, siteDir string) string {
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

	if hookPath == "" && siteDir != "" {
		conventional := filepath.Join(".polis", "webapp", "hooks", string(event)+".sh")
		fullPath := filepath.Join(siteDir, conventional)
		if _, err := os.Stat(fullPath); err == nil {
			hookPath = conventional
		}
	}

	return hookPath
}
