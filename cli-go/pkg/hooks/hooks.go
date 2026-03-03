// Package hooks provides post-action automation for polis events.
package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	Executed bool   `json:"executed"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
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

	// Execute hook
	cmd := exec.Command(hookPath)
	cmd.Env = env
	cmd.Dir = siteDir // Run in site directory

	// Pass JSON payload to stdin
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal hook payload: %w", err)
	}
	cmd.Stdin = bytes.NewReader(jsonPayload)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return &HookResult{
			Executed: true,
			Output:   string(output),
			Error:    err.Error(),
		}, fmt.Errorf("hook failed: %w\nOutput: %s", err, output)
	}

	return &HookResult{
		Executed: true,
		Output:   string(output),
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

// GetHookPath returns the hook path for a given event.
// Checks explicit config first, then falls back to the conventional
// location .polis/hooks/{event}.sh if siteDir is provided.
// Returns empty string if no hook is found.
func GetHookPath(config *HookConfig, event HookEvent) string {
	return GetHookPathWithDiscovery(config, event, "")
}

// GetHookPathWithDiscovery returns the hook path for a given event,
// checking explicit config first, then auto-discovering from siteDir.
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
