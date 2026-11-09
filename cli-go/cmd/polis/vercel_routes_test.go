package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/privatepath"
	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// routeSet is one routing to hold to the rules below: the template, and the
// routing generated from it for an instance.
type routeSet struct {
	name          string
	headerSources []string // as the set states them (template forms, or route expressions)
	rewrites      int      // anything that sends a request somewhere other than the build's own routes
}

// builderConfig is the routing `vercel build` writes for the function alone:
// a filesystem phase, then every path to the function.
const builderConfig = `{"version":3,"routes":[{"handle":"filesystem"},{"src":"^(?:/(.*))$","dest":"/api/index","check":true}]}`

// routeSets returns the template and the routing generated from it, so every
// rule here holds for both: the template is what is reviewed, the generated
// routing is what is deployed.
func routeSets(t *testing.T) []routeSet {
	t.Helper()
	var tpl map[string]json.RawMessage
	if err := json.Unmarshal(provision.VercelRoutesTemplate(), &tpl); err != nil {
		t.Fatalf("the routes template does not parse: %v", err)
	}
	template := routeSet{name: "template"}
	if _, ok := tpl["rewrites"]; ok {
		template.rewrites++
	}
	for _, group := range []string{"headers", "static"} {
		var rules []struct {
			Source string `json:"source"`
		}
		if err := json.Unmarshal(tpl[group], &rules); err != nil {
			t.Fatalf("the template's %s do not parse: %v", group, err)
		}
		for _, r := range rules {
			template.headerSources = append(template.headerSources, r.Source)
		}
	}
	if len(template.headerSources) == 0 {
		t.Fatal("the routes template declares no header rules")
	}

	raw, err := provision.VercelInstanceConfig([]byte(builderConfig), provision.InstanceRouting{PageCSP: "default-src 'self'"})
	if err != nil {
		t.Fatalf("generating an instance's routing: %v", err)
	}
	var gen struct {
		Routes []map[string]any `json:"routes"`
	}
	if err := json.Unmarshal(raw, &gen); err != nil {
		t.Fatalf("the generated routing does not parse: %v", err)
	}
	generated := routeSet{name: "generated"}
	for _, r := range gen.Routes {
		src, _ := r["src"].(string)
		if _, ok := r["headers"]; ok {
			generated.headerSources = append(generated.headerSources, regexLiteral(src))
		}
		if dest, _ := r["dest"].(string); dest != "" && dest != "/api/index" {
			generated.rewrites++
		}
	}
	return []routeSet{template, generated}
}

// regexLiteral is the path a generated route's expression names before any
// group or anchor.
func regexLiteral(src string) string {
	lit := strings.TrimPrefix(src, "^")
	if i := strings.IndexAny(lit, "($.*"); i >= 0 && lit[i] != '.' {
		lit = lit[:i]
	}
	if i := strings.Index(lit, ".*"); i >= 0 {
		lit = lit[:i]
	}
	lit = strings.TrimSuffix(lit, "$")
	return strings.ReplaceAll(lit, `\`, "")
}

// ⭐⭐ PUBLISH = DEPLOY: the routing carries HEADERS ONLY. The site's public
// tree is the deployment's static files, so nothing is rewritten anywhere; a
// rewrite reappearing would mean something is being served from somewhere
// other than the deployment (the public Blob store this replaced served HTML
// as a download).
func TestVercelRoutesRewriteNothing(t *testing.T) {
	for _, set := range routeSets(t) {
		if set.rewrites != 0 {
			t.Errorf("%s: %d rewrite(s); the routing must carry headers only", set.name, set.rewrites)
		}
	}
}

// ⛔⛔ .well-known MUST BE REACHABLE AT THE DOMAIN, OR REGISTRATION FAILS.
//
// The domain is the identity: signatures, .well-known/polis and every DS row
// are keyed on it. ⚠️ A site whose .well-known/polis 404s cannot register,
// cannot be verified and cannot be followed. It reaches the domain as a static
// file, so it must be a public key, and its header rules must exist.
func TestVercelRoutesServeWellKnownAtTheDomain(t *testing.T) {
	if !provision.IsPublicKey(".well-known/polis") {
		t.Fatal(".well-known/polis is not a public key: it would never be deployed")
	}
	for _, set := range routeSets(t) {
		found := false
		for _, h := range set.headerSources {
			if strings.HasPrefix(h, "/.well-known") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no header rule covers /.well-known/ (its CORS and content type)", set.name)
		}
	}
}

// ⛔⛔ NOTHING PRIVATE MAY BE SERVED, AND ON A BLOB BACKEND THERE ARE NO FILE
// MODES TO ALSO CARRY IT.
//
// On POSIX, ".polis must not be world-readable" is carried by mode 0700 AND by
// the handler refusing private names. On Vercel a static file is served to
// anyone, so the selection of what becomes one (provision.IsPublicKey, the
// declared privatepath list plus .env) is the whole of the property on the
// site's domain, and no header rule may even name a private path. The private
// store keeps .polis off the store's own URL.
func TestVercelRoutesNeverServeAPrivateName(t *testing.T) {
	for _, set := range routeSets(t) {
		for _, h := range set.headerSources {
			if routeNamesPrivate(h) {
				t.Errorf("%s: a header rule names a private path (%s); it must not be addressable", set.name, h)
			}
		}
	}
	// ⚠️ Each denial's own probe paths, not ProbeTargets' URL-encoded spellings:
	// those are request forms, and a store key is never URL-decoded.
	n := 0
	for _, e := range privatepath.Entries {
		for _, p := range e.Probe {
			n++
			if provision.IsPublicKey(strings.TrimPrefix(p, "/")) {
				t.Errorf("%s would be deployed as a static file", p)
			}
		}
	}
	if n < 5 {
		t.Fatalf("only %d probe paths; the list is not being read", n)
	}
}

// The checker itself catches what it is for, so the test above cannot pass by
// checking nothing.
func TestRouteNamesPrivateCatchesTheList(t *testing.T) {
	for _, bad := range []string{"/.polis/:path*", "/.env", "/content/.git/:path*",
		"/site/id_rsa", "/.ssh/:path*", "/.GIT/config"} {
		if !routeNamesPrivate(bad) {
			t.Errorf("routeNamesPrivate(%q) = false", bad)
		}
	}
	for _, ok := range []string{"/.well-known/:path*", "/content/:path*", "/", "/robots.txt", "/posts/(.*)"} {
		if routeNamesPrivate(ok) {
			t.Errorf("routeNamesPrivate(%q) = true", ok)
		}
	}
}

// routeNamesPrivate reports whether the literal part of a path pattern
// (everything before its first parameter or group) names a private path.
func routeNamesPrivate(pattern string) bool {
	lit := pattern
	if i := strings.IndexAny(lit, ":(*"); i >= 0 {
		lit = lit[:i]
	}
	return privatepath.Denied(lit)
}

// ⭐ Every served surface a reader or crawler fetches must be deployable as a
// static file, or it 404s on Vercel while working on a POSIX one — the worst
// kind of difference, because it only shows up in production.
func TestVercelRoutesCoverTheServedSurface(t *testing.T) {
	for _, must := range []string{
		".well-known/polis", ".well-known/did.json", "robots.txt", "rsl.xml",
		"posts/2026/hello.html", "tags/x.html", "license/index.html", "content/pub.polis.core/bundle.json",
		"styles.css", "index.html", "public.jsonl",
		"policies/rules.jsonl", "favicon.svg", "site/snippets/about.md",
	} {
		if !provision.IsPublicKey(must) {
			t.Errorf("%s is not a public key: it would never be deployed, and would 404 on Vercel while "+
				"working on a POSIX site", must)
		}
	}
}

// ⛔⛔ THE PUBLISH PATH NEVER DEPLOYS PER WRITE (publish = deploy, rule 1).
//
// ✏️ This used to say "publishing must trigger no deployment", the 2026-09-27
// decision that Blob held content and deployments held code. Since 2026-10-13
// a remote instance on Vercel serves its public tree as deployment static
// files, so a publish DOES reach readers through a deployment. ⛔ But never one
// per write: a theme change re-renders every page, and one deployment per page
// would hit Vercel's 60-per-5-minutes limit at once. Writes land in the store;
// ONE deploy at the end of the request carries them all.
//
// ⭐ It holds by CONSTRUCTION: the content packages write through the storage
// seam and have no client for a platform API. This test keeps it that way,
// because the way it breaks is someone adding a "refresh the site" call to a
// writer that looks helpful.
func TestThePublishPathNeverDeploysPerWrite(t *testing.T) {
	// ⚠️ Vercel's deployment endpoints, plus the CLI that wraps them.
	forbidden := []string{
		"api.vercel.com",
		"/v13/deployments",
		"/v6/deployments",
		"vercel --prod",
		"vercel deploy",
	}
	roots := []string{
		filepath.Join(repoRoot, "cli-go", "pkg", "publish"),
		filepath.Join(repoRoot, "cli-go", "pkg", "comment"),
		filepath.Join(repoRoot, "cli-go", "pkg", "render"),
		filepath.Join(repoRoot, "cli-go", "pkg", "tag"),
		filepath.Join(repoRoot, "cli-go", "pkg", "metadata"),
	}
	scanned := 0
	for _, root := range roots {
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") {
				return err
			}
			if strings.HasSuffix(p, "_test.go") {
				return nil
			}
			scanned++
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			for _, f := range forbidden {
				if strings.Contains(string(b), f) {
					rel, _ := filepath.Rel(repoRoot, p)
					t.Errorf("%s mentions %q — the content packages must not deploy. Writes "+
						"land in the store, and one deploy at the end of the request carries "+
						"them all; a writer that deploys would make one deployment per page.", rel, f)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	// ⛔ Non-vacuity: a scan that reads nothing passes.
	if scanned < 20 {
		t.Fatalf("scanned only %d files across the publish path; the roots are wrong", scanned)
	}
	t.Logf("%d files scanned across the publish path, no deployment trigger", scanned)
}

// ⭐ The identity key's two halves reach the world differently: the private
// key never, the public half only as published in .well-known/polis.
//
// ⚠️ This covers the SITE'S DOMAIN. The store's own URL is covered by keeping
// .polis in a private store.
func TestTheIdentityKeyIsNeverDeployedAndItsPublicHalfIs(t *testing.T) {
	for _, private := range []string{site.PrivateKeyKey, site.PublicKeyKey, ".polis/keys/x"} {
		if provision.IsPublicKey(private) {
			t.Errorf("%s is a public key: the private key tree would be deployed", private)
		}
	}
	if !provision.IsPublicKey(site.WellKnownKey) {
		t.Error(".well-known/polis is not a public key: the public half of the identity key would be unreachable")
	}
}
