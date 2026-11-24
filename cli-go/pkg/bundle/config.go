package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// BundleRegistry is the per-tenant private bundle config. It records which
// bundles are installed in .polis/bundles/, which theme and shape are active,
// the schema version, and the per-shape/per-theme version stamps that drive
// Patrol's force-resync detection.
//
// File location: .polis/bundles/registry.json (private, per-tenant).
//
// Active theme/shape are stored as fully-qualified names so future multi-bundle
// installs can disambiguate items across bundles. Format:
//
//	pub.polis.themes.vice
//	pub.polis.shapes.v3
//
// See ParseFQN for the parser rules.
type BundleRegistry struct {
	SchemaVersion    int               `json:"schema_version"`
	ActiveTheme      string            `json:"active_theme"`
	ActiveShape      string            `json:"active_shape"`
	InstalledBundles []InstalledBundle `json:"installed_bundles"`
	// Notifications holds the per-tenant set of notification rule
	// declarations. step-06/6.f.1 (sanctioned exception): relocated
	// here from the per-content-type Notifications field on bundle.json
	// so they're (a) private (registry.json is in .polis/, bundle.json
	// is public-visible at content/pub.polis.core/), (b) bundle-
	// agnostic (a single set across all installed bundles), and (c)
	// flat (no nesting under content type — the rule's `on` field
	// already keys to an event type). Patrol/Medic migrates existing
	// tenants on each cycle (medic.go migrateNotificationRulesToRegistry).
	//
	// `omitempty` keeps the registry quiet for tenants that have no
	// rules (e.g. someone who deliberately cleared them) — without it
	// SaveRegistry would always emit `"notifications": null`.
	Notifications []NotificationRule `json:"notifications,omitempty"`
	// Extra: members this build does not model, kept on save (extra.go).
	Extra map[string]json.RawMessage `json:"-"`
}

// CurrentRegistrySchemaVersion is the schema version this build writes.
// Increment when adding fields whose absence isn't merely "not set yet."
// step-06/6.f.1: bumped to 2 — `notifications` field added. Older
// registry files (schema=1) are still readable; LoadRegistry doesn't
// gate on schema version, so absence of the field just means "no
// rules saved yet" (DefaultRegistry's set fills in via Medic).
const CurrentRegistrySchemaVersion = 2

// InstalledBundle describes one bundle present in .polis/bundles/, including
// the per-shape and per-theme versions that were installed last sync. These
// drive Patrol's "needs resync" check — when shipped versions advance past
// the recorded ones, Medic re-runs EnsureReferencePayload to refresh the
// fixtures on disk.
//
// Listing-is-activation: presence in installed_bundles[] means the bundle
// is active. There used to be an `Active bool` field here; it was always
// true and never read in production. Step-01 cleanup C1 removed it. A
// shipped Patrol/Medic migration strips the legacy field from existing
// tenants' registry.json — see C4's stripBundleActiveFields.
type InstalledBundle struct {
	Name          string            `json:"name"`
	Path          string            `json:"path"`
	ShapeVersions map[string]string `json:"shape_versions,omitempty"` // shapeName → version installed
	ThemeVersions map[string]string `json:"theme_versions,omitempty"` // themeName → version installed
	// Extra: members this build does not model, kept on save (extra.go).
	Extra map[string]json.RawMessage `json:"-"`
}

// RegistryKey is the tenant's bundle registry, relative to the site root.
//
// ⭐ A KEY, so every reader and writer addresses it the same way on any
// backend. It replaced the exported RegistryPath builder.
const RegistryKey = ".polis/bundles/registry.json"

// LoadRegistry reads the tenant's bundle registry.
// Returns os.ErrNotExist (wrapped) if the file is missing — callers can use
// errors.Is to detect and fall through to a legacy-config path.
func LoadRegistry(site storage.Site) (*BundleRegistry, error) {
	// ⚠️ Absence stays fs.ErrNotExist (wrapped): the store's ErrNotFound wraps
	// it, which is what every caller's errors.Is checks.
	data, err := site.Store().Get(context.Background(), RegistryKey)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", RegistryKey, err)
	}
	var reg BundleRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", RegistryKey, err)
	}
	return &reg, nil
}

// LoadRegistryOrInit returns DefaultRegistry() ONLY when the file is
// genuinely missing on disk; any other LoadRegistry failure (read
// permission denied, malformed JSON, partial write mid-rotation) is
// returned as an error so callers don't silently overwrite a broken-
// but-present registry with defaults.
//
// This replaces the silent `if err != nil { reg = DefaultRegistry() }`
// pattern at the four mutator call sites (EnsureReferencePayload,
// SetActiveThemeName, SetActiveShapeName, AdoptLegacyActiveTheme).
// That pattern was the root cause of the 2026-04-29 discover.polis.pub
// regression: hand-edits via root-ssh left registry.json owned by
// root:root mode 0600, the polis service ran as the polis user and got
// permission-denied on read, the silent fallback wrote DefaultRegistry
// over the user's edits via temp+rename (which works because polis owns
// the parent directory), and active_theme/active_shape were lost.
//
// Callers that need a fresh registry on missing-file (typical
// init/healing paths) should keep using this helper. Callers that need
// to differentiate missing-vs-broken should call LoadRegistry directly
// and inspect the error themselves.
func LoadRegistryOrInit(site storage.Site) (*BundleRegistry, error) {
	reg, err := LoadRegistry(site)
	if err == nil {
		return reg, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		reg := DefaultRegistry()
		reg.ActiveShape = ShapeForNewRegistry(site)
		return reg, nil
	}
	return nil, err
}

// ShapeForNewRegistry is the shape a registry created now should record.
//
// ⭐ A SITE THAT ALREADY HAS POSTS BUT NO REGISTRY PREDATES THE STREAM SHAPE.
// The registry arrived with the stream (v0.63), and every site before it
// rendered the blog shape, so creating its registry is not the moment to
// change how it looks: it keeps the blog (v3), and Tailor's
// active-shape-upgrade reports the stream as available. A new site (polis
// init, hosted signup) has no posts yet and starts on the stream.
//
// On hosted, Medic still moves any v3 tenant to the stream on its next cycle;
// that is the operator's rule for custodied sites, not this function's.
func ShapeForNewRegistry(site storage.Site) string {
	if hasPostSources(site) {
		return "pub.polis.shapes.v3"
	}
	return DefaultRegistry().ActiveShape
}

// hasPostSources reports whether the site holds any post source, in the
// current layout or in the pre-0.58 posts/ directory. A listing error reads as
// "no posts" (the new-site default), because the caller is creating a missing
// registry and must not fail for it.
func hasPostSources(site storage.Site) bool {
	ctx := context.Background()
	for _, prefix := range []string{"content/pub.polis.core/post", "posts"} {
		entries, _ := site.Store().List(ctx, prefix, storage.Recursive())
		for _, e := range entries {
			if !e.IsDir && strings.HasSuffix(e.Key, ".md") && !strings.Contains(e.Key, "/.versions/") {
				return true
			}
		}
	}
	return false
}

// LoadRegistryRaw reads .polis/bundles/registry.json as a raw map. Tri-state
// return mirrors site.LoadWellKnownRaw:
//
//	(nil, nil) — file does not exist (not an error)
//	(nil, err) — file present but read failure or malformed JSON
//	(raw, nil) — file present and parsed OK
//
// Used by integrity checks (patrol F2) that need to distinguish a malformed
// registry from an absent one. Legacy helpers that want "assume-absent-on-error"
// semantics just discard the error.
func LoadRegistryRaw(site storage.Site) (map[string]interface{}, error) {
	data, err := site.Store().Get(context.Background(), RegistryKey)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// SaveRegistry writes the tenant's bundle registry, creating the parent
// directory if needed. The file is written atomically (temp + rename) so a
// crash mid-write doesn't corrupt the file.
func SaveRegistry(site storage.Site, reg *BundleRegistry) error {
	if reg == nil {
		return fmt.Errorf("registry is nil")
	}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	data = append(data, '\n')

	// ⭐⭐⭐ THROUGH THE SITE, so this works on any backend. It was
	// atomicfile.WriteFile on a real path, and that made it the one write in
	// the render path that still needed a filesystem: NewPageRenderer picks a
	// theme when none is set, persists the choice here, and ⛔ on Vercel the
	// filesystem is READ-ONLY outside /tmp — so the FIRST RENDER of a new
	// blob-backed instance failed on it.
	//
	// ⚠️ The MkdirAll that used to precede it is gone because the store's Put
	// creates parent prefixes, and at 0700 under .polis — which is the rule
	// NewSite carries. On a backend with no directories there is nothing to
	// create.
	if err := atomicfile.Write(context.Background(), site, RegistryKey, data, 0600); err != nil {
		return err
	}
	return nil
}

// DefaultRegistry returns a registry initialized for a fresh site that only
// has pub.polis.core installed. Used by polis init.
//
// step-06/6.f.1: Notifications now seeded from DefaultCoreBundle's
// per-content-type rules (flattened into a single list). Same set of
// rules; new home. The fixture bundle.json no longer ships them — see
// DefaultCoreBundle's per-type Notifications fields, which now stay
// empty.
func DefaultRegistry() *BundleRegistry {
	return &BundleRegistry{
		SchemaVersion: CurrentRegistrySchemaVersion,
		ActiveShape:   "pub.polis.shapes.v5",
		InstalledBundles: []InstalledBundle{
			{
				Name: "pub.polis.core",
				Path: ".polis/bundles/pub.polis.core",
			},
		},
		Notifications: defaultNotificationRules(),
	}
}

// defaultNotificationRules returns the canonical set of notification
// rule declarations shipped with pub.polis.core. Same rules that used
// to live under per-content-type Notifications in bundle.json before
// step-06/6.f.1.
func defaultNotificationRules() []NotificationRule {
	falseVal := false
	return []NotificationRule{
		// post events
		{ID: "new-post", On: "pub.polis.post.published", Relevance: "followed_author", Template: "{{actor}} published a new post", Icon: "pencil"},
		{ID: "updated-post", On: "pub.polis.post.republished", Relevance: "followed_author", Template: "{{actor}} updated a post", Icon: "pencil", Enabled: &falseVal},
		// comment events
		{ID: "new-comment", On: "pub.polis.comment.published", Relevance: "target_domain", Template: "{{actor}} commented on {{post_name}}", Icon: "comment"},
		{ID: "blessing-requested", On: "pub.polis.comment.blessing.requested", Relevance: "target_domain", Template: "{{actor}} requested a blessing on {{post_name}}", Icon: "prayer"},
		{ID: "blessing-granted", On: "pub.polis.comment.blessing.granted", Relevance: "source_domain", Template: "{{actor}} blessed your comment", Icon: "check"},
		{ID: "blessing-denied", On: "pub.polis.comment.blessing.denied", Relevance: "source_domain", Template: "{{actor}} denied your comment", Icon: "x"},
		// follow events
		{ID: "new-follower", On: "pub.polis.follow.announced", Relevance: "target_domain", Template: "{{actor}} started following you", Icon: "follow", Batch: "24h"},
		{ID: "lost-follower", On: "pub.polis.follow.removed", Relevance: "target_domain", Template: "{{actor}} unfollowed you", Icon: "unfollow"},
	}
}

// HasNotificationRule reports whether the registry has a rule with the
// given ID. Used by Medic's migration to skip duplicates when merging
// from bundle.json.
func (r *BundleRegistry) HasNotificationRule(id string) bool {
	for _, rule := range r.Notifications {
		if rule.ID == id {
			return true
		}
	}
	return false
}

// AllNotificationRules returns the registry's notification rules
// filtered to enabled-only. Step-06/6.f.1 replacement for
// Bundle.AllNotificationRules — same shape, new home.
func (r *BundleRegistry) AllNotificationRules() []NotificationRule {
	var out []NotificationRule
	for _, rule := range r.Notifications {
		if rule.IsEnabled() {
			out = append(out, rule)
		}
	}
	return out
}

// FindInstalledBundle returns a pointer to the registry entry for the given
// bundle name, or nil if not installed. The returned pointer can be mutated
// in-place; the caller is responsible for SaveRegistry.
func (r *BundleRegistry) FindInstalledBundle(name string) *InstalledBundle {
	for i := range r.InstalledBundles {
		if r.InstalledBundles[i].Name == name {
			return &r.InstalledBundles[i]
		}
	}
	return nil
}

// RecordInstalledVersions stamps the registry with the shape/theme versions
// from the bundle manifest. Called by EnsureReferencePayload after a successful
// install/refresh — Patrol's checkBundleReferencePayload then compares the
// recorded versions against the bundle's shipped versions to detect drift.
func (r *BundleRegistry) RecordInstalledVersions(b *Bundle) {
	entry := r.FindInstalledBundle(b.Name)
	if entry == nil {
		entry = &InstalledBundle{
			Name: b.Name,
			Path: filepath.Join(".polis", "bundles", b.Name),
		}
		r.InstalledBundles = append(r.InstalledBundles, *entry)
		entry = r.FindInstalledBundle(b.Name)
	}
	entry.ShapeVersions = make(map[string]string, len(b.Shapes))
	for name, sh := range b.Shapes {
		if sh != nil {
			entry.ShapeVersions[name] = sh.Version
		}
	}
	entry.ThemeVersions = make(map[string]string, len(b.Themes))
	for name, th := range b.Themes {
		if th != nil {
			entry.ThemeVersions[name] = th.Version
		}
	}
}

// NeedsRefresh returns true if the bundle's currently-shipped shape/theme
// versions differ from what the registry says is installed. Called by
// Patrol's checkBundleReferencePayload — when this returns true, Medic
// re-runs EnsureReferencePayload to land the new fixtures.
//
// Returns true if the bundle is not yet recorded in the registry at all.
func (r *BundleRegistry) NeedsRefresh(b *Bundle) bool {
	entry := r.FindInstalledBundle(b.Name)
	if entry == nil {
		return true
	}
	for name, sh := range b.Shapes {
		if sh == nil {
			continue
		}
		if entry.ShapeVersions[name] != sh.Version {
			return true
		}
	}
	for name, th := range b.Themes {
		if th == nil {
			continue
		}
		if entry.ThemeVersions[name] != th.Version {
			return true
		}
	}
	return false
}

// FQN is a parsed fully-qualified name like pub.polis.themes.vice.
type FQN struct {
	Namespace string // e.g. "pub.polis"
	Kind      string // "themes" or "shapes"
	Name      string // e.g. "vice", "v3"
}

// String reconstructs the fully-qualified name. Satisfies fmt.Stringer
// so FQN values format cleanly when passed through fmt.Print / log helpers.
func (f FQN) String() string {
	return f.Namespace + "." + f.Kind + "." + f.Name
}

// ParseFQN parses a fully-qualified theme or shape reference.
//
// Format: <namespace>.<kind>.<name>, where:
//   - <kind> is "themes" or "shapes" (the parser's pivot points)
//   - <namespace> is everything before the last .<kind>. occurrence
//   - <name> is the segment after .<kind>.
//
// Bundle names cannot contain ".themes." or ".shapes." substrings (enforced by
// Bundle.Validate); if a string carries multiple kind separators we split at
// the last one — i.e. the kind closest to the name segment wins.
//
// Example:
//
//	pub.polis.themes.vice    → {Namespace: "pub.polis", Kind: "themes", Name: "vice"}
//	pub.polis.shapes.v3      → {Namespace: "pub.polis", Kind: "shapes", Name: "v3"}
//	com.alice.themes.cool    → {Namespace: "com.alice", Kind: "themes", Name: "cool"}
func ParseFQN(s string) (FQN, error) {
	if s == "" {
		return FQN{}, fmt.Errorf("empty FQN")
	}
	for _, sep := range []string{".themes.", ".shapes."} {
		idx := strings.LastIndex(s, sep)
		if idx <= 0 {
			continue
		}
		ns := s[:idx]
		// The kind sits between the surrounding dots; rest after sep is the name.
		kind := strings.Trim(sep, ".")
		name := s[idx+len(sep):]
		if name == "" {
			return FQN{}, fmt.Errorf("FQN %q missing name segment", s)
		}
		return FQN{Namespace: ns, Kind: kind, Name: name}, nil
	}
	return FQN{}, fmt.Errorf("FQN %q does not contain .themes. or .shapes. separator", s)
}

// QualifyTheme builds a fully-qualified theme FQN from an unqualified name
// (e.g. "vice") under the default pub.polis namespace. Used by the migration
// from .well-known/polis (which stored bare names) to registry.json, and by
// SetActiveThemeName.
//
// A name already qualified under the default namespace passes through
// unchanged, so "vice" and "pub.polis.themes.vice" both give
// "pub.polis.themes.vice" (qualifying unconditionally once recorded
// "pub.polis.themes.pub.polis.themes.vice" for `polis init --theme <FQN>`).
func QualifyTheme(name string) string {
	if strings.HasPrefix(name, themeFQNPrefix) {
		return name
	}
	return themeFQNPrefix + name
}

// themeFQNPrefix is the default namespace and kind of a theme FQN.
const themeFQNPrefix = "pub.polis.themes."

// IsBareName reports whether s looks like a bare (unqualified) identifier
// that could be qualified as pub.polis.themes.<s> / pub.polis.shapes.<s>.
// Accepts lowercase letters, digits, underscore, and hyphen. Used by
// registry-integrity remediation to distinguish a typo'd FQN ("vise"
// instead of "vice") from random gibberish, so the former can be
// normalized rather than reset.
func IsBareName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// QualifyShape builds a fully-qualified shape FQN. A name already qualified
// is returned as it is: qualifying twice wrote "pub.polis.shapes.pub.polis.shapes.v5"
// into new sites' registries.
func QualifyShape(name string) string {
	const prefix = "pub.polis.shapes."
	if strings.HasPrefix(name, prefix) {
		return name
	}
	return prefix + name
}

// AdoptLegacyActiveTheme is the REGISTRY half of the active_theme migration:
// it records a theme found in a pre-step-01/1e .well-known/polis into the
// tenant's registry.json, creating the registry if missing.
//
//   - If registry.json doesn't exist → create it with DefaultRegistry() values
//     plus the migrated active_theme (fully-qualified).
//   - If registry.json exists and already has active_theme set → do not
//     overwrite.
//   - If registry.json exists but active_theme is empty → populate from legacy.
//
// ⛔ IT DOES NOT TOUCH .well-known/polis. That document belongs to pkg/site,
// whose one writer refuses identity loss; site.MigrateActiveThemeToRegistry
// calls this and then strips the legacy field through that writer. This
// package used to write the file itself (site imports bundle, so it could not
// call the writer) — which put a well-known write outside every guard.
func AdoptLegacyActiveTheme(site storage.Site, legacyTheme string) error {
	if legacyTheme == "" {
		return nil
	}
	// Load existing registry, or start from DefaultRegistry only if the
	// file is genuinely missing. Read errors (permission denied) and
	// parse errors return up so we don't silently overwrite a present-
	// but-broken registry with defaults.
	reg, err := LoadRegistryOrInit(site)
	if err != nil {
		return fmt.Errorf("load registry for migration: %w", err)
	}

	if reg.ActiveTheme == "" {
		reg.ActiveTheme = QualifyTheme(legacyTheme)
	}
	if reg.ActiveShape == "" {
		reg.ActiveShape = ShapeForNewRegistry(site)
	}
	if len(reg.InstalledBundles) == 0 {
		reg.InstalledBundles = DefaultRegistry().InstalledBundles
	}

	if err := SaveRegistry(site, reg); err != nil {
		return fmt.Errorf("save registry: %w", err)
	}

	return nil
}

// GetActiveThemeName resolves the unqualified active theme name for a tenant.
// Reads the registry first; falls back to .well-known/polis active_theme for
// pre-migration sites; returns "" if neither has it set.
//
// This is the function callers should use anywhere they need the active theme
// name. Centralizing avoids the legacy fallback logic being duplicated.
func GetActiveThemeName(site storage.Site) (string, error) {
	if reg, err := LoadRegistry(site); err == nil && reg.ActiveTheme != "" {
		fqn, err := ParseFQN(reg.ActiveTheme)
		if err != nil {
			return "", fmt.Errorf("invalid active_theme in registry: %w", err)
		}
		return fqn.Name, nil
	}
	// Legacy fallback: read from well-known.
	data, err := site.Store().Get(context.Background(), ".well-known/polis")
	if err != nil {
		return "", nil
	}
	var wk map[string]interface{}
	if err := json.Unmarshal(data, &wk); err != nil {
		return "", nil
	}
	if name, _ := wk["active_theme"].(string); name != "" {
		return name, nil
	}
	return "", nil
}

// SetActiveThemeName writes the active theme name into the tenant's registry,
// fully-qualifying as pub.polis.themes.<name>. Creates the registry if missing.
//
// Callers in the webapp/CLI theme-switcher path should use this. Pre-existing
// active_theme entries in .well-known/polis are NOT touched here — that's the
// migration's job (site.MigrateActiveThemeToRegistry).
func SetActiveThemeName(site storage.Site, themeName string) error {
	if themeName == "" {
		return fmt.Errorf("theme name is required")
	}
	reg, err := LoadRegistryOrInit(site)
	if err != nil {
		return fmt.Errorf("load registry to set active_theme: %w", err)
	}
	reg.ActiveTheme = QualifyTheme(themeName)
	return SaveRegistry(site, reg)
}

// GetActiveShapeName mirrors GetActiveThemeName for the active shape.
// Defaults to "v5" when the registry names no shape. (It was "v3", then "v4";
// each cutover moved it forward, and on a custodied deployment Medic's
// upgradeActiveShape moves a registry that names an older shape.)
//
// ⭐ With NO registry at all, the answer is the shape the registry will record
// when it is created (ShapeForNewRegistry): the blog for a site that already
// has posts, so reading and creating agree.
func GetActiveShapeName(site storage.Site) (string, error) {
	reg, err := LoadRegistry(site)
	if err == nil && reg.ActiveShape != "" {
		fqn, err := ParseFQN(reg.ActiveShape)
		if err != nil {
			return "", fmt.Errorf("invalid active_shape in registry: %w", err)
		}
		return fqn.Name, nil
	}
	if err != nil && errors.Is(err, os.ErrNotExist) {
		if fqn, perr := ParseFQN(ShapeForNewRegistry(site)); perr == nil {
			return fqn.Name, nil
		}
	}
	return "v5", nil
}

// SetActiveShapeName writes the active shape name into the tenant's registry,
// fully-qualifying as pub.polis.shapes.<name>. Mirrors SetActiveThemeName.
// Creates the registry if missing.
func SetActiveShapeName(site storage.Site, shapeName string) error {
	if shapeName == "" {
		return fmt.Errorf("shape name is required")
	}
	reg, err := LoadRegistryOrInit(site)
	if err != nil {
		return fmt.Errorf("load registry to set active_shape: %w", err)
	}
	reg.ActiveShape = QualifyShape(shapeName)
	return SaveRegistry(site, reg)
}
