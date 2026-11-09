package theme

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The site's own CSS (CustomCSSKey): read it, replace it. Replacing it does not
// render; the caller re-renders, as a theme switch does.

// MaxCustomCSSBytes caps the custom CSS. A site's own palette and a few rules
// are a few hundred bytes; the cap only stops a runaway write.
const MaxCustomCSSBytes = 20000

// customCSSPrevKey keeps the version a SetCustomCSS replaced: a single-step
// undo, until the site has a history of its in-place changes.
const customCSSPrevKey = "site/custom.css.prev"

// GetCustomCSS returns the site's custom CSS, or "" when it has none.
func GetCustomCSS(site storage.Site) (string, error) {
	data, err := site.Store().Get(context.Background(), CustomCSSKey)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", CustomCSSKey, err)
	}
	return string(data), nil
}

var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssAtRule  = regexp.MustCompile(`@(-?[a-z][a-z0-9-]*)`)
	cssURLArg  = regexp.MustCompile(`url\(\s*(['"]?)([^'")]*)`)
)

// cssFetching are the CSS functions that can make the browser fetch something.
// url() is checked separately (only data:image is allowed); the rest are
// refused outright. image-set() and image() take a bare string as a URL, so a
// check for url( alone misses them.
var cssFetching = []string{
	"image-set(", "-webkit-image-set(", "image(", "cross-fade(", "-webkit-cross-fade(",
	"element(", "-moz-element(", "src(", "expression(",
}

// cssAllowedAtRules are the at-rules custom CSS may use: none of them loads
// anything. ⛔ @import, @font-face and @namespace are not among them.
var cssAllowedAtRules = map[string]bool{
	"media": true, "supports": true, "keyframes": true, "-webkit-keyframes": true,
	"container": true, "layer": true,
}

// ValidateCustomCSS refuses CSS that is too large, or that could make a
// reader's browser fetch anything from anywhere.
//
// ⛔⛔ IT MUST REFUSE EVERY FETCH, NOT JUST THE OBVIOUS ONES. The custom CSS is
// in every reader's styles.css, and the page policy allows https images from
// anywhere (posts embed them), so a fetch the check misses is a beacon. On
// polis.pub a logged-in reader's own handle is in the page they read, so CSS
// selectors plus one beacon would tell a site WHICH polis.pub readers visit
// it. A regex for url(https:…) missed: image-set("https://…"), escapes
// (u\72l(…), url(https\3a //…), url(/\/host)), and @\69mport. So:
//   - ⛔ no backslash at all: escapes are how every name and URL check is evaded;
//   - comments are removed before checking, so ur/**/l( is read as url(;
//   - no function that fetches (cssFetching), and url() only for this site's
//     own files or data:image (sameSiteCSSURL);
//   - only at-rules that load nothing (cssAllowedAtRules).
//
// Names are ASCII case-insensitive in CSS, so the check is on the lowercased
// text. It errs towards refusing: a site's palette and a few rules never need
// any of this.
func ValidateCustomCSS(css string) error {
	if len(css) > MaxCustomCSSBytes {
		return fmt.Errorf("custom CSS is %d bytes; the most a site takes is %d", len(css), MaxCustomCSSBytes)
	}
	if strings.Contains(css, "\\") {
		return fmt.Errorf("custom CSS may not use backslash escapes")
	}
	text := strings.ToLower(cssComment.ReplaceAllString(css, ""))
	if strings.Contains(text, "/*") {
		return fmt.Errorf("custom CSS has a comment that is never closed")
	}
	for _, m := range cssAtRule.FindAllStringSubmatch(text, -1) {
		if !cssAllowedAtRules[m[1]] {
			return fmt.Errorf("custom CSS may not use @%s: only @media, @supports, @keyframes, @container and @layer, none of which load anything", m[1])
		}
	}
	for _, f := range cssFetching {
		if strings.Contains(text, f) {
			return fmt.Errorf("custom CSS may not use %s): it can load something from elsewhere", strings.TrimSuffix(f, "("))
		}
	}
	urls := cssURLArg.FindAllStringSubmatch(text, -1)
	if strings.Count(text, "url(") != len(urls) {
		return fmt.Errorf("custom CSS has a url( it cannot read")
	}
	for _, m := range urls {
		if err := sameSiteCSSURL(strings.TrimSpace(m[2])); err != nil {
			return err
		}
	}
	return nil
}

// sameSiteCSSURL allows a url() value that stays on this site, or a
// data:image/ value. ⛔ Refused: any other scheme; "//host" (protocol-
// relative); and ANY whitespace or control character, because a browser's URL
// parser strips tabs and newlines, so url("/<TAB>/host") becomes "//host".
func sameSiteCSSURL(u string) error {
	if strings.HasPrefix(u, "data:image/") {
		return nil
	}
	for _, r := range u {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("custom CSS url() values may not contain whitespace or control characters")
		}
	}
	if strings.HasPrefix(u, "//") {
		return fmt.Errorf("custom CSS may load only files on this site or data:image/ values, not %q", u)
	}
	if head, _, _ := strings.Cut(u, "/"); strings.Contains(head, ":") {
		return fmt.Errorf("custom CSS may load only files on this site or data:image/ values, not %q", u)
	}
	return nil
}

// SetCustomCSS replaces the site's custom CSS, keeping the version it replaces.
// An empty (or all-whitespace) css removes it.
func SetCustomCSS(site storage.Site, css string) error {
	if err := ValidateCustomCSS(css); err != nil {
		return err
	}
	ctx := context.Background()
	store := site.Store()
	prev, err := store.Get(ctx, CustomCSSKey)
	switch {
	case err == nil:
		if perr := store.Put(ctx, customCSSPrevKey, prev); perr != nil {
			return fmt.Errorf("keep the previous custom CSS: %w", perr)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("read %s: %w", CustomCSSKey, err)
	}
	if strings.TrimSpace(css) == "" {
		if derr := store.Delete(ctx, CustomCSSKey); derr != nil && !errors.Is(derr, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", CustomCSSKey, derr)
		}
		return nil
	}
	return store.Put(ctx, CustomCSSKey, []byte(css))
}

// Property is one custom property a theme declares on :root.
type Property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

var cssDecl = regexp.MustCompile(`(--[A-Za-z0-9_-]+)\s*:\s*([^;{}]+);`)

// ThemeProperties lists the custom properties a theme declares in its first
// :root block, in order: the names custom CSS overrides to recolour the site
// without restating the theme's rules. A theme with no such block has none.
func ThemeProperties(site storage.Site, themeName string) ([]Property, error) {
	ctx := context.Background()
	var css []byte
	var err error
	for _, key := range []string{
		bundleThemesKey + "/" + themeName + "/" + themeName + ".css",
		localThemesKey + "/" + themeName + "/" + themeName + ".css",
	} {
		if css, err = site.Store().Get(ctx, key); err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read theme %q: %w", themeName, err)
	}
	return RootProperties(string(css)), nil
}

// RootProperties lists the custom properties declared in the first :root
// block of a stylesheet, in order. A stylesheet with none has none.
func RootProperties(s string) []Property {
	start := strings.Index(s, ":root")
	if start < 0 {
		return nil
	}
	open := strings.Index(s[start:], "{")
	if open < 0 {
		return nil
	}
	body := s[start+open+1:]
	if end := strings.Index(body, "}"); end >= 0 {
		body = body[:end]
	}
	var props []Property
	for _, m := range cssDecl.FindAllStringSubmatch(body, -1) {
		props = append(props, Property{Name: m[1], Value: strings.TrimSpace(m[2])})
		if len(props) == 80 {
			break
		}
	}
	return props
}
