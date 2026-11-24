package api

import (
	"encoding/json"
	"net/http"
)

// Live operator switches, as the shared code sees them.
//
// A host may turn a feature off while it runs, with no restart: polis.pub's
// operator does it from the machine when a feature is under attack. The host
// owns the switches and how they are stored; the shared code only asks, through
// a func the host sets (Host.SwitchedOff, and server.Server.SwitchedOff). A nil
// func means nothing is ever off, which is right for a laptop and a self-hosted
// site.
//
// ⛔ A switch only ever turns something OFF. Asking it can never enable what
// the feature's own settings have off.

// The switch names the shared code asks about. The host's list of switches
// must use these same words.
const (
	SwitchMCP            = "mcp"
	SwitchAPITokens      = "api-tokens"
	SwitchAgentWrites    = "agent-writes"
	SwitchRosie          = "rosie"
	SwitchWidgetComments = "widget-comments"
)

// SwitchedOffMessage is the one sentence a switched-off entry point answers.
const SwitchedOffMessage = "This is switched off for now. Please try again later."

// SwitchedOffRetryAfter is the Retry-After a switched-off entry point sends, in
// seconds: long enough not to hammer a feature under attack, short enough that
// clients come back soon after it is on again.
const SwitchedOffRetryAfter = "300"

// WriteSwitchedOff answers a request to a switched-off feature: 503, with
// Retry-After and one plain sentence, in the API's error envelope.
//
// ⭐ Never 404: that reads as "never existed" and breaks a client's retry.
func WriteSwitchedOff(w http.ResponseWriter) {
	w.Header().Set("Retry-After", SwitchedOffRetryAfter)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "error",
		"error": map[string]any{
			"code":    "switched_off",
			"message": SwitchedOffMessage,
		},
	})
}

// switchedOff asks the host whether a switch is off.
func (h *handlers) switchedOff(name string) bool {
	return h.host != nil && h.host.SwitchedOff != nil && h.host.SwitchedOff(name)
}
