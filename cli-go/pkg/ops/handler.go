package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
)

// Handler processes content-type operations.
type Handler interface {
	// Handle executes an action and returns the result.
	Handle(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error)

	// Actions returns the list of actions supported for a content type.
	Actions(contentType string) []string
}

// HandlerEnv provides context to handlers during execution.
type HandlerEnv struct {
	Resolver     *resolve.Resolver
	PrivateKey   []byte
	PublicKey    []byte
	BaseURL      string
	DiscoveryURL string
	DiscoveryKey string
	CLIThemesDir string
	Bundle       *bundle.Bundle
}

// ExecutableHandler dispatches to an external binary via JSON stdin/stdout.
type ExecutableHandler struct {
	Path string
}

// NewExecutableHandler creates a handler that calls an external binary.
func NewExecutableHandler(path string) *ExecutableHandler {
	return &ExecutableHandler{Path: path}
}

func (h *ExecutableHandler) Handle(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	input, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	cmd := exec.CommandContext(ctx, h.Path)
	cmd.Stdin = bytes.NewReader(input)

	// Set environment variables for the handler
	cmd.Env = append(cmd.Environ(),
		"POLIS_SITE_DIR="+env.Resolver.SiteDir(),
		"POLIS_BASE_URL="+env.BaseURL,
		"POLIS_DISCOVERY_URL="+env.DiscoveryURL,
	)

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("handler exited with code %d: %s", exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("handler execution failed: %w", err)
	}

	var result ActionResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("unmarshal handler output: %w", err)
	}

	return &result, nil
}

func (h *ExecutableHandler) Actions(contentType string) []string {
	// Executable handlers self-report via introspection
	return nil
}

// HTTPHandler dispatches to an HTTP endpoint via JSON POST.
type HTTPHandler struct {
	URL    string
	Client *http.Client
}

// NewHTTPHandler creates a handler that POSTs to an HTTP URL.
func NewHTTPHandler(url string) *HTTPHandler {
	return &HTTPHandler{
		URL: url,
		Client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (h *HTTPHandler) Handle(ctx context.Context, req ActionRequest, env HandlerEnv) (*ActionResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := h.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("handler returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result ActionResult
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &result, nil
}

func (h *HTTPHandler) Actions(contentType string) []string {
	// HTTP handlers self-report via introspection
	return nil
}
