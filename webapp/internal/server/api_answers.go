package server

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// ⭐ WHAT EACH HOST OPERATION ANSWERS ON SUCCESS, stated in the API's
// description (there is no common success wrapper; each operation says its
// own shape). The handlers are the site's settings, agents, messages and
// source reads; this table is applied where the host's routes are assembled
// (apiHost), and TestEveryOperationStatesItsAnswer fails on an operation that
// states nothing.

type hostAnswer struct {
	output     map[string]any
	outputType string // a body that is not JSON
	status     int    // when the default (200, 201 for a create) is not it
}

var (
	hStr  = apidesc.String
	hNum  = apidesc.Num
	hBool = apidesc.Bool
	hObj  = apidesc.Obj
	hArr  = apidesc.Arr
	hOut  = apidesc.Out
)

// policyAnswer is the owner's rules as they stand.
var policyAnswer = hOut(map[string]any{
	"order":   hStr("How the lists are read: private first."),
	"private": hArr("The private rules, in order.", hOut(map[string]any{"active": hBool("In force."), "policy": hStr("The rule.")})),
	"public":  hArr("The public rules, in order.", hOut(map[string]any{"active": hBool("In force."), "policy": hStr("The rule.")})),
})

// tokensAnswer is the site's design-token values after a change.
var tokensAnswer = hOut(map[string]any{
	"contract_version": hStr("The token contract's version."), "theme": hStr("The active theme."),
	"site_values": hObj("The values the site sets, token → value."), "values": hObj("Every token's value in effect."),
	"applies": hBool("The active theme honours them."), "note": hStr("Said when they do not apply."),
	"withheld": hStr("Said when a value was held back."), "previous": hObj("The site's values before the change."),
})

var connectionsAnswer = hOut(map[string]any{
	"connections": hArr("The agent connections, newest first: grant_id, grant, name, client, scopes, created, last_used, live, off, via (token or sign-in).", nil),
})

var rosieAnswer = hObj("Rosie's state: text, not_started, on, never, history (url, behaviours, basis, since, state, withdrawn_at, withdrawal), and live, off_since or error where they apply.")

var epochAnswer = hOut(map[string]any{"epoch_id": hNum("The messages key's new epoch."), "current_epoch": hNum("The epoch now current.")})

var hostAnswers = map[string]hostAnswer{
	"pub.polis.post.get":    {output: hOut(map[string]any{"path": hStr("The post's source path."), "markdown": hStr("Its words, without frontmatter.")})},
	"pub.polis.comment.get": {output: hOut(map[string]any{"path": hStr("The comment's source path."), "markdown": hStr("Its words, without frontmatter.")})},
	"pub.polis.post.body":   {output: hOut(map[string]any{"url": hStr("The post's address."), "body_html": hStr("Its body, rendered to HTML, the title heading left out.")})},

	"pub.polis.site.get": {output: hOut(map[string]any{
		"base_url":    hStr("The site's address. Always present; every other field is present when the site has it."),
		"author_name": hStr("The author's name."), "avatar": hObj("The avatar, or null."),
		"public_key": hStr("The identity key."), "did": hStr("Its did:web name."), "since": hStr("When the site began."),
		"followers": hNum("How many sites follow it."), "theme": hStr("The active theme."),
		"terms": hStr("Where its terms of use are."), "about": hStr("The bio, as markdown."), "about_html": hStr("The bio, as HTML."),
	})},
	"pub.polis.site.set_name":   {output: hOut(map[string]any{"success": hBool("Always true."), "author_name": hStr("The name now shown.")})},
	"pub.polis.site.set_avatar": {output: hOut(map[string]any{"success": hBool("Always true."), "avatar": hObj("The avatar now set, or null.")})},
	"pub.polis.site.set_about":  {output: hOut(map[string]any{"success": hBool("Always true.")})},
	"pub.polis.site.set_theme":  {output: hOut(map[string]any{"success": hBool("Always true."), "theme": hStr("The active theme now.")})},
	"pub.polis.theme.tokens": {output: hOut(map[string]any{
		"contract_version": hStr("The token contract's version."), "theme": hStr("The active theme."),
		"site_values": hObj("The values the site sets."), "values": hObj("Every token's value in effect."),
		"applies": hBool("The active theme honours them."), "note": hStr("Said when they do not apply."), "withheld": hStr("Said when a value is held back."),
		"tokens": hArr("Each token: name, kind, allowed, default, changes.", nil), "fonts": hArr("The fonts a font token may name.", hStr("A font.")),
		"how": hStr("How to change them."),
	})},
	"pub.polis.site.set_tokens":   {output: tokensAnswer},
	"pub.polis.site.reset_tokens": {output: tokensAnswer},
	"pub.polis.site.rebuild": {output: hOut(map[string]any{
		"success": hBool("Always true."), "path": hStr("What was rendered: /."),
		"posts_rendered": hNum("Post pages written."), "comments_rendered": hNum("Comment pages written."),
	})},
	"pub.polis.site.status": {output: hOut(map[string]any{
		"directory": hObj("The site's listing in the discovery service: configured, domain, is_registered, created_at, registry_url, or error."),
		"publish":   hObj("Whether readers see the latest: state (not-applicable, live, behind, publishing, unknown), and where the site publishes by deploying, what is waiting and the last deploy."),
	})},
	"pub.polis.site.validate": {output: hOut(map[string]any{
		"status":    hStr("valid, not_found, incomplete or invalid."),
		"errors":    hArr("Each problem: code, message, path, suggestion. Absent when there are none.", nil),
		"site_info": hObj("What the site says of itself: site_title, public_key, version, created, active_theme."),
	})},
	"pub.polis.site.register": {output: hOut(map[string]any{
		"success": hBool("It was registered."), "domain": hStr("The site."), "created_at": hStr("When the service first listed it."),
		"registry_url": hStr("Where the service lists it."), "reconciled_follows": hNum("Follows announced again."),
	})},
	"pub.polis.site.unregister": {output: hOut(map[string]any{
		"success": hBool("It was removed."), "domain": hStr("The site."), "message": hStr("The service's answer."),
	})},
	"pub.polis.site.policy":         {output: policyAnswer},
	"pub.polis.site.set_policy":     {output: policyAnswer},
	"pub.polis.site.reset_policy":   {output: policyAnswer},
	"pub.polis.site.policy_tighten": {output: policyAnswer},
	"pub.polis.site.policy_release": {output: policyAnswer},
	"pub.polis.site.rotate_key": {output: hOut(map[string]any{
		"success": hBool("Always true."), "public_key": hStr("The new identity key."),
		"key_history_epoch": hNum("Its place in the key history."),
		"following_resign":  hStr("How re-signing the follow list went."), "blessed_resign": hStr("How re-signing the blessed comments went."),
	})},
	"pub.polis.license.set": {output: hOut(map[string]any{
		"status":  hStr("success."),
		"license": hObj("The terms now in force: stated, and when stated profile, content_usage, summary, terms_url, asserted."),
	})},
	"pub.polis.site.download": {outputType: "application/zip"},

	"pub.polis.agent.list_tokens": {output: hOut(map[string]any{
		"keys": hArr("The site's tokens, never their secrets: id, name, scopes, created_at, grant.", nil),
	})},
	"pub.polis.agent.make_token": {status: 201, output: hOut(map[string]any{
		"key": hObj("The token's record: id, name, scopes, created_at, grant."), "token": hStr("The token itself, shown this once."),
		"warning": hStr("For a personal token: how to keep it."),
	})},
	"pub.polis.agent.revoke_token":     {status: 204},
	"pub.polis.agent.list_connections": {output: connectionsAnswer},
	"pub.polis.agent.disconnect":       {output: connectionsAnswer},
	"pub.polis.agent.rosie":            {output: hOut(map[string]any{"rosie": rosieAnswer})},
	"pub.polis.agent.set_rosie":        {output: hOut(map[string]any{"status": hStr("success."), "rosie": rosieAnswer})},

	"pub.polis.dm.recipients": {output: hOut(map[string]any{
		"recipients": hArr("Who the owner can message: domain, url, author_name, status (open, no-dm, no-follow, unknown), reason, follows_us, avatar.", nil),
	})},
	"pub.polis.dm.recipient_key": {output: hOut(map[string]any{
		"public_key_messages": hStr("The recipient's messages key."), "recipient_epoch": hNum("Its epoch."),
	})},
	"pub.polis.dm.recipient_protection": {output: hOut(map[string]any{
		"status": hStr("ok, or unknown when it could not be read."), "protected": hBool("The recipient uses a messages password."), "domain": hStr("The recipient."),
	})},
	"pub.polis.dm.send_sealed": {output: hOut(map[string]any{
		"message_id": hStr("The message's id."), "status": hStr("sent, or saved as unsent."),
		"warning": hStr("Said, with status 202, when it was stored but not delivered."),
	})},
	"pub.polis.dm.keyring": {output: hOut(map[string]any{
		"schema_version": hNum("The keyring's format."), "revision": hNum("Its revision, for a conditional change."),
		"current": hNum("The current epoch."), "generator": hStr("What wrote it."),
		"epochs": hArr("Each epoch: id, kind, public_key_messages, server_held, created_at, and its wrapped keys and their KDF settings.", nil),
	})},
	"pub.polis.dm.set_password":    {output: epochAnswer},
	"pub.polis.dm.new_key":         {output: epochAnswer},
	"pub.polis.dm.change_password": {output: hOut(map[string]any{"success": hBool("Always true."), "revision": hNum("The keyring's revision after.")})},
	"pub.polis.dm.rewrap_earlier":  {output: hOut(map[string]any{"epoch": hNum("The epoch re-wrapped."), "revision": hNum("The keyring's revision after.")})},
}

// withAnswers puts each route's stated answer on its declaration.
func withAnswers(routes []api.HostRoute) []api.HostRoute {
	for i := range routes {
		a, ok := hostAnswers[routes[i].Op.Type+"."+routes[i].Op.Action]
		if !ok {
			continue // TestEveryOperationStatesItsAnswer names it
		}
		routes[i].Op.Output, routes[i].Op.OutputType, routes[i].Op.Status = a.output, a.outputType, a.status
	}
	return routes
}
