package license

import (
	"fmt"
	"strings"
	"time"
)

// The frontmatter key under which materialised terms live.
const FrontmatterKey = "license"

// blockKeys is the emission order for the materialised YAML block. Fixed so the
// bytes are deterministic — they are inside a signature.
//
// ⚠️ CONSTRAINT ON THIS KEY SET, and it is not obvious: most polis frontmatter
// parsers skip indented lines, but judge.parseFrontmatter and
// judge.parseCommentFrontmatter trim a key BEFORE matching it, so they do not.
// A nested child named `signature:` or `current-version:` would be read as
// though it were a top-level field. No key below may ever take one of those
// names. (Recorded rather than fixed: the general form of that bug belongs to
// the typed-signing-base epic.)
var blockKeys = []string{"v", "profile", "train-ai", "search", "ai-input", "attribution", "terms", "contact", "asserted"}

// Block renders Terms as the nested YAML `license:` block that gets stamped
// into a work's frontmatter at publish time.
//
// This is the materialisation — the artifact that TRAVELS. Quote, mirror, or
// scrape the work and no site context comes with it, so the block carries both
// the profile name AND the expanded values: profile alone would force a reader
// to resolve something, values alone would lose which preset was chosen. Both
// means the terms are readable without fetching anything.
//
// Returns "" for nil terms — absent means unstated, which is a defined state.
func Block(t *Terms) string {
	if t == nil {
		return ""
	}
	vals := map[string]string{
		"v":           t.V,
		"profile":     t.Profile,
		"train-ai":    t.TrainAI,
		"search":      t.Search,
		"ai-input":    t.AIInput,
		"attribution": t.Attribution,
		"terms":       t.Terms,
		"contact":     t.Contact,
		"asserted":    t.Asserted,
	}
	var b strings.Builder
	b.WriteString(FrontmatterKey + ":\n")
	for _, k := range blockKeys {
		v := vals[k]
		if v == "" {
			continue
		}
		fmt.Fprintf(&b, "  %s: %s\n", k, yamlScalar(v))
	}
	return strings.TrimRight(b.String(), "\n")
}

// yamlScalar quotes a value only when it needs it. Licence values are drawn
// from fixed vocabularies and URLs on the author's own domain, so quoting is
// almost never required — but a URL containing ": " would otherwise reparse as
// a nested key.
func yamlScalar(s string) string {
	if strings.Contains(s, ": ") || strings.HasSuffix(s, ":") ||
		strings.ContainsAny(s, "\n\"") ||
		strings.HasPrefix(s, " ") || strings.HasSuffix(s, " ") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// ParseBlock reads a materialised `license:` block back out of a work's
// frontmatter. Returns nil when the work carries no terms — which is not an
// error, it is the author not having said.
//
// Modelled on publish.ExtractVersionHistory: a dedicated extractor beside the
// flat parsers rather than a change to them. The shared map[string]string
// parsers deliberately skip indented lines, and that behaviour is relied on
// elsewhere; widening them to understand nesting would be a much larger blast
// radius than this epic warrants.
func ParseBlock(content string) *Terms {
	fm, ok := frontmatterSection(content)
	if !ok {
		return nil
	}

	var (
		t      Terms
		inside bool
		found  bool
	)
	for _, line := range strings.Split(fm, "\n") {
		if !inside {
			if strings.TrimRight(line, " \t") == FrontmatterKey+":" {
				inside = true
			}
			continue
		}
		// A non-indented, non-empty line ends the block.
		if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		key, val, cut := strings.Cut(trimmed, ":")
		if !cut {
			continue
		}
		val = unquoteScalar(strings.TrimSpace(val))
		if val == "" {
			continue
		}
		found = true
		switch strings.TrimSpace(key) {
		case "v":
			t.V = val
		case "profile":
			t.Profile = val
		case "train-ai":
			t.TrainAI = val
		case "search":
			t.Search = val
		case "ai-input":
			t.AIInput = val
		case "attribution":
			t.Attribution = val
		case "terms":
			t.Terms = val
		case "contact":
			t.Contact = val
		case "asserted":
			t.Asserted = val
		}
	}
	if !found {
		return nil
	}
	return &t
}

// AuthoredProfile reads what the AUTHOR wrote, as opposed to what publish
// stamped. Authors write the terse form — `license: reserved` — or nothing at
// all, in which case the site default applies. Conflating the authored and
// materialised forms is the trap; they are different shapes of the same key.
//
// Returns ("", false) when the key is absent or is already a materialised
// block rather than a scalar.
func AuthoredProfile(content string) (string, bool) {
	fm, ok := frontmatterSection(content)
	if !ok {
		return "", false
	}
	for _, line := range strings.Split(fm, "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		key, val, cut := strings.Cut(line, ":")
		if !cut || strings.TrimSpace(key) != FrontmatterKey {
			continue
		}
		val = unquoteScalar(strings.TrimSpace(val))
		if val == "" {
			return "", false // `license:` opening a nested block
		}
		return val, true
	}
	return "", false
}

// StripBlock removes an existing `license:` block (or scalar) from frontmatter.
// Used on republish, where the work is re-materialised rather than accumulating
// stacked blocks.
func StripBlock(frontmatter string) string {
	lines := strings.Split(frontmatter, "\n")
	out := make([]string, 0, len(lines))
	skipping := false
	for _, line := range lines {
		if skipping {
			if line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				skipping = false
			} else {
				continue
			}
		}
		if key, _, cut := strings.Cut(line, ":"); cut &&
			!strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") &&
			strings.TrimSpace(key) == FrontmatterKey {
			skipping = true
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Resolve produces the terms to stamp into a work at publish time.
//
// Precedence is work > site, resolved here and frozen into the signed payload.
// (A directory cascade would sit between the two; it is deferred, and safe to
// defer precisely because materialisation gives it no wire footprint — the
// published output is resolved terms either way, so inserting a middle layer
// later changes nothing already fetched.)
//
// authored is the profile name the author wrote on the work, or "" for none.
// site is the terms from license.json, or nil if the site has stated none.
// Returns nil when neither states anything: absent means unstated. Not
// permitted, not denied — silence is silence, and consumers hold the policy.
func Resolve(authored string, site *Terms, baseURL string) (*Terms, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	if authored != "" {
		profile, stated, err := ParseProfileName(authored)
		if err != nil {
			return nil, err
		}
		if !stated {
			// An explicit `license: none` on the work overrides a site default
			// with silence. Saying "no terms here" is itself a choice.
			return nil, nil
		}
		return ProfileTerms(profile, baseURL, now)
	}

	if site == nil {
		return nil, nil
	}

	// Copy the site's terms and stamp the assertion time of THIS work. The
	// site file says what she would grant now; the work records what she
	// granted then, and the date is what makes that question answerable.
	stamped := *site
	stamped.Asserted = now
	if baseURL != "" && stamped.Terms == "" {
		base := strings.TrimRight(baseURL, "/")
		stamped.Terms = base + "/license"
	}
	return &stamped, nil
}

// frontmatterSection returns the text between the opening and closing `---`.
func frontmatterSection(content string) (string, bool) {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return "", false
	}
	rest := trimmed[3:]
	rest = strings.TrimPrefix(rest, "\n")
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return "", false
	}
	return rest[:end], true
}

// unquoteScalar reverses yamlScalar for the simple forms polis emits.
func unquoteScalar(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return strings.ReplaceAll(v[1:len(v)-1], `\"`, `"`)
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
	}
	return v
}
