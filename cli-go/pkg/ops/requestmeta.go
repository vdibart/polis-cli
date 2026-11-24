package ops

import (
	"context"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// ⭐ ONE FIELD SET ON EVERY EVENT A WRITE CAUSES.
//
// An event from a write says which request it belongs to, which operation it
// was, who made it and through what, so one request id finds the request log,
// the domain event, the render and the act log alike, and a dashboard can count
// writes by operation and by caller. The transport (the /v1 router, or an
// in-process caller such as polis.pub's signup) puts a RequestMeta in the
// request's context; the engine names the operation on it and stamps every
// event emitted during the write with the same fields.

// Surfaces: who wrote, as the field records it.
const (
	SurfaceOwnerApp = "v5"     // the owner, signed in, in their own browser app
	SurfaceToken    = "token"  // a bearer token that carries no grant (the owner's own key, a script)
	SurfaceAgent    = "agent"  // a write under a grant: an agent's, marked
	SurfaceSignup   = "signup" // polis.pub's signup, publishing a new owner's first post
	SurfaceCLI      = "cli"
	SurfaceSite     = "site"   // another site, by a signed request (a message delivered)
	SurfacePublic   = "public" // no credential
	SurfaceWidget   = "widget" // the embeddable widget, a visitor's own site acting on a foreign page
)

// RequestMeta is what the transport knows about a request, plus the operation
// the engine dispatched it as. It is a pointer in the context so the router
// that installed it can read what the engine noted (the operation) afterwards.
type RequestMeta struct {
	RequestID string
	Surface   string
	Op        string // <type>.<action>, set by the engine or by a host route
}

type requestMetaKey struct{}

// WithRequestMeta installs m in ctx.
func WithRequestMeta(ctx context.Context, m *RequestMeta) context.Context {
	return context.WithValue(ctx, requestMetaKey{}, m)
}

// RequestMetaFrom is the request's metadata, or nil outside a request that
// installed one.
func RequestMetaFrom(ctx context.Context) *RequestMeta {
	m, _ := ctx.Value(requestMetaKey{}).(*RequestMeta)
	return m
}

// NoteOp records the operation a request is, for the router's act log and
// error record. A no-op outside a request that installed metadata.
func NoteOp(ctx context.Context, op string) {
	if m := RequestMetaFrom(ctx); m != nil && op != "" {
		m.Op = op
	}
}

// EventFields adds D8's field set to fields (an event's own fields win): the
// request id, the operation, the surface, and the agent marker when the write
// is an agent's. It returns fields, allocating when nil.
func EventFields(ctx context.Context, op string, marker discovery.AgentMarker, fields map[string]any) map[string]any {
	if fields == nil {
		fields = map[string]any{}
	}
	set := func(k string, v any) {
		if _, ok := fields[k]; !ok && v != "" {
			fields[k] = v
		}
	}
	surface := ""
	if m := RequestMetaFrom(ctx); m != nil {
		set("request_id", m.RequestID)
		surface = m.Surface
	}
	if marker.Agent != "" || marker.Grant != "" {
		surface = SurfaceAgent
		set("agent", marker.Agent)
		set("grant", marker.Grant)
	}
	set("op", op)
	set("surface", surface)
	return fields
}

// RequestFields is EventFields for an event logged outside the engine, by a
// handler that has only the request's context: the operation is the one the
// router noted, and the marker the request's grant, if any.
func RequestFields(ctx context.Context, fields map[string]any) map[string]any {
	op := ""
	if m := RequestMetaFrom(ctx); m != nil {
		op = m.Op
	}
	return EventFields(ctx, op, MarkerOf(GrantFrom(ctx)), fields)
}
