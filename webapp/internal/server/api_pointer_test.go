package server

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// ⭐ The api pointer is dark until switched on: off, a remote site publishes
// none; on, it names the description and the instance's /mcp; switched off
// again, it is withdrawn. A site with no public API never publishes one.
func TestAPIPointerFollowsTheSwitch(t *testing.T) {
	s := newConfiguredServer(t)
	s.Mode = mode.RemoteServerSigned
	s.EnsureAPIPointer()
	if p := site.APIPointerOf(s.Site()); p != nil {
		t.Fatalf("switch off: pointer %+v published", p)
	}
	s.APIPointerEnabled = true
	s.EnsureAPIPointer()
	p := site.APIPointerOf(s.Site())
	base := strings.TrimRight(s.GetBaseURL(), "/")
	if p == nil || p.Description != base+"/v1/openapi.json" || p.Version != "v1" || p.MCP != base+"/mcp" {
		t.Fatalf("switch on: pointer %+v, want description, version and %s/mcp", p, base)
	}
	s.APIPointerEnabled = false
	s.EnsureAPIPointer()
	if p := site.APIPointerOf(s.Site()); p != nil {
		t.Fatalf("switched off again: pointer %+v still published", p)
	}
	s.Mode = mode.Local
	s.APIPointerEnabled = true
	s.EnsureAPIPointer()
	if p := site.APIPointerOf(s.Site()); p != nil {
		t.Fatalf("local: pointer %+v published for a site with no public API", p)
	}
}
