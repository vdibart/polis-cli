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

// Bundle represents a bundle.json declaration.
type Bundle struct {
	Name        string                 `json:"name"`
	Version     string                 `json:"version"`
	Description string                 `json:"description,omitempty"`
	Handler     Handler                `json:"handler"`
	DS          *DSConfig              `json:"ds,omitempty"`
	Types       map[string]ContentType `json:"types"`
	Artifacts   []string               `json:"artifacts,omitempty"`
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
type ContentType struct {
	Dir           string              `json:"dir"`
	Mount         string              `json:"mount,omitempty"`
	Renderer      string              `json:"renderer,omitempty"`
	Storage       *StorageConfig      `json:"storage,omitempty"`
	Emits         []string            `json:"emits,omitempty"`
	Notifications []NotificationRule  `json:"notifications,omitempty"`
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
// Example: PrivateDir("pub.polis.post") → ".polis/content/pub.polis.core/posts"
// Note: uses plural form (posts, comments) to distinguish from public singular dirs.
func (b *Bundle) PrivateDir(typeName string) (string, error) {
	ct, ok := b.Types[typeName]
	if !ok {
		return "", fmt.Errorf("unknown content type: %s", typeName)
	}
	// Private dirs use plural: post→posts, comment→comments
	plural := ct.Dir + "s"
	return filepath.Join(".polis", "content", b.Name, plural), nil
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
func DefaultCoreBundle() *Bundle {
	falseVal := false
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
				Notifications: []NotificationRule{
					{ID: "new-post", On: "pub.polis.post.published", Relevance: "followed_author", Template: "{{actor}} published a new post", Icon: "pencil"},
					{ID: "updated-post", On: "pub.polis.post.republished", Relevance: "followed_author", Template: "{{actor}} updated a post", Icon: "pencil", Enabled: &falseVal},
				},
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
				Notifications: []NotificationRule{
					{ID: "new-comment", On: "pub.polis.comment.published", Relevance: "target_domain", Template: "{{actor}} commented on {{post_name}}", Icon: "comment"},
					{ID: "blessing-requested", On: "pub.polis.comment.blessing.requested", Relevance: "target_domain", Template: "{{actor}} requested a blessing on {{post_name}}", Icon: "prayer"},
					{ID: "blessing-granted", On: "pub.polis.comment.blessing.granted", Relevance: "source_domain", Template: "{{actor}} blessed your comment", Icon: "check"},
					{ID: "blessing-denied", On: "pub.polis.comment.blessing.denied", Relevance: "source_domain", Template: "{{actor}} denied your comment", Icon: "x"},
				},
			},
			"pub.polis.follow": {
				Dir:   "follow",
				Mount: "/follow",
				Emits: []string{"pub.polis.follow.announced", "pub.polis.follow.removed"},
				Notifications: []NotificationRule{
					{ID: "new-follower", On: "pub.polis.follow.announced", Relevance: "target_domain", Template: "{{actor}} started following you", Icon: "follow", Batch: "24h"},
					{ID: "lost-follower", On: "pub.polis.follow.removed", Relevance: "target_domain", Template: "{{actor}} unfollowed you", Icon: "unfollow"},
				},
			},
			"pub.polis.feed": {
				Dir:      "feed",
				Mount:    "/feed",
				Renderer: "html",
			},
			"pub.polis.dm": {
				Dir:     "dm",
				Storage: &StorageConfig{Pattern: "flat"},
			},
		},
		Artifacts: []string{"index.jsonl"},
	}
}
