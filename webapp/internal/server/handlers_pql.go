package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/pql"
)

// handleStreamPQL implements GET /pql/<sentence> — the unified, PQL-native
// data endpoint. It parses the PQL sentence (tenant-relative: an omitted
// from/about clause defaults to the serving tenant), then:
//
//   - Accept: application/json (or ?format=json) → JSON envelope (below).
//   - otherwise → HTML infinity-stream shell (Phase 5; placeholder 406 today).
//
// The JSON path translates the PQL Filter into the existing stream/items query
// params and delegates to the proven handleStreamItems pipeline, then re-wraps
// its {items,next_cursor} body in the standardized, versioned envelope. This
// reuses 100% of the validation/dispatch/pagination logic — the endpoint
// "speaks PQL" without duplicating the engine.
//
// SCOPE-RESOLUTION BOUNDARY: first-person scopes (me/my-*) are owner-private.
// The owner-auth gate lives at the routing layer (hosted multiplexer's
// pqlRequiresOwnerAuth; localhost owner-trust by construction) — the same
// separation handleStreamItems relies on. The `about` relation and
// fully-qualified event types are DS-only on this tenant surface and are
// rejected here.
func (s *Server) handleStreamPQL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenant := extractDomainFromURL(s.GetBaseURL())
	defaultScope := ""
	if tenant != "" {
		defaultScope = "@" + tenant
	}

	filter, err := pql.ParseURL(r.URL.Path, defaultScope)
	if err != nil || filter == nil {
		// Malformed sentence. JSON callers get a 400; HTML callers will (in
		// Phase 5) redirect to the default filter — for now, 400 as well.
		writeStreamError(w, http.StatusBadRequest, "invalid PQL sentence")
		return
	}

	if !requestWantsJSON(r) {
		// HTML branch: serve the SSR infinity-stream shell, seeded with this
		// filter so stream.js hydrates into the right view. The rendered
		// index.html shell IS the default posts view; for any other filter we
		// inject window.__POLIS_INITIAL_PQL and stream.js re-filters on hydrate.
		// Reads index.html directly (not via the "/" path) so the "/" →
		// /pql/all+posts+by+date redirect can't loop back here.
		shell, rerr := NewDataDirStorage(s.DataDir).ReadPublicFile("", "index.html")
		if rerr != nil {
			http.NotFound(w, r)
			return
		}
		isDefaultView := filter.Type == "posts" && filter.Relation == "from" &&
			(filter.Modifier == "by-date" || filter.Modifier == "") &&
			defaultScope != "" && filter.Scope == defaultScope
		if !isDefaultView {
			shell = injectInitialPQL(shell, filter)
		}
		// The shell references root-relative assets (base.css, styles.css) with
		// bare relative hrefs. Served one level deep at /pql/<sentence>, the
		// browser would resolve them against /pql/ (e.g. /pql/base.css) — which
		// this same handler answers with a JSON error (wrong MIME). A <base
		// href="/"> pins relative resolution to the site root. Mirrors
		// serveTenantSPA's <base href="/_/"> for the admin SPA.
		shell = injectBaseHref(shell)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(shell)
		return
	}

	// This tenant surface serves rendered content views (alias types) over the
	// local feed cache. Fully-qualified event types and the `about` (involved)
	// relation are served by the discovery service /pql/ endpoint.
	if isFQEventType(filter.Type) {
		writeStreamError(w, http.StatusBadRequest,
			"fully-qualified event types are served by the discovery service /pql/ endpoint, not this tenant surface")
		return
	}
	if filter.Relation == "about" {
		writeStreamError(w, http.StatusBadRequest,
			"the 'about' relation is served by the discovery service /pql/ endpoint; not available on this tenant surface")
		return
	}

	// Translate the PQL Filter into the existing stream/items query params and
	// delegate to handleStreamItems via a capturing writer.
	params := pqlToStreamParams(filter)
	copyTransportParams(r.URL.Query(), params)

	r2 := r.Clone(r.Context())
	r2.URL = &url.URL{Path: "/api/v1/stream/items", RawQuery: params.Encode()}
	r2.Method = http.MethodGet

	cap := &captureWriter{header: http.Header{}}
	s.handleStreamItems(cap, r2)

	if cap.status != 0 && cap.status != http.StatusOK {
		// Pass the underlying error through unchanged (preserves the
		// {"error": ...} contract + status code).
		for k, v := range cap.header {
			w.Header()[k] = v
		}
		w.WriteHeader(cap.status)
		_, _ = w.Write(cap.buf.Bytes())
		return
	}

	// Decode the inner {items, next_cursor} and re-wrap in the versioned envelope.
	var inner struct {
		Items      json.RawMessage `json:"items"`
		NextCursor string          `json:"next_cursor"`
	}
	_ = json.Unmarshal(cap.buf.Bytes(), &inner)
	if len(inner.Items) == 0 {
		inner.Items = json.RawMessage("[]")
	}

	sentence, _ := pql.Compose(filter)
	envelope := map[string]interface{}{
		"version": "pql.v1",
		"query":   sentence,
		"url":     pql.ComposeURL(filter, "", defaultScope),
		"parsed": map[string]string{
			"qualifier": filter.Qualifier,
			"type":      filter.Type,
			"relation":  filter.Relation,
			"scope":     filter.Scope,
			"modifier":  filter.Modifier,
		},
		"tenant": tenant,
		"items":  inner.Items,
		"pagination": map[string]interface{}{
			"next_cursor": inner.NextCursor,
			"has_more":    inner.NextCursor != "",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(envelope)
}

// requestWantsJSON reports whether the caller wants the JSON envelope
// (Accept: application/json, or the ?format=json override for easy testing).
func requestWantsJSON(r *http.Request) bool {
	if strings.EqualFold(r.URL.Query().Get("format"), "json") {
		return true
	}
	return strings.Contains(r.Header.Get("Accept"), "application/json")
}

// aliasInternalTypes is the set of friendly-alias internal type values the
// tenant surface renders. Anything else parsed in the type slot is a
// fully-qualified event type (DS-only here).
var aliasInternalTypes = map[string]bool{
	"posts": true, "comments": true, "profiles": true,
	"dms": true, "drafts": true, "activity": true,
}

func isFQEventType(t string) bool { return !aliasInternalTypes[t] }

// injectInitialPQL inserts a window.__POLIS_INITIAL_PQL seed into the HTML
// shell so stream.js re-filters on hydrate. Only the structured filter is
// emitted (internal values); the modifier is omitted when empty so stream.js's
// type-change default (by-date) stands.
func injectInitialPQL(html []byte, f *pql.Filter) []byte {
	seed := map[string]string{
		"qualifier": f.Qualifier,
		"type":      f.Type,
		"scope":     f.Scope,
	}
	if f.Modifier != "" {
		seed["modifier"] = f.Modifier
	}
	b, err := json.Marshal(seed)
	if err != nil {
		return html
	}
	// Defang any "<" so a handle/value can't break out of the <script>.
	safe := strings.ReplaceAll(string(b), "<", "\\u003c")
	tag := "<script>window.__POLIS_INITIAL_PQL=" + safe + ";</script>"
	s := string(html)
	if i := strings.LastIndex(s, "</head>"); i >= 0 {
		return []byte(s[:i] + tag + s[i:])
	}
	return append([]byte(tag), html...)
}

// injectBaseHref inserts <base href="/"> immediately after <head> so the
// shell's root-relative assets resolve against the site root even when the
// shell is served one level deep at /pql/<sentence>. No-op if <head> is
// absent. Inserted right after <head> so it precedes the first <link>.
func injectBaseHref(html []byte) []byte {
	const head = "<head>"
	s := string(html)
	if i := strings.Index(s, head); i >= 0 {
		return []byte(s[:i+len(head)] + `<base href="/">` + s[i+len(head):])
	}
	return html
}

// pqlToStreamParams maps a (from-relation) PQL Filter onto the legacy
// stream/items query params. Modifier maps to either sort= or with=.
func pqlToStreamParams(f *pql.Filter) url.Values {
	v := url.Values{}
	v.Set("qualifier", f.Qualifier)
	v.Set("type", f.Type) // handleStreamItems accepts the same internal tokens (incl. dms)
	v.Set("scope", f.Scope)
	switch f.Modifier {
	case "by-date", "by-name", "by-activity":
		v.Set("sort", f.Modifier)
	case "with-comments":
		v.Set("with", "comments")
	case "to-bless":
		v.Set("with", "pending-blessing")
	}
	return v
}

// copyTransportParams threads transport-only params (not part of the PQL
// grammar) from the inbound request onto the delegated query.
func copyTransportParams(src, dst url.Values) {
	for _, k := range []string{"cursor", "time", "surface", "limit", "order", "search", "before_url", "after_url"} {
		if val := src.Get(k); val != "" {
			dst.Set(k, val)
		}
	}
}

// captureWriter buffers a delegated handler's response so handleStreamPQL can
// re-wrap a 200 body in the PQL envelope (or pass an error through verbatim).
type captureWriter struct {
	header http.Header
	status int
	buf    bytes.Buffer
}

func (c *captureWriter) Header() http.Header {
	if c.header == nil {
		c.header = http.Header{}
	}
	return c.header
}

func (c *captureWriter) WriteHeader(status int) { c.status = status }

func (c *captureWriter) Write(b []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.buf.Write(b)
}
