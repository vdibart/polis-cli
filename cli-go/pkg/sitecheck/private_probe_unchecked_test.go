package sitecheck

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// refusingServer answers the home page, refuses or fails on the private names
// listed in answers (by path), and 404s the rest.
func refusingServer(t *testing.T, answers map[string]int, hangUp map[string]bool) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte("<html>home</html>"))
			return
		}
		if hangUp[r.URL.RawPath] || hangUp[r.URL.Path] {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		if code, ok := answers[r.URL.Path]; ok {
			http.Error(w, http.StatusText(code), code)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// A 429, a 5xx or no response says the server did not answer, not that it
// would not serve the file. The probe used to count those as "not served"; a
// rate-limited Judge sweep reported 871 private names as safe that way.
func TestProbePrivateReportsRefusedAndFailedNamesAsUnchecked(t *testing.T) {
	ts := refusingServer(t,
		map[string]int{"/.polis/keys/id_ed25519": 429, "/.env": 503, "/.git/config": 500},
		map[string]bool{"/.ssh/id_ed25519": true})

	res := ProbePrivate(ts.URL, "", nil, PrivateProbeTargets())
	if len(res.Exposures) != 0 {
		t.Errorf("exposures = %+v, want none", res.Exposures)
	}
	// The probe also asks for encoded spellings of the key path, which this
	// server decodes to the same file, so compare decoded paths.
	got := map[string]int{}
	for _, u := range res.Unchecked {
		p, _ := url.PathUnescape(strings.TrimPrefix(u.URL, ts.URL))
		if s, seen := got[p]; seen && s != u.Status {
			t.Errorf("%s answered both %d and %d", p, s, u.Status)
		}
		got[p] = u.Status
	}
	want := map[string]int{"/.polis/keys/id_ed25519": 429, "/.env": 503, "/.git/config": 500, "/.ssh/id_ed25519": 0}
	if len(got) != len(want) {
		t.Errorf("unchecked = %v, want exactly %v", got, want)
	}
	for path, status := range want {
		if s, ok := got[path]; !ok || s != status {
			t.Errorf("%s: unchecked status = %d (listed %v), want %d", path, s, ok, status)
		}
	}
	if res.Unanswered != 1 || res.Checked() != res.Asked-len(res.Unchecked) {
		t.Errorf("unanswered %d, checked %d of %d", res.Unanswered, res.Checked(), res.Asked)
	}
}

// A private key in an error body is still an exposure: the refusal does not
// hide what came back.
func TestProbePrivateKeyInARefusalIsStillAnExposure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.polis/keys/id_ed25519" {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(testKey))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	res := ProbePrivate(ts.URL, "", nil, []string{"/.polis/keys/id_ed25519"})
	if len(res.Exposures) != 1 || !res.Exposures[0].KeyHeader || len(res.Unchecked) != 0 {
		t.Errorf("exposures %+v unchecked %+v; want the key reported as exposed", res.Exposures, res.Unchecked)
	}
}

// polis validate <url>: a name that could not be checked is named as NOT
// checked, never folded into "none is served".
func TestRemotePrivateFilesCheckNamesWhatItCouldNotCheck(t *testing.T) {
	ts := refusingServer(t, map[string]int{"/.git/config": 429, "/.env": 502}, nil)
	r := &Report{}
	NewRemote().checkRemotePrivateFiles(r, ts.URL)
	c := checkByID(t, r, "identity.private_files_served")
	if c.Outcome != OutcomePassed {
		t.Fatalf("outcome %v, want passed (for what was checked): %+v", c.Outcome, c)
	}
	if !strings.Contains(c.Detail, "2 more NOT checked") {
		t.Errorf("detail does not say 2 were not checked: %q", c.Detail)
	}
	joined := strings.Join(c.Findings, "\n")
	for _, want := range []string{"/.git/config: NOT checked — refused (429", "/.env: NOT checked — server error (502)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("findings do not name %q:\n%s", want, joined)
		}
	}
	if c.Examined != len(PrivateProbeTargets())-2 {
		t.Errorf("examined = %d, want %d (the unchecked are not counted as examined)", c.Examined, len(PrivateProbeTargets())-2)
	}

	// Every name refused: nothing was checked, so the check did not run.
	all := map[string]int{}
	for _, p := range PrivateProbeTargets() {
		all[p] = 429
	}
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte("<html>home</html>"))
			return
		}
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	}))
	defer ts2.Close()
	r2 := &Report{}
	NewRemote().checkRemotePrivateFiles(r2, ts2.URL)
	if c := checkByID(t, r2, "identity.private_files_served"); c.Outcome != OutcomeNotApplicable {
		t.Errorf("every name refused: outcome %v, want not_applicable", c.Outcome)
	}
}
