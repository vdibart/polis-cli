package url

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestNoCommentURLConstructionOutsidePolisurl is WS5 guard ③ (comment-infra
// remediation, plan plans/comment-registration-severe-bug.md): the URL-derivation
// lint. It FAILS if any Go code OUTSIDE this package (cli-go/pkg/url) *constructs*
// a comment URL from parts — either via fmt.Sprintf with the comment-path literal
// in the format string, or via string concatenation against that literal.
//
// Why: Defect 1 (beseech.go) and Defect 5 (chaplain.go) BOTH built the registered
// comment URL as fmt.Sprintf("%s/comments/%s/%s.md", …) — the mount path, not the
// canonical content/ source path — and drifted from how posts derive their URL.
// The fix funnels ALL comment-URL construction through CommentContentURL /
// CommentURLToContentRel here. This lint makes that funnel load-bearing: a future
// hand-built comment URL fails the build instead of silently re-registering the
// wrong scheme for another ~10 weeks.
//
// PARSING is explicitly allowed everywhere — strings.Replace/Index/Contains/
// HasPrefix/TrimPrefix(u, "/comments/", …) take the literal as a CALL ARGUMENT,
// not a Sprintf format string or a `+` operand, so they don't trip the AST checks
// below. Only CONSTRUCTION is forbidden.
func TestNoCommentURLConstructionOutsidePolisurl(t *testing.T) {
	root := repoRoot(t)
	// The one package allowed to know the comment-URL shape (this package).
	allowedDir := filepath.Join(root, "cli-go", "pkg", "url")

	var violations []string
	for _, moduleTree := range []string{filepath.Join(root, "cli-go"), filepath.Join(root, "webapp")} {
		err := filepath.WalkDir(moduleTree, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				base := d.Name()
				if base == "vendor" || base == "testdata" || base == "fixtures" || base == ".git" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if strings.HasPrefix(path, allowedDir+string(os.PathSeparator)) {
				return nil // polisurl is the sanctioned construction home
			}
			violations = append(violations, scanFileForConstruction(t, path)...)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", moduleTree, err)
		}
	}

	if len(violations) > 0 {
		t.Errorf("comment-URL construction found outside cli-go/pkg/url (WS5 guard ③).\n"+
			"Build comment URLs via polisurl.CommentContentURL / parse via CommentURLToContentRel instead.\n%s",
			strings.Join(violations, "\n"))
	}
}

// TestIsMountCommentFileLiteral pins the matcher: it must catch the actual
// Defect-1/5 construction forms and stay clear of the legitimate literals the
// codebase actually contains (canonical paths, DS endpoints, log strings).
func TestIsMountCommentFileLiteral(t *testing.T) {
	// Note: literals here are the raw token values (with surrounding quotes),
	// matching what go/ast yields — Contains is quote-agnostic so it's fine.
	bad := []string{
		`"%s/comments/%s/%s.md"`,   // Defect 1 (beseech.go) + Defect 5 (chaplain.go)
		`"%s/comments/%s/%s.html"`, // mount .html variant
		`"/comments/" + "x.md"`,    // (single literal form) concat-ish
	}
	good := []string{
		`"/content/pub.polis.core/comment/blessed.json"`,           // canonical index (clone.go)
		`"content/pub.polis.core/comment/"`,                        // canonical local path (publish.go)
		`"/v1/content/comments/counts"`,                            // DS endpoint (client.go)
		`"/v1/content/comments/latest"`,                            // DS endpoint (client.go)
		`"Moved %d comment(s) to content/pub.polis.core/comment/"`, // log msg (tailor)
		`"/comments/"`, // bare search literal (strings.Index parsing) — no file marker
		`".html"`,      // WS2 display-suffix concat operand
	}
	for _, v := range bad {
		if !isMountCommentFileLiteral(v) {
			t.Errorf("expected MATCH (bad construction): %s", v)
		}
	}
	for _, v := range good {
		if isMountCommentFileLiteral(v) {
			t.Errorf("expected NO match (legitimate literal): %s", v)
		}
	}
}

// isMountCommentFileLiteral matches a string literal that is (part of) a MOUNT
// comment-FILE path being built — the precise Defect-1/5 signature. It requires
// the `/comments/` mount segment AND a file/format marker (`.md`, `.html`, or a
// `%` format verb). This deliberately does NOT match:
//   - the canonical local path `content/pub.polis.core/comment/` (singular
//     `/comment/`, no `/comments/`) — that's the CORRECT path, used everywhere;
//   - DS endpoint paths like `/v1/content/comments/counts` (no file/format marker);
//   - mount→canonical transform literals or `strings.Index(u, "/comments/")`
//     parsing (the bare `/comments/` search literal lacks a file marker).
func isMountCommentFileLiteral(v string) bool {
	if !strings.Contains(v, "/comments/") {
		return false
	}
	return strings.Contains(v, ".md") || strings.Contains(v, ".html") || strings.Contains(v, "%")
}

// scanFileForConstruction parses one Go file and reports lines that CONSTRUCT a
// mount comment-file URL: a fmt.Sprintf whose format literal matches, or a `+`
// concatenation with a matching string-literal operand.
func scanFileForConstruction(t *testing.T, path string) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	litMatches := func(n ast.Expr) bool {
		bl, ok := n.(*ast.BasicLit)
		return ok && bl.Kind == token.STRING && isMountCommentFileLiteral(bl.Value)
	}

	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.CallExpr:
			// fmt.Sprintf("…/comments/…%s.md", …) — the exact Defect-1/5 form.
			if sel, ok := e.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Sprintf" {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "fmt" && len(e.Args) > 0 {
					if litMatches(e.Args[0]) {
						out = append(out, location(fset, e.Pos(), path, "fmt.Sprintf builds a mount comment-file URL"))
					}
				}
			}
		case *ast.BinaryExpr:
			// concatenation producing a mount comment-file path
			if e.Op == token.ADD && (litMatches(e.X) || litMatches(e.Y)) {
				out = append(out, location(fset, e.Pos(), path, "string concat builds a mount comment-file URL"))
			}
		}
		return true
	})
	return out
}

func location(fset *token.FileSet, pos token.Pos, path, why string) string {
	p := fset.Position(pos)
	return "  " + path + ":" + strconv.Itoa(p.Line) + " — " + why
}

// repoRoot walks up from the test's working directory until it finds the polis
// repo root (the directory containing both cli-go/ and webapp/).
func repoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if isDir(filepath.Join(dir, "cli-go")) && isDir(filepath.Join(dir, "webapp")) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skip("repo root (dir with cli-go/ + webapp/) not found — skipping cross-module lint")
		}
		dir = parent
	}
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
