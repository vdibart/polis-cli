package bundle

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
func EnsureReferencePayload(siteDir, bundleName string) error {
	if siteDir == "" {
		return fmt.Errorf("siteDir is required")
	}
	if bundleName == "" {
		return fmt.Errorf("bundleName is required")
	}

	srcRoot := ReferenceRoot + "/" + bundleName
	if _, err := fs.Stat(referencePayload, srcRoot); err != nil {
		return fmt.Errorf("no reference payload for bundle %q: %w", bundleName, err)
	}

	dstRoot := filepath.Join(siteDir, ".polis", "bundles", bundleName)
	if err := os.MkdirAll(dstRoot, 0700); err != nil {
		return fmt.Errorf("create bundle dir: %w", err)
	}

	walkErr := fs.WalkDir(referencePayload, srcRoot, func(embedPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(embedPath, srcRoot)
		rel = strings.TrimPrefix(rel, "/")
		dstPath := filepath.Join(dstRoot, rel)

		if d.IsDir() {
			if rel == "" {
				return nil // root already created
			}
			return os.MkdirAll(dstPath, 0755)
		}

		data, err := referencePayload.ReadFile(embedPath)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", embedPath, err)
		}
		// Skip the write when on-disk content already matches the embedded
		// reference. Saves mtime churn on every Medic cycle and lets file
		// watchers / mtime-based diff tools stay quiet on no-op syncs.
		if existing, err := os.ReadFile(dstPath); err == nil && bytesEqual(existing, data) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			return fmt.Errorf("create parent dir for %s: %w", dstPath, err)
		}
		if err := os.WriteFile(dstPath, data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", dstPath, err)
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
		reg, err := LoadRegistryOrInit(siteDir)
		if err != nil {
			return fmt.Errorf("load registry to record installed versions: %w", err)
		}
		reg.RecordInstalledVersions(DefaultCoreBundle())
		if err := SaveRegistry(siteDir, reg); err != nil {
			return fmt.Errorf("record installed versions: %w", err)
		}
	}
	return nil
}

// CompareReferencePayload walks the embedded reference payload for the named
// bundle and compares each file's bytes against the corresponding file under
// <siteDir>/.polis/bundles/<bundleName>/. Returns the list of relative paths
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
func CompareReferencePayload(siteDir, bundleName string) ([]string, error) {
	if siteDir == "" {
		return nil, fmt.Errorf("siteDir is required")
	}
	if bundleName == "" {
		return nil, fmt.Errorf("bundleName is required")
	}

	srcRoot := ReferenceRoot + "/" + bundleName
	if _, err := fs.Stat(referencePayload, srcRoot); err != nil {
		return nil, fmt.Errorf("no reference payload for bundle %q: %w", bundleName, err)
	}

	dstRoot := filepath.Join(siteDir, ".polis", "bundles", bundleName)

	var mismatches []string
	walkErr := fs.WalkDir(referencePayload, srcRoot, func(embedPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(embedPath, srcRoot)
		rel = strings.TrimPrefix(rel, "/")
		dstPath := filepath.Join(dstRoot, rel)

		want, err := referencePayload.ReadFile(embedPath)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", embedPath, err)
		}
		got, err := os.ReadFile(dstPath)
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

