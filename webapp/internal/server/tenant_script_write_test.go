package server

import (
	"bytes"
	"context"
	"github.com/vdibart/polis-cli/cli-go/pkg/styleguide"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/publicfile"
	"github.com/vdibart/polis-cli/webapp/internal/serve"
)

// ⛔ THE PROPERTY: nothing a site's owner (or an agent holding their token) can
// send gets a file a browser will run as script into the site's served tree.
//
// On polis.pub that is what keeps one tenant out of another's visitors. Every
// tenant is same-site with every other, `polis_session` and the JS-readable
// `polis_widget_token` are `Domain=.polis.pub` cookies, and the tenant CSP is
// `script-src 'self' https://polis.pub`, where 'self' is the tenant's own
// subdomain. So tenant isolation lasts exactly as long as a tenant cannot get a
// `.js` or `.svg` (serve.ScriptCapableExtensions) served from its own origin.
// serve/static_execution_test.go covers snippets by hand; this sweeps EVERY
// route the tenant handler mounts and EVERY site API operation (which the MCP
// endpoint projects), so a new write path is covered the day it is added,
// without anyone having to list it.
//
// If this fails, do not adjust the test to pass: the change has given a
// tenant a way to run script on a polis.pub subdomain.

// hostileNames are the values every string field and path segment is set to.
var hostileNames = []string{
	"evil.js",
	"evil.svg",
	"../../evil.js",
	"posts/../evil.svg",
	"site/snippets/evil.js",
	"stream.js",
}

// hostileBody is a JSON body with every string field any handler in this
// package decodes set to v. The field names are read from the package's own
// source, so a handler that starts reading a new field is swept with it.
func hostileBody(t *testing.T, fields []string, v string) []byte {
	t.Helper()
	m := map[string]string{}
	for _, f := range fields {
		m[f] = v
	}
	return jsonBody(t, m).Bytes()
}

var stringFieldTag = regexp.MustCompile("\\bstring\\s+`json:\"([A-Za-z0-9_]+)")

func decodedStringFields(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob("*.go")
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range stringFieldTag.FindAllSubmatch(src, -1) {
			seen[string(m[1])] = true
		}
	}
	// The names a handler might read from a map rather than a struct.
	for _, f := range []string{"path", "filename", "name", "file", "id", "slug", "key", "theme", "pattern", "snippet"} {
		seen[f] = true
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

var routeLiteral = regexp.MustCompile(`mux\.HandleFunc\(\s*("([^"]+)"|([A-Za-z]+))`)

// routePatterns reads every pattern SetupRoutes registers. ⛔ A pattern it
// cannot resolve fails the test rather than being skipped, so no route can
// fall out of the sweep by being written differently.
func routePatterns(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	consts := map[string]string{
		"CapabilitiesPath":      CapabilitiesPath,
		"styleguide":            styleguide.Path, // mux.HandleFunc(styleguide.Path, …): the read regex stops at the dot
		"RecoveryChallengePath": RecoveryChallengePath,
		"RecoveryVerifyPath":    RecoveryVerifyPath,
		"EnrolPathPrefix":       EnrolPathPrefix,
		"ProvidersPath":         ProvidersPath,
		"SignInBeginPath":       SignInBeginPath,
		"SignInCompletePath":    SignInCompletePath,
		"SignOutPath":           SignOutPath,
	}
	var out []string
	for _, m := range routeLiteral.FindAllSubmatch(src, -1) {
		switch {
		case len(m[2]) > 0:
			out = append(out, string(m[2]))
		case consts[string(m[3])] != "":
			out = append(out, consts[string(m[3])])
		default:
			t.Fatalf("routes.go registers %s, which this sweep cannot resolve; add it to consts", m[1])
		}
	}
	if n := bytes.Count(src, []byte("mux.HandleFunc(")); n != len(out) {
		t.Fatalf("routes.go has %d mux.HandleFunc calls but %d were read", n, len(out))
	}
	if len(out) < 20 { // 27 after the v4 app's routes were deleted (2026-11-22)
		t.Fatalf("read only %d routes; the parser has stopped matching routes.go", len(out))
	}
	return out
}

// scriptFiles is every served, script-capable file in the site, except the v4
// stream controller when it is byte-for-byte the bundle's own.
func scriptFiles(t *testing.T, dataDir string, controller []byte) []string {
	t.Helper()
	var found []string
	filepath.WalkDir(dataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		key, _ := filepath.Rel(dataDir, p)
		key = filepath.ToSlash(key)
		if !serve.ScriptCapableExtensions[strings.ToLower(filepath.Ext(key))] || !publicfile.Servable(key) {
			return nil
		}
		if key == "stream.js" {
			if b, _ := os.ReadFile(p); bytes.Equal(b, controller) {
				return nil
			}
		}
		// ⭐ favicon.svg is GENERATED by polis from the avatar settings, which
		// site.ValidateAvatar holds to #RRGGBB colours and a fixed pattern list.
		// It is allowed only while it carries nothing a browser could run and
		// none of the values this sweep sent.
		if key == "favicon.svg" {
			if b, _ := os.ReadFile(p); svgIsInert(b) {
				return nil
			}
		}
		found = append(found, key)
		return nil
	})
	return found
}

func TestNoTenantWritePathProducesAScriptFile(t *testing.T) {
	if testing.Short() {
		t.Skip("sweeps every route")
	}
	controller, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.js")
	if err != nil {
		t.Fatal(err)
	}
	// A discovery service that refuses everything, so no call leaves the test.
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	defer ds.Close()

	s := newConfiguredServer(t)
	s.DiscoveryURL = ds.URL
	mux := http.NewServeMux()
	SetupRoutes(mux, s)
	fields := decodedStringFields(t)

	report := func(what string) {
		for _, f := range scriptFiles(t, s.DataDir, controller) {
			t.Errorf("%s left a script-capable file in the served tree: %s", what, f)
			os.Remove(filepath.Join(s.DataDir, f))
		}
	}
	report("the test site itself")

	stalled := map[string]bool{}
	// 1. Every owner route, every method, hostile path tail, and three shapes of
	// body. ⚠️ All three are needed, and a mutation proved it: a body with EVERY
	// field hostile is refused by any handler with an enum field (a snippet's
	// "source") before it writes, so it hides a hole the empty body (defaults,
	// name from the path) and the one-field bodies (name from the body) reach.
	type attempt struct {
		v    string
		body []byte
	}
	var attempts []attempt
	for _, v := range hostileNames {
		attempts = append(attempts, attempt{v, hostileBody(t, fields, v)}, attempt{v, []byte("{}")})
	}
	for _, f := range fields {
		for _, v := range []string{"evil.js", "evil.svg"} {
			attempts = append(attempts, attempt{v, hostileBody(t, []string{f}, v)})
		}
	}
	for _, pattern := range routePatterns(t) {
		start := time.Now()
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			for _, a := range attempts {
				if stalled[pattern] {
					break
				}
				v := a.v
				paths := []string{pattern}
				if strings.HasSuffix(pattern, "/") {
					paths = append(paths, pattern+v, pattern+strings.ReplaceAll(v, "/", "%2F"))
				}
				for _, p := range paths {
					// A deadline, because some routes stream until the client leaves (/api/sse).
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					req := httptest.NewRequestWithContext(ctx, method, "http://test-site.polis.pub"+p, bytes.NewReader(a.body))
					req.Header.Set("Content-Type", "application/json")
					// ⚠️ In its own goroutine with a wait limit: on this bare test server a
					// handler can panic holding a lock (the SSE client map is never made),
					// and every later request to it then blocks forever. A route that does
					// not answer is reported, never waited on.
					done := make(chan struct{})
					go func() {
						defer close(done)
						defer func() { recover() }() // a panic is another test's failure, not this one's
						mux.ServeHTTP(httptest.NewRecorder(), req)
					}()
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						stalled[pattern] = true
					}
					cancel()
				}
			}
			// ⚠️ After EACH method, not each route: a DELETE to the same route
			// removes what a PUT just wrote, which once hid a real hole.
			report(method + " " + pattern)
		}
		if stalled[pattern] {
			t.Logf("route %s did not answer within 3s for some requests (swept as far as it got)", pattern)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Logf("slow route %s: %s", pattern, d)
		}
	}

	// 2. Every site API operation (what /v1 and /mcp dispatch to), with every
	// declared input and the common addressing fields set hostile.
	engine, err := s.newContentEngine()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range engine.Operations() {
		for _, v := range hostileNames {
			payload := map[string]any{}
			for _, f := range fields {
				payload[f] = v
			}
			if props, ok := op.Input["properties"].(map[string]any); ok {
				for name := range props {
					payload[name] = v
				}
			}
			func() {
				defer func() { recover() }()
				engine.Dispatch(context.Background(), ops.ActionRequest{Action: op.Action, ContentType: op.Type, Payload: payload})
			}()
		}
		report("site API " + op.Type + " " + op.Action)
	}
}

var svgActive = regexp.MustCompile(`(?i)<script|<foreignobject|\bon[a-z]+\s*=|javascript:|href\s*=|<iframe|<embed|<object`)

func svgIsInert(b []byte) bool {
	if svgActive.Match(b) {
		return false
	}
	for _, v := range hostileNames {
		if bytes.Contains(b, []byte(v)) {
			return false
		}
	}
	return true
}
