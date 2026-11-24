package site

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// APIPointer is the `api` member of .well-known/polis: where this site's API
// describes itself, which version, and its MCP endpoint where it has one.
//
// ⭐ DISCOVERY BY POINTER, NEVER BY CONVENTION. A consumer starting from a
// site's address reads this and nothing else, so a self-hosted site is found
// exactly as a hosted one is. Absent when the site has no API an outsider can
// reach (a laptop's static site), which is a defined state.
//
// ⚠️ Unmodelled on WellKnown on purpose: it rides Extra, which every writer
// of the document preserves (TestEveryWellKnownWriterPreservesEveryMember,
// whose fixture carries it).
type APIPointer struct {
	Description string `json:"description"`
	Version     string `json:"version"`
	MCP         string `json:"mcp,omitempty"`
}

const apiPointerKey = "api"

// APIPointerOf returns the site's api pointer, or nil when it has none.
func APIPointerOf(site storage.Site) *APIPointer {
	raw, err := LoadWellKnownRaw(site)
	if err != nil || raw == nil {
		return nil
	}
	m, ok := raw[apiPointerKey].(map[string]interface{})
	if !ok {
		return nil
	}
	p := &APIPointer{}
	p.Description, _ = m["description"].(string)
	p.Version, _ = m["version"].(string)
	p.MCP, _ = m["mcp"].(string)
	return p
}

// SetAPIPointer writes the api pointer, or removes it when p is nil, and
// reports whether it changed anything.
//
// ⚠️ .well-known/polis is tracked by Patrol (mtime) and Judge (hash), so this
// writes only when the value differs from what is there.
func SetAPIPointer(site storage.Site, p *APIPointer) (bool, error) {
	cur := APIPointerOf(site)
	if (cur == nil && p == nil) || (cur != nil && p != nil && *cur == *p) {
		return false, nil
	}
	raw, err := LoadWellKnownRaw(site)
	if err != nil {
		return false, err
	}
	if raw == nil {
		return false, errNoWellKnown(site, "")
	}
	if p == nil {
		delete(raw, apiPointerKey)
	} else {
		m := map[string]interface{}{"description": p.Description, "version": p.Version}
		if p.MCP != "" {
			m["mcp"] = p.MCP
		}
		raw[apiPointerKey] = m
	}
	return true, SaveWellKnownRaw(site, raw)
}
