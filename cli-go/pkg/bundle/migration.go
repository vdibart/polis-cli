package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
)

// UpgradeActiveShape flips a tenant's active_shape from
// `pub.polis.shapes.v3` to `pub.polis.shapes.v4` on the v4 cutover.
// Idempotent: returns (false, nil) for tenants already on v4, or on any
// other (empty/unknown) shape value — we only touch tenants we know are
// on the legacy default.
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
func UpgradeActiveShape(siteDir string) (bool, error) {
	reg, err := LoadRegistry(siteDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if reg.ActiveShape != "pub.polis.shapes.v3" {
		return false, nil
	}
	if err := SetActiveShapeName(siteDir, "v4"); err != nil {
		return false, fmt.Errorf("flip active_shape v3→v4: %w", err)
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
func MigrateNotificationRulesToRegistry(siteDir string) (migrated int, removed int, err error) {
	bundlePath := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	bundleData, rdErr := os.ReadFile(bundlePath)
	if rdErr != nil {
		if os.IsNotExist(rdErr) {
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

	reg, lErr := LoadRegistryOrInit(siteDir)
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

	if sErr := SaveRegistry(siteDir, reg); sErr != nil {
		return 0, 0, fmt.Errorf("save registry: %w", sErr)
	}

	out, mErr := json.MarshalIndent(raw, "", "  ")
	if mErr != nil {
		return 0, 0, fmt.Errorf("marshal cleaned bundle.json: %w", mErr)
	}
	out = append(out, '\n')
	if wErr := atomicfile.WriteFile(bundlePath, out, 0644); wErr != nil {
		return 0, 0, fmt.Errorf("write bundle.json: %w", wErr)
	}
	return migrated, removed, nil
}

// NotificationRulesNeedMigration reports whether bundle.json carries
// any per-content-type `notifications` array that should be relocated
// to registry.json. Detection-only; no side effects. Used by Patrol
// and Tailor to flag pre-migration tenants.
func NotificationRulesNeedMigration(siteDir string) bool {
	bundlePath := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	data, err := os.ReadFile(bundlePath)
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
