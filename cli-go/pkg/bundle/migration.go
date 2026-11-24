package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// UpgradeActiveShape flips a custodied site's active_shape from the blog shape
// (v3) or the stream (v4) to `pub.polis.shapes.v5`, the default. Both older
// shapes are no longer supported, and still render exactly as before for a
// site that keeps them (Vincent, 2026-11-22: "if someone has v3 or v4 the
// instance should continue to work and render the same way they do now").
// ⛔ Medic runs it, on polis.pub and other custodied deployments, never Tailor:
// a self-hosted site is never moved, only told how to move.
// Idempotent: returns (false, nil) for a site already on v5, on any other
// shape value, or with a registry from a newer schema, which this build does
// not rewrite.
//
// The cutover is signaled by the active_shape value change, not by a
// registry schema bump; CurrentRegistrySchemaVersion is unaffected.
// Callers should pair this with a re-render so the on-disk HTML
// matches the new shape (hosted Medic does this via FixAction-driven
// re-render; Tailor relies on its existing checkRenderSite pass).
//
// Returns (flipped, err). When flipped is true the caller can log /
// attribute the action; a false-without-error means the tenant didn't
// need migration.
func UpgradeActiveShape(site storage.Site) (bool, error) {
	reg, err := LoadRegistry(site)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !shapeMovedOnCustodied(reg.ActiveShape) || reg.SchemaVersion > CurrentRegistrySchemaVersion {
		return false, nil
	}
	if err := SetActiveShapeName(site, "v5"); err != nil {
		return false, fmt.Errorf("flip active_shape %s→v5: %w", reg.ActiveShape, err)
	}
	return true, nil
}

// MigrateNotificationRulesToRegistry relocates per-content-type
// `notifications` arrays out of bundle.json's types map and into the
// flat top-level `notifications` field on registry.json. Per step-06/
// 6.f.1 (sanctioned exception): rules become private (registry.json
// lives in .polis/, bundle.json is public-visible), bundle-agnostic
// (single set across installed bundles), and flat (the rule's `on`
// field already keys to an event type, so no nesting under content
// type is needed).
//
// Dedup is registry-wins: a rule whose ID already exists in
// registry.Notifications is dropped from bundle.json rather than
// overwritten. Returns (migrated, removed, err) where `migrated`
// counts new IDs added to the registry and `removed` counts rules
// dropped from bundle.json (everything that was there).
//
// The migration is two-step (registry first, then bundle.json) so a
// crash mid-write leaves the registry as the authoritative source —
// the worst-case re-run is a duplicate detection (no rules added, all
// removed) rather than orphaned rules.
func MigrateNotificationRulesToRegistry(site storage.Site) (migrated int, removed int, err error) {
	// ⭐ One key for the read and the write, so the migration rewrites the
	// object it read on any backend.
	bundleData, rdErr := site.Store().Get(context.Background(), BundleJSONKey)
	if rdErr != nil {
		if errors.Is(rdErr, fs.ErrNotExist) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("read bundle.json: %w", rdErr)
	}

	var raw map[string]interface{}
	if jErr := json.Unmarshal(bundleData, &raw); jErr != nil {
		return 0, 0, fmt.Errorf("parse bundle.json: %w", jErr)
	}

	typesRaw, ok := raw["types"].(map[string]interface{})
	if !ok || typesRaw == nil {
		return 0, 0, nil
	}

	type harvest struct {
		typeName string
		rule     NotificationRule
	}
	var harvested []harvest
	for typeName, ctRaw := range typesRaw {
		ct, ok := ctRaw.(map[string]interface{})
		if !ok {
			continue
		}
		notifs, ok := ct["notifications"].([]interface{})
		if !ok || len(notifs) == 0 {
			continue
		}
		nbytes, _ := json.Marshal(notifs)
		var rules []NotificationRule
		if jErr := json.Unmarshal(nbytes, &rules); jErr != nil {
			continue
		}
		for _, r := range rules {
			harvested = append(harvested, harvest{typeName: typeName, rule: r})
		}
	}

	if len(harvested) == 0 {
		return 0, 0, nil
	}

	reg, lErr := LoadRegistryOrInit(site)
	if lErr != nil {
		return 0, 0, fmt.Errorf("load registry: %w", lErr)
	}
	for _, h := range harvested {
		removed++
		if reg.HasNotificationRule(h.rule.ID) {
			continue
		}
		reg.Notifications = append(reg.Notifications, h.rule)
		migrated++
	}

	for typeName, ctRaw := range typesRaw {
		ct, ok := ctRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasNotifs := ct["notifications"]; hasNotifs {
			delete(ct, "notifications")
			typesRaw[typeName] = ct
		}
	}
	raw["types"] = typesRaw

	if sErr := SaveRegistry(site, reg); sErr != nil {
		return 0, 0, fmt.Errorf("save registry: %w", sErr)
	}

	out, mErr := json.MarshalIndent(raw, "", "  ")
	if mErr != nil {
		return 0, 0, fmt.Errorf("marshal cleaned bundle.json: %w", mErr)
	}
	out = append(out, '\n')
	if wErr := atomicfile.Write(context.Background(), site, BundleJSONKey, out, 0644); wErr != nil {
		return 0, 0, fmt.Errorf("write bundle.json: %w", wErr)
	}
	return migrated, removed, nil
}

// BundleJSONKey is the core bundle manifest, relative to the site root.
//
// ⚠️ The migration and its detector read the same key, so the detector cannot
// report "needs migrating" for a file the migration would not find.
const BundleJSONKey = "content/pub.polis.core/bundle.json"

// NotificationRulesNeedMigration reports whether bundle.json carries
// any per-content-type `notifications` array that should be relocated
// to registry.json. Detection-only; no side effects. Used by Patrol
// and Tailor to flag pre-migration tenants.
func NotificationRulesNeedMigration(site storage.Site) bool {
	data, err := site.Store().Get(context.Background(), BundleJSONKey)
	if err != nil {
		return false
	}
	var raw map[string]interface{}
	if json.Unmarshal(data, &raw) != nil {
		return false
	}
	typesRaw, ok := raw["types"].(map[string]interface{})
	if !ok {
		return false
	}
	for _, ctRaw := range typesRaw {
		ct, ok := ctRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if notifs, ok := ct["notifications"].([]interface{}); ok && len(notifs) > 0 {
			return true
		}
	}
	return false
}

// shapeMovedOnCustodied reports whether an active_shape value (short or fully
// qualified) is one Medic moves a custodied site off: the blog shape (v3) and
// the stream (v4).
func shapeMovedOnCustodied(shape string) bool {
	switch shape {
	case "v3", "v4", "pub.polis.shapes.v3", "pub.polis.shapes.v4":
		return true
	}
	return false
}
