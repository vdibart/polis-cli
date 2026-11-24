package apidesc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

var sample = []Operation{
	{Type: "pub.polis.post", Action: "list", Method: "GET", Path: "/v1/content/post", Summary: "s", Description: "d", Auth: AuthPublic},
	{Type: "pub.polis.post", Action: "create", Method: "POST", Path: "/v1/content/post", Summary: "s", Description: "d",
		Input: Object(map[string]any{"markdown": String("x")}, "markdown"), Auth: AuthOwner, Scope: ScopeFull, Write: true, Signs: true},
	{Type: "pub.polis.dm", Action: "deliver", Method: "POST", Path: "/v1/content/dm/actions/deliver", Summary: "s", Description: "d",
		Auth: AuthSiteSigned, Write: true},
	{Type: "pub.polis.dm", Action: "get", Method: "GET", Path: "/v1/content/dm/{id}", Summary: "s", Description: "d",
		PathParams: map[string]string{"id": "x"}, Auth: AuthOwner, Scope: ScopeFull, OwnerOnly: true},
}

func describeFor(t *testing.T, m mode.Mode) map[string]any {
	t.Helper()
	doc := Describe(sample, Manual(), capability.For(m), Options{SiteURL: "https://alice.example/"})
	// It must survive a round trip as JSON, which is how it is served.
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("%s: marshal: %v", m, err)
	}
	var back map[string]any
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("%s: unmarshal: %v", m, err)
	}
	return back
}

func ids(doc map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, v := range doc["x-polis-operations"].([]any) {
		out[v.(string)] = true
	}
	return out
}

func manualIDs(doc map[string]any) map[string]bool {
	out := map[string]bool{}
	if doc["x-polis-manual"] == nil {
		return out
	}
	for _, v := range doc["x-polis-manual"].([]any) {
		out[v.(map[string]any)["id"].(string)] = true
	}
	return out
}

// ⭐ One description per site: each of the four modes lists only what a site
// in that mode can do.
func TestDescriptionListsOnlyWhatTheSiteCanDo(t *testing.T) {
	for _, tc := range []struct {
		m          mode.Mode
		signs      bool // post.create present
		siteToSite bool // dm.deliver present
		account    bool // account.email (an operator's) present
		ownSignIn  bool // signin.methods (an owner's own instance) present
	}{
		{mode.Local, true, false, false, false},
		{mode.RemoteServerSigned, true, true, false, true},
		{mode.RemoteClientSigned, false, true, false, true},
		{mode.Custodied, true, true, true, false},
	} {
		doc := describeFor(t, tc.m)
		got := ids(doc)
		if !got["post.list"] || !got["dm.get"] {
			t.Errorf("%s: public and owner reads missing: %v", tc.m, got)
		}
		if got["post.create"] != tc.signs {
			t.Errorf("%s: post.create present = %v, want %v (an operation that signs needs an instance holding the key)", tc.m, got["post.create"], tc.signs)
		}
		if got["dm.deliver"] != tc.siteToSite {
			t.Errorf("%s: dm.deliver present = %v, want %v (other sites can reach only a public API)", tc.m, got["dm.deliver"], tc.siteToSite)
		}
		man := manualIDs(doc)
		if man["account.email"] != tc.account {
			t.Errorf("%s: account.email present = %v, want %v (account operations need an operator)", tc.m, man["account.email"], tc.account)
		}
		if man["signin.methods"] != tc.ownSignIn {
			t.Errorf("%s: signin.methods present = %v, want %v", tc.m, man["signin.methods"], tc.ownSignIn)
		}
		if !man["key.rotate"] || !man["license.change"] {
			t.Errorf("%s: owner-only entries every site has are missing: %v", tc.m, man)
		}
		if doc["openapi"] != "3.1.0" || doc["info"].(map[string]any)["x-polis-preview"] != true {
			t.Errorf("%s: not an OpenAPI 3.1 preview document", tc.m)
		}
	}
}

// ⭐ While the API is a preview, the description says what that promises: a
// change may come, it is in the changelog, and none is silent. A reader of
// /v1/openapi.json learns it without the developer reference.
func TestAPreviewDescriptionStatesItsPromise(t *testing.T) {
	info := describeFor(t, mode.Custodied)["info"].(map[string]any)
	desc, _ := info["description"].(string)
	if info["x-polis-preview"] != Preview {
		t.Fatalf("x-polis-preview = %v, want %v", info["x-polis-preview"], Preview)
	}
	if Preview != strings.Contains(desc, PreviewPromise) {
		t.Errorf("preview %v, but the description says %q", Preview, desc)
	}
	for _, want := range []string{"changelog", "nothing changes silently", "v2"} {
		if !strings.Contains(PreviewPromise, want) {
			t.Errorf("the promise does not say %q: %s", want, PreviewPromise)
		}
	}
	if strings.Contains(PreviewPromise, "launch") {
		t.Errorf("the promise still ties the preview to a launch: %s", PreviewPromise)
	}
}

// ⛔ Every write is marked as a write and carries a credential; a public
// operation needs none. A client asks the owner before a write from these
// marks, so a mismarked one would publish without asking (Z12, M2).
func TestEveryOperationIsMarked(t *testing.T) {
	doc := describeFor(t, mode.Custodied)
	for path, item := range doc["paths"].(map[string]any) {
		for method, raw := range item.(map[string]any) {
			op := raw.(map[string]any)
			sec := op["security"].([]any)
			switch op["x-polis-auth"] {
			case "public":
				if len(sec) != 0 || op["x-polis-write"] == true {
					t.Errorf("%s %s: a public operation must need no credential and change nothing", method, path)
				}
			case "owner":
				if len(sec) != 1 || op["x-polis-scope"] == nil {
					t.Errorf("%s %s: an owner operation must name its scope", method, path)
				}
			case "public-or-owner":
				if len(sec) != 2 || op["x-polis-write"] == true {
					t.Errorf("%s %s: public-or-owner offers both and changes nothing", method, path)
				}
			case "site-signed":
				if len(sec) != 1 {
					t.Errorf("%s %s: a site-signed operation must name its scheme", method, path)
				}
			default:
				t.Errorf("%s %s: no auth mark", method, path)
			}
			if _, ok := op["x-polis-write"].(bool); !ok {
				t.Errorf("%s %s: not marked as a write or a read", method, path)
			}
		}
	}
}

// ⛔ An owner-only operation is outside every agent scope: it is served only
// to the owner's full-access token.
func TestOwnerOnlyOperationsNeedTheOwnersFullToken(t *testing.T) {
	for _, op := range sample {
		if op.OwnerOnly && (op.Auth != AuthOwner || op.Scope != ScopeFull) {
			t.Errorf("%s is owner-only but reachable with %s/%s", op.ID(), op.Auth, op.Scope)
		}
	}
}

// ⛔ A sign-in-only operation is owner-only first: no agent scope reaches it,
// and then no token at all.
func TestSignInOnlyOperationsAreOwnerOnly(t *testing.T) {
	op := Operation{Type: "pub.polis.site", Action: "download", Method: "GET", Path: "/v1/site/download",
		Auth: AuthOwner, Scope: ScopeFull, OwnerOnly: true, SignInOnly: true}
	obj := operationObject(op)
	if obj["x-polis-sign-in-only"] != true || obj["x-polis-owner-only"] != true {
		t.Fatalf("a sign-in-only operation is described as owner-only and sign-in-only: %v", obj)
	}
	if _, ok := operationObject(Operation{Type: "pub.polis.post", Action: "create"})["x-polis-sign-in-only"]; ok {
		t.Error("only a sign-in-only operation carries the mark")
	}
}

// Each manual entry says why it is the owner's alone and how to do it, with a
// link into the owner's app on this site.
func TestManualEntriesExplainAndLink(t *testing.T) {
	doc := describeFor(t, mode.Custodied)
	for _, raw := range doc["x-polis-manual"].([]any) {
		m := raw.(map[string]any)
		if m["why"] == "" || len(m["steps"].([]any)) == 0 {
			t.Errorf("%s: an entry needs a reason and steps", m["id"])
		}
		if !strings.HasPrefix(m["link"].(string), "https://alice.example/_/") {
			t.Errorf("%s: link %q is not into the owner's app on this site", m["id"], m["link"])
		}
	}
}
