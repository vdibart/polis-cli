package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/blessing"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/notification"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/pql"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
	"github.com/vdibart/polis-cli/webapp/internal/api"
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
// SCOPE-RESOLUTION BOUNDARY: first-person scopes (me/my-*) and the drafts and
// dms types are owner-private. This handler does NOT check that: the owner
// gate does (ownerGateMiddleware, through PQLRequiresOwner, which hosted calls
// too), in every mode, and refuses another site's page even on loopback. A
// route that reaches this handler without passing that gate serves drafts and
// decrypted DMs to anyone. The `about` relation and
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
		// Malformed sentence: a 400 carrying the parser's own reason, so a
		// caller (an agent, most often) can correct it in one retry.
		writeStreamError(w, http.StatusBadRequest, pql.Explain(pql.SentenceOfURL(r.URL.Path), err))
		return
	}

	if !requestWantsJSON(r) {
		// HTML branch: serve the SSR infinity-stream shell, seeded with this
		// filter so stream.js hydrates into the right view. The rendered
		// index.html shell IS the default posts view; for any other filter we
		// put the filter in the page as data and stream.js re-filters on hydrate.
		// Reads index.html directly, not through the "/" path's handler.
		shell, rerr := NewDataDirStorage(s.Site()).ReadPublicFile("", "index.html")
		if rerr != nil {
			http.NotFound(w, r)
			return
		}
		isDefaultView := filter.Type == "posts" && filter.Relation == "from" &&
			(filter.Modifier == "by-date" || filter.Modifier == "") &&
			defaultScope != "" && filter.Scope == defaultScope
		if !isDefaultView {
			shell = injectInitialPQL(shell, filter)
			shell = fillViewNeedsScript(shell)
		}
		// The shell references root-relative assets (base.css, styles.css) with
		// bare relative hrefs. Served one level deep at /pql/<sentence>, the
		// browser would resolve them against /pql/ (e.g. /pql/base.css) — which
		// this same handler answers with a JSON error (wrong MIME). A <base
		// href="/"> pins relative resolution to the site root. Mirrors
		// serveTenantSPA's <base href="/_/"> for the admin SPA.
		shell = injectBaseHref(shell)
		// A view's address is navigation, not content (v5 spec 4.2, SEO rule
		// 6; every shape): not indexed, but its links are followed to the item
		// pages, which are what search engines should index.
		shell = injectNoindex(shell)
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
	// ⭐ "about me" is the owner's own activity (followed you, blessed your
	// comment, commented on your post), from the site's notifications. Any
	// other "about" is served by the discovery service's /pql/.
	if filter.Relation == "about" && !aboutMe(filter) {
		writeStreamError(w, http.StatusBadRequest,
			"the 'about' relation is served by the discovery service /pql/ endpoint, except 'all activity about me'")
		return
	}

	page, status, errBody, errHeader := s.runPQL(r, filter, r.URL.Query())
	if status != http.StatusOK {
		// Pass the underlying error through unchanged (preserves the
		// {"error": ...} contract + status code).
		for k, v := range errHeader {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = w.Write(errBody) //nolint:gosec // G705: the body is the JSON error contract, written with its JSON content type
		return
	}

	// ⭐ counts=<type>[,<type>…]: how many items the same sentence finds for
	// each named type, so a section's filters can show their numbers. The
	// sentence's other words (qualifier, relation, scope, modifier) stay, so
	// the counts react to the filter in force.
	var counts map[string]int
	var capped []string
	if raw := strings.TrimSpace(r.URL.Query().Get("counts")); raw != "" {
		var cerr error
		counts, capped, cerr = s.countPQL(r, filter, raw)
		if cerr != nil {
			writeStreamError(w, http.StatusBadRequest, cerr.Error())
			return
		}
	}
	inner := page

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
	if counts != nil {
		envelope["counts"] = counts
		if len(capped) > 0 {
			envelope["counts_capped"] = capped
		}
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

// injectInitialPQL puts the view's filter into the HTML shell as a JSON data
// block, <script type="application/json" id="polis-initial-pql">, which
// stream.js reads on hydrate. ⛔ Data, never an inline script: tenant pages
// are served with script-src 'self', so an inline script never runs and the
// view's address showed the default posts (found live 2026-11-16). A data
// block is not executed, so the policy does not apply to it. Only the structured filter is
// emitted (internal values); the modifier is omitted when empty so stream.js's
// type-change default (by-date) stands. ⛔ The relation is part of the
// sentence: without it "all comments on <post>" hydrates as "… from <post>",
// a different sentence the server refuses.
func injectInitialPQL(html []byte, f *pql.Filter) []byte {
	seed := map[string]string{
		"qualifier": f.Qualifier,
		"type":      f.Type,
		"relation":  f.Relation,
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
	tag := `<script type="application/json" id="polis-initial-pql">` + safe + "</script>"
	s := string(html)
	if i := strings.LastIndex(s, "</head>"); i >= 0 {
		return []byte(s[:i] + tag + s[i:])
	}
	return append([]byte(tag), html...)
}

// viewNeedsScriptMark is where a shell that wants it says so: a kept comment
// (render keeps <!--! … --> comments) that the v5 shape puts in its index.html.
// A shell without it (v4's) is served as it always was.
const viewNeedsScriptMark = "<!--!polis:view-needs-script-->"

// viewNeedsScriptNote is what a reader without script sees on a view's page:
// the shell is the default view, so without script it shows the posts, and it
// says so rather than passing them off as the view asked for.
const viewNeedsScriptNote = `<noscript><p class="needs-script">This view is drawn by the page's script, which is off, so the posts are shown instead. Every post and comment is also on its own page.</p></noscript>`

// fillViewNeedsScript puts the no-script note where the shell marks its place.
func fillViewNeedsScript(html []byte) []byte {
	return bytes.Replace(html, []byte(viewNeedsScriptMark), []byte(viewNeedsScriptNote), 1)
}

// injectNoindex marks a view's page "noindex, follow" right after <head>.
func injectNoindex(html []byte) []byte {
	const head = "<head>"
	s := string(html)
	if i := strings.Index(s, head); i >= 0 {
		return []byte(s[:i+len(head)] + `<meta name="robots" content="noindex, follow">` + s[i+len(head):])
	}
	return html
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

// pqlDefaultWindow is the time window a PQL read gets when it names none: the
// stream endpoint's ceiling (maxStreamWindowDays).
const pqlDefaultWindow = "30d"

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

// pqlPage is one page of a PQL read.
type pqlPage struct {
	Items      json.RawMessage `json:"items"`
	NextCursor string          `json:"next_cursor"`
}

// runPQL reads one page for a filter through handleStreamItems, with the
// transport parameters taken from transport. A non-200 comes back with its
// body and headers for the caller to pass through.
func (s *Server) runPQL(r *http.Request, filter *pql.Filter, transport url.Values) (pqlPage, int, []byte, http.Header) {
	if aboutMe(filter) {
		return s.activityAboutMe(transport)
	}
	if filter.Relation == "on" {
		return s.commentsOnPost(r, filter, transport)
	}
	params := pqlToStreamParams(filter)
	copyTransportParams(transport, params)
	// ⭐ A sentence that names no window reads the widest one the server
	// allows. The stream endpoint's own 24h default is for its legacy
	// callers; a PQL reader (the v5 sections, an agent over /v1/query)
	// asked for "all posts from my network", not yesterday's.
	if params.Get("time") == "" {
		params.Set("time", pqlDefaultWindow)
	}

	r2 := r.Clone(r.Context())
	r2.URL = &url.URL{Path: "/api/v1/stream/items", RawQuery: params.Encode()}
	r2.Method = http.MethodGet

	cap := &captureWriter{header: http.Header{}}
	s.handleStreamItems(cap, r2)
	if cap.status != 0 && cap.status != http.StatusOK {
		return pqlPage{}, cap.status, cap.buf.Bytes(), cap.header
	}
	var page pqlPage
	_ = json.Unmarshal(cap.buf.Bytes(), &page)
	if len(page.Items) == 0 {
		page.Items = json.RawMessage("[]")
	}
	return page, http.StatusOK, nil, nil
}

// countReachedTheCache reports whether a count of the owner's network is the
// feed cache's own limit rather than the network's number. The cache keeps
// the newest posts and comments up to a bound; a network that outposts it
// (discover.polis.pub alone, which every polis.pub site follows from signup,
// posts more than 300 times a month) fills it, and the count then says the
// bound forever. Reported in counts_capped, so the number reads "at least".
func (s *Server) countReachedTheCache(f *pql.Filter, n int) bool {
	if f.Scope != "my-network" && f.Scope != "my-mutuals" {
		return false
	}
	kind := map[string]string{"posts": "post", "comments": "comment"}[f.Type]
	if kind == "" {
		return false
	}
	cfg, err := s.feedCacheForScope("network").LoadConfig()
	if err != nil || cfg == nil {
		return false
	}
	limit := cfg.Limit(kind)
	return limit > 0 && n >= limit
}

// pqlCountPages caps how many full pages one count reads, so a count can
// never become an unbounded walk. A count that hits it is reported in
// counts_capped and is a lower bound.
const pqlCountPages = 10

// countPQL counts, for each type named in raw, the items the sentence finds
// with that type in place of its own.
//
// ⛔ A COUNT NEVER WIDENS WHAT THE REQUEST MAY SEE. The owner gate judged the
// sentence as sent. A sibling that would need the owner when the sentence
// itself did not (counts=messages on a public read) is refused: the number of
// someone's messages is theirs, even as a number.
func (s *Server) countPQL(r *http.Request, filter *pql.Filter, raw string) (map[string]int, []string, error) {
	ownerSentence := PQLRequiresOwner(r)
	counts := map[string]int{}
	var capped []string
	// ⛔ Each type is counted once: a repeated word would repeat every page
	// read (and, for threads and waiting requests, the discovery service
	// calls behind them).
	seenWord := map[string]bool{}
	for _, word := range strings.Split(raw, ",") {
		word = strings.TrimSpace(word)
		if word == "" || seenWord[word] {
			continue
		}
		seenWord[word] = true
		internal, ok := pql.InternalType(word)
		if !ok {
			return nil, nil, fmt.Errorf("counts: %q is not a PQL type", word)
		}
		sibling := *filter
		sibling.Type = internal
		probe := r.Clone(r.Context())
		probe.URL = &url.URL{Path: pql.ComposeURL(&sibling, "", "")}
		if PQLRequiresOwner(probe) && !ownerSentence {
			return nil, nil, fmt.Errorf("counts: %s are the owner's alone, and this sentence is not", word)
		}
		n, pages := 0, 0
		cursor := ""
		for {
			transport := url.Values{"limit": {strconv.Itoa(maxStreamLimit)}}
			// The count reads the window the list reads, or the number
			// and the list disagree.
			if t := r.URL.Query().Get("time"); t != "" {
				transport.Set("time", t)
			}
			if cursor != "" {
				transport.Set("cursor", cursor)
			}
			page, status, _, _ := s.runPQL(r, &sibling, transport)
			if status != http.StatusOK {
				return nil, nil, fmt.Errorf("counts: %s could not be counted", word)
			}
			var items []json.RawMessage
			_ = json.Unmarshal(page.Items, &items)
			n += len(items)
			pages++
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
			if pages >= pqlCountPages {
				capped = append(capped, word)
				// A site whose counts outgrow the cap should get a
				// stored count instead of paging; this says when.
				s.LogEvent("pub.polis.pql.counts_capped", map[string]interface{}{"type": word, "pages": pages})
				break
			}
		}
		if !slices.Contains(capped, word) && s.countReachedTheCache(&sibling, n) {
			capped = append(capped, word)
		}
		counts[word] = n
	}
	return counts, capped, nil
}

// aboutMe reports whether a filter asks for the owner's own activity: all
// activity about me (or my).
func aboutMe(f *pql.Filter) bool {
	return f.Relation == "about" && f.Type == "activity" && pql.IsFirstPerson(f.Scope)
}

// aboutMeRules are the notification rules that are about the owner, and the
// only ones "all activity about me" serves. ⛔ An ALLOW-LIST: a rule added
// later (one that might carry more than that something happened) is left out
// until it is named here, never served under the notify scope by default.
// A followed author publishing is in "from my network" instead.
var aboutMeRules = map[string]bool{
	"new-comment": true, "blessing-requested": true, "blessing-granted": true,
	"blessing-denied": true, "new-follower": true, "lost-follower": true,
}

// activityAboutMe pages through the site's notifications that are about the
// owner, newest first, as stream items. The cursor is an offset.
func (s *Server) activityAboutMe(transport url.Values) (pqlPage, int, []byte, http.Header) {
	mgr := notification.NewManager(s.Site(), s.GetDiscoveryDomain())
	all, err := mgr.List()
	if err != nil {
		return pqlPage{}, http.StatusInternalServerError, []byte(`{"error":"could not read activity"}`), http.Header{"Content-Type": {"application/json"}}
	}
	var mine []notification.StateEntry
	for i := len(all) - 1; i >= 0; i-- { // the file appends newest last
		if aboutMeRules[all[i].RuleID] {
			mine = append(mine, all[i])
		}
	}
	limit := parseStreamLimit(transport.Get("limit"))
	offset, _ := strconv.Atoi(transport.Get("cursor"))
	if offset < 0 || offset > len(mine) {
		offset = len(mine)
	}
	end := offset + limit
	if end > len(mine) {
		end = len(mine)
	}
	items := make([]map[string]interface{}, 0, end-offset)
	for _, e := range mine[offset:end] {
		item := map[string]interface{}{
			"id":      e.ID,
			"type":    "event",
			"event":   e.RuleID,
			"actor":   e.Actor,
			"message": e.Message,
			"link":    v5EventLink(e),
		}
		// ⚠️ An older entry's time may be in a JavaScript form: every item's
		// published is RFC 3339, and one that cannot be read is left out
		// rather than passed on in a form no reader can sort.
		if at := notification.PublishedRFC3339(e.CreatedAt); at != "" {
			item["published"] = at
		} else if e.CreatedAt != "" {
			s.LogWarn("activity about me: %s has an unreadable time", e.ID)
		}
		items = append(items, item)
	}
	b, _ := json.Marshal(items)
	next := ""
	if end < len(mine) {
		next = strconv.Itoa(end)
	}
	return pqlPage{Items: b, NextCursor: next}, http.StatusOK, nil, nil
}

// commentsOnPost serves "all comments on <post>": every comment made on the
// post, from the discovery service's content records, oldest first, each
// marked blessed or not from the post author's blessing records.
//
// ⛔ BLESSING NEVER FILTERS BY DEFAULT. Whether a comment is blessed matters to
// the post's author; for anyone else it is information, shown on each item.
// Only "all blessed comments on <post>" keeps just the blessed ones.
func (s *Server) commentsOnPost(r *http.Request, filter *pql.Filter, transport url.Values) (pqlPage, int, []byte, http.Header) {
	jsonErr := func(status int, msg string) (pqlPage, int, []byte, http.Header) {
		b, _ := json.Marshal(map[string]string{"error": msg})
		return pqlPage{}, status, b, http.Header{"Content-Type": {"application/json"}}
	}
	if filter.Type != "comments" {
		return jsonErr(http.StatusBadRequest, "'on' is for comments: all comments on <post address>")
	}
	if s.DiscoveryURL == "" {
		return jsonErr(http.StatusBadRequest, "discovery service not configured")
	}
	post := polisurl.NormalizeToMD(filter.Scope)
	client := s.NewReadDSClient(r)

	comments, err := client.QueryContent("pub.polis.comment", map[string]string{
		"metadata.in_reply_to": post, "limit": strconv.Itoa(maxStreamLimit),
	})
	if err != nil {
		s.LogError("comments on post: content query failed: %v", err)
		s.LogEvent("pub.polis.pql.thread_failed", map[string]interface{}{"stage": "comments"})
		return jsonErr(http.StatusBadGateway, "could not reach the discovery service")
	}
	blessed := map[string]bool{}
	if rel, err := client.QueryRelationships("pub.polis.comment.blessing", map[string]string{
		"status": "granted", "target_url": post,
	}); err == nil {
		for _, rec := range rel.Records {
			blessed[polisurl.NormalizeToMD(rec.SourceURL)] = true
		}
	} else {
		// ⚠️ Without the blessing records nothing can be marked blessed:
		// answer the comments with every mark false rather than guess.
		s.LogWarn("comments on post: blessing query failed: %v", err)
		s.LogEvent("pub.polis.pql.thread_failed", map[string]interface{}{"stage": "blessings"})
	}

	// ⛔ THE CORE PROMISE: comments that aren't blessed never appear next to
	// the owner's content on the owner's own site. A thread on one of this
	// site's own posts is blessed only, for everyone but the owner (signed in,
	// or a token that may read the owner's content); on that, the owner sees
	// everything, marked.
	blessedOnly := filter.Modifier == "blessed"
	if s.isOwnPost(post) && !s.isOwnerReading(r) {
		blessedOnly = true
	}

	records := comments.Records
	sort.SliceStable(records, func(i, j int) bool { return records[i].CreatedAt < records[j].CreatedAt })
	var items []map[string]interface{}
	if filter.Modifier == "to-bless" {
		// ⭐ "to bless" keeps only the comments waiting for the post author's
		// decision, from the author's own pending list (waitingByPost), which
		// is empty for anyone but this site's owner and for a post that is
		// not this site's.
		waiting, err := s.waitingByPost(r)
		if err != nil {
			s.LogError("comments on post: pending query failed: %v", err)
			s.LogEvent("pub.polis.pql.thread_failed", map[string]interface{}{"stage": "pending"})
			return jsonErr(http.StatusBadGateway, "could not reach the discovery service")
		}
		if !s.isOwnPost(post) {
			waiting = nil
		}
		items = waitingCommentItems(waiting[post], records, post)
		records = nil
	}
	for _, rec := range records {
		isBlessed := blessed[polisurl.NormalizeToMD(rec.URL)]
		if blessedOnly && !isBlessed {
			continue
		}
		author := rec.Actor
		if author == "" {
			author = rec.Author
		}
		domain := recordDomain(author)
		authorURL := ""
		if domain != "" {
			authorURL = "https://" + domain
		}
		items = append(items, map[string]interface{}{
			"id":            rec.ID.String(),
			"type":          "comment",
			"url":           rec.URL,
			"version":       rec.Version,
			"author_domain": domain,
			"author_url":    authorURL,
			"published":     rec.CreatedAt,
			"target_url":    post,
			"blessed":       isBlessed,
		})
	}

	limit := parseStreamLimit(transport.Get("limit"))
	offset, _ := strconv.Atoi(transport.Get("cursor"))
	if offset < 0 || offset > len(items) {
		offset = len(items)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	page := items[offset:end]
	if page == nil {
		page = []map[string]interface{}{}
	}
	s.fillCommentWords(page)
	b, _ := json.Marshal(page)
	next := ""
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return pqlPage{Items: b, NextCursor: next}, http.StatusOK, nil, nil
}

// threadWordsTTL is how long a fetched comment's words are kept.
const threadWordsTTL = 24 * time.Hour

// fillCommentWords gives each comment in a thread its words, as
// comment_body_html: an item that names only its writer cannot be read,
// blessed or decided. The owner's own comments come from the site's own
// sources; anyone else's from the commenter's site, through the SSRF-guarded,
// cached fetch the stream's focused comment uses, rendered and sanitised
// server-side (goldmark + bluemonday). A comment whose words can't be had
// keeps its other fields and no words.
//
// ⛔ Only the page being answered is filled, a few at a time, so a thread
// costs at most one page of fetches, and each is cached.
func (s *Server) fillCommentWords(page []map[string]interface{}) {
	mine := extractDomainFromURL(s.GetBaseURL())
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, item := range page {
		addr, _ := item["url"].(string)
		if addr == "" {
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func(item map[string]interface{}, addr string) {
			defer wg.Done()
			defer func() { <-slots }()
			var words string
			if author, _ := item["author_domain"].(string); author != "" && author == mine {
				words = render.LoadOwnCommentContent(s.Site(), addr)
			} else {
				words, _ = render.LoadOrFetchCommentHTML(s.Site(), addr, threadWordsTTL)
			}
			if words != "" {
				item["comment_body_html"] = words
			}
		}(item, addr)
	}
	wg.Wait()
}

// waitingCommentItems draws a post's waiting comments as thread items,
// oldest first. The pending list decides which comments wait; the content
// records only fill in what the list does not carry, so a request whose
// comment the records missed is still shown.
func waitingCommentItems(waiting []blessing.IncomingRequest, records []discovery.ContentRecord, post string) []map[string]interface{} {
	byURL := map[string]discovery.ContentRecord{}
	for _, rec := range records {
		byURL[polisurl.NormalizeToMD(rec.URL)] = rec
	}
	items := make([]map[string]interface{}, 0, len(waiting))
	for _, p := range waiting {
		id, addr, version, author, published := "pending:"+p.ID, p.CommentURL, p.CommentVersion, p.Author, p.CreatedAt
		if rec, ok := byURL[polisurl.NormalizeToMD(p.CommentURL)]; ok {
			id, addr, version, published = rec.ID.String(), rec.URL, rec.Version, rec.CreatedAt
			if a := rec.Actor; a != "" {
				author = a
			} else if rec.Author != "" {
				author = rec.Author
			}
		}
		domain := recordDomain(author)
		authorURL := ""
		if domain != "" {
			authorURL = "https://" + domain
		}
		items = append(items, map[string]interface{}{
			"id":            id,
			"type":          "comment",
			"url":           addr,
			"version":       version,
			"author_domain": domain,
			"author_url":    authorURL,
			"published":     published,
			"target_url":    post,
			"blessed":       false,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i]["published"].(string) < items[j]["published"].(string)
	})
	return items
}

// recordDomain is the site a discovery service record names as its actor or
// author. ⚠️ The DS sends a bare domain ("bob.polis.pub"), not a URL, so
// reading it as a URL found no host and every comment in a thread came back
// with no author. A full URL is accepted too.
func recordDomain(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.Contains(v, "://") {
		v = "https://" + v
	}
	return strings.ToLower(discovery.ExtractDomainFromURL(v))
}

// isOwnPost reports whether a post address is on this site.
func (s *Server) isOwnPost(post string) bool {
	// ⛔ A host's trailing dot (alice.polis.pub.) is the same host, and must
	// not slip a thread past the own-post rule.
	norm := func(d string) string { return strings.TrimSuffix(strings.ToLower(d), ".") }
	mine := norm(discovery.ExtractDomainFromURL(s.GetBaseURL()))
	return mine != "" && norm(discovery.ExtractDomainFromURL(post)) == mine
}

// OwnerSessionHeader is set by polis.pub's front door, and only there, when
// the request carries this tenant's own owner session (it removes any copy a
// caller sent). A tenant server trusts it in custodied mode only.
const OwnerSessionHeader = "X-Polis-Owner-Session"

// isOwnerReading reports whether a request is the owner's: the API marked it
// (a token that may read the owner's content), or the owner is signed in.
//
// ⛔ NEVER owner.IsOwner IN CUSTODIED MODE. There the operator provider admits
// every request, on the premise that polis.pub's front door already checked
// a session, and public /pql/ sentences and /v1 skip that check. Trusting it
// showed every visitor the unblessed comments on the owner's posts. On
// polis.pub the front door's explicit mark is the only session evidence.
func (s *Server) isOwnerReading(r *http.Request) bool {
	if api.IsOwnerRead(r.Context()) {
		return true
	}
	return s.ownerSignedIn(r)
}

// ownerSignedIn reports whether the request carries the site owner's own
// sign-in: the front door's mark on polis.pub, otherwise the owner seam
// (loopback on a laptop, an enrolled provider on a remote instance). See
// isOwnerReading for why custodied mode reads the mark alone.
func (s *Server) ownerSignedIn(r *http.Request) bool {
	if s.effectiveMode() == mode.Custodied {
		return r.Header.Get(OwnerSessionHeader) == "1"
	}
	ok, _ := owner.IsOwner(s.instance(), r)
	return ok
}

// apiOwnerSession is how the owner's own interface reaches /v1 with no token
// (api.Host.OwnerSession): the owner signed in, on a request from this site's
// own page.
//
// ⛔ CROSS-SITE IS REFUSED FIRST, reads included. The sign-in is a cookie (or,
// on a laptop, loopback), which the browser sends with any page's request;
// on polis.pub every tenant is same-site with every other. /v1 has no other
// cross-site check, since a token never rides along on its own.
func (s *Server) apiOwnerSession(r *http.Request) bool {
	if crossSiteRequest(r) {
		return false
	}
	return s.ownerSignedIn(r)
}
