package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/vdibart/polis-cli/cli-go/pkg/connection"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/pql"
	"github.com/vdibart/polis-cli/cli-go/pkg/seen"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// QueryPath is the API's one read of content: a PQL sentence.
const QueryPath = "/v1/query"

// QueryParam carries the sentence: /v1/query?pql=<sentence>. Named for what it
// holds, so q stays free for a search parameter.
const QueryParam = "pql"

// Query is how the host serves PQL to the API: the same handler that serves
// /pql/<sentence>, never a second implementation, and the host's own rule for
// which sentences are the owner's alone.
type Query struct {
	// Handler serves a /pql/<sentence> request.
	Handler http.Handler
	// Scope is the scope a /pql/<sentence> request needs: "" for public
	// content, apidesc.ScopeRead for the owner's own private content,
	// apidesc.ScopeFull for messages (the owner's alone).
	Scope func(*http.Request) string
}

// SearchRule is what the search parameter matches: posts and comments by the
// site's PQL handler (webapp/internal/server/handlers_stream.go, search=),
// profiles by the discovery service's site directory (domain or author name).
const SearchRule = "Add search=<words> to narrow it: posts and comments whose title or excerpt holds the " +
	"words (case-insensitive; never a post's body), among what the site already holds; profiles whose " +
	"domain or name holds them (for all polis, every site the discovery service knows)."

// queryOperation describes the query operation. It is declared here, beside
// its route, because it is the host's PQL handler rather than an engine
// action.
var queryOperation = apidesc.Operation{
	Type: "pub.polis.query", Action: "pql", Title: "Find posts, comments and people", Method: "GET", Path: QueryPath,
	Summary: "Read content with a PQL sentence",
	Description: "Asks for posts, comments, activity, profiles or messages with one sentence in polis's " +
		"query language (PQL), passed as pql, e.g. \"all posts from my network by date\", \"all comments " +
		"from me\", \"all posts from alice.polis.pub\". " + pql.Vocabulary() + " " + SearchRule + " " +
		"Answers the same JSON as /pql/<sentence>; a sentence that does not parse is answered 400 with the " +
		"parser's reason. A " +
		"sentence about public content needs no credential. One about the owner's own private content " +
		"(first-person scopes such as me and my network, drafts) needs a token with the read scope, and " +
		"messages need the owner's own token. Add counts=posts,comments (any PQL types) for how many items the same sentence finds of " +
		"each type, the numbers a filter shows; a public sentence cannot count messages or drafts. Add " +
		"time=1h, 24h, 2d, 7d or 30d for how far back the network's posts and comments go; without it, and " +
		"for all, the widest window, 30 days (the owner's own content is not cut by it). Add " +
		"new_for=gather (or write, message) to mark each item new or not against what the owner has seen " +
		"there, and only_new=1 to keep only the new ones; it needs the read scope and clears nothing. " +
		"Settings and site facts are not PQL; they have their own operations.",
	Auth: apidesc.AuthPublicOrOwner, Scope: apidesc.ScopeRead,
	Output: apidesc.Out(map[string]any{
		"version": apidesc.String("pql.v1."), "query": apidesc.String("The sentence as read."), "url": apidesc.String("Its /pql/ address."),
		"parsed":        apidesc.Obj("The sentence's parts: qualifier, type, relation, scope, modifier."),
		"tenant":        apidesc.String("The site asked."),
		"items":         apidesc.Arr("The matching items, as the stream shows them (with new when new_for is given).", nil),
		"pagination":    apidesc.Obj("next_cursor and has_more."),
		"counts":        apidesc.Obj("With counts=: how many of each type, type → number."),
		"counts_capped": apidesc.Arr("The counts that are lower bounds.", apidesc.String("A type.")),
		"new_for":       apidesc.String("With new_for=: the section new is measured against."),
	}),
	QueryParams: map[string]string{
		"pql":      "The PQL sentence, e.g. \"all posts from my network by date\".",
		"counts":   "Optional: PQL types to count with the same sentence, comma-separated (posts,comments).",
		"new_for":  "Optional: gather, write or message; marks each item new or not against what the owner has seen there.",
		"only_new": "Optional: 1 keeps only the new items (with new_for).",
		"time":     "Optional: 1h, 24h, 2d, 7d, 30d or all; how far back the network's items go. Default and all: 30 days.",
		"search":   "Optional: words the items' title or excerpt must hold.",
		"limit":    "Optional: how many items per page.",
		"cursor":   "Optional: the next_cursor of the previous page.",
	},
	RequiredQuery: []string{"pql"},
}

// QueryOperation is the query operation's description, for tests and for
// whatever describes this API.
func QueryOperation() apidesc.Operation { return queryOperation }

// handleQuery serves GET /v1/query?pql=<sentence> by handing the host's PQL
// handler the /pql/<sentence> request it would have got, asking for JSON.
func (h *handlers) handleQuery(w http.ResponseWriter, r *http.Request, site storage.Site) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	sentence := strings.TrimSpace(r.URL.Query().Get(QueryParam))
	if sentence == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", QueryParam+" is required: a PQL sentence")
		return
	}
	// The rest of the query string passes through as PQL's transport
	// parameters (limit, cursor, search, …).
	rest := r.URL.Query()
	rest.Del(QueryParam)
	inner := r.Clone(r.Context())
	inner.URL = &url.URL{Path: "/pql/" + strings.ReplaceAll(sentence, " ", "+"), RawQuery: rest.Encode()}
	inner.Header.Set("Accept", "application/json")

	// ⭐ Scopes are not ranked: the request needs every scope that applies,
	// the sentence's own, each counted type's, and read for new_for.
	needs := map[string]bool{}
	add := func(scope string) {
		if scope != "" {
			needs[scope] = true
		}
	}
	add(h.host.Query.Scope(inner))
	// ⛔ Counts never widen what the token may see: each counted type is a
	// sentence of its own.
	if raw := rest.Get("counts"); raw != "" {
		if f, err := pql.ParseURL(inner.URL.Path, "@__tenant__"); err == nil && f != nil {
			for _, word := range strings.Split(raw, ",") {
				internal, ok := pql.InternalType(strings.TrimSpace(word))
				if !ok {
					continue // the PQL handler refuses it
				}
				sibling := *f
				sibling.Type = internal
				probe := inner.Clone(inner.Context())
				probe.URL = &url.URL{Path: pql.ComposeURL(&sibling, "", "")}
				add(h.host.Query.Scope(probe))
			}
		}
	}
	// new_for=<section>: mark each item new or not against the owner's seen
	// time for that section, and with only_new=1 keep only the new ones. A
	// seen time is the owner's own state, so it needs at least read.
	newFor := rest.Get("new_for")
	if newFor != "" {
		add(apidesc.ScopeRead)
	}
	// The owner's full token covers everything, so when it's needed it is
	// the only check; otherwise each scope in turn.
	if needs[apidesc.ScopeFull] {
		needs = map[string]bool{apidesc.ScopeFull: true}
	}
	names := make([]string, 0, len(needs))
	for n := range needs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !h.requireScope(w, r, site, n) {
			return
		}
	}
	// ⭐ A token that may read the owner's own private content is the owner
	// reading: say so in the request's context, which no caller can set, so
	// the site may show the owner what it shows no one else (unblessed
	// comments on its own posts).
	if h.tokenMayRead(r, site) {
		inner = inner.WithContext(WithOwnerRead(inner.Context()))
	}
	if newFor == "" {
		h.host.Query.Handler.ServeHTTP(w, inner)
		return
	}
	h.serveWithNew(w, inner, site, newFor, rest.Get("only_new") == "1")
}

// serveWithNew runs the query and marks each item new when it is later than
// the section's seen time (every item is new in a section never marked).
//
// ⭐ Read only: marking a section seen is seen.mark's alone.
func (h *handlers) serveWithNew(w http.ResponseWriter, inner *http.Request, site storage.Site, section string, onlyNew bool) {
	known := false
	for _, s := range seen.Sections {
		known = known || s == section
	}
	if !known {
		writeError(w, http.StatusBadRequest, "invalid_request", "new_for must be one of "+strings.Join(seen.Sections, ", "))
		return
	}
	times, err := seen.Get(site)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not read the seen times")
		return
	}
	since, marked := times[section]

	rec := httptest.NewRecorder()
	h.host.Query.Handler.ServeHTTP(rec, inner)
	if rec.Code != http.StatusOK {
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	var env map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not read the query's answer")
		return
	}
	items, _ := env["items"].([]any)
	kept := make([]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		isNew := !marked
		if marked {
			if t, err := time.Parse(time.RFC3339, fmt.Sprint(item["published"])); err == nil {
				isNew = t.After(since)
			}
		}
		item["new"] = isNew
		if onlyNew && !isNew {
			continue
		}
		kept = append(kept, item)
	}
	env["items"] = kept
	env["new_for"] = section
	writeJSON(w, http.StatusOK, env)
}

type ownerReadKey struct{}

// WithOwnerRead marks a request as the owner reading their own content.
func WithOwnerRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownerReadKey{}, true)
}

// IsOwnerRead reports whether the API marked a request as the owner reading
// (a token that may read the owner's private content). ⛔ Only the API sets it,
// in the request's context, never from anything the caller sent.
func IsOwnerRead(ctx context.Context) bool {
	v, _ := ctx.Value(ownerReadKey{}).(bool)
	return v
}

// tokenMayRead reports, without answering anything, whether the request
// carries a valid token that may act in the read scope.
func (h *handlers) tokenMayRead(r *http.Request, site storage.Site) bool {
	if h.ownerSession(r) {
		return true
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	key, ok := h.lookupToken(site, r, strings.TrimPrefix(auth, "Bearer "))
	if !ok || !key.Allows(apidesc.ScopeRead) {
		return false
	}
	// A scoped token while api-tokens is off sees what anyone sees.
	if !key.Unscoped() && h.switchedOff(SwitchAPITokens) {
		return false
	}
	// ⛔ A personal token reads only while its grant is live and holds read.
	if key.PersonalToken() {
		live, err := connection.ResolveIn(r.Context(), site, key.Grant)
		if err != nil || live == nil {
			return false
		}
		for _, s := range live.Scopes() {
			if apidesc.SameScope(s, apidesc.ScopeRead) {
				return true
			}
		}
		return false
	}
	return true
}
