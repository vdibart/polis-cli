package render

import (
	"encoding/json"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

var updateV5Golden = flag.Bool("update-v5-golden", false, "re-record testdata/v5")

// renderV5Site builds the fixed v5 fixture site (the same posts as the render
// baseline, one of the site's own comments, and a bio) and renders it.
func renderV5Site(t *testing.T, themeName string) string {
	t.Helper()
	dir := t.TempDir()
	renderV5SiteAt(t, dir, themeName, nil)
	return dir
}

// renderV5SiteAt is renderV5Site into a given directory, with extra run on
// the site after the fixture is written and before it is rendered.
func renderV5SiteAt(t *testing.T, dir, themeName string, extra func(dir string)) {
	t.Helper()
	setupStreamTestSite(t, dir)
	if err := bundle.SetActiveShapeName(storage.At(dir), "v5"); err != nil {
		t.Fatal(err)
	}
	if err := bundle.SetActiveThemeName(storage.At(dir), themeName); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "site", "snippets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "site", "snippets", "about.md"), []byte("I write about *signed* things.\n\n- one\n- two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeStreamPost(t, dir, "20260315", "alpha", "Alpha", "First body, with a [link](https://example.org) and `code`.")
	writeStreamPost(t, dir, "20260320", "bravo", "Bravo", "## A heading\n\nSecond body.\n\n> A quotation.")
	writeStreamPost(t, dir, "20260325", "charlie", "Charlie", "Third body text.")
	writeV5Comment(t, dir, "20260326", "on-their-post", "https://other.example/posts/20260301/their-post.md",
		"So it's about the value of the item <b>…</b> interesting angle.")
	// Published, and still waiting for the post's author to bless it.
	writeV5Comment(t, dir, "20260327", "still-waiting", "https://other.example/posts/20260302/another.md", "A thought, not yet blessed.")
	pending := filepath.Join(dir, ".polis", "bundles", "pub.polis.core", "comments", "pending")
	if err := os.MkdirAll(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, "still-waiting.md"), []byte("---\n---\nA thought, not yet blessed.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if extra != nil {
		extra(dir)
	}

	pinned := time.Date(2026, 3, 26, 9, 0, 0, 0, time.UTC)
	_ = filepath.WalkDir(filepath.Join(dir, "content"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			_ = os.Chtimes(p, pinned, pinned)
		}
		return nil
	})
	r, err := NewPageRenderer(PageConfig{
		Site:              storage.At(dir),
		BaseURL:           "https://example.com",
		PostsSourceDir:    "content/pub.polis.core/post",
		PostsMountDir:     "posts",
		CommentsSourceDir: "content/pub.polis.core/comment",
		CommentsMountDir:  "comments",
		Offline:           true,
	})
	if err != nil {
		t.Fatalf("NewPageRenderer: %v", err)
	}
	if r.shapeName != "v5" {
		t.Fatalf("shape = %q, want v5", r.shapeName)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
}

func writeV5Comment(t *testing.T, dir, date, slug, replyTo, body string) {
	t.Helper()
	cdir := filepath.Join(dir, "content", "pub.polis.core", "comment", date)
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	published := date[:4] + "-" + date[4:6] + "-" + date[6:8] + "T12:00:00Z"
	md := "---\npublished: " + published + "\nin-reply-to:\n  url: " + replyTo + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(cdir, slug+".md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := metadata.AppendToPublicIndex(storage.At(dir), &metadata.IndexEntry{
		Type:      "comment",
		Path:      "content/pub.polis.core/comment/" + date + "/" + slug + ".md",
		Published: published,
		InReplyTo: &metadata.InReplyToEntry{URL: replyTo},
	}); err != nil {
		t.Fatal(err)
	}
}

// v5Pages is every page the fixture site renders to, and its golden.
var v5Pages = []string{
	"index.html",
	"posts/20260315/alpha.html",
	"posts/20260325/charlie.html",
	"comments/20260326/on-their-post.html",
	"comments/20260327/still-waiting.html",
	"about.html",
}

// TestV5Golden pins the v5 shape's output for the fixture site, page by page,
// in the polis default. Re-record with -update-v5-golden after a deliberate
// change, and read the diff: it is the change readers will see.
func TestV5Golden(t *testing.T) {
	dir := renderV5Site(t, "daryl") // daryl sets nothing: the polis default
	if out := os.Getenv("POLIS_V5_DUMP"); out != "" {
		_ = os.CopyFS(out, os.DirFS(dir))
	}
	for _, page := range v5Pages {
		got, err := os.ReadFile(filepath.Join(dir, page))
		if err != nil {
			t.Errorf("%s was not rendered: %v", page, err)
			continue
		}
		golden := filepath.Join("testdata", "v5", strings.ReplaceAll(page, "/", "__"))
		if *updateV5Golden {
			_ = os.MkdirAll(filepath.Dir(golden), 0o755)
			if err := os.WriteFile(golden, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Errorf("no golden for %s (record with -update-v5-golden)", page)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from its golden", page)
		}
	}
}

// TestV5CommentShowsWhereItIsShown: a comment the post's author blessed says
// where it is shown, and carries the state beside it (data-blessed); one
// still waiting says neither.
func TestV5CommentShowsWhereItIsShown(t *testing.T) {
	dir := renderV5Site(t, "especial-light")
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	blessed := read("comments/20260326/on-their-post.html")
	if !strings.Contains(blessed, `data-blessed="1"`) || !strings.Contains(blessed, "shown on <a href=\"https://other.example/posts/20260301/their-post.html\"") {
		t.Error("a blessed comment should say where it is shown, and carry data-blessed")
	}
	waiting := v5FocusCard(read("comments/20260327/still-waiting.html"))
	if strings.Contains(waiting, "data-blessed") || strings.Contains(waiting, "shown on") {
		t.Error("a comment still waiting for its blessing must not claim to be shown")
	}
}

// TestV5ViewsPageCarriesWhatTheControllerDraws: index.html is every view's
// page, so it carries each section's toolbar, with the counts render knows,
// and the following card's templates; an item's own page carries the same
// toolbar and no way "back": it is the stream at that item.
func TestV5ViewsPageCarriesWhatTheControllerDraws(t *testing.T) {
	dir := renderV5Site(t, "especial-light")
	index, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	for _, want := range []string{
		`data-for="posts"`, `data-for="comments"`, `data-for="following"`,
		`<b data-count="posts">3</b> <span data-noun="post">posts</span>`, `<b data-count="comments">2</b> <span data-noun="comment">comments</span>`,
		`<template id="v5-following">`, `<template id="v5-person">`, "⟪rows⟫", "⟪domain⟫",
	} {
		if !strings.Contains(string(index), want) {
			t.Errorf("index.html lacks %q", want)
		}
	}
	post, _ := os.ReadFile(filepath.Join(dir, "posts/20260315/alpha.html"))
	// Landing on an item is the same as scrolling the home page to it: the same toolbar, the same counts.
	for _, want := range []string{`data-for="posts"`, `data-for="comments"`, `<b data-count="posts">3</b>`, `<b data-count="comments">2</b>`} {
		if !strings.Contains(string(post), want) {
			t.Errorf("a post's own page should carry the home page's toolbar; it lacks %q", want)
		}
	}
	if strings.Contains(string(post), "<template") {
		t.Error("only index.html carries the following card's templates")
	}
	if strings.Contains(string(post), `class="back"`) || strings.Contains(string(post), "← all") {
		t.Error("a post's own page is the stream at that post: it should carry no way back")
	}
	if !strings.Contains(string(post), `class="way on" href="/"`) {
		t.Error("the menu's posts link should still lead to the posts, for a reader with no script")
	}
}

// TestV5EveryThemeRenders renders the fixture site in every theme that has a
// v5 definition: each one's stylesheet carries its values (never withheld),
// and its pages ask for its fonts. With POLIS_V5_DUMP_THEMES=<dir>, each
// rendering is copied to <dir>/<theme>, for a look at every theme.
func TestV5EveryThemeRenders(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "bundle", "fixtures", "pub.polis.core", "themes"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join("..", "bundle", "fixtures", "pub.polis.core", "themes", e.Name(), "v5.json")); err != nil {
			continue
		}
		n++
		dir := renderV5Site(t, e.Name())
		css, _ := os.ReadFile(filepath.Join(dir, "styles.css"))
		if !strings.Contains(string(css), "The theme: "+e.Name()) || strings.Contains(string(css), "is withheld") {
			t.Errorf("%s: styles.css does not carry the theme", e.Name())
		}
		if out := os.Getenv("POLIS_V5_DUMP_THEMES"); out != "" {
			_ = os.CopyFS(filepath.Join(out, e.Name()), os.DirFS(dir))
		}
	}
	if n < 10 {
		t.Errorf("rendered %d themes, want every converted theme", n)
	}
}

// TestV5PagesLeaveNoVariableUnfilled: the template engine leaves a variable
// with no value as written, so every v5 page is checked for a stray {{...}},
// in every theme, and for a site with no bio (a clone of vdibart.polis.pub
// rendered "{{bio}}" on its about page).
func TestV5PagesLeaveNoVariableUnfilled(t *testing.T) {
	dir := renderV5Site(t, "daryl")
	if err := os.Remove(filepath.Join(dir, "site", "snippets", "about.md")); err != nil {
		t.Fatal(err)
	}
	r, err := NewPageRenderer(PageConfig{Site: storage.At(dir), BaseURL: "https://example.com", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatal(err)
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") || strings.Contains(p, string(filepath.Separator)+".polis"+string(filepath.Separator)) {
			return nil
		}
		b, _ := os.ReadFile(p)
		if i := strings.Index(string(b), "{{"); i >= 0 {
			rel, _ := filepath.Rel(dir, p)
			t.Errorf("%s leaves a variable unfilled: %.40s", rel, string(b)[i:])
		}
		return nil
	})
}

// TestV5PagesCarryWhatCrawlersRead: every v5 page carries v4's head (the
// description, Open Graph and Twitter cards, JSON-LD), and a page's licence
// tags are ITS OWN terms: a post that states terms carries them, frozen at
// publish; a post that states none carries no licence tags at all.
func TestV5PagesCarryWhatCrawlersRead(t *testing.T) {
	dir := renderV5Site(t, "daryl")
	// Rewrite charlie with its own terms, as a publish materialises them, and render again.
	src := filepath.Join(dir, "content", "pub.polis.core", "post", "20260325", "charlie.md")
	md := "---\ntitle: Charlie\npublished: 2026-03-25T12:00:00Z\nlicense:\n  v: pub.polis.license.v1\n  profile: pub.polis.license.reserved/1\n  train-ai: n\n  search: y\n  ai-input: n\n  attribution: required\n  terms: https://example.com/license\n  contact: https://example.com/license\n  asserted: 2026-03-01T00:00:00Z\n---\nThird body text.\n"
	if err := os.WriteFile(src, []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewPageRenderer(PageConfig{Site: storage.At(dir), BaseURL: "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post", PostsMountDir: "posts",
		CommentsSourceDir: "content/pub.polis.core/comment", CommentsMountDir: "comments", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatal(err)
	}
	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(dir, p)); return string(b) }

	for _, p := range []string{"index.html", "about.html", "posts/20260315/alpha.html", "posts/20260325/charlie.html", "comments/20260326/on-their-post.html"} {
		page := read(p)
		for _, want := range []string{`<meta name="description"`, `<meta property="og:title"`, `<meta name="twitter:card"`, `<link rel="sitemap"`} {
			if !strings.Contains(page, want) {
				t.Errorf("%s lacks %s", p, want)
			}
		}
	}
	charlie := read("posts/20260325/charlie.html")
	if !strings.Contains(charlie, `<link rel="license" href="https://example.com/license">`) || !strings.Contains(charlie, `content-usage`) {
		t.Error("a post that states its own terms must carry them in its head")
	}
	if !strings.Contains(charlie, `"@type": "BlogPosting"`) {
		t.Error("a post's page carries a BlogPosting JSON-LD")
	}
	alpha := read("posts/20260315/alpha.html")
	if strings.Contains(alpha, `rel="license"`) || strings.Contains(alpha, "content-usage") {
		t.Error("a post that states no terms must carry no licence tags, and no neighbour's chip may state terms for it: absent means unstated")
	}
	if !strings.Contains(read("index.html"), `"@type": "WebSite"`) {
		t.Error("the posts view carries a WebSite JSON-LD")
	}
}

// TestV5Neighbours: three above and three below, the unused share of an edge
// moving to the other side (4.2; v4 padded its edges the same way).
func TestV5Neighbours(t *testing.T) {
	for _, tc := range []struct{ n, i, above, below int }{
		{1, 0, 0, 0},  // one item: alone
		{10, 0, 0, 6}, // the newest: six older below
		{10, 9, 6, 0}, // the oldest: six newer above
		{10, 5, 3, 3}, // the middle: three and three
		{10, 1, 1, 5}, // near the top: the rest below
		{10, 8, 5, 1}, // near the bottom: the rest above
		{4, 1, 1, 2},  // fewer than the budget: all of them
	} {
		a, b := v5Neighbours(tc.n, tc.i)
		if len(a) != tc.above || len(b) != tc.below {
			t.Errorf("n=%d i=%d: %d above, %d below; want %d, %d", tc.n, tc.i, len(a), len(b), tc.above, tc.below)
		}
		for k, x := range a {
			if x != tc.i-len(a)+k {
				t.Errorf("n=%d i=%d: above must be the nearest newer items in order, got %v", tc.n, tc.i, a)
			}
		}
	}
}

// TestV5ItemPagesAreStreams: an item's page is the item, open and marked as
// the page's own, among its folded neighbours, which carry their opening
// lines only; every page has one h1; the home page is the top of the stream,
// never the whole archive; the sitemap lists every page a reader can land on.
func TestV5ItemPagesAreStreams(t *testing.T) {
	dir := renderV5Site(t, "daryl")
	long := strings.Repeat("A sentence that keeps the post going for a while. ", 40)
	for _, d := range []string{"20260301", "20260302", "20260303", "20260304", "20260305", "20260306", "20260307"} {
		writeStreamPost(t, dir, d, "p"+d, "Post "+d, long+"END-OF-"+d)
	}
	r, err := NewPageRenderer(PageConfig{Site: storage.At(dir), BaseURL: "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post", PostsMountDir: "posts",
		CommentsSourceDir: "content/pub.polis.core/comment", CommentsMountDir: "comments", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatal(err)
	}
	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(dir, p)); return string(b) }

	// Ten posts, newest first: 20260325 charlie, 20260320 bravo, 20260315 alpha, then 0307 … 0301.
	page := read("posts/20260315/alpha.html") // index 2: two above, four below
	if n := strings.Count(page, "data-neighbour"); n != 6 {
		t.Errorf("alpha's page carries %d neighbours, want 6", n)
	}
	if strings.Count(page, "data-focus") != 1 || !strings.Contains(page, `data-path="/posts/20260315/alpha.html" data-focus`) {
		t.Error("the page's own item is marked data-focus, once")
	}
	if strings.Index(page, `data-path="/posts/20260325/charlie.html"`) > strings.Index(page, `data-path="/posts/20260315/alpha.html" data-focus`) {
		t.Error("newer neighbours come above the item")
	}
	if strings.Contains(page, "END-OF-20260307") {
		t.Error("a neighbour carried its full words; it must carry its opening lines only")
	}
	if !strings.Contains(read("posts/20260307/p20260307.html"), "END-OF-20260307") {
		t.Error("the item itself, open on its own page, carries its full words")
	}
	for _, p := range []string{"index.html", "posts/20260315/alpha.html", "comments/20260326/on-their-post.html", "about.html"} {
		if n := strings.Count(read(p), "<h1"); n != 1 {
			t.Errorf("%s has %d h1, want one", p, n)
		}
	}
	if n := strings.Count(read("index.html"), `<article class="item post`); n != 7 {
		t.Errorf("the home page carries %d posts, want the top of the stream: 7", n)
	}
	sm := read("sitemap.xml")
	for _, want := range []string{"https://example.com/posts/20260315/alpha.html", "https://example.com/comments/20260326/on-their-post.html", "https://example.com/about"} {
		if !strings.Contains(sm, want) {
			t.Errorf("the sitemap lacks %s", want)
		}
	}
}

// v5FocusCard is the page's own item: its <article data-focus> and nothing
// of its neighbours.
func v5FocusCard(page string) string {
	i := strings.Index(page, "data-focus")
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(page[:i], "<article")
	end := strings.Index(page[i:], "</article>")
	return page[start : i+end]
}

// The owner's own avatar wins over a theme's mark style: a site whose owner
// chose colours is drawn in them, as its favicon is.
func TestV5MarkIsTheOwnersAvatar(t *testing.T) {
	dir := renderV5Site(t, "vice")
	wkPath := filepath.Join(dir, ".well-known", "polis")
	b, err := os.ReadFile(wkPath)
	if err != nil {
		t.Fatal(err)
	}
	var wk map[string]any
	if err := json.Unmarshal(b, &wk); err != nil {
		t.Fatal(err)
	}
	wk["avatar"] = map[string]any{"bg": "#2a9d8f", "fg": "#ffffff"}
	b, _ = json.Marshal(wk)
	if err := os.WriteFile(wkPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewPageRenderer(PageConfig{Site: storage.At(dir), BaseURL: "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post", PostsMountDir: "posts",
		CommentsSourceDir: "content/pub.polis.core/comment", CommentsMountDir: "comments", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(filepath.Join(dir, "posts", "20260315", "alpha.html"))
	if !strings.Contains(string(page), "#2a9d8f") {
		t.Error("the mark is not drawn in the owner's own colour")
	}
	if strings.Contains(string(page), "#d06888") {
		t.Error("the theme's mark colour replaced the owner's own")
	}
}

// Top left is whose site this is: the author's mark and handle, one link to
// about. About does not repeat them; its pinned row carries the name.
func TestV5AuthorGoesHome(t *testing.T) {
	dir := renderV5Site(t, "daryl")
	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(dir, p)); return string(b) }
	for _, p := range []string{"index.html", "posts/20260315/alpha.html", "about.html"} {
		page := read(p)
		i := strings.Index(page, `<a class="author" href="/"`)
		if i < 0 {
			t.Fatalf("%s: no author link home (the site's front page) at the top left", p)
		}
		link := page[i : i+strings.Index(page[i:], "</a>")]
		if !strings.Contains(link, "avatar-initial") || !strings.Contains(link, `<span class="ah">`) {
			t.Errorf("%s: the author link must hold the mark and the handle: %s", p, link)
		}
		if strings.Contains(page, `class="wordmark"`) {
			t.Errorf("%s: polis's wordmark still takes the top left", p)
		}
	}
	about := read("about.html")
	card := about[strings.Index(about, `<article class="item about`):]
	card = card[:strings.Index(card, "</article>")]
	if strings.Contains(card, `class="hd"`) || strings.Contains(card, "avatar-initial") {
		t.Error("about repeats the mark and handle the top bar already shows")
	}
}

// Every item page carries JSON-LD (4.2, SEO rule 1): a comment's is a Comment
// about what it answers, and its <title> says it is a reply. The home page's
// WebSite has a name even when the site has no title.
func TestV5HeadsNameEveryPage(t *testing.T) {
	dir := renderV5Site(t, "daryl")
	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(dir, p)); return string(b) }
	c := read("comments/20260326/on-their-post.html")
	if !strings.Contains(c, `"@type": "Comment"`) {
		t.Error("a comment's page carries no Comment JSON-LD")
	}
	if !strings.Contains(c, "<title>A reply to “") {
		t.Error("a comment's page title must say it is a reply, not borrow the post's title")
	}
	if strings.Contains(read("index.html"), `"name": ""`) {
		t.Error("the WebSite JSON-LD has an empty name")
	}
	idx := read("index.html")
	for _, want := range []string{`aria-label="search posts"`, `aria-label="search comments"`, `aria-label="search sites"`} {
		if !strings.Contains(idx, want) {
			t.Errorf("the toolbar lacks 3.8's label %s", want)
		}
	}
}

// TestV5CommentPageFromItsRegisteredAddress: a blessed copy links straight to
// the comment's own page, never to its source or through a redirect.
func TestV5CommentPageFromItsRegisteredAddress(t *testing.T) {
	for in, want := range map[string]string{
		"https://a.example/content/pub.polis.core/comment/20261116/x.md":   "https://a.example/comments/20261116/x.html",
		"https://a.example/content/pub.polis.core/comment/20261116/x.html": "https://a.example/comments/20261116/x.html",
		"https://a.example/comments/20261116/x.html":                       "https://a.example/comments/20261116/x.html",
	} {
		if got := V5CommentPage(in); got != want {
			t.Errorf("V5CommentPage(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestV5AboutNamesItsKeyAndHowToCheckIt: about's SIGNED WITH fact is the key
// and a link to check it, with no sentence after the key (removed 2026-11-22).
func TestV5AboutNamesItsKeyAndHowToCheckIt(t *testing.T) {
	dir := renderV5Site(t, "vice")
	wkPath := filepath.Join(dir, ".well-known", "polis")
	b, err := os.ReadFile(wkPath)
	if err != nil {
		t.Fatal(err)
	}
	var wk map[string]any
	if err := json.Unmarshal(b, &wk); err != nil {
		t.Fatal(err)
	}
	wk["public_key"] = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabcdefghijklmnopqrstuvwxyz0123456789 test"
	b, _ = json.Marshal(wk)
	if err := os.WriteFile(wkPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := NewPageRenderer(PageConfig{Site: storage.At(dir), BaseURL: "https://example.com",
		PostsSourceDir: "content/pub.polis.core/post", PostsMountDir: "posts",
		CommentsSourceDir: "content/pub.polis.core/comment", CommentsMountDir: "comments", Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RenderAll(true); err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(filepath.Join(dir, "about.html"))
	want := `<code>ed25519 · AAAAC3Nz…456789</code> · <a href="/.well-known/polis">Check it</a>`
	if !strings.Contains(string(page), want) {
		t.Errorf("about's SIGNED WITH fact is not the key and its Check it link; want %q in:\n%s", want, page)
	}
	if strings.Contains(string(page), "signed with it") {
		t.Error("about still says every post and comment is signed with it")
	}
}

// TestV5PagesCarryTheOwnersItemsHidden: home in the menu and edit on a post
// (spec 3.8) are on every page for every reader, hidden by the stylesheet
// unless the visitor nav marks the page pn-owner, so the page names no one.
func TestV5PagesCarryTheOwnersItemsHidden(t *testing.T) {
	dir := renderV5Site(t, "vice")
	page, _ := os.ReadFile(filepath.Join(dir, "posts", "20260315", "alpha.html"))
	for _, want := range []string{
		`<a class="way own-only" href="/_/" data-way="home"`,
		`<a class="own-only edit" href="/_/write#edit:/posts/20260315/alpha.html">edit</a>`,
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("the post page lacks %q", want)
		}
	}
	css, err := os.ReadFile(filepath.Join("..", "bundle", "fixtures", "pub.polis.core", "shapes", "v5", "v5.css"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".own-only { display: none; }", `.pn-owner .way[data-way="following"] { display: none; }`, ".pn-owner .way.own-only { display: inline-flex; }"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("v5.css lacks %q", want)
		}
	}
}
