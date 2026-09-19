package sitecheck

// Does the PUBLIC surface still say what the author said?
//
// ⛔ THE HOLE THIS FILLS. Judge asks whether what a site SERVES matches what it
// WROTE, and it asks from the LOOPBACK — upstream of any CDN, edge or proxy
// (Judge's served-artifact check). So an intermediary that adds directives to robots.txt
// on the public wire does not exist from where Judge stands: it reports OK,
// correctly and forever, while the file the world reads says something else.
// That is not Judge's bug and giving Judge a public fetch would not be a fix.
//
// This asks the same question from OUTSIDE, in `polis validate <url>` — the one
// thing in the system that sees what the world sees. ⚠️ It therefore runs when
// the tenant runs it, not hourly over the fleet. The edge is not ours to police
// continuously, and a per-run answer the author can act on beats an hourly
// alert nobody can.
//
// ⛔ IT REPAIRS NOTHING and it never tells the author what her terms should be.
// robots.txt is very often managed by a host the tenant does not control.
//
// # The three properties, and the middle one is not presence
//
//  1. PRESENCE — the site's own generated section is in the served file,
//     intact. Its boundary is self-identifying: license.RobotsHeader.
//
//  2. PRECEDENCE — ⛔ NOT a substring test. Under RFC 9309 §2.2.1 a crawler
//     obeys the group whose `User-agent` match is MOST SPECIFIC, and groups
//     naming the same value are merged into one. So a third party adding
//     `User-agent: GPTBot` / `Disallow: /` does not sit alongside polis's
//     `User-agent: *` group — it REPLACES it for that agent. Our directives are
//     present in the file and never consulted. ⭐ Containment proves nothing;
//     the question is which group wins per agent.
//
//  3. DIRECTION — and it is ASYMMETRIC. A third party TIGHTENING is at worst an
//     annoyance; a third party LOOSENING is somebody answering a licence
//     question on the author's behalf. Which of those it is depends on what the
//     author SIGNED, so the comparison reads license.json and never
//     pattern-matches the file:
//
//     signed refuses (reserved) + edge restricts  → aligned, a note
//     signed refuses (reserved) + edge permits    → ⛔ the attack, a warning
//     signed permits (open)     + edge restricts  → ⚠️ contradiction, a warning
//     signed permits (open)     + edge permits    → aligned, a note
//
// Everything here is a warning at most. See OutcomeWarning.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// direction is which way a third party moved the terms relative to what the
// author signed. It is the ONE new noun this check introduces, and there is
// deliberately no second axis: everything else here is reported as prose.
type direction string

const (
	moveNone direction = ""
	// moveRestrictive — the served file denies an agent something the signed
	// licence permits.
	moveRestrictive direction = "restrictive"
	// movePermissive — the served file leaves an agent free to do something the
	// signed licence refuses. Includes SILENCE: a group that governs an agent
	// and carries no content-usage rule has erased the author's refusal for
	// that agent, and under AIPREF an absent preference is "unknown", which is
	// latitude to act.
	movePermissive direction = "permissive"
)

// robotsGroup is one RFC 9309 group: the user-agent values it names and the
// rules governing them, after merging.
type robotsGroup struct {
	agents []string
	// label is the nearest comment line at or before the group, carried
	// forward. An injector that brackets its block with a comment — and they do
	// — becomes attributable without anyone maintaining a table of vendors.
	// ⛔ This is NOT edge-specific parsing: we quote whatever comment is there,
	// or we say nothing.
	label string

	allowRoot    bool
	disallowRoot bool
	// usage is the group's site-wide (path-less) `Content-Usage` value.
	usage    string
	hasUsage bool
}

// deniesRoot reports whether the group forbids crawling `/`.
//
// RFC 9309 §2.2.2: the most specific (longest) path match wins, and where an
// Allow and a Disallow are equally specific the LEAST restrictive rule applies.
// So a group carrying both `Allow: /` and `Disallow: /` allows.
func (g *robotsGroup) deniesRoot() bool { return g.disallowRoot && !g.allowRoot }

// parseRobots reads a robots.txt into its groups.
//
// ⚠️ Group boundaries, not line order, are what matters: consecutive
// `User-agent:` lines name ONE group (§2.2.1), and the first non-user-agent
// rule ends the header. Directives before any group (a bare `Sitemap:`) belong
// to no group and are skipped — they are not rules.
func parseRobots(body string) []robotsGroup {
	var groups []robotsGroup
	var cur *robotsGroup
	inHeader := false
	label := ""

	for _, raw := range strings.Split(body, "\n") {
		line := raw
		if i := strings.Index(line, "#"); i >= 0 {
			if c := strings.TrimSpace(strings.TrimLeft(line[i:], "# ")); c != "" && strings.TrimSpace(line[:i]) == "" {
				label = c
			}
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		if key == "user-agent" {
			if cur == nil || !inHeader {
				groups = append(groups, robotsGroup{label: label})
				cur = &groups[len(groups)-1]
				inHeader = true
			}
			cur.agents = append(cur.agents, value)
			continue
		}
		inHeader = false
		if cur == nil {
			continue
		}
		switch key {
		case "allow":
			if value == "/" || value == "" {
				cur.allowRoot = true
			}
		case "disallow":
			if value == "/" {
				cur.disallowRoot = true
			}
		case "content-usage":
			// attach-04 §3's path pattern is optional; a value starting with
			// "/" is scoped to a path and is not the group's site-wide
			// preference.
			if strings.HasPrefix(value, "/") {
				continue
			}
			cur.usage, cur.hasUsage = value, true
		}
	}
	return groups
}

// mergeRobots collapses groups per RFC 9309 §2.2.1 — "groups with the same
// user-agent value are merged" — and returns them keyed by lowercased agent.
//
// ⭐ This merge is why the live case is subtle: an edge that prepends its own
// `User-agent: *` group does not shadow polis's, it JOINS it. What shadows
// polis is the NAMED groups it adds alongside.
func mergeRobots(groups []robotsGroup) map[string]*robotsGroup {
	merged := map[string]*robotsGroup{}
	for i := range groups {
		for _, a := range groups[i].agents {
			k := strings.ToLower(a)
			m, ok := merged[k]
			if !ok {
				m = &robotsGroup{agents: []string{a}, label: groups[i].label}
				merged[k] = m
			}
			m.allowRoot = m.allowRoot || groups[i].allowRoot
			m.disallowRoot = m.disallowRoot || groups[i].disallowRoot
			if groups[i].hasUsage {
				m.usage, m.hasUsage = groups[i].usage, true
			}
		}
	}
	return merged
}

// usageValues parses a `Content-Usage` value — an RFC 8941 dictionary of
// category=token pairs — into a map.
func usageValues(v string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(v, ",") {
		k, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(val)
	}
	return out
}

// authorDirection is which way the AUTHOR leans, from the signed licence.
// Refusing anything makes her stance restrictive; granting with no refusal
// makes it permissive; stating nothing makes there be no stance to compare
// against.
func authorDirection(t *license.Terms) direction {
	stated := false
	for _, v := range []string{t.TrainAI, t.Search, t.AIInput} {
		switch v {
		case license.Disallow:
			return moveRestrictive
		case license.Allow:
			stated = true
		}
	}
	if stated {
		return movePermissive
	}
	return moveNone
}

// move is one way a group moves the terms, and why.
type move struct {
	dir  direction
	what string
}

// groupMoves reports how one governing group moves the signed terms for the
// agents it names.
//
// ⭐ ACCESS DOMINATES. A group that forbids crawling `/` cannot be permissive
// about anything: the agent may not fetch the bytes, so a missing content-usage
// rule costs the author nothing. Getting this backwards is what would turn the
// live, benign Cloudflare block into a fleet of false warnings.
func groupMoves(g *robotsGroup, t *license.Terms) []move {
	var moves []move

	if g.deniesRoot() {
		return []move{{moveRestrictive, "forbids it from crawling the site at all, where this site's own robots.txt allows every agent"}}
	}

	eff := map[string]string{}
	if g.hasUsage {
		eff = usageValues(g.usage)
	}
	for _, k := range []struct{ key, signed string }{
		{"train-ai", t.TrainAI},
		{"search", t.Search},
	} {
		if k.signed == "" {
			continue
		}
		got, present := eff[k.key]
		switch {
		case present && got == k.signed:
			// aligned; nothing to say
		case present && got != k.signed && k.signed == license.Disallow:
			moves = append(moves, move{movePermissive, fmt.Sprintf("states %s=%s, where the signed licence refuses it (%s=%s)", k.key, got, k.key, k.signed)})
		case present && got != k.signed:
			moves = append(moves, move{moveRestrictive, fmt.Sprintf("states %s=%s, where the signed licence grants it (%s=%s)", k.key, got, k.key, k.signed)})
		case !present && k.signed == license.Disallow:
			moves = append(moves, move{movePermissive, fmt.Sprintf("carries no %s rule, so the signed licence's refusal of %s never reaches it — an absent AIPREF preference is \"unknown\", which is latitude", k.key, k.key)})
		}
	}
	return moves
}

// checkRemotePublicTerms is the whole epic: fetch the PUBLIC licence surfaces
// and ask whether they still say what the author signed.
//
// terms is the licence already fetched and SIGNATURE-VERIFIED by
// checkRemoteLicense. Nil means there is nothing to compare against, and the
// checks say so rather than passing.
func (rr *Remote) checkRemotePublicTerms(r *Report, base string, terms *license.Terms) {
	if terms == nil {
		// Covers both shapes of nil: the site stated no terms at all, and the
		// terms it states did not verify. Either way there is no signed
		// original, and comparing the public file to itself would be theatre.
		const why = "this site states no terms, or the terms it states could not be read or did not verify — either way there is no signed original to compare the public surface against"
		r.na("content.license_robots", FamilyContent, why)
		r.na("content.license_rsl", FamilyContent, why)
		return
	}
	rr.checkPublicRobots(r, base, terms)
	rr.checkPublicRSL(r, base, terms)
}

func (rr *Remote) checkPublicRobots(r *Report, base string, terms *license.Terms) {
	body, err := rr.client.FetchContent(base + "/robots.txt")
	if err != nil {
		// D8. A fetch that failed is NOT CHECKED, never fine — this is the one
		// check in the suite that reads the public wire, and a silent skip
		// would leave the whole hole it exists to close looking closed.
		r.na("content.license_robots", FamilyContent, fmt.Sprintf("the public robots.txt could not be fetched (%v) — what the world reads was NOT checked", err))
		return
	}

	var findings []string
	worst := moveNone
	author := authorDirection(terms)

	// 1 · Presence and intactness of the site's own section.
	idx := strings.Index(body, license.RobotsHeader)
	if idx < 0 {
		worst = flip(author)
		findings = append(findings, "polis's own generated section is NOT in the served file — the signed terms reach nobody, whatever else the file says")
	} else {
		findings = append(findings, sectionFindings(body[idx+len(license.RobotsHeader):], terms)...)
		if len(findings) > 0 {
			worst = flip(author)
		}
	}

	// 2 · Precedence, and 3 · direction. Every agent the file names other than
	// `*` gets its own group, and that group REPLACES polis's for it.
	merged := mergeRobots(parseRobots(body))
	agents := make([]string, 0, len(merged))
	for k := range merged {
		if k != "*" {
			agents = append(agents, k)
		}
	}
	sort.Strings(agents)

	shadowed := 0
	for _, a := range agents {
		g := merged[a]
		moves := groupMoves(g, terms)
		if len(moves) == 0 {
			continue
		}
		shadowed++
		for _, m := range moves {
			if m.dir != author {
				worst = m.dir
			}
			findings = append(findings, fmt.Sprintf("%s %s: a group naming %s wins over polis's `User-agent: *` group (RFC 9309 §2.2.1) and %s%s",
				severityWord(m.dir, author), m.dir, g.agents[0], m.what, attribution(g.label)))
		}
	}

	// The wildcard group is MERGED with ours rather than replacing it, so it is
	// only a finding when a third party put a conflicting site-wide preference
	// into it.
	if w, ok := merged["*"]; ok && w.hasUsage && w.usage != license.ContentUsage(terms) {
		for _, m := range groupMoves(w, terms) {
			if m.dir != author {
				worst = m.dir
			}
			findings = append(findings, fmt.Sprintf("%s %s: the merged `User-agent: *` group %s%s", severityWord(m.dir, author), m.dir, m.what, attribution(w.label)))
		}
	}

	detail := fmt.Sprintf("public robots.txt: %d byte(s), %d group(s), %d agent(s) governed by a group other than polis's",
		len(body), len(merged), shadowed)

	switch {
	case len(findings) == 0:
		r.pass("content.license_robots", FamilyContent, detail+" — nothing outside polis's own section", 1)
	case worst == moveNone:
		// ⭐ Everything found moves the terms the way the author already did.
		// It is a NOTE — recorded in full, and explicitly not a problem — which
		// is the whole reason this reports two severities rather than one.
		r.add(Check{
			ID: "content.license_robots", Family: FamilyContent, Outcome: OutcomePassed, Examined: 1,
			Detail:   detail + " — every third-party directive moves the terms the way the signed licence already does. Aligned, and NOT a problem to fix.",
			Findings: findings,
		})
	default:
		r.warn("content.license_robots", FamilyContent,
			detail+" — a third party moved the terms AWAY from what the author signed. polis cannot change what an intermediary serves; this is a report, not a defect in your site.",
			1, findings...)
	}
}

// flip returns the direction that OPPOSES the author's, so an absent or altered
// section is weighed the same way an injected directive is: against the stance
// the author actually took.
func flip(author direction) direction {
	if author == moveRestrictive {
		return movePermissive
	}
	return moveRestrictive
}

// severityWord names, in the finding itself, which half of the asymmetry this
// is. A reader must not have to re-derive it from the table.
func severityWord(m, author direction) string {
	if m == author {
		return "aligned —"
	}
	if m == movePermissive {
		return "GRANTS WHAT THE AUTHOR REFUSED —"
	}
	return "CONTRADICTS THE AUTHOR'S GRANT —"
}

// attribution quotes the nearest preceding comment, which is how an injecting
// intermediary that labels its block names itself. Empty when it did not.
func attribution(label string) string {
	if label == "" {
		return " (origin undeterminable — the block carries no comment naming it)"
	}
	return fmt.Sprintf(" (from a block labelled %q)", label)
}

// sectionFindings checks that polis's own section, from just after its header
// to the end of the file, is what the signed licence projects.
//
// ⚠️ WHY NOT A WHOLE-FILE BYTE COMPARISON. One input to license.Robots is
// unknowable from outside: the per-path overrides are materialised by walking
// works on disk, which HTTP cannot enumerate. So the site-wide directives — the
// ones that carry the author's stated terms — are compared exactly, path-scoped
// rules are accepted as ours, and anything else in the section is a finding.
func sectionFindings(section string, terms *license.Terms) []string {
	var findings []string
	wantUsage := license.ContentUsage(terms)
	sawUsage := false
	groups := 0

	for _, raw := range strings.Split(section, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			findings = append(findings, fmt.Sprintf("polis's own section carries a line it did not write: %q", line))
			continue
		}
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		switch key {
		case "user-agent":
			groups++
			if groups > 1 || value != "*" {
				findings = append(findings, fmt.Sprintf("a `User-agent: %s` group was appended INSIDE polis's own section — polis writes exactly one, for `*`", value))
			}
		case "allow":
			if value != "/" {
				findings = append(findings, fmt.Sprintf("polis's section says `Allow: %s`; it writes `Allow: /`", value))
			}
		case "content-usage":
			if strings.HasPrefix(value, "/") {
				continue // a per-work override rule; not enumerable from outside
			}
			sawUsage = true
			if value != wantUsage {
				findings = append(findings, fmt.Sprintf("the served site-wide preference is %q; the signed licence says %q", value, wantUsage))
			}
		case "license", "sitemap":
			// URLs; checked for reachability elsewhere, not for their text.
		default:
			findings = append(findings, fmt.Sprintf("polis's own section carries a directive it did not write: %q", line))
		}
	}
	if wantUsage != "" && !sawUsage {
		findings = append(findings, fmt.Sprintf("polis's section carries NO site-wide Content-Usage rule; the signed licence says %q", wantUsage))
	}
	return findings
}

// checkPublicRSL asks the same question of rsl.xml, which is where every term
// AIPREF cannot express lives — attribution, ai-input.
//
// Unlike robots.txt this one IS fully recomputable from the signed licence, so
// the comparison is the bytes themselves, against the same projection function
// the renderer and Medic both use.
func (rr *Remote) checkPublicRSL(r *Report, base string, terms *license.Terms) {
	want, err := license.RSL(terms, strings.TrimSuffix(site.LicenseProjectionBase(terms, base), "/")+"/")
	if err != nil || want == "" {
		r.na("content.license_rsl", FamilyContent, "these terms project no rsl.xml, so there is nothing to compare")
		return
	}
	body, ferr := rr.client.FetchContent(base + "/rsl.xml")
	if ferr != nil {
		r.na("content.license_rsl", FamilyContent, fmt.Sprintf("the public rsl.xml could not be fetched (%v) — the licence terms AIPREF cannot express were NOT checked", ferr))
		return
	}
	if strings.TrimSpace(body) == strings.TrimSpace(want) {
		r.pass("content.license_rsl", FamilyContent, "public rsl.xml is exactly what this site's signed licence projects", 1)
		return
	}
	r.warn("content.license_rsl", FamilyContent,
		"public rsl.xml is NOT what this site's signed licence projects — the terms AIPREF cannot express (attribution, ai-input) may be reaching the world altered", 1,
		"served:   "+strings.Join(strings.Fields(body), " "),
		"expected: "+strings.Join(strings.Fields(want), " "))
}
