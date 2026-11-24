package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/seen"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// SeenPath is where the owner's seen times are read and set.
const SeenPath = "/v1/seen"

// The seen operations are declared here, beside their route, because they are
// the owner's app state rather than an engine action on a content type.
var seenOperations = []apidesc.Operation{
	{
		Type: "pub.polis.seen", Action: "get", Title: "See where you left off", Method: "GET", Path: SeenPath,
		Summary: "Read how far the owner has seen each section of their app",
		Description: "For each section (gather, write, message), the time the owner has seen it up to. " +
			"An item is new to the owner when it is later than its section's time; a section with no " +
			"time has everything new. Reading this changes nothing, and no other read clears anything.",
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeNotify,
		Output: apidesc.Out(map[string]any{
			"sections": apidesc.Arr("The sections that keep a seen time.", apidesc.String("A section.")),
			"seen":     apidesc.Obj("Each section's seen time, RFC 3339."),
		}),
	},
	{
		Type: "pub.polis.seen", Action: "mark", Title: "Mark a section as seen", Method: "POST", Path: SeenPath,
		Summary: "Mark a section of the owner's app as seen up to a time",
		Description: "Records that the owner has seen a section (gather, write or message) up to up_to, " +
			"so everything up to then stops being new. The owner's app sends the time it arrived when the " +
			"owner leaves the section. The time never moves backwards and a future time is taken as now. " +
			"Call it for the owner only when they ask (\"I've caught up\"): reading content never clears " +
			"anything by itself.",
		Input: apidesc.Object(map[string]any{
			"section": apidesc.String("gather, write or message."),
			"up_to":   apidesc.String("An RFC 3339 time: everything up to it is seen."),
		}, "section", "up_to"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeNotify,
		Output: apidesc.Out(map[string]any{
			"sections": apidesc.Arr("The sections that keep a seen time.", apidesc.String("A section.")),
			"seen":     apidesc.Obj("Each section's seen time, RFC 3339."),
		}),
		Write: true,
	},
}

// SeenOperations is the seen operations' descriptions.
func SeenOperations() []apidesc.Operation { return seenOperations }

func seenJSON(times map[string]time.Time) map[string]any {
	out := map[string]any{}
	for s, t := range times {
		out[s] = t.UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{"sections": seen.Sections, "seen": out}
}

// handleSeen serves GET and POST /v1/seen.
func (h *handlers) handleSeen(w http.ResponseWriter, r *http.Request, site storage.Site) {
	switch r.Method {
	case http.MethodGet:
		ops.NoteOp(r.Context(), "pub.polis.seen.get")
		if !h.requireScope(w, r, site, apidesc.ScopeNotify) {
			return
		}
		times, err := seen.Get(site)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "could not read the seen times")
			return
		}
		writeJSON(w, http.StatusOK, seenJSON(times))
	case http.MethodPost:
		// The operation is noted before the scope check, so the write's audit
		// row and an error name it, as the other routes' do.
		ops.NoteOp(r.Context(), "pub.polis.seen.mark")
		if !h.requireScope(w, r, site, apidesc.ScopeNotify) {
			return
		}
		var req struct {
			Section string `json:"section"`
			UpTo    string `json:"up_to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "body must be JSON: {\"section\", \"up_to\"}")
			return
		}
		upTo, err := time.Parse(time.RFC3339Nano, req.UpTo)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "up_to must be an RFC 3339 time")
			return
		}
		if _, err := seen.Mark(site, req.Section, upTo, time.Now()); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		h.logSecurity("pub.polis.seen.marked", ops.RequestFields(r.Context(), map[string]any{"section": req.Section}))
		times, err := seen.Get(site)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "could not read the seen times")
			return
		}
		writeJSON(w, http.StatusOK, seenJSON(times))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST only")
	}
}
