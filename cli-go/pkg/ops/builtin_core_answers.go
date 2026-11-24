package ops

import (
	"fmt"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
)

// ⭐ WHAT EACH ENGINE OPERATION ANSWERS ON SUCCESS, stated in the API's
// description (there is no common success wrapper; each operation says its
// own shape). The engine sends an operation's result data as the body: 201 for
// a create, 200 otherwise. Kept beside the declarations and merged into them
// at load, so a declaration and its answer cannot be listed apart: an
// operation with no answer here panics at start, and one named here that is
// not an operation does too.

var (
	ansStr  = apidesc.String
	ansNum  = apidesc.Num
	ansBool = apidesc.Bool
	ansObj  = apidesc.Obj
	ansArr  = apidesc.Arr
	ansOut  = apidesc.Out
	ansPath = ansStr("The item's source path on the site, e.g. content/pub.polis.core/post/20261121/hello.md.")
)

// signedPost is the answer of a publish or a republish.
var signedPost = ansOut(map[string]any{
	"path": ansPath, "title": ansStr("The post's title."), "version": ansStr("The signed version, sha256:…."),
	"signature": ansStr("The signature over the post."), "url": ansStr("The post's public address."),
})

// commentWritten is comment.create's answer when it writes from text.
var commentWritten = ansOut(map[string]any{
	"comment":      ansObj("The signed comment: id, comment_url, comment_version, in_reply_to, root_post, author, timestamp, status, and its title and excerpt where it has them."),
	"sent":         ansBool("Whether it reached the discovery service for the post's author to bless."),
	"status":       ansStr("When sent: the service's answer (pending, blessed, deferred)."),
	"message":      ansStr("When sent: the service's message."),
	"auto_blessed": ansBool("When sent: the post's author had already blessed it."),
	"send_error":   ansStr("When not sent: why. The comment is signed and kept; send it again by comment_id."),
	"success":      ansBool("When sending an earlier comment by comment_id: it was sent."),
})

var coreAnswers = map[string]map[string]any{
	"pub.polis.post.list": ansOut(map[string]any{
		"posts": ansArr("The site's published posts, newest first, as its public index holds them.", nil),
		"count": ansNum("How many."),
	}),
	"pub.polis.post.create":     signedPost,
	"pub.polis.post.update":     signedPost,
	"pub.polis.post.save_draft": ansOut(map[string]any{"id": ansStr("The draft's id; send it again to replace this draft.")}),
	"pub.polis.post.unpublish": ansOut(map[string]any{
		"path":      ansStr("The source path the post had."),
		"url":       ansStr("Its address, as the discovery service knew it."),
		"draft_id":  ansStr("The draft its text is now in."),
		"discovery": ansStr("unpublished, skipped (no discovery service, or the site is not registered), or failed: <why>; the site's own unpublish stands either way."),
	}),
	"pub.polis.post.discard_draft": ansOut(map[string]any{"id": ansStr("The draft's id."), "discarded": ansBool("Always true.")}),
	"pub.polis.comment.create":     commentWritten,
	"pub.polis.comment.update": ansOut(map[string]any{
		"comment_id": ansStr("The comment's id."), "path": ansPath, "version": ansStr("The new signed version."),
		"signature": ansStr("The signature over it."), "blessing_state": ansStr("Where its blessing stands."),
		"rebeseeched": ansBool("It was sent again for blessing."), "deferred": ansBool("Sending it again was deferred."),
	}),
	"pub.polis.follow.list": ansOut(map[string]any{
		"following": ansArr("The sites followed.", ansOut(map[string]any{
			"url": ansStr("The site's address."), "added_at": ansStr("When it was followed."),
			"site_title": ansStr("Its title, where known."), "author_name": ansStr("Its author's name, where known."),
		})),
		"count":     ansNum("How many."),
		"signature": ansObj("Whether the follow list's signature checks out: status, and a message when it does not."),
	}),
	"pub.polis.dm.list": ansOut(map[string]any{
		"conversations": ansArr("The owner's conversations.", ansOut(map[string]any{
			"id": ansStr("The conversation's id."), "peer_domain": ansStr("The other site's domain."), "peer_url": ansStr("Its address."),
			"last_message_at": ansStr("When the last message was."), "unread_count": ansNum("Messages not yet read."),
			"last_preview": ansStr("Always empty: messages are sealed."),
		})),
		"count": ansNum("How many."),
	}),
	"pub.polis.dm.get": ansOut(map[string]any{
		"conversation_id": ansStr("The conversation's id."), "peer_domain": ansStr("The other site's domain."), "peer_url": ansStr("Its address."),
		"messages": ansArr("Its messages, oldest first: id, from, to, content, timestamp, status, key_epoch, locked, reply_to_id; a locked one carries ciphertext, nonce and box_pub instead of content.", nil),
		"count":    ansNum("How many."),
	}),
	"pub.polis.dm.send": ansOut(map[string]any{
		"message_id": ansStr("The message's id."), "conversation_id": ansStr("Its conversation."), "status": ansStr("sent, or queued when not delivered."),
		"error": ansStr("When saved but not delivered: why. It stays unsent and can be retried."),
	}),
	"pub.polis.dm.mark_read": ansOut(map[string]any{"conversation_id": ansStr("The conversation now read.")}),
	"pub.polis.dm.delete":    ansOut(map[string]any{"conversation_id": ansStr("The conversation removed."), "deleted": ansBool("Always true.")}),
	"pub.polis.dm.retry": ansOut(map[string]any{
		"unsent_count": ansNum("Messages not yet delivered."),
		"unsent":       ansArr("Each: conversation_id, message_id, to, timestamp.", nil),
		"retried":      ansNum("0 when there is nothing unsent (it resends nothing)."),
		"message":      ansStr("When there is nothing unsent: says so."),
	}),
	"pub.polis.dm.deliver": ansOut(map[string]any{
		"message_id": ansStr("The message as stored here."), "status": ansStr("received."),
	}),
	"pub.polis.dm.protection_status": ansOut(map[string]any{
		"status": ansStr("ok, or unknown when it could not be read."), "protected": ansBool("The site uses a messages password."), "domain": ansStr("The site."),
	}),
	"pub.polis.tag.list": ansOut(map[string]any{
		"tags":  ansArr("Every tag: tag, count, updated.", nil),
		"count": ansNum("How many."),
	}),
	"pub.polis.tag.apply":  ansOut(map[string]any{"tag": ansStr("The tag."), "target_uri": ansStr("What was tagged."), "count": ansNum("How many it now tags.")}),
	"pub.polis.tag.remove": ansOut(map[string]any{"tag": ansStr("The tag."), "target_uri": ansStr("What was untagged."), "count": ansNum("How many it now tags.")}),
	"pub.polis.tag.delete": ansOut(map[string]any{"tag": ansStr("The tag."), "deleted": ansBool("Always true.")}),
	"pub.polis.theme.list": ansOut(map[string]any{
		"themes": ansArr("The themes the owner may choose: name, version, css, compatible_shapes.", nil),
		"count":  ansNum("How many."),
	}),
	"pub.polis.theme.get": ansOut(map[string]any{
		"name": ansStr("The theme."), "version": ansStr("Its version."), "css": ansStr("Its stylesheet's name."),
		"compatible_shapes": ansArr("The shapes it styles.", ansStr("A shape.")),
	}),
	"pub.polis.license.get": ansOut(map[string]any{
		"stated":  ansBool("Whether the site has stated terms of use."),
		"terms":   ansObj("The terms (v, profile, train-ai, search, …), or null when none are stated."),
		"pointer": ansStr("Where the signed terms live on the site."),
	}),
}

func init() {
	for i := range coreOperations {
		op := &coreOperations[i]
		a, ok := coreAnswers[op.Type+"."+op.Action]
		if !ok {
			panic(fmt.Sprintf("ops: %s.%s states no answer (builtin_core_answers.go)", op.Type, op.Action))
		}
		op.Output = a
	}
	for name := range coreAnswers {
		found := false
		for _, op := range coreOperations {
			if op.Type+"."+op.Action == name {
				found = true
			}
		}
		if !found {
			panic(fmt.Sprintf("ops: builtin_core_answers.go names %s, which is not an operation", name))
		}
	}
}
