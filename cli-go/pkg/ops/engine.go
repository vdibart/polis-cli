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
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"net/http"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
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
	CLIThemesDir string
	Logger       Logger

	// Keys, when set, supplies the key pair at CALL time, overriding the
	// fixed fields above. ⭐ A long-lived host passes it so the engine signs
	// with the key it holds NOW: one that loads its key after building the
	// engine (a deferred start), or reloads it after a rotation, would
	// otherwise leave the engine with nil or the retired key.
	Keys func() (private, public []byte)

	// EmitEvent, if set, routes structured operation events (e.g. dm.* security
	// signals) to the host's event sink. nil = discard.
	EmitEvent func(action string, fields map[string]any)

	// AfterWrite is called once after each write operation succeeds, with
	// what it did. ⭐ A host passes NewAfterWrite(...).Apply, so a write's
	// render, hook and draft removal happen the same way whoever called.
	AfterWrite func(ctx context.Context, w Written)

	// DMHTTPClient and AllowPrivateDMRecipients: how messages are sent from
	// this host (dm_send.go, the outbound address rule).
	DMHTTPClient             *http.Client
	AllowPrivateDMRecipients bool

	// Capabilities is what the site can do, derived from its mode by the
	// host. The API's description lists only what these allow.
	Capabilities capability.Set

	handlers map[string]Handler // bundle name → handler
}

// Describer is a Handler that describes its operations (apidesc). The core
// handler is one; a handler that is not is left out of the description.
type Describer interface {
	Operations() []apidesc.Operation
}

// Operations is every described operation the engine's handlers serve, in a
// stable order.
func (e *Engine) Operations() []apidesc.Operation {
	var names []string
	for name := range e.handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []apidesc.Operation
	for _, name := range names {
		if d, ok := e.handlers[name].(Describer); ok {
			out = append(out, d.Operations()...)
		}
	}
	return out
}

// Describe is the site's API description (OpenAPI 3.1), generated from the
// described operations and the site's capabilities.
func (e *Engine) Describe(mcp string) map[string]any {
	return apidesc.Describe(e.Operations(), apidesc.Manual(), e.Capabilities,
		apidesc.Options{SiteURL: e.BaseURL, MCP: mcp})
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
	// Code is the HTTP status for the answer when the transport's default
	// (201 for a create, 200 otherwise) is not it: 202 for a message saved
	// but not delivered. Not sent in the body.
	Code int `json:"-"`
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
		Resolver:     cfg.Resolver,
		Bundles:      cfg.Bundles,
		PrivateKey:   cfg.PrivateKey,
		Keys:         cfg.Keys,
		PublicKey:    cfg.PublicKey,
		BaseURL:      cfg.BaseURL,
		DiscoveryURL: cfg.DiscoveryURL,
		CLIThemesDir: cfg.CLIThemesDir,
		Logger:       cfg.Logger,
		EmitEvent:    cfg.EmitEvent,
		AfterWrite:   cfg.AfterWrite,
		Capabilities: cfg.Capabilities,

		DMHTTPClient:             cfg.DMHTTPClient,
		AllowPrivateDMRecipients: cfg.AllowPrivateDMRecipients,
		handlers:                 make(map[string]Handler),
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
	Resolver     *resolve.Resolver
	Bundles      map[string]*bundle.Bundle
	PrivateKey   []byte
	PublicKey    []byte
	Keys         func() (private, public []byte)
	BaseURL      string
	DiscoveryURL string
	CLIThemesDir string
	Logger       Logger
	EmitEvent    func(action string, fields map[string]any)
	AfterWrite   func(ctx context.Context, w Written)
	Capabilities capability.Set
	// DMHTTPClient is the host's shared HTTP client for sending messages, and
	// AllowPrivateDMRecipients is true only in local mode (dm_send.go).
	DMHTTPClient             *http.Client
	AllowPrivateDMRecipients bool
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

	priv, pub := e.PrivateKey, e.PublicKey
	if e.Keys != nil {
		priv, pub = e.Keys()
	}

	// Write operations require a private key
	if isWrite && priv == nil {
		return nil, fmt.Errorf("not configured: no private key available")
	}

	// ⭐ Name the operation on the request, and stamp every event the write
	// emits (the DM package's lifecycle events included) with D8's fields.
	op := fullType + "." + req.Action
	NoteOp(ctx, op)
	marker := MarkerOf(GrantFrom(ctx))
	emit := e.EmitEvent
	if emit != nil {
		raw := emit
		emit = func(action string, fields map[string]any) {
			raw(action, EventFields(ctx, op, marker, fields))
		}
	}

	// Build handler environment
	env := HandlerEnv{
		Resolver:     e.Resolver,
		PrivateKey:   priv,
		PublicKey:    pub,
		BaseURL:      e.BaseURL,
		DiscoveryURL: e.DiscoveryURL,
		CLIThemesDir: e.CLIThemesDir,
		EmitEvent:    emit,
		Bundle:       b,
		Marker:       marker,

		DMHTTPClient:             e.DMHTTPClient,
		AllowPrivateDMRecipients: e.AllowPrivateDMRecipients,
	}

	// Dispatch to handler
	result, err := h.Handle(ctx, req, env)
	if err != nil {
		return nil, err
	}

	// Post-write: what the write causes, once, whoever called.
	if isWrite && e.AfterWrite != nil {
		e.AfterWrite(ctx, writtenFrom(req, result, env.Marker))
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
		"save_draft", "discard_draft", "unpublish",
		"refresh",
		"send", "deliver", "mark_read", "retry",
		"apply", "remove":
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

// writtenFrom is the Written value for a successful write: the operation, and
// what its answer says about the item it wrote.
func writtenFrom(req ActionRequest, result *ActionResult, marker discovery.AgentMarker) Written {
	w := Written{Type: req.ContentType, Action: req.Action, Marker: marker}
	if result != nil {
		w.Result = result.Data
		w.Path, _ = result.Data["path"].(string)
		w.Title, _ = result.Data["title"].(string)
		w.Version, _ = result.Data["version"].(string)
	}
	w.DraftID, _ = req.Payload["draft_id"].(string)
	return w
}
