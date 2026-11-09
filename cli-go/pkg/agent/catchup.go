package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
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

// CatchUpKey is where the record lives, relative to the site root.
//
// ⭐ A KEY, so the record and its reader agree on any backend. ⚠️ It matters
// more than it looks: if the read and the write disagreed on a blob site, the
// catch-up pass would run on EVERY boot, because "not done" is what a failed
// read reports.
const CatchUpKey = ".polis/webapp/agents/" + Rosie + "-catch-up.json"

// CatchUpRecord is the record.
type CatchUpRecord struct {
	Grant string `json:"grant"`
	At    string `json:"at"`
}

// CatchUpDone reports whether the pass has run under this grant URL.
func CatchUpDone(site storage.Site, grantURL string) bool {
	data, err := site.Store().Get(context.Background(), CatchUpKey)
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
func RecordCatchUp(site storage.Site, grantURL string) error {
	// ⭐ No MkdirAll: atomicfile.Write has the store create the parent, and
	// `.polis` is a declared private prefix, so it comes out 0700 as the
	// literal here used to make it.
	data, _ := json.MarshalIndent(CatchUpRecord{Grant: grantURL, At: time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	return atomicfile.Write(context.Background(), site, CatchUpKey, append(data, '\n'), 0600)
}

// NeedsCatchUp reports whether the site has a live Rosie grant whose catch-up
// pass has not run — what the hosted switch-on path uses to decide which
// tenants to reach.
func NeedsCatchUp(site storage.Site) bool {
	g, err := LiveRosieGrant(site)
	if err != nil || !g.Valid() {
		return false
	}
	return !CatchUpDone(site, g.URL())
}
