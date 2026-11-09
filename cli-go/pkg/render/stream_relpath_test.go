package render

import (
	"path/filepath"
	"testing"
)

// streamRelativePath is key arithmetic now, with no filesystem. It must give
// exactly the href the path form gave (filepath.Rel from the page's directory
// to the target, both under one root), or every stream page's stylesheet link
// moves.
func TestStreamRelativePathMatchesThePathForm(t *testing.T) {
	cases := []struct{ out, target, want string }{
		{"index.html", "styles.css", "styles.css"},
		{"index.html", "base.css", "base.css"},
		{"posts/20260101/slug.html", "styles.css", "../../styles.css"},
		{"posts/slug.html", "base.css", "../base.css"},
		{"posts/20260101/slug.html", "posts/x.css", "../x.css"},
		{"posts/20260101/slug.html", "posts/20260101/x.css", "x.css"},
		{"a/b/c/d.html", "a/x/y.css", "../../x/y.css"},
	}
	const root = "/srv/site"
	for _, c := range cases {
		got := streamRelativePath(c.out, c.target)
		if got != c.want {
			t.Errorf("streamRelativePath(%q, %q) = %q, want %q", c.out, c.target, got, c.want)
		}
		old, err := filepath.Rel(filepath.Dir(filepath.Join(root, c.out)), filepath.Join(root, c.target))
		if err != nil {
			t.Fatal(err)
		}
		if filepath.ToSlash(old) != got {
			t.Errorf("%q → %q: key form %q, path form %q; they must agree", c.out, c.target, got, old)
		}
	}
}
