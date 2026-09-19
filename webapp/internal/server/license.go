package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// publishConfigWithLicense returns the publish config carrying the author's
// per-work licence override.
//
// DiscoveryConfig() returns nil when discovery is unconfigured — the ordinary
// localhost case — and publish then falls back to package globals. Those
// globals are set from these same server fields (server.go), so rebuilding the
// struct here is behaviourally identical while giving the licence override
// somewhere to live. Without this the localhost path would nil-deref.
func (s *Server) publishConfigWithLicense(authoredLicense string) *publish.DiscoveryConfig {
	cfg := s.DiscoveryConfig()
	if cfg == nil {
		if authoredLicense == "" {
			return nil // nothing to carry; keep the existing globals path
		}
		cfg = &publish.DiscoveryConfig{
			DiscoveryURL: s.DiscoveryURL,
			DiscoveryKey: s.DiscoveryKey,
			BaseURL:      s.BaseURL,
			Generator:    "polis-cli-go/" + s.CLIVersion,
		}
	}
	cfg.LicenseProfile = authoredLicense
	return cfg
}

// logLicenseMaterialised records the terms a work was actually published
// under, read back from the file that was just written.
//
// It reads the result rather than the intent on purpose. What matters
// operationally is what a work SAYS, not what we meant it to say — if
// resolution ever went wrong, the log should show the wrong answer rather than
// the right intention. It is also the only record that survives to answer "what
// terms did this go out with?" without re-reading the corpus.
//
// A work published with no terms logs stated:false rather than nothing at all,
// so "the author states nothing" and "the event never fired" stay
// distinguishable in Axiom.
func (s *Server) logLicenseMaterialised(postPath string) {
	fields := map[string]interface{}{"path": postPath, "stated": false}

	data, err := os.ReadFile(filepath.Join(s.DataDir, postPath))
	if err == nil {
		if terms := license.ParseBlock(string(data)); terms != nil {
			fields["stated"] = true
			fields["profile"] = terms.Profile
			fields["content_usage"] = license.ContentUsage(terms)
			if terms.AIInput != "" {
				fields["ai_input"] = terms.AIInput
			}
		}
	}
	s.LogEvent("pub.polis.license.materialised", fields)
}

// licenseSettings describes the site's current terms for the settings screen.
//
// Always returns a value, never nil, and always carries `stated`. "This site
// says nothing" and "the field is missing" have to stay distinguishable in the
// UI: the first deserves a prompt to choose, the second is a bug.
func (s *Server) licenseSettings() map[string]interface{} {
	out := map[string]interface{}{"stated": false}

	terms, err := site.SiteTerms(s.DataDir)
	if err != nil || terms == nil {
		return out
	}
	out["stated"] = true
	out["profile"] = terms.Profile
	out["content_usage"] = license.ContentUsage(terms)
	out["summary"] = render.TermsSummary(terms)
	out["terms_url"] = terms.Terms
	out["asserted"] = terms.Asserted
	return out
}

// handleSiteLicense states or withdraws the site's terms.
//
// ⚠️ This signs with the site's key on the owner's behalf, which is why it is
// an owner-authenticated route and why the CHOICE always comes from the user.
// The server never picks a profile — it applies the one it was handed. An
// operator-chosen licence would be the operator speaking for the author, in her
// name, under her key.
//
// Stating terms is not retroactive and this endpoint does not pretend
// otherwise: already-published works keep the terms they were signed with. Only
// works published after this call carry the new ones.
func (s *Server) handleSiteLicense(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Profile string `json:"profile"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Withdrawal must be named. ParseProfileName reads "" as "none" — right at
	// the CLI prompt, wrong here, where `{}` or a client bug would otherwise
	// withdraw the user's signed terms without their having chosen anything.
	if strings.TrimSpace(req.Profile) == "" {
		http.Error(w, `Missing 'profile' (want: reserved, open, none)`, http.StatusBadRequest)
		return
	}

	profile, stated, err := license.ParseProfileName(req.Profile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !stated {
		if err := site.WithdrawLicense(s.DataDir); err != nil {
			s.LogError("Failed to withdraw licence: %v", err)
			http.Error(w, "Failed to withdraw licence", http.StatusInternalServerError)
			return
		}
		s.LogEvent("pub.polis.license.withdrawn", map[string]interface{}{})
	} else {
		if _, err := site.StateLicense(s.DataDir, req.Profile, s.GetBaseURL(), s.PrivateKey); err != nil {
			s.LogError("Failed to state licence: %v", err)
			http.Error(w, "Failed to state licence", http.StatusInternalServerError)
			return
		}
		s.LogEvent("pub.polis.license.stated", map[string]interface{}{"profile": profile})
	}

	// Regenerate the projections so robots.txt, rsl.xml, and the terms page
	// stop disagreeing with the signed source the moment it changes.
	if renderer, rerr := render.NewPageRenderer(render.PageConfig{
		DataDir: s.DataDir,
		BaseURL: s.GetBaseURL(),
	}); rerr == nil {
		if lerr := renderer.RenderLicenseSurfaces(); lerr != nil {
			s.LogError("Failed to regenerate licence surfaces: %v", lerr)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"license": s.licenseSettings(),
	})
}
