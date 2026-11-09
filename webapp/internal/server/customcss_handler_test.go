package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

func TestHandleCustomCSS(t *testing.T) {
	s := newConfiguredServer(t)
	if err := bundle.EnsureReferencePayload(s.Site(), "pub.polis.core"); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.handleCustomCSS(w, httptest.NewRequest(http.MethodGet, "/api/settings/custom-css", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"css":""`) {
		t.Fatalf("GET with none = %d %s", w.Code, w.Body.String())
	}
	for _, c := range []struct {
		body string
		want int
	}{
		{`{"css":"a { color: red; }"}`, http.StatusOK},
		{`{"css":"@import 'x.css';"}`, http.StatusBadRequest},
		{`{}`, http.StatusBadRequest},
		{`not json`, http.StatusBadRequest},
	} {
		w = httptest.NewRecorder()
		s.handleCustomCSS(w, httptest.NewRequest(http.MethodPost, "/api/settings/custom-css", strings.NewReader(c.body)))
		if w.Code != c.want {
			t.Errorf("POST %s = %d %s, want %d", c.body, w.Code, w.Body.String(), c.want)
		}
	}
	w = httptest.NewRecorder()
	s.handleCustomCSS(w, httptest.NewRequest(http.MethodGet, "/api/settings/custom-css", nil))
	if !strings.Contains(w.Body.String(), "color: red") {
		t.Errorf("GET after set = %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	s.handleCustomCSS(w, httptest.NewRequest(http.MethodPut, "/api/settings/custom-css", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT = %d, want 405", w.Code)
	}
}

func TestCustomCSSRouteIsOwnerOnly(t *testing.T) {
	s, _ := passkeyServer(t)
	if code, _ := gateRefusal(t, s, "/api/settings/custom-css"); code == http.StatusOK {
		t.Error("/api/settings/custom-css passed the owner gate without a session")
	}
}

// ⭐ The palette the app follows: the active theme's :root properties, and the
// ones the site's own CSS sets over them.
func TestHandleSitePalette(t *testing.T) {
	s := newConfiguredServer(t)
	if err := bundle.EnsureReferencePayload(s.Site(), "pub.polis.core"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.handleCustomCSS(w, httptest.NewRequest(http.MethodPost, "/api/settings/custom-css",
		strings.NewReader(`{"css":":root {\n  --color-bg: #fff8ef;\n}\nh1 { color: red; }"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("set custom css = %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.handleSitePalette(w, httptest.NewRequest(http.MethodGet, "/api/site/palette", nil))
	var resp struct {
		Theme      string `json:"theme"`
		Properties []struct{ Name, Value string }
		Custom     []struct{ Name, Value string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != http.StatusOK {
		t.Fatalf("palette = %d %s", w.Code, w.Body.String())
	}
	if resp.Theme == "" || len(resp.Properties) == 0 {
		t.Errorf("palette lacks the theme or its properties: %s", w.Body.String())
	}
	if len(resp.Custom) != 1 || resp.Custom[0].Name != "--color-bg" || resp.Custom[0].Value != "#fff8ef" {
		t.Errorf("custom = %+v, want the one :root override", resp.Custom)
	}
	if code, _ := gateRefusal(t, mustPasskeyServer(t), "/api/site/palette"); code == http.StatusOK {
		t.Error("/api/site/palette passed the owner gate without a session")
	}
}

func mustPasskeyServer(t *testing.T) *Server {
	t.Helper()
	s, _ := passkeyServer(t)
	return s
}
