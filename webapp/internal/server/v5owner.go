package server

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/pql"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐ THE v5 OWNER APP: the owner's interface for a site switched to v5, served
// at /_/ beside today's app, which a site on v4 keeps byte for byte.
//
// It is built on /v1 alone (v5 spec 1.2), reached on the owner's own sign-in
// (api.Host.OwnerSession), so nothing here serves data: only the shell, its
// files, and the addresses of spec 3.9. Both servers call this (the laptop's
// spaHandler and polis.pub's serveTenantSPA), so the two cannot drift.

// ⭐ A site on v5 gets the v5 owner app, on every instance (a laptop, a remote
// instance, polis.pub); a site still on v4 or v3 keeps the v4 app. D1's switch
// is the site's version, which moves app and shape together. The POLIS_V5_APP
// hold that kept the app back while it was built is gone: every polis.pub
// tenant runs v5, and a new site starts on it.

// V5ShapeName is the shape a site is on when it has been switched to v5.
const V5ShapeName = "pub.polis.shapes.v5"

// SiteOnV5 reports whether a site has been switched to v5 (its active shape,
// which the rollout record sets on polis.pub).
func SiteOnV5(site storage.Site) bool {
	name, _ := bundle.GetActiveShapeName(site) // the short name: "v5"
	return name == "v5" || name == V5ShapeName
}

// V5OwnerDir is where the v5 owner app's files sit in the embedded web
// files, and V5OwnerPrefix where they are served: under /_/, so they are the
// owner's (polis.pub serves /_/ to the signed-in owner only) and never sit in
// the site's public paths.
const (
	V5OwnerDir    = "v5"
	V5OwnerPrefix = "/_/v5/"
)

// v5Sections are the signed-in sections, in the menu's order (spec 1.3).
var v5Sections = []string{"gather", "write", "message", "enhance"}

// v5EnhanceGroups are enhance's groups, each with its own address (3.9).
var v5EnhanceGroups = map[string]bool{"you": true, "site": true, "people": true, "agents": true, "data": true}

func isV5Section(s string) bool {
	for _, x := range v5Sections {
		if x == s {
			return true
		}
	}
	return false
}

// V5SectionForSentence is the section that holds what a PQL sentence
// describes (3.9's redirect rule): the owner's own posts, comments and
// drafts, and comments waiting to be blessed, are write's; messages are
// message's; everything else (activity, anyone else's posts and comments,
// profiles) is gather's. A sentence that does not parse is gather's.
func V5SectionForSentence(sentence string) string {
	// An address in the sentence travels encoded (%2F), as on /pql/.
	if dec, err := url.PathUnescape(sentence); err == nil {
		sentence = dec
	}
	f, err := pql.ParseURL("/pql/"+sentence, "@__tenant__")
	if err != nil || f == nil {
		return "gather"
	}
	switch {
	case f.Type == "dms":
		return "message"
	case f.Type == "drafts":
		return "write"
	case f.Type == "comments" && f.Modifier == "to-bless":
		return "write"
	case (f.Type == "posts" || f.Type == "comments") && f.Relation == "from" && f.Scope == "me":
		return "write"
	}
	return "gather"
}

// V5OwnerPlace says where a /_/ address goes on a site switched to v5.
// redirect is "" when the address is a v5 place and the shell answers it;
// otherwise it is where to send the browser (302), with the query kept.
//
// ⭐ EVERY ADDRESS KEEPS WORKING (D3). The v5 places are spec 3.9's table;
// everything else under /_/ is a legacy address from today's app, its
// notification links or emails already sent, and goes to its v5 place as
// 3.9's legacy table says. Anything left over goes to /_/ (the signed-in
// home), as the retired v3 addresses already do.
//
// ⚠️ A fragment never reaches a server, so the hash forms in the legacy table
// (/_/#blessings, /_/#followers) are the shell's to redirect.
func V5OwnerPlace(urlPath, rawQuery string) (redirect string) {
	withQuery := func(p string) string {
		if rawQuery != "" {
			return p + "?" + rawQuery
		}
		return p
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(urlPath, "/_"), "/")
	if urlPath != "/_" && !strings.HasPrefix(urlPath, "/_/") {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	switch {
	case rest == "":
		return "" // the signed-in home
	case strings.HasPrefix(urlPath, V5OwnerPrefix):
		return "" // the app's own files
	case len(parts) == 1 && isV5Section(parts[0]):
		return ""
	case len(parts) >= 3 && isV5Section(parts[0]) && parts[1] == "pql" && parts[2] != "":
		// A sentence under the wrong section goes to the section that holds
		// it, so a PQL address never fails for being in the wrong place.
		sentence := strings.Join(parts[2:], "/")
		if want := V5SectionForSentence(sentence); want != parts[0] {
			return withQuery("/_/" + want + "/pql/" + sentence)
		}
		return ""
	case len(parts) == 2 && parts[0] == "enhance" && v5EnhanceGroups[parts[1]]:
		return ""
	case parts[0] == "kay":
		return "" // unchanged, where Kay is switched on
	}

	// The legacy table (3.9).
	switch {
	case parts[0] == "pql" && len(parts) >= 2 && parts[1] != "":
		sentence := strings.Join(parts[1:], "/")
		return withQuery("/_/" + V5SectionForSentence(sentence) + "/pql/" + sentence)
	case rest == "settings":
		return withQuery("/_/enhance")
	case parts[0] == "messages":
		return withQuery("/_/message")
	case len(parts) >= 3 && (parts[0] == "posts" || parts[0] == "comments") && isFourDigitYear(parts[1]):
		return withQuery("/" + rest) // the public page, as today
	case rest == "posts" || parts[0] == "posts":
		return withQuery("/_/write")
	case rest == "feed":
		return withQuery("/_/gather")
	case rest == "following":
		return withQuery("/_/gather") // the follow intent and its token ride along unchanged
	case rest == "social/followers":
		return "/_/gather/pql/all+activity+about+me"
	case rest == "comments": // ?filter=blessed (an event's link), or any other filter
		return "/_/write/pql/all+comments+from+me"
	}
	return "/_/"
}

func isFourDigitYear(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// v5AssetVersion is a v5 file's content hash, the ?v= cache buster the
// shell's references carry. ⭐ Worked out on each render rather than cached:
// the shell is served once per navigation and the files are small, and a
// cache keyed by the file system panics for one that is a map.
func v5AssetVersion(fsys fs.FS, name string) string {
	return fileVersion(fsys, V5OwnerDir+"/"+name)
}

// fileVersion is the content hash of one file of the embedded web UI, by its
// path from the UI's root, or "" when there is no such file.
func fileVersion(fsys fs.FS, path string) string {
	b, err := fs.ReadFile(fsys, path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// V5OwnerShell is the v5 app's page: v5/index.html with each {{asset:<file>}}
// (one of the app's own files, under /_/v5/) and each {{site:<file>}} (a file
// the site serves from its root, such as the messages key's /dm.js) replaced
// by the file's address and content hash, and {{csp_nonce}} by the response's
// nonce.
//
// ⛔ Every script and stylesheet the shell loads from the site carries a
// version. The site root answers those files as immutable for a year, so an
// address that does not change with the bytes would keep a browser on the old
// copy, a security fix included (TestTheV5ShellVersionsEverySameOriginFile).
func V5OwnerShell(fsys fs.FS, nonce string) ([]byte, error) {
	raw, err := fs.ReadFile(fsys, V5OwnerDir+"/index.html")
	if err != nil {
		return nil, err
	}
	out := expandVersioned(string(raw), "{{asset:", func(name string) (string, string) {
		return V5OwnerPrefix + name, v5AssetVersion(fsys, name)
	})
	out = expandVersioned(out, "{{site:", func(name string) (string, string) {
		return "/" + name, fileVersion(fsys, name)
	})
	return []byte(strings.ReplaceAll(out, "{{csp_nonce}}", nonce)), nil
}

// expandVersioned replaces each <open>name}} in s with the address ref gives
// for name, carrying ?v=<version> when the file exists.
func expandVersioned(s, open string, ref func(name string) (addr, version string)) string {
	for {
		i := strings.Index(s, open)
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], "}}")
		if j < 0 {
			return s
		}
		addr, v := ref(s[i+len(open) : i+j])
		if v != "" {
			addr += "?v=" + v
		}
		s = s[:i] + addr + s[i+j+2:]
	}
}

// ServeV5Owner answers a /_/ request on a site switched to v5: a legacy
// address redirects, one of the app's files is served, and every v5 place
// gets the shell, which draws the place from the address.
func ServeV5Owner(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	if target := V5OwnerPlace(r.URL.Path, r.URL.RawQuery); target != "" {
		http.Redirect(w, r, target, http.StatusFound) //nolint:gosec // G710: the target is one of the site's own /_/ addresses or its own public path, built from the request's path
		return
	}
	if strings.HasPrefix(r.URL.Path, V5OwnerPrefix) {
		name := strings.TrimPrefix(r.URL.Path, V5OwnerPrefix)
		data, err := fs.ReadFile(fsys, V5OwnerDir+"/"+name)
		if err != nil || strings.HasSuffix(name, ".html") {
			http.NotFound(w, r)
			return
		}
		switch {
		case strings.HasSuffix(name, ".js"):
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case strings.HasSuffix(name, ".css"):
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		_, _ = w.Write(data) //nolint:gosec // G705: the embedded app's own file
		return
	}
	nonce := GenerateCSPNonce()
	page, err := V5OwnerShell(fsys, nonce)
	if err != nil {
		http.Error(w, "the v5 app is missing from this build", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", CSPAdminSPA(nonce))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(page)
}
