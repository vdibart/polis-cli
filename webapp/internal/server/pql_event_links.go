package server

import (
	"net/url"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/notification"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// v5EventLink is where an event in "all activity about me" takes the owner:
// its place in v5 (spec 3.9), worked out from what the event is about.
//
// ⚠️ An entry's stored link is not used. Every stored entry was written by
// an older writer that no longer exists, with a v4 address
// (/_/social/followers, /_/comments?filter=blessed, /_/blessings), and nothing
// rewrites a state file. So the link is derived on the way out, from the
// event's kind and its payload (the discovery service event's), the same way
// for every entry old or new.
//
//   - a follow or unfollow → the follower's site
//   - a blessing granted or turned away → the owner's comment, on its own page
//   - a comment on the owner's post → that post, open in write
//   - a request to bless → that post's waiting comments, in write
//
// Where the payload lacks what a kind needs, the link is the nearest place
// that holds it (the legacy table's mapping), never a v4 address.
func v5EventLink(e notification.StateEntry) string {
	str := func(k string) string {
		v, _ := e.Payload[k].(string)
		return strings.TrimSpace(v)
	}
	switch e.RuleID {
	case "new-follower", "lost-follower":
		if d := recordDomain(e.Actor); d != "" {
			return "https://" + d
		}
		return "/_/gather/pql/all+activity+about+me"
	case "blessing-granted", "blessing-denied":
		comment := str("source_url")
		if comment == "" {
			// The entry's id is its dedupe key, "<rule>:<comment address>".
			comment = strings.TrimPrefix(e.ID, e.RuleID+":")
		}
		if strings.HasPrefix(comment, "https://") || strings.HasPrefix(comment, "http://") {
			return render.V5CommentPage(polisurl.NormalizeToHTML(comment))
		}
		return "/_/write/pql/all+comments+from+me"
	case "new-comment":
		if p := postPath(str("target_url")); p != "" {
			return "/_/write#" + p
		}
		return "/_/write"
	case "blessing-requested":
		if post := str("target_url"); post != "" {
			return "/_/write/pql/all+comments+on+" + url.PathEscape(polisurl.NormalizeToMD(post)) + "+to+bless"
		}
		return "/_/write/pql/all+comments+from+all+polis+to+bless"
	}
	return "/_/gather/pql/all+activity+about+me"
}

// postPath is a post's path on its site, as its page (/posts/<date>/<slug>.html),
// the form "/_/write#<post path>" takes (the public site's own form for one post).
func postPath(addr string) string {
	u, err := url.Parse(polisurl.NormalizeToHTML(addr))
	if err != nil || u.Path == "" || u.Path == "/" {
		return ""
	}
	return u.Path
}
