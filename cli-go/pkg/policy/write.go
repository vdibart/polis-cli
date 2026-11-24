package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Writing rules for someone other than the owner (an agent, over the API).
//
// ⛔ ONLY EVER TIGHTENS. Rules are evaluated first-match-wins, private file
// first, so the one change that can never loosen a decision is a DENY rule
// placed at the very top of the private file: whatever it matches, it decides
// first, and it can only say no. ⛔ NOT review: review is weaker than deny, so
// "review pub.polis.comment from all" on top would turn every comment the
// owner denies into one waiting for them (found by review, 2026-11-14).
// Removing a rule is NOT safe in general (an allow above a looser bless, once
// removed, lets the bless decide), so the only rules that may be removed this
// way are ones added this way, by the same hand.
//
// ⭐ The owner's own lines are never rewritten: an added rule is inserted as
// one line after the file's header, and a removal deletes only its own line.

// AddedByField marks a rule added through TightenPrivate, so only such rules
// can be removed by RemoveAdded.
const AddedByField = "added_by"

// writeMu serialises this package's read-change-write of the private rules
// within one process. ⚠️ It does not cover another writer of the file (the
// owner editing it by hand at the same moment).
var writeMu sync.Mutex

// ErrNotTightening is a rule whose verb could loosen a decision.
var ErrNotTightening = errors.New("only deny rules can be added this way")

// TightenPrivate adds a deny rule at the top of the site's private rules,
// marked as added by addedBy. It refuses any other verb, and a rule that does
// not parse.
func TightenPrivate(site storage.Site, rule, addedBy string) error {
	parsed, err := Parse(rule)
	if err != nil {
		return fmt.Errorf("invalid rule: %w", err)
	}
	if parsed.Action != "deny" {
		return ErrNotTightening
	}
	if strings.TrimSpace(addedBy) == "" {
		return errors.New("addedBy is required")
	}
	line, err := json.Marshal(map[string]interface{}{"active": true, "policy": strings.TrimSpace(rule), AddedByField: addedBy})
	if err != nil {
		return err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	lines, err := privateLines(site)
	if err != nil {
		return err
	}
	at := 0
	if len(lines) > 0 && isHeader(lines[0]) {
		at = 1
	}
	out := append(append(append([]string(nil), lines[:at]...), string(line)), lines[at:]...)
	return writePrivate(site, out)
}

// RemoveAdded removes rule from the private file if, and only if, addedBy
// added it there. It reports whether anything was removed; the owner's own
// rules are never touched.
func RemoveAdded(site storage.Site, rule, addedBy string) (bool, error) {
	writeMu.Lock()
	defer writeMu.Unlock()
	lines, err := privateLines(site)
	if err != nil {
		return false, err
	}
	rule = strings.TrimSpace(rule)
	kept := make([]string, 0, len(lines))
	removed := false
	for _, l := range lines {
		var entry map[string]interface{}
		if !removed && json.Unmarshal([]byte(l), &entry) == nil &&
			entry["policy"] == rule && entry[AddedByField] == addedBy {
			removed = true
			continue
		}
		kept = append(kept, l)
	}
	if !removed {
		return false, nil
	}
	return true, writePrivate(site, kept)
}

func isHeader(line string) bool {
	var h map[string]interface{}
	if json.Unmarshal([]byte(line), &h) != nil {
		return false
	}
	_, ok := h["version"]
	return ok
}

// privateLines reads the private file's lines, or a fresh file's header when
// there is none.
func privateLines(site storage.Site) ([]string, error) {
	data, err := site.Store().Get(context.Background(), PrivateRulesKey)
	if errors.Is(err, fs.ErrNotExist) {
		data = []byte(DefaultPrivatePolicyContent())
	} else if err != nil {
		return nil, fmt.Errorf("read private rules: %w", err)
	}
	var lines []string
	for _, l := range bytes.Split(data, []byte("\n")) {
		if s := strings.TrimRight(string(l), "\r"); strings.TrimSpace(s) != "" {
			lines = append(lines, s)
		}
	}
	return lines, nil
}

func writePrivate(site storage.Site, lines []string) error {
	body := strings.Join(lines, "\n") + "\n"
	if err := site.Store().Put(context.Background(), PrivateRulesKey, []byte(body), storage.WithMode(0o600)); err != nil {
		return fmt.Errorf("write private rules: %w", err)
	}
	return nil
}
