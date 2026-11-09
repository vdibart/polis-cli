package sitecheck

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

const testKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----\n"

func TestProbePrivateReportsAServedKeyByURLAndStatusOnly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte("<html>home</html>"))
		case "/.polis/keys/id_ed25519":
			w.Write([]byte(testKey))
		case "/content/.git/config":
			w.Write([]byte("[core]\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	res := ProbePrivate(ts.URL, "", nil, PrivateProbeTargets())
	got := map[string]PrivateExposure{}
	for _, e := range res.Exposures {
		got[strings.TrimPrefix(e.URL, ts.URL)] = e
	}
	if e, ok := got["/.polis/keys/id_ed25519"]; !ok || e.Status != 200 || !e.KeyHeader {
		t.Errorf("served key not reported with its key header: %+v", got)
	}
	if e, ok := got["/content/.git/config"]; !ok || e.KeyHeader {
		t.Errorf("served git config not reported (or wrongly flagged as a key): %+v", got)
	}
	// The encoded spellings of the key path reach the same file on a server
	// that decodes before routing, so they are exposures too; nothing else is.
	for p := range got {
		if dec, _ := url.PathUnescape(p); dec != "/.polis/keys/id_ed25519" && dec != "/content/.git/config" {
			t.Errorf("unexpected exposure %s", p)
		}
	}
	if _, ok := got["/%2Epolis/keys/id_ed25519"]; !ok {
		t.Errorf("the encoded spelling of the key path was not reported")
	}
	if res.Asked != len(PrivateProbeTargets()) || res.Unanswered != 0 {
		t.Errorf("asked %d unanswered %d", res.Asked, res.Unanswered)
	}
}

// A host whose fallback answers 200 with the home page for any path is not
// serving the file asked for.
func TestProbePrivateIgnoresAHomePageFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>the app shell</html>"))
	}))
	defer ts.Close()
	if res := ProbePrivate(ts.URL, "", nil, PrivateProbeTargets()); len(res.Exposures) != 0 {
		t.Errorf("an SPA fallback was reported as an exposure: %+v", res.Exposures)
	}
}

// A key in the body is an exposure whatever the status.
func TestProbePrivateReportsAKeyInAnErrorBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.polis/keys/id_ed25519" {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(testKey))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	res := ProbePrivate(ts.URL, "", nil, []string{"/.polis/keys/id_ed25519"})
	if len(res.Exposures) != 1 || res.Exposures[0].Status != 403 || !res.Exposures[0].KeyHeader {
		t.Errorf("a 403 carrying a key was not reported: %+v", res.Exposures)
	}
}

func TestProbePrivateNeverFollowsARedirect(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.env":
			http.Redirect(w, r, "/leak", http.StatusFound)
		case "/leak":
			w.Write([]byte(testKey))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	if res := ProbePrivate(ts.URL, "", nil, []string{"/.env"}); len(res.Exposures) != 0 {
		t.Errorf("the probe followed a redirect into a body: %+v", res.Exposures)
	}
}

func TestProbePrivateSendsTheHostHeader(t *testing.T) {
	var hosts []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)
		http.NotFound(w, r)
	}))
	defer ts.Close()
	ProbePrivate(ts.URL, "alice.example", nil, []string{"/.env"})
	for _, h := range hosts {
		if h != "alice.example" {
			t.Errorf("request went to host %q", h)
		}
	}
}

func TestProbePrivateNeverAsksForVersionHistory(t *testing.T) {
	for _, p := range PrivateProbeTargets() {
		if strings.Contains(strings.ToLower(p), ".versions") || strings.HasPrefix(p, "/.well-known") {
			t.Errorf("the probe asks for a public name: %s", p)
		}
	}
}

// ⛔ An exposure report carries the URL and status, never the body. A field
// that could hold bytes would be the first step to logging a private key.
func TestPrivateExposureCannotCarryABody(t *testing.T) {
	typ := reflect.TypeOf(PrivateExposure{})
	want := map[string]reflect.Kind{"URL": reflect.String, "Status": reflect.Int, "KeyHeader": reflect.Bool}
	if typ.NumField() != len(want) {
		t.Fatalf("PrivateExposure has %d fields; it must have exactly URL, Status and KeyHeader", typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if k, ok := want[f.Name]; !ok || f.Type.Kind() != k {
			t.Errorf("unexpected field %s %s", f.Name, f.Type)
		}
	}
}

func TestRemotePrivateFilesCheckFailsOnAnExposure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.polis/keys/id_ed25519" {
			w.Write([]byte(testKey))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	r := &Report{}
	NewRemote().checkRemotePrivateFiles(r, ts.URL)
	c := checkByID(t, r, "identity.private_files_served")
	if c.Outcome != OutcomeFailed {
		t.Fatalf("outcome %v, want failed: %+v", c.Outcome, c)
	}
	joined := strings.Join(c.Findings, "\n")
	if !strings.Contains(joined, "/.polis/keys/id_ed25519") || strings.Contains(joined, "b3BlbnNzaC") {
		t.Errorf("finding must name the URL and never the body: %q", joined)
	}
}

func TestRemotePrivateFilesCheckPassesOnACleanSite(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	r := &Report{}
	NewRemote().checkRemotePrivateFiles(r, ts.URL)
	if c := checkByID(t, r, "identity.private_files_served"); c.Outcome != OutcomePassed {
		t.Errorf("outcome %v, want passed", c.Outcome)
	}
}

// nonceShell is an app page carrying a fresh CSP nonce per response, as the
// self-hosted server's fallback writes one.
func nonceShell(extra string) []byte {
	return []byte(`<html><script type="importmap" nonce="` + testNonce() + `">{}</script>` + extra + `</html>`)
}

var nonceSeq int

func testNonce() string {
	nonceSeq++
	return strings.Repeat(string(rune('A'+nonceSeq%26)), 22) + "=="
}

// ⭐ A fallback that differs from the home page ONLY by its nonce is the home
// page: polis validate passes against the self-hosted server's own fallback.
func TestProbePrivateIgnoresAFallbackThatDiffersOnlyByNonce(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(nonceShell(""))
	}))
	defer ts.Close()
	if res := ProbePrivate(ts.URL, "", nil, PrivateProbeTargets()); len(res.Exposures) != 0 {
		t.Errorf("a fallback differing only by nonce was reported as %d exposures", len(res.Exposures))
	}
	r := &Report{}
	NewRemote().checkRemotePrivateFiles(r, ts.URL)
	if c := checkByID(t, r, "identity.private_files_served"); c.Outcome != OutcomePassed {
		t.Errorf("outcome %v, want passed: %+v", c.Outcome, c)
	}
}

// ⛔ Masking the nonce excuses nothing else: a 2xx that differs from the home
// page in anything but the nonce is still served, and a key is an exposure.
func TestProbePrivateStillReportsAnythingButTheNonce(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write(nonceShell(""))
		case "/.env":
			w.Write(nonceShell("POLIS_BASE_URL=x"))
		case "/.polis/keys/id_ed25519":
			w.WriteHeader(http.StatusNotFound)
			w.Write(append(nonceShell(""), testKey...))
		default:
			w.Write(nonceShell(""))
		}
	}))
	defer ts.Close()
	res := ProbePrivate(ts.URL, "", nil, []string{"/.env", "/.polis/keys/id_ed25519", "/.polis/storage-salt"})
	got := map[string]PrivateExposure{}
	for _, e := range res.Exposures {
		got[strings.TrimPrefix(e.URL, ts.URL)] = e
	}
	if _, ok := got["/.env"]; !ok {
		t.Error("⛔ a page differing from the home page beyond its nonce was excused")
	}
	if e, ok := got["/.polis/keys/id_ed25519"]; !ok || !e.KeyHeader || e.Status != 404 {
		t.Errorf("⛔ a key in a 404 body was not reported: %+v", got)
	}
	if _, ok := got["/.polis/storage-salt"]; ok || len(got) != 2 {
		t.Errorf("the plain fallback was reported: %+v", got)
	}
}

// ⭐ A rendered home page with an app behind every other path (a remote
// instance on Vercel): the app page a private name gets is the server's
// unknown-path fallback, not the file.
func TestProbePrivateIgnoresAnAppFallbackThatIsNotTheHomePage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte("<html>the rendered site</html>"))
			return
		}
		w.Write(nonceShell("the owner app"))
	}))
	defer ts.Close()
	if res := ProbePrivate(ts.URL, "", nil, PrivateProbeTargets()); len(res.Exposures) != 0 {
		t.Errorf("an app fallback was reported as %d exposures", len(res.Exposures))
	}
	r := &Report{}
	NewRemote().checkRemotePrivateFiles(r, ts.URL)
	if c := checkByID(t, r, "identity.private_files_served"); c.Outcome != OutcomePassed {
		t.Errorf("outcome %v, want passed: %+v", c.Outcome, c)
	}
}

// ⛔ The fallback excuses only itself: a private name answered with anything
// else is served, and a key is an exposure, though both differ from the home
// page and the fallback alike.
func TestProbePrivateStillReportsWhatIsNeitherHomeNorFallback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Write([]byte("<html>the rendered site</html>"))
		case "/.env":
			w.Write([]byte("POLIS_BASE_URL=https://x.example\n"))
		case "/.polis/keys/id_ed25519":
			w.Write(append(nonceShell("the owner app"), testKey...))
		default:
			w.Write(nonceShell("the owner app"))
		}
	}))
	defer ts.Close()
	res := ProbePrivate(ts.URL, "", nil, []string{"/.env", "/.polis/keys/id_ed25519", "/.polis/storage-salt"})
	got := map[string]PrivateExposure{}
	for _, e := range res.Exposures {
		got[strings.TrimPrefix(e.URL, ts.URL)] = e
	}
	if _, ok := got["/.env"]; !ok {
		t.Error("⛔ a served .env was excused")
	}
	if e, ok := got["/.polis/keys/id_ed25519"]; !ok || !e.KeyHeader {
		t.Error("⛔ a key inside an app-page body was excused")
	}
	if _, ok := got["/.polis/storage-salt"]; ok {
		t.Error("the plain fallback was reported")
	}
	if res.Asked != 3 {
		t.Errorf("asked %d; the fallback request must not count as a probe target", res.Asked)
	}
}
