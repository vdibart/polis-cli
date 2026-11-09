// Package serve provides shared tenant-static-content serving used by
// both the multi-tenant hosted service and the single-tenant non-hosted
// polis-server (under --reader / --mirror modes).
//
// The handler logic was extracted from hosted's serveTenantPublic so the
// non-hosted server can reuse the exact same path-resolution, traversal
// block, dot-path block, content-type, source-redirect, and CORS rules.
// The extraction's interface design (this file) lets each caller provide
// its own storage backend (multi-tenant disk vs single-tenant data dir)
// and post-process hook (hosted injects a nav widget for authenticated
// visitors; non-hosted has no equivalent).
package serve

import (
	"context"
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/privatepath"
	"github.com/vdibart/polis-cli/cli-go/pkg/publicfile"
)

// Storage abstracts tenant file I/O for the public-content handler. The
// handle parameter identifies the tenant in multi-tenant mode; single-
// tenant adapters ignore it and resolve against their own data dir.
type Storage interface {
	StatPublicFile(handle, relPath string) (fs.FileInfo, error)
	ReadPublicFile(handle, relPath string) ([]byte, error)
	LoadBundle(handle string) *bundle.Bundle
}

// PostProcessFunc lets the caller mutate HTML responses before they're
// written. Used by the hosted service to inject a nav widget for
// authenticated visitors. Called only when ext == ".html". Non-hosted
// callers pass nil.
type PostProcessFunc func(content []byte, r *http.Request) []byte

type personalisedKey struct{}

// Personalised marks a request whose HTML response will carry something about
// the person asking (the hosted nav for a signed-in visitor). ⛔ Such a response
// is never stored by a shared cache, nor kept by the browser: it is the page
// for one person, and a cached copy could be shown to the next.
func Personalised(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), personalisedKey{}, true))
}

// IsPersonalised reports whether Personalised marked r.
func IsPersonalised(r *http.Request) bool {
	v, _ := r.Context().Value(personalisedKey{}).(bool)
	return v
}

// rootFiles are files served at the tenant root by EXACT NAME, mapped to the
// content type each one's specification requires.
//
// The permission is a path because the requirement is a path. robots.txt is
// fixed at exactly /robots.txt by its spec, so allowing ".txt" in the
// extension switch below would grant strictly more than is being asked for —
// every .txt anywhere in a tenant's public tree, including ones they never
// meant to publish. Naming the file keeps each future addition
// (ads.txt, humans.txt, .well-known/security.txt) a deliberate decision.
//
// robots.txt was written to disk correctly and refused here from the licence
// epic until 2026-08-28: the AIPREF content-usage rule and the RSL License:
// directive — the licence's only surfaces a static self-hoster can publish —
// reached nobody, while Cloudflare's managed robots.txt filled the gap
// plausibly enough that nothing looked broken.
var rootFiles = publicfile.RootFiles

// crossOriginRootFiles are root files other origins read by name: the licence
// surfaces a browser-side reader fetches beside .well-known/polis. Named for
// the same reason rootFiles is — a root .xml is not public-by-protocol just
// because rsl.xml is.
var crossOriginRootFiles = map[string]bool{
	"robots.txt": true,
	"rsl.xml":    true,
}

// allowsCrossOrigin reports whether clean (a cleaned, slash-less path) is
// readable from any origin. ⚠️ Every return path that writes a body must ask
// it: .well-known/polis and robots.txt leave through early returns, and both
// went out without the header while did.json had it (epic 09 E4).
func allowsCrossOrigin(clean string) bool {
	return strings.HasPrefix(clean, "content/") ||
		strings.HasPrefix(clean, ".well-known") ||
		strings.HasPrefix(clean, "posts/") ||
		strings.HasPrefix(clean, "comments/") ||
		crossOriginRootFiles[clean]
}

func setCrossOrigin(w http.ResponseWriter, clean string) {
	if allowsCrossOrigin(clean) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	}
}

// The handler's two denials, as variables only so the fault-injection test in
// this package can switch them off and show the outside-in probe catching the
// exposure; nothing else assigns them.
//
// denied refuses a private name in any segment (privatepath). It does not
// see an extensionless file, and most private files have no extension (id_rsa,
// .git/HEAD, .polis/keys/id_ed25519), so extensionlessServed is the second
// layer: an existing extensionless file is served only where it says.
var (
	denied              = privatepath.Denied
	extensionlessServed = publicfile.ExtensionlessServed
)

// ServeTenantPublic serves rendered public content for a tenant.
//
// URL → file mapping:
//   - /              → index.html
//   - /path/to/post  → path/to/post.html (extensionless → .html)
//   - /styles.css    → styles.css
//   - /posts/x.md    → raw markdown (for programmability)
//   - /robots.txt   → served by exact name (see rootFiles)
//   - /.well-known/* → allowed (protocol requirement); the only place an
//     extensionless file is served
//   - a private name in ANY segment (.polis, .env, .git, .ssh, key files,
//     editor backups; see privatepath.Entries) → 404. .versions is a stated
//     exception: version history is public.
//   - paths with ..  → blocked (traversal)
//
// CORS: posts/, comments/, content/, .well-known/, and the root robots.txt
// and rsl.xml get Access-Control-Allow-Origin: * (see allowsCrossOrigin). The lazy-fetch path on the stream
// (cli-go/pkg/bundle/.../shapes/v4/stream.js) depends on this for
// cross-origin reads of rendered HTML.
//
// Source-redirect: /content/.../foo.html requests get 301'd to the
// configured mount path (e.g., /posts/.../foo.html) per the Bundle's
// SourceToMountPath rules.
//
// handle is empty in single-tenant mode (Storage adapters that ignore it).
// postProcess may be nil when the caller has no HTML rewrites to apply.

// ServedExtensions is the set of file extensions ServeTenantPublic will return
// from a tenant's own data directory.
//
// `.js` is here because the v4 SHAPE installs `/stream.js` at the tenant data
// dir root (theme.CopyStreamController copies it from the bundle so v4 pages'
// <script src="/stream.js" defer> resolve to a 200). Without it the controller
// never loads and the filter dropdowns, comment panel and pagination all
// silently no-op. Bug present since 5.a's extraction (2026-04-27); caught
// during step-05/5.i Phase B local testing on 2026-04-28.
var ServedExtensions = publicfile.ServedExtensions

// ScriptCapableExtensions are the members of ServedExtensions a browser may
// execute as script from a tenant's own origin.
//
// ⛔ THIS IS LOAD-BEARING FOR CROSS-TENANT ISOLATION, AND NOT OBVIOUSLY SO.
// Tenant content is served under `script-src 'self' https://polis.pub`
// (server.CSPTenantContent), and `'self'` is the TENANT'S OWN SUBDOMAIN. So the
// CSP stops a tenant executing script on its own pages only for as long as a
// tenant cannot get a file with one of these extensions into its data dir.
// That, in turn, is the only thing keeping the cross-tenant `polis_widget_token`
// cookie unreadable — the cookie is deliberately JS-readable and shared across
// `*.polis.pub`.
//
// Nothing in the write paths was designed as a control: snippets land as
// `.html`/`.md` because of how snippet resolution works, drafts are `.md` and
// hooks are `.sh` under `.polis/` (which this file refuses as a dot-path), and
// post bodies go through bluemonday. The guarantee is real but incidental, so
// it is asserted in static_execution_test.go rather than left to be rediscovered.
//
// ⚠️ `.svg` counts. It is served as `image/svg+xml` and an SVG can carry script.
var ScriptCapableExtensions = map[string]bool{".js": true, ".svg": true}

func ServeTenantPublic(
	w http.ResponseWriter, r *http.Request,
	storage Storage, handle string,
	postProcess PostProcessFunc,
) {
	ServeTenantPublicGated(w, r, storage, handle, postProcess, nil)
}

// GateFunc lets the caller refuse a response after the file to be served is
// known and before any of it is written. clean is the resolved, slash-less
// path; contentUsage is the Content-Usage value derived from the bytes being
// served ("" when the resource carries no terms). It returns true when it has
// written the response itself.
//
// The hosted service uses it to hold AI training crawlers to a site's own
// terms. The self-hosted server passes nil: a self-hoster's access control is
// their own web server's business.
type GateFunc func(w http.ResponseWriter, r *http.Request, clean, contentUsage string) bool

// ServeTenantPublicGated is ServeTenantPublic with an optional gate.
//
// ⚠️ robots.txt is served before the gate is ever consulted, on purpose: it is
// where a site's terms are published, and a crawler has to be able to read
// them to obey them.
func ServeTenantPublicGated(
	w http.ResponseWriter, r *http.Request,
	storage Storage, handle string,
	postProcess PostProcessFunc,
	gate GateFunc,
) {
	urlPath := r.URL.Path

	// A path of slashes alone ("//", from a crawler joining a base URL that
	// ends in "/" to a path that starts with one) is the root. Without this,
	// stripping one slash below left "/", which the storage refuses as
	// absolute and logs as path traversal: a false security alarm.
	if strings.TrimLeft(urlPath, "/") == "" {
		urlPath = "/"
	}

	// Map root to index.html
	if urlPath == "/" || urlPath == "" || urlPath == "/index.html" {
		// PQL landing redirect: a human browser (Accept: text/html) landing on
		// the bare tenant root — or the explicit /index.html — is sent to the
		// canonical PQL URL so the address bar reflects the filter (and
		// scroll-driven post URLs build on it). Programmatic / non-HTML clients
		// still get index.html unchanged. handlers_pql.go's HTML branch reads
		// index.html directly (relPath), never via "/" or "/index.html", so
		// this can't loop back into itself.
		if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/pql/all+posts+by+date", http.StatusFound)
			return
		}
		urlPath = "/index.html"
	}

	// Clean the path and block traversal
	// ⭐ path.Clean, not filepath.Clean: forward slashes on every OS, so the
	// private-name check below sees the same segments on Windows as here.
	// ⭐ TrimLeft, not TrimPrefix: "//posts/x.html" is "posts/x.html", never
	// the absolute "/posts/x.html".
	clean := path.Clean(strings.TrimLeft(urlPath, "/"))
	if strings.Contains(clean, "..") {
		http.NotFound(w, r)
		return
	}

	// Private names, in any segment (see privatepath). A plain 404, so a
	// refused path looks like an absent one.
	if denied(clean) {
		http.NotFound(w, r)
		return
	}

	// Short-circuit OPTIONS preflight before any file I/O. CORS-prefixed
	// paths get the full Allow-Origin/Methods headers; non-CORS paths get
	// just Allow. Either way the response carries no body — OPTIONS isn't
	// supposed to return the resource representation, and reading the
	// file would be wasted I/O.
	if r.Method == http.MethodOptions {
		w.Header().Set("Allow", "GET, OPTIONS")
		setCrossOrigin(w, clean)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Content source path redirect: .html requests on content/ paths get
	// 301'd to the current mount path. .md requests fall through to serve
	// raw markdown.
	if strings.HasPrefix(clean, "content/") && strings.HasSuffix(clean, ".html") {
		b := storage.LoadBundle(handle)
		if b != nil {
			if mounted := b.SourceToMountPath(clean); mounted != clean {
				http.Redirect(w, r, "/"+mounted, http.StatusMovedPermanently)
				return
			}
		}
	}

	// Mount-path .md alias → canonical content source path (301). The signed
	// markdown lives ONLY under content/pub.polis.core/<type>/; the mount dirs
	// (posts/, comments/) hold rendered .html. So ANY mount .md request 301s to
	// its canonical content/ path — unconditionally, even when a legacy
	// duplicate .md physically exists at the mount (pre-split artifact; the
	// canonical copy is the one under content/). This (a) heals comments
	// mis-registered with the discovery service at the mount URL (those DS
	// URLs are signature-bound + the (type,url) upsert key, so this is a
	// PERMANENT compatibility shim), and (b) canonicalizes onto a single source
	// of truth. Programmability is preserved — a client swapping .html→.md still
	// gets the markdown, served canonically from content/. content/... is a
	// no-op for MountToSourcePath, so the canonical .md is served as raw
	// markdown below without looping.
	if strings.HasSuffix(clean, ".md") {
		if b := storage.LoadBundle(handle); b != nil {
			if src := b.MountToSourcePath(clean); src != clean {
				http.Redirect(w, r, "/"+src, http.StatusMovedPermanently)
				return
			}
		}
	}

	// Spec-mandated root files, by exact name (see rootFiles).
	if ctype, ok := rootFiles[clean]; ok {
		data, err := storage.ReadPublicFile(handle, clean)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "no-cache")
		setCrossOrigin(w, clean)
		w.Write(data)
		return
	}

	// Determine extension and resolve file path
	ext := filepath.Ext(clean)

	if ext == "" {
		// Try exact path first (e.g. .well-known/polis), then directory index, then .html
		if info, err := storage.StatPublicFile(handle, clean); err == nil {
			if info.IsDir() {
				// Directory: try index.html inside it (e.g. /posts/ -> posts/index.html)
				indexClean := filepath.Join(clean, "index.html")
				if _, err := storage.StatPublicFile(handle, indexClean); err == nil {
					clean = indexClean
					ext = ".html"
				} else {
					http.NotFound(w, r)
					return
				}
			} else if extensionlessServed(clean) {
				// Exact file match. Only .well-known/ serves extensionless
				// files (.well-known/polis); anywhere else an extensionless
				// file is a key, a git internal or a dotfile far more often
				// than it is content, and ServedExtensions cannot see it. It
				// falls through to the .html lookup below instead.
				data, err := storage.ReadPublicFile(handle, clean)
				if err != nil {
					http.NotFound(w, r)
					return
				}
				if gate != nil && gate(w, r, clean, "") {
					return
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				setCrossOrigin(w, clean)
				w.Write(data)
				return
			}
		}
		if ext == "" {
			clean = clean + ".html"
			ext = ".html"
		}
	}

	// Only allow safe extensions — see ServedExtensions.
	if !ServedExtensions[ext] {
		http.NotFound(w, r)
		return
	}

	// Read file from tenant data directory
	data, err := storage.ReadPublicFile(handle, clean)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Set content type
	switch ext {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case ".json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	case ".jsonl":
		w.Header().Set("Content-Type", "application/jsonl; charset=utf-8")
	case ".md":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	case ".xml":
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	}

	// Allow cross-origin reads for public content + well-known files.
	// Originally this covered the widget's following.json fetch; the v4
	// stream's lazy-fetch path (cli-go/pkg/bundle/.../shapes/v4/stream.js)
	// extends the requirement to /posts/ and /comments/ (rendered HTML
	// mounts that cross-origin readers fetch to extract focus body via
	// the <main class="focus-content"> protocol marker per D-PROXY).
	setCrossOrigin(w, clean)

	// Licence headers, derived from the bytes being served so they can never
	// disagree with the document they are attached to.
	setLicenseHeaders(w, ext, data)

	// The gate reads the terms off the header just derived, so its decision
	// and the header on this response come from the same bytes.
	if gate != nil && gate(w, r, clean, w.Header().Get("Content-Usage")) {
		return
	}

	// Cache-Control:
	//   - Rendered .html: short browser TTL (5 min) so the v4 stream's
	//     lazy-fetch flow doesn't re-revalidate every per-post page on
	//     every scroll session. Per-post HTML is static between
	//     publishes; a republish takes up to 5 min to propagate to
	//     clients holding cached copies — acceptable for the alpha.
	//     Without this, the ~12KB gzipped lazy-fetch round-trip fires
	//     every time the user scrolls a post into view, even within
	//     the same session.
	//   - Everything else (.json, .jsonl, .md, .css, .js, etc.): keeps
	//     no-cache. Index data + the controller bundle move with the
	//     tenant's content; no-cache + the version query param on
	//     /stream.js?v=X.Y.Z gives ETag-style revalidation without
	//     stale-bundle risk.
	if ext == ".html" && IsPersonalised(r) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Add("Vary", "Cookie")
	} else if ext == ".html" {
		w.Header().Set("Cache-Control", "public, max-age=300")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	// Apply post-process hook for HTML responses (hosted nav-widget injection).
	if ext == ".html" && postProcess != nil {
		data = postProcess(data, r)
	}

	w.Write(data)
}
