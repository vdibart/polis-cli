package theme

// ⭐ THE SITE'S OWN TOKEN VALUES: the owner's choices, layered over the active
// theme's (the v5 spec, part 6, "How the tokens are set").
//
// A tour:
//
//  1. The owner's values are a file on the site, SiteTokensKey, in the same
//     form as a theme's own definition ({"note", "tokens"}), so the owner's
//     look is ready to be shared as a theme later. It is a source and the
//     site's own: beside custom.css, outside the installed bundle, so a
//     bundle resync never touches it and a theme switch keeps it.
//  2. SetSiteTokens and ResetSiteTokens are the only writers. Every value is
//     checked against the contract, and the combination (the theme with the
//     owner's values over it) against the contrast check, before anything is
//     written: a refused change leaves the site as it was. Each returns the
//     values the site had before, so a caller can put them back exactly
//     (enhance's Randomise, then Revert).
//  3. LoadV5SiteTheme is what a render draws: the active theme with the
//     owner's values over it. A file that fails the contract or the contrast
//     check at render (written by hand, or by an older build) is withheld,
//     never deleted, and the render goes on with the theme alone.
//
// ⛔ Site-wide only: there is no per-post or per-element value.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// V5ContractVersion is the token contract's version. It moves when a token is
// added, removed or changes meaning, so an agent can tell the vocabulary it
// learned from theme.tokens is still the one the site speaks.
const V5ContractVersion = "1"

// SiteTokensKey is the site's own token values.
const SiteTokensKey = "site/v5.json" //nolint:gosec // G101: a file path for design tokens (colours, fonts), not a credential

// V5TokenInfo is one token as the contract describes it to a reader.
type V5TokenInfo struct {
	Name    string   `json:"name"`
	Kind    string   `json:"kind"`
	Allowed []string `json:"allowed,omitempty"`
	Default string   `json:"default"`
	Changes string   `json:"changes"`
}

// kindNames are the kinds as a reader sees them.
var kindNames = map[V5Kind]string{
	V5Colour:         "colour",
	V5Choice:         "choice",
	V5ChoiceOrColour: "choice or colour",
	V5Colours:        "one to three colours",
	V5Font:           "font",
	V5ChoiceOrFont:   "choice or font",
}

// V5Contract is every token, in the declaration's order.
func V5Contract() []V5TokenInfo {
	out := make([]V5TokenInfo, 0, len(V5Tokens))
	for _, t := range V5Tokens {
		k := kindNames[t.Kind]
		if k == "" {
			k = "value"
		}
		out = append(out, V5TokenInfo{Name: t.Name, Kind: k, Allowed: t.Allowed, Default: t.Default, Changes: t.Changes})
	}
	return out
}

// V5FontNames are the families a font token may name, sorted.
func V5FontNames() []string {
	out := make([]string, 0, len(V5Fonts))
	for f := range V5Fonts {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// ReadSiteTokens reads the site's own values: empty when it has none.
func ReadSiteTokens(site storage.Site) (*V5Theme, error) {
	b, err := site.Store().Get(context.Background(), SiteTokensKey)
	if errors.Is(err, fs.ErrNotExist) {
		return &V5Theme{Tokens: map[string]string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", SiteTokensKey, err)
	}
	return ParseV5Theme(b)
}

// LoadV5SiteTheme is what a v5 render draws: the active theme's definition
// with the site's own values over it. withheld says why the site's values
// were left out, when they were; the theme alone is drawn then. err is the
// theme's own (LoadV5Theme): a refused theme draws in the polis default, and
// the site's values over that default.
func LoadV5SiteTheme(site storage.Site, themeName string) (t *V5Theme, withheld string, err error) {
	base, err := LoadV5Theme(site, themeName)
	if err != nil {
		base = nil
	}
	own, rerr := ReadSiteTokens(site)
	if rerr != nil {
		return base, "the site's own values do not pass the token contract: " + rerr.Error(), err
	}
	if len(own.Tokens) == 0 {
		return base, "", err
	}
	merged := layer(base, own)
	if problems := CheckV5Contrast(merged); len(problems) > 0 {
		return base, "the site's own values fail the contrast check over this theme: " + joinProblems(problems), err
	}
	return merged, "", err
}

// layer is the theme with the site's values over it. The theme's note stays;
// the site's fonts that fell back are carried so the render can say so.
func layer(base, own *V5Theme) *V5Theme {
	out := &V5Theme{Tokens: map[string]string{}}
	if base != nil {
		out.Note = base.Note
		out.FellBack = append(out.FellBack, base.FellBack...)
		for k, v := range base.Tokens {
			out.Tokens[k] = v
		}
	}
	for k, v := range own.Tokens {
		out.Tokens[k] = v
	}
	out.FellBack = append(out.FellBack, own.FellBack...)
	return out
}

func joinProblems(ps []V5ContrastProblem) string {
	why := make([]string, 0, len(ps))
	for _, p := range ps {
		why = append(why, p.String())
	}
	return strings.Join(why, "; ")
}

// V5TokenError is a refused change: every reason, so one answer names them all.
type V5TokenError struct{ Reasons []string }

func (e *V5TokenError) Error() string { return strings.Join(e.Reasons, "; ") }

// SetSiteTokens sets some of the site's own values (replace: the given values
// become all of them). Every value is checked, and the result over the active
// theme must pass the contrast check; a refused change writes nothing. A font
// the catalog does not have is refused here, not fallen back to: whoever sets
// it is there to be told. It returns the site's own values from before.
func SetSiteTokens(site storage.Site, themeName string, values map[string]string, replace bool) (previous map[string]string, err error) {
	own, err := ReadSiteTokens(site)
	if err != nil {
		return nil, err
	}
	previous = copyMap(own.Tokens)
	var reasons []string
	names := make([]string, 0, len(values))
	for n := range values {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := checkV5Value(n, values[n]); err != nil {
			reasons = append(reasons, err.Error())
		}
	}
	if len(reasons) > 0 {
		return previous, &V5TokenError{Reasons: reasons}
	}
	next := copyMap(own.Tokens)
	if replace {
		next = map[string]string{}
	}
	for n, v := range values {
		next[n] = v
	}
	if err := writeSiteTokens(site, themeName, own.Note, next); err != nil {
		return previous, err
	}
	return previous, nil
}

// ResetSiteTokens puts some of the site's values (or all, with names empty)
// back to the active theme's, by removing them from the site's own. It
// returns the site's own values from before.
func ResetSiteTokens(site storage.Site, themeName string, names []string) (previous map[string]string, err error) {
	own, err := ReadSiteTokens(site)
	if err != nil {
		return nil, err
	}
	previous = copyMap(own.Tokens)
	var reasons []string
	for _, n := range names {
		if _, ok := v5Token(n); !ok {
			reasons = append(reasons, n+" is not a token")
		}
	}
	if len(reasons) > 0 {
		return previous, &V5TokenError{Reasons: reasons}
	}
	next := map[string]string{}
	if len(names) > 0 {
		next = copyMap(own.Tokens)
		for _, n := range names {
			delete(next, n)
		}
	}
	if err := writeSiteTokens(site, themeName, own.Note, next); err != nil {
		return previous, err
	}
	return previous, nil
}

// writeSiteTokens checks the result over the active theme and writes it, or
// removes the file when no value is left.
func writeSiteTokens(site storage.Site, themeName, note string, values map[string]string) error {
	ctx := context.Background()
	if len(values) == 0 {
		if err := site.Store().Delete(ctx, SiteTokensKey); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", SiteTokensKey, err)
		}
		return nil
	}
	base, _ := LoadV5Theme(site, themeName)
	if problems := CheckV5Contrast(layer(base, &V5Theme{Tokens: values})); len(problems) > 0 {
		reasons := make([]string, 0, len(problems))
		for _, p := range problems {
			reasons = append(reasons, "fails the contrast check: "+p.String())
		}
		return &V5TokenError{Reasons: reasons}
	}
	b, err := json.MarshalIndent(V5Theme{Note: note, Tokens: values}, "", "  ")
	if err != nil {
		return err
	}
	return site.Store().Put(ctx, SiteTokensKey, append(b, '\n'))
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
