// Package ops provides a content-type dispatch engine for polis.
//
// The Engine routes ActionRequests to the appropriate Handler based on
// content type, which is resolved through the bundle system. Three handler
// types are supported: builtin Go handlers, external executables (JSON
// stdin/stdout), and HTTP endpoints (JSON POST).
package ops

import (
	"context"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
)

// Engine dispatches content-type operations to the appropriate handler.
type Engine struct {
	Resolver     *resolve.Resolver
	Bundles      map[string]*bundle.Bundle
	PrivateKey   []byte
	PublicKey    []byte
	BaseURL      string
	DiscoveryURL string
	DiscoveryKey string
	CLIThemesDir string
	Logger       Logger

	// OnContentChanged is called after any write operation succeeds.
	OnContentChanged func()

	handlers map[string]Handler // bundle name → handler
}

// Logger is the interface for engine logging.
type Logger interface {
	Info(format string, args ...interface{})
	Error(format string, args ...interface{})
	Debug(format string, args ...interface{})
}

// ActionRequest describes a content-type operation to perform.
type ActionRequest struct {
	Action      string         `json:"action"`
	ContentType string         `json:"content_type"`
	Payload     map[string]any `json:"payload"`
}

// ActionResult is the outcome of a dispatched operation.
type ActionResult struct {
	Status string         `json:"status"`
	Data   map[string]any `json:"data,omitempty"`
}

// Event represents a side-effect event emitted by an operation.
type Event struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data,omitempty"`
}

// NewEngine creates an Engine with the given configuration and registers
// handlers for all provided bundles.
func NewEngine(cfg EngineConfig) (*Engine, error) {
	if cfg.Resolver == nil {
		return nil, fmt.Errorf("resolver is required")
	}
	if cfg.Bundles == nil {
		cfg.Bundles = make(map[string]*bundle.Bundle)
	}
	if cfg.Logger == nil {
		cfg.Logger = nopLogger{}
	}

	e := &Engine{
		Resolver:         cfg.Resolver,
		Bundles:          cfg.Bundles,
		PrivateKey:       cfg.PrivateKey,
		PublicKey:        cfg.PublicKey,
		BaseURL:          cfg.BaseURL,
		DiscoveryURL:     cfg.DiscoveryURL,
		DiscoveryKey:     cfg.DiscoveryKey,
		CLIThemesDir:     cfg.CLIThemesDir,
		Logger:           cfg.Logger,
		OnContentChanged: cfg.OnContentChanged,
		handlers:         make(map[string]Handler),
	}

	// Register handlers for each bundle
	for name, b := range cfg.Bundles {
		h, err := e.handlerForBundle(b)
		if err != nil {
			return nil, fmt.Errorf("handler for bundle %q: %w", name, err)
		}
		e.handlers[name] = h
	}

	return e, nil
}

// EngineConfig holds the configuration for creating an Engine.
type EngineConfig struct {
	Resolver         *resolve.Resolver
	Bundles          map[string]*bundle.Bundle
	PrivateKey       []byte
	PublicKey        []byte
	BaseURL          string
	DiscoveryURL     string
	DiscoveryKey     string
	CLIThemesDir     string
	Logger           Logger
	OnContentChanged func()
}

// Dispatch routes an ActionRequest to the appropriate handler.
func (e *Engine) Dispatch(ctx context.Context, req ActionRequest) (*ActionResult, error) {
	// Resolve short type names (e.g., "post" → "pub.polis.post")
	fullType := e.resolveTypeName(req.ContentType)
	req.ContentType = fullType

	// Find the bundle that owns this content type
	bundleName, b := e.bundleForType(fullType)
	if b == nil {
		return nil, fmt.Errorf("unknown content type: %s", fullType)
	}

	// Look up the handler
	h, ok := e.handlers[bundleName]
	if !ok {
		return nil, fmt.Errorf("no handler registered for bundle %q", bundleName)
	}

	// Check if this is a write operation
	isWrite := isWriteAction(req.Action)

	// Write operations require a private key
	if isWrite && e.PrivateKey == nil {
		return nil, fmt.Errorf("not configured: no private key available")
	}

	// Build handler environment
	env := HandlerEnv{
		Resolver:     e.Resolver,
		PrivateKey:   e.PrivateKey,
		PublicKey:    e.PublicKey,
		BaseURL:      e.BaseURL,
		DiscoveryURL: e.DiscoveryURL,
		DiscoveryKey: e.DiscoveryKey,
		CLIThemesDir: e.CLIThemesDir,
		Bundle:       b,
	}

	// Dispatch to handler
	result, err := h.Handle(ctx, req, env)
	if err != nil {
		return nil, err
	}

	// Post-write: render + notify
	if isWrite && e.OnContentChanged != nil {
		e.OnContentChanged()
	}

	return result, nil
}

// ListBundles returns information about all installed bundles.
func (e *Engine) ListBundles() []BundleInfo {
	var infos []BundleInfo
	for name, b := range e.Bundles {
		info := BundleInfo{
			Name:        name,
			Version:     b.Version,
			Description: b.Description,
		}
		for typeName := range b.Types {
			h, ok := e.handlers[name]
			if !ok {
				continue
			}
			info.Types = append(info.Types, TypeInfo{
				Name:    typeName,
				Actions: h.Actions(typeName),
			})
		}
		infos = append(infos, info)
	}
	return infos
}

// BundleInfo describes an installed bundle.
type BundleInfo struct {
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Description string     `json:"description"`
	Types       []TypeInfo `json:"types"`
}

// TypeInfo describes a content type and its available actions.
type TypeInfo struct {
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
}

// resolveTypeName expands short names to fully-qualified type names.
// "post" → "pub.polis.post" if unambiguous across all bundles.
func (e *Engine) resolveTypeName(name string) string {
	// Already fully qualified
	if strings.Contains(name, ".") {
		return name
	}

	// Try "pub.polis.<name>" first (most common)
	candidate := "pub.polis." + name
	if _, b := e.bundleForType(candidate); b != nil {
		return candidate
	}

	// Search all bundles for a type ending in ".<name>"
	suffix := "." + name
	var match string
	for _, b := range e.Bundles {
		for typeName := range b.Types {
			if strings.HasSuffix(typeName, suffix) {
				if match != "" {
					// Ambiguous — return original
					return name
				}
				match = typeName
			}
		}
	}
	if match != "" {
		return match
	}

	return name
}

// bundleForType finds the bundle that declares a given content type.
func (e *Engine) bundleForType(typeName string) (string, *bundle.Bundle) {
	for name, b := range e.Bundles {
		if _, ok := b.Types[typeName]; ok {
			return name, b
		}
	}
	return "", nil
}

// handlerForBundle creates the appropriate handler for a bundle based on its
// handler configuration.
func (e *Engine) handlerForBundle(b *bundle.Bundle) (Handler, error) {
	switch b.Handler.Type {
	case "builtin", "":
		return NewBuiltinCoreHandler(), nil
	case "executable":
		return NewExecutableHandler(b.Handler.Path), nil
	case "http":
		return NewHTTPHandler(b.Handler.URL), nil
	default:
		return nil, fmt.Errorf("unknown handler type: %s", b.Handler.Type)
	}
}

// isWriteAction returns true for actions that modify content.
func isWriteAction(action string) bool {
	switch action {
	case "create", "update", "delete",
		"bless", "deny", "revoke", "sync",
		"draft.save", "draft.delete",
		"refresh",
		"send", "deliver", "mark_read", "retry":
		return true
	default:
		return false
	}
}

// nopLogger discards all log messages.
type nopLogger struct{}

func (nopLogger) Info(string, ...interface{})  {}
func (nopLogger) Error(string, ...interface{}) {}
func (nopLogger) Debug(string, ...interface{}) {}
