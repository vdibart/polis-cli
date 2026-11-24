// Package apidesc describes a site's public API: what each operation does,
// what it takes, who may call it and whether it changes anything, and writes
// that as an OpenAPI 3.1 document.
//
// ⭐⭐ THE DOCUMENT IS GENERATED, NEVER WRITTEN BY HAND. Each operation is
// declared once, beside the handler that serves it (ops.BuiltinCoreHandler's
// Operations), and the handler's list of actions is derived from those
// declarations, so an operation cannot be served without being described or
// described without being served. Everything that reads the API — the owner's
// new UI, an outside builder, and every MCP server polis runs (`polis mcp`,
// an instance's /mcp, polis.pub's /mcp) — reads this one description.
//
// ⭐ THE DESCRIPTION IS PER SITE. Describe takes the site's capability set and
// lists only what that site can do. A site with no operator has no account
// operations; a site whose instance never holds the owner's key cannot sign,
// so an operation that signs is left out there (until such an instance can
// hold the operation for the owner's signature); a site with no public API
// has no operations other sites call.
//
// ⛔ DESCRIPTION TEXT IS THE PRODUCT SURFACE. An agent chooses an operation
// and its arguments from these words alone (the Kay demo's finding with v0),
// so each one says what it does, what it changes, and what it will not do.
package apidesc

import (
	"sort"
	"strconv"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
)

// Version is the API version the description documents.
const Version = "v1"

// Preview marks the whole API as a preview, which it is until every site
// action has one implementation: until then an operation and its shape may
// still change, every change is listed in the changelog, and nothing changes
// silently. Then v1 is declared stable (it changes only by addition; a
// breaking change is v2, served beside v1, and v1 stays three months after v2
// is announced), and this becomes false. The developer reference, "The API
// describes itself", states the same promise.
const Preview = true

// PreviewPromise is what the description says while it is a preview.
const PreviewPromise = "This API is a preview (x-polis-preview): an operation or its shape may still " +
	"change, every change is listed in the changelog, and nothing changes silently. When it is declared " +
	"stable, v1 changes only by addition, and a breaking change is v2, served beside v1."

// DescriptionPath is where a site serves its description.
const DescriptionPath = "/v1/openapi.json"

// Auth is who may call an operation.
type Auth string

const (
	// AuthPublic: anyone, with no credential.
	AuthPublic Auth = "public"
	// AuthOwner: a bearer token the owner issued, with the scope named in
	// Operation.Scope.
	AuthOwner Auth = "owner"
	// AuthSiteSigned: another polis site, by a request signed with that
	// site's key (direct-message delivery). No token.
	AuthSiteSigned Auth = "site-signed"
	// AuthPublicOrOwner: what the request asks decides it. Public content
	// needs no credential; the owner's private content needs a token with
	// the scope in Operation.Scope (the query operation).
	AuthPublicOrOwner Auth = "public-or-owner"
)

// The scopes a token can carry: what an owner can grant an agent, version 1
// of the vocabulary (a grant names it as the behaviour set "agent/1").
//
// ⛔ ScopeFull is the owner's own token and is never granted to an agent.
// Owner-only operations need it. There is no scope for messages: reading them
// is the owner's alone, and sending gets one later.
const (
	ScopeFull       = "full"
	ScopeRead       = "read"
	ScopePublish    = "publish"
	ScopeComment    = "comment"
	ScopeBless      = "bless"
	ScopeFollow     = "follow"
	ScopeTag        = "tag"
	ScopeAppearance = "appearance"
	ScopeProfile    = "profile"
	ScopePolicyRead = "policy:read"
	// ScopePolicyWrite only tightens: it adds deny rules at the top of the
	// private rules and removes only rules the same grant added (a review rule
	// would loosen a deny, so it is refused too). See policy.TightenPrivate.
	ScopePolicyWrite = "policy:write"
	ScopeNotify      = "notify"
	ScopeStatus      = "status"
	// ScopeRender is the name appearance had before the vocabulary; keys
	// minted with it keep working, as appearance.
	ScopeRender = "render"
)

// Scopes is the grantable vocabulary, each with the sentence a consent screen
// shows for it.
var Scopes = []struct{ Name, Sentence string }{
	{ScopeRead, "Read your drafts, your network's posts and who you follow (never your messages)"},
	{ScopePublish, "Publish and republish posts as you"},
	{ScopeComment, "Write, edit and send comments as you"},
	{ScopeBless, "Bless or turn away comments on your posts"},
	{ScopeFollow, "Follow and unfollow sites as you"},
	{ScopeTag, "Tag your posts and comments"},
	{ScopeAppearance, "Change your site's theme and styles"},
	{ScopeProfile, "Change your name, avatar and bio"},
	{ScopePolicyRead, "Read your rules for who may comment and message"},
	{ScopePolicyWrite, "Tighten your rules for who may comment and message (never loosen them)"},
	{ScopeNotify, "See that something happened (a message from someone, a new follower, a comment waiting), never what it said"},
	{ScopeStatus, "See how your site is running: when it last synced, whether it published, its version"},
}

// PolicyWriteWarning is said beside policy:write wherever an owner grants
// scopes (the consent page, Settings). ⛔ That scope is never pre-ticked.
const PolicyWriteWarning = "Only tick this if you want it to change who may reach you."

// ScopeVocabulary is Scopes as the description publishes it (x-polis-scopes):
// each scope's name, the sentence a consent screen shows, whether an agent's
// grant may carry it, and the warning said beside it, if any. ⭐ Every place an
// owner grants scopes renders from this one list (the consent page reads
// Scopes; the owner app reads the description), so none can offer a different
// set.
func ScopeVocabulary() []map[string]any {
	out := make([]map[string]any, 0, len(Scopes))
	for _, s := range Scopes {
		e := map[string]any{"name": s.Name, "sentence": s.Sentence, "agent": AgentGrantable(s.Name)}
		if s.Name == ScopePolicyWrite {
			e["warning"] = PolicyWriteWarning
		}
		out = append(out, e)
	}
	return out
}

// KnownScope reports whether name is a scope a token may carry: the
// vocabulary, full, or render (appearance's earlier name).
func KnownScope(name string) bool {
	if name == ScopeFull || name == ScopeRender {
		return true
	}
	for _, s := range Scopes {
		if s.Name == name {
			return true
		}
	}
	return false
}

// AgentGrantable reports whether an agent's grant may carry name.
//
// ⛔ No full (that is the owner's own token), no tag (ruled 2026-11-23:
// tagging stays with the owner, in the CLI or the app), and no message scope
// (there is none). Everything else in the vocabulary may be granted. render is
// appearance's old name and is granted as appearance.
//
// ⭐ follow became grantable on 2026-11-23, once the follow list's entries
// could carry the marker inside its signature. It widens no existing grant: a
// grant names its scopes, and none written before then could name follow.
func AgentGrantable(name string) bool {
	switch name {
	case ScopeFull, ScopeTag:
		return false
	}
	return KnownScope(name)
}

// SameScope reports whether two scope names grant the same thing: equal, or
// appearance and render.
func SameScope(a, b string) bool {
	alias := func(s string) string {
		if s == ScopeRender {
			return ScopeAppearance
		}
		return s
	}
	return alias(a) == alias(b)
}

// Operation is one thing a caller can do.
type Operation struct {
	// Type is the content type's full name (pub.polis.post); Action is the
	// engine action (list, create, …). Together they are the operation's id.
	Type   string
	Action string

	// Method and Path are how it is reached over HTTP. Path is relative to
	// the site and may hold {id}.
	Method string
	Path   string

	// Title is the operation's name for a person, short and plain ("Restyle
	// your site"): what an agent app shows for the tool. Every operation an
	// agent may be offered declares one (x-polis-title).
	Title string

	Summary     string
	Description string

	// Input is the JSON Schema of the request body, nil for none.
	Input map[string]any
	// PathParams describes each {name} in Path.
	PathParams map[string]string
	// QueryParams describes each query-string parameter; RequiredQuery names
	// the ones a caller must send.
	QueryParams   map[string]string
	RequiredQuery []string

	Auth  Auth
	Scope string // for AuthOwner

	// Write: it changes something. Every write is marked, so a client can
	// ask the owner before calling it.
	Write bool
	// Destructive: it removes something that cannot be restored by calling
	// another operation.
	Destructive bool
	// Signs: it signs with the site's key, so the instance must hold it.
	Signs bool
	// OwnerOnly: it is on the owner-only list. It is served to the owner's
	// own full-access token and to no agent scope, ever.
	OwnerOnly bool
	// SignInOnly: an owner-only operation served on the owner's own sign-in
	// alone, and to no token, the owner's full-access one included. ⛔ For
	// what a leaked token must never do: hand over the private key, mint
	// tokens that outlive its own revocation, or rotate the key.
	SignInOnly bool

	// ⭐ THE SUCCESS ANSWER, stated, so a caller knows what it gets back
	// (answers are not wrapped in a common envelope; each
	// operation says its own shape). Output is the JSON Schema of the body;
	// OutputType names a body that is not JSON (application/zip for the
	// download); Status is the success code when the default (201 for a
	// create, 200 otherwise) is not it, 204 meaning no body.
	// TestEveryOperationStatesItsAnswer fails on one that states nothing.
	Output     map[string]any
	OutputType string
	Status     int
}

// SuccessStatus is the code an operation answers when it succeeds.
func (o Operation) SuccessStatus() int {
	if o.Status != 0 {
		return o.Status
	}
	if o.Method == "POST" && o.Action == "create" {
		return 201
	}
	return 200
}

// ID is the operation's stable id: the type's short name and the action
// (post.create, dm.mark_read).
func (o Operation) ID() string {
	return strings.TrimPrefix(o.Type, "pub.polis.") + "." + o.Action
}

// HoldsKey reports whether the instance behind a site holds the owner's
// signing key, so an operation that signs can run there.
//
// ⭐ Derived from two capabilities rather than added as a sixth, because it is
// not a new fact: an instance holds the key when it may sign unattended, and
// a site with no public API is the owner's own machine, which holds it too.
// The one site that does not is a remote instance where only the owner signs.
func HoldsKey(c capability.Set) bool {
	return c.InstanceCanSignUnattended || !c.HasPublicApi
}

// Available reports whether an operation is offered on a site with these
// capabilities.
func (o Operation) Available(c capability.Set) bool {
	if o.Signs && !HoldsKey(c) {
		return false
	}
	if o.Auth == AuthSiteSigned && !c.HasPublicApi {
		return false
	}
	return true
}

// InstanceMCP is the address of the instance's own MCP endpoint, or "" where
// there is none an outside agent can reach: a site with no public API (the
// owner's laptop serves /mcp only to itself) and a site with an operator
// (polis.pub does not route a tenant's /mcp; its own hosted MCP server is
// named when it exists).
func InstanceMCP(c capability.Set, siteURL string) string {
	if !c.HasPublicApi || c.HasOperator || siteURL == "" {
		return ""
	}
	return strings.TrimRight(siteURL, "/") + "/mcp"
}

// Pointer is what a site's .well-known/polis `api` member names: the
// description's address, the version and the MCP endpoint. ok is false for a
// site with no API an outsider can reach, which publishes no pointer.
func Pointer(c capability.Set, siteURL string) (description, version, mcp string, ok bool) {
	if !c.HasPublicApi || siteURL == "" {
		return "", "", "", false
	}
	return strings.TrimRight(siteURL, "/") + DescriptionPath, Version, InstanceMCP(c, siteURL), true
}

// Options are the per-site facts the document states.
type Options struct {
	// SiteURL is the site's base URL, the document's server.
	SiteURL string
	// MCP is the address of an MCP server for this site, "" for none.
	MCP string
	// Scopes, when not nil, is what the token asking can do: an owner
	// operation outside them is left out, so a client never offers what the
	// token will be refused. nil describes everything (the public document,
	// and the owner's own full token).
	Scopes []string
}

// allows reports whether a described operation is within o.Scopes.
func (o Options) allows(op Operation) bool {
	if o.Scopes == nil || op.Auth != AuthOwner {
		return true
	}
	for _, s := range o.Scopes {
		if SameScope(s, op.Scope) {
			return true
		}
	}
	return false
}

// Describe writes the OpenAPI 3.1 document for a site: the operations it can
// serve, then the owner-only operations as manual entries (Manual).
func Describe(ops []Operation, manual []ManualEntry, c capability.Set, o Options) map[string]any {
	paths := map[string]any{}
	var ids []string
	for _, op := range ops {
		if !op.Available(c) || !o.allows(op) {
			continue
		}
		ids = append(ids, op.ID())
		item, _ := paths[op.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[op.Path] = item
		}
		item[strings.ToLower(op.Method)] = operationObject(op)
	}
	sort.Strings(ids)

	var manuals []map[string]any
	for _, m := range manual {
		if m.Available(c) {
			manuals = append(manuals, m.object(o.SiteURL))
		}
	}

	about := "The API of one polis site: a personal website whose posts and comments are signed " +
		"with its owner's key. Reading public content needs no credential. Changing anything needs a " +
		"token the owner issued. Some things only the owner can do, by hand, in their own app; those " +
		"are listed under x-polis-manual with the steps to give them."
	if Preview {
		about += " " + PreviewPromise
	}
	info := map[string]any{
		"title":           "polis site API",
		"version":         Version,
		"description":     about,
		"x-polis-preview": Preview,
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info":    info,
		"paths":   paths,
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"token": map[string]any{
					"type": "http", "scheme": "bearer",
					"description": "A token the site's owner issued. It carries the scopes it was issued with.",
				},
				"siteSigned": map[string]any{
					"type": "apiKey", "in": "header", "name": "X-Polis-Signature",
					"description": "Another polis site's request, signed with that site's key " +
						"(X-Polis-Domain, X-Polis-Timestamp and X-Polis-Signature).",
				},
			},
		},
		"x-polis-capabilities": c,
		"x-polis-operations":   ids,
		"x-polis-manual":       manuals,
		"x-polis-prompts":      promptsFor(ids),
		"x-polis-scopes":       ScopeVocabulary(),
	}
	if o.SiteURL != "" {
		doc["servers"] = []map[string]any{{"url": strings.TrimRight(o.SiteURL, "/")}}
	}
	if o.MCP != "" {
		doc["x-polis-mcp"] = o.MCP
	}
	return doc
}

func operationObject(op Operation) map[string]any {
	obj := map[string]any{
		"operationId":         op.ID(),
		"summary":             op.Summary,
		"description":         op.Description,
		"x-polis-type":        op.Type,
		"x-polis-action":      op.Action,
		"x-polis-auth":        string(op.Auth),
		"x-polis-write":       op.Write,
		"x-polis-destructive": op.Destructive,
		"x-polis-signs":       op.Signs,
		"x-polis-owner-only":  op.OwnerOnly,
	}
	if op.SignInOnly {
		obj["x-polis-sign-in-only"] = true
	}
	if op.Title != "" {
		obj["x-polis-title"] = op.Title
	}
	switch op.Auth {
	case AuthPublic:
		obj["security"] = []any{}
	case AuthOwner:
		obj["security"] = []any{map[string]any{"token": []string{op.Scope}}}
		obj["x-polis-scope"] = op.Scope
	case AuthSiteSigned:
		obj["security"] = []any{map[string]any{"siteSigned": []string{}}}
	case AuthPublicOrOwner:
		// OpenAPI's "either": no credential, or a token with the scope.
		obj["security"] = []any{map[string]any{}, map[string]any{"token": []string{op.Scope}}}
		obj["x-polis-scope"] = op.Scope
	}
	var params []map[string]any
	var names []string
	for name := range op.PathParams {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		params = append(params, map[string]any{
			"name": name, "in": "path", "required": true,
			"description": op.PathParams[name], "schema": map[string]any{"type": "string"},
		})
	}
	var qnames []string
	for name := range op.QueryParams {
		qnames = append(qnames, name)
	}
	sort.Strings(qnames)
	for _, name := range qnames {
		required := false
		for _, r := range op.RequiredQuery {
			required = required || r == name
		}
		params = append(params, map[string]any{
			"name": name, "in": "query", "required": required,
			"description": op.QueryParams[name], "schema": map[string]any{"type": "string"},
		})
	}
	if len(params) > 0 {
		obj["parameters"] = params
	}
	if op.Input != nil {
		obj["requestBody"] = map[string]any{
			"required": true,
			"content":  map[string]any{"application/json": map[string]any{"schema": op.Input}},
		}
	}
	success := map[string]any{"description": "Done."}
	switch {
	case op.SuccessStatus() == 204:
		success["description"] = "Done. There is no body."
	case op.OutputType != "":
		success["content"] = map[string]any{op.OutputType: map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}
	case op.Output != nil:
		success["content"] = map[string]any{"application/json": map[string]any{"schema": op.Output}}
	}
	errAnswer := func(desc string) map[string]any {
		return map[string]any{"description": desc, "content": map[string]any{"application/json": map[string]any{"schema": ErrorEnvelope}}}
	}
	obj["responses"] = map[string]any{
		strconv.Itoa(op.SuccessStatus()): success,
		"400":                            errAnswer("The request was not valid; error.message says why."),
		"401":                            errAnswer("A credential is needed, or the one given is not valid."),
		"403":                            errAnswer("The credential is valid but does not allow this."),
		"default":                        errAnswer("Any other error, in the same envelope."),
	}
	return obj
}

// ErrorEnvelope is every /v1 error's body: branch on code, show message.
var ErrorEnvelope = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"status": map[string]any{"type": "string", "enum": []string{"error"}},
		"error": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code":    String("Machine-readable and stable, e.g. invalid_request, insufficient_scope, not_found, rate_limited."),
				"message": String("For a person."),
			},
			"required":             []string{"code", "message"},
			"additionalProperties": true,
		},
	},
	"required": []string{"status", "error"},
}

// Out is the JSON Schema of an operation's answer: an object with these
// properties. ⭐ Open (additionalProperties allowed): an answer may gain a
// field without breaking a caller that reads the ones it knows.
func Out(props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props}
}

// Num, Bool, Obj and Arr are answer fields, each with a description.
func Num(desc string) map[string]any  { return map[string]any{"type": "number", "description": desc} }
func Bool(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func Obj(desc string) map[string]any  { return map[string]any{"type": "object", "description": desc} }

// Arr is an array field whose items are item (nil for any).
func Arr(desc string, item map[string]any) map[string]any {
	a := map[string]any{"type": "array", "description": desc}
	if item != nil {
		a["items"] = item
	}
	return a
}

// Object is a JSON Schema object with the given properties; required names
// the ones a caller must send.
func Object(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// String is a JSON Schema string with a description.
func String(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
