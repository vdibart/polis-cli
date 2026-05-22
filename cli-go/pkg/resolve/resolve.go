// Package resolve provides centralized path resolution for polis sites.
//
// A Resolver is initialized from the .well-known/polis identity document and
// loaded bundles. It replaces all hardcoded paths across packages with a single
// source of truth derived from bundle declarations.
package resolve

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

// Resolver provides path resolution for a polis site.
type Resolver struct {
	siteDir string
	bundles map[string]*bundle.Bundle // keyed by bundle name
}

// New creates a Resolver rooted at siteDir with the given bundles.
func New(siteDir string, bundles map[string]*bundle.Bundle) *Resolver {
	return &Resolver{
		siteDir: siteDir,
		bundles: bundles,
	}
}

// SiteDir returns the absolute site root directory.
func (r *Resolver) SiteDir() string {
	return r.siteDir
}

// --- Site-level paths (not bundle-specific) ---

// WellKnownPath returns the absolute path to .well-known/polis.
func (r *Resolver) WellKnownPath() string {
	return filepath.Join(r.siteDir, ".well-known", "polis")
}

// KeysDir returns the absolute path to the keys directory.
func (r *Resolver) KeysDir() string {
	return filepath.Join(r.siteDir, ".polis", "keys")
}

// PrivateKeyPath returns the absolute path to the private key.
func (r *Resolver) PrivateKeyPath() string {
	return filepath.Join(r.siteDir, ".polis", "keys", "id_ed25519")
}

// PublicKeyPath returns the absolute path to the public key.
func (r *Resolver) PublicKeyPath() string {
	return filepath.Join(r.siteDir, ".polis", "keys", "id_ed25519.pub")
}

// SiteSubdir returns the absolute path to a site/ subdirectory.
// Example: SiteSubdir("snippets") → <siteDir>/site/snippets
func (r *Resolver) SiteSubdir(name string) string {
	return filepath.Join(r.siteDir, "site", name)
}

// SnippetsDir returns the absolute path to site/snippets/.
func (r *Resolver) SnippetsDir() string {
	return r.SiteSubdir("snippets")
}

// ThemesDir returns the absolute path to site/themes/.
func (r *Resolver) ThemesDir() string {
	return r.SiteSubdir("themes")
}

// LogsDir returns the absolute path to .polis/logs/.
func (r *Resolver) LogsDir() string {
	return filepath.Join(r.siteDir, ".polis", "logs")
}

// PublicPoliciesPath returns the absolute path to policies/rules.jsonl.
func (r *Resolver) PublicPoliciesPath() string {
	return filepath.Join(r.siteDir, "policies", "rules.jsonl")
}

// PrivatePoliciesPath returns the absolute path to .polis/policies/rules.jsonl.
func (r *Resolver) PrivatePoliciesPath() string {
	return filepath.Join(r.siteDir, ".polis", "policies", "rules.jsonl")
}

// WebappConfigPath returns the absolute path to .polis/webapp/config.json.
func (r *Resolver) WebappConfigPath() string {
	return filepath.Join(r.siteDir, ".polis", "webapp", "config.json")
}

// HooksDir returns the absolute path to .polis/webapp/hooks/.
func (r *Resolver) HooksDir() string {
	return filepath.Join(r.siteDir, ".polis", "webapp", "hooks")
}

// --- Bundle-level paths ---

// BundlePath returns the absolute path to a bundle's bundle.json.
func (r *Resolver) BundlePath(bundleName string) string {
	return filepath.Join(r.siteDir, "content", bundleName, "bundle.json")
}

// BundleDir returns the absolute path to a bundle's content directory.
func (r *Resolver) BundleDir(bundleName string) string {
	return filepath.Join(r.siteDir, "content", bundleName)
}

// --- Content type paths ---

// findBundle locates the bundle containing the given content type.
func (r *Resolver) findBundle(typeName string) (*bundle.Bundle, error) {
	for _, b := range r.bundles {
		if _, ok := b.Types[typeName]; ok {
			return b, nil
		}
	}
	return nil, fmt.Errorf("no bundle contains content type %q", typeName)
}

// ContentPath returns the absolute source content directory for a content type.
// Example: ContentPath("pub.polis.post") → <siteDir>/content/pub.polis.core/post
func (r *Resolver) ContentPath(typeName string) (string, error) {
	b, err := r.findBundle(typeName)
	if err != nil {
		return "", err
	}
	rel, err := b.ContentDir(typeName)
	if err != nil {
		return "", err
	}
	return filepath.Join(r.siteDir, rel), nil
}

// MountPath returns the absolute rendered output directory for a content type.
// Example: MountPath("pub.polis.post") → <siteDir>/posts
func (r *Resolver) MountPath(typeName string) (string, error) {
	b, err := r.findBundle(typeName)
	if err != nil {
		return "", err
	}
	rel, err := b.MountDir(typeName)
	if err != nil {
		return "", err
	}
	return filepath.Join(r.siteDir, rel), nil
}

// PrivatePath returns the absolute private state directory for a content type,
// optionally with a subdirectory appended.
// Example: PrivatePath("pub.polis.post", "drafts") → <siteDir>/.polis/bundles/pub.polis.core/posts/drafts
// Example: PrivatePath("pub.polis.comment", "pending") → <siteDir>/.polis/bundles/pub.polis.core/comments/pending
func (r *Resolver) PrivatePath(typeName string, subdirs ...string) (string, error) {
	b, err := r.findBundle(typeName)
	if err != nil {
		return "", err
	}
	rel, err := b.PrivateDir(typeName)
	if err != nil {
		return "", err
	}
	parts := append([]string{r.siteDir, rel}, subdirs...)
	return filepath.Join(parts...), nil
}

// Artifact returns the absolute path to a bundle-level artifact.
// Example: Artifact("pub.polis.core", "index.jsonl") → <siteDir>/content/pub.polis.core/index.jsonl
func (r *Resolver) Artifact(bundleName, filename string) string {
	return filepath.Join(r.siteDir, "content", bundleName, filename)
}

// TypeArtifact returns the absolute path to a content-type-level artifact.
// Example: TypeArtifact("pub.polis.comment", "blessed.json") → <siteDir>/content/pub.polis.core/comment/blessed.json
func (r *Resolver) TypeArtifact(typeName, filename string) (string, error) {
	path, err := r.ContentPath(typeName)
	if err != nil {
		return "", err
	}
	return filepath.Join(path, filename), nil
}

// --- DS paths ---

// DSBundleDir returns the absolute path to a bundle's DS directory for a domain.
// Example: DSBundleDir("pub.polis.core", "ds.polis.pub") → <siteDir>/.polis/ds/ds.polis.pub/pub.polis.core
func (r *Resolver) DSBundleDir(bundleName, domain string) string {
	return filepath.Join(r.siteDir, ".polis", "ds", domain, bundleName)
}

// DSStatePath returns the absolute path to a bundle's DS state directory for a domain.
// Example: DSStatePath("pub.polis.core", "ds.polis.pub") → <siteDir>/.polis/ds/ds.polis.pub/pub.polis.core/state
func (r *Resolver) DSStatePath(bundleName, domain string) string {
	return filepath.Join(r.DSBundleDir(bundleName, domain), "state")
}

// DSConfigPath returns the absolute path to a bundle's DS config directory for a domain.
// Example: DSConfigPath("pub.polis.core", "ds.polis.pub") → <siteDir>/.polis/ds/ds.polis.pub/pub.polis.core/config
func (r *Resolver) DSConfigPath(bundleName, domain string) string {
	return filepath.Join(r.DSBundleDir(bundleName, domain), "config")
}

// --- Helpers for common operations ---

// IndexPath returns the absolute path to a bundle's content index.
// Example: IndexPath("pub.polis.core") → <siteDir>/content/pub.polis.core/index.jsonl
func (r *Resolver) IndexPath(bundleName string) string {
	return r.Artifact(bundleName, "index.jsonl")
}

// FollowingPath returns the absolute path to the following.json file.
func (r *Resolver) FollowingPath() (string, error) {
	return r.TypeArtifact("pub.polis.follow", "following.json")
}

// FollowersPath returns the absolute path to the followers.json file.
func (r *Resolver) FollowersPath() (string, error) {
	return r.TypeArtifact("pub.polis.follow", "followers.json")
}

// BlessedCommentsPath returns the absolute path to the blessed comments index.
func (r *Resolver) BlessedCommentsPath() (string, error) {
	return r.TypeArtifact("pub.polis.comment", "blessed.json")
}

// VersionsDir returns the .versions subdirectory path within a content type's date dir.
// Example: VersionsDir("pub.polis.post", "20260301") → <siteDir>/content/pub.polis.core/post/20260301/.versions
func (r *Resolver) VersionsDir(typeName, dateDir string) (string, error) {
	contentDir, err := r.ContentPath(typeName)
	if err != nil {
		return "", err
	}
	return filepath.Join(contentDir, dateDir, ".versions"), nil
}

// --- Relative path helpers ---

// Rel returns the path relative to site root. If the path is not under site root,
// it returns the path unchanged.
func (r *Resolver) Rel(absPath string) string {
	rel, err := filepath.Rel(r.siteDir, absPath)
	if err != nil {
		return absPath
	}
	return rel
}

// Abs returns the absolute path from a site-relative path.
func (r *Resolver) Abs(relPath string) string {
	if filepath.IsAbs(relPath) {
		return relPath
	}
	return filepath.Join(r.siteDir, relPath)
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
