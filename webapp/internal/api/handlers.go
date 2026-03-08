package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
)

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
	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      "list",
		ContentType: contentType,
	})
	if err != nil {
		handleDispatchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleContentGet handles GET /v1/content/{type}/{id}
func (h *handlers) handleContentGet(w http.ResponseWriter, r *http.Request, contentType, id string) {
	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      "get",
		ContentType: contentType,
		Payload:     map[string]any{"id": id},
	})
	if err != nil {
		handleDispatchError(w, err)
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

	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      "create",
		ContentType: contentType,
		Payload:     payload,
	})
	if err != nil {
		handleDispatchError(w, err)
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

	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      "update",
		ContentType: contentType,
		Payload:     payload,
	})
	if err != nil {
		handleDispatchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleContentDelete handles DELETE /v1/content/{type}/{id}
func (h *handlers) handleContentDelete(w http.ResponseWriter, r *http.Request, contentType, id string) {
	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      "delete",
		ContentType: contentType,
		Payload:     map[string]any{"id": id},
	})
	if err != nil {
		handleDispatchError(w, err)
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

	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      action,
		ContentType: contentType,
		Payload:     payload,
	})
	if err != nil {
		handleDispatchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleDraftsList handles GET /v1/content/{type}/drafts
func (h *handlers) handleDraftsList(w http.ResponseWriter, r *http.Request, contentType string) {
	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      "draft.list",
		ContentType: contentType,
	})
	if err != nil {
		handleDispatchError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result.Data)
}

// handleDraftsGet handles GET /v1/content/{type}/drafts/{id}
func (h *handlers) handleDraftsGet(w http.ResponseWriter, r *http.Request, contentType, id string) {
	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      "draft.get",
		ContentType: contentType,
		Payload:     map[string]any{"id": id},
	})
	if err != nil {
		handleDispatchError(w, err)
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

	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      "draft.save",
		ContentType: contentType,
		Payload:     payload,
	})
	if err != nil {
		handleDispatchError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result.Data)
}

// handleDraftsDelete handles DELETE /v1/content/{type}/drafts/{id}
func (h *handlers) handleDraftsDelete(w http.ResponseWriter, r *http.Request, contentType, id string) {
	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      "draft.delete",
		ContentType: contentType,
		Payload:     map[string]any{"id": id},
	})
	if err != nil {
		handleDispatchError(w, err)
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

	result, err := h.engine.Dispatch(context.Background(), ops.ActionRequest{
		Action:      "deliver",
		ContentType: "pub.polis.dm",
		Payload:     payload,
	})
	if err != nil {
		handleDispatchError(w, err)
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

func handleDispatchError(w http.ResponseWriter, err error) {
	msg := err.Error()

	// Map known error patterns to HTTP status codes
	switch {
	case strings.Contains(msg, "not configured") || strings.Contains(msg, "no private key"):
		writeError(w, http.StatusServiceUnavailable, "not_configured", msg)
	case strings.Contains(msg, "required") || strings.Contains(msg, "invalid"):
		writeError(w, http.StatusBadRequest, "invalid_request", msg)
	case strings.Contains(msg, "unknown content type") || strings.Contains(msg, "not found"):
		writeError(w, http.StatusNotFound, "not_found", msg)
	case strings.Contains(msg, "unsupported action"):
		writeError(w, http.StatusBadRequest, "unsupported_action", msg)
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", msg)
	}
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
