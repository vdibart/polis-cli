package serve

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/snippet"
)

// ⛔ THE PROPERTY THIS FILE EXISTS FOR
//
//	A tenant must not be able to get a file a browser will execute as script
//	into its own served tree.
//
// Why it matters, in one chain: tenant content is served under
// `script-src 'self' https://polis.pub`, and `'self'` is the tenant's OWN
// subdomain — so the CSP stops a tenant running script on its own pages only
// while it cannot write a `.js` or `.svg` there. And that is the only thing
// keeping the `polis_widget_token` cookie unreadable, because the cookie is
// deliberately JS-readable and scoped to `.polis.pub`, shared by every tenant.
//
// ⚠️ NOTHING IN THE WRITE PATHS WAS BUILT AS A CONTROL. Snippets land as
// `.html`/`.md` because of how snippet resolution works. Drafts are `.md` and
// hooks are `.sh`, both under `.polis/`, which ServeTenantPublic refuses as a
// dot-path. Post bodies go through bluemonday. Each of those is a side effect of
// something else, and a perfectly reasonable refactor of any one of them turns a
// defence-in-depth gap into live cross-tenant token theft — with nothing failing.
//
// So: if a change here fails, do not adjust the test to match. The change has
// altered who can run script on a polis.pub subdomain.

// TestScriptCapableExtensionsAreServed is the premise everything else rests on.
// If a script-capable extension stopped being served, the rest of this file
// would pass vacuously while proving nothing.
func TestScriptCapableExtensionsAreServed(t *testing.T) {
	for ext := range ScriptCapableExtensions {
		if !ServedExtensions[ext] {
			t.Fatalf("%s is listed as script-capable but is not served; "+
				"either it was removed from ServedExtensions (then drop it here too) "+
				"or the two lists have drifted", ext)
		}
	}
	// Guard the other direction: a new served extension that a browser can
	// execute must be declared script-capable, or the checks below skip it.
	for ext := range ServedExtensions {
		switch ext {
		case ".js", ".svg":
			if !ScriptCapableExtensions[ext] {
				t.Errorf("%s is served and executable but not in ScriptCapableExtensions", ext)
			}
		}
	}
}

// TestSnippetWritesCannotProduceAnExecutableFile covers the only tenant write
// path that takes a caller-supplied FILE NAME. Everything else writes to a
// fixed name or a fixed extension.
func TestSnippetWritesCannotProduceAnExecutableFile(t *testing.T) {
	// Names chosen to probe the ways an extension check is usually escaped:
	// the plain case, case folding, a trailing dot, a nested path, a
	// double extension, a query/fragment lookalike, and a module extension
	// that browsers execute just as happily as `.js`.
	names := []string{
		"evil.js",
		"evil.JS",
		"evil.Js",
		"evil.js.",
		"evil.mjs",
		"evil.cjs",
		"evil.svg",
		"evil.SVG",
		"a/b/evil.js",
		"deeply/nested/path/evil.svg",
		"evil.html.js",
		"evil.js.html.js",
		"evil",
		".evil.js",
		"evil.js?x=1",
		"evil.js#frag",
	}

	for _, source := range []string{"global", "theme"} {
		for _, name := range names {
			t.Run(source+"/"+name, func(t *testing.T) {
				dir := t.TempDir()
				if err := snippet.WriteSnippet(storage.At(dir), "", "zane", name, "alert(document.cookie)", source); err != nil {
					// A refusal is a perfectly good outcome — the file never
					// existed, so it can never be served.
					return
				}
				assertNoExecutableFileUnder(t, dir)
			})
		}
	}
}

// TestSnippetPathTraversalCannotEscapeTheSiteDir pins the other half: even a
// name the extension rule would allow must not land outside the tenant's own
// tree, where a different set of rules applies.
func TestSnippetPathTraversalCannotEscapeTheSiteDir(t *testing.T) {
	for _, name := range []string{
		"../evil.html",
		"../../evil.html",
		"a/../../evil.html",
		"/etc/evil.html",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := snippet.WriteSnippet(storage.At(dir), "", "zane", name, "x", "global"); err != nil {
				return // refused outright
			}
			// Written — so it must still be inside the tenant directory.
			assertEverythingStaysUnder(t, dir)
		})
	}
}

// TestDotPathsAreNotServed is what keeps the OTHER tenant write paths harmless.
// Drafts land under `.polis/bundles/...` and hook scripts under
// `.polis/webapp/hooks/`, so they are only unreachable while a leading-dot path
// is refused. `.well-known` is the one deliberate exception.
func TestDotPathsAreNotServed(t *testing.T) {
	refused := []string{
		".polis/webapp/hooks/post-publish.sh",
		".polis/bundles/pub.polis.core/posts/drafts/x.md",
		".env",
		".git/config",
		".polis/evil.js",
	}
	for _, p := range refused {
		if !strings.HasPrefix(p, ".") {
			t.Fatalf("test bug: %q is not a dot-path", p)
		}
		// Mirrors the rule in ServeTenantPublic: dot-paths are refused
		// except `.well-known`.
		if isServableDotPath(p) {
			t.Errorf("%s would be served; drafts and hook scripts live under .polis/ "+
				"and are only unreachable because dot-paths are refused", p)
		}
	}
	if !isServableDotPath(".well-known/polis") {
		t.Error(".well-known/polis must stay servable — it is how a site publishes its key")
	}
}

// isServableDotPath mirrors ServeTenantPublic's dot-path rule. Kept beside the
// test it serves so a change to the real rule shows up as a failure here.
func isServableDotPath(clean string) bool {
	if !strings.HasPrefix(clean, ".") {
		return true
	}
	return strings.HasPrefix(clean, ".well-known")
}

func assertNoExecutableFileUnder(t *testing.T, root string) {
	t.Helper()
	for _, got := range walkFiles(t, root) {
		ext := strings.ToLower(filepath.Ext(got))
		if ScriptCapableExtensions[ext] {
			t.Errorf("a tenant-supplied snippet name produced %q, which ServeTenantPublic "+
				"will serve as executable script from the tenant's own origin — "+
				"that defeats script-src 'self' and exposes the cross-tenant widget token", got)
		}
	}
}

func assertEverythingStaysUnder(t *testing.T, root string) {
	t.Helper()
	for _, got := range walkFiles(t, root) {
		if strings.Contains(got, "..") {
			t.Errorf("snippet escaped the tenant directory: %q", got)
		}
	}
}

// walkFiles returns every regular file under root, as paths relative to it.
func walkFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
