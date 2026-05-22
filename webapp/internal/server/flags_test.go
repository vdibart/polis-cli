package server

import (
	"strings"
	"testing"
)

// TestResolveModes_Defaults covers the three valid invocations the plan
// settled on (§5.a "Three valid invocations") plus the explicit
// dogfood-mode invocation (--owner --reader).
func TestResolveModes_Defaults(t *testing.T) {
	cases := []struct {
		name        string
		opts        RunOptions
		wantData    string
		wantSurface string
	}{
		{
			name:        "default (no flags) → owner + editor",
			opts:        RunOptions{},
			wantData:    DataModeOwner,
			wantSurface: SurfaceEditor,
		},
		{
			name:        "explicit owner only → owner + editor",
			opts:        RunOptions{DataMode: DataModeOwner},
			wantData:    DataModeOwner,
			wantSurface: SurfaceEditor,
		},
		{
			name:        "owner + reader (dogfood mode)",
			opts:        RunOptions{DataMode: DataModeOwner, Surface: SurfaceReader},
			wantData:    DataModeOwner,
			wantSurface: SurfaceReader,
		},
		{
			name:        "mirror only → mirror + reader (default)",
			opts:        RunOptions{DataMode: DataModeMirror},
			wantData:    DataModeMirror,
			wantSurface: SurfaceReader,
		},
		{
			name:        "explicit mirror + reader",
			opts:        RunOptions{DataMode: DataModeMirror, Surface: SurfaceReader},
			wantData:    DataModeMirror,
			wantSurface: SurfaceReader,
		},
		{
			name:        "explicit owner + editor",
			opts:        RunOptions{DataMode: DataModeOwner, Surface: SurfaceEditor},
			wantData:    DataModeOwner,
			wantSurface: SurfaceEditor,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotData, gotSurface, err := resolveModes(tc.opts)
			if err != nil {
				t.Fatalf("resolveModes returned error: %v", err)
			}
			if gotData != tc.wantData {
				t.Errorf("DataMode = %q, want %q", gotData, tc.wantData)
			}
			if gotSurface != tc.wantSurface {
				t.Errorf("Surface = %q, want %q", gotSurface, tc.wantSurface)
			}
		})
	}
}

// TestResolveModes_MirrorEditorRejected covers the error case the plan
// called out: --mirror --editor must fail at startup with a clear message.
func TestResolveModes_MirrorEditorRejected(t *testing.T) {
	_, _, err := resolveModes(RunOptions{DataMode: DataModeMirror, Surface: SurfaceEditor})
	if err == nil {
		t.Fatal("expected error for --mirror --editor, got nil")
	}
	msg := err.Error()
	// Must mention both flags so the operator knows how to fix it.
	if !strings.Contains(msg, "--mirror") || !strings.Contains(msg, "--editor") {
		t.Errorf("error message %q should mention both --mirror and --editor", msg)
	}
	// Must hint at the fix.
	if !strings.Contains(msg, "--reader") {
		t.Errorf("error message %q should hint at --reader as the fix", msg)
	}
}

// TestResolveModes_InvalidValues covers parse-error paths in case the
// caller (e.g., main.go) ever lets through a typo'd flag value.
func TestResolveModes_InvalidValues(t *testing.T) {
	cases := []struct {
		name string
		opts RunOptions
	}{
		{"invalid data mode", RunOptions{DataMode: "guest"}},
		{"invalid surface", RunOptions{Surface: "preview"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := resolveModes(tc.opts); err == nil {
				t.Errorf("expected error for %v, got nil", tc.opts)
			}
		})
	}
}
