package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
)

// The catch-up record (epic 11 D13).
//
// When Rosie becomes live for a user she makes ONE pass over blessing requests
// still pending, because the sync cursor already passed over requests that
// arrived while no grant stood. The record says which grant that pass ran
// under, so it runs once per grant and never again.
//
// ⚠️ PRIVATE STATE, AND SAFELY DELETABLE. Losing it re-runs one pass, which
// decides nothing twice: a request already decided is no longer pending, and
// a request a `review` rule holds stays held.

// CatchUpPath is where the record lives.
func CatchUpPath(siteDir string) string {
	return filepath.Join(siteDir, ".polis", "webapp", "agents", Rosie+"-catch-up.json")
}

// CatchUpRecord is the record.
type CatchUpRecord struct {
	Grant string `json:"grant"`
	At    string `json:"at"`
}

// CatchUpDone reports whether the pass has run under this grant URL.
func CatchUpDone(siteDir, grantURL string) bool {
	data, err := os.ReadFile(CatchUpPath(siteDir))
	if err != nil {
		return false
	}
	var r CatchUpRecord
	if json.Unmarshal(data, &r) != nil {
		return false
	}
	return grantURL != "" && r.Grant == grantURL
}

// RecordCatchUp records that the pass ran under grantURL.
func RecordCatchUp(siteDir, grantURL string) error {
	p := CatchUpPath(siteDir)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(CatchUpRecord{Grant: grantURL, At: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	return atomicfile.WriteFile(p, append(data, '\n'), 0600)
}

// NeedsCatchUp reports whether the site has a live Rosie grant whose catch-up
// pass has not run — what the hosted switch-on path uses to decide which
// tenants to reach.
func NeedsCatchUp(siteDir string) bool {
	g, err := LiveRosieGrant(siteDir)
	if err != nil || !g.Valid() {
		return false
	}
	return !CatchUpDone(siteDir, g.URL())
}
