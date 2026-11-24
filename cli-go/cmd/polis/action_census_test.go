package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ⛔⛔ EVERY SITE ACTION HAS ONE IMPLEMENTATION, AND IT LIVES IN THE ACTION LAYER.
//
// The action layer is cli-go/pkg/ops: each operation is declared there, served
// on /v1, projected into MCP, and (in time) called in-process by the CLI. A
// handler that calls the functions below itself is a second implementation of
// that action by definition, and second implementations drift: one copy of
// publish dropped the author's per-work licence, another ran no hooks, a third
// logged no events. Nothing stopped them, which is why this test exists.
//
// ⚠️ THE LIST BELOW IS WHAT IS LEFT, NOT WHAT IS ALLOWED. Each entry names the
// caller (file and function) and why it still exists. It shrinks as callers
// move onto the action layer, and an entry whose call has gone fails the test,
// so the list cannot outlive what it records. ⛔ Do not add an entry to make
// the build pass: put the action in cli-go/pkg/ops and reach it from there. The
// only lasting entries are the named exceptions: getting signed in and
// enrolling on polis.pub, and the old browser app's routes until it retires.

// actionFunctions are the underlying functions an action's implementation
// calls: package import path suffix → function names.
var actionFunctions = map[string][]string{
	"cli-go/pkg/publish":   {"PublishPost", "PublishPostAs", "RepublishPost", "RepublishPostAs"},
	"cli-go/pkg/comment":   {"SignComment", "BeseechComment"},
	"cli-go/pkg/following": {"FollowWithBlessing", "UnfollowWithDenial", "FollowWithBlessingMarked", "UnfollowWithDenialMarked"},
	"cli-go/pkg/dm":        {"NewSender", "NewSenderWithHTTP"},
	"cli-go/pkg/tag":       {"ApplyTag", "RemoveTarget", "DeleteTag"},
}

// actionLayer is where the calls belong.
const actionLayer = "cli-go/pkg/ops/"

const (
	whyShared = "a handler only /v1 reaches (its /api route went with the old app, 2026-11-22): one implementation, not yet moved into the action layer"
)

// actionCallsLeft is every call outside the action layer, keyed
// "file|function|pkg.Func". The function is the enclosing declaration, so a
// call moving within it does not churn the list.
var actionCallsLeft = map[string]string{
	// The handlers the /v1 DM operations forward to (their /api routes were deleted with the v4 app).
	"webapp/internal/server/handlers.go|(*Server).handleDMSendSealed|dm.NewSenderWithHTTP":       whyShared,
	"webapp/internal/server/handlers.go|(*Server).handleDMRecipientKey|dm.NewSenderWithHTTP":     whyShared + " (a lookup of the recipient's key, not a send)",
	"webapp/internal/server/handlers.go|(*Server).handleDMProtectionStatus|dm.NewSenderWithHTTP": whyShared + " (a lookup of the recipient's protection, not a send)",
}

// actionCall is one call site found.
type actionCall struct{ file, fn, call string }

func (c actionCall) key() string { return c.file + "|" + c.fn + "|" + c.call }

// funcName names a declaration the way the list does: "(*T).m", "T.m" or "f".
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	switch t := fd.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return "(*" + id.Name + ")." + fd.Name.Name
		}
	case *ast.Ident:
		return t.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// scanActionCalls parses one file and returns its calls to actionFunctions.
func scanActionCalls(fset *token.FileSet, path, rel string) ([]actionCall, error) {
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	// The local name each watched package is imported under.
	local := map[string]string{} // local name → package short name
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		for suffix := range actionFunctions {
			if !strings.HasSuffix(p, "/"+suffix) {
				continue
			}
			short := suffix[strings.LastIndex(suffix, "/")+1:]
			name := short
			if imp.Name != nil {
				name = imp.Name.Name
			}
			local[name] = suffix
		}
	}
	if len(local) == 0 {
		return nil, nil
	}
	var out []actionCall
	for _, decl := range f.Decls {
		enclosing := "(package level)"
		if fd, ok := decl.(*ast.FuncDecl); ok {
			enclosing = funcName(fd)
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			suffix, ok := local[id.Name]
			if !ok {
				return true
			}
			for _, fn := range actionFunctions[suffix] {
				if sel.Sel.Name == fn {
					short := suffix[strings.LastIndex(suffix, "/")+1:]
					out = append(out, actionCall{rel, enclosing, short + "." + fn})
				}
			}
			return true
		})
	}
	return out, nil
}

func TestEverySiteActionHasOneImplementation(t *testing.T) {
	fset := token.NewFileSet()
	found := map[string]actionCall{}
	scanned := 0
	for _, root := range []string{
		filepath.Join(repoRoot, "cli-go"),
		filepath.Join(repoRoot, "webapp"),
	} {
		err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				if n := info.Name(); n == "node_modules" || n == "testdata" || strings.HasPrefix(n, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(repoRoot, p)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, actionLayer) {
				return nil // where the calls belong
			}
			for suffix := range actionFunctions {
				if strings.HasPrefix(rel, suffix+"/") {
					return nil // the definitions and their own package
				}
			}
			scanned++
			calls, perr := scanActionCalls(fset, p, rel)
			if perr != nil {
				return fmt.Errorf("parse %s: %w", rel, perr)
			}
			for _, c := range calls {
				found[c.key()] = c
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	// ⛔ Non-vacuity: a scan that reads nothing, or finds nothing, passes.
	if scanned < 300 {
		t.Fatalf("scanned only %d files; repoRoot is probably wrong", scanned)
	}
	if len(found) == 0 {
		t.Fatal("found no call at all; the scan is broken (the old app's routes still call these functions)")
	}

	var keys []string
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := actionCallsLeft[k]; !ok {
			c := found[k]
			t.Errorf("%s calls %s in %s: a second implementation of a site action. Declare the "+
				"action as an operation in %s and reach it from there (/v1 serves it, MCP is "+
				"generated from it). ⛔ Do not add it to actionCallsLeft to make this pass.",
				c.file, c.call, c.fn, actionLayer)
		}
	}
	for k := range actionCallsLeft {
		if _, ok := found[k]; ok {
			continue
		}
		if absentClosedSource(strings.SplitN(k, "|", 2)[0]) {
			continue
		}
		t.Errorf("%s is listed but the call has gone: remove the entry, so the list stays a "+
			"record of what is left", k)
	}
}

// TestActionCensusSeesAliasedImportsAndClosures pins the scanner: an aliased
// import and a call inside a closure are both found, under the enclosing
// declaration, so neither can hide a second implementation.
func TestActionCensusSeesAliasedImportsAndClosures(t *testing.T) {
	dir := t.TempDir()
	src := `package x

import pub "github.com/vdibart/polis-cli/cli-go/pkg/publish"

type S struct{}

func (s *S) handle() {
	go func() { _, _ = pub.PublishPost(nil, "", "", nil) }()
}

func publish() {}
`
	p := filepath.Join(dir, "x.go")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	calls, err := scanActionCalls(token.NewFileSet(), p, "x.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].key() != "x.go|(*S).handle|publish.PublishPost" {
		t.Fatalf("calls = %+v, want one x.go|(*S).handle|publish.PublishPost", calls)
	}
}
