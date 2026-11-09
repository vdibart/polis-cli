// Package resolve answers questions about a site's loaded bundles: which
// bundle owns a content type, which type a mount belongs to.
//
// ⛔ It is NOT a path resolver any more. It used to build an absolute path for
// every file a site holds, from a directory string, and nothing outside its
// own tests called any of those builders: a site is addressed through its
// storage.Site handle, by key. The handle is carried here so the content API's
// handlers reach the site through it.
package resolve

import (
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Resolver holds a site and its loaded bundles.
type Resolver struct {
	site    storage.Site
	bundles map[string]*bundle.Bundle // keyed by bundle name
}

// New creates a Resolver for site with the given bundles.
func New(site storage.Site, bundles map[string]*bundle.Bundle) *Resolver {
	return &Resolver{
		site:    site,
		bundles: bundles,
	}
}

// Site returns the site handle.
func (r *Resolver) Site() storage.Site {
	return r.site
}

// SiteDir returns the site's root directory.
//
// ⚠️ Only for what genuinely needs a directory: a hook is a local script run
// with the site directory as its working directory (kind 3).
func (r *Resolver) SiteDir() string {
	return r.site.Dir()
}

// findBundle locates the bundle containing the given content type.
func (r *Resolver) findBundle(typeName string) (*bundle.Bundle, error) {
	for _, b := range r.bundles {
		if _, ok := b.Types[typeName]; ok {
			return b, nil
		}
	}
	return nil, fmt.Errorf("no bundle contains content type %q", typeName)
}

// --- Bundle enumeration ---

// Bundles returns all loaded bundles.
func (r *Resolver) Bundles() map[string]*bundle.Bundle {
	return r.bundles
}

// Bundle returns a specific bundle by name.
func (r *Resolver) Bundle(name string) (*bundle.Bundle, error) {
	b, ok := r.bundles[name]
	if !ok {
		return nil, fmt.Errorf("bundle %q not loaded", name)
	}
	return b, nil
}

// ContentTypeNames returns all content type names across all bundles.
func (r *Resolver) ContentTypeNames() []string {
	var names []string
	for _, b := range r.bundles {
		for name := range b.Types {
			names = append(names, name)
		}
	}
	return names
}

// BundleForType returns the bundle name that owns the given content type.
func (r *Resolver) BundleForType(typeName string) (string, error) {
	b, err := r.findBundle(typeName)
	if err != nil {
		return "", err
	}
	return b.Name, nil
}

// MountToType maps a mount path (e.g., "/posts") to its content type name.
func (r *Resolver) MountToType(mount string) (string, error) {
	// Normalize: ensure leading slash
	if !strings.HasPrefix(mount, "/") {
		mount = "/" + mount
	}
	for _, b := range r.bundles {
		for typeName, ct := range b.Types {
			if ct.Mount == mount {
				return typeName, nil
			}
		}
	}
	return "", fmt.Errorf("no content type has mount %q", mount)
}
