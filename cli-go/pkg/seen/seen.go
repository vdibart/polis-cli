// Package seen holds how far the owner has seen each section of their app:
// one time per section, "seen up to when you arrived".
//
// ⭐ THE RULE IT SERVES. When the owner leaves gather, everything that was new
// when they arrived is seen, whether they scrolled past it or not; items that
// arrived while they were there stay new for next time. So one time per
// section is the whole state: the app sends the time it arrived when it
// leaves, and an item is new when it is later than its section's time.
//
// ⭐ IT BELONGS TO THE ACCOUNT, NOT A DEVICE, so the owner's phone and laptop
// agree. And it only moves forward: a stale tab that leaves late cannot make
// seen things new again.
//
// ⚠️ ONE FILE PER SECTION, so marking one section never touches another. Two
// leaves of the same section at the same moment can still race, and the one
// that loses leaves some items new: the forgiving failure.
package seen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Sections are the parts of the owner's app that keep a seen time: every
// section, since leaving any of them is also how the app knows where the
// owner was last (v5 spec 1.6, 3.9). Enhance has nothing that is new, so its
// time only says when the owner was last there.
var Sections = []string{"gather", "write", "message", "enhance"}

// dir is where the times live: private, never a public file.
const dir = ".polis/seen/"

func valid(section string) bool {
	for _, s := range Sections {
		if s == section {
			return true
		}
	}
	return false
}

type record struct {
	Section string `json:"section"`
	UpTo    string `json:"up_to"`
}

// Get returns each section's seen time. A section never marked is absent:
// everything in it is new.
func Get(site storage.Site) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	for _, s := range Sections {
		t, ok, err := get(site, s)
		if err != nil {
			return nil, err
		}
		if ok {
			out[s] = t
		}
	}
	return out, nil
}

func get(site storage.Site, section string) (time.Time, bool, error) {
	data, err := site.Store().Get(context.Background(), dir+section+".json")
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read seen time for %s: %w", section, err)
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		// A file this build cannot read counts as never marked: the items
		// stay new, which is the safe way to be wrong.
		return time.Time{}, false, nil
	}
	t, err := time.Parse(time.RFC3339Nano, r.UpTo)
	if err != nil {
		return time.Time{}, false, nil
	}
	return t, true, nil
}

// Mark records that the owner has seen a section up to upTo, and returns the
// section's time afterwards. It never moves a time backwards, and a time in
// the future is taken as now.
func Mark(site storage.Site, section string, upTo, now time.Time) (time.Time, error) {
	if !valid(section) {
		names := append([]string(nil), Sections...)
		sort.Strings(names)
		return time.Time{}, fmt.Errorf("unknown section %q (one of %v)", section, names)
	}
	if upTo.IsZero() {
		return time.Time{}, fmt.Errorf("up_to is required")
	}
	if upTo.After(now) {
		upTo = now
	}
	upTo = upTo.UTC()
	cur, ok, err := get(site, section)
	if err != nil {
		return time.Time{}, err
	}
	if ok && !upTo.After(cur) {
		return cur, nil
	}
	b, _ := json.Marshal(record{Section: section, UpTo: upTo.Format(time.RFC3339Nano)})
	if err := site.Store().Put(context.Background(), dir+section+".json", append(b, '\n'), storage.WithMode(0o600)); err != nil {
		return time.Time{}, fmt.Errorf("save seen time for %s: %w", section, err)
	}
	return upTo, nil
}
