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
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
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

// ServeTenantPublic serves rendered public content for a tenant.
//
// URL → file mapping:
//   - /              → index.html
//   - /path/to/post  → path/to/post.html (extensionless → .html)
//   - /styles.css    → styles.css
//   - /posts/x.md    → raw markdown (for programmability)
//   - /.well-known/* → allowed (protocol requirement)
//   - /.polis/*      → blocked (private data)
//   - paths with ..  → blocked (traversal)
//
// CORS: posts/, comments/, content/, .well-known/ get
// Access-Control-Allow-Origin: *. The lazy-fetch path on the stream
// (cli-go/pkg/bundle/.../shapes/v4/stream.js) depends on this for
// cross-origin reads of rendered HTML.
//
// Source-redirect: /content/.../foo.html requests get 301'd to the
// configured mount path (e.g., /posts/.../foo.html) per the Bundle's
// SourceToMountPath rules.
//
// handle is empty in single-tenant mode (Storage adapters that ignore it).
// postProcess may be nil when the caller has no HTML rewrites to apply.
func ServeTenantPublic(
	w http.ResponseWriter, r *http.Request,
	storage Storage, handle string,
	postProcess PostProcessFunc,
) {
	urlPath := r.URL.Path

	// Map root to index.html
	if urlPath == "/" || urlPath == "" {
		urlPath = "/index.html"
	}

	// Clean the path and block traversal
	clean := filepath.Clean(strings.TrimPrefix(urlPath, "/"))
	if strings.Contains(clean, "..") {
		http.NotFound(w, r)
		return
	}

	// Block dot-paths except .well-known/
	if strings.HasPrefix(clean, ".") && !strings.HasPrefix(clean, ".well-known") {
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
		if strings.HasPrefix(clean, "content/") ||
			strings.HasPrefix(clean, ".well-known") ||
			strings.HasPrefix(clean, "posts/") ||
			strings.HasPrefix(clean, "comments/") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		}
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
			} else {
				// Exact file match — serve as JSON for .well-known, otherwise octet-stream
				data, err := storage.ReadPublicFile(handle, clean)
				if err != nil {
					http.NotFound(w, r)
					return
				}
				if strings.HasPrefix(clean, ".well-known") {
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
				}
				w.Write(data)
				return
			}
		}
		if ext == "" {
			clean = clean + ".html"
			ext = ".html"
		}
	}

	// Only allow safe extensions. `.js` is in the allow-list because
	// the v4 SHAPE installs `/stream.js` at the tenant data dir root
	// (theme.CopyStreamController copies it from the bundle into the
	// tenant root so v4 pages' <script src="/stream.js" defer> resolve
	// to a 200). Without `.js` here, the controller never loads and
	// the filter dropdowns + comment panel + pagination all silently
	// no-op. Bug present since 5.a's extraction (2026-04-27); caught
	// during step-05/5.i Phase B local testing on 2026-04-28.
	switch ext {
	case ".html", ".css", ".js", ".json", ".jsonl", ".md", ".svg", ".xml":
		// allowed
	default:
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
	if strings.HasPrefix(clean, "content/") ||
		strings.HasPrefix(clean, ".well-known") ||
		strings.HasPrefix(clean, "posts/") ||
		strings.HasPrefix(clean, "comments/") {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
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
	if ext == ".html" {
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
