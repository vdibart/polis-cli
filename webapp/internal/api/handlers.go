package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

// writeActionTimeout caps every mutating v1 API dispatch so a stuck
// downstream (DS round-trip, slow disk on DM delivery fan-out) can't
// hold the request goroutine open indefinitely. Reads use the bare
// request context — the caller's client timeout governs them.
const writeActionTimeout = 30 * time.Second

// handlers holds the engine and provides HTTP handler methods.
type handlers struct {
	engine         *ops.Engine
	securityLogger SecurityLogger
}

// logSecurity emits a security event if a logger is configured.
func (h *handlers) logSecurity(event string, fields map[string]interface{}) {
	if h.securityLogger != nil {
		h.securityLogger(event, fields)
	}
}

// handleContentList handles GET /v1/content/{type}
func (h *handlers) handleContentList(w http.ResponseWriter, r *http.Request, contentType string) {
	result, err := h.engine.Dispatch(r.Context(), ops.ActionRequest{
		Action:      "list",
		ContentType: contentType,
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleContentGet handles GET /v1/content/{type}/{id}
func (h *handlers) handleContentGet(w http.ResponseWriter, r *http.Request, contentType, id string) {
	result, err := h.engine.Dispatch(r.Context(), ops.ActionRequest{
		Action:      "get",
		ContentType: contentType,
		Payload:     map[string]any{"id": id},
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleContentCreate handles POST /v1/content/{type}
func (h *handlers) handleContentCreate(w http.ResponseWriter, r *http.Request, contentType string) {
	payload, err := parsePayload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), writeActionTimeout)
	defer cancel()
	result, err := h.engine.Dispatch(ctx, ops.ActionRequest{
		Action:      "create",
		ContentType: contentType,
		Payload:     payload,
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result.Data)
}

// handleContentUpdate handles PUT /v1/content/{type}/{id}
func (h *handlers) handleContentUpdate(w http.ResponseWriter, r *http.Request, contentType, id string) {
	payload, err := parsePayload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	payload["id"] = id

	ctx, cancel := context.WithTimeout(r.Context(), writeActionTimeout)
	defer cancel()
	result, err := h.engine.Dispatch(ctx, ops.ActionRequest{
		Action:      "update",
		ContentType: contentType,
		Payload:     payload,
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleContentDelete handles DELETE /v1/content/{type}/{id}
func (h *handlers) handleContentDelete(w http.ResponseWriter, r *http.Request, contentType, id string) {
	ctx, cancel := context.WithTimeout(r.Context(), writeActionTimeout)
	defer cancel()
	result, err := h.engine.Dispatch(ctx, ops.ActionRequest{
		Action:      "delete",
		ContentType: contentType,
		Payload:     map[string]any{"id": id},
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleContentAction handles POST /v1/content/{type}/actions/{action}
func (h *handlers) handleContentAction(w http.ResponseWriter, r *http.Request, contentType, action string) {
	payload, err := parsePayload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), writeActionTimeout)
	defer cancel()
	result, err := h.engine.Dispatch(ctx, ops.ActionRequest{
		Action:      action,
		ContentType: contentType,
		Payload:     payload,
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleDraftsList handles GET /v1/content/{type}/drafts
func (h *handlers) handleDraftsList(w http.ResponseWriter, r *http.Request, contentType string) {
	result, err := h.engine.Dispatch(r.Context(), ops.ActionRequest{
		Action:      "draft.list",
		ContentType: contentType,
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleDraftsGet handles GET /v1/content/{type}/drafts/{id}
func (h *handlers) handleDraftsGet(w http.ResponseWriter, r *http.Request, contentType, id string) {
	result, err := h.engine.Dispatch(r.Context(), ops.ActionRequest{
		Action:      "draft.get",
		ContentType: contentType,
		Payload:     map[string]any{"id": id},
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleDraftsCreate handles POST /v1/content/{type}/drafts
func (h *handlers) handleDraftsCreate(w http.ResponseWriter, r *http.Request, contentType string) {
	payload, err := parsePayload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), writeActionTimeout)
	defer cancel()
	result, err := h.engine.Dispatch(ctx, ops.ActionRequest{
		Action:      "draft.save",
		ContentType: contentType,
		Payload:     payload,
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result.Data)
}

// handleDraftsDelete handles DELETE /v1/content/{type}/drafts/{id}
func (h *handlers) handleDraftsDelete(w http.ResponseWriter, r *http.Request, contentType, id string) {
	ctx, cancel := context.WithTimeout(r.Context(), writeActionTimeout)
	defer cancel()
	result, err := h.engine.Dispatch(ctx, ops.ActionRequest{
		Action:      "draft.delete",
		ContentType: contentType,
		Payload:     map[string]any{"id": id},
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleBundlesList handles GET /v1/bundles
func (h *handlers) handleBundlesList(w http.ResponseWriter, r *http.Request) {
	bundles := h.engine.ListBundles()
	writeJSON(w, http.StatusOK, map[string]any{"bundles": bundles})
}

// handleBundleGet handles GET /v1/bundles/{name}
func (h *handlers) handleBundleGet(w http.ResponseWriter, r *http.Request, name string) {
	bundles := h.engine.ListBundles()
	for _, b := range bundles {
		if b.Name == name {
			writeJSON(w, http.StatusOK, b)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "bundle not found: "+name)
}

// handleDMDeliver handles POST /v1/content/dm/actions/deliver with signed-request auth.
// The senderDomain has already been verified by the signed request middleware.
func (h *handlers) handleDMDeliver(w http.ResponseWriter, r *http.Request, senderDomain string) {
	payload, err := parsePayload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	// Pass the verified sender domain and raw envelope to the engine
	payload["sender_domain"] = senderDomain

	// The envelope is the full request body, re-marshal it
	envelopeJSON, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid envelope")
		return
	}
	payload["envelope"] = string(envelopeJSON)

	ctx, cancel := context.WithTimeout(r.Context(), writeActionTimeout)
	defer cancel()
	result, err := h.engine.Dispatch(ctx, ops.ActionRequest{
		Action:      "deliver",
		ContentType: "pub.polis.dm",
		Payload:     payload,
	})
	if err != nil {
		h.handleDispatchError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result.Data)
}

// ── Helpers ─────────────────────────────────────────────────────────

func parsePayload(r *http.Request) (map[string]any, error) {
	if r.Body == nil {
		return map[string]any{}, nil
	}
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// handleDispatchError maps dispatch errors to HTTP responses.
//
// R18-15 (2026-05-18): EVERY branch now returns a generic, category-
// keyed message. Pre-fix the matched sub-cases (not-configured,
// invalid, not-found, unsupported-action) echoed `err.Error()`
// verbatim. Downstream packages (dm, following, publish) wrap errors
// with absolute filesystem paths, DS URLs, or wrapped-chain dumps;
// any substring match on "invalid" or "not found" surfaced the
// wrapper verbatim to API callers, leaking server layout.
//
// New posture: response body carries only the category code + a
// short, fixed message that describes the category. The full `err`
// is logged server-side via `pub.polis.api.dispatch_error` keyed by
// `request_id` so operators can correlate without leaking layout.
// Mirrors the previous default arm's posture (R16-15) — completes
// the scrub.
func (h *handlers) handleDispatchError(w http.ResponseWriter, r *http.Request, err error) {
	msg := err.Error()

	var status int
	var code, publicMessage string
	switch {
	case strings.Contains(msg, "not configured") || strings.Contains(msg, "no private key"):
		status = http.StatusServiceUnavailable
		code = "not_configured"
		publicMessage = "The site is not fully configured for this action."
	case strings.Contains(msg, "required") || strings.Contains(msg, "invalid"):
		status = http.StatusBadRequest
		code = "invalid_request"
		publicMessage = "The request is missing or has invalid fields. Reference X-Request-Id for support."
	case strings.Contains(msg, "unknown content type") || strings.Contains(msg, "not found"):
		status = http.StatusNotFound
		code = "not_found"
		publicMessage = "The requested resource was not found."
	case strings.Contains(msg, "unsupported action"):
		status = http.StatusBadRequest
		code = "unsupported_action"
		publicMessage = "The requested action is not supported on this content type."
	default:
		status = http.StatusInternalServerError
		code = "internal_error"
		publicMessage = "An internal error occurred. Reference X-Request-Id for support."
	}

	// Always log the full err server-side, keyed by request_id so we can
	// correlate without leaking. Previously only the default arm did this.
	requestID := w.Header().Get("X-Request-Id")
	h.logSecurity("pub.polis.api.dispatch_error", map[string]interface{}{
		"request_id": requestID,
		"path":       r.URL.Path,
		"method":     r.Method,
		"code":       code,
		"error":      msg,
	})
	writeError(w, status, code, publicMessage)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"status": "error",
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}
