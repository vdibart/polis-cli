// =============================================================================
// HANDBOOK TRAIL MARKER
// =============================================================================
// This file is the *grammar* of the URL-as-filter thread. It parses PQL sentences
// (the human-readable filter language) and composes them back from filter state.
//
// Trail across files (URL-as-filter):
//   producer  — webapp/internal/webui/www/app.js: navigateTo() intercepts URLs
//   grammar   — this file (pql.js): parse / compose / parseURL / composeURL
//   consumer  — bundle-assets/pub.polis.core/shapes/v4/stream.js: applies filter
//
// One grammar, three parsers (all assert docs/general/reference/pql-golden.jsonl):
//   JS — this file        Go — cli-go/pkg/pql        TS — discovery-service/core/pql.ts
// The sentence is also a server endpoint: GET /pql/<sentence>
//   webapp — webapp/internal/server/handlers_pql.go (JSON envelope + HTML shell)
//   DS     — discovery-service/server/src/index.ts (public cross-tenant queries)
//
// Pull the thread:
//   github.com/vdibart/polis-cli/blob/main/docs/general/reference/pql.md          (spec)
//   github.com/vdibart/polis-cli/blob/main/docs/general/concepts/infinity-stream.md (why)
//   github.com/vdibart/polis-cli/blob/main/docs/handbook/url-as-filter.md  (tour)
//   github.com/vdibart/polis-cli/blob/main/AGENTS.md                    (map)
// =============================================================================

// PQL v0 parser — sentence filter as URL.
//
// The owner SPA's filter views are URL-addressable as
//   /_/pql/<sentence>
// where <sentence> is the displayed sentence filter with spaces
// replaced by `+`. The parser splits on `+`, walks the token stream
// positionally, and produces a filter-state object the v4 stream
// controller accepts via PolisStream.setFilter.
//
// Two vocabularies live in tension:
//   - PQL grammar tokens (user-facing): `messages`, `my network`,
//     `by date`, etc. — what the user sees in the sentence + URL.
//   - Internal JS values (controller / server): `dms`, `my-network`,
//     `by-date`, etc. — what existing dispatch code already speaks.
// The parser maps between them at the URL boundary so neither end
// needs to be rewritten.
//
// Grammar:
//   sentence       := qualifier type "from" scope [modifier]
//   qualifier      := "all" | "new"           ; "new" reserved
//   type           := "posts" | "comments" | "profiles"
//                   | "messages" | "drafts" | "activity"
//   scope          := "me"
//                   | "my" "network"
//                   | "my" "mutuals"
//                   | "all" "polis"
//                   | <handle>                ; FQDN pattern
//   modifier       := "by" <key>              ; date | name | activity
//                   | "with" <key>            ; comments
//                   | "to" <key>              ; bless
//
// Type-conditional rules are NOT enforced by the parser — that's the
// dispatcher's job (server-side rejection + UI slot-hiding). The
// parser is liberal in accepting any well-formed sentence; downstream
// layers narrow the valid combinations per type.
//
// Removed-but-once-supported tokens (e.g. `follows` type) parse as
// "unknown type" and fall back to null — the caller treats null as
// "render the default filter."

(function () {
    'use strict';

    // ── Vocabulary ────────────────────────────────────────────────

    // type token ↔ internal value. Only `messages` ↔ `dms` is mapped;
    // the rest pass through unchanged.
    var TYPE_TO_INTERNAL = {
        'posts': 'posts',
        'comments': 'comments',
        'profiles': 'profiles',
        'messages': 'dms',
        'drafts': 'drafts',
        'activity': 'activity',
    };
    var TYPE_TO_TOKEN = invertMap(TYPE_TO_INTERNAL);

    // Qualifiers. `new` is in the vocabulary for grammar completeness
    // but currently locked off at the UI layer — sentence
    // dropdown only surfaces `all`. The parser accepts it; the
    // dispatcher ignores qualifier=new for now.
    var QUALIFIERS = ['all', 'new'];

    // Relations. `from` = authored-by (actor axis); `about` = concerns/targets
    // (involved axis, maps to the DS involvedDomain filter). Exactly one per
    // sentence. On a tenant the relation+scope clause is optional (see the
    // defaultScope arg to parse/compose).
    var RELATIONS = ['from', 'about'];

    // Fully-qualified event types (e.g. pub.polis.follow.announced) are accepted
    // in the type slot for operational precision, alongside the friendly aliases.
    var EVENT_TYPE_PATTERN = /^pub\.polis\.[a-z]+(\.[a-z]+)+$/;

    // Scope atoms — multi-token phrases that resolve to a single
    // internal value. Order matters when atoms share a leading token
    // (e.g. both `my network` and `my mutuals` start with `my`) —
    // longer / more specific atoms are matched first.
    var SCOPE_ATOMS = [
        { tokens: ['me'],            value: 'me' },
        { tokens: ['my', 'network'], value: 'my-network' },
        { tokens: ['my', 'mutuals'], value: 'my-mutuals' },
        { tokens: ['all', 'polis'],  value: 'all-polis' },
    ];

    // Modifier clauses, keyword-led. Adding `by popularity`,
    // `to review`, `with images`, etc. later is a one-line vocabulary
    // update — no grammar restructure required.
    var MODIFIER_CLAUSES = [
        { tokens: ['by', 'date'],       value: 'by-date' },
        { tokens: ['by', 'name'],       value: 'by-name' },
        { tokens: ['by', 'activity'],   value: 'by-activity' },
        { tokens: ['with', 'comments'], value: 'with-comments' },
        { tokens: ['to', 'bless'],      value: 'to-bless' },
    ];

    // Handle pattern — a fully-qualified domain (alice.polis.pub,
    // sub.alice.polis.pub, alice.example.com). Lower-cased; the
    // sentence filter normalizes to lower-case for display.
    var HANDLE_PATTERN = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/i;

    // ── Core API ──────────────────────────────────────────────────

    // parse: PQL sentence string → filter state object.
    // Returns null for any malformed input (caller falls back to
    // default filter). Examples:
    //   "all activity from my network"
    //     → { qualifier:"all", type:"activity", scope:"my-network", modifier:null }
    //   "all messages from alice.polis.pub by date"
    //     → { qualifier:"all", type:"dms", scope:"@alice.polis.pub", modifier:"by-date" }
    // parse: PQL sentence string → filter state object. The optional
    // defaultScope (the serving tenant, e.g. "@alice.polis.pub") makes the
    // relation+scope clause optional: when omitted, the filter defaults to
    // { relation:"from", scope:defaultScope }. Pass nothing/null in the owner
    // SPA, where scope is always explicit.
    function parse(sentence, defaultScope) {
        if (typeof sentence !== 'string') return null;
        var tokens = sentence.trim().toLowerCase().split(/\s+/).filter(Boolean);
        if (tokens.length < 2) return null;

        var i = 0;
        var qualifier = tokens[i++];
        if (QUALIFIERS.indexOf(qualifier) < 0) return null;

        var typeToken = tokens[i++];
        var type = resolveType(typeToken);
        if (type === null) return null;

        var relation = 'from';
        var scope;
        if (i < tokens.length && RELATIONS.indexOf(tokens[i]) >= 0) {
            relation = tokens[i++];
            var sc = matchScope(tokens, i);
            if (!sc) return null;
            scope = sc.value;
            i += sc.consumed;
        } else if (defaultScope) {
            scope = defaultScope; // tenant-relative: clause omitted
        } else {
            return null; // strict parse requires a relation + scope clause
        }

        var modifier = null;
        if (i < tokens.length) {
            var mod = matchModifier(tokens, i);
            if (!mod) return null;
            modifier = mod.value;
            i += mod.consumed;
        }
        if (i !== tokens.length) return null; // unconsumed trailing tokens

        return {
            qualifier: qualifier,
            type: type,
            relation: relation,
            scope: scope,
            modifier: modifier,
        };
    }

    // resolveType: alias → internal value, or a fully-qualified event type
    // passes through verbatim. Returns null for an unknown type.
    function resolveType(token) {
        if (TYPE_TO_INTERNAL.hasOwnProperty(token)) return TYPE_TO_INTERNAL[token];
        if (EVENT_TYPE_PATTERN.test(token)) return token;
        return null;
    }

    // typeTokenFor: internal type value → sentence token (alias or verbatim FQ
    // event type). Returns null for an invalid value.
    function typeTokenFor(type) {
        if (TYPE_TO_TOKEN.hasOwnProperty(type)) return TYPE_TO_TOKEN[type];
        if (EVENT_TYPE_PATTERN.test(type)) return type;
        return null;
    }

    // compose: filter state object → PQL sentence string (spaces).
    // Inverse of parse. Returns "" for any invalid state. When defaultScope is
    // supplied and the filter is the tenant default (relation=from,
    // scope=defaultScope), the relation+scope clause is omitted so the sentence
    // reads "all posts by date".
    function compose(state, defaultScope) {
        if (!state) return '';
        var qualifier = state.qualifier || 'all';
        if (QUALIFIERS.indexOf(qualifier) < 0) return '';

        var typeToken = typeTokenFor(state.type);
        if (!typeToken) return '';

        var relation = state.relation || 'from';
        if (RELATIONS.indexOf(relation) < 0) return '';

        var modifierTokens = state.modifier ? modifierToTokens(state.modifier) : [];
        if (state.modifier && modifierTokens.length === 0) return '';

        // Tenant-relative omission: drop the clause for the default (from, tenant).
        if (defaultScope && relation === 'from' && state.scope === defaultScope) {
            return [qualifier, typeToken].concat(modifierTokens).join(' ');
        }

        var scopeTokens = scopeToTokens(state.scope);
        if (!scopeTokens) return '';

        return [qualifier, typeToken, relation]
            .concat(scopeTokens)
            .concat(modifierTokens)
            .join(' ');
    }

    // parseURL: extract PQL state from a location object.
    // Returns { pqlState }. pqlState is null when the URL isn't a PQL
    // path or when the sentence is malformed.
    // parseURL: extract PQL state from a location object. Matches both the SPA
    // form (/_/pql/<sentence>) and the public form (/pql/<sentence>). The
    // optional defaultScope enables tenant-relative parsing on public pages.
    function parseURL(location, defaultScope) {
        var pathname = (location && location.pathname) || '';
        var match = pathname.match(/(?:^|\/)(?:_\/)?pql\/(.+?)\/?$/);
        if (!match) return { pqlState: null };
        // Path-form uses `+` for spaces. decodeURIComponent first to
        // handle any %-encoded characters (recipient handles with dots
        // pass through unchanged; user-typed spaces would become %20).
        var sentence;
        try {
            sentence = decodeURIComponent(match[1]).replace(/\+/g, ' ');
        } catch (e) {
            return { pqlState: null };
        }
        return { pqlState: parse(sentence, defaultScope) };
    }

    // composeURL: filter state → full SPA URL.
    // basePath defaults to '/_'; pass App.basePath for parity. Returns
    // the base path alone when state is invalid (caller renders the
    // default filter at that URL).
    function composeURL(state, basePath, defaultScope) {
        var bp = basePath || '/_';
        var sentence = compose(state, defaultScope);
        if (!sentence) return bp + '/';
        return bp + '/pql/' + sentence.replace(/ /g, '+');
    }

    // ── Internals ─────────────────────────────────────────────────

    function matchScope(tokens, start) {
        // Try multi-token atoms first (longest-match isn't strictly
        // necessary here since SCOPE_ATOMS doesn't have prefix
        // collisions beyond `my`, but iterating in declaration order
        // gives deterministic resolution).
        for (var i = 0; i < SCOPE_ATOMS.length; i++) {
            var atom = SCOPE_ATOMS[i];
            if (matchSequence(tokens, start, atom.tokens)) {
                return { value: atom.value, consumed: atom.tokens.length };
            }
        }
        // Possessive-network form: `<handle>'s network`. Two-token
        // sequence; internal value is `@<handle>:network`. Used by the
        // bio "N followers" / "N following" stat-links on public pages
        // — sentence reads "all profiles from discover.polis.pub's
        // network by date". Tried BEFORE the bare-handle match since a
        // token ending in 's would still match HANDLE_PATTERN if the 's
        // happened to fit the FQDN shape; checking for the possessive
        // first guarantees the more-specific form wins.
        var posToken = tokens[start];
        var nextToken = tokens[start + 1];
        if (posToken && nextToken === 'network' && /'s$/i.test(posToken)) {
            var bareHandle = posToken.slice(0, -2);
            if (HANDLE_PATTERN.test(bareHandle)) {
                return { value: '@' + bareHandle + ':network', consumed: 2 };
            }
        }
        // Handle: single token matching FQDN pattern. Internal value
        // is `@<handle>` to match the existing parseStreamScope
        // contract on the server side.
        var token = tokens[start];
        if (token && HANDLE_PATTERN.test(token)) {
            return { value: '@' + token, consumed: 1 };
        }
        return null;
    }

    function matchModifier(tokens, start) {
        for (var i = 0; i < MODIFIER_CLAUSES.length; i++) {
            var clause = MODIFIER_CLAUSES[i];
            if (matchSequence(tokens, start, clause.tokens)) {
                return { value: clause.value, consumed: clause.tokens.length };
            }
        }
        return null;
    }

    function matchSequence(tokens, start, sequence) {
        for (var i = 0; i < sequence.length; i++) {
            if (tokens[start + i] !== sequence[i]) return false;
        }
        return true;
    }

    function scopeToTokens(scope) {
        if (typeof scope !== 'string') return null;
        // Multi-token atoms.
        for (var i = 0; i < SCOPE_ATOMS.length; i++) {
            if (SCOPE_ATOMS[i].value === scope) return SCOPE_ATOMS[i].tokens.slice();
        }
        // Possessive-network form: `@<handle>:network` →
        // [`<handle>'s`, `network`].
        if (scope.indexOf('@') === 0 && scope.slice(-8) === ':network') {
            var bareNet = scope.slice(1, -8);
            return HANDLE_PATTERN.test(bareNet) ? [bareNet + "'s", 'network'] : null;
        }
        // Handle form: internal `@<handle>` → bare handle in the URL.
        if (scope.indexOf('@') === 0) {
            var bare = scope.substring(1);
            return HANDLE_PATTERN.test(bare) ? [bare] : null;
        }
        return null;
    }

    function modifierToTokens(modifier) {
        for (var i = 0; i < MODIFIER_CLAUSES.length; i++) {
            if (MODIFIER_CLAUSES[i].value === modifier) {
                return MODIFIER_CLAUSES[i].tokens.slice();
            }
        }
        return [];
    }

    function invertMap(m) {
        var out = {};
        for (var k in m) if (Object.prototype.hasOwnProperty.call(m, k)) {
            out[m[k]] = k;
        }
        return out;
    }

    // ── Public surface ────────────────────────────────────────────

    window.PQL = {
        parse: parse,
        compose: compose,
        parseURL: parseURL,
        composeURL: composeURL,
    };
})();
