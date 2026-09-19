package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Settings → "Publish no terms" must leave nothing behind.
//
// This path was the sharpest illustration of the mirror defect: it called
// RenderLicenseSurfaces immediately after withdrawing, under a comment saying
// the projections must "stop disagreeing with the signed source the moment it
// changes" — and that function opened with `if terms == nil { return nil }`.
// It did nothing in precisely the case where the disagreement is created.
func TestSettingsWithdrawalRemovesEveryProjection(t *testing.T) {
	s := newConfiguredServer(t)
	if err := bundle.EnsureReferencePayload(s.DataDir, "pub.polis.core"); err != nil {
		t.Fatalf("install reference payload: %v", err)
	}

	// State terms through the same endpoint, so the fixture is what a user's
	// own click produces rather than something the test arranged.
	post := func(profile string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/site/license", jsonBody(t, map[string]string{"profile": profile}))
		w := httptest.NewRecorder()
		s.handleSiteLicense(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("POST /api/site/license %q = %d: %s", profile, w.Code, w.Body.String())
		}
	}
	post("reserved")

	mount := site.LicenseMountDir(s.DataDir)
	surfaces := []string{
		"robots.txt",
		"rsl.xml",
		filepath.Join(mount, "index.html"),
		filepath.Join(mount, "reserved-1.html"),
	}
	for _, rel := range surfaces {
		if _, err := os.Stat(filepath.Join(s.DataDir, rel)); err != nil {
			t.Fatalf("stating terms did not write %s — %v", rel, err)
		}
	}

	post("none")

	for _, rel := range append(surfaces, mount) {
		if _, err := os.Stat(filepath.Join(s.DataDir, rel)); !os.IsNotExist(err) {
			t.Errorf("%s survived withdrawal from Settings", rel)
		}
	}
	if terms, err := site.SiteTerms(s.DataDir); err != nil || terms != nil {
		t.Errorf("after withdrawal SiteTerms = %v, %v; want nil, nil", terms, err)
	}

	// And the whole point: the site is now indistinguishable from one that
	// never stated terms.
	fresh := newConfiguredServer(t)
	if err := bundle.EnsureReferencePayload(fresh.DataDir, "pub.polis.core"); err != nil {
		t.Fatalf("install reference payload: %v", err)
	}
	renderer, err := render.NewPageRenderer(render.PageConfig{DataDir: fresh.DataDir, BaseURL: fresh.BaseURL})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if err := renderer.RenderLicenseSurfaces(); err != nil {
		t.Fatalf("RenderLicenseSurfaces: %v", err)
	}
	for _, rel := range append(surfaces, mount) {
		_, withdrawnErr := os.Stat(filepath.Join(s.DataDir, rel))
		_, neverErr := os.Stat(filepath.Join(fresh.DataDir, rel))
		if os.IsNotExist(withdrawnErr) != os.IsNotExist(neverErr) {
			t.Errorf("%s: withdrawn site and never-stated site disagree (%v vs %v)", rel, withdrawnErr, neverErr)
		}
	}
}

// Settings must read sensibly for a tenant that has never stated terms.
//
// Removing the hosted-signup default makes this EVERY new tenant's state
// rather than an edge case, so the never-stated branch is now the common one.
// It is also the whole discoverable path — polis.pub asks nothing at
// registration, deliberately — so it has to stand on its own on day one.
//
// ⛔ The shape matters as much as the flag: `stated` false with no profile,
// summary or content_usage alongside it. A leaked field from a prior state is
// how the UI would end up rendering terms nobody stated.
func TestSettingsLicenceSectionForATenantThatNeverStatedTerms(t *testing.T) {
	s := newConfiguredServer(t)

	info := s.licenseSettings()

	if stated, ok := info["stated"].(bool); !ok || stated {
		t.Fatalf("licenseSettings()[\"stated\"] = %v; want false", info["stated"])
	}
	for _, key := range []string{"profile", "summary", "content_usage", "terms_url", "asserted"} {
		if v, present := info[key]; present {
			t.Errorf("never-stated tenant carries %s = %v; the UI must have nothing to render as terms", key, v)
		}
	}

	// And it arrives on the payload Settings actually reads, under the key the
	// client destructures — `settings.license || { stated: false }`.
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	w := httptest.NewRecorder()
	s.handleSettings(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/settings = %d: %s", w.Code, w.Body.String())
	}
	var payload struct {
		License map[string]interface{} `json:"license"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if payload.License == nil {
		t.Fatal("/api/settings omits `license` entirely")
	}
	if stated, _ := payload.License["stated"].(bool); stated {
		t.Errorf("/api/settings reports stated=true for a site with no licence")
	}
}

// An empty or missing profile is not a choice. license.ParseProfileName reads
// "" as "none" (right for the CLI prompt, where enter must not state terms),
// but on this endpoint it meant a malformed request — `{}` or a client bug —
// silently withdrew the user's signed terms. Withdrawal must be named.
func TestSiteLicenseEmptyProfileIsRejectedAndKeepsTerms(t *testing.T) {
	s := newConfiguredServer(t)
	if err := bundle.EnsureReferencePayload(s.DataDir, "pub.polis.core"); err != nil {
		t.Fatalf("install reference payload: %v", err)
	}
	post := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/site/license", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.handleSiteLicense(w, req)
		return w
	}
	if w := post(`{"profile":"reserved"}`); w.Code != http.StatusOK {
		t.Fatalf("state reserved = %d: %s", w.Code, w.Body.String())
	}

	for _, body := range []string{`{}`, `{"profile":""}`, `{"profile":"   "}`, `{"profile":null}`} {
		if w := post(body); w.Code != http.StatusBadRequest {
			t.Errorf("POST %s = %d, want 400: %s", body, w.Code, w.Body.String())
		}
		if terms, err := site.SiteTerms(s.DataDir); err != nil || terms == nil {
			t.Fatalf("POST %s withdrew the terms (SiteTerms = %v, %v)", body, terms, err)
		}
	}

	for _, word := range []string{"unstated", "none"} {
		if w := post(`{"profile":"reserved"}`); w.Code != http.StatusOK {
			t.Fatalf("restate = %d: %s", w.Code, w.Body.String())
		}
		if w := post(`{"profile":"` + word + `"}`); w.Code != http.StatusOK {
			t.Fatalf("withdraw with %q = %d: %s", word, w.Code, w.Body.String())
		}
		if terms, err := site.SiteTerms(s.DataDir); err != nil || terms != nil {
			t.Errorf("%q did not withdraw (SiteTerms = %v, %v)", word, terms, err)
		}
	}
}
