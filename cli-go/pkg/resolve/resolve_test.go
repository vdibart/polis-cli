package resolve

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

func testResolver(t *testing.T) *Resolver {
	t.Helper()
	b := bundle.DefaultCoreBundle()
	return New(storage.At("/home/alice/mysite"), map[string]*bundle.Bundle{
		b.Name: b,
	})
}

func TestMountToType(t *testing.T) {
	r := testResolver(t)

	tests := []struct {
		mount string
		want  string
	}{
		{"/posts", "pub.polis.post"},
		{"/comments", "pub.polis.comment"},
		{"/follow", "pub.polis.follow"},
		{"posts", "pub.polis.post"}, // without leading slash
	}

	for _, tt := range tests {
		got, err := r.MountToType(tt.mount)
		if err != nil {
			t.Errorf("MountToType(%q): %v", tt.mount, err)
			continue
		}
		if got != tt.want {
			t.Errorf("MountToType(%q) = %q, want %q", tt.mount, got, tt.want)
		}
	}
}

func TestBundleForType(t *testing.T) {
	r := testResolver(t)
	got, err := r.BundleForType("pub.polis.post")
	if err != nil {
		t.Fatalf("BundleForType: %v", err)
	}
	if got != "pub.polis.core" {
		t.Errorf("BundleForType() = %q, want pub.polis.core", got)
	}
}

func TestContentTypeNames(t *testing.T) {
	r := testResolver(t)
	names := r.ContentTypeNames()
	if len(names) != 9 {
		t.Errorf("got %d content types, want 9", len(names))
	}
}

func TestUnknownTypeFails(t *testing.T) {
	r := testResolver(t)
	if _, err := r.BundleForType("com.example.unknown"); err == nil {
		t.Error("expected error for unknown type")
	}
}

func TestTheSiteIsTheOneGiven(t *testing.T) {
	r := testResolver(t)
	if r.Site().ID() != storage.At("/home/alice/mysite").ID() || r.SiteDir() != "/home/alice/mysite" {
		t.Errorf("Site = %q, SiteDir = %q", r.Site().ID(), r.SiteDir())
	}
}
