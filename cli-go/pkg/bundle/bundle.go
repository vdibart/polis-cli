// Package bundle provides types and loading for polis bundles.
//
// A bundle is a namespaced package that declares content types, events,
// rendering, and storage layout. The initial bundle pub.polis.core ships
// with four content types: post, comment, follow, and feed.
package bundle

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/notification"
)

// StreamShapeVersion is the single source of truth for the stream shape's
// version. Used by:
//   - The default-bundle declaration below (DefaultCoreBundle), so Patrol/Medic
//     resync triggers when this constant bumps.
//   - The render layer to cache-bust /stream.js in served HTML
//     (`<script src="/stream.js?v={{stream_shape_version}}" defer>`), so
//     browsers pick up new controller code instead of the stale cached copy.
//
// Bump this constant when shipping any user-visible change in stream.html,
// stream.js, stream-*.html partials, or stream.css. Granular history lives
// in git: `git log -- cli-go/pkg/bundle/bundle.go cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/`.
// Inline shape/theme comments below describe CURRENT invariants, not history.
//
// (The wire-format shape name remains "pub.polis.shapes.v4" / map key "v4"
// for backwards compatibility with installed tenants.)
const StreamShapeVersion = "1.7.99"

// Bundle represents a bundle.json declaration.
type Bundle struct {
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Description string                 `json:"description,omitempty"`
	Handler     Handler                `json:"handler"`
	DS          *DSConfig              `json:"ds,omitempty"`
	Types       map[string]ContentType `json:"types"`
	Shapes      map[string]*Shape      `json:"shapes,omitempty"`
	Themes      map[string]*Theme      `json:"themes,omitempty"`
	Artifacts   []string               `json:"artifacts,omitempty"`
}

// Shape declares a rendering approach: a set of templates and supporting
// partials that produce HTML for a class of polis output (e.g., blog page
// rendering, v4 infinity-stream).
type Shape struct {
	Name       string            `json:"name"`
	Version    string            `json:"version"`
	Entry      map[string]string `json:"entry"`
	Partials   []string          `json:"partials,omitempty"`
	DefaultCSS string            `json:"default_css,omitempty"`
}

// Theme declares a CSS-only presentation layer compatible with one or more
// shapes. Metadata lives in bundle.json; the CSS file lives in the bundle's
// themes/<name>/ directory.
//
// DisplayName is a human-facing label used by UI pickers. When empty, the
// picker falls back to Name. Used to keep tenant-data (registry FQNs,
// directory names, Medic remediation logic) stable while presenting a
// friendlier label to users — e.g. studio13-nk is stored internally but
// labeled "studio13" to match the original brand.
type Theme struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	CSS              string   `json:"css"`
	DisplayName      string   `json:"display_name,omitempty"`
	CompatibleShapes []string `json:"compatible_shapes,omitempty"`
}

// Handler declares how the system invokes this bundle.
type Handler struct {
	Type string `json:"type"` // "builtin", "executable", "http"
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
}

// DSConfig holds discovery service integration settings.
type DSConfig struct {
	SubscribesTo []string `json:"subscribes_to,omitempty"`
}

// ContentType declares a single content type within a bundle.
//
// Private types (R18-14, 2026-05-18): the `Private` flag marks
// content types whose data is owner-private — reads must require
// auth even on the v1 API. The webapp's
// `webapp/internal/api/router.go:isPrivateContentType` ALLOWLIST
// must stay in parity with every type that ships `Private: true`.
// `TestIsPrivateContentType_Parity` in the api package asserts this
// invariant by walking `DefaultCoreBundle().Types` and failing if
// any `Private: true` type is missing from the allowlist (or vice
// versa). When a new private content type is added to a bundle,
// adding it to `isPrivateContentType` is therefore CI-enforced, not
// documentation-enforced.
type ContentType struct {
	Dir           string             `json:"dir"`
	Mount         string             `json:"mount,omitempty"`
	Renderer      string             `json:"renderer,omitempty"`
	Storage       *StorageConfig     `json:"storage,omitempty"`
	Emits         []string           `json:"emits,omitempty"`
	Notifications []NotificationRule `json:"notifications,omitempty"`
	Private       bool               `json:"private,omitempty"`
}

// StorageConfig controls how content files are organized on disk.
type StorageConfig struct {
	Pattern    string `json:"pattern"`              // "dated" or "flat"
	DateFormat string `json:"date_format,omitempty"` // e.g. "YYYYMMDD"
	Versions   bool   `json:"versions,omitempty"`
}

// NotificationRule declares when and how to notify users about events.
type NotificationRule struct {
	ID        string `json:"id"`
	On        string `json:"on"`
	Relevance string `json:"relevance"`
	Template  string `json:"template"`
	Icon      string `json:"icon,omitempty"`
	Enabled   *bool  `json:"enabled,omitempty"` // nil = true (default enabled)
	Batch     string `json:"batch,omitempty"`
}

// IsEnabled returns whether this notification rule is active.
func (n *NotificationRule) IsEnabled() bool {
	if n.Enabled == nil {
		return true
	}
	return *n.Enabled
}

// LoadBundle reads and parses a bundle.json file.
func LoadBundle(path string) (*Bundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bundle %s: %w", path, err)
	}

	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse bundle %s: %w", path, err)
	}

	if err := b.Validate(); err != nil {
		return nil, fmt.Errorf("invalid bundle %s: %w", path, err)
	}

	return &b, nil
}

// MergeDefaults adds any content types, shapes, or themes from defaults that
// are missing in b. Existing entries in b are never overwritten. Returns true
// if any entries were added.
func (b *Bundle) MergeDefaults(defaults *Bundle) bool {
	added := false

	if b.Types == nil {
		b.Types = make(map[string]ContentType)
	}
	for name, ct := range defaults.Types {
		if _, exists := b.Types[name]; !exists {
			b.Types[name] = ct
			added = true
		}
	}

	if defaults.Shapes != nil {
		if b.Shapes == nil {
			b.Shapes = make(map[string]*Shape)
		}
		for name, sh := range defaults.Shapes {
			if _, exists := b.Shapes[name]; !exists {
				b.Shapes[name] = sh
				added = true
			}
		}
	}

	if defaults.Themes != nil {
		if b.Themes == nil {
			b.Themes = make(map[string]*Theme)
		}
		for name, th := range defaults.Themes {
			if _, exists := b.Themes[name]; !exists {
				b.Themes[name] = th
				added = true
			}
		}
	}

	return added
}

// SaveBundle writes a bundle to disk as JSON.
func SaveBundle(path string, b *Bundle) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create bundle dir: %w", err)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bundle: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// Validate checks that the bundle declaration is well-formed.
func (b *Bundle) Validate() error {
	if b.Name == "" {
		return fmt.Errorf("bundle name is required")
	}
	// Reserved substrings used by the FQN parser for theme/shape references
	// (e.g. pub.polis.themes.vice, pub.polis.shapes.v3).
	if strings.Contains(b.Name, ".themes.") {
		return fmt.Errorf("bundle name %q cannot contain reserved substring %q", b.Name, ".themes.")
	}
	if strings.Contains(b.Name, ".shapes.") {
		return fmt.Errorf("bundle name %q cannot contain reserved substring %q", b.Name, ".shapes.")
	}
	if b.Version == "" {
		return fmt.Errorf("bundle version is required")
	}
	if b.Handler.Type == "" {
		return fmt.Errorf("handler type is required")
	}
	switch b.Handler.Type {
	case "builtin":
		// no extra fields needed
	case "executable":
		if b.Handler.Path == "" {
			return fmt.Errorf("executable handler requires path")
		}
	case "http":
		if b.Handler.URL == "" {
			return fmt.Errorf("http handler requires url")
		}
	default:
		return fmt.Errorf("unknown handler type: %s", b.Handler.Type)
	}

	dirs := make(map[string]bool)
	mounts := make(map[string]bool)
	notifIDs := make(map[string]bool)

	for typeName, ct := range b.Types {
		if ct.Dir == "" {
			return fmt.Errorf("type %s: dir is required", typeName)
		}
		if dirs[ct.Dir] {
			return fmt.Errorf("type %s: duplicate dir %q", typeName, ct.Dir)
		}
		dirs[ct.Dir] = true

		if ct.Mount != "" {
			if mounts[ct.Mount] {
				return fmt.Errorf("type %s: duplicate mount %q", typeName, ct.Mount)
			}
			mounts[ct.Mount] = true
		}

		for _, n := range ct.Notifications {
			if n.ID == "" {
				return fmt.Errorf("type %s: notification missing id", typeName)
			}
			if notifIDs[n.ID] {
				return fmt.Errorf("type %s: duplicate notification id %q", typeName, n.ID)
			}
			notifIDs[n.ID] = true
			if n.On == "" {
				return fmt.Errorf("type %s: notification %s missing on field", typeName, n.ID)
			}
			if n.Relevance == "" {
				return fmt.Errorf("type %s: notification %s missing relevance", typeName, n.ID)
			}
			if n.Template == "" {
				return fmt.Errorf("type %s: notification %s missing template", typeName, n.ID)
			}
		}
	}

	for shapeName, sh := range b.Shapes {
		if sh == nil {
			return fmt.Errorf("shape %s: declaration is nil", shapeName)
		}
		if sh.Name == "" {
			return fmt.Errorf("shape %s: name is required", shapeName)
		}
		if sh.Name != shapeName {
			return fmt.Errorf("shape %s: name %q does not match map key", shapeName, sh.Name)
		}
		if sh.Version == "" {
			return fmt.Errorf("shape %s: version is required", shapeName)
		}
		if len(sh.Entry) == 0 {
			return fmt.Errorf("shape %s: entry map is required and non-empty", shapeName)
		}
	}

	for themeName, th := range b.Themes {
		if th == nil {
			return fmt.Errorf("theme %s: declaration is nil", themeName)
		}
		if th.Name == "" {
			return fmt.Errorf("theme %s: name is required", themeName)
		}
		if th.Name != themeName {
			return fmt.Errorf("theme %s: name %q does not match map key", themeName, th.Name)
		}
		if th.Version == "" {
			return fmt.Errorf("theme %s: version is required", themeName)
		}
		if th.CSS == "" {
			return fmt.Errorf("theme %s: css is required", themeName)
		}
	}

	return nil
}

// ContentDir returns the source content directory for a type, relative to site root.
// Example: ContentDir("pub.polis.post") → "content/pub.polis.core/post"
func (b *Bundle) ContentDir(typeName string) (string, error) {
	ct, ok := b.Types[typeName]
	if !ok {
		return "", fmt.Errorf("unknown content type: %s", typeName)
	}
	return filepath.Join("content", b.Name, ct.Dir), nil
}

// MountDir returns the rendered output directory for a type, relative to site root.
// Returns the mount path without the leading slash.
// Example: MountDir("pub.polis.post") → "posts"
func (b *Bundle) MountDir(typeName string) (string, error) {
	ct, ok := b.Types[typeName]
	if !ok {
		return "", fmt.Errorf("unknown content type: %s", typeName)
	}
	if ct.Mount == "" {
		return "", fmt.Errorf("type %s has no mount point", typeName)
	}
	return strings.TrimPrefix(ct.Mount, "/"), nil
}

// PrivateDir returns the private state directory for a type, relative to site root.
// Example: PrivateDir("pub.polis.post") → ".polis/bundles/pub.polis.core/posts"
// Note: uses plural form (posts, comments) to distinguish from public singular dirs.
func (b *Bundle) PrivateDir(typeName string) (string, error) {
	ct, ok := b.Types[typeName]
	if !ok {
		return "", fmt.Errorf("unknown content type: %s", typeName)
	}
	// Private dirs use plural: post→posts, comment→comments
	plural := ct.Dir + "s"
	return filepath.Join(".polis", "bundles", b.Name, plural), nil
}

// ShapeDir returns the per-tenant installed directory for a shape's templates,
// relative to site root.
// Example: ShapeDir("v3") → ".polis/bundles/pub.polis.core/shapes/v3"
func (b *Bundle) ShapeDir(shapeName string) (string, error) {
	if _, ok := b.Shapes[shapeName]; !ok {
		return "", fmt.Errorf("unknown shape: %s", shapeName)
	}
	return filepath.Join(".polis", "bundles", b.Name, "shapes", shapeName), nil
}

// GetShape returns the Shape declaration for the given name, or an error if
// not declared in this bundle.
func (b *Bundle) GetShape(name string) (*Shape, error) {
	sh, ok := b.Shapes[name]
	if !ok {
		return nil, fmt.Errorf("shape %q not declared in bundle %s", name, b.Name)
	}
	return sh, nil
}

// ThemeDir returns the per-tenant installed directory for a theme's assets,
// relative to site root.
// Example: ThemeDir("vice") → ".polis/bundles/pub.polis.core/themes/vice"
func (b *Bundle) ThemeDir(themeName string) (string, error) {
	if _, ok := b.Themes[themeName]; !ok {
		return "", fmt.Errorf("unknown theme: %s", themeName)
	}
	return filepath.Join(".polis", "bundles", b.Name, "themes", themeName), nil
}

// GetTheme returns the Theme declaration for the given name, or an error if
// not declared in this bundle.
func (b *Bundle) GetTheme(name string) (*Theme, error) {
	th, ok := b.Themes[name]
	if !ok {
		return nil, fmt.Errorf("theme %q not declared in bundle %s", name, b.Name)
	}
	return th, nil
}

// SourceToMountPath maps a source-relative path to its current mount-relative path.
// For example, "content/pub.polis.core/post/20260302/slug.html" becomes
// "posts/20260302/slug.html" using the post type's mount config.
// Returns the original path unchanged if no mapping is found.
func (b *Bundle) SourceToMountPath(path string) string {
	for _, ct := range b.Types {
		if ct.Mount == "" {
			continue
		}
		contentPrefix := filepath.Join("content", b.Name, ct.Dir) + "/"
		if strings.HasPrefix(path, contentPrefix) {
			rest := strings.TrimPrefix(path, contentPrefix)
			mountDir := strings.TrimPrefix(ct.Mount, "/")
			return filepath.Join(mountDir, rest)
		}
	}
	return path
}

// AllEmittedEvents returns all event names emitted by all types in this bundle.
func (b *Bundle) AllEmittedEvents() []string {
	var events []string
	for _, ct := range b.Types {
		events = append(events, ct.Emits...)
	}
	return events
}

// AllNotificationRules returns all enabled notification rules across all types.
func (b *Bundle) AllNotificationRules() []NotificationRule {
	var rules []NotificationRule
	for _, ct := range b.Types {
		for _, r := range ct.Notifications {
			if r.IsEnabled() {
				rules = append(rules, r)
			}
		}
	}
	return rules
}

// MatchesEvent returns true if the given event name matches any subscribes_to pattern.
func (b *Bundle) MatchesEvent(eventName string) bool {
	if b.DS == nil {
		return false
	}
	for _, pattern := range b.DS.SubscribesTo {
		if matchGlob(pattern, eventName) {
			return true
		}
	}
	return false
}

// TypeForEvent returns the content type name that emits the given event, or "" if none.
func (b *Bundle) TypeForEvent(eventName string) string {
	for typeName, ct := range b.Types {
		for _, e := range ct.Emits {
			if e == eventName {
				return typeName
			}
		}
	}
	return ""
}

// matchGlob performs simple glob matching where * matches any suffix.
func matchGlob(pattern, s string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(s, prefix)
	}
	return pattern == s
}

// iconLookup maps bundle icon names to display characters.
var iconLookup = map[string]string{
	"pencil":   "\U0001F4DD",
	"comment":  "\U0001F4AC",
	"prayer":   "\U0001F514",
	"check":    "\u2713",
	"x":        "\u2717",
	"follow":   "\U0001F464",
	"unfollow": "\U0001F464",
}

// NotificationRules converts all bundle notification declarations into notification.Rule structs.
func (b *Bundle) NotificationRules() []notification.Rule {
	var rules []notification.Rule
	for _, ct := range b.Types {
		for _, nr := range ct.Notifications {
			enabled := true
			if nr.Enabled != nil {
				enabled = *nr.Enabled
			}
			icon := nr.Icon
			if mapped, ok := iconLookup[nr.Icon]; ok {
				icon = mapped
			}
			rule := notification.Rule{
				ID:        nr.ID,
				EventType: nr.On,
				Enabled:   enabled,
				Filter:    notification.RuleFilter{Relevance: nr.Relevance},
				Template:  notification.RuleTemplate{Icon: icon, Message: nr.Template},
			}
			if nr.Batch != "" {
				rule.Batch = true
				rule.BatchWindow = nr.Batch
			}
			rules = append(rules, rule)
		}
	}
	return rules
}

// DefaultCoreBundle returns the default pub.polis.core bundle definition.
//
// step-06/6.f.1: per-content-type Notifications removed from this
// definition. They now live in BundleRegistry.Notifications (private
// per-tenant config under .polis/bundles/registry.json) — see
// defaultNotificationRules in config.go for the canonical set.
// Patrol/Medic relocates notifications from existing tenants'
// bundle.json on each healing cycle (see medic.go
// migrateNotificationRulesToRegistry).
func DefaultCoreBundle() *Bundle {
	return &Bundle{
		Name:        "pub.polis.core",
		Version:     "1.0.0",
		Description: "Core polis content types",
		Handler:     Handler{Type: "builtin"},
		DS:          &DSConfig{SubscribesTo: []string{"pub.polis.*"}},
		Types: map[string]ContentType{
			"pub.polis.post": {
				Dir:      "post",
				Mount:    "/posts",
				Renderer: "html",
				Storage:  &StorageConfig{Pattern: "dated", DateFormat: "YYYYMMDD", Versions: true},
				Emits:    []string{"pub.polis.post.published", "pub.polis.post.republished", "pub.polis.post.removed"},
			},
			"pub.polis.comment": {
				Dir:      "comment",
				Mount:    "/comments",
				Renderer: "html",
				Storage:  &StorageConfig{Pattern: "dated", DateFormat: "YYYYMMDD", Versions: false},
				Emits: []string{
					"pub.polis.comment.published", "pub.polis.comment.republished",
					"pub.polis.comment.blessing.requested", "pub.polis.comment.blessing.granted", "pub.polis.comment.blessing.denied",
				},
			},
			"pub.polis.follow": {
				Dir:     "follow",
				Mount:   "/follow",
				Emits:   []string{"pub.polis.follow.announced", "pub.polis.follow.removed"},
				Private: true, // owner's outbound follow list — auth required on v1 API reads
			},
			"pub.polis.feed": {
				Dir:      "feed",
				Mount:    "/feed",
				Renderer: "html",
				Private:  true, // local feed cache — auth required on v1 API reads
			},
			"pub.polis.dm": {
				Dir:     "dm",
				Storage: &StorageConfig{Pattern: "flat"},
				Private: true, // encrypted at rest; previews decrypted server-side only for owner
			},
			"pub.polis.tag": {
				Dir:     "tag",
				Mount:   "/tags",
				Storage: &StorageConfig{Pattern: "flat"},
				Emits:   []string{"pub.polis.tag.applied", "pub.polis.tag.removed"},
			},
			"pub.polis.theme": {
				// Singular Dir matches the convention of other content types
				// (post, comment, follow, dm, tag). PrivateDir would yield
				// .polis/bundles/<bundle>/themes (note: PrivateDir uses plural
				// suffix, so "theme" → "themes" — matches ThemeDir output).
				Dir:     "theme",
				Storage: &StorageConfig{Pattern: "flat"},
				Emits:   []string{"pub.polis.theme.installed", "pub.polis.theme.removed"},
			},
		},
		Shapes: map[string]*Shape{
			"v3": {
				Name:    "v3",
				Version: "1.0.0",
				Entry: map[string]string{
					"post":           "post.html",
					"comment":        "comment.html",
					"comment_inline": "comment-inline.html",
					"index":          "index.html",
					"archive":        "posts.html",
					"tag":            "tag.html",
					"tag_index":      "tag-index.html",
				},
				Partials: []string{
					"snippets/about.html",
					"snippets/also-reading.html",
					"snippets/blessed-comment.html",
					"snippets/comment-item.html",
					"snippets/polis-widget.html",
					"snippets/post-item.html",
				},
				DefaultCSS: "themes/_shared/base.css",
			},
			"v4": {
				// v4 SHAPE — stream-shell artifact. Each per-post URL renders
				// as a static HTML file via `polis render`: stream shell +
				// focus post inlined + adjacent sibling excerpts (newer-
				// direction rail above, older-direction rail below). Same
				// artifact whether hosted or self-hosted; logged-out / public
				// view of a polis site.
				//
				// Cross-tenant body-extraction protocol: the focus body MUST
				// remain wrapped in <main class="focus-content"
				// data-polis-focus="true">. Cross-tenant fetchers select on
				// that <main> element. DO NOT rename or restructure without
				// coordinating a protocol change.
				//
				// Bumping StreamShapeVersion triggers NeedsRefresh on every
				// tenant's next Patrol cycle → Medic EnsureReferencePayload
				// installs the new SHAPE files + cache-busts /stream.js via
				// the controller-script ?v= query param. History lives in
				// git: see the StreamShapeVersion doc comment at the top of
				// this file for the recommended log query.
				Name:    "v4",
				Version: StreamShapeVersion,
				Entry: map[string]string{
					"stream":  "stream.html",
					"post":    "stream-post.html",
					"comment": "stream-comment.html",
					"profile": "stream-profile.html",
					"mention": "stream-mention.html",
				},
				Partials: []string{
					"snippets/sentence-filter.html",
				},
				DefaultCSS: "themes/_shared/base.css",
			},
		},
		Themes: map[string]*Theme{
			// _shared holds CSS extracted from the per-theme files —
			// rules byte-identical across the extraction-pool themes
			// (especial, especial-light, sols, studio13, vice, zane,
			// turbo) live here once. Each per-theme file overrides
			// only what's distinctive. _shared is a system theme,
			// not user-selectable (theme.isValidTheme excludes it).
			"_shared": {
				Name:             "_shared",
				Version:          "1.1.0",
				CSS:              "base.css",
				CompatibleShapes: []string{"pub.polis.shapes.v3", "pub.polis.shapes.v4"},
			},
			"especial": {
				Name:             "especial",
				Version:          "1.1.0",
				CSS:              "especial.css",
				CompatibleShapes: []string{"pub.polis.shapes.v3", "pub.polis.shapes.v4"},
			},
			"especial-light": {
				Name:             "especial-light",
				Version:          "1.1.0",
				CSS:              "especial-light.css",
				CompatibleShapes: []string{"pub.polis.shapes.v3", "pub.polis.shapes.v4"},
			},
			"sols": {
				Name:             "sols",
				Version:          "1.1.0",
				CSS:              "sols.css",
				CompatibleShapes: []string{"pub.polis.shapes.v3", "pub.polis.shapes.v4"},
			},
			// studio13 + studio13-nk: paired themes preserving brand
			// identity across the v3→v4 split. studio13 (stream-only) is
			// CSS-only against the shared markup selectors. studio13-nk
			// (blog-only) keeps the HTML overrides + distinctive CSS that
			// pre-rename studio13 tenants depended on. DisplayName on
			// studio13-nk renders as "studio13" in the picker — the
			// rename is tenant-data-hygiene, not a branding change. The
			// shape-aware picker filter (CompatibleShapes) ensures only
			// one of the two appears for any given tenant's active shape.
			"studio13": {
				Name:             "studio13",
				Version:          "1.0.0",
				CSS:              "studio13.css",
				CompatibleShapes: []string{"pub.polis.shapes.v4"},
			},
			"studio13-nk": {
				Name:             "studio13-nk",
				Version:          "1.0.1",
				CSS:              "studio13-nk.css",
				DisplayName:      "studio13",
				CompatibleShapes: []string{"pub.polis.shapes.v3"},
			},
			"turbo": {
				Name:             "turbo",
				Version:          "1.1.0",
				CSS:              "turbo.css",
				CompatibleShapes: []string{"pub.polis.shapes.v3", "pub.polis.shapes.v4"},
			},
			"vice": {
				Name:             "vice",
				Version:          "1.1.0",
				CSS:              "vice.css",
				CompatibleShapes: []string{"pub.polis.shapes.v3", "pub.polis.shapes.v4"},
			},
			"zane": {
				Name:             "zane",
				Version:          "1.1.0",
				CSS:              "zane.css",
				CompatibleShapes: []string{"pub.polis.shapes.v3", "pub.polis.shapes.v4"},
			},
		},
		Artifacts: []string{"index.jsonl"},
	}
}
