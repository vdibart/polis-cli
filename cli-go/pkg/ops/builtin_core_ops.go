package ops

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
)

// The core bundle's operations, described.
//
// ⭐⭐ THIS IS THE ONE DECLARATION. Actions (what Handle accepts, and what
// /v1/bundles publishes) is derived from it, and the API's description
// (/v1/openapi.json) is generated from it, so nothing can be served without
// being described or described without being served.
// TestActionsAreExactlyWhatTheHandlerAccepts holds the handler to it.
//
// ⛔ The words are the product surface: an agent picks an operation from its
// description alone. Say what it does, what it changes, and what it won't do.

const (
	contentPath = "/v1/content/"
	typePost    = "pub.polis.post"
	typeComment = "pub.polis.comment"
	typeFollow  = "pub.polis.follow"
	typeDM      = "pub.polis.dm"
	typeTag     = "pub.polis.tag"
	typeTheme   = "pub.polis.theme"
	typeLicense = "pub.polis.license"
)

func at(short string) string        { return contentPath + short }
func action(short, a string) string { return contentPath + short + "/actions/" + a }

var tagName = apidesc.String("The tag's name, e.g. \"design\". It is normalised: \"Design\" and \"design\" are the same tag.")
var targetURI = apidesc.String("The full URL of one of this site's posts or comments.")

// coreOperations is every operation the core handler serves.
//
// ⭐ Each owner operation names the scope an agent's token needs for it (the
// vocabulary in apidesc). Messages stay on ScopeFull: reading them is the
// owner's alone and no agent may send them yet.
var coreOperations = []apidesc.Operation{
	// ── posts ──────────────────────────────────────────────────────────
	{
		Type: typePost, Action: "list", Title: "List your posts", Method: "GET", Path: at("post"),
		Summary: "List the site's published posts",
		Description: "The site's own published posts from its public index, newest first: each post's title, " +
			"date, address and version. No bodies; fetch a post's address for its text. For each post's " +
			"opening and its comment counts, use the query operation instead (pub.polis.query.pql, e.g. " +
			"'all posts from me'). Public.",
		Auth: apidesc.AuthPublic,
	},
	{
		Type: typePost, Action: "create", Title: "Publish a post", Method: "POST", Path: at("post"),
		Summary: "Publish a new post, signed with the site's key",
		Description: "Publishes a new post in the owner's name: the markdown is signed with the site's key, " +
			"registered with the discovery service, and the site is rebuilt so readers see it. The first " +
			"heading or line becomes the title. A published post is public and permanent in the sense that " +
			"others may already have a signed copy; publish only what the owner asked for.",
		Input: apidesc.Object(map[string]any{
			"markdown": apidesc.String("The post's text, in markdown. Must not be empty."),
			"filename": apidesc.String("Optional file name for the post; chosen from the title when absent."),
			"draft_id": apidesc.String("Optional: the owner's draft this post was written in, as a save answered it. It is removed once the post is published."),
		}, "markdown"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopePublish,
		Write: true, Signs: true,
	},

	{
		Type: typePost, Action: "update", Title: "Edit a post", Method: "PUT", Path: at("post") + "/{id}",
		Summary: "Change one of the owner's published posts and sign the new version",
		Description: "Rewrites one of the owner's posts with new markdown and signs the new version with the " +
			"site's key. The post keeps its address; readers see the new version, marked as edited, and its " +
			"earlier versions stay public. Name the post by its source path (from post.list).",
		PathParams: map[string]string{"id": "Ignored; the post is named by path in the body."},
		Input: apidesc.Object(map[string]any{
			"path":     apidesc.String("The post's source path, e.g. content/pub.polis.core/post/20261113/hello.md."),
			"markdown": apidesc.String("The post's new text, in markdown."),
		}, "path", "markdown"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopePublish,
		Write: true, Signs: true,
	},
	// ⛔ Owner only (decided 2026-11-21): taking a post down cannot be undone,
	// and the discovery service request it signs carries no agent marker yet.
	{
		Type: typePost, Action: "unpublish", Title: "Unpublish a post", Method: "POST", Path: action("post", "unpublish"),
		Summary: "Take one of the owner's published posts down, back to a draft",
		Description: "Removes one of the owner's posts from the site and from the discovery service: its page, " +
			"its earlier versions and its place in the site's index go, and its text comes back as a draft " +
			"(without its signature). It cannot be undone: publishing the draft again is a new post, and " +
			"comments blessed on it are no longer shown. Name the post by its source path (from post.list). " +
			"Owner only: never available to an agent.",
		Input: apidesc.Object(map[string]any{
			"path": apidesc.String("The post's source path, e.g. content/pub.polis.core/post/20261113/hello.md."),
		}, "path"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
		Write: true, Signs: true, Destructive: true,
	},
	// ⛔ Drafts are the owner's alone (decided 2026-11-16): agents act directly
	// and keep no drafts for approval, so both are owner-only. Reading drafts
	// is a PQL read, 'all drafts from me'.
	{
		Type: typePost, Action: "save_draft", Method: "POST", Path: action("post", "save_draft"),
		Summary: "Save the owner's unfinished post, new or by id",
		Description: "Keeps unfinished writing on the site, unsigned and private, so the owner can come back " +
			"to it from any device. Without an id it makes a new draft; with one it replaces that draft. " +
			"Answers the draft's id. Nothing is published. Owner only: never available to an agent.",
		Input: apidesc.Object(map[string]any{
			"id":       apidesc.String("Optional: the draft to replace, as an earlier save answered it."),
			"markdown": apidesc.String("The draft's text, in markdown (at most 256KB)."),
		}, "markdown"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
		Write: true,
	},
	{
		Type: typePost, Action: "discard_draft", Method: "POST", Path: action("post", "discard_draft"),
		Summary: "Discard one of the owner's drafts",
		Description: "Removes one unpublished draft from the site. It cannot be restored. Publishing a draft " +
			"does not need this: name it as post.create's draft_id and it is removed once the post is out. " +
			"Owner only: never available to an agent.",
		Input: apidesc.Object(map[string]any{
			"id": apidesc.String("The draft's id, as 'all drafts from me' or a save answered it."),
		}, "id"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
		Write: true, Destructive: true,
	},

	// ── comments ───────────────────────────────────────────────────────
	{
		Type: typeComment, Action: "create", Title: "Write a comment", Method: "POST", Path: at("comment"),
		Summary: "Write a comment as the owner and send it to the post's author for blessing",
		Description: "Writes a comment from text, signs it with the site's key, and sends it to the author of " +
			"the post (or comment) it replies to, whose own software decides whether to bless it; until then " +
			"it waits. Give in_reply_to and markdown. If it is signed but cannot be sent (sent: false), it " +
			"stays on the owner's site and can be sent again later by its comment_id alone. Comment only " +
			"when the owner asked.",
		Input: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"in_reply_to": apidesc.String("The address of the post or comment this replies to."),
				"root_post":   apidesc.String("Optional: the post at the top of the thread, when replying to a comment."),
				"markdown":    apidesc.String("The comment's text, in markdown (at most 64KB)."),
				"comment_id":  apidesc.String("Instead of the above: the id of a comment already signed here, to send it."),
			},
			"additionalProperties": false,
		},
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeComment,
		Write: true, Signs: true,
	},
	{
		Type: typeComment, Action: "update", Title: "Edit one of your comments", Method: "PUT", Path: at("comment") + "/{id}",
		Summary: "Change the text of one of the owner's comments and sign it again",
		Description: "Rewrites one of the owner's own comments with new markdown and signs the new version " +
			"with the site's key. The comment keeps its address; readers see the new version.",
		PathParams: map[string]string{"id": "Ignored; the comment is named by path in the body."},
		Input: apidesc.Object(map[string]any{
			"path":     apidesc.String("The comment's file path on the site, e.g. comments/20261113/reply.md."),
			"markdown": apidesc.String("The comment's new text, in markdown."),
		}, "path", "markdown"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeComment,
		Write: true, Signs: true,
	},

	// ── follows ────────────────────────────────────────────────────────
	{
		Type: typeFollow, Action: "list", Title: "See who you follow", Method: "GET", Path: at("follow"),
		Summary: "List the sites the owner follows",
		Description: "The owner's follow list: each followed site's address and when it was followed. " +
			"Needs a token with the read scope; the public copy is the site's following.json.",
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeRead,
	},

	// ── direct messages ────────────────────────────────────────────────
	{
		Type: typeDM, Action: "list", Method: "GET", Path: at("dm"),
		Summary: "List the owner's conversations",
		Description: "The owner's direct-message conversations: each peer, the last message's time and " +
			"how many are unread. Owner only: never available to an agent.",
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
	},
	{
		Type: typeDM, Action: "get", Method: "GET", Path: at("dm") + "/{id}",
		Summary: "Read one conversation",
		Description: "The messages in one conversation, with the text of those the instance can open. " +
			"Owner only: never available to an agent, because reading messages is the owner's alone.",
		PathParams: map[string]string{"id": "The conversation's id, from the conversation list."},
		Auth:       apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
	},
	{
		Type: typeDM, Action: "send", Method: "POST", Path: action("dm", "send"),
		Summary: "Send a direct message to a site the owner and the recipient both follow",
		Description: "Encrypts a message to the recipient's published messages key and delivers it, signed " +
			"by this site. Only to a site that follows the owner back. Needs the owner's own token: agents cannot send messages yet. " +
			"Which send: this one seals the message ON THE SITE, so the site sees its text; use it from a script or a " +
			"tool that holds the text. dm.send_sealed is for a client that seals the message itself (the owner's " +
			"app, when the owner keeps a messages password), so the site never sees the text. A message saved but " +
			"not delivered answers 202 with a warning, and can be retried.",
		Input: apidesc.Object(map[string]any{
			"recipient_url": apidesc.String("The recipient site's address, e.g. https://alice.polis.pub."),
			"content":       apidesc.String("The message text."),
			"reply_to_id":   apidesc.String("Optional: the id of the message this replies to."),
		}, "recipient_url", "content"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull,
		Write: true, Signs: true,
	},
	{
		Type: typeDM, Action: "mark_read", Method: "POST", Path: action("dm", "mark_read"),
		Summary:     "Mark a conversation as read",
		Description: "Marks every message in one conversation as read for the owner. Owner only.",
		Input: apidesc.Object(map[string]any{
			"conversation_id": apidesc.String("The conversation's id."),
		}, "conversation_id"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
		Write: true,
	},
	{
		Type: typeDM, Action: "delete", Method: "POST", Path: action("dm", "delete"),
		Summary: "Delete a conversation from this site",
		Description: "Removes one conversation and its messages from this site. The other site keeps its " +
			"own copy. Cannot be undone. Owner only.",
		Input: apidesc.Object(map[string]any{
			"id": apidesc.String("The conversation's id."),
		}, "id"),
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
		Write: true, Destructive: true,
	},
	{
		Type: typeDM, Action: "retry", Method: "POST", Path: action("dm", "retry"),
		Summary: "List the owner's messages that have not been delivered yet",
		Description: "Lists every message this site sent that the recipient has not yet accepted: its " +
			"conversation, id, recipient and time. Despite its name it resends nothing and changes nothing. " +
			"Owner only.",
		Auth: apidesc.AuthOwner, Scope: apidesc.ScopeFull, OwnerOnly: true,
	},
	{
		Type: typeDM, Action: "deliver", Method: "POST", Path: action("dm", "deliver"),
		Summary: "Deliver a message from another polis site",
		Description: "How another polis site hands this site an encrypted message. The request is signed " +
			"with the sending site's key, and this site's own rules decide whether to accept it. Not for " +
			"agents or the owner.",
		Input: apidesc.Object(map[string]any{
			"sender_domain": apidesc.String("The sending site's domain."),
			"envelope":      map[string]any{"type": "object", "description": "The sealed message envelope."},
		}, "sender_domain", "envelope"),
		Auth:  apidesc.AuthSiteSigned,
		Write: true,
	},
	{
		Type: typeDM, Action: "protection_status", Method: "POST", Path: action("dm", "protection_status"),
		Summary: "Tell another polis site whether this site's messages are password-protected",
		Description: "Asked by another polis site, by a signed request, before it sends a message. Not for " +
			"agents or the owner.",
		Input: apidesc.Object(map[string]any{
			"sender_domain": apidesc.String("The asking site's domain."),
		}, "sender_domain"),
		Auth: apidesc.AuthSiteSigned,
	},

	// ── tags ───────────────────────────────────────────────────────────
	{
		Type: typeTag, Action: "list", Title: "List your tags", Method: "GET", Path: at("tag"),
		Summary:     "List the site's tags",
		Description: "Every tag the owner has made, with the posts and comments each is applied to. Public.",
		Auth:        apidesc.AuthPublic,
	},
	{
		Type: typeTag, Action: "apply", Title: "Tag a post or comment", Method: "POST", Path: action("tag", "apply"),
		Summary: "Tag one of the site's posts or comments",
		Description: "Adds a post or comment to a tag, making the tag if it is new. The tag file is signed " +
			"with the site's key and announced to the discovery service.",
		Input: apidesc.Object(map[string]any{"tag": tagName, "target_uri": targetURI}, "tag", "target_uri"),
		Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeTag,
		Write: true, Signs: true,
	},
	{
		Type: typeTag, Action: "remove", Title: "Untag a post or comment", Method: "POST", Path: action("tag", "remove"),
		Summary: "Take a post or comment out of a tag",
		Description: "Removes one post or comment from a tag and signs the tag again. The post or comment " +
			"itself is unchanged.",
		Input: apidesc.Object(map[string]any{"tag": tagName, "target_uri": targetURI}, "tag", "target_uri"),
		Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeTag,
		Write: true, Signs: true,
	},
	{
		Type: typeTag, Action: "delete", Title: "Delete a tag", Method: "POST", Path: action("tag", "delete"),
		Summary: "Delete a tag",
		Description: "Deletes a tag and withdraws it from the discovery service. The posts and comments it " +
			"named are unchanged.",
		Input: apidesc.Object(map[string]any{"tag": tagName}, "tag"),
		Auth:  apidesc.AuthOwner, Scope: apidesc.ScopeTag,
		Write: true, Destructive: true, Signs: true,
	},

	// ── themes ─────────────────────────────────────────────────────────
	{
		Type: typeTheme, Action: "list", Title: "List your themes", Method: "GET", Path: at("theme"),
		Summary:     "List the themes this site can use",
		Description: "The themes installed on the site and which one is active. Public.",
		Auth:        apidesc.AuthPublic,
	},
	{
		Type: typeTheme, Action: "get", Title: "Describe a theme", Method: "GET", Path: at("theme") + "/{id}",
		Summary:     "Describe one theme",
		Description: "One installed theme's declaration, as the bundle declares it. Public.",
		PathParams:  map[string]string{"id": "The theme's name, e.g. vice."},
		Auth:        apidesc.AuthPublic,
	},

	// ── terms of use ───────────────────────────────────────────────────
	{
		Type: typeLicense, Action: "get", Title: "Read your terms of use", Method: "GET", Path: at("license") + "/current",
		Summary: "Read the site's current terms of use",
		Description: "The terms the owner states for using their work now: AI training, search, AI input " +
			"and attribution. For a particular post, read the post: its own terms are inside its signature. " +
			"Read only: changing the terms is the owner's alone.",
		Auth: apidesc.AuthPublic,
	},
}

// Operations is the core handler's described operations.
func (h *BuiltinCoreHandler) Operations() []apidesc.Operation { return coreOperations }
