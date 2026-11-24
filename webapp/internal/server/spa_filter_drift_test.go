package server

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

// TestSPA_StreamControllerFilterChangeHook — step-06/6.f.2 review
// concern #1. stream.js must expose the onFilterChange registry AND
// fire it from every filter-mutating call site (selectOption + the
// four setFilter* setters + setFilter batch).
func TestSPA_StreamControllerFilterChangeHook(t *testing.T) {
	jsBytes, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.js")
	if err != nil {
		t.Fatalf("read stream.js: %v", err)
	}
	src := string(jsBytes)

	if !strings.Contains(src, "addFilterChangeListener") {
		t.Errorf("stream.js missing addFilterChangeListener — onFilterChange hook not wired")
	}
	if !strings.Contains(src, "onFilterChange: addFilterChangeListener") {
		t.Errorf("stream.js doesn't expose onFilterChange on PolisStream — owner-extras can't subscribe")
	}
	// fireFilterChange must be called from every filter-mutating call
	// site. Spot-check via callsite count: the function exists once
	// (definition) plus a call from selectOption + 4 setters + setFilter
	// batch = at least 6 occurrences.
	count := strings.Count(src, "fireFilterChange()")
	if count < 6 {
		t.Errorf("expected fireFilterChange() called from at least 6 mutation sites (selectOption + 4 setters + setFilter batch); found %d", count)
	}
}

// TestSPA_StreamCSSHasActivityMode asserts the v4 stream.css ships
// the body.is-activity-mode rules that compact entries in activity
// view. Catches accidental removal of the Layer-2 gate.
func TestSPA_StreamCSSHasActivityMode(t *testing.T) {
	cssBytes, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.css")
	if err != nil {
		t.Fatalf("read stream.css: %v", err)
	}
	src := string(cssBytes)
	if !strings.Contains(src, "body.is-activity-mode") {
		t.Errorf("stream.css missing body.is-activity-mode gate — activity render mode won't compact entries")
	}
	if !strings.Contains(src, ".activity-signal") {
		t.Errorf("stream.css missing .activity-signal style — afterRender's appended element won't render with intended layout")
	}
}

// TestSPA_StreamCSSHasEditorCard — step-06/6.h. The bundle stream.css
// must ship .entry--editor styling + body.is-editor-focus full-screen
// takeover rules.
func TestSPA_StreamCSSHasEditorCard(t *testing.T) {
	cssBytes, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.css")
	if err != nil {
		t.Fatalf("read stream.css: %v", err)
	}
	src := string(cssBytes)
	if !strings.Contains(src, ".entry.entry--editor") && !strings.Contains(src, ".entry--editor") {
		t.Errorf("stream.css missing .entry--editor styling")
	}
	if !strings.Contains(src, "body.is-editor-focus") {
		t.Errorf("stream.css missing body.is-editor-focus full-screen takeover rules")
	}
	// The takeover must hide the topbar + non-editor stream entries +
	// layout-left so the editor truly "takes over."
	if !strings.Contains(src, "body.is-editor-focus .polis-topbar") {
		t.Errorf("focus mode doesn't hide .polis-topbar — full takeover incomplete")
	}
}

// TestSPA_StreamFilterChangePreservesPinned — step-06/6.h. stream.js's
// clearStreamForFilterChange must skip [data-polis-pinned] entries so
// the editor card stays put across filter changes (resolved-decision
// #17: sticky-across-filter-changes only).
func TestSPA_StreamFilterChangePreservesPinned(t *testing.T) {
	jsBytes, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.js")
	if err != nil {
		t.Fatalf("read stream.js: %v", err)
	}
	src := string(jsBytes)
	// The clear loop must include a hasAttribute('data-polis-pinned')
	// check that continues without removing.
	if !strings.Contains(src, "data-polis-pinned") {
		t.Errorf("stream.js doesn't reference data-polis-pinned — pinned entries won't survive filter change")
	}
	// Find the clearStreamForFilterChange function and confirm it
	// references the pinned attribute.
	clearIdx := strings.Index(src, "function clearStreamForFilterChange")
	if clearIdx == -1 {
		t.Fatal("clearStreamForFilterChange function missing")
	}
	clearEnd := strings.Index(src[clearIdx:], "\n    }\n")
	if clearEnd == -1 {
		t.Fatal("clearStreamForFilterChange close not found")
	}
	clearBlock := src[clearIdx : clearIdx+clearEnd]
	if !strings.Contains(clearBlock, "data-polis-pinned") {
		t.Errorf("clearStreamForFilterChange doesn't check data-polis-pinned — pinned entries (editor card) get cleared on filter change")
	}
}

// TestSPA_StreamCSSHasEntryActions — step-06/6.i. stream.css must ship
// .entry-actions hover-revealed chrome rules so the owner-action
// buttons surface on hover and stay hidden otherwise. Visibility is
// gated on body.is-owner (Layer 2 defense in depth — even if a public
// visitor somehow ended up with a decorated DOM, CSS would suppress
// the chrome).
func TestSPA_StreamCSSHasEntryActions(t *testing.T) {
	cssBytes, err := fs.ReadFile(bundle.ReferencePayloadFS(), "pub.polis.core/shapes/v4/stream.css")
	if err != nil {
		t.Fatalf("read stream.css: %v", err)
	}
	src := string(cssBytes)
	if !strings.Contains(src, ".entry-actions") {
		t.Errorf("stream.css missing .entry-actions selector")
	}
	if !strings.Contains(src, "body.is-owner .entry:hover") {
		t.Errorf("stream.css missing body.is-owner .entry:hover rule — chrome won't reveal on hover")
	}
}
