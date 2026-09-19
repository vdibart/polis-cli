package ops

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

// candidateActions is every verb any core type has ever advertised or the v1
// API dispatches. Each one is either advertised by Actions() and accepted by
// Handle, or neither.
var candidateActions = []string{
	"list", "get", "create", "update", "delete", "render",
	"draft.list", "draft.get", "draft.save", "draft.delete",
	"bless", "deny", "revoke", "sync",
	"apply", "remove",
	"send", "deliver", "protection_status", "mark_read", "retry",
}

// Actions() is published at /v1/bundles as what a caller may do. It said post
// supported get/update/delete/render and four draft verbs, comment
// list/get/bless/deny/revoke/sync, and follow create/delete — the handler
// refused every one, so each v1 route for them (including all of
// /v1/content/{type}/drafts) answered 400 while the bundle listing invited the
// call (Signet epic 41). This holds the two to one truth in both directions.
func TestActionsAreExactlyWhatTheHandlerAccepts(t *testing.T) {
	engine, _ := newTestEngine(t)
	h := NewBuiltinCoreHandler()

	types := make([]string, 0)
	for name := range bundle.DefaultCoreBundle().Types {
		types = append(types, name)
	}
	sort.Strings(types)

	for _, typ := range types {
		advertised := map[string]bool{}
		for _, a := range h.Actions(typ) {
			advertised[a] = true
		}
		for _, action := range candidateActions {
			_, err := engine.Dispatch(context.Background(), ActionRequest{
				Action:      action,
				ContentType: typ,
				Payload:     map[string]any{},
			})
			refused := err != nil && strings.Contains(err.Error(), "unsupported")
			switch {
			case advertised[action] && refused:
				t.Errorf("%s advertises %q but the handler refuses it: %v", typ, action, err)
			case !advertised[action] && !refused:
				t.Errorf("%s accepts %q but Actions() does not advertise it (err=%v)", typ, action, err)
			}
		}
	}
}

// A declared type the handler has no operations for is a caller asking for an
// unsupported action, not a server fault: attestation and actor dispatched to
// "unsupported content type", which the v1 API mapped to 500.
func TestDeclaredTypeWithoutOperationsIsAnUnsupportedAction(t *testing.T) {
	engine, _ := newTestEngine(t)
	for _, typ := range []string{"pub.polis.attestation", "pub.polis.actor"} {
		_, err := engine.Dispatch(context.Background(), ActionRequest{Action: "list", ContentType: typ})
		if err == nil || !strings.Contains(err.Error(), "unsupported action") {
			t.Errorf("%s list: err = %v, want an unsupported action error", typ, err)
		}
	}
}

// GET /v1/content/theme/{name} passes the path segment as "id", like every
// other get; the theme handler read only "name" and always answered 400.
func TestDispatchThemeGetByID(t *testing.T) {
	engine, _ := newTestEngine(t)
	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "get",
		ContentType: "pub.polis.theme",
		Payload:     map[string]any{"id": "vice"},
	})
	if err != nil {
		t.Fatalf("dispatch theme/get by id: %v", err)
	}
	if result.Data["name"] != "vice" {
		t.Errorf("name = %v, want vice", result.Data["name"])
	}
}
