package server

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// TestTheV5ShellVersionsEverySameOriginFile: every script and stylesheet the
// v5 app's page loads from the site itself carries ?v=<the file's content
// hash>. The site answers those files as immutable for a year, so an address
// that did not change with the bytes would keep a browser on its old copy
// for ever, a fix to the messages key's cryptography included.
func TestTheV5ShellVersionsEverySameOriginFile(t *testing.T) {
	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		t.Fatal(err)
	}
	page, err := V5OwnerShell(webFS, "n")
	if err != nil {
		t.Fatal(err)
	}
	out := string(page)
	if strings.Contains(out, "{{") {
		t.Fatalf("the shell still holds an unexpanded placeholder:\n%s", out)
	}

	refs := regexp.MustCompile(`<(?:script[^>]*\ssrc|link[^>]*rel="stylesheet"[^>]*\shref|meta name="polis-editor"[^>]*\scontent)="([^"]+)"`).FindAllStringSubmatch(out, -1)
	if len(refs) == 0 {
		t.Fatal("found no scripts or stylesheets in the shell")
	}
	seen := 0
	for _, m := range refs {
		ref := m[1]
		if !strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "//") {
			continue // another origin's (fonts): not ours to version
		}
		seen++
		path, v, ok := strings.Cut(ref, "?v=")
		if !ok || v == "" {
			t.Errorf("%s carries no version", ref)
			continue
		}
		file := strings.TrimPrefix(path, "/")
		if rest, isApp := strings.CutPrefix(path, V5OwnerPrefix); isApp {
			file = V5OwnerDir + "/" + rest
		}
		if want := fileVersion(webFS, file); v != want {
			t.Errorf("%s: version %q, want the hash of %s's bytes %q", ref, v, file, want)
		}
	}
	for _, f := range []string{"dm.js", "dm-crypto.js", "dm-session.js", "vendor/tweetnacl.js", "vendor/hash-wasm-argon2.js", "vendor/bip39.js", "vendor/milkdown-kit-7.23.0.js"} {
		if !strings.Contains(out, `"/`+f+`?v=`) {
			t.Errorf("the shell does not load /%s with a version", f)
		}
	}
	if seen < 6 {
		t.Errorf("checked %d same-origin files; the shell loads at least the six of the messages key", seen)
	}
}

// TestASiteFilesVersionFollowsItsBytes: a changed file gets a new address.
func TestASiteFilesVersionFollowsItsBytes(t *testing.T) {
	shell := `<script src="{{site:dm.js}}"></script>`
	a := fstest.MapFS{"v5/index.html": {Data: []byte(shell)}, "dm.js": {Data: []byte("one")}}
	b := fstest.MapFS{"v5/index.html": {Data: []byte(shell)}, "dm.js": {Data: []byte("two")}}
	pa, _ := V5OwnerShell(a, "n")
	pb, _ := V5OwnerShell(b, "n")
	if string(pa) == string(pb) || !strings.Contains(string(pa), "/dm.js?v=") {
		t.Fatalf("a changed dm.js kept its address:\n%s\n%s", pa, pb)
	}
}
