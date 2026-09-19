package render

import (
	"fmt"
	"html"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
)

// The three human-readable surfaces, and why there are three rather than one.
// Each answers a different question, and they change on different clocks:
//
//	per-post notice   "what may I do with THIS?"        frozen at publish
//	profile page      "what does `reserved` MEAN?"      never, per version
//	site terms page   "what are her terms NOW?"         whenever she edits
//
// Collapsing them loses the distinction that makes non-retroactivity visible
// instead of a caveat buried in a spec.

// licenseNoticeHTML renders the per-post terms line — the surface that answers
// "what may I do with this?" for one specific work.
//
// It describes the terms THAT POST was signed with, read back out of its own
// frontmatter, so it is frozen the moment the post is published. It never
// reflects the author's current licence, and that is the point.
//
// Returns "" when the work states no terms, so nothing is rendered rather than
// a notice announcing an absence.
func licenseNoticeHTML(t *license.Terms) string {
	if t == nil {
		return ""
	}
	summary := TermsSummary(t)
	if summary == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString(`<div class="entry-license" data-polis-license="` + html.EscapeString(t.Profile) + `">`)
	b.WriteString(`<span class="entry-license-summary">` + html.EscapeString(summary) + `</span>`)
	if t.Terms != "" {
		fmt.Fprintf(&b, ` <a class="entry-license-link" rel="license" href="%s">Terms</a>`, html.EscapeString(t.Terms))
	}
	if t.Asserted != "" {
		// The date is what makes "what were the terms in March?" answerable —
		// which is exactly what non-retroactivity requires a reader to be able
		// to ask.
		fmt.Fprintf(&b, ` <span class="entry-license-asserted">as of %s</span>`, html.EscapeString(shortDate(t.Asserted)))
	}
	b.WriteString(`</div>`)
	return b.String()
}

// TermsSummary renders terms as one plain sentence a person can read.
//
// ⚠️ It must describe what was ACTUALLY granted, including the parts that are
// easy to leave out. AIPREF's `search` category expressly permits a search
// application to train models internally, provided those models and their
// outputs never leave a link-back, non-summarising search (vocab-06 §4.2). That
// is not a loophole — an index IS a model, and refusing search-internal
// training is refusing search — but a summary that says only "no AI training"
// while the emitted signal permits it would misdescribe its own metadata. That
// is the Creative Commons trap this design exists to avoid: a plain reading of
// CC BY permits training, and a great many people licensed their archives in
// 2015 without knowing it.
func TermsSummary(t *license.Terms) string {
	if t == nil {
		return ""
	}

	var sentences []string

	// Sentence 1 — the training axis, and the carve-out that comes with
	// allowing search. These belong together: stating the denial without the
	// exception is the misdescription.
	switch {
	case t.TrainAI == license.Disallow && t.Search == license.Allow:
		sentences = append(sentences,
			"No AI training on this work, except the models a search engine needs "+
				"to index it and link readers back")
	case t.TrainAI == license.Disallow:
		sentences = append(sentences, "No AI training on this work")
	case t.TrainAI == license.Allow:
		sentences = append(sentences, "AI training on this work is permitted")
	case t.Search == license.Allow:
		sentences = append(sentences, "Search engines may index this work and link readers back")
	}

	if t.TrainAI != "" && t.Search == license.Disallow {
		sentences = append(sentences, "Search indexing is not permitted")
	}

	// Sentence 2 — the axis people actually feel: being summarised instead of
	// visited.
	switch t.AIInput {
	case license.Disallow:
		sentences = append(sentences, "Nobody may summarise it into an answer that replaces the visit")
	case license.Allow:
		sentences = append(sentences, "Summarising it into an answer is permitted")
	}

	if t.Attribution == license.AttributionRequired {
		sentences = append(sentences, "Credit is required wherever it is used")
	}

	if len(sentences) == 0 {
		return ""
	}
	return strings.Join(sentences, ". ") + "."
}

// shortDate trims an RFC3339 timestamp to its date.
func shortDate(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ts
}
