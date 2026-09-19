package render

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// RenderLicenseSurfaces writes every generated licence surface: the site terms
// page, the versioned profile explanations, robots.txt, and rsl.xml.
//
// All of them are PROJECTIONS of the signed license.json. None is ever
// hand-authored, which is the property that makes them impossible to
// contradict: there is one source, so there is nothing to diverge from.
//
// A site that has stated no terms gets nothing — not an empty robots.txt, not a
// terms page saying "no terms". The host must not speak for the user, and an
// unsigned default would contradict the signed layer's silence in the most
// visible place.
func (r *PageRenderer) RenderLicenseSurfaces() error {
	terms, err := site.SiteTerms(r.config.DataDir)
	if err != nil {
		return fmt.Errorf("read site licence: %w", err)
	}
	if terms == nil {
		return nil
	}

	mountDir, err := r.licenseMountDir()
	if err != nil {
		return err
	}
	outDir := filepath.Join(r.config.DataDir, mountDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create licence mount dir: %w", err)
	}

	if err := r.writeSiteTermsPage(outDir, mountDir, terms); err != nil {
		return err
	}
	if err := r.writeProfilePage(outDir, mountDir, terms.Profile); err != nil {
		return err
	}
	if err := r.writeRobotsAndRSL(); err != nil {
		return err
	}
	return nil
}

// licenseMountDir returns the site's own mount for the licence type, honouring
// its bundle declaration rather than assuming /license.
//
// Delegates to site.LicenseMountDir so the writer of these pages and the
// withdrawal that removes them resolve the same directory. Two answers here
// would mean withdrawal cleaning a mount the renderer never wrote to.
func (r *PageRenderer) licenseMountDir() (string, error) {
	return site.LicenseMountDir(r.config.DataDir), nil
}

// profileSlug turns a profile tag into a filename. The tag itself stays the
// identifier everywhere it matters; this is only a place to put a page.
func profileSlug(profile string) string {
	s := strings.TrimPrefix(profile, "pub.polis.license.")
	return strings.ReplaceAll(s, "/", "-")
}

// writeSiteTermsPage renders the surface that answers "what are her terms NOW?"
//
// ⚠️ It MUST state non-retroactivity. Without that, a reader who sees
// restrictive current terms assumes older posts are covered — or, worse, sees
// permissive current terms and treats an older reserved post as fair game. A
// terms page that omits it is not merely incomplete, it actively misleads.
func (r *PageRenderer) writeSiteTermsPage(outDir, mountDir string, t *license.Terms) error {
	profileURL := "/" + mountDir + "/" + profileSlug(t.Profile) + ".html"
	sourceURL := site.LicenseURLPath(r.config.DataDir)

	var b strings.Builder
	fmt.Fprintf(&b, `<h1 class="license-title">Terms of use</h1>`)
	fmt.Fprintf(&b, `<p class="license-lede">%s</p>`, html.EscapeString(TermsSummary(t)))

	b.WriteString(`<h2>What this means, exactly</h2>`)
	b.WriteString(`<table class="license-table"><thead><tr><th>Term</th><th>Value</th><th>Layer</th></tr></thead><tbody>`)
	// The layer column is not decoration. AIPREF is a PREFERENCE signal and
	// explicitly not a licence; RSL is genuine licensing. Presenting them as
	// one thing overclaims in a way anyone in that world catches immediately.
	for _, row := range []struct{ key, val, layer, note string }{
		{"train-ai", t.TrainAI, "IETF AIPREF", "use in producing or refining a generative model"},
		{"search", t.Search, "IETF AIPREF", "search that links back to the original; excludes summarising"},
		{"ai-input", t.AIInput, "RSL", "summarising the work into an answer"},
		{"attribution", t.Attribution, "RSL", "credit when the work is used"},
	} {
		if row.val == "" {
			continue
		}
		fmt.Fprintf(&b, `<tr><td><code>%s</code><span class="license-note">%s</span></td><td>%s</td><td>%s</td></tr>`,
			html.EscapeString(row.key), html.EscapeString(row.note),
			html.EscapeString(readableValue(row.val)), html.EscapeString(row.layer))
	}
	b.WriteString(`</tbody></table>`)
	b.WriteString(`<p class="license-layers">The AIPREF rows are a <strong>preference</strong> — a stated wish, honoured or not. ` +
		`The RSL rows are <strong>licence terms</strong> — a grant, on conditions. ` +
		`Neither is enforcement: this is evidence, not a fence.</p>`)

	// The required statement.
	b.WriteString(`<h2 class="license-heading-warn">These terms are not retroactive</h2>`)
	b.WriteString(`<p class="license-nonretro"><strong>These are my terms going forward. Each work carries its own.</strong> ` +
		`Every post was signed with the terms in force the day it was published, and those terms are inside its signature. ` +
		`Changing this page does not change them — for anything already published, look at the work itself.</p>`)

	fmt.Fprintf(&b, `<h2>Sources</h2><ul class="license-sources">`)
	fmt.Fprintf(&b, `<li><a href="%s">What <code>%s</code> means</a> — the permanent explanation of this profile</li>`,
		html.EscapeString(profileURL), html.EscapeString(t.Profile))
	fmt.Fprintf(&b, `<li><a href="%s">The signed licence</a> — the source of truth everything here is generated from</li>`,
		html.EscapeString(sourceURL))
	fmt.Fprintf(&b, `<li><a href="/rsl.xml">rsl.xml</a> and <a href="/robots.txt">robots.txt</a> — the same terms, machine-readable</li>`)
	b.WriteString(`</ul>`)

	if t.Contact != "" {
		fmt.Fprintf(&b, `<h2>Anything else</h2><p>For any use not granted above, ask: <a href="%s">%s</a>. `+
			`That is the point of publishing terms at all — so there is somewhere to ask.</p>`,
			html.EscapeString(t.Contact), html.EscapeString(t.Contact))
	}

	return r.writeLicenseHTML(filepath.Join(outDir, "index.html"), mountDir, "Terms of use", b.String())
}

// readableValue turns an AIPREF/RSL token into something a person reads. The
// raw token stays visible beside it on the profile page — nobody should have to
// take our word for what `n` meant.
func readableValue(v string) string {
	switch v {
	case license.Allow:
		return "allowed (y)"
	case license.Disallow:
		return "not allowed (n)"
	case license.AttributionRequired:
		return "required"
	default:
		return v
	}
}

// writeProfilePage renders the permanent explanation of one profile version.
//
// One page per version, and it never changes: a 2026 post's profile has to mean
// in 2030 exactly what it meant when it was signed. It is also only a
// CONVENIENCE — the profile is a semantic tag, the operative values travel in
// the payload, and if every copy of this page vanished the terms would still say
// everything they say.
//
// It is a DEED, not legal code. Creative Commons splits licences three ways —
// legal code, human-readable deed, machine-readable metadata — and polis writes
// the last two only. We name selections from other people's vocabularies; we
// never author terms.
func (r *PageRenderer) writeProfilePage(outDir, mountDir, profile string) error {
	terms, err := license.ProfileTerms(profile, "", "")
	if err != nil {
		return nil // unknown profile: nothing to explain
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<h1 class="license-title">%s</h1>`, html.EscapeString(profile))
	fmt.Fprintf(&b, `<p class="license-lede">%s</p>`, html.EscapeString(TermsSummary(terms)))

	b.WriteString(`<h2>In plain language</h2>`)
	switch profile {
	case license.ProfileReserved:
		// The carve-out sentence. AIPREF's `search` category expressly permits
		// a search application to train models internally so long as those
		// models and their outputs never leave a link-back, non-summarising
		// search. Saying only "no AI training" while the emitted signal permits
		// that would misdescribe our own metadata.
		b.WriteString(`<p>Read it, quote it, link to it. Search engines may index it and send people here.</p>`)
		b.WriteString(`<p><strong>A search engine may build the models it needs to run a search that links back to you. ` +
			`Nobody may train a general model on your work, and nobody may summarise it into an answer that replaces the visit.</strong></p>`)
		b.WriteString(`<p>Credit is required wherever the work is used. For anything else, ask.</p>`)
	case license.ProfileOpen:
		b.WriteString(`<p>Anyone may use this work for anything, including training AI models and summarising it into answers. ` +
			`No permission needed and no conditions attached.</p>`)
	}

	b.WriteString(`<h2>The values this name stands for</h2>`)
	b.WriteString(`<table class="license-table"><thead><tr><th>Key</th><th>Value</th><th>Defined by</th></tr></thead><tbody>`)
	for _, row := range []struct{ key, val, src string }{
		{"train-ai", terms.TrainAI, "IETF AIPREF, draft-ietf-aipref-vocab-06 §4.1"},
		{"search", terms.Search, "IETF AIPREF, draft-ietf-aipref-vocab-06 §4.2"},
		{"ai-input", terms.AIInput, "RSL 1.0, usage vocabulary"},
		{"attribution", terms.Attribution, "RSL 1.0"},
	} {
		if row.val == "" {
			continue
		}
		fmt.Fprintf(&b, `<tr><td><code>%s</code></td><td><code>%s</code></td><td>%s</td></tr>`,
			html.EscapeString(row.key), html.EscapeString(row.val), html.EscapeString(row.src))
	}
	b.WriteString(`</tbody></table>`)

	b.WriteString(`<h2>What this page is, and is not</h2>`)
	b.WriteString(`<p>This is a <strong>deed</strong> — a plain-language description — not legal code. ` +
		`Every value above is drawn from a published standard and carries that standard's meaning; ` +
		`nothing here is language we wrote. Where a term needs legal force, it is the standard's instrument that supplies it.</p>`)
	b.WriteString(`<p>This page is a <strong>convenience</strong>. The profile name is a tag, not a link, and every work ` +
		`carries the expanded values alongside it — so if this page disappeared tomorrow, the terms would still say ` +
		`exactly what they say. That is deliberate: no author's terms should depend on someone else's server staying up.</p>`)
	b.WriteString(`<p class="license-nonretro"><strong>This version of this profile will not change.</strong> ` +
		`A work signed under it in 2026 means the same thing in 2036. A revised selection gets a new version number instead.</p>`)

	return r.writeLicenseHTML(filepath.Join(outDir, profileSlug(profile)+".html"), mountDir, profile, b.String())
}

// writeLicenseHTML wraps body content in a minimal page using the site's own
// stylesheets, so the terms pages look like the rest of the site without the
// licence type having to own a shape template.
func (r *PageRenderer) writeLicenseHTML(outPath, mountDir, title, body string) error {
	depth := strings.Count(strings.Trim(mountDir, "/"), "/") + 1
	up := strings.Repeat("../", depth)

	siteTitle := r.getSiteTitle()
	pageTitle := title
	if siteTitle != "" {
		pageTitle = title + " — " + siteTitle
	}

	page := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>` + html.EscapeString(pageTitle) + `</title>
    <link rel="stylesheet" href="` + up + `base.css">
    <link rel="stylesheet" href="` + up + `styles.css">
    <link rel="icon" type="image/svg+xml" href="` + up + `favicon.svg">
</head>
<body class="license-page">
    <div class="content-col">
        <header class="site-header">
            <a href="` + up + `index.html" class="site-identity-text">
                <div class="site-name">` + html.EscapeString(siteTitle) + `</div>
            </a>
        </header>
        <article class="license-body">
` + body + `
        </article>
    </div>
</body>
</html>
`
	return os.WriteFile(outPath, []byte(page), 0644)
}

// writeRobotsAndRSL writes the two file-based standards surfaces.
//
// These matter more than the response headers, not less: a self-hoster on S3 or
// GitHub Pages cannot set headers at all, so if a term lived only in
// `Content-Usage:` it would be invisible to exactly the people polis is for.
// Every term is expressed in at least one file.
//
// ⚠️ THE RENDERER DOES NOT DECIDE WHAT THESE FILES SAY. It only writes what
// site.LicenseProjections returns, because Medic restores the same two files
// and the two must agree byte for byte — a renderer with its own idea of the
// content is one half of an hourly write war. Anything that should change about
// robots.txt or rsl.xml changes there, once, for both writers.
func (r *PageRenderer) writeRobotsAndRSL() error {
	robots, rsl, err := site.LicenseProjections(r.config.DataDir, r.config.BaseURL)
	if err != nil {
		return fmt.Errorf("build licence projections: %w", err)
	}

	if len(robots) > 0 {
		if err := os.WriteFile(filepath.Join(r.config.DataDir, "robots.txt"), robots, 0644); err != nil {
			return fmt.Errorf("write robots.txt: %w", err)
		}
	}
	if len(rsl) > 0 {
		if err := os.WriteFile(filepath.Join(r.config.DataDir, "rsl.xml"), rsl, 0644); err != nil {
			return fmt.Errorf("write rsl.xml: %w", err)
		}
	}
	return nil
}
