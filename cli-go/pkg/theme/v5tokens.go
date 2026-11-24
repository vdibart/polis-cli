package theme

// ⭐ THE v5 TOKEN CONTRACT: every value a theme (and, later, the owner's
// agent) may set on a v5 site, declared once.
//
// A tour:
//
//  1. V5Tokens is the declaration: each token's name, kind, allowed values,
//     default and what it changes. The validator, the stylesheet writer and
//     (later) the API description all read it, so none can drift from it.
//  2. A theme's v5 definition is themes/<name>/v5.json: a note (kept as a
//     comment in the site's styles.css) and a map of token names to values.
//     It is the theme's one definition: the shape, every site and the theme
//     gallery read it, and nothing keeps a copy.
//  3. ParseV5Theme reads one and refuses what the contract does not allow,
//     naming the token (a font the catalog lacks falls back to the default). CheckV5Contrast refuses a combination that fails a
//     ratio the contract requires, naming the pair and the ratio reached.
//  4. V5ThemeCSS turns the values into custom properties for the shape's
//     stylesheet. A theme never supplies CSS: every choice is drawn by the
//     shape from a fixed list, and every texture is drawn here, by polis,
//     from its settings. Nothing a theme sets can make a reader's browser
//     fetch anything (a font is a name, and the shape builds its address).
//  5. V5FontsHref and V5MarkStyle are what render needs besides the
//     stylesheet: the fonts' address, and the site's mark in the theme's
//     colours.
//
// It follows the v5 design's theme token contract (spec part 6), with the
// tokens added on 2026-11-16 to carry the approved designs.

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// V5Kind is what a token holds.
type V5Kind int

const (
	V5Colour         V5Kind = iota // a hex colour, #rgb or #rrggbb
	V5Choice                       // one of Allowed
	V5ChoiceOrColour               // one of Allowed, or a hex colour
	V5Colours                      // one to three hex colours
	V5Font                         // a family in the font catalog
	V5ChoiceOrFont                 // one of Allowed, or a family in the font catalog
)

// V5Token is one token of the contract.
type V5Token struct {
	Name    string
	Kind    V5Kind
	Allowed []string // the choices, for the choice kinds
	Default string   // what a site gets when nothing sets it (the polis default)
	Changes string   // what it changes, in a reader's words
}

// v5GradientDirections are bg-gradient-angle's choices, and v5GradientCSS
// what each writes ("down" writes nothing: it is linear-gradient's default).
var (
	v5GradientDirections = []string{"down", "down-right", "right", "up-right", "up", "up-left", "left", "down-left"}
	v5GradientCSS        = map[string]string{
		"down-right": "to bottom right", "right": "to right", "up-right": "to top right",
		"up": "to top", "up-left": "to top left", "left": "to left", "down-left": "to bottom left",
	}
)

// V5Tokens is the contract, in the order a theme's values are written.
var V5Tokens = []V5Token{
	{"scheme", V5Choice, []string{"light", "dark"}, "light", "light or dark: a dark theme's cards carry depth, and the browser's own controls follow"},
	{"site", V5Colour, nil, "#397f84", "the box and card strokes, the active filter, the chrome's accents; lines, links and filled buttons derive from it"},
	{"bg", V5Colour, nil, "#e0deda", "the page behind the cards"},
	{"bg-gradient", V5ChoiceOrColour, []string{"none"}, "none", "the page fades from bg to this colour, in bg-gradient-angle's direction"},
	{"bg-gradient-angle", V5Choice, v5GradientDirections, "down", "which way the page's gradient runs from bg: down (top to bottom), or toward a side or corner"},
	{"bar", V5ChoiceOrColour, []string{"bg"}, "bg", "the top bar and the footer"},
	{"surface", V5Colour, nil, "#eceae6", "cards and the section box"},
	{"card-hover", V5Colour, nil, "#efede9", "the rollover's lift"},
	{"panel", V5Colour, nil, "#d4d2ce", "other sites' initials, on the following list (quiet chips are derived from the card, not this)"},
	{"text", V5Colour, nil, "#1c1c2a", "titles, handles, the words that lead"},
	{"text-soft", V5Colour, nil, "#3a3a48", "body text"},
	{"text-muted", V5Colour, nil, "#605c56", "dates, labels, secondary links"},
	{"ink", V5Colour, nil, "#f8f5ee", "text on a filled button"},
	{"accent-2", V5ChoiceOrColour, []string{"site"}, "site", "a second accent: commenters' and replied-to links, counts, selection and focus rings (never the new dot)"},
	{"wordmark", V5Colour, nil, "#005a72", "polis's wordmark, in the footer (the top left is the author's)"},
	{"mark", V5ChoiceOrColour, []string{"own"}, "own", "the site's mark on its own pages: its own colours, or this one (only where the owner chose no avatar colours; chosen colours always win)"},
	{"mark-style", V5Choice, []string{"solid", "gradient", "split", "ring"}, "solid", "how a themed mark is filled: one colour, a gradient to mark-2, split by a bolt, or a ring of mark-2"},
	{"mark-2", V5Colour, nil, "#ffffff", "the mark's second colour (gradient, split, ring)"},
	{"mark-ink", V5Colour, nil, "#ffffff", "the mark's initial and pattern, on a themed mark"},
	{"heading-color", V5ChoiceOrColour, []string{"text", "site"}, "text", "the colour of titles"},
	{"heading-weight", V5Choice, []string{"regular", "medium", "semibold", "bold"}, "medium", "titles"},
	{"heading-style", V5Choice, []string{"upright", "italic"}, "upright", "titles"},
	{"heading-case", V5Choice, []string{"as-written", "small-caps"}, "as-written", "titles"},
	{"font-content", V5Font, nil, "Newsreader", "bodies and comments (and titles, unless font-heading says otherwise)"},
	{"font-heading", V5ChoiceOrFont, []string{"content"}, "content", "titles"},
	{"font-ui", V5Font, nil, "Inter", "handles, dates, buttons, labels"},
	{"date-style", V5Choice, []string{"plain", "signal"}, "plain", "the date that leads a card: plain, or signal (monospace in accent-2, after a ▸)"},
	{"texture", V5Choice, []string{"none", "grain", "dots", "glitter", "lines", "grid", "waves"}, "none", "the pattern on the page behind the cards, drawn by polis"},
	{"texture-colors", V5Colours, nil, "site", "the pattern's colours"},
	{"texture-scale", V5Choice, []string{"small", "medium", "large"}, "medium", "the pattern's size"},
	{"texture-density", V5Choice, []string{"sparse", "medium", "dense"}, "medium", "how much of the pattern there is"},
	{"texture-strength", V5Choice, []string{"faint", "soft", "clear"}, "faint", "how strongly the pattern shows (capped, so it never competes with the cards)"},
	{"card-texture", V5Choice, []string{"none", "grain"}, "none", "a grain inside the cards"},
	{"rollover", V5Choice, []string{"off", "lift", "glow"}, "lift", "a card under the pointer: nothing; the lift and the edge turning; or both and a soft halo"},
	{"rollover-color", V5ChoiceOrColour, []string{"stroke"}, "stroke", "the edge (and halo) under the pointer"},
	{"card-edge", V5Choice, []string{"none", "hairline", "site"}, "site", "the card's outline"},
	{"link-style", V5Choice, []string{"solid", "dotted", "wavy", "hover"}, "solid", "links in what people wrote"},
	{"quote-style", V5Choice, []string{"rule", "mark", "block"}, "rule", "quotations in what people wrote"},
	{"top-bar", V5Choice, []string{"solid", "translucent"}, "solid", "the bar behind the menu"},
	{"motion", V5Choice, []string{"calm", "default", "lively"}, "default", "how long movements take (reduced motion always wins)"},
	// The shape tokens: geometry, each a short fixed scale whose values keep the style guide's measured
	// alignment by construction (see v5ShapeCSS).
	{"corner-radius", V5Choice, []string{"square", "small", "medium", "large"}, "medium", "how round every corner is: cards (square 0, small 7px, medium 14px, large 21px), and the box, buttons and chips in proportion; the mark stays a circle"},
	{"spacing", V5Choice, []string{"compact", "default", "roomy"}, "default", "the room inside and between cards (a card's padding, the gap between cards, the footer's rule); never the space between the first row and the words under the mark"},
	{"stroke-weight", V5Choice, []string{"thin", "medium", "thick"}, "thin", "the outline of cards, the section box and the pinned row: 1px, 2px or 3px (rules inside a card stay hairlines)"},
}

// v5Spacing is each spacing value's geometry, in whole pixels so every edge
// falls on a pixel at 1x and 2x: a card's padding (top, right, bottom), the
// mark's distance from the card's left edge, the gap between cards, and the
// footer's rule under the body and its row under the rule. The mark keeps its
// 14px from the words (10px on a phone) at every value, and the first row and
// the second line are not here at all: those are what the style guide
// measures.
var v5Spacing = map[string][7]int{
	"compact": {18, 26, 16, 16, 6, 16, 12},
	"default": {24, 34, 22, 22, 10, 22, 16},
	"roomy":   {30, 42, 28, 28, 16, 28, 20},
}

// v5ShapeCSS is the shape tokens' custom properties: nothing for a default,
// so a theme that sets none writes what it always did.
func v5ShapeCSS(t *V5Theme, add func(prop, val string)) {
	if c := t.Get("corner-radius"); c != "medium" {
		add("corner", map[string]string{"square": "0", "small": ".5", "large": "1.5"}[c])
	}
	if sp := t.Get("spacing"); sp != "default" {
		v := v5Spacing[sp]
		for i, prop := range []string{"pad-t", "pad-r", "pad-b", "pad-edge", "gap", "rule-gap", "rule-pad"} {
			add(prop, fmt.Sprintf("%dpx", v[i]))
		}
	}
	if w := t.Get("stroke-weight"); w != "thin" {
		add("stroke", map[string]string{"medium": "2px", "thick": "3px"}[w])
	}
}

// v5Token finds a token by name.
func v5Token(name string) (V5Token, bool) {
	for _, t := range V5Tokens {
		if t.Name == name {
			return t, true
		}
	}
	return V5Token{}, false
}

// V5Fonts is the font catalog: the families a theme may name, and how each
// is asked of Google Fonts (weights the shape uses, as real weights, never a
// synthesised bold). A name outside it falls back to the default font
// (fontFallsBack), so a catalog refresh can never break a theme.
var V5Fonts = map[string]string{
	"Inter":            "Inter:wght@400;500;600;700",
	"Newsreader":       "Newsreader:ital,opsz,wght@0,6..72,400;0,6..72,500;0,6..72,600;0,6..72,700;1,6..72,400;1,6..72,500",
	"JetBrains Mono":   "JetBrains+Mono:wght@400;500",
	"Space Grotesk":    "Space+Grotesk:wght@400;500;600;700",
	"Playfair Display": "Playfair+Display:ital,wght@0,400;0,500;0,600;0,700;1,400;1,600",
	"Lora":             "Lora:ital,wght@0,400;0,500;0,600;0,700;1,400;1,500",
	"IBM Plex Sans":    "IBM+Plex+Sans:wght@400;500;600;700",
	"Source Serif 4":   "Source+Serif+4:ital,opsz,wght@0,8..60,400;0,8..60,600;0,8..60,700;1,8..60,400",
}

// v5FontFallback is the stack after a family: a serif or a sans.
var v5FontFallback = map[string]string{
	"Inter": "sans", "Space Grotesk": "sans", "IBM Plex Sans": "sans", "JetBrains Mono": "mono",
	"Newsreader": "serif", "Playfair Display": "serif", "Lora": "serif", "Source Serif 4": "serif",
}

// V5Theme is a theme's v5 definition, as read from its v5.json.
type V5Theme struct {
	Note   string            `json:"note,omitempty"`
	Tokens map[string]string `json:"tokens"`
	// FellBack names each font the catalog does not have, which draws in the
	// default instead: "font-ui: Comic Sans MS". Read, never written.
	FellBack []string `json:"-"`
}

// fontName is the shape of a family's name: letters, digits and spaces. A
// font-token value of this shape that the catalog does not have falls back to
// the default; any other value is refused, as every other token's is.
var fontName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ]{0,63}$`)

// fontFallsBack says whether a value falls back to the default font rather
// than being refused: a font token, holding a family's name the catalog does
// not have (and, for a choice-or-font token, not one of its choices).
func fontFallsBack(name, v string) bool {
	tok, ok := v5Token(name)
	if !ok || (tok.Kind != V5Font && tok.Kind != V5ChoiceOrFont) {
		return false
	}
	if _, known := V5Fonts[v]; known {
		return false
	}
	for _, a := range tok.Allowed {
		if v == a {
			return false
		}
	}
	return fontName.MatchString(v)
}

// ParseV5Theme reads a theme's v5.json and checks every value against the
// contract. An unknown token or a value the contract does not allow is
// refused, naming it. A font the catalog does not have is the one exception:
// it falls back to the default, and FellBack names it.
func ParseV5Theme(data []byte) (*V5Theme, error) {
	var t V5Theme
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf("v5 theme: %w", err)
	}
	if t.Tokens == nil {
		t.Tokens = map[string]string{}
	}
	if strings.Contains(t.Note, "*/") {
		return nil, fmt.Errorf("v5 theme: the note may not contain */")
	}
	var problems []string
	names := make([]string, 0, len(t.Tokens))
	for n := range t.Tokens {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if fontFallsBack(n, t.Tokens[n]) {
			t.FellBack = append(t.FellBack, n+": "+t.Tokens[n])
			delete(t.Tokens, n)
			continue
		}
		if err := checkV5Value(n, t.Tokens[n]); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("v5 theme: %s", strings.Join(problems, "; "))
	}
	return &t, nil
}

func checkV5Value(name, v string) error {
	tok, ok := v5Token(name)
	if !ok {
		return fmt.Errorf("%s is not a token", name)
	}
	isChoice := func() bool {
		for _, a := range tok.Allowed {
			if v == a {
				return true
			}
		}
		return false
	}
	switch tok.Kind {
	case V5Colour:
		if _, ok := parseHex(v); !ok {
			return fmt.Errorf("%s must be a colour like #1a2b3c (got %q)", name, v)
		}
	case V5Choice:
		if !isChoice() {
			return fmt.Errorf("%s must be one of %s (got %q)", name, strings.Join(tok.Allowed, ", "), v)
		}
	case V5ChoiceOrColour:
		if _, ok := parseHex(v); !ok && !isChoice() {
			return fmt.Errorf("%s must be %s or a colour (got %q)", name, strings.Join(tok.Allowed, ", "), v)
		}
	case V5Colours:
		parts := splitColours(v)
		if len(parts) < 1 || len(parts) > 3 {
			return fmt.Errorf("%s takes one to three colours (got %q)", name, v)
		}
		for _, p := range parts {
			if _, ok := parseHex(p); !ok && p != "site" {
				return fmt.Errorf("%s: %q is not a colour", name, p)
			}
		}
	case V5Font:
		if _, ok := V5Fonts[v]; !ok {
			return fmt.Errorf("%s: %q is not in the font catalog", name, v)
		}
	case V5ChoiceOrFont:
		if _, ok := V5Fonts[v]; !ok && !isChoice() {
			return fmt.Errorf("%s: %q is not in the font catalog", name, v)
		}
	}
	return nil
}

// Get is a token's value: the theme's, or the contract's default.
func (t *V5Theme) Get(name string) string {
	if t != nil {
		if v, ok := t.Tokens[name]; ok {
			return v
		}
	}
	tok, _ := v5Token(name)
	return tok.Default
}

// resolved colours a token stands for, after "site", "bg" and the like.
func (t *V5Theme) colour(name string) string {
	v := t.Get(name)
	switch {
	case name == "bar" && v == "bg":
		return t.Get("bg")
	case name == "accent-2" && v == "site":
		return t.Get("site")
	case name == "heading-color" && v == "text":
		return t.Get("text")
	case name == "heading-color" && v == "site":
		return t.Get("site")
	case name == "rollover-color" && v == "stroke":
		return t.Get("site")
	}
	return v
}

// SiteText is the site colour as text: the site colour itself where it
// reaches 4.5:1 on a card, else moved toward the text colour until it does
// (the contract's derived link and filled-button colour).
func (t *V5Theme) SiteText() string { return t.asText(t.Get("site")) }

// asText is a colour moved toward the text colour until it reaches 4.5:1 on
// a card, so an accent stays legible as small text whatever hue it is.
func (t *V5Theme) asText(colour string) string {
	start, _ := parseHex(colour)
	surface, _ := parseHex(t.Get("surface"))
	toward, _ := parseHex(t.Get("text"))
	c := start
	for i := 0; i <= 50 && contrast(c, surface) < 4.5; i++ {
		c = mix(start, toward, float64(i)/50)
	}
	return c.hex()
}

// V5ContrastProblem is one pair that fails its ratio.
type V5ContrastProblem struct {
	Pair         string
	Ratio, Needs float64
}

func (p V5ContrastProblem) String() string {
	return fmt.Sprintf("%s reaches %.2f:1, needs %.1f:1", p.Pair, p.Ratio, p.Needs)
}

// CheckV5Contrast is the contract's contrast check: every pair the contract
// names, with the ratio it needs. Text sits only on cards, the section box,
// the panel and the bar, never on the page, so the page needs no ratio.
func CheckV5Contrast(t *V5Theme) []V5ContrastProblem {
	type pair struct {
		a, b  string
		needs float64
	}
	pairs := []pair{
		{"text", "surface", 4.5}, {"text", "panel", 4.5}, {"text", "bar", 4.5},
		{"text-soft", "surface", 4.5},
		{"text-muted", "surface", 4.5}, {"text-muted", "bar", 4.5},
		{"site", "surface", 3},
		{"accent-2", "surface", 3},
		{"heading-color", "surface", 3},
		{"rollover-color", "surface", 3},
		{"wordmark", "bar", 3},
	}
	var out []V5ContrastProblem
	for _, p := range pairs {
		a, _ := parseHex(t.colour(p.a))
		b, _ := parseHex(t.colour(p.b))
		if r := contrast(a, b); r < p.needs {
			out = append(out, V5ContrastProblem{p.a + " on " + p.b, r, p.needs})
		}
	}
	// Derived: text on a filled button (the site colour as text, under ink).
	ink, _ := parseHex(t.Get("ink"))
	fill, _ := parseHex(t.SiteText())
	if r := contrast(ink, fill); r < 4.5 {
		out = append(out, V5ContrastProblem{"ink on a filled button", r, 4.5})
	}
	if t.Get("mark") != "own" {
		m, _ := parseHex(t.Get("mark"))
		s, _ := parseHex(t.Get("surface"))
		if r := contrast(m, s); r < 3 {
			out = append(out, V5ContrastProblem{"mark on surface", r, 3})
		}
	}
	return out
}

// V5ThemeCSS writes a theme's values as the custom properties the shape's
// stylesheet draws from, after the note (as a comment) and a header naming
// the theme. Only what the theme sets is written, with what derives from it;
// everything else stays the shape's polis default.
func V5ThemeCSS(name string, t *V5Theme) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n/* ---------- The theme: %s (its one definition is themes/%s/v5.json) ---------- */\n", name, name)
	if t.Note != "" {
		b.WriteString("/*\n")
		for _, line := range strings.Split(strings.TrimRight(t.Note, "\n"), "\n") {
			b.WriteString("   " + line + "\n")
		}
		b.WriteString("*/\n")
	}
	set := func(n string) bool { _, ok := t.Tokens[n]; return ok }
	var decl []string
	add := func(prop, val string) { decl = append(decl, fmt.Sprintf("  --%s: %s;", prop, val)) }

	if t.Get("scheme") == "dark" {
		add("scheme", "dark")
		add("card-shadow", "0 4px 12px rgba(0, 0, 0, .3)")
	}
	for _, n := range []string{"site", "bg", "surface", "card-hover", "panel", "text", "text-soft", "text-muted", "ink", "mark-ink", "wordmark"} {
		if set(n) {
			add(n, t.Get(n))
		}
	}
	if set("site") || set("surface") || set("text") {
		add("site-text", t.SiteText())
	}
	if set("bar") {
		add("bar", t.colour("bar"))
	}
	if set("accent-2") {
		add("accent-2", t.colour("accent-2"))
		add("accent-2-text", t.asText(t.colour("accent-2")))
	}
	if set("heading-color") {
		add("title", t.colour("heading-color"))
	}
	if set("heading-weight") {
		add("title-weight", map[string]string{"regular": "400", "medium": "500", "semibold": "600", "bold": "700"}[t.Get("heading-weight")])
	}
	if t.Get("heading-style") == "italic" {
		add("title-style", "italic")
	}
	if t.Get("heading-case") == "small-caps" {
		add("title-caps", "small-caps")
	}
	if set("font-content") {
		add("font-content", fontStack(t.Get("font-content")))
	}
	if set("font-ui") {
		add("font-ui", fontStack(t.Get("font-ui")))
	}
	if h := t.Get("font-heading"); h != "content" {
		add("font-title", fontStack(h))
	}
	if t.Get("date-style") == "signal" {
		add("date-font", "var(--font-mono)")
		add("date-size", "11.5px")
		add("date-color", "var(--accent-2-text)")
		add("date-lead", `"▸ "`)
	}
	if img, size := v5PageImage(t); img != "" {
		add("page-image", img)
		add("page-size", size)
	}
	if t.Get("card-texture") == "grain" {
		add("card-texture", grainURL(t.Get("text"), 0.75, 0.10))
	}
	switch t.Get("rollover") {
	case "off":
		add("card-hover", "var(--surface)")
		add("roll", "var(--edge)")
	case "glow":
		add("roll-glow", "0 0 0 4px color-mix(in srgb, var(--roll) 30%, transparent), 0 0 30px 4px color-mix(in srgb, var(--roll) 45%, transparent)")
	}
	if c := t.Get("rollover-color"); c != "stroke" && t.Get("rollover") != "off" {
		add("roll", c)
	}
	switch t.Get("card-edge") {
	case "none":
		add("edge", "transparent")
	case "hairline":
		add("edge", "var(--line)")
	}
	switch t.Get("link-style") {
	case "dotted", "wavy":
		add("link-line-style", t.Get("link-style"))
	case "hover":
		add("link-line", "none")
	}
	switch t.Get("quote-style") {
	case "mark":
		add("quote-edge", "0")
		add("quote-mark", `"“"`)
	case "block":
		add("quote-edge", "0")
		add("quote-bg", "color-mix(in srgb, var(--site) 8%, transparent)")
	}
	if t.Get("top-bar") == "translucent" {
		add("bar-fill", "color-mix(in srgb, var(--bar) 78%, transparent)")
		add("bar-blur", "blur(10px)")
	}
	switch t.Get("motion") {
	case "calm":
		add("motion", "1.6")
	case "lively":
		add("motion", ".6")
	}
	v5ShapeCSS(t, add)
	if len(decl) > 0 {
		b.WriteString(":root {\n" + strings.Join(decl, "\n") + "\n}\n")
	}
	return b.String()
}

// fontStack is a family and its fallbacks.
func fontStack(family string) string {
	switch v5FontFallback[family] {
	case "serif":
		return fmt.Sprintf("'%s', Georgia, 'Times New Roman', serif", family)
	case "mono":
		return fmt.Sprintf("'%s', 'SF Mono', Consolas, monospace", family)
	}
	return fmt.Sprintf("'%s', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif", family)
}

// V5FontsHref is the address of the fonts a theme's pages use: built by the
// shape from the catalog, never named by a theme. JetBrains Mono is always
// there (dates, code).
func V5FontsHref(t *V5Theme) string {
	families := []string{t.Get("font-ui"), t.Get("font-content")}
	if h := t.Get("font-heading"); h != "content" {
		families = append(families, h)
	}
	families = append(families, "JetBrains Mono")
	seen := map[string]bool{}
	var q []string
	for _, f := range families {
		if spec, ok := V5Fonts[f]; ok && !seen[f] {
			seen[f] = true
			q = append(q, "family="+spec)
		}
	}
	return "https://fonts.googleapis.com/css2?" + strings.Join(q, "&") + "&display=swap"
}

// V5MarkStyle is the inline fill of the site's own mark when the theme sets
// it (mark is not "own"): its colours, with the site's own pattern drawn over
// them in mark-ink. Empty when the mark keeps its own colours. The mark's
// pattern and initial never change; only their colours do.
func V5MarkStyle(t *V5Theme) (fill, ink string) {
	if t == nil || t.Get("mark") == "own" {
		return "", ""
	}
	a, b := t.Get("mark"), t.Get("mark-2")
	switch t.Get("mark-style") {
	case "gradient":
		fill = fmt.Sprintf("linear-gradient(135deg, %s, %s)", a, b)
	case "split":
		fill = fmt.Sprintf("linear-gradient(118deg, %s 0 46%%, %s 46%% 52%%, %s 52%%)", a, t.Get("surface"), b)
	case "ring":
		fill = fmt.Sprintf("radial-gradient(circle, %s 0 52%%, %s 54%% 62%%, %s 64%%)", a, t.Get("surface"), b)
	default:
		fill = fmt.Sprintf("linear-gradient(%s, %s)", a, a)
	}
	return fill, t.Get("mark-ink")
}

// ---------- The page's pattern, drawn by polis from its settings ----------

// v5PageImage is the page's background layers (the pattern, then the
// gradient) and their sizes.
func v5PageImage(t *V5Theme) (string, string) {
	var layers, sizes []string
	if k := t.Get("texture"); k != "none" {
		cols := splitColours(t.Get("texture-colors"))
		for i, c := range cols {
			if c == "site" {
				cols[i] = t.Get("site")
			}
		}
		scale := map[string]float64{"small": 0.75, "medium": 1, "large": 1.5}[t.Get("texture-scale")]
		dense := map[string]float64{"sparse": 1.5, "medium": 1, "dense": 0.7}[t.Get("texture-density")]
		alpha := map[string]float64{"faint": 0.35, "soft": 0.55, "clear": 0.8}[t.Get("texture-strength")]
		l, s := texture(k, cols, scale, dense, alpha)
		layers, sizes = append(layers, l...), append(sizes, s...)
	}
	if g := t.Get("bg-gradient"); g != "none" {
		// "down" is CSS's own default direction, so it is written as before,
		// byte for byte; any other direction is named.
		dir := ""
		if d := v5GradientCSS[t.Get("bg-gradient-angle")]; d != "" {
			dir = d + ", "
		}
		layers = append(layers, fmt.Sprintf("linear-gradient(%s%s, %s)", dir, t.Get("bg"), g))
		sizes = append(sizes, "auto")
	}
	return strings.Join(layers, ", "), strings.Join(sizes, ", ")
}

func texture(kind string, cols []string, scale, dense, alpha float64) ([]string, []string) {
	rgba := func(c string, a float64) string {
		x, _ := parseHex(c)
		return fmt.Sprintf("rgba(%d, %d, %d, %s)", int(x.r*255+.5), int(x.g*255+.5), int(x.b*255+.5), trimFloat(a))
	}
	px := func(v float64) string { return trimFloat(math.Round(v)) + "px" }
	switch kind {
	case "grain":
		freq := map[bool]float64{true: .9, false: .75}[scale <= 1]
		return []string{grainURL(cols[0], freq, alpha*0.2)}, []string{px(160 * scale)}
	case "dots":
		step := 18 * scale * dense
		return []string{fmt.Sprintf("radial-gradient(circle, %s 0 1px, transparent 1.6px)", rgba(cols[0], alpha))}, []string{px(step) + " " + px(step)}
	case "glitter":
		at := []string{"20% 30%", "70% 60%", "45% 85%"}
		base := []float64{37, 53, 61}
		var ls, ss []string
		for i := 0; i < 3; i++ {
			c := cols[i%len(cols)]
			ls = append(ls, fmt.Sprintf("radial-gradient(circle at %s, %s 0 1px, transparent 1.6px)", at[i], rgba(c, alpha)))
			ss = append(ss, px(base[i]*scale*dense)+" "+px((base[i]+6-float64(i)*2)*scale*dense))
		}
		return ls, ss
	case "lines":
		gap := 14 * scale * dense
		return []string{fmt.Sprintf("repeating-linear-gradient(135deg, %s 0 1px, transparent 1px %s)", rgba(cols[0], alpha*0.5), px(gap))}, []string{"auto"}
	case "grid":
		step := 28 * scale * dense
		line := rgba(cols[0], alpha*0.4)
		return []string{
			fmt.Sprintf("linear-gradient(%s 1px, transparent 1px)", line),
			fmt.Sprintf("linear-gradient(90deg, %s 1px, transparent 1px)", line),
		}, []string{px(step) + " " + px(step), px(step) + " " + px(step)}
	case "waves":
		w := 120 * scale
		h := 24 * scale * dense
		svg := fmt.Sprintf("<svg xmlns='http://www.w3.org/2000/svg' width='%s' height='%s'><path d='M0 %s q%s -%s %s 0 t%s 0' fill='none' stroke='%s' stroke-opacity='%s' stroke-width='1'/></svg>",
			trimFloat(w), trimFloat(h), trimFloat(h/2), trimFloat(w/4), trimFloat(h/3), trimFloat(w/2), trimFloat(w/2), cols[0], trimFloat(alpha*0.6))
		return []string{svgURL(svg)}, []string{px(w) + " " + px(h)}
	}
	return nil, nil
}

// grainURL is a film grain in one colour, at an opacity: an SVG noise filter,
// as a data: image (nothing fetched).
func grainURL(colour string, freq, opacity float64) string {
	x, _ := parseHex(colour)
	svg := fmt.Sprintf("<svg xmlns='http://www.w3.org/2000/svg' width='160' height='160'><filter id='n'><feTurbulence type='fractalNoise' baseFrequency='%s' numOctaves='2' stitchTiles='stitch'/><feColorMatrix values='0 0 0 0 %s  0 0 0 0 %s  0 0 0 0 %s  0 0 0 %s 0'/></filter><rect width='100%%' height='100%%' filter='url(#n)'/></svg>",
		trimFloat(freq), trimFloat(round2(x.r)), trimFloat(round2(x.g)), trimFloat(round2(x.b)), trimFloat(round2(opacity)))
	return svgURL(svg)
}

func svgURL(svg string) string {
	esc := strings.NewReplacer("#", "%23", "<", "%3C", ">", "%3E", `"`, "'").Replace(svg)
	return `url("data:image/svg+xml;utf8,` + esc + `")`
}

// ---------- Colour arithmetic ----------

type rgb struct{ r, g, b float64 }

func parseHex(s string) (rgb, bool) {
	if !strings.HasPrefix(s, "#") || (len(s) != 4 && len(s) != 7) {
		return rgb{}, false
	}
	h := s[1:]
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return rgb{}, false
	}
	return rgb{float64(v>>16&0xff) / 255, float64(v>>8&0xff) / 255, float64(v&0xff) / 255}, true
}

func (c rgb) hex() string {
	return fmt.Sprintf("#%02x%02x%02x", int(c.r*255+.5), int(c.g*255+.5), int(c.b*255+.5))
}

func mix(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

func luminance(c rgb) float64 {
	ch := func(v float64) float64 {
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.r) + 0.7152*ch(c.g) + 0.0722*ch(c.b)
}

// contrast is the WCAG contrast ratio of two colours.
func contrast(a, b rgb) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func splitColours(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func trimFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
