package server

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// TestSPA_EveryAppReferenceIsDefined fails when an SPA script or index.html
// uses an App.* member that nothing defines, or app.js calls a this.* method
// that nothing defines.
//
// The v3 hard cutover (0f846687) deleted App.grantBlessing, denyBlessing,
// unpublishComment and extractTitleFromMarkdown while owner-extras.js kept
// calling them behind `typeof … === 'function'` guards. The guards turned four
// broken features into console warnings: Bless, Deny and Unpublish on a comment
// did nothing, and nothing failed. deleteDraft and unpublishPost had the same
// break and were only found by hand.
//
// "Defined" means a top-level key of the `const App = {…}` literal in app.js
// (method or property), or an assignment `App.x =` / `this.x =` in any SPA
// script. Line comments are ignored, so prose that names a retired method does
// not count as a use.
func TestSPA_EveryAppReferenceIsDefined(t *testing.T) {
	appJS, err := fs.ReadFile(webui.Assets, "www/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	defined := appLiteralKeys(t, string(appJS))

	sources := map[string]string{}
	entries, err := fs.ReadDir(webui.Assets, "www")
	if err != nil {
		t.Fatalf("read www: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !(strings.HasSuffix(name, ".js") || name == "index.html") {
			continue
		}
		data, err := fs.ReadFile(webui.Assets, "www/"+name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		sources[name] = stripLineComments(string(data))
	}

	assignRe := regexp.MustCompile(`\b(?:App|this)\.([A-Za-z_$][\w$]*)\s*=[^=]`)
	for _, src := range sources {
		for _, m := range assignRe.FindAllStringSubmatch(src, -1) {
			defined[m[1]] = true
		}
	}

	useRe := regexp.MustCompile(`\bApp\.([A-Za-z_$][\w$]*)`)
	missing := map[string][]string{}
	for name, src := range sources {
		for _, m := range useRe.FindAllStringSubmatch(src, -1) {
			if !defined[m[1]] {
				missing[m[1]] = appendUnique(missing[m[1]], name)
			}
		}
	}
	// Inside the literal, a method reaches its siblings through `this.`: five
	// such calls outlived their methods in the same cutover (_saveFeedState
	// threw on every navigation away from the stream).
	thisCallRe := regexp.MustCompile(`\bthis\.([A-Za-z_$][\w$]*)\s*\(`)
	for _, m := range thisCallRe.FindAllStringSubmatch(sources["app.js"], -1) {
		if !defined[m[1]] {
			missing[m[1]] = appendUnique(missing[m[1]], "app.js (this.)")
		}
	}

	if len(missing) > 0 {
		var lines []string
		for member, files := range missing {
			lines = append(lines, "App."+member+" used in "+strings.Join(files, ", "))
		}
		sort.Strings(lines)
		t.Errorf("SPA scripts use App members nothing defines:\n  %s", strings.Join(lines, "\n  "))
	}

	// Guard the scanner itself: if it stops finding the literal's keys, every
	// use would read as missing, and if it finds nothing to check the test
	// passes vacuously.
	for _, known := range []string{"api", "showToast", "grantBlessing", "denyBlessing", "unpublishComment"} {
		if !defined[known] {
			t.Errorf("scanner did not find App.%s defined", known)
		}
	}
	if !useRe.MatchString(sources["owner-extras.js"]) {
		t.Error("scanner found no App.* uses in owner-extras.js")
	}
}

// appLiteralKeys returns the top-level keys of `const App = {` in app.js:
// lines at the literal's four-space member indent that open a method
// (`name(`, `async name(`) or a property (`name:`).
func appLiteralKeys(t *testing.T, src string) map[string]bool {
	t.Helper()
	start := strings.Index(src, "const App = {")
	end := -1
	if start != -1 {
		if i := strings.Index(src[start:], "\n};"); i != -1 {
			end = start + i
		}
	}
	if start == -1 || end == -1 {
		t.Fatal("could not locate the `const App = {…};` literal in app.js")
	}
	keyRe := regexp.MustCompile(`(?m)^    (?:async\s+)?([A-Za-z_$][\w$]*)\s*(?:\(|:)`)
	keys := map[string]bool{}
	for _, m := range keyRe.FindAllStringSubmatch(src[start:end], -1) {
		keys[m[1]] = true
	}
	return keys
}

// stripLineComments drops `//` comments that start a line (after indentation).
// Trailing comments are left alone: telling one from `//` inside a string or a
// URL needs a tokenizer, and a false use there only makes the test stricter.
func stripLineComments(src string) string {
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "//") {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}
