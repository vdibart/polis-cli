package server

import (
	"encoding/json"
	"net/http"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// The capability descriptor, served rather than injected.
//
// ⛔⛔ FETCHED, NEVER INJECTED, AND THAT IS THE WHOLE POINT. The SPA used to
// read `window.__POLIS_HOSTED`, a global the hosted service spliced into the
// HTML. That is a PRIVILEGED PATH NO THIRD PARTY CAN HAVE: our own surface
// learned what the instance could do by a route unavailable to anyone else's.
// ⭐ Replacing it with a fetch means our surface goes through the same door a
// third party would — which is the test the whole capability model exists to
// pass.
//
// ⭐⭐ AND IT DISSOLVES A FOURTH CONTEXT NOBODY HAD NAMED: a SERVED EXPORT,
// where someone unzips a site and serves the files. It has no API and no mode.
// With a fetch, the descriptor's PRESENCE is the API-available signal — a
// served export simply gets no response. ⛔ It is not a flag, and no flag
// should be added for it.
//
// ⚠️ IT IS DELIBERATELY PUBLIC. A capability set says what an instance can do,
// never who is using it or what is on it, and a third party must be able to
// read it before deciding how to talk to this site. ⛔ Do not put it behind the
// session gate: that would recreate the privileged path in a new shape.

// CapabilitiesPath is where the descriptor is served.
//
// ⚠️ On the hosted service this path must be RELEASED BEFORE the generic /api/
// session check, or a third party gets 401 and concludes the instance has no
// API at all — the opposite of what the descriptor says.
const CapabilitiesPath = "/api/capabilities"

// handleCapabilities serves the descriptor derived from this instance's mode.
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, `{"error": "method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	m := s.Mode
	if m == "" {
		// ⭐ Empty means local: `polis serve` on a laptop needs no
		// configuration, and that is the common case.
		m = mode.Local
	}

	w.Header().Set("Content-Type", "application/json")
	// ⚠️ A third party may cache this briefly, but the mode can change with a
	// redeploy, so the window is short rather than absent.
	w.Header().Set("Cache-Control", "public, max-age=60")
	// ⭐ Readable cross-origin on purpose: a third-party surface deciding how
	// to talk to this site is exactly the intended reader.
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(capability.For(m))
}
