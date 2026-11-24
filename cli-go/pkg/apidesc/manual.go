package apidesc

import (
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
)

// ManualEntry is something only the owner can do, by hand, in their own app.
// It has no operation and sits outside every scope: an agent asked to do it
// gives the owner these steps instead.
//
// ⭐ WHY IS PART OF THE ENTRY, so an agent can explain rather than just
// refuse.
//
// ⭐ THE STEPS ARE THE v5 SPEC'S PART 5, word for word but for its marks:
// they name controls only by the spec's labels, and
// TestTheOwnerOnlyStepsAreThePartFiveSteps (webapp/internal/hosted) holds
// them to it.
type ManualEntry struct {
	ID      string
	Summary string
	Why     string
	Steps   []string
	// Link is where in the owner's app to start, relative to the site.
	Link string
	// Needs reports whether the site can do this at all.
	Needs func(capability.Set) bool
}

// StepsProvisional is true while the steps name the current app's labels
// rather than the new UI's spec. ⭐ False since 2026-11-18 (Vincent): the
// steps are the v5 spec's part 5, named by its labels and linked to its
// places, and a test holds them to it. Sites still on today's app until
// cutover are told v5's labels: decided, knowingly.
const StepsProvisional = false

// Available reports whether the entry applies to a site with these
// capabilities.
func (m ManualEntry) Available(c capability.Set) bool { return m.Needs == nil || m.Needs(c) }

func (m ManualEntry) object(siteURL string) map[string]any {
	link := m.Link
	if siteURL != "" && strings.HasPrefix(link, "/") {
		link = strings.TrimRight(siteURL, "/") + link
	}
	return map[string]any{
		"id":                m.ID,
		"summary":           m.Summary,
		"why":               m.Why,
		"steps":             m.Steps,
		"link":              link,
		"steps_provisional": StepsProvisional,
	}
}

func reachable(c capability.Set) bool         { return c.HasPublicApi }
func ownersOwnInstance(c capability.Set) bool { return c.HasPublicApi && !c.HasOperator }
func operated(c capability.Set) bool          { return c.HasOperator }

// Manual is the owner-only list as manual entries. Each is something that
// exists in the owner's app today; an owner-only operation that is not built
// yet (leaving polis.pub, signing out everywhere on polis.pub) is added when
// it is.
func Manual() []ManualEntry {
	return []ManualEntry{
		{
			ID:      "key.rotate",
			Summary: "Replace the site's signing key with a new one",
			Why: "The key is the site's identity: everything the site has signed is checked against it. " +
				"Only the owner may replace it.",
			Steps: []string{
				"Open enhance › Your data › Your key.",
				"In Your key, press Rotate.",
				"Read what rotating does, then confirm.",
			},
			Link: "/_/enhance/data#key",
		},
		{
			ID:      "site.download",
			Summary: "Download a copy of the whole site, keys included",
			Why: "The archive holds the site's private key. Whoever has a copy can sign as the site, " +
				"so only the owner takes one.",
			Steps: []string{
				"Open enhance › Your data › Take it with you.",
				"In Take it with you, press Download.",
				"Keep the archive where only you can reach it.",
			},
			Link: "/_/enhance/data#download",
		},
		{
			ID:      "post.unpublish",
			Summary: "Take one of the owner's published posts down",
			Why: "It cannot be undone: the post leaves the site and polis, publishing its text again makes a " +
				"new post, and comments shown on it are not shown again. Only the owner takes a post down.",
			Steps: []string{
				"Open write and find the post.",
				"Press edit on it.",
				"Press unpublish and confirm. Its text comes back as a draft.",
			},
			Link: "/_/write",
		},
		{
			ID:      "license.change",
			Summary: "State or change the terms others may use the owner's work under",
			Why: "The terms are a signed statement in the owner's name about their own work. " +
				"Nobody states them for the owner. An agent may read them.",
			Steps: []string{
				"Open enhance › Your site › Terms of use.",
				"In Terms of use, press Change and choose.",
			},
			Link: "/_/enhance/site#terms",
		},
		{
			ID:      "agent.grant",
			Summary: "Switch an agent on or off, or change what it may do",
			Why:     "An agent must never widen its own permissions. Only the owner grants or withdraws them.",
			Steps: []string{
				"To connect an agent: copy Your agent address (enhance › Agents) into the agent's app, sign in, tick what it may do, and press Connect.",
				"To change or end one: in enhance › Agents, press Change or Disconnect beside it (disconnecting is permanent).",
				"For a script: Make a token, in enhance › Agents.",
				"Rosie: her switch, in enhance › Agents.",
			},
			Link:  "/_/enhance/agents",
			Needs: reachable,
		},
		{
			ID:      "messages.read",
			Summary: "Read the owner's direct messages",
			Why: "Messages are encrypted end to end and opened only in the owner's browser. " +
				"No agent reads them.",
			Steps: []string{
				"Open message.",
				"If the messages are locked, enter your messages password in Your messages are locked and press Unlock.",
				"Then open the conversation.",
			},
			Link:  "/_/message",
			Needs: reachable,
		},
		{
			ID:      "messages.password",
			Summary: "Set or change the password that protects the owner's messages",
			Why:     "The password unlocks the owner's messages, and the server never receives it.",
			Steps: []string{
				"Open enhance › People › Message password.",
				"In Message password, press Set a password (the first time) or Change.",
				"Save the recovery phrase it shows: it is the only way back if the password is forgotten.",
			},
			Link:  "/_/enhance/people#message-password",
			Needs: reachable,
		},
		{
			ID:      "signin.methods",
			Summary: "Add or remove a way to sign in (a passkey, a recovery key)",
			Why:     "An agent must not be able to lock the owner out or keep itself in.",
			Steps: []string{
				"Open enhance › You.",
				"Press Add another, or remove one.",
			},
			Link:  "/_/enhance/you#passkey",
			Needs: ownersOwnInstance,
		},
		{
			ID:      "signin.everywhere",
			Summary: "Sign out of every browser and device at once",
			Why:     "Only the owner ends the owner's sessions.",
			Steps: []string{
				"Open enhance › You › Signed in here.",
				"Press Everywhere. Agents stay connected: they have their own permission.",
			},
			Link:  "/_/enhance/you#signed-in",
			Needs: ownersOwnInstance,
		},
		{
			ID:      "account.email",
			Summary: "Change the email address the owner signs in with",
			Why:     "The email is how the owner proves who they are to polis.pub.",
			Steps: []string{
				"Open enhance › You › Email.",
				"In Email, press Change and enter the new address.",
				"Confirm from the email sent to it.",
			},
			Link:  "/_/enhance/you#email",
			Needs: operated,
		},
	}
}
