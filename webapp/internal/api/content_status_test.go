package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Status codes the v1 content API answers for requests that are the caller's
// to get right (Signet epic 41's findings). None of them is a server fault.
func TestContentAPIAnswersCallerMistakesAsClientErrors(t *testing.T) {
	mux, _, _ := testSetup(t)
	cases := []struct {
		path string
		want int
	}{
		// The route passes {name} as "id"; the handler read only "name" → 400.
		{"/v1/content/theme/vice", http.StatusOK},
		{"/v1/content/pub.polis.theme/vice", http.StatusOK},
		{"/v1/content/theme/no-such-theme", http.StatusNotFound},
		// ⚠️ Not found is decided by a sentinel error, not by the words in the
		// message: handleDispatchError matches "invalid" before "not found", so
		// a theme name containing it used to answer 400.
		{"/v1/content/theme/invalid-theme-name", http.StatusNotFound},
		// Declared types with no API operations → 500.
		{"/v1/content/attestation", http.StatusBadRequest},
		{"/v1/content/pub.polis.attestation/x", http.StatusBadRequest},
		{"/v1/content/actor", http.StatusBadRequest},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.want {
			t.Errorf("GET %s = %d, want %d: %s", tc.path, w.Code, tc.want, w.Body.String())
		}
	}
}
