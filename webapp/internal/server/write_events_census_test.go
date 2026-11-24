package server

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/apidesc"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/webapp/internal/api"
)

// ⛔⛔ EVERY WRITE THE API DESCRIBES NAMES THE EVENT IT IS LOGGED UNDER.
//
// A write the API serves is logged once, as a domain event, so an operator
// can count writes by operation and caller. The engine's operations declare
// theirs beside their other effects (ops.DeclaredWriteEvent). The host's
// operations (site settings, agents, messages, follow, blessing, seen) are
// served by handlers, and this table is where each one's event is named. A new
// write operation fails here until its event is named, and a named event must
// be one the code emits: it must be in the generated event catalog.
var hostWriteEvents = map[string]string{
	"pub.polis.site.set_name":       "pub.polis.site.author_name_update",
	"pub.polis.site.set_avatar":     "pub.polis.site.avatar_update",
	"pub.polis.site.set_about":      "pub.polis.site.about_update",
	"pub.polis.site.set_theme":      "pub.polis.site.theme_switch",
	"pub.polis.site.set_tokens":     "pub.polis.site.tokens_.*",
	"pub.polis.site.reset_tokens":   "pub.polis.site.tokens_.*",
	"pub.polis.site.rebuild":        "pub.polis.site.render",
	"pub.polis.site.policy_tighten": "pub.polis.policy.tightened",
	"pub.polis.site.policy_release": "pub.polis.policy.released",
	"pub.polis.site.set_policy":     "pub.polis.policy.set",
	"pub.polis.site.reset_policy":   "pub.polis.policy.reset",
	"pub.polis.site.rotate_key":     "pub.polis.site.key_rotate",
	"pub.polis.site.register":       "pub.polis.site.register",
	"pub.polis.site.unregister":     "pub.polis.site.unregister",
	"pub.polis.license.set":         "pub.polis.license.stated",
	"pub.polis.agent.make_token":    "pub.polis.api_key.minted",
	"pub.polis.agent.revoke_token":  "pub.polis.api_key.revoked",
	"pub.polis.agent.disconnect":    "pub.polis.connection.switched_off",
	"pub.polis.agent.set_rosie":     "pub.polis.agent.grant_issued",
	"pub.polis.dm.send_sealed":      "dm.send_sealed.delivered",
	"pub.polis.dm.set_password":     "pub.polis.dm.password_set",
	"pub.polis.dm.change_password":  "pub.polis.dm.password_change",
	"pub.polis.dm.new_key":          "pub.polis.dm.new_key",
	"pub.polis.dm.rewrap_earlier":   "pub.polis.dm.earlier_rewrapped",
	"pub.polis.follow.create":       "pub.polis.follow.add",
	"pub.polis.follow.delete":       "pub.polis.follow.remove",
	"pub.polis.comment.bless":       "pub.polis.comment.blessing.grant",
	"pub.polis.comment.deny":        "pub.polis.comment.blessing.deny",
	"pub.polis.comment.unbless":     "pub.polis.comment.blessing.revoke",
	"pub.polis.seen.mark":           "pub.polis.seen.marked",
}

var catalogEventRE = regexp.MustCompile("(?m)^\\| `([^`]+)` \\|")

func TestEveryDescribedWriteNamesItsEvent(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", ".internal", "ops", "logging.md"))
	if err != nil {
		t.Skip("the operator docs are not in this tree")
	}
	catalog := map[string]bool{}
	for _, m := range catalogEventRE.FindAllStringSubmatch(string(raw), -1) {
		catalog[m[1]] = true
	}
	if len(catalog) < 100 {
		t.Fatalf("read only %d catalog rows; the generated catalog is missing", len(catalog))
	}

	s := newConfiguredServer(t)
	engine, err := s.newContentEngine()
	if err != nil {
		t.Fatal(err)
	}
	host := map[string]bool{}
	var hostOps []apidesc.Operation
	for _, hr := range s.apiHost().Routes {
		hostOps = append(hostOps, hr.Op)
	}
	hostOps = append(hostOps, api.BlessingOperations()...)
	hostOps = append(hostOps, api.FollowOperations()...)
	hostOps = append(hostOps, api.SeenOperations()...)

	seen := 0
	for _, op := range hostOps {
		if !op.Write {
			continue
		}
		seen++
		name := op.Type + "." + op.Action
		host[name] = true
		ev, ok := hostWriteEvents[name]
		if !ok {
			t.Errorf("%s is a write the API describes and names no event: add it to hostWriteEvents, and log one in its handler", name)
			continue
		}
		if !catalog[ev] {
			t.Errorf("%s names %s, which the code does not emit (it is not in the generated event catalog)", name, ev)
		}
	}
	for _, op := range engine.Operations() {
		if !op.Write {
			continue
		}
		seen++
		name := op.Type + "." + op.Action
		ev, ok := ops.DeclaredWriteEvent(name)
		if !ok {
			t.Errorf("%s is an engine write with no declared event (cli-go/pkg/ops/afterwrite.go, writeEffects)", name)
			continue
		}
		if ev != "" && !catalog[ev] {
			t.Errorf("%s declares %s, which is not in the generated event catalog", name, ev)
		}
	}
	for name := range hostWriteEvents {
		if !host[name] {
			t.Errorf("hostWriteEvents names %s, which is not a write the API describes", name)
		}
	}
	if seen < 30 {
		t.Fatalf("checked only %d write operations; the scan is broken", seen)
	}
}
