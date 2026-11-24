package render

// ⭐ THE v5 SHAPE: the public site as cards under the site's own menu.
//
// A tour, in the order things happen:
//
//  1. Every item is one card, drawn by one template for its kind (post.html,
//     comment.html, about.html). The card is the item's HTML wherever it
//     appears: on its own page and in a view alike.
//  2. Every page is the frame (frame.html): the top bar with the site's menu,
//     the content column, the footer. A page is the frame around cards.
//  3. An item's own page is the frame with that one card, open: a post at
//     posts/<date>/<slug>.html, a comment at comments/<date>/<slug>.html, the
//     profile at /about (about.html at the root, served without its suffix).
//  4. A view is the frame with every card of its sentence, folded. Render
//     writes one, index.html (all posts by date). Every other view is served
//     as v4 serves it: /pql/<sentence> answers with index.html marked with its
//     sentence, and the controller (/v5.js) redraws the cards from the JSON:
//     the sentence's items, each drawn by taking its card from its own page,
//     so a card is never drawn twice in two places. The one card with no own
//     page, following, is drawn from its templates, which index.html carries
//     as <template>s (v5Templates).
//  5. State rides beside an item as data- attributes on its card, never in
//     its words: data-path (its own page), data-blessed on a comment the
//     post's author blessed.
//  6. The look is the theme's tokens only: styles.css is the shape's
//     stylesheet, then the theme's v5.css, then the site's own CSS.
//
// It follows the v5 design (the rules for cards, canonical files, the public
// site and how an item is drawn) and its drawing of the public site.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/sitemap"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"github.com/vdibart/polis-cli/cli-go/pkg/template"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// v5Sections is the site's menu, in its order (spec 3.8), each item with
// its icon (the v5 design's, drawn in 06-site).
var v5Sections = []struct{ key, label, icon string }{
	{"posts", "posts", `<path d="M5 6h14M5 11h14M5 16h9"/>`},
	{"comments", "comments", `<path d="M4 5h16v11H9l-5 4z"/>`},
	{"about", "about", `<circle cx="12" cy="8.5" r="3.6"/><path d="M5 20c.8-3.8 3.6-5.6 7-5.6s6.2 1.8 7 5.6"/>`},
	{"following", "following", `<circle cx="9" cy="9" r="3"/><path d="M3.5 19c.6-3 2.8-4.5 5.5-4.5s4.9 1.5 5.5 4.5"/><circle cx="16.5" cy="8" r="2.5"/><path d="M15.8 13.6c2.3.1 4.1 1.6 4.7 4.4"/>`},
}

// v5Post is a post as the v5 cards need it.
type v5Post struct {
	page      string // its own page, site-relative: posts/20260315/alpha.html
	source    string // its signed source, site-relative
	title     string
	published string
	version   string // the index's current_version: what its card shows changes with it
}

// v5Comment is one of the site's own comments, made on someone's post.
type v5Comment struct {
	page      string
	source    string
	published string
}

// renderV5All renders a v5 site: every item's page, the default view, the
// profile, the stylesheet and the controller, then the sitemap and the terms.
func (r *PageRenderer) renderV5All(force bool) (*RenderStats, error) {
	stats := &RenderStats{}
	if err := theme.CopyV5CSS(r.config.Site, r.themeName); err != nil {
		return nil, fmt.Errorf("write v5 styles: %w", err)
	}
	if err := theme.CopyV5Controller(r.config.Site); err != nil {
		return nil, fmt.Errorf("write v5 controller: %w", err)
	}
	// The theme's values, with the site's own over them, reach the pages too:
	// the fonts' address, and the site's mark in the theme's colours. A
	// refused definition draws in the polis default, as styles.css does.
	r.v5theme, _, _ = theme.LoadV5SiteTheme(r.config.Site, r.themeName)
	if r.v5theme != nil {
		for _, f := range r.v5theme.FellBack {
			fmt.Fprintf(os.Stderr, "[!] theme %s: font not offered (%s); the default font is used\n", r.themeName, f)
		}
	}

	posts, comments, err := r.loadV5Items()
	if err != nil {
		return nil, fmt.Errorf("load the public index: %w", err)
	}

	// 3. Each item's own page is its stream (4.2): the item, open, with up to
	// three neighbours above (newer) and three below (older), folded, carrying
	// their opening lines. Each folded card is drawn once and reused.
	postFolded := make([]string, len(posts))
	for i, p := range posts {
		card, _, err := r.v5PostCard(p, false)
		if err != nil {
			return nil, err
		}
		postFolded[i] = card
	}
	// The counts come first: an item's page carries the same toolbar as the
	// home page, so landing on it is the same as scrolling the home page to it.
	blessedBy := r.v5BlessedCounts(posts)
	counts := v5CountsOf(posts, comments, blessedBy)
	commentFolded := make([]string, len(comments))
	for i, c := range comments {
		card, _, _, _, err := r.v5CommentCard(c, false)
		if err != nil {
			return nil, err
		}
		commentFolded[i] = card
	}
	for i, p := range posts {
		card, head, err := r.v5PostCard(p, true)
		if err != nil {
			return nil, err
		}
		if err := r.writeV5Page(p.page, "posts", true, v5Stream(postFolded, i, card), p.title, head, "", counts); err != nil {
			return nil, err
		}
		stats.PostsRendered++
	}
	for i, c := range comments {
		card, title, _, head, err := r.v5CommentCard(c, true)
		if err != nil {
			return nil, err
		}
		if err := r.writeV5Page(c.page, "comments", true, v5Stream(commentFolded, i, card), title, head, "", counts); err != nil {
			return nil, err
		}
		stats.CommentsRendered++
	}
	about := r.v5AboutCard()
	siteHead, err := r.v5SiteHead("about")
	if err != nil {
		return nil, err
	}
	if err := r.writeV5Page("about.html", "about", true, about, "about", siteHead, ""); err != nil {
		return nil, err
	}

	if err := r.writeV5Index(posts, postFolded, counts); err != nil {
		return nil, err
	}
	stats.IndexGenerated = true

	mods := map[string]time.Time{}
	overrides := map[string]bool{}
	for _, p := range posts {
		mods[p.source] = r.v5SourceMod(p.source)
		overrides[p.source] = r.v5Override(p.source)
	}
	for _, c := range comments {
		mods[c.source] = r.v5SourceMod(c.source)
	}
	if err := r.writeV5Sitemap(posts, comments, func(key string) time.Time {
		if t, ok := mods[key]; ok {
			return t
		}
		return r.v5SourceMod(key)
	}); err != nil {
		fmt.Fprintf(os.Stderr, "[!] sitemap rebuild failed: %v\n", err)
	}
	if err := r.RenderLicenseSurfaces(); err != nil {
		return nil, fmt.Errorf("failed to render licence surfaces: %w", err)
	}
	r.saveV5Manifest(r.v5Record(posts, comments, blessedBy, mods, overrides))
	return stats, nil
}

// loadV5Items reads the public index: the site's posts and its own comments,
// newest first, each with its own page's address.
func (r *PageRenderer) loadV5Items() ([]v5Post, []v5Comment, error) {
	entries, err := metadata.LoadPublicIndex(r.config.Site)
	if err != nil {
		return nil, nil, err
	}
	var posts []v5Post
	var comments []v5Comment
	for _, e := range entries {
		switch {
		case e.Type == "post" || strings.HasPrefix(e.Path, "posts/"):
			posts = append(posts, v5Post{
				page:      strings.TrimSuffix(r.sourceToMountPath(e.Path, "post"), ".md") + ".html",
				source:    strings.TrimPrefix(e.Path, "/"),
				title:     e.Title,
				published: e.Published,
				version:   e.CurrentVersion,
			})
		case e.Type == "comment" || strings.HasPrefix(e.Path, "comments/"):
			comments = append(comments, v5Comment{
				page:      strings.TrimSuffix(r.sourceToMountPath(e.Path, "comment"), ".md") + ".html",
				source:    strings.TrimPrefix(e.Path, "/"),
				published: e.Published,
			})
		}
	}
	for i, j := 0, len(posts)-1; i < j; i, j = i+1, j-1 {
		posts[i], posts[j] = posts[j], posts[i]
	}
	for i, j := 0, len(comments)-1; i < j; i, j = i+1, j-1 {
		comments[i], comments[j] = comments[j], comments[i]
	}
	return posts, comments, nil
}

// v5PostCard draws a post's card. Open, it carries the post in full and its
// blessed comments in its thread; folded, the same card shows its opening
// lines (the stylesheet folds it), so the HTML differs only by the open class
// and the thread.
func (r *PageRenderer) v5PostCard(p v5Post, open bool) (string, string, error) {
	src, err := r.config.Site.Store().Get(context.Background(), p.source)
	if err != nil {
		return "", "", fmt.Errorf("read post %s: %w", p.source, err)
	}
	fm := parseFrontmatter(string(src))
	body := StripLeadingTitleHeading(stripFrontmatter(string(src)), fm["title"])
	bodyHTML, err := MarkdownToHTML(body)
	if err != nil {
		return "", "", fmt.Errorf("render post %s: %w", p.source, err)
	}
	title := fm["title"]
	if title == "" {
		title = p.title
	}
	vars := r.v5ItemVars(p.page, p.published, open)
	vars["agent"] = v5AgentAttr(fm)
	vars["title"] = html.EscapeString(title)
	// The pinned row's lead: the title, or an untitled post's opening words.
	lead := title
	if title == "" || TitleStartsFirstSentence(title, body) {
		lead = MarkdownToPlainText(body, 90)
	}
	vars["lead"] = html.EscapeString(lead)
	if title == "" || TitleStartsFirstSentence(title, body) {
		vars["title_class"] = " untitled"
	}
	vars["body"] = bodyHTML
	if !open {
		vars["body"] = v5Opening(body) // a neighbour carries its opening lines, never its full words (4.2, SEO rule 3)
	}
	vars["terms"] = v5TermsLink(license.ParseBlock(string(src)), open)

	blessed, _ := r.loadBlessedCommentsForPost(p.source)
	vars["comment_count"] = fmt.Sprintf("%d", len(blessed))
	vars["comment_noun"] = "comments"
	if len(blessed) == 1 {
		vars["comment_noun"] = "comment"
	}
	if open {
		var t strings.Builder
		for _, c := range blessed {
			// The date links to the comment on its writer's own site: a blessed
			// comment shown here is a copy, and the copy points to the original (4.1).
			fmt.Fprintf(&t, `<div class="cm">%s<div class="cm-meta"><a href="https://%s" rel="noopener">%s</a><a class="cm-d" href="%s" rel="noopener" target="_blank">%s</a></div><div class="cm-x">%s</div></div>`,
				c.AuthorAvatarHTML, html.EscapeString(c.AuthorDomain), html.EscapeString(c.AuthorDomain), html.EscapeString(V5CommentPage(c.URL)), html.EscapeString(v5Date(c.Published)), c.Content)
		}
		if len(blessed) == 0 {
			t.WriteString(`<div class="cm-none">No comments yet.</div>`)
		}
		vars["thread"] = t.String()
		// The place to reply, under the comments: the widget's (polis.pub's),
		// which covers it when it loads. Without script it says where to go.
		domain := html.EscapeString(r.getAuthorDomain())
		vars["reply"] = fmt.Sprintf(`<div data-polis-widget="reply" data-page-type="post" data-post-url="%s" data-author="%s">`+
			`<p class="reply-fallback"><a href="https://polis.pub/?intent=comment&amp;author=%s" rel="nofollow">Reply from your own polis site</a></p></div>`,
			html.EscapeString(r.buildURL(strings.TrimPrefix(p.page, "/"))), domain, domain)
	}
	card, err := r.renderV5("post", vars)
	if err != nil || !open {
		return card, "", err
	}
	// The post's own page: its head carries the post's OWN terms, frozen at
	// publish (never the site's current ones), as v4's does.
	terms := license.ParseBlock(string(src))
	licenseURL := ""
	if terms != nil {
		licenseURL = terms.Terms
	}
	head, err := r.v5Head(v5HeadData{
		title: title, description: buildOGDescription(body), canonical: r.buildURL(p.page),
		terms: terms, jsonld: "post", published: p.published, modified: fm["updated"], licenseURL: licenseURL,
	})
	return card, head, err
}

// v5CommentCard draws one of the site's own comments: what it answered, then
// its words in the comment panel, then its terms. It returns the title of the
// post it answered too, for the page's title.
func (r *PageRenderer) v5CommentCard(c v5Comment, open bool) (string, string, string, string, error) {
	src, err := r.config.Site.Store().Get(context.Background(), c.source)
	if err != nil {
		return "", "", "", "", fmt.Errorf("read comment %s: %w", c.source, err)
	}
	fm := parseFrontmatter(string(src))
	bodyHTML, err := MarkdownToHTML(stripFrontmatter(string(src)))
	if err != nil {
		return "", "", "", "", fmt.Errorf("render comment %s: %w", c.source, err)
	}
	replyURL := fm["in_reply_to"]
	if replyURL == "" {
		replyURL = parseNestedField(string(src), "in-reply-to", "url")
	}
	var title, domain string
	if replyURL != "" {
		if r.replyCache == nil {
			r.replyCache = loadReplyContextCache(r.config.Site)
		}
		// ⛔ The title comes from the other site: escaped before it is drawn
		// (spec 4.1; a stored-XSS bug in v3's comment page came from this).
		title, _, domain = resolveReplyContext(r.config.Site, replyURL, r.replyCache, r.config.Offline)
	}
	if title == "" {
		title = "a post"
	}
	vars := r.v5ItemVars(c.page, c.published, open)
	vars["agent"] = v5AgentAttr(fm)
	replyPage := html.EscapeString(strings.TrimSuffix(replyURL, ".md") + v5HTMLSuffix(replyURL))
	vars["reply_url"] = replyPage
	vars["reply_title"] = html.EscapeString(title)
	vars["reply_site"] = html.EscapeString(domain)
	vars["body"] = bodyHTML
	if !open {
		vars["body"] = v5Opening(stripFrontmatter(string(src)))
	}
	vars["terms"] = v5TermsLink(license.ParseBlock(string(src)), open)
	if replyURL != "" && r.v5CommentBlessed(c.source) {
		vars["blessed"] = ` data-blessed="1"`
		where := "the post"
		if domain != "" {
			where = html.EscapeString(domain) + "’s post"
		}
		vars["shown"] = fmt.Sprintf(`<span class="shown">shown on <a href="%s" rel="noopener">%s</a></span>`, replyPage, where)
	}
	card, err := r.renderV5("comment", vars)
	if err != nil || !open {
		return card, title, domain, "", err
	}
	terms := license.ParseBlock(string(src))
	licenseURL := ""
	if terms != nil {
		licenseURL = terms.Terms
	}
	head, err := r.v5Head(v5HeadData{
		title: "A reply to “" + title + "”", description: buildOGDescription(stripFrontmatter(string(src))),
		canonical: r.buildURL(c.page), terms: terms,
		jsonld: "comment", published: c.published, about: replyURL, licenseURL: licenseURL,
	})
	// The page's own name is what it is: a reply to the other site's post, as
	// its Open Graph title says, never that post's title alone.
	return card, "A reply to “" + title + "”", domain, head, err
}

// v5CommentBlessed: the post's author blessed this comment, so their post
// shows it. A comment reaches the public index when it is published, and
// waits in the site's pending directory, under the same name, until the
// post's author blesses it; one that is public and no longer waiting was
// blessed (the comment package's lifecycle: pending, then blessed).
//
// ⚠️ It is what the site last heard. A blessing the post's author withdraws
// later is not recorded on the commenter's site, so the line would stay.
func (r *PageRenderer) v5CommentBlessed(source string) bool {
	waiting := ".polis/bundles/pub.polis.core/comments/pending/" + path.Base(source)
	_, err := r.config.Site.Store().Stat(context.Background(), waiting)
	return err != nil
}

// v5AboutCard draws the profile: the owner's bio, then the facts a reader can
// check (the terms, the key the site signs with). Always open, and still: a
// click does nothing to it, so it shows no rollover (spec 1.5).
func (r *PageRenderer) v5AboutCard() string {
	vars := map[string]string{
		"mark":        r.v5Mark(),
		"site_domain": html.EscapeString(r.getAuthorDomain()),
		"name":        html.EscapeString(v5Or(r.getAuthorName(), r.getAuthorDomain())),
		"bio":         "", // a site with no bio shows none, never the variable's name
		"facts":       r.v5AboutFacts(),
	}
	if data, err := r.config.Site.Store().Get(context.Background(), "site/snippets/about.md"); err == nil {
		if bio, err := MarkdownToHTML(string(data)); err == nil {
			vars["bio"] = bio
		}
	}
	card, err := r.renderV5("about", vars)
	if err != nil {
		return ""
	}
	return card
}

// v5ItemVars are what every card has: its own page, its date, its mark, and
// whether it is open.
func (r *PageRenderer) v5ItemVars(page, published string, open bool) map[string]string {
	vars := map[string]string{
		"path": "/" + strings.TrimPrefix(page, "/"),
		"date": html.EscapeString(v5Date(published)),
		"iso":  html.EscapeString(formatISO(published)),
		"mark": r.v5Mark(),
		"open": "",
		// Every variable a card template names has a value, empty when the
		// card has none: an unset name would be left in the page as written.
		"title_class": "",
		"thread":      "",
		"terms":       "",
		"blessed":     "",
		"agent":       "",
		"hx":          "h3",
		"on_tag":      "div",
		"role":        ` data-neighbour`,
		"site_out":    html.EscapeString(r.getAuthorDomain()),
		"shown":       "",
		"reply":       "",
	}
	if open {
		// The page's own item: its heading is the page's one h1 (4.2, SEO rule 2).
		vars["open"] = " open"
		vars["hx"] = "h1"
		vars["on_tag"] = "h1"
		vars["role"] = ` data-focus`
	}
	return vars
}

// writeV5Page puts cards in the frame and writes the page. section lights the
// menu item; single marks an item's own page (one card, open). templates and
// counts are the views': with them the box carries every section's toolbar.
// templates are index.html's only. ⭐ An item's own page carries the same
// toolbars as the home page, and no "← all posts": it is the stream at that
// item, loading newer and older around it, so landing on it is the same as
// scrolling the home page down to it (Vincent 2026-11-20). The menu's lit
// section is still a link to its view, for a reader with no script.
func (r *PageRenderer) writeV5Page(page, section string, single bool, cards, title, head, templates string, counts ...v5Counts) error {
	page = strings.TrimPrefix(page, "/")
	site := r.getSiteTitle()
	if site == "" {
		site = r.getAuthorDomain()
	}
	pageTitle := site
	if title != "" {
		pageTitle = title + " — " + site
	}
	vars := map[string]string{
		"page_title":     html.EscapeString(pageTitle),
		"site_domain":    html.EscapeString(r.getAuthorDomain()),
		"mark":           r.v5Mark(),
		"section":        section,
		"body_class":     "",
		"cards":          cards,
		"canonical_url":  html.EscapeString(r.buildURL(v5Address(page))),
		"v5_version":     bundle.V5ShapeVersion,
		"fonts_href":     html.EscapeString(theme.V5FontsHref(r.v5theme)),
		"widget_version": WidgetVersion,
		"templates":      templates,
		"head":           head,
		"site_h1":        "",
	}
	if page == "index.html" {
		// The home page's one h1 is the site's name (4.2, SEO rule 2): its cards
		// are all folded, each with a lower heading.
		vars["site_h1"] = `<h1 class="sr-only">` + html.EscapeString(site) + "</h1>\n"
	}
	tools := ""
	if section == "about" {
		tools = r.v5AboutNote()
	}
	if len(counts) > 0 {
		tools = v5Toolbars(counts[0])
	}
	vars["menu"] = v5Menu(section, r.getAuthorDomain(), tools)
	if single {
		vars["body_class"] = " single"
	}
	out, err := r.renderV5("frame", vars)
	if err != nil {
		return fmt.Errorf("render %s: %w", page, err)
	}
	out = StripHTMLComments(out)
	if err := r.config.Site.Store().Put(context.Background(), page, []byte(out), storage.Rebuildable()); err != nil {
		return fmt.Errorf("write %s: %w", page, err)
	}
	return nil
}

// renderV5 renders one of the shape's templates with its variables.
func (r *PageRenderer) renderV5(name string, vars map[string]string) (string, error) {
	tmpl, ok := r.templates.V5[name]
	if !ok {
		return "", fmt.Errorf("v5 template %q is missing", name)
	}
	ctx := template.NewRenderContext()
	ctx.Vars = vars
	return r.engine.Render(tmpl, ctx)
}

// v5Menu draws the site's menu: the current section inside the box, at its
// right, and the others trailing it (spec 1.3, 3.8). Each is a link, so the
// menu works without script.
func v5Menu(current, domain, tools string) string {
	link := func(key, label, icon string, on bool) string {
		href := "/"
		switch key {
		case "comments":
			href = "/pql/all+comments+by+date"
		case "about":
			href = "/about"
		case "following":
			href = v5FollowingPath(domain)
		}
		class := "way"
		current := ""
		if on {
			class += " on"
			current = ` aria-current="page"`
		}
		return fmt.Sprintf(`<a class="%s" href="%s" data-way="%s" title="%s"%s><svg viewBox="0 0 24 24" aria-hidden="true">%s</svg><span class="lbl">%s</span></a>`, class, href, key, label, current, icon, label)
	}
	var b strings.Builder
	b.WriteString(`<div class="box">`)
	b.WriteString(tools)
	for _, s := range v5Sections {
		if s.key == current {
			b.WriteString(link(s.key, s.label, s.icon, true))
		}
	}
	b.WriteString(`</div><div class="trail">`)
	for _, s := range v5Sections {
		if s.key != current {
			b.WriteString(link(s.key, s.label, s.icon, false))
		}
	}
	// ⭐ home, the owner's only (spec 3.8): drawn for every reader and hidden
	// by the stylesheet unless the page's body carries pn-owner, which the
	// visitor nav sets only for the site's own author. The page stays the
	// same for everyone and names no reader.
	b.WriteString(`<a class="way own-only" href="/_/" data-way="home" title="home"><svg viewBox="0 0 24 24" aria-hidden="true">` + v5HomeIcon + `</svg><span class="lbl">home</span></a>`)
	b.WriteString(`</div>`)
	return b.String()
}

// v5HomeIcon is home's mark in the menu: a house.
const v5HomeIcon = `<path d="M4 11l8-6.5 8 6.5"/><path d="M6.5 9.5V19h11V9.5"/>`

// v5TermsLink is the footer's `carries its own terms`, where an item states
// its own terms: a link to them, with their plain sentence as its title.
//
// ⛔ rel="license" only on the page's own item: a page's rel="license" states
// the PAGE's terms, so a neighbour's chip carrying it would declare that
// neighbour's terms as the page's (4.2: pages carry neighbours).
func v5TermsLink(t *license.Terms, own bool) string {
	if t == nil || TermsSummary(t) == "" {
		return ""
	}
	href := t.Terms
	if href == "" {
		href = "/license"
	}
	rel := ""
	if own {
		rel = ` rel="license"`
	}
	return fmt.Sprintf(`<a class="terms"%s href="%s" title="%s">carries its own terms</a>`,
		rel, html.EscapeString(href), html.EscapeString(TermsSummary(t)))
}

// v5Date is the full date the owner's own archive shows: "28 Aug 2026"
// (spec 1.5, the first row).
func v5Date(published string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02"} {
		if t, err := time.Parse(layout, published); err == nil {
			return t.Format("2 Jan 2006")
		}
	}
	return published
}

func v5HTMLSuffix(u string) string {
	if strings.HasSuffix(u, ".md") {
		return ".html"
	}
	return ""
}

// v5FollowingPath is the following view's address: its sentence.
func v5FollowingPath(domain string) string {
	return "/pql/all+profiles+from+" + url.QueryEscape(domain+"'s") + "+network"
}

// v5Counts are what a view's toolbar states: how many posts, how many with
// comments, how many comments. Known at render, and replaced by the
// controller from the site's /pql/ counts as a page loads.
type v5Counts struct {
	posts, withComments, comments int
}

const v5SearchIcon = `<svg class="lens" viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="6.5"/><path d="M20 20l-4.2-4.2"/></svg>` +
	`<svg class="x" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>`

// v5Toolbars are the views' toolbars, one per section, each at the left of
// the box (spec 1.3): search, and for posts the choice of every post or only
// those with comments. The stylesheet shows the current section's, and only
// where the controller runs: without script, search cannot work and a view
// other than the posts cannot be drawn, so neither is offered.
func v5Toolbars(c v5Counts) string {
	find := func(key, what, placeholder string) string {
		// The labels are 3.8's, word for word: "search posts", "search comments", "search sites".
		return fmt.Sprintf(`<button class="sq" type="button" data-find="%[1]s" aria-label="search %[2]s" data-label="search %[2]s" aria-expanded="false" title="search %[2]s">%[4]s</button>`+
			`<label class="find" data-find="%[1]s"><input type="search" autocomplete="off" spellcheck="false" placeholder="%[3]s" aria-label="search %[2]s"></label>`,
			key, what, placeholder, v5SearchIcon)
	}
	plural := func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	}
	var b strings.Builder
	b.WriteString(`<div class="tbar" data-for="posts">` + find("posts", "posts", "search posts"))
	// ⭐ Each number carries data-count: the controller replaces it, as the
	// page loads, with what the site's /pql/ counts now, so a page the last
	// change did not rewrite never shows a stale number for long (and with
	// no script the toolbar is not shown at all).
	fmt.Fprintf(&b, `<div class="views"><a class="on" data-v="posts" href="/"><b data-count="posts">%d</b> <span data-noun="post">%s</span></a><a data-v="cm" href="/pql/all+posts+with+comments"><b data-count="with-comments">%d</b> with comments</a></div></div>`,
		c.posts, plural(c.posts, "post", "posts"), c.withComments)
	b.WriteString(`<div class="tbar" data-for="comments">` + find("comments", "comments", "search comments"))
	fmt.Fprintf(&b, `<span class="note"><b data-count="comments">%d</b> <span data-noun="comment">%s</span></span></div>`, c.comments, plural(c.comments, "comment", "comments"))
	b.WriteString(`<div class="tbar" data-for="following">` + find("following", "sites", "search sites") + `<span class="note"></span></div>`)
	return b.String()
}

// v5Templates are the following card and its row, as <template>s for the
// controller to fill: each slot the script fills is ⟪name⟫, and the script
// escapes what it puts there.
func (r *PageRenderer) v5Templates() (string, error) {
	name := r.getAuthorName()
	if name == "" {
		name = r.getAuthorDomain()
	}
	card, err := r.renderV5("following", map[string]string{
		"path":      html.EscapeString(v5FollowingPath(r.getAuthorDomain())),
		"mark":      r.v5Mark(),
		"count":     "⟪count⟫",
		"site_name": html.EscapeString(name),
		"sum":       html.EscapeString(r.v5FollowingSum(name)),
		"rows":      "⟪rows⟫",
	})
	if err != nil {
		return "", err
	}
	row, err := r.renderV5("person", map[string]string{"domain": "⟪domain⟫", "initial": "⟪initial⟫"})
	if err != nil {
		return "", err
	}
	return `<template id="v5-following">` + strings.TrimSpace(card) + `</template>` +
		`<template id="v5-person">` + strings.TrimSpace(row) + `</template>`, nil
}

// v5FollowingSum is the following card's one sentence: where the list lives,
// and that it is signed only when the site's follow file carries a signature
// (an unsigned one is legitimate, and the card must not claim otherwise).
func (r *PageRenderer) v5FollowingSum(name string) string {
	if f, err := following.LoadSite(r.config.Site); err == nil && f != nil && f.Signature != "" {
		return "This list is public, signed, and lives on " + name + "’s own site."
	}
	return "This list is public, and lives on " + name + "’s own site."
}

// v5Address is a page's address as readers use it: the site's root for
// index.html, /about for the profile (served without its suffix), and the
// page's own path otherwise.
func v5Address(page string) string {
	switch page {
	case "index.html":
		return ""
	case "about.html":
		return "about"
	}
	return page
}

// V5CommentPage is a comment's own page (4.1, /comments/<date>/<slug>.html)
// from the address it was registered under (its signed source, under the
// comment type's content folder), so a copy links straight to the original
// rather than through a redirect only some hosts serve. Any other address is
// left as it is.
func V5CommentPage(u string) string {
	const src = "/content/pub.polis.core/comment/"
	i := strings.Index(u, src)
	if i < 0 {
		return u
	}
	return u[:i] + "/comments/" + strings.TrimSuffix(strings.TrimSuffix(u[i+len(src):], ".html"), ".md") + ".html"
}

// v5ViewNeedsScriptMark marks where index.html, served as another view's page
// (/pql/<sentence>), says that view needs script. The server fills it there
// and leaves it alone on the default view; a kept comment, so render keeps it.
const v5ViewNeedsScriptMark = "<!--!polis:view-needs-script-->\n"

// v5Mark is the site's own mark on its v5 pages: its own colours, or, where
// the theme sets the mark, the theme's fill with the site's own pattern and
// initial drawn over it in the theme's ink. The pattern and the initial never
// change; only their colours do.
func (r *PageRenderer) v5Mark() string {
	// ⛔ The owner's own avatar wins: a site whose owner chose its colours is
	// drawn in them, as its favicon and every other surface draw it (Vincent,
	// 2026-11-17: the theme's peach beside the tab's teal was wrong). A theme's
	// mark style is for a site whose owner chose none.
	if r.loadLocalAvatarConfig().BG != "" {
		return r.buildAvatarHTML()
	}
	fill, ink := theme.V5MarkStyle(r.v5theme)
	if fill == "" {
		return r.buildAvatarHTML()
	}
	name := r.getAuthorName()
	if name == "" {
		name = r.getAuthorDomain()
	}
	initial := "?"
	if runes := []rune(name); len(runes) > 0 {
		initial = string(runes[0])
	}
	cfg := r.loadLocalAvatarConfig()
	style := "background:" + fill + ";color:" + ink + ";"
	if gen, ok := avatarPatterns[cfg.Pattern]; ok && cfg.Pattern != "" && cfg.Pattern != "none" {
		svg := base64.StdEncoding.EncodeToString([]byte(gen(ink)))
		style = "background:url(data:image/svg+xml;base64," + svg + ") center/cover," + fill + ";color:" + ink + ";"
		initial = ""
	}
	return fmt.Sprintf(`<span class="avatar-initial" style="%s">%s</span>`, html.EscapeString(style), html.EscapeString(initial))
}

// v5HeadData is what a page's head says about it: what crawlers and link
// previews read, and the terms it carries.
type v5HeadData struct {
	title, description, canonical string
	terms                         *license.Terms // the terms this page states, or nil (none stated)
	jsonld                        string         // "post" (BlogPosting), "comment" (Comment), "site" (WebSite), or "" (none)
	about                         string         // a comment's: the address of what it answers
	published, modified           string
	licenseURL                    string
}

// v5Head is a page's head beyond its title and canonical link, as v4's is:
// the description, the sitemap, Open Graph and Twitter cards, JSON-LD, and
// the licence (<link rel="license"> and the AIPREF content-usage meta).
// Every value is escaped here, because the template engine substitutes raw.
// ⛔ A page with no stated terms carries no licence tags: absent means
// unstated, and an empty assertion would be worse than silence.
func (r *PageRenderer) v5Head(d v5HeadData) (string, error) {
	desc := d.description
	if desc == "" {
		desc = d.title
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<meta name="description" content="%s">`+"\n", attrEscape(desc))
	b.WriteString(`<link rel="sitemap" type="application/xml" title="Sitemap" href="/sitemap.xml">` + "\n")
	ogType := "website"
	if d.jsonld == "post" {
		ogType = "article"
	}
	image := attrEscape(r.buildURL("favicon.svg"))
	fmt.Fprintf(&b, `<meta property="og:title" content="%s">`+"\n", attrEscape(d.title))
	fmt.Fprintf(&b, `<meta property="og:description" content="%s">`+"\n", attrEscape(desc))
	fmt.Fprintf(&b, `<meta property="og:url" content="%s">`+"\n", attrEscape(d.canonical))
	fmt.Fprintf(&b, `<meta property="og:type" content="%s">`+"\n", ogType)
	fmt.Fprintf(&b, `<meta property="og:image" content="%s">`+"\n", image)
	b.WriteString(`<meta name="twitter:card" content="summary">` + "\n")
	fmt.Fprintf(&b, `<meta name="twitter:title" content="%s">`+"\n", attrEscape(d.title))
	fmt.Fprintf(&b, `<meta name="twitter:description" content="%s">`+"\n", attrEscape(desc))
	fmt.Fprintf(&b, `<meta name="twitter:image" content="%s">`+"\n", image)
	var jsonLD string
	var err error
	switch d.jsonld {
	case "post":
		published := formatISO(d.published)
		modified := formatISO(d.modified)
		if modified == "" {
			modified = published
		}
		jsonLD, err = buildBlogPostingJSONLD(d.title, published, modified, r.getAuthorName(), r.buildURL(""), d.canonical, d.licenseURL)
	case "site":
		name := r.getSiteTitle()
		if name == "" {
			name = r.getAuthorDomain()
		}
		jsonLD, err = buildWebSiteJSONLD(name, r.buildURL(""), desc)
	case "comment":
		jsonLD, err = buildCommentJSONLD(d.title, formatISO(d.published), r.getAuthorName(), r.buildURL(""), d.canonical, d.about, d.licenseURL)
	}
	if err != nil {
		return "", fmt.Errorf("build JSON-LD: %w", err)
	}
	if jsonLD != "" {
		b.WriteString(jsonLD + "\n")
	}
	b.WriteString(license.HeadHTML(d.terms))
	return b.String(), nil
}

// v5SiteHead is the head of a page about the site as a whole (the posts
// view, which every other view's address also serves, and about): the site's
// CURRENT terms, the ones robots.txt and rsl.xml state, and a WebSite JSON-LD.
func (r *PageRenderer) v5SiteHead(page string) (string, error) {
	terms, _ := site.SiteTerms(r.config.Site)
	title := r.getSiteTitle()
	if title == "" {
		title = r.getAuthorDomain()
	}
	desc := ""
	if data, err := r.config.Site.Store().Get(context.Background(), "site/snippets/about.md"); err == nil {
		desc = buildOGDescription(string(data))
	}
	return r.v5Head(v5HeadData{
		title: title, description: desc, canonical: r.buildURL(page), terms: terms, jsonld: "site",
	})
}

// v5AgentAttr is the state an agent's act carries beside its card (part 4):
// data-agent, with the grant, when the item's frontmatter (inside its
// signature) marks it as written by a user agent under a grant.
func v5AgentAttr(fm map[string]string) string {
	if fm["agent"] == "" {
		return ""
	}
	return fmt.Sprintf(` data-agent="%s" data-grant="%s"`, html.EscapeString(fm["agent"]), html.EscapeString(fm["grant"]))
}

func v5Or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// v5AboutFacts are what a reader of about can check for themselves (spec 3.8,
// 06-site): the site's terms in plain words with the full terms; the key it
// signs with, short, and where to check it; and its other name, did:web,
// where it publishes a DID document. A fact the site does not have is left
// out, never shown empty or linked to nothing.
func (r *PageRenderer) v5AboutFacts() string {
	var b strings.Builder
	fact := func(k, v string) {
		fmt.Fprintf(&b, `<div class="fact"><span class="k">%s</span><span>%s</span></div>`, k, v)
	}
	if terms, _ := site.SiteTerms(r.config.Site); terms != nil {
		href := terms.Terms
		if href == "" {
			href = "/license"
		}
		fact("TERMS", html.EscapeString(TermsSummary(terms))+` <a href="`+html.EscapeString(href)+`">The full terms</a>`)
	}
	ctx := context.Background()
	if data, err := r.config.Site.Store().Get(ctx, ".well-known/polis"); err == nil {
		var wk struct {
			PublicKey string `json:"public_key"`
		}
		if json.Unmarshal(data, &wk) == nil && wk.PublicKey != "" {
			short := wk.PublicKey
			if parts := strings.Fields(short); len(parts) >= 2 {
				k := parts[1]
				if len(k) > 16 {
					k = k[:8] + "…" + k[len(k)-6:]
				}
				short = strings.TrimPrefix(parts[0], "ssh-") + " · " + k
			}
			fact("SIGNED WITH", `<code>`+html.EscapeString(short)+`</code> · <a href="/.well-known/polis">Check it</a>`)
		}
	}
	if ok, _ := r.config.Site.Store().Exists(ctx, ".well-known/did.json"); ok {
		fact("ALSO KNOWN AS", `<code>did:web:`+html.EscapeString(r.getAuthorDomain())+`</code>`)
	}
	return b.String()
}

// v5AboutNote is about's toolbar note (06-site; spec 3.8): how long the site
// has been on polis and how many follow it, as of this render. A fact the
// site does not have is left out.
func (r *PageRenderer) v5AboutNote() string {
	var parts []string
	if data, err := r.config.Site.Store().Get(context.Background(), ".well-known/polis"); err == nil {
		var wk struct {
			Created string `json:"created"`
		}
		if json.Unmarshal(data, &wk) == nil && wk.Created != "" {
			parts = append(parts, "on polis since <b>"+html.EscapeString(v5Date(wk.Created))+"</b>")
		}
	}
	ctx := template.NewRenderContext()
	r.populateSiteIdentity(ctx, 0)
	if ctx.FollowersCount > 0 {
		noun := "followers"
		if ctx.FollowersCount == 1 {
			noun = "follower"
		}
		parts = append(parts, fmt.Sprintf("<b>%d</b> %s", ctx.FollowersCount, noun))
	}
	if len(parts) == 0 {
		return ""
	}
	return `<div class="tbar on" data-for="about"><span class="note">` + strings.Join(parts, " · ") + `</span></div>`
}

// v5NeighbourBudget is how many neighbours an item's page carries (4.2): three
// above and three below, the balance moving to the other side at an edge.
const v5NeighbourBudget = 6

// v5Neighbours picks item i's neighbours in a newest-first list: up to three
// newer above and three older below, and at an edge the unused share moves to
// the other side, so a page carries six whenever the list has seven items
// (v4 padded its edges the same way). It returns the indexes, newest first.
func v5Neighbours(n, i int) (above, below []int) {
	want := v5NeighbourBudget / 2
	if n <= 1 {
		return nil, nil
	}
	nAbove := min(i, want)
	nBelow := min(n-1-i, want)
	// Give an edge's unused share to the other side.
	if extra := want - nAbove; extra > 0 {
		nBelow = min(n-1-i, nBelow+extra)
	}
	if extra := want - nBelow; extra > 0 {
		nAbove = min(i, nAbove+extra)
	}
	for k := i - nAbove; k < i; k++ {
		above = append(above, k)
	}
	for k := i + 1; k <= i+nBelow; k++ {
		below = append(below, k)
	}
	return above, below
}

// v5Stream is an item's page: its folded neighbours above, the item open, and
// its folded neighbours below.
func v5Stream(folded []string, i int, open string) string {
	above, below := v5Neighbours(len(folded), i)
	var b strings.Builder
	for _, k := range above {
		b.WriteString(folded[k])
	}
	b.WriteString(open)
	for _, k := range below {
		b.WriteString(folded[k])
	}
	return b.String()
}

// v5Opening is a folded card's words: the item's opening lines, about one
// paragraph cut at a sentence, as plain text. A neighbour never carries an
// item's full words, so no page repeats another's text (4.2, SEO rule 3);
// opening the card fetches its page for the rest.
func v5Opening(markdown string) string {
	text := MarkdownToPlainText(markdown, 600)
	if len([]rune(text)) > 420 {
		r := []rune(text)[:420]
		cut := string(r)
		end := -1
		for _, mark := range []string{". ", "? ", "! ", ".\n", "?\n", "!\n"} {
			if k := strings.LastIndex(cut, mark); k > end {
				end = k
			}
		}
		if end >= 120 {
			text = cut[:end+1]
		} else if k := strings.LastIndex(cut, " "); k > 0 {
			text = cut[:k] + "…"
		} else {
			text = cut + "…"
		}
	}
	return "<p>" + html.EscapeString(strings.TrimSpace(text)) + "</p>"
}

// writeV5Index writes the home page: the top of the posts stream (4.2), the
// newest post and up to six older, folded. Never the whole archive: the rest
// load as the reader scrolls, and every page links on to its neighbours.
// folded need only hold the cards it shows.
func (r *PageRenderer) writeV5Index(posts []v5Post, folded []string, counts v5Counts) error {
	var cards strings.Builder
	for i := range posts {
		if i <= v5NeighbourBudget {
			cards.WriteString(folded[i])
		}
	}
	if len(posts) == 0 {
		cards.WriteString(`<p class="empty">Nothing here yet.</p>`)
	}
	// index.html is also every other view's page (/pql/<sentence> serves it),
	// so it carries every section's toolbar and the following card's templates.
	templates, err := r.v5Templates()
	if err != nil {
		return err
	}
	// The server puts its no-script note for a view's page where this mark is.
	indexHead, err := r.v5SiteHead("")
	if err != nil {
		return err
	}
	return r.writeV5Page("index.html", "posts", false, v5ViewNeedsScriptMark+cards.String(), "", indexHead, templates, counts)
}

// writeV5Sitemap lists every page a reader can land on (4.2, SEO rule 4):
// the home page, each post's and each comment's page, and about. Each is
// dated by its source (about by its bio, or the home page's newest post),
// through modOf: the file's time, or the render record's for a source no
// change touched.
func (r *PageRenderer) writeV5Sitemap(posts []v5Post, comments []v5Comment, modOf func(string) time.Time) error {
	var entries []sitemap.Entry
	add := func(page string, mod time.Time) {
		if mod.IsZero() {
			return // never a zero lastmod: wrong data, not just a missing hint
		}
		entries = append(entries, sitemap.Entry{URL: r.buildURL(strings.TrimPrefix(v5Address(page), "/")), LastMod: mod})
	}
	if len(posts) > 0 {
		add("index.html", modOf(posts[0].source))
	}
	for _, p := range posts {
		add(p.page, modOf(p.source))
	}
	for _, c := range comments {
		add(c.page, modOf(c.source))
	}
	about := modOf("site/snippets/about.md")
	if about.IsZero() && len(posts) > 0 {
		about = modOf(posts[0].source)
	}
	add("about.html", about)
	body, err := sitemap.Build(entries)
	if err != nil {
		return fmt.Errorf("build sitemap: %w", err)
	}
	return sitemap.Write(r.config.Site, body)
}
