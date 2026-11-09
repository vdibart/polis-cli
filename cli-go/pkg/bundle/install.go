package bundle

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// referencePayload holds the canonical bundle payload shipped with the CLI.
// Each tenant's .polis/bundles/<name>/ is populated from the corresponding
// subtree here — via polis init (new sites) or Medic resync (existing sites).
//
//go:embed all:fixtures
var referencePayload embed.FS

// ReferenceRoot is the prefix inside the embedded FS where bundle payloads live.
const ReferenceRoot = "fixtures"

// ReferencePayloadFS returns the embedded bundle reference payload as a
// read-only filesystem rooted at the fixtures directory. Callers see paths
// like `pub.polis.core/shapes/v4/stream.js` directly (the `fixtures/` prefix
// is stripped). Used by the webapp to serve shape assets (stream.js,
// stream.css, snippet HTML) to the owner SPA without duplicating the files
// in webapp/internal/webui/www. Returns the bare embed.FS if Sub fails;
// callers can detect the difference by checking whether `fixtures/` is in
// the path.
func ReferencePayloadFS() fs.FS {
	sub, err := fs.Sub(referencePayload, ReferenceRoot)
	if err != nil {
		return referencePayload
	}
	return sub
}

// EnsureReferencePayload installs the reference payload for the named bundle
// into the tenant's .polis/bundles/<bundleName>/ directory.
//
// It writes every file from the embedded fixtures/<bundleName>/ subtree
// whose contents differ from what's already on disk, creating parent
// directories as needed. Files that already match the embedded content are
// left untouched (mtime-idempotent — important once Medic calls this on
// every cycle). Files in the target that are not in the reference are
// preserved, so private per-tenant state that shares the bundle's
// directory (drafts, pending comments) survives.
//
// On success the bundle's shipped shape/theme versions are recorded in
// .polis/bundles/registry.json via BundleRegistry.RecordInstalledVersions —
// Patrol's checkBundleReferencePayload uses those stamps to decide whether
// the next cycle needs to re-install.
//
// Called by polis init (to populate a new site) and by Medic's resync loop
// (to re-apply the reference when shipped versions advance). Safe to call
// repeatedly.
func EnsureReferencePayload(site storage.Site, bundleName string) error {
	if !site.Valid() {
		return fmt.Errorf("a site handle is required")
	}
	if bundleName == "" {
		return fmt.Errorf("bundleName is required")
	}

	srcRoot := ReferenceRoot + "/" + bundleName
	if _, err := fs.Stat(referencePayload, srcRoot); err != nil {
		return fmt.Errorf("no reference payload for bundle %q: %w", bundleName, err)
	}

	// ⭐ Copied BY KEY into the site's store. ⛔ The rule the MkdirAlls here
	// used to carry still holds, and the store now carries it: everything under
	// .polis is created 0700 (the site store declares .polis a private
	// prefix), because a 0755 child of a 0700 root is unreachable today but
	// becomes live the moment the tree is copied anywhere else — an export
	// unzipped, an archive restored. An embedded tree has no empty
	// directories, so every directory the payload holds is the parent of a
	// file the store writes. TestEnsureReferencePayloadKeepsTheBundleTreePrivate
	// pins it.
	dstRoot := ".polis/bundles/" + bundleName
	ctx := context.Background()

	walkErr := fs.WalkDir(referencePayload, srcRoot, func(embedPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(embedPath, srcRoot), "/")
		dstKey := dstRoot + "/" + rel

		data, err := referencePayload.ReadFile(embedPath)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", embedPath, err)
		}
		// Skip the write when stored content already matches the embedded
		// reference. Saves mtime churn on every Medic cycle and lets file
		// watchers / mtime-based diff tools stay quiet on no-op syncs.
		if existing, err := site.Store().Get(ctx, dstKey); err == nil && bytesEqual(existing, data) {
			return nil
		}
		if err := site.Store().Put(ctx, dstKey, data); err != nil {
			return fmt.Errorf("write %s: %w", dstKey, err)
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	// Stamp the registry with the versions we just installed so Patrol can
	// detect drift on subsequent cycles.
	//
	// Use LoadRegistryOrInit (introduced 2026-04-29) so we only fall
	// back to DefaultRegistry when the file is GENUINELY missing. Read
	// errors (permission denied) and parse errors propagate as a hard
	// error — they used to silently overwrite the registry with
	// DefaultRegistry, which clobbered hand-set active_theme/active_shape
	// when an operator's edit happened to leave the file unreadable
	// for the polis service user. The discover.polis.pub regression
	// (root-ssh hand-edit producing root:root mode 0600 file vs polis
	// service running as polis user → permission denied → silent
	// reset to v3 + random theme) was the smoking gun.
	if bundleName == DefaultCoreBundle().Name {
		reg, err := LoadRegistryOrInit(site)
		if err != nil {
			return fmt.Errorf("load registry to record installed versions: %w", err)
		}
		reg.RecordInstalledVersions(DefaultCoreBundle())
		if err := SaveRegistry(site, reg); err != nil {
			return fmt.Errorf("record installed versions: %w", err)
		}
	}
	return nil
}

// CompareReferencePayload walks the embedded reference payload for the named
// bundle and compares each file's bytes against the corresponding file under
// the site's .polis/bundles/<bundleName>/. Returns the list of relative paths
// (within the bundle dir) where on-disk content is missing or differs.
//
// Files present on disk that are NOT in the embedded reference are ignored —
// tenants legitimately add private state (drafts, pending comments) into the
// bundle subtree, and we don't want to flag those.
//
// Read-only: does not mutate the filesystem. Patrol's F1 check uses this to
// detect the "right version, wrong bytes" case that checkBundleReferencePayload
// can't see. Per-cycle cost is bounded by the embed size (~21 files / ~500KB
// for pub.polis.core); well under 10ms on any modern filesystem.
func CompareReferencePayload(site storage.Site, bundleName string) ([]string, error) {
	if !site.Valid() {
		return nil, fmt.Errorf("a site handle is required")
	}
	if bundleName == "" {
		return nil, fmt.Errorf("bundleName is required")
	}

	srcRoot := ReferenceRoot + "/" + bundleName
	if _, err := fs.Stat(referencePayload, srcRoot); err != nil {
		return nil, fmt.Errorf("no reference payload for bundle %q: %w", bundleName, err)
	}

	dstRoot := ".polis/bundles/" + bundleName
	ctx := context.Background()

	var mismatches []string
	walkErr := fs.WalkDir(referencePayload, srcRoot, func(embedPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(embedPath, srcRoot), "/")

		want, err := referencePayload.ReadFile(embedPath)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", embedPath, err)
		}
		got, err := site.Store().Get(ctx, dstRoot+"/"+rel)
		if err != nil {
			// Missing on disk or unreadable — both count as a mismatch.
			mismatches = append(mismatches, rel)
			return nil
		}
		if !bytesEqual(got, want) {
			mismatches = append(mismatches, rel)
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return mismatches, nil
}

// bytesEqual is a minimal local equality helper to avoid pulling in
// bytes.Equal just for this one callsite.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
