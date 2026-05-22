package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultCoreBundleIsValid(t *testing.T) {
	b := DefaultCoreBundle()
	if err := b.Validate(); err != nil {
		t.Fatalf("default core bundle should be valid: %v", err)
	}
}

func TestLoadBundle(t *testing.T) {
	dir := t.TempDir()
	b := DefaultCoreBundle()
	path := filepath.Join(dir, "bundle.json")
	if err := SaveBundle(path, b); err != nil {
		t.Fatalf("save bundle: %v", err)
	}

	loaded, err := LoadBundle(path)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if loaded.Name != "pub.polis.core" {
		t.Errorf("got name %q, want pub.polis.core", loaded.Name)
	}
	if len(loaded.Types) != 7 {
		t.Errorf("got %d types, want 7", len(loaded.Types))
	}
}

func TestContentDir(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		typeName string
		want     string
	}{
		{"pub.polis.post", "content/pub.polis.core/post"},
		{"pub.polis.comment", "content/pub.polis.core/comment"},
		{"pub.polis.follow", "content/pub.polis.core/follow"},
		{"pub.polis.feed", "content/pub.polis.core/feed"},
	}

	for _, tt := range tests {
		got, err := b.ContentDir(tt.typeName)
		if err != nil {
			t.Errorf("ContentDir(%q): %v", tt.typeName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ContentDir(%q) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestMountDir(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		typeName string
		want     string
	}{
		{"pub.polis.post", "posts"},
		{"pub.polis.comment", "comments"},
		{"pub.polis.follow", "follow"},
		{"pub.polis.feed", "feed"},
	}

	for _, tt := range tests {
		got, err := b.MountDir(tt.typeName)
		if err != nil {
			t.Errorf("MountDir(%q): %v", tt.typeName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("MountDir(%q) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestPrivateDir(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		typeName string
		want     string
	}{
		{"pub.polis.post", ".polis/bundles/pub.polis.core/posts"},
		{"pub.polis.comment", ".polis/bundles/pub.polis.core/comments"},
		{"pub.polis.follow", ".polis/bundles/pub.polis.core/follows"},
		{"pub.polis.feed", ".polis/bundles/pub.polis.core/feeds"},
	}

	for _, tt := range tests {
		got, err := b.PrivateDir(tt.typeName)
		if err != nil {
			t.Errorf("PrivateDir(%q): %v", tt.typeName, err)
			continue
		}
		if got != tt.want {
			t.Errorf("PrivateDir(%q) = %q, want %q", tt.typeName, got, tt.want)
		}
	}
}

func TestSourceToMountPath(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		input string
		want  string
	}{
		// Post source → mount
		{"content/pub.polis.core/post/20260302/slug.html", "posts/20260302/slug.html"},
		{"content/pub.polis.core/post/20260302/slug.md", "posts/20260302/slug.md"},
		// Comment source → mount
		{"content/pub.polis.core/comment/20260302/reply.html", "comments/20260302/reply.html"},
		// Feed source → mount
		{"content/pub.polis.core/feed/index.html", "feed/index.html"},
		// No match — returned unchanged
		{"some/other/path.html", "some/other/path.html"},
		{"content/com.example/post/foo.html", "content/com.example/post/foo.html"},
		// Exact content dir (no trailing file) — no match (no trailing slash after dir)
		{"content/pub.polis.core/post", "content/pub.polis.core/post"},
	}

	for _, tt := range tests {
		got := b.SourceToMountPath(tt.input)
		if got != tt.want {
			t.Errorf("SourceToMountPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSourceToMountPathNoMount(t *testing.T) {
	// Bundle with a type that has no mount — should return path unchanged
	b := &Bundle{
		Name:    "test.bundle",
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types: map[string]ContentType{
			"test.type": {Dir: "data"},
		},
	}
	input := "content/test.bundle/data/file.html"
	got := b.SourceToMountPath(input)
	if got != input {
		t.Errorf("SourceToMountPath(%q) = %q, want unchanged", input, got)
	}
}

func TestMatchesEvent(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		event string
		want  bool
	}{
		{"pub.polis.post.published", true},
		{"pub.polis.comment.blessing.granted", true},
		{"pub.polis.follow.announced", true},
		{"pub.polis.site.registered", true},
		{"com.example.recipes.recipe.published", false},
	}

	for _, tt := range tests {
		got := b.MatchesEvent(tt.event)
		if got != tt.want {
			t.Errorf("MatchesEvent(%q) = %v, want %v", tt.event, got, tt.want)
		}
	}
}

func TestTypeForEvent(t *testing.T) {
	b := DefaultCoreBundle()

	tests := []struct {
		event string
		want  string
	}{
		{"pub.polis.post.published", "pub.polis.post"},
		{"pub.polis.comment.blessing.requested", "pub.polis.comment"},
		{"pub.polis.follow.announced", "pub.polis.follow"},
		{"pub.polis.site.registered", ""},
		{"unknown.event", ""},
	}

	for _, tt := range tests {
		got := b.TypeForEvent(tt.event)
		if got != tt.want {
			t.Errorf("TypeForEvent(%q) = %q, want %q", tt.event, got, tt.want)
		}
	}
}

// step-06/6.f.1: notification rules relocated from per-content-type
// Bundle.Types[*].Notifications to BundleRegistry.Notifications. Test
// reflects the new home — DefaultRegistry's set is the canonical
// shipped set; Bundle's AllNotificationRules returns empty for the
// default core bundle (preserved as a method for any custom bundle
// that still ships rules under the old shape, though pub.polis.core
// no longer does).
func TestNotificationRuleEnabled(t *testing.T) {
	r := DefaultRegistry()
	rules := r.AllNotificationRules()

	foundNewPost := false
	for _, rule := range rules {
		if rule.ID == "new-post" {
			foundNewPost = true
		}
		if rule.ID == "updated-post" {
			t.Error("updated-post should not appear in AllNotificationRules (disabled by default)")
		}
	}
	if !foundNewPost {
		t.Error("new-post should appear in registry's AllNotificationRules")
	}

	// Bundle-level method still works for non-core bundles that ship
	// per-content-type rules, but DefaultCoreBundle now returns empty.
	b := DefaultCoreBundle()
	if got := b.AllNotificationRules(); len(got) != 0 {
		t.Errorf("DefaultCoreBundle should ship no per-type Notifications post-6.f.1; got %d", len(got))
	}
}

func TestValidateRejectsMissingName(t *testing.T) {
	b := &Bundle{
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types:   map[string]ContentType{},
	}
	if err := b.Validate(); err == nil {
		t.Error("expected validation error for missing name")
	}
}

func TestValidateRejectsDuplicateDirs(t *testing.T) {
	b := &Bundle{
		Name:    "test",
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types: map[string]ContentType{
			"a": {Dir: "same"},
			"b": {Dir: "same"},
		},
	}
	if err := b.Validate(); err == nil {
		t.Error("expected validation error for duplicate dirs")
	}
}

func TestValidateRejectsDuplicateMounts(t *testing.T) {
	b := &Bundle{
		Name:    "test",
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types: map[string]ContentType{
			"a": {Dir: "adir", Mount: "/same"},
			"b": {Dir: "bdir", Mount: "/same"},
		},
	}
	if err := b.Validate(); err == nil {
		t.Error("expected validation error for duplicate mounts")
	}
}

func TestSaveBundleCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "bundle.json")
	b := DefaultCoreBundle()
	if err := SaveBundle(path, b); err != nil {
		t.Fatalf("save bundle to nested path: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("bundle file should exist: %v", err)
	}
}

func TestMergeDefaults_AddsMissingTypes(t *testing.T) {
	b := &Bundle{
		Name:    "pub.polis.core",
		Version: "1.0.0",
		Types: map[string]ContentType{
			"pub.polis.post": {Dir: "post"},
		},
	}
	defaults := DefaultCoreBundle()

	added := b.MergeDefaults(defaults)
	if !added {
		t.Error("expected MergeDefaults to report additions")
	}
	if _, ok := b.Types["pub.polis.dm"]; !ok {
		t.Error("expected pub.polis.dm to be added")
	}
	// Original type should be preserved, not overwritten
	if b.Types["pub.polis.post"].Dir != "post" {
		t.Error("existing type should not be overwritten")
	}
}

func TestMergeDefaults_NoOpWhenComplete(t *testing.T) {
	b := DefaultCoreBundle()
	defaults := DefaultCoreBundle()

	added := b.MergeDefaults(defaults)
	if added {
		t.Error("expected no additions when bundle already has all types")
	}
}

func TestMergeDefaults_NilTypes(t *testing.T) {
	b := &Bundle{Name: "pub.polis.core", Version: "1.0.0"}
	defaults := DefaultCoreBundle()

	added := b.MergeDefaults(defaults)
	if !added {
		t.Error("expected additions when types is nil")
	}
	if len(b.Types) != len(defaults.Types) {
		t.Errorf("expected %d types, got %d", len(defaults.Types), len(b.Types))
	}
}

func TestDefaultCoreBundleHasBlogShape(t *testing.T) {
	b := DefaultCoreBundle()
	sh, err := b.GetShape("v3")
	if err != nil {
		t.Fatalf("GetShape(v3): %v", err)
	}
	if sh.Name != "v3" {
		t.Errorf("shape name = %q, want v3", sh.Name)
	}
	if sh.Version == "" {
		t.Error("shape version should be set")
	}
	for _, key := range []string{"post", "comment", "index", "archive"} {
		if _, ok := sh.Entry[key]; !ok {
			t.Errorf("blog shape missing entry %q", key)
		}
	}
}

func TestDefaultCoreBundleHasThemes(t *testing.T) {
	b := DefaultCoreBundle()
	wantThemes := []string{"_shared", "especial-light", "vice"}
	for _, name := range wantThemes {
		th, err := b.GetTheme(name)
		if err != nil {
			t.Errorf("GetTheme(%q): %v", name, err)
			continue
		}
		if th.Name != name {
			t.Errorf("theme %q name = %q, want %q", name, th.Name, name)
		}
		if th.CSS == "" {
			t.Errorf("theme %q css should be set", name)
		}
		if len(th.CompatibleShapes) == 0 {
			t.Errorf("theme %q compatible_shapes should be set", name)
		}
	}
}

// TestDefaultCoreBundle_Studio13NkDisplayName covers the hotfix-studio13-cleanup
// DisplayName aliasing: studio13-nk is the internal name (tenant-data-stable),
// but user-facing pickers should label it "studio13" to preserve the brand.
func TestDefaultCoreBundle_Studio13NkDisplayName(t *testing.T) {
	b := DefaultCoreBundle()
	th, err := b.GetTheme("studio13-nk")
	if err != nil {
		t.Fatalf("GetTheme(\"studio13-nk\"): %v", err)
	}
	if th.DisplayName != "studio13" {
		t.Errorf("studio13-nk DisplayName = %q, want %q", th.DisplayName, "studio13")
	}
	// Other themes should NOT have DisplayName set (Name is their public label).
	for _, name := range []string{"vice", "sols", "turbo", "zane", "especial", "especial-light"} {
		t2, _ := b.GetTheme(name)
		if t2 != nil && t2.DisplayName != "" {
			t.Errorf("%s DisplayName = %q, want empty (Name is the label)", name, t2.DisplayName)
		}
	}
}

// TestDefaultCoreBundle_Studio13Rename covers the step-02/2.0 rename: the
// pre-rename "studio13" theme was replaced by "studio13-nk" (Nick Katsivelos
// attribution) to preserve existing tenants' visual identity while freeing
// the "studio13" name for a future v4-era CSS-only theme. Medic's shape-
// gated remediation in healRegistryIntegrity migrates tenant registries.
func TestDefaultCoreBundle_Studio13Rename(t *testing.T) {
	b := DefaultCoreBundle()

	// studio13-nk is the renamed theme; must be present.
	th, err := b.GetTheme("studio13-nk")
	if err != nil {
		t.Fatalf("GetTheme(\"studio13-nk\"): %v", err)
	}
	if th.Name != "studio13-nk" {
		t.Errorf("studio13-nk Name = %q, want %q", th.Name, "studio13-nk")
	}
	if th.Version != "1.0.1" {
		t.Errorf("studio13-nk Version = %q, want %q (bumped to trigger resync)", th.Version, "1.0.1")
	}
	if th.CSS != "studio13-nk.css" {
		t.Errorf("studio13-nk CSS = %q, want %q", th.CSS, "studio13-nk.css")
	}
	// blog-only: the preserved HTML overrides target v3 markup.
	if len(th.CompatibleShapes) != 1 || th.CompatibleShapes[0] != "pub.polis.shapes.v3" {
		t.Errorf("studio13-nk CompatibleShapes = %v, want [pub.polis.shapes.v3]", th.CompatibleShapes)
	}

	// "studio13" is the v4-era CSS-only relaunch authored in step-02/2.a.1.
	// It is intentionally stream-only (CompatibleShapes does not include v3) so
	// blog-shape tenants cannot select it and are routed to studio13-nk instead.
	newS, err := b.GetTheme("studio13")
	if err != nil {
		t.Fatalf("GetTheme(\"studio13\"): %v", err)
	}
	if newS.Name != "studio13" {
		t.Errorf("new studio13 Name = %q, want %q", newS.Name, "studio13")
	}
	if newS.Version != "1.0.0" {
		t.Errorf("new studio13 Version = %q, want %q (fresh theme)", newS.Version, "1.0.0")
	}
	if newS.CSS != "studio13.css" {
		t.Errorf("new studio13 CSS = %q, want %q", newS.CSS, "studio13.css")
	}
	// stream-only: CSS targets shared markup but positioning is v4-era.
	if len(newS.CompatibleShapes) != 1 || newS.CompatibleShapes[0] != "pub.polis.shapes.v4" {
		t.Errorf("new studio13 CompatibleShapes = %v, want [pub.polis.shapes.v4]", newS.CompatibleShapes)
	}
}

// TestDefaultCoreBundle_Step02_2a2_VersionBumps covers the version bump
// required by step-02/2.a.2: _shared and the 6 existing extraction-pool themes
// advance to 1.1.0 to trigger version-gated resync → Medic EnsureReferencePayload
// → fresh (extracted) CSS installed on all hosted tenants next Patrol cycle.
// The new studio13 stays at 1.0.0 (fresh, not yet installed anywhere).
// studio13-nk stays at 1.0.1 (bumped in 2.0; not in extraction pool).
func TestDefaultCoreBundle_Step02_2a2_VersionBumps(t *testing.T) {
	b := DefaultCoreBundle()

	wantV110 := []string{"_shared", "especial", "especial-light", "sols", "turbo", "vice", "zane"}
	for _, name := range wantV110 {
		th, err := b.GetTheme(name)
		if err != nil {
			t.Fatalf("GetTheme(%q): %v", name, err)
		}
		if th.Version != "1.1.0" {
			t.Errorf("%s Version = %q, want %q (step-02/2.a.2 bump for resync)", name, th.Version, "1.1.0")
		}
	}

	// new studio13 is unchanged by extraction in terms of visibility:
	// it's a fresh stream-only theme at 1.0.0, no installed tenants yet.
	if s, _ := b.GetTheme("studio13"); s == nil || s.Version != "1.0.0" {
		t.Errorf("new studio13 Version = %q, want %q (fresh theme)", s.Version, "1.0.0")
	}
	// studio13-nk was bumped in 2.0; NOT in extraction pool, so not re-bumped.
	if s, _ := b.GetTheme("studio13-nk"); s == nil || s.Version != "1.0.1" {
		t.Errorf("studio13-nk Version = %q, want %q (2.0 bump preserved)", s.Version, "1.0.1")
	}
}

// TestDefaultCoreBundle_StreamShape covers the v4 SHAPE declaration
// authored in step-02/2.b.1 + 2.b.2. Verifies the shape is declared with
// the expected entries (no "dm" — DMs are owner-dashboard-only per WS-10),
// that the 6 v3+v4 themes gained v4 compatibility, and that studio13-nk
// stayed blog-only while new studio13 stayed stream-only.
func TestDefaultCoreBundle_StreamShape(t *testing.T) {
	b := DefaultCoreBundle()

	// stream shape declared.
	v4, err := b.GetShape("v4")
	if err != nil {
		t.Fatalf("GetShape(\"v4\"): %v", err)
	}
	if v4.Name != "v4" {
		t.Errorf("v4 Name = %q, want %q", v4.Name, "v4")
	}
	// Version progression:
	//   1.0.1 -> 1.1.0  unified entry-grammar DOM contract
	//   1.1.0 -> 1.1.1  milestone-label scrub + .polis-v4 -> .polis-stream
	//   1.1.1 -> 1.2.0  stream controller skeleton (real, not placeholder)
	//   1.2.0 -> 1.2.1  focus-restore-on-scroll-up fix (canonical URL on data attr)
	//   1.2.1 -> 1.3.0  polymorphic item renderers + insertion plumbing
	//   1.3.0 -> 1.3.1  ARIA live region + j/k keyboard nav + Esc popover hook
	//   1.3.1 -> 1.3.2  focus body resize fix + j/k boundary highlight feedback
	//   1.3.2 -> 1.3.3  focus tracking: ratio-tie bug -> closest-to-center
	//   1.3.3 -> 1.3.4  honor SSR focus on initial load (no URL poisoning)
	//   1.3.4 -> 1.4.0  4.b review-round: BL-1 user-scroll gate, BL-2 hash
	//                   strip, BL-3 clearDynamicEntries focus reset, BL-4
	//                   focus-styling CSS contract docs
	//   1.4.0 -> 1.4.1  4.b review polish: BP-1 flash timer cancel, BP-2
	//                   boundary short-circuit, BP-3 trust-chain docs,
	//                   Q-B6 getFocusedEntry + onEscape alias
	//   1.4.1 -> 1.5.0  4.c lazy fetch + session cache + prefetch (skel)
	//   1.5.0 -> 1.6.0  4.c bake-bodies refactor: SSR siblings get full body
	//   1.6.0 -> 1.6.1  hydration URL-stamping selector fix for new shape
	//   1.6.1 -> 1.6.2  pushState first focus change to preserve landing URL
	//   1.6.2 -> 1.6.3  absolute favicon path (no re-fetch on URL change)
	//   1.6.3 -> 1.7.0  applyOriginOverride() helper (test-only no-op in prod)
	//   1.7.0 -> 1.7.1  cache-bust controller URL via ?v=StreamShapeVersion
	// Each bump triggers Patrol's NeedsRefresh on every tenant's next cycle
	// so Medic's EnsureReferencePayload installs the new SHAPE files.
	//
	// Asserted via the StreamShapeVersion const (single source of truth, also
	// used by render to cache-bust /stream.js URLs in served HTML).
	if v4.Version != StreamShapeVersion {
		t.Errorf("stream shape Version = %q, want StreamShapeVersion %q", v4.Version, StreamShapeVersion)
	}
	wantEntries := map[string]string{
		"stream":  "stream.html",
		"post":    "stream-post.html",
		"comment": "stream-comment.html",
		"profile": "stream-profile.html",
		"mention": "stream-mention.html",
	}
	if len(v4.Entry) != len(wantEntries) {
		t.Errorf("v4 Entry has %d keys, want %d", len(v4.Entry), len(wantEntries))
	}
	for k, want := range wantEntries {
		if got, ok := v4.Entry[k]; !ok || got != want {
			t.Errorf("v4.Entry[%q] = %q (present=%v), want %q", k, got, ok, want)
		}
	}
	// No "dm" entry — DMs are owner-dashboard-only, deferred to WS-10.
	if _, ok := v4.Entry["dm"]; ok {
		t.Error("v4.Entry must NOT declare \"dm\" — DMs are WS-10 owner-dashboard territory")
	}
	// Sentence-filter partial declared.
	foundSF := false
	for _, p := range v4.Partials {
		if p == "snippets/sentence-filter.html" {
			foundSF = true
		}
	}
	if !foundSF {
		t.Errorf("v4 Partials should include snippets/sentence-filter.html; got %v", v4.Partials)
	}

	// blog shape unchanged (regression sentinel).
	if _, err := b.GetShape("v3"); err != nil {
		t.Errorf("blog shape should still be declared: %v", err)
	}

	// Theme compatibility roster.
	wantV3V4 := []string{"especial", "especial-light", "sols", "turbo", "vice", "zane"}
	for _, name := range wantV3V4 {
		th, err := b.GetTheme(name)
		if err != nil {
			t.Fatalf("GetTheme(%q): %v", name, err)
		}
		hasV3, hasV4 := false, false
		for _, s := range th.CompatibleShapes {
			if s == "pub.polis.shapes.v3" {
				hasV3 = true
			}
			if s == "pub.polis.shapes.v4" {
				hasV4 = true
			}
		}
		if !hasV3 || !hasV4 {
			t.Errorf("%s CompatibleShapes = %v, want both v3 and v4", name, th.CompatibleShapes)
		}
	}

	// studio13-nk stays blog-only (HTML overrides target v3 markup).
	if s, _ := b.GetTheme("studio13-nk"); s != nil {
		if len(s.CompatibleShapes) != 1 || s.CompatibleShapes[0] != "pub.polis.shapes.v3" {
			t.Errorf("studio13-nk CompatibleShapes = %v, want [pub.polis.shapes.v3] (blog-only)", s.CompatibleShapes)
		}
	}

	// new studio13 stays stream-only (v4-era relaunch).
	if s, _ := b.GetTheme("studio13"); s != nil {
		if len(s.CompatibleShapes) != 1 || s.CompatibleShapes[0] != "pub.polis.shapes.v4" {
			t.Errorf("new studio13 CompatibleShapes = %v, want [pub.polis.shapes.v4] (stream-only)", s.CompatibleShapes)
		}
	}
}

func TestBundleShapeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	b := DefaultCoreBundle()
	path := filepath.Join(dir, "bundle.json")
	if err := SaveBundle(path, b); err != nil {
		t.Fatalf("save bundle: %v", err)
	}

	loaded, err := LoadBundle(path)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	sh, err := loaded.GetShape("v3")
	if err != nil {
		t.Fatalf("round-tripped shape missing: %v", err)
	}
	if len(sh.Entry) != len(b.Shapes["v3"].Entry) {
		t.Errorf("shape entry count = %d, want %d", len(sh.Entry), len(b.Shapes["v3"].Entry))
	}
	if sh.DefaultCSS != b.Shapes["v3"].DefaultCSS {
		t.Errorf("shape default_css = %q, want %q", sh.DefaultCSS, b.Shapes["v3"].DefaultCSS)
	}
	if len(sh.Partials) != len(b.Shapes["v3"].Partials) {
		t.Errorf("shape partial count = %d, want %d", len(sh.Partials), len(b.Shapes["v3"].Partials))
	}
}

func TestBundleThemeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	b := DefaultCoreBundle()
	path := filepath.Join(dir, "bundle.json")
	if err := SaveBundle(path, b); err != nil {
		t.Fatalf("save bundle: %v", err)
	}
	loaded, err := LoadBundle(path)
	if err != nil {
		t.Fatalf("load bundle: %v", err)
	}
	if len(loaded.Themes) != len(b.Themes) {
		t.Errorf("theme count = %d, want %d", len(loaded.Themes), len(b.Themes))
	}
	vice, err := loaded.GetTheme("vice")
	if err != nil {
		t.Fatalf("round-tripped theme missing: %v", err)
	}
	if vice.CSS != "vice.css" {
		t.Errorf("vice.CSS = %q, want vice.css", vice.CSS)
	}
}

func TestShapeDir(t *testing.T) {
	b := DefaultCoreBundle()
	got, err := b.ShapeDir("v3")
	if err != nil {
		t.Fatalf("ShapeDir(v3): %v", err)
	}
	want := filepath.Join(".polis", "bundles", "pub.polis.core", "shapes", "v3")
	if got != want {
		t.Errorf("ShapeDir(v3) = %q, want %q", got, want)
	}
	// v4 was declared in step-02/2.b.2.
	gotV4, err := b.ShapeDir("v4")
	if err != nil {
		t.Fatalf("ShapeDir(v4): %v", err)
	}
	wantV4 := filepath.Join(".polis", "bundles", "pub.polis.core", "shapes", "v4")
	if gotV4 != wantV4 {
		t.Errorf("ShapeDir(v4) = %q, want %q", gotV4, wantV4)
	}
	// v99 remains undeclared.
	if _, err := b.ShapeDir("v99"); err == nil {
		t.Error("ShapeDir(v99) should error — not declared")
	}
}

func TestThemeDir(t *testing.T) {
	b := DefaultCoreBundle()
	got, err := b.ThemeDir("vice")
	if err != nil {
		t.Fatalf("ThemeDir: %v", err)
	}
	want := filepath.Join(".polis", "bundles", "pub.polis.core", "themes", "vice")
	if got != want {
		t.Errorf("ThemeDir(vice) = %q, want %q", got, want)
	}
	if _, err := b.ThemeDir("nonexistent"); err == nil {
		t.Error("ThemeDir(nonexistent) should error — not declared")
	}
}

func TestValidateRejectsBundleNameWithReservedSubstring(t *testing.T) {
	cases := []string{
		"pub.polis.themes.foo",
		"pub.polis.shapes.bar",
		"com.evil.themes.hack",
	}
	for _, name := range cases {
		b := &Bundle{
			Name:    name,
			Version: "1.0.0",
			Handler: Handler{Type: "builtin"},
			Types:   map[string]ContentType{},
		}
		if err := b.Validate(); err == nil {
			t.Errorf("expected validation error for reserved substring in name %q", name)
		}
	}
}

func TestValidateRejectsShapeMissingVersion(t *testing.T) {
	b := &Bundle{
		Name:    "test.bundle",
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types:   map[string]ContentType{},
		Shapes: map[string]*Shape{
			"v3": {Name: "v3", Entry: map[string]string{"post": "post.html"}},
		},
	}
	if err := b.Validate(); err == nil {
		t.Error("expected validation error for shape missing version")
	}
}

func TestValidateRejectsShapeMissingEntry(t *testing.T) {
	b := &Bundle{
		Name:    "test.bundle",
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types:   map[string]ContentType{},
		Shapes: map[string]*Shape{
			"v3": {Name: "v3", Version: "1.0.0"},
		},
	}
	if err := b.Validate(); err == nil {
		t.Error("expected validation error for shape missing entry map")
	}
}

func TestValidateRejectsShapeNameMismatch(t *testing.T) {
	b := &Bundle{
		Name:    "test.bundle",
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types:   map[string]ContentType{},
		Shapes: map[string]*Shape{
			"v3": {Name: "v4", Version: "1.0.0", Entry: map[string]string{"post": "post.html"}},
		},
	}
	if err := b.Validate(); err == nil {
		t.Error("expected validation error for shape name/key mismatch")
	}
}

func TestValidateRejectsThemeMissingCSS(t *testing.T) {
	b := &Bundle{
		Name:    "test.bundle",
		Version: "1.0.0",
		Handler: Handler{Type: "builtin"},
		Types:   map[string]ContentType{},
		Themes: map[string]*Theme{
			"vice": {Name: "vice", Version: "1.0.0"},
		},
	}
	if err := b.Validate(); err == nil {
		t.Error("expected validation error for theme missing css")
	}
}

func TestMergeDefaults_AddsMissingShapesAndThemes(t *testing.T) {
	b := &Bundle{
		Name:    "pub.polis.core",
		Version: "1.0.0",
		Types:   map[string]ContentType{"pub.polis.post": {Dir: "post"}},
	}
	defaults := DefaultCoreBundle()

	added := b.MergeDefaults(defaults)
	if !added {
		t.Error("expected MergeDefaults to report additions")
	}
	if _, ok := b.Shapes["v3"]; !ok {
		t.Error("expected blog shape to be added")
	}
	if _, ok := b.Themes["_shared"]; !ok {
		t.Error("expected _shared theme to be added")
	}
	if _, ok := b.Themes["vice"]; !ok {
		t.Error("expected vice theme to be added")
	}
}

func TestMergeDefaults_PreservesExistingShapes(t *testing.T) {
	customShape := &Shape{
		Name:    "v3",
		Version: "0.9.0-custom",
		Entry:   map[string]string{"post": "custom-post.html"},
	}
	b := &Bundle{
		Name:    "pub.polis.core",
		Version: "1.0.0",
		Types:   map[string]ContentType{"pub.polis.post": {Dir: "post"}},
		Shapes:  map[string]*Shape{"v3": customShape},
	}
	defaults := DefaultCoreBundle()

	b.MergeDefaults(defaults)
	if b.Shapes["v3"].Version != "0.9.0-custom" {
		t.Error("existing shape should not be overwritten by defaults")
	}
}

func TestAllEmittedEvents(t *testing.T) {
	b := DefaultCoreBundle()
	events := b.AllEmittedEvents()

	// pub.polis.core emits: 3 (post) + 5 (comment) + 2 (follow) + 2 (tag) + 2 (theme) = 14
	if len(events) != 14 {
		t.Errorf("got %d events, want 14", len(events))
	}

	// Check a few key events exist
	eventSet := make(map[string]bool)
	for _, e := range events {
		eventSet[e] = true
	}
	for _, expected := range []string{
		"pub.polis.post.published",
		"pub.polis.comment.blessing.requested",
		"pub.polis.follow.announced",
	} {
		if !eventSet[expected] {
			t.Errorf("missing event: %s", expected)
		}
	}
}
