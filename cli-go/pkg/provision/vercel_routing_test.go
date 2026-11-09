package provision

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// realBuilderConfig is the config.json `vercel build` wrote for the function
// in the first live build, 2026-10-12 (webapp/deploy/vercel/deploy.sh, Vercel CLI 50.1.3).
const realBuilderConfig = `{
  "version": 3,
  "routes": [
    { "handle": "filesystem" },
    { "src": "^(?:/(.*))$", "dest": "/api/index", "check": true },
    { "src": "^/api(/.*)?$", "status": 404 },
    { "handle": "error" },
    { "status": 404, "src": "^(?!/api).*$", "dest": "/404.html" },
    { "handle": "miss" },
    { "src": "^/api/(.+)(?:\\.(?:go))$", "dest": "/api/$1", "check": true }
  ],
  "crons": []
}`

const testCSP = "default-src 'self'; frame-ancestors 'self'"

// headersFor plays a response through the generated routes the way the router
// does for headers: every header route before the filesystem phase applies to
// every response, and the hit phase's apply only when a static file answered.
func headersFor(t *testing.T, cfg []byte, p string, static bool) map[string]string {
	t.Helper()
	var c struct {
		Routes []map[string]any `json:"routes"`
	}
	if err := json.Unmarshal(cfg, &c); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	phase := ""
	for _, r := range c.Routes {
		if h, ok := r["handle"].(string); ok {
			phase = h
			continue
		}
		hs, ok := r["headers"].(map[string]any)
		if !ok {
			continue
		}
		if phase != "" && !(phase == "hit" && static) {
			continue
		}
		if src, _ := r["src"].(string); regexp.MustCompile(src).MatchString(p) {
			for k, v := range hs {
				got[k] = v.(string)
			}
		}
	}
	return got
}

// ⭐ Static files get the types Vercel cannot infer and the page CSP; the
// .well-known rules reach every response; the function gets no CSP from the
// routing, because it sets its own.
func TestTheInstanceRoutingSetsHeadersOnStaticFilesOnly(t *testing.T) {
	cfg, err := VercelInstanceConfig([]byte(realBuilderConfig), InstanceRouting{PageCSP: testCSP})
	if err != nil {
		t.Fatal(err)
	}
	type want struct{ key, value string }
	for _, c := range []struct {
		path   string
		static bool
		want   []want
		absent []string
	}{
		{"/", true, []want{{"Content-Security-Policy", testCSP}, {"X-Content-Type-Options", "nosniff"}}, nil},
		{"/index.html", true, []want{{"Content-Security-Policy", testCSP}}, nil},
		{"/posts/20261013/hello.html", true, []want{{"Content-Security-Policy", testCSP}}, nil},
		// ⛔ A directory's index answers a path with no .html in it.
		{"/posts/", true, []want{{"Content-Security-Policy", testCSP}}, nil},
		{"/posts/20261013/", true, []want{{"Content-Security-Policy", testCSP}}, nil},
		{"/.well-known/polis", true, []want{{"Content-Type", "application/json; charset=utf-8"},
			{"Access-Control-Allow-Origin", "*"}, {"Cache-Control", "public, max-age=60"}}, []string{"Content-Security-Policy"}},
		{"/policies/rules.jsonl", true, []want{{"Content-Type", "application/jsonl; charset=utf-8"}}, []string{"Content-Security-Policy"}},
		{"/public.jsonl", true, []want{{"Content-Type", "application/jsonl; charset=utf-8"}}, nil},
		{"/styles.css", true, []want{{"X-Content-Type-Options", "nosniff"}}, []string{"Content-Security-Policy", "Content-Type"}},
		{"/robots.txt", true, []want{{"Cache-Control", "public, max-age=300"}}, nil},
		// ⛔ The function: no page CSP and no content type from the routing.
		{"/_/", false, nil, []string{"Content-Security-Policy", "Content-Type"}},
		{"/index.html", false, nil, []string{"Content-Security-Policy"}},
		{"/.well-known/polis", false, []want{{"Access-Control-Allow-Origin", "*"}}, []string{"Content-Type"}},
	} {
		got := headersFor(t, cfg, c.path, c.static)
		for _, w := range c.want {
			if got[w.key] != w.value {
				t.Errorf("%s (static=%v): %s = %q; want %q", c.path, c.static, w.key, got[w.key], w.value)
			}
		}
		for _, k := range c.absent {
			if v, ok := got[k]; ok {
				t.Errorf("%s (static=%v): %s is set (%q); it must not be", c.path, c.static, k, v)
			}
		}
	}
}

// ⛔ The routing adds headers and nothing else: no rewrite, no destination,
// and the build's own routes survive in order.
func TestTheInstanceRoutingAddsNoRewrite(t *testing.T) {
	cfg, err := VercelInstanceConfig([]byte(realBuilderConfig), InstanceRouting{PageCSP: testCSP})
	if err != nil {
		t.Fatal(err)
	}
	var in, out struct {
		Routes []map[string]any `json:"routes"`
	}
	_ = json.Unmarshal([]byte(realBuilderConfig), &in)
	_ = json.Unmarshal(cfg, &out)
	var built []string
	for _, r := range in.Routes {
		b, _ := json.Marshal(r)
		built = append(built, string(b))
	}
	var kept []string
	for _, r := range out.Routes {
		if _, ok := r["headers"]; ok {
			if _, ok := r["dest"]; ok {
				t.Errorf("a header route has a destination: %v", r)
			}
			continue
		}
		if r["handle"] == "hit" {
			continue
		}
		b, _ := json.Marshal(r)
		kept = append(kept, string(b))
	}
	if strings.Join(kept, "\n") != strings.Join(built, "\n") {
		t.Errorf("the build's own routes changed:\n got %v\nwant %v", kept, built)
	}
	s := string(cfg)
	for _, bad := range []string{"blob.vercel-storage.com", "/_blob", ".private."} {
		if strings.Contains(s, bad) {
			t.Errorf("the routing still names %s", bad)
		}
	}
	if !strings.Contains(s, `"crons"`) {
		t.Error("the build's crons were dropped")
	}
}

// ⭐ The generated routing is pinned: a change to it is a change to how every
// instance serves, and shows up here as a diff to review.
// POLIS_UPDATE_GOLDEN=1 rewrites the golden file.
func TestTheInstanceRoutingMatchesItsGolden(t *testing.T) {
	cfg, err := VercelInstanceConfig([]byte(realBuilderConfig), InstanceRouting{PageCSP: testCSP})
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "vercel_instance_config.golden.json")
	if os.Getenv("POLIS_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, append(cfg, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read %s: %v (POLIS_UPDATE_GOLDEN=1 writes it)", golden, err)
	}
	if strings.TrimSpace(string(want)) != strings.TrimSpace(string(cfg)) {
		t.Errorf("the generated routing differs from %s; review the change, then POLIS_UPDATE_GOLDEN=1", golden)
	}
}

// ⛔ The page CSP is required, and must be one header value.
func TestTheInstanceRoutingRefusesABadPageCSP(t *testing.T) {
	for _, csp := range []string{"", "  ", "default-src 'self'\r\nX-Evil: 1", "a\nb", pageCSPPlaceholder} {
		if _, err := VercelInstanceConfig([]byte(realBuilderConfig), InstanceRouting{PageCSP: csp}); err == nil {
			t.Errorf("page CSP %q was accepted", csp)
		}
	}
}

// ⛔ A build without a filesystem phase is refused rather than guessed at.
func TestTheInstanceRoutingNeedsTheBuildsFilesystemPhase(t *testing.T) {
	if _, err := VercelInstanceConfig([]byte(`{"version":3,"routes":[]}`), InstanceRouting{PageCSP: testCSP}); err == nil {
		t.Error("routing without a filesystem phase was generated")
	}
}

// A build that already has a hit phase gets ours first in it, not a second.
func TestTheInstanceRoutingJoinsAnExistingHitPhase(t *testing.T) {
	built := `{"version":3,"routes":[{"handle":"filesystem"},{"src":"^/(.*)$","dest":"/api/index"},{"handle":"hit"},{"src":"^/x$","headers":{"a":"b"},"continue":true}]}`
	cfg, err := VercelInstanceConfig([]byte(built), InstanceRouting{PageCSP: testCSP})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(cfg), `"handle": "hit"`); n != 1 {
		t.Errorf("%d hit phases; want 1", n)
	}
	if got := headersFor(t, cfg, "/a.html", true)["Content-Security-Policy"]; got != testCSP {
		t.Errorf("CSP in the joined hit phase = %q", got)
	}
}

// ⭐ PutCode with routing uploads the routed config.json.
func TestPutCodeUploadsTheInstanceRouting(t *testing.T) {
	ctx := context.Background()
	f := newFakeVercel(t)
	p := f.provisioner(t)
	inst, _ := p.Create(ctx, CreateRequest{Name: "alice"})
	code := testBuild(t, FormatTarGz, map[string][]byte{"config.json": []byte(realBuilderConfig)})
	code.Routing = &InstanceRouting{PageCSP: testCSP}
	if _, err := p.PutCode(ctx, inst, code); err != nil {
		t.Fatal(err)
	}
	for _, df := range f.deployed {
		if df.File == ".vercel/output/config.json" {
			if got := headersFor(t, f.uploaded[df.SHA], "/", true)["Content-Security-Policy"]; got != testCSP {
				t.Errorf("the uploaded config.json gives / the CSP %q", got)
			}
			return
		}
	}
	t.Fatal("no config.json was deployed")
}
