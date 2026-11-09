// Package navshortcut holds the owner's nav shortcuts: buttons in the
// webapp's nav bar that each open one PQL sentence, for example "all posts
// from discover.polis.pub with comments".
//
// ⚠️ EXPERIMENTAL. Part of an exploratory feature: the owner's AI changing
// their site through the instance's API. This package's API may change or be
// removed without notice; do not depend on it.
//
// ⭐ BEHAVIOUR AS DATA, like streammark. A shortcut is a label, an icon from
// a closed set and a PQL sentence; the instance validates it and the app draws
// the button. A shortcut carries no URL, no markup and no code: the sentence
// must parse as PQL, and is stored in its canonical form.
package navshortcut

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"unicode"

	"github.com/vdibart/polis-cli/cli-go/pkg/pql"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Key is where the shortcuts live: private, never a public file.
const Key = ".polis/webapp/nav-shortcuts.json"

// Icons are the icons a shortcut may use; the app ships each one.
var Icons = []string{"star", "bolt", "bookmark", "flag", "sparkle", "chat"}

const (
	MaxShortcuts = 5
	MaxLabelLen  = 32
	maxIDLen     = 40
)

// Shortcut is one nav-bar button.
type Shortcut struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Icon     string `json:"icon"`
	Sentence string `json:"sentence"`
}

// File is the shortcuts file.
type File struct {
	Shortcuts []Shortcut `json:"shortcuts"`
}

// Normalize validates the shortcuts and returns them in canonical form. Every
// problem is named; none is guessed at.
func Normalize(f File) (File, error) {
	if len(f.Shortcuts) > MaxShortcuts {
		return File{}, fmt.Errorf("at most %d shortcuts, got %d", MaxShortcuts, len(f.Shortcuts))
	}
	out := File{Shortcuts: make([]Shortcut, 0, len(f.Shortcuts))}
	seen := map[string]bool{}
	for i, sc := range f.Shortcuts {
		n := i + 1
		sc.Label = strings.TrimSpace(sc.Label)
		if sc.Label == "" || len([]rune(sc.Label)) > MaxLabelLen || !plainLabel(sc.Label) {
			return File{}, fmt.Errorf("shortcut %d: label must be 1 to %d characters of plain text", n, MaxLabelLen)
		}
		if sc.Icon == "" {
			sc.Icon = "star"
		}
		if !knownIcon(sc.Icon) {
			return File{}, fmt.Errorf("shortcut %d: icon must be one of %s", n, strings.Join(Icons, ", "))
		}
		filter, err := pql.Parse(strings.TrimSpace(sc.Sentence))
		if err != nil {
			return File{}, fmt.Errorf("shortcut %d: %q is not a PQL sentence polis understands (%v). %s", n, sc.Sentence, err, pql.Vocabulary())
		}
		canon, err := pql.Compose(filter)
		if err != nil {
			return File{}, fmt.Errorf("shortcut %d: %v", n, err)
		}
		sc.Sentence = canon
		sc.ID = strings.TrimSpace(sc.ID)
		if sc.ID == "" {
			sc.ID = fmt.Sprintf("shortcut-%d", n)
		}
		if len(sc.ID) > maxIDLen || seen[sc.ID] {
			return File{}, fmt.Errorf("shortcut %d: id must be unique and at most %d characters", n, maxIDLen)
		}
		seen[sc.ID] = true
		out.Shortcuts = append(out.Shortcuts, sc)
	}
	return out, nil
}

func knownIcon(name string) bool {
	for _, i := range Icons {
		if i == name {
			return true
		}
	}
	return false
}

// plainLabel refuses markup and control characters; the app also inserts the
// label as text, never as HTML.
func plainLabel(s string) bool {
	for _, c := range s {
		if c == '<' || c == '>' || c == '&' || c == '"' || unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// Load reads the shortcuts. A site with no file has none.
func Load(site storage.Site) (File, error) {
	data, err := site.Store().Get(context.Background(), Key)
	if errors.Is(err, fs.ErrNotExist) {
		return File{Shortcuts: []Shortcut{}}, nil
	}
	if err != nil {
		return File{}, fmt.Errorf("read nav shortcuts: %w", err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return File{}, fmt.Errorf("parse nav shortcuts: %w", err)
	}
	// ⛔ What was written is checked again on the way in: only Save writes the
	// file today, but a file that did not come through Save (hand-edited, an
	// older build, a restored backup) must not reach a page unchecked.
	n, err := Normalize(f)
	if err != nil {
		return File{}, fmt.Errorf("nav shortcuts on disk are not valid: %w", err)
	}
	if n.Shortcuts == nil {
		n.Shortcuts = []Shortcut{}
	}
	return n, nil
}

// Save validates and writes the shortcuts (mode 600), replacing what was
// there. An empty list removes them all.
func Save(site storage.Site, f File) (File, error) {
	n, err := Normalize(f)
	if err != nil {
		return File{}, err
	}
	b, _ := json.MarshalIndent(n, "", "  ")
	if err := site.Store().Put(context.Background(), Key, append(b, '\n'), storage.WithMode(0o600)); err != nil {
		return File{}, fmt.Errorf("write nav shortcuts: %w", err)
	}
	return n, nil
}
