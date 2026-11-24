// Package theme provides theme loading and management for polis.
package theme

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Templates holds the loaded theme templates.
//
// blog shape populates Post/Comment/Index/etc. stream shape (step-02/2.b) populates
// Stream/StreamPost/etc. Fields not relevant to the active shape stay empty.
// Consumers must either gate reads on shape name or check field-non-empty —
// reading a v4 field when shape is v3 is a silent logic error.
//
// A parallel ShapeTemplates type was considered and rejected in 2.b for
// diff-size reasons. If additional shapes land (v5+), a more principled
// map-keyed-by-logical-name approach should replace this flat struct.
type Templates struct {
	// blog shape fields.
	Post          string // post.html - required
	Comment       string // comment.html - required
	CommentInline string // comment-inline.html - required
	Index         string // index.html - required
	Archive       string // posts.html - optional (archive page)
	Tag           string // tag.html - optional (single tag page)
	TagIndex      string // tag-index.html - optional (tag index page)

	// stream shape fields (step-02/2.b). Loaded from shapes/v4/ when
	// LoadShape is called with shapeName=="v4".
	Stream     string // stream.html - required for v4
	StreamPost string // stream-post.html - sibling-excerpt partial (the only
	// live entry template — included via {{> stream-post}}. Comment/profile/
	// mention/dm entries render client-side in stream.js; their SSR templates
	// were removed as dead scaffolding.

	// V5 holds the v5 shape's templates by logical name ("frame", "post",
	// "comment", "about"): the map-keyed form the note above asked for, used
	// by v5 alone so the v3 and v4 fields stay as they are.
	V5 map[string]string
}

// Manifest represents the site manifest (metadata/manifest.json).
type Manifest struct {
	Version      string `json:"version"`
	ActiveTheme  string `json:"active_theme"`
	PostCount    int    `json:"post_count"`
	CommentCount int    `json:"comment_count"`
}

// LoadShape loads templates for the given shape with optional per-theme overrides.
//
// Template resolution, per file (checked in order):
//  1. Theme — allows a theme to override specific templates (e.g. studio13
//     overrides post.html). Found by resolveThemeSource: the installed
//     bundle theme, then site/themes/<themeName>/, then <cliThemesDir>/<themeName>/.
//  2. Shape — the tenant's installed shape fixtures under
//     .polis/bundles/pub.polis.core/shapes/<shapeName>/ in the site.
//
// If the shape dir is missing (tenant not yet installed), LoadShape returns a
// clear error pointing at the expected path so operators can diagnose.
func LoadShape(site storage.Site, cliThemesDir, shapeName, themeName string) (*Templates, error) {
	if shapeName == "" {
		return nil, fmt.Errorf("shape name is required")
	}
	if themeName == "" {
		return nil, fmt.Errorf("theme name is required")
	}

	// ⭐ THE SHAPE LIVES IN THE SITE, so it is found through the STORE. This
	// was storage.PathExists on a joined path, which made a blob-backed site
	// unloadable: "shape v4 not found", because an object store has no
	// directory to stat.
	shapeKey := bundleShapesKey + "/" + shapeName
	shapeEntries, _ := site.Store().List(context.Background(), shapeKey)
	if len(shapeEntries) == 0 {
		return nil, fmt.Errorf("shape %q not found at %s (run polis init or wait for Patrol/Medic resync)", shapeName, shapeKey)
	}
	shapeSrc := siteSource(site, shapeKey)

	// Theme source is optional — only used when a theme overrides shape markup.
	themeSrc := resolveThemeSource(site, cliThemesDir, themeName)

	// Dispatch on shape name. Each shape has its own filename set under
	// shapeDir and populates distinct Templates fields. Cross-shape
	// callers (e.g. render.NewPageRenderer) get a single struct back with
	// the active shape's fields populated; gating on shape name at read
	// time keeps v3 consumers unaffected by v4 extensions.
	switch shapeName {
	case "v3":
		return loadBlogTemplates(themeSrc, shapeSrc)
	case "v4":
		return loadStreamTemplates(themeSrc, shapeSrc)
	case "v5":
		return loadV5Templates(themeSrc, shapeSrc)
	default:
		return nil, fmt.Errorf("unsupported shape %q (known: v3, v4, v5)", shapeName)
	}
}

// resolveThemeSource finds a theme as a SOURCE across the locations valid
// during the SHAPE refactor transition window, in order:
//
//  1. Installed bundle theme (post-refactor canonical):
//     .polis/bundles/pub.polis.core/themes/<name>/ in the site
//  2. Per-tenant override:
//     site/themes/<name>/ in the site (legacy; consolidated by Patrol/Medic in 1.g)
//  3. CLI-shipped repo theme (legacy, only for unmigrated themes):
//     <cliThemesDir>/<name>/
//
// Returns an empty source if the theme isn't found in any location. Callers
// must handle this case (LoadShape treats it as "no theme overrides").
//
// ⭐ The first two live inside the site's store and the third is a path,
// because the CLI's installed themes directory is not a site. Site-first, so
// a tenant's own theme wins over a shipped one, on any backend.
func resolveThemeSource(site storage.Site, cliThemesDir, name string) templateSource {
	ctx := context.Background()
	for _, dirKey := range []string{bundleThemesKey, localThemesKey} {
		key := dirKey + "/" + name
		if entries, err := site.Store().List(ctx, key); err == nil && len(entries) > 0 {
			return siteSource(site, key)
		}
	}
	if cliThemesDir != "" {
		d := filepath.Join(cliThemesDir, name)
		if present, _ := storage.PathExists(d); present {
			return dirSource(d)
		}
	}
	return templateSource{}
}

// templateSource is somewhere templates can be read from: a prefix inside a
// site's store, or a directory on a filesystem.
//
// ⭐⭐⭐ IT EXISTS BECAUSE THE FALLBACK MUST STAY ONE FUNCTION. Templates are
// looked up theme-first then shape, and a blob-backed site keeps both inside
// its store while the CLI's installed themes directory is genuinely a path.
// Writing the fallback twice is how the two drift, and a theme override that
// silently stops applying on one backend is the kind of difference nobody
// reports as a bug.
type templateSource struct {
	// site is set when this source lives inside a site's store.
	site storage.Site
	key  string

	// dir is set when this source is a plain directory outside any site.
	dir string
}

func siteSource(site storage.Site, key string) templateSource {
	return templateSource{site: site, key: key}
}

func dirSource(dir string) templateSource { return templateSource{dir: dir} }

func (t templateSource) empty() bool { return t.key == "" && t.dir == "" }

func (t templateSource) read(filename string) ([]byte, error) {
	if t.key != "" {
		return t.site.Store().Get(context.Background(), t.key+"/"+filename)
	}
	if t.dir != "" {
		return storage.NewPOSIX(t.dir).Get(context.Background(), filename)
	}
	return nil, fmt.Errorf("template source is empty")
}

func (t templateSource) String() string {
	if t.key != "" {
		return t.key
	}
	return t.dir
}

// readFromSources reads a template from the theme first, then the shape. An
// empty theme source means "no theme overrides".
func readFromSources(theme, base templateSource, filename string) (string, error) {
	for _, src := range []templateSource{theme, base} {
		if src.empty() {
			continue
		}
		if content, err := src.read(filename); err == nil {
			return string(content), nil
		}
	}
	return "", fmt.Errorf("template %q not found in theme or shape", filename)
}

// loadBlogTemplates loads the blog shape template set from themeDir (if present)
// falling back to baseDir (the shape fixture dir). Original pre-step-02
// behavior preserved verbatim.
func loadBlogTemplates(themeDir, baseDir templateSource) (*Templates, error) {
	templates := &Templates{}

	// Load required templates (theme dir first, then shape dir)
	required := map[string]*string{
		"post.html":           &templates.Post,
		"comment.html":        &templates.Comment,
		"comment-inline.html": &templates.CommentInline,
		"index.html":          &templates.Index,
	}

	for filename, dest := range required {
		content, err := readFromSources(themeDir, baseDir, filename)
		if err != nil {
			return nil, fmt.Errorf("required template %q: %w", filename, err)
		}
		*dest = content
	}

	// Load optional templates (same fallback pattern)
	if content, err := readFromSources(themeDir, baseDir, "posts.html"); err == nil {
		templates.Archive = content
	}
	if content, err := readFromSources(themeDir, baseDir, "tag.html"); err == nil {
		templates.Tag = content
	}
	if content, err := readFromSources(themeDir, baseDir, "tag-index.html"); err == nil {
		templates.TagIndex = content
	}

	return templates, nil
}

// loadStreamTemplates loads the stream shape template set (step-02/2.b).
// Requires stream.html (the per-post page shell) and loads stream-post.html —
// the only entry partial, included via {{> stream-post}} for the SSR focus +
// sibling posts. Comment/profile/mention/dm entries render client-side in
// stream.js, so their SSR templates were removed. Themes may override via
// themeDir but most v4 themes are CSS-only and use the shape-shipped markup.
func loadStreamTemplates(themeDir, baseDir templateSource) (*Templates, error) {
	templates := &Templates{}

	// stream.html is the only required template.
	content, err := readFromSources(themeDir, baseDir, "stream.html")
	if err != nil {
		return nil, fmt.Errorf("required template %q: %w", "stream.html", err)
	}
	templates.Stream = content

	// Per-type partials — all optional. The render pipeline gates their
	// use on item-type at compose time.
	optional := map[string]*string{
		"stream-post.html": &templates.StreamPost,
	}
	for filename, dest := range optional {
		if content, err := readFromSources(themeDir, baseDir, filename); err == nil {
			*dest = content
		}
	}

	return templates, nil
}

// V5Templates are the v5 shape's templates, each required: the frame every
// page shares, and one template per kind of item.
var V5Templates = map[string]string{
	"frame":     "frame.html",
	"post":      "post.html",
	"comment":   "comment.html",
	"about":     "about.html",
	"following": "following.html",
	"person":    "person.html",
}

// loadV5Templates reads the v5 templates. ⚠️ v5 takes NO template override
// from a theme: a v5 theme is tokens only (the token contract), so a theme
// that carries v3 markup (studio13-nk's post.html) can never shadow a v5
// card. Only the shape's own files are read.
func loadV5Templates(_ templateSource, shapeSrc templateSource) (*Templates, error) {
	t := &Templates{V5: map[string]string{}}
	for name, file := range V5Templates {
		content, err := readFromSources(templateSource{}, shapeSrc, file)
		if err != nil {
			return nil, fmt.Errorf("required v5 template %q: %w", file, err)
		}
		t.V5[name] = content
	}
	return t, nil
}

// GetActiveTheme returns the active theme name. Reads from
// .polis/bundles/registry.json (post-1e canonical) with a legacy fallback to
// active_theme in .well-known/polis for pre-migration sites.
func GetActiveTheme(site storage.Site) (string, error) {
	return bundle.GetActiveThemeName(site)
}

// reservedThemes are system themes a user never gets by chance or by choice:
// sols is the logged-out landing theme, stardust is why.polis.pub's, and the
// Elwood pair is defined in the bundle but not offered until it joins v5. The
// webapp refuses them in its theme picker (and the SPA filters them from its
// list); SelectRandomTheme never picks them. Reservation blocks selection
// only: a site can still be put on one through registry.json directly.
var reservedThemes = map[string]bool{
	"sols":          true,
	"stardust":      true,
	"elwood-signal": true, // defined, until the pair joins v5
	"elwood-noise":  true,
}

// IsReserved reports whether a theme name — short ("sols") or fully qualified
// ("pub.polis.themes.sols") — is a reserved system theme.
func IsReserved(name string) bool {
	return reservedThemes[strings.TrimPrefix(name, "pub.polis.themes.")]
}

// Selectable reports whether an owner (or their agent) may choose a theme by
// this name: not a system directory (_base, _shared), not reserved, and not
// retired (bundle.RetiredThemeNames: an old site's bundle.json still declares
// one). Any list offered as choices filters by it, so nothing is offered that
// the theme switch would refuse.
func Selectable(name string) bool {
	n := strings.TrimPrefix(name, "pub.polis.themes.")
	return n != "" && n != "_base" && n != "_shared" && !IsReserved(n) && !bundle.IsRetiredTheme(n)
}

// ErrThemeNotSaved means a theme was chosen but the choice could not be saved.
// The returned name is still usable for this render.
var ErrThemeNotSaved = errors.New("theme chosen but not saved")

// SelectRandomTheme picks a random theme from available themes and persists the choice.
// It never picks a reserved theme, unless reserved themes are all there is.
// This matches the bash CLI's select_theme() behavior.
//
// ⭐ A CHOICE THAT CANNOT BE SAVED IS NOT RANDOM. Nothing remembers it, so the
// next render chooses again, and a random choice would give the site a
// different theme on every render: exactly what a registry.json that does not
// parse produced, since the save refuses to overwrite a present-but-broken
// registry. So when the save fails it returns the first eligible theme by
// name, the same one every time, wrapped in ErrThemeNotSaved. A caller that
// only needs a theme to render with (render) uses the name; a caller that
// reports a repair (Tailor, Medic) reports the failure, because nothing was
// repaired.
//
// ⛔ IT PICKS ONLY A THEME POLIS SHIPS, compatible with the site's active shape.
// ListThemes also lists the owner's own themes (site/themes/), and choosing one
// of those by chance is wrong twice over: a theme of the owner's is theirs to
// choose, and a stream render reads a theme's CSS from the bundle tree only, so
// the site stopped rendering. A first render, or Tailor's and Medic's registry
// repairs, did exactly that on a customised site.
func SelectRandomTheme(site storage.Site, cliThemesDir string) (string, error) {
	themes, err := ListThemes(site, cliThemesDir)
	if err != nil || len(themes) == 0 {
		return "", fmt.Errorf("no themes found")
	}
	themes = shippedForActiveShape(site, themes)
	if len(themes) == 0 {
		return "", fmt.Errorf("no themes found: no theme polis ships is installed for this site's shape")
	}
	sort.Strings(themes)
	var eligible []string
	for _, t := range themes {
		if !IsReserved(t) {
			eligible = append(eligible, t)
		}
	}
	if len(eligible) == 0 {
		eligible = themes
	}
	selected := eligible[rand.IntN(len(eligible))] //nolint:gosec // G404: cosmetic randomness (a colour or a default pick), never a secret
	// Persist the choice so future renders use the same theme
	if err := SetActiveTheme(site, selected); err != nil {
		stable := append([]string(nil), eligible...)
		sort.Strings(stable)
		return stable[0], fmt.Errorf("%w: %v", ErrThemeNotSaved, err)
	}
	return selected, nil
}

// shippedForActiveShape keeps the themes polis ships (declared in the core
// bundle, not a system directory) that declare the site's active shape.
func shippedForActiveShape(site storage.Site, themes []string) []string {
	shape, err := bundle.GetActiveShapeName(site)
	if err != nil || shape == "" {
		shape = "v5"
	}
	want := bundle.QualifyShape(shape)
	declared := bundle.DefaultCoreBundle().Themes
	var out []string
	for _, name := range themes {
		th, ok := declared[name]
		if !ok || strings.HasPrefix(name, "_") {
			continue
		}
		for _, s := range th.CompatibleShapes {
			if s == want {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// SetActiveTheme updates the active theme in .polis/bundles/registry.json.
func SetActiveTheme(site storage.Site, themeName string) error {
	return bundle.SetActiveThemeName(site, themeName)
}

// RemoveLegacyThemeLocations blindly deletes theme directories from the legacy
// per-tenant locations:
//   - <dataDir>/site/themes/
//   - <dataDir>/.polis/themes/
//
// Per the step-01 forced-upgrade policy, any user customizations in those
// locations are lost — the canonical replacement is installed at
// .polis/bundles/pub.polis.core/themes/ via bundle.EnsureReferencePayload.
//
// One-time migration; idempotent (no-op when neither dir exists).
func RemoveLegacyThemeLocations(site storage.Site) error {
	for _, key := range legacyThemeKeys {
		if err := site.Store().Delete(context.Background(), key,
			storage.DeleteRecursive(), storage.IgnoreMissing()); err != nil {
			return fmt.Errorf("remove %s: %w", key, err)
		}
	}
	return nil
}

// LegacyThemeLocationsExist reports whether either legacy theme directory
// is present on the tenant. Used by Patrol to flag tenants needing migration.
func LegacyThemeLocationsExist(site storage.Site) bool {
	st := site.Store()
	for _, key := range legacyThemeKeys {
		if st.Capabilities().RealPath {
			// NOT the storage interface: this asks whether a legacy directory
			// is THERE, and an EMPTY one still means the tenant needs
			// migrating. A prefix listing cannot see an empty directory, so
			// on a filesystem it would answer "no" for exactly the case
			// Patrol is looking for.
			root, err := st.RealPath(key)
			if err != nil {
				continue
			}
			if info, err := os.Stat(root); err == nil && info.IsDir() {
				return true
			}
			continue
		}
		// ⭐ No filesystem, so no empty directories: a legacy location exists
		// exactly when something is stored under it.
		if entries, err := st.List(context.Background(), key); err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}

// CopyBaseCSS copies the shared base CSS file to base.css at the site root.
// Shared CSS lives in the installed bundle's _shared theme — pre-refactor it
// was at themes/_base/base.css.
func CopyBaseCSS(site storage.Site, cliThemesDir string) error {
	// ⚠️ TRY-IN-ORDER, not check-then-copy. The old form read PathExists and
	// DISCARDED its error, so an unreadable candidate fell through to the next
	// one; a missing one did too. Copying and treating any failure as "try the
	// next" preserves that exactly, with one read instead of two.
	candidates := []string{
		bundleThemesKey + "/_shared/base.css", // installed bundle (canonical)
		localThemesKey + "/_base/base.css",    // legacy local override
	}
	for _, key := range candidates {
		if err := copySiteFile(site, key, "base.css"); err == nil {
			return nil
		}
	}
	// No base.css found — not an error (older themes may not have it).
	return nil
}

// CopyCSS copies the active theme's CSS file to styles.css at the site root.
// The CSS filename matches the theme name ({themename}.css). Looked up in the
// installed bundle first, then legacy locations during the transition.
func CopyCSS(site storage.Site, cliThemesDir, themeName string) error {
	cssFilename := themeName + ".css"

	// ⚠️ Try-in-order; see the note in CopyBaseCSS for why this is equivalent
	// to the old check-then-copy and not a behaviour change.
	for _, key := range []string{
		bundleThemesKey + "/" + themeName + "/" + cssFilename, // installed bundle
		localThemesKey + "/" + themeName + "/" + cssFilename,  // legacy override
	} {
		if data, err := site.Store().Get(context.Background(), key); err == nil {
			return writeStyles(site, data)
		}
	}

	// ⚠️ The ONE candidate outside the site: a legacy CLI-shipped theme. That
	// root is not a site, so the source stays a path.
	if cliThemesDir != "" {
		cliCSSPath := filepath.Join(cliThemesDir, themeName, cssFilename)
		if data, err := storage.ReadAtPath(cliCSSPath); err == nil {
			return writeStyles(site, data)
		}
	}

	return fmt.Errorf("CSS file not found: %s", cssFilename)
}

// CopyStreamCSS writes styles.css for the stream shape by concatenating the shape's
// structural stylesheet (shapes/v4/stream.css) with the active theme's CSS.
// Order matters: shape first, theme last, so theme custom-property overrides win.
//
// The tenant-installed bundle tree is the source of truth. Legacy CLI-shipped
// fallbacks are not consulted here because v4 support only exists in post-
// refactor sites (Patrol/Medic guarantees the shape payload is installed).
func CopyStreamCSS(site storage.Site, themeName string) error {
	ctx := context.Background()
	shapeCSS, err := site.Store().Get(ctx, bundleShapesKey+"/v4/stream.css")
	if err != nil {
		return fmt.Errorf("read stream shape CSS: %w", err)
	}

	themeCSS, err := site.Store().Get(ctx, bundleThemesKey+"/"+themeName+"/"+themeName+".css")
	if err != nil {
		return fmt.Errorf("read v4 theme CSS %q: %w", themeName, err)
	}

	out := make([]byte, 0, len(shapeCSS)+len(themeCSS)+64)
	out = append(out, shapeCSS...)
	out = append(out, '\n')
	out = append(out, themeCSS...)
	return writeStyles(site, out)
}

// V5TokensFile is a theme's v5 definition: its note and its token values,
// checked against the token contract (v5tokens.go), never CSS. ⭐ A THEME IS DEFINED IN ONE PLACE: this file,
// in the theme's own directory. The shape, every site and the theme gallery
// read it; nothing keeps a copy.
const V5TokensFile = "v5.json"

// CopyV5CSS writes a v5 site's styles.css: the shape's stylesheet (which
// declares every token at the polis default), then the theme's values, from
// its one definition (themes/<name>/v5.json, read through the token contract),
// then the site's own CSS (writeStyles). The parts are joined as written, so
// their comments reach readers: a theme's note survives as a comment.
// A theme with no v5 definition draws in the polis default; one whose
// definition the contract refuses is withheld, and the stylesheet says why.
// A font polis does not offer draws in the default, and the stylesheet says so.
func CopyV5CSS(site storage.Site, themeName string) error {
	ctx := context.Background()
	shapeCSS, err := site.Store().Get(ctx, bundleShapesKey+"/v5/v5.css")
	if err != nil {
		return fmt.Errorf("read v5 shape CSS: %w", err)
	}
	out := append([]byte{}, shapeCSS...)
	t, withheld, err := LoadV5SiteTheme(site, themeName)
	if err != nil {
		out = append(out, []byte(fmt.Sprintf("\n/* ---------- The theme %s is withheld: its v5 definition does not pass the token contract ---------- */\n", themeName))...)
	}
	if withheld != "" {
		out = append(out, []byte(styleNote("---------- "+SiteTokensKey+" is withheld: "+withheld+" ----------"))...)
	}
	if t != nil {
		for _, f := range t.FellBack {
			out = append(out, []byte(styleNote("A font polis does not offer ("+f+"); the default font is used."))...)
		}
		name := themeName
		if err != nil || name == "" {
			name = "the polis default"
		}
		out = append(out, V5ThemeCSS(name, t)...)
		if own, _ := ReadSiteTokens(site); withheld == "" && own != nil && len(own.Tokens) > 0 {
			out = append(out, []byte(styleNote("The site's own values ("+SiteTokensKey+") are layered over the theme above."))...)
		}
	}
	return writeStyles(site, out)
}

// LoadV5Theme reads a theme's v5 definition from the installed bundle: nil
// when the theme has none (it draws in the polis default), an error when the
// contract refuses it or it fails the contrast check.
func LoadV5Theme(site storage.Site, themeName string) (*V5Theme, error) {
	if themeName == "" {
		return nil, nil
	}
	data, err := site.Store().Get(context.Background(), bundleThemesKey+"/"+themeName+"/"+V5TokensFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read v5 definition of theme %q: %w", themeName, err)
	}
	t, err := ParseV5Theme(data)
	if err != nil {
		return nil, fmt.Errorf("theme %q: %w", themeName, err)
	}
	if problems := CheckV5Contrast(t); len(problems) > 0 {
		var why []string
		for _, p := range problems {
			why = append(why, p.String())
		}
		return nil, fmt.Errorf("theme %q fails the contrast check: %s", themeName, strings.Join(why, "; "))
	}
	return t, nil
}

// styleNote is a line for a reader of styles.css. ⛔ Its text may carry a
// value someone set, so it can never close the comment.
func styleNote(text string) string {
	return "\n/* " + strings.ReplaceAll(text, "*/", "* /") + " */\n"
}

// CopyV5Controller copies the v5 shape's controller to the site root as
// /v5.js: the script that opens and folds cards and redraws a view from its
// sentence. Every page reads without it.
func CopyV5Controller(site storage.Site) error {
	return copySiteFile(site, bundleShapesKey+"/v5/v5.js", "v5.js")
}

// CustomCSSKey is the site's own CSS, layered after the theme in styles.css on
// every render. ⭐ A SOURCE, AND THE SITE'S OWN: it sits beside the site's
// snippets rather than in the installed bundle, so a bundle resync never
// touches it, and a theme switch keeps it. Nothing interprets it; the render
// appends it verbatim.
const CustomCSSKey = "site/custom.css"

// customCSSMarker opens the custom layer in styles.css, so a reader of the
// generated file can see where the theme ends.
const customCSSMarker = "\n/* ── site/custom.css: the site's own CSS, layered after the theme ── */\n"

// customCSSWithheld replaces the custom layer when site/custom.css fails
// ValidateCustomCSS at render.
const customCSSWithheld = "\n/* ── site/custom.css withheld: it does not pass the custom CSS check (set it again to see why) ── */\n"

// writeStyles writes styles.css: the theme's CSS, then the site's custom CSS
// when it has any. ⛔ Every writer of styles.css goes through here, or a
// render path would silently drop the owner's change.
func writeStyles(site storage.Site, themeCSS []byte) error {
	ctx := context.Background()
	out := themeCSS
	custom, err := site.Store().Get(ctx, CustomCSSKey)
	switch {
	case err == nil && len(bytes.TrimSpace(custom)) > 0 && ValidateCustomCSS(string(custom)) != nil:
		// ⛔ Checked again here, at every render, and not only when it was set:
		// a file written by any other path (by hand, a restore, an older build
		// with a weaker check) must not reach readers. It is withheld, not
		// deleted, and the render goes on with the theme alone.
		out = make([]byte, 0, len(themeCSS)+len(customCSSWithheld))
		out = append(out, themeCSS...)
		out = append(out, customCSSWithheld...)
	case err == nil && len(bytes.TrimSpace(custom)) > 0:
		out = make([]byte, 0, len(themeCSS)+len(customCSSMarker)+len(custom)+1)
		out = append(out, themeCSS...)
		out = append(out, customCSSMarker...)
		out = append(out, custom...)
		if custom[len(custom)-1] != '\n' {
			out = append(out, '\n')
		}
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("read %s: %w", CustomCSSKey, err)
	}
	return site.Store().Put(ctx, "styles.css", out)
}

// CopyStreamController copies the stream shape's stream.js placeholder (WS-2 stub) to
// the tenant root so v4 pages' `<script src="/stream.js" defer>` reference
// resolves to a 200 instead of a 404. The real controller lands in step 4 and
// will trigger a shape version bump for cache-bust + tenant resync.
//
// Source: <dataDir>/.polis/bundles/pub.polis.core/shapes/v4/stream.js
//
//	(installed by Patrol/Medic from the embedded reference payload).
//
// Destination: <dataDir>/stream.js
func CopyStreamController(site storage.Site) error {
	if err := copySiteFile(site, bundleShapesKey+"/v4/stream.js", "stream.js"); err != nil {
		return fmt.Errorf("read v4 controller stub: %w", err)
	}
	return nil
}

// Site-relative keys. ⭐ KEYS, NOT PATHS: every reader and writer addresses
// these the same way, on any backend.
const (
	bundleThemesKey = ".polis/bundles/pub.polis.core/themes"
	bundleShapesKey = ".polis/bundles/pub.polis.core/shapes"
	localThemesKey  = "site/themes"
)

// legacyThemeKeys are the two theme locations the step-01 forced upgrade
// retired: the per-tenant override and the old CLI-managed copy under .polis.
var legacyThemeKeys = []string{localThemesKey, ".polis/themes"}

// copySiteFile copies one file WITHIN a site, through its store.
//
// ⭐ Most of this package's copying is site-to-site — the installed bundle to
// the served root — so it needs no filesystem at all. ⚠️ Only a copy whose
// SOURCE is the CLI themes directory crosses roots, and that one is CopyCSS's
// last candidate, read with storage.ReadAtPath.
func copySiteFile(site storage.Site, srcKey, dstKey string) error {
	data, err := site.Store().Get(context.Background(), srcKey)
	if err != nil {
		return err
	}
	return site.Store().Put(context.Background(), dstKey, data)
}

// ListShapes returns the names of the shapes installed in the site — the
// directories under .polis/bundles/pub.polis.core/shapes/, which is where
// LoadShape looks and the only place it looks.
func ListShapes(site storage.Site) ([]string, error) {
	entries, err := site.Store().List(context.Background(), bundleShapesKey)
	if err != nil {
		return nil, err
	}
	var shapes []string
	for _, entry := range entries {
		if entry.IsDir {
			shapes = append(shapes, pathpkg.Base(entry.Key))
		}
	}
	return shapes, nil
}

// ListThemes returns the names of all available themes.
// It combines themes from the installed bundle, the per-tenant override
// directory, and the CLI themes directory (legacy fallback for unmigrated themes).
func ListThemes(site storage.Site, cliThemesDir string) ([]string, error) {
	themeSet := make(map[string]bool)

	// ⭐ Both site-rooted lookups go through the STORE, so a site backed by an
	// object store finds its themes. Only the CLI themes directory below stays
	// on a path, because that root is not a site.
	ctx := context.Background()
	for _, dirKey := range []string{bundleThemesKey, localThemesKey} {
		entries, err := site.Store().List(ctx, dirKey)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir {
				continue
			}
			name := pathpkg.Base(entry.Key)
			if isValidThemeKey(site, dirKey+"/"+name) {
				themeSet[name] = true
			}
		}
	}

	// CLI-shipped themes (legacy — only the unmigrated ones in step 1).
	if cliThemesDir != "" {
		if entries, err := storage.ListDir(cliThemesDir); err == nil {
			for _, entry := range entries {
				if name := pathpkg.Base(entry.Key); entry.IsDir && isValidTheme(filepath.Join(cliThemesDir, name)) {
					themeSet[name] = true
				}
			}
		}
	}

	var themes []string
	for name := range themeSet {
		themes = append(themes, name)
	}

	return themes, nil
}

// isValidTheme checks if a directory contains a valid theme.
// A theme is valid if it has either all required HTML templates (legacy full theme)
// or at least a CSS file (CSS-only theme that inherits templates from the shape).
// System dirs (_base, _shared) are excluded — they're not selectable themes.
// isValidThemeKey is isValidTheme for a theme INSIDE a site, addressed by key.
//
// ⛔⛔ IT EXISTS BECAUSE THEME DISCOVERY WAS THE ONE THING STILL ON THE
// FILESYSTEM, and that made a blob-backed site unrenderable: NewPageRenderer
// failed with "no theme available: no themes found", because ListThemes walked
// directories that an object store does not have. ⚠️ Rendering itself already
// wrote through the seam — the claim that a blob instance could render "with no
// render-specific work" was true of the WRITES and false of the LOOKUP.
//
// ⭐ The rules are identical to isValidTheme: a CSS-only theme has
// {name}.css, a legacy full theme has all four templates, and `_base` and
// `_shared` are not themes.
func isValidThemeKey(site storage.Site, themeKey string) bool {
	name := pathpkg.Base(themeKey)
	if name == "_base" || name == "_shared" {
		return false
	}
	ctx := context.Background()
	if present, _ := site.Store().Exists(ctx, themeKey+"/"+name+".css"); present {
		return true
	}
	for _, file := range []string{"post.html", "comment.html", "comment-inline.html", "index.html"} {
		if present, _ := site.Store().Exists(ctx, themeKey+"/"+file); !present {
			return false
		}
	}
	return true
}

// isValidTheme is the same question for a theme outside any site — the CLI's
// installed themes directory, which is legitimately a path.
func isValidTheme(themeDir string) bool {
	name := filepath.Base(themeDir)
	if name == "_base" || name == "_shared" {
		return false
	}

	// CSS-only theme: has a {name}.css file (templates come from _base)
	cssFile := filepath.Join(themeDir, name+".css")
	if present, _ := storage.PathExists(cssFile); present {
		return true
	}

	// Legacy full theme: has all 4 required templates
	required := []string{"post.html", "comment.html", "comment-inline.html", "index.html"}
	st := storage.NewPOSIX(themeDir)
	for _, file := range required {
		if present, _ := st.Exists(context.Background(), file); !present {
			return false
		}
	}
	return true
}

// CalculateCSSPath returns the relative path to styles.css from a given file path.
// For example:
// - posts/2026/01/post.html -> ../../../styles.css
// - comments/2026/01/comment.html -> ../../../styles.css
// - index.html -> styles.css
func CalculateCSSPath(filePath string) string {
	// Clean and normalize the path
	filePath = filepath.Clean(filePath)
	filePath = filepath.ToSlash(filePath) // Use forward slashes

	// Count directory depth
	parts := strings.Split(filePath, "/")
	depth := len(parts) - 1 // Subtract 1 for the filename

	if depth <= 0 {
		return "styles.css"
	}

	// Build relative path
	var prefix string
	for i := 0; i < depth; i++ {
		prefix += "../"
	}

	return prefix + "styles.css"
}

// CalculateHomePath returns the relative path to index.html from a given file path.
func CalculateHomePath(filePath string) string {
	// Clean and normalize the path
	filePath = filepath.Clean(filePath)
	filePath = filepath.ToSlash(filePath) // Use forward slashes

	// Count directory depth
	parts := strings.Split(filePath, "/")
	depth := len(parts) - 1 // Subtract 1 for the filename

	if depth <= 0 {
		return "index.html"
	}

	// Build relative path
	var prefix string
	for i := 0; i < depth; i++ {
		prefix += "../"
	}

	return prefix + "index.html"
}

// ThemePalette holds a theme's name and representative colors for UI display.
//
// DisplayName is the human-facing label (from bundle.Theme.DisplayName). When
// empty, the picker UI should fall back to Name. Useful when tenant-data
// identifiers diverge from branding — e.g. studio13-nk stored internally,
// labeled "studio13" in the picker.
type ThemePalette struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name,omitempty"`
	Colors      []string `json:"colors"` // 5 hex colors: bg, text, accent1, accent2, cyan
	Active      bool     `json:"active"`
}

// cssColorVar matches CSS custom property declarations like --color-bg: #1a1525;
var cssColorVar = regexp.MustCompile(`^\s*--color-([a-z0-9-]+)\s*:\s*(#[0-9a-fA-F]{3,8})\s*;`)

// ExtractPalette reads a theme's CSS file from a directory and extracts 5
// representative colors: bg, text, two accent colors, and cyan.
func ExtractPalette(themeDir, themeName string) ThemePalette {
	return extractPalette(dirSource(themeDir), themeName)
}

// extractPalette is ExtractPalette over a source, so a theme inside a site's
// store is read through the store. An empty or unreadable source yields a
// palette with no colors.
func extractPalette(src templateSource, themeName string) ThemePalette {
	palette := ThemePalette{Name: themeName}
	if src.empty() {
		return palette
	}

	// Whole-file rather than streamed: a theme's CSS is a few kilobytes.
	css, err := src.read(themeName + ".css")
	if err != nil {
		return palette
	}

	// Parse all --color-* variables from the :root block
	vars := make(map[string]string)
	inRoot := false
	scanner := bufio.NewScanner(bytes.NewReader(css))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, ":root") {
			inRoot = true
			continue
		}
		if inRoot && trimmed == "}" {
			break
		}
		if !inRoot {
			continue
		}
		if m := cssColorVar.FindStringSubmatch(line); m != nil {
			vars[m[1]] = m[2]
		}
	}

	// Pick 5 representative colors: bg, text, accent1, accent2, cyan
	bg := vars["bg"]
	text := vars["text"]
	cyan := vars["cyan"]

	// Find two accent colors: prefer theme-specific named accents
	// (skip bg*, text*, border*, and *-soft/*-dim/*-glow variants)
	var accents []string
	for name, val := range vars {
		if name == "bg" || name == "text" || name == "cyan" {
			continue
		}
		if strings.HasPrefix(name, "bg") || strings.HasPrefix(name, "text") || strings.HasPrefix(name, "border") {
			continue
		}
		if strings.HasSuffix(name, "-soft") || strings.HasSuffix(name, "-dim") || strings.HasSuffix(name, "-glow") {
			continue
		}
		accents = append(accents, val)
	}
	// Sort for deterministic output
	sort.Strings(accents)

	accent1 := ""
	accent2 := ""
	if len(accents) >= 1 {
		accent1 = accents[0]
	}
	if len(accents) >= 2 {
		accent2 = accents[1]
	}

	palette.Colors = []string{bg, text, accent1, accent2, cyan}
	return palette
}

// ListThemesWithPalettes returns all available themes with their color palettes.
// The active theme is marked with Active=true. DisplayName is populated from
// the bundle declaration (bundle.Theme.DisplayName) when available, so picker
// UIs can show a friendlier label than the internal theme name.
func ListThemesWithPalettes(site storage.Site, cliThemesDir string) ([]ThemePalette, error) {
	themes, err := ListThemes(site, cliThemesDir)
	if err != nil {
		return nil, err
	}

	activeTheme, _ := GetActiveTheme(site)
	defaults := bundle.DefaultCoreBundle()

	var palettes []ThemePalette
	for _, name := range themes {
		// ⭐ The same resolver template loading uses, so the palette shown for
		// a theme comes from the copy that renders.
		p := extractPalette(resolveThemeSource(site, cliThemesDir, name), name)
		p.Active = (name == activeTheme)
		if th, ok := defaults.Themes[name]; ok && th.DisplayName != "" {
			p.DisplayName = th.DisplayName
		}
		palettes = append(palettes, p)
	}

	sort.Slice(palettes, func(i, j int) bool {
		return palettes[i].Name < palettes[j].Name
	})

	return palettes, nil
}

// ListUserSelectableThemes returns themes the picker UI should offer, filtered
// by compatibility with the tenant's active shape. Themes whose
// CompatibleShapes doesn't include the active shape are excluded. This keeps
// blog-only themes (e.g. studio13-nk with its HTML overrides) off v4 pickers,
// and stream-only themes (e.g. the future v4-era studio13) off v3 pickers.
//
// When activeShape is empty, defaults to v3 (matches bundle.GetActiveShapeName).
// Themes not declared in DefaultCoreBundle (orphans on disk) are excluded —
// installed-but-undeclared is a disk-drift condition covered by Patrol's
// orphan-dir check.
func ListUserSelectableThemes(site storage.Site, cliThemesDir, activeShape string) ([]ThemePalette, error) {
	if activeShape == "" {
		activeShape = "v3"
	}
	wantFQN := bundle.QualifyShape(activeShape)
	all, err := ListThemesWithPalettes(site, cliThemesDir)
	if err != nil {
		return nil, err
	}
	defaults := bundle.DefaultCoreBundle()
	filtered := make([]ThemePalette, 0, len(all))
	for _, p := range all {
		th, ok := defaults.Themes[p.Name]
		if !ok {
			continue // not declared — don't surface
		}
		if !compatibleWithShape(th, wantFQN) {
			continue
		}
		filtered = append(filtered, p)
	}
	return filtered, nil
}

// OfferedForShape reports whether a theme may be switched to on a site whose
// active shape is activeShape: declared in the core bundle and compatible
// with that shape, the same test the picker applies. ⛔ The theme switch
// must ask this, not only whether the theme's directory exists: a v5-only
// theme (daryl, defined by its v5.json alone) has no stylesheet a v3 or v4
// render could copy, and a theme switched to before that copy fails leaves
// the site on a theme it cannot draw.
func OfferedForShape(name, activeShape string) bool {
	if activeShape == "" {
		activeShape = "v3"
	}
	th, ok := bundle.DefaultCoreBundle().Themes[strings.TrimPrefix(name, "pub.polis.themes.")]
	return ok && compatibleWithShape(th, bundle.QualifyShape(activeShape))
}

// compatibleWithShape reports whether a theme declares compatibility with the
// given fully-qualified shape FQN. An empty CompatibleShapes list means
// "compatible with none" — themes must opt in explicitly.
func compatibleWithShape(th *bundle.Theme, shapeFQN string) bool {
	for _, s := range th.CompatibleShapes {
		if s == shapeFQN {
			return true
		}
	}
	return false
}
