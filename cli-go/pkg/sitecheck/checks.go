package sitecheck

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/did"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
)

// CheckStatus is the result of a single check. Patrol and Judge both alias this
// type, so its JSON shape is load-bearing: it lands in stored results and in
// fleet alerts. Do not add, rename or reorder fields casually.
type CheckStatus struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// PolicyWarning records a problem with a single policy rule. Aliased by Patrol;
// same JSON-shape caution applies.
type PolicyWarning struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Rule  string `json:"rule"`
	Error string `json:"error"`
}

// ---------- Key permissions ----------

// KeyPerms checks that the private key is not world- or group-readable.
// Only answerable where the key is present: a clone and a remote site have no
// .polis/ at all, and the caller must report that as not-applicable rather than
// letting "private key not found" read as a defect on a site that is fine.
func KeyPerms(siteDir string) CheckStatus {
	privKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519")
	info, err := os.Stat(privKeyPath)
	if os.IsNotExist(err) {
		return CheckStatus{OK: false, Message: "private key not found"}
	}
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("failed to stat: %v", err)}
	}
	mode := info.Mode().Perm()
	if mode != 0600 && mode != 0400 {
		return CheckStatus{OK: false, Message: fmt.Sprintf("unsafe permissions %04o (expected 0600 or 0400)", mode)}
	}
	return CheckStatus{OK: true}
}

// ---------- Bundle metadata ----------

// BundleJSON checks that the core bundle declaration exists, parses, and
// carries the two fields everything else keys off.
func BundleJSON(siteDir string) CheckStatus {
	return BundleJSONBytes(readOrNil(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")))
}

// BundleJSONBytes is BundleJSON over bytes already in hand — the same
// predicate, for a caller that fetched the file over HTTP rather than reading
// it off disk. A nil slice means absent.
func BundleJSONBytes(data []byte, readErr error) CheckStatus {
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return CheckStatus{OK: false, Message: "missing"}
		}
		return CheckStatus{OK: false, Message: fmt.Sprintf("failed to read: %v", readErr)}
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("invalid JSON: %v", err)}
	}
	if s, _ := raw["name"].(string); s == "" {
		return CheckStatus{OK: false, Message: "missing required field: name"}
	}
	if s, _ := raw["version"].(string); s == "" {
		return CheckStatus{OK: false, Message: "missing required field: version"}
	}
	return CheckStatus{OK: true}
}

func readOrNil(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// ---------- Index consistency ----------

// IndexEntryRule is how one index entry type is checked against the files it
// points at: where that type's files live, which files are that type's, and
// whether an entry's current_version is the version of a file's bytes.
type IndexEntryRule struct {
	// Dir is the site-relative directory holding the type's files. The core
	// bundle's default layout, as every other path in this package.
	Dir string
	// Suffix selects the type's files in Dir, for the phantom walk.
	Suffix string
	// VersionMatches reports whether version (with or without its "sha256:"
	// prefix) is the current_version of data. An error means data could not be
	// read as this type at all.
	VersionMatches func(data []byte, version string) (bool, error)
}

// ⛔ indexEntryRules IS THE RULE — data, not a branch (Signet epic 42 D2).
//
// A post's and a comment's current_version is the sha256 of the canonicalised
// markdown BODY; a tag's and an attestation's is the sha256 of the CANONICAL
// SIGNING JSON. Widening a type filter without the per-type hash reports every
// tag as a mismatch, and an `if entry.Type != "post"` is how the check came to
// report OK for three types it never looked at. So the per-type facts live in
// one declaration, the check reads it, and an entry type with no row is
// DISCLOSED as unchecked rather than skipped — TestEveryIndexContributorHasARule
// fails the build when index.Contributors grows a type this table lacks.
var indexEntryRules = map[string]IndexEntryRule{
	"post":        {"content/pub.polis.core/post", ".md", markdownBodyVersionMatches},
	"comment":     {"content/pub.polis.core/comment", ".md", markdownBodyVersionMatches},
	"tag":         {"content/pub.polis.core/tag", ".json", tagVersionMatches},
	"attestation": {"content/pub.polis.core/attestation", ".json", attestationVersionMatches},
}

// IndexEntryRuleFor returns the rule for an index entry type, if there is one.
func IndexEntryRuleFor(entryType string) (IndexEntryRule, bool) {
	rule, ok := indexEntryRules[entryType]
	return rule, ok
}

func markdownBodyVersionMatches(data []byte, version string) (bool, error) {
	_, body := SplitFrontmatterBody(string(data))
	return VerifyHash(body, strings.TrimPrefix(version, "sha256:")), nil
}

func tagVersionMatches(data []byte, version string) (bool, error) {
	var tf tag.TagFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return false, err
	}
	canonical, err := tag.CanonicalJSON(&tf)
	if err != nil {
		return false, err
	}
	return SHA256Hex(canonical) == strings.TrimPrefix(version, "sha256:"), nil
}

func attestationVersionMatches(data []byte, version string) (bool, error) {
	var r attestation.Record
	if err := json.Unmarshal(data, &r); err != nil {
		return false, err
	}
	canonical, err := attestation.CanonicalJSON(&r)
	if err != nil {
		return false, err
	}
	return SHA256Hex(canonical) == strings.TrimPrefix(version, "sha256:"), nil
}

// IndexConsistency verifies that index.jsonl entries match real files on disk:
// no orphans (indexed but missing), no phantoms (on disk but unindexed), and no
// mismatches between an entry's current_version and the file's bytes — for
// every entry type indexEntryRules declares.
//
// ⭐ A clean result NAMES what it examined (epic 20 D8): "I checked and it was
// fine", never "I did not check". Entries of a type with no rule are counted
// and named in the message, never silently skipped.
func IndexConsistency(siteDir string) CheckStatus {
	// ⭐ ReadPublicIndex, not LoadPublicIndex: a line that does not parse is a
	// finding here, never a silent skip — otherwise a half-garbage index reads
	// as clean over the half that parses (Signet epic 44 C1, close-out E2).
	entries, report, err := metadata.ReadPublicIndex(siteDir)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckStatus{OK: true, Message: "no index file"}
		}
		return CheckStatus{OK: false, Message: "cannot load index.jsonl: " + err.Error()}
	}

	var issues []string
	if report != nil && report.Skipped > 0 {
		issues = append(issues, malformedLinesIssue(report.SkippedLines))
	}

	if len(entries) == 0 && len(issues) == 0 {
		return CheckStatus{OK: true, Message: "empty index"}
	}

	indexedPaths := make(map[string]bool)
	checked := map[string]int{}
	unchecked := map[string]int{}

	for _, entry := range entries {
		rule, ok := indexEntryRules[entry.Type]
		if !ok {
			unchecked[entry.Type]++
			continue
		}
		checked[entry.Type]++
		indexedPaths[filepath.FromSlash(entry.Path)] = true

		// Check file exists
		absPath := filepath.Join(siteDir, entry.Path)
		data, err := os.ReadFile(absPath)
		if err != nil {
			if os.IsNotExist(err) {
				issues = append(issues, fmt.Sprintf("orphan: %s (indexed but missing)", entry.Path))
			} else {
				issues = append(issues, fmt.Sprintf("read error: %s: %s", entry.Path, err.Error()))
			}
			continue
		}

		// Check the version if the entry declares one
		if entry.CurrentVersion != "" {
			match, verr := rule.VersionMatches(data, entry.CurrentVersion)
			switch {
			case verr != nil:
				issues = append(issues, fmt.Sprintf("unreadable: %s is indexed as a %s and does not parse as one: %s", entry.Path, entry.Type, verr.Error()))
			case !match:
				issues = append(issues, fmt.Sprintf("hash_mismatch: %s", entry.Path))
			}
		}
	}

	// Check for phantom files (on disk but not in index), per type
	for _, entryType := range sortedKeys(indexEntryRules) {
		rule := indexEntryRules[entryType]
		dir := filepath.Join(siteDir, filepath.FromSlash(rule.Dir))
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && d.Name() == ".versions" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), rule.Suffix) {
				return nil
			}
			relPath, _ := filepath.Rel(siteDir, path)
			if !indexedPaths[relPath] {
				issues = append(issues, fmt.Sprintf("phantom: %s (on disk but not indexed)", relPath))
			}
			return nil
		})
	}

	if len(issues) > 0 {
		return CheckStatus{OK: false, Message: fmt.Sprintf("%d issues: %s", len(issues), strings.Join(issues, "; "))}
	}
	return CheckStatus{OK: true, Message: indexCensus(checked, unchecked)}
}

// malformedLinesIssue names the index lines that did not parse.
func malformedLinesIssue(lines []int) string {
	nums := make([]string, len(lines))
	for i, n := range lines {
		nums[i] = fmt.Sprint(n)
	}
	return fmt.Sprintf("malformed: %d line(s) of index.jsonl do not parse as an entry (lines %s)", len(lines), strings.Join(nums, ", "))
}

// indexCensus is the clean result's sentence: what was examined, by type, and
// what was not.
func indexCensus(checked, unchecked map[string]int) string {
	var parts []string
	total := 0
	for _, t := range sortedKeys(checked) {
		parts = append(parts, fmt.Sprintf("%s %d", t, checked[t]))
		total += checked[t]
	}
	noun := "entries"
	if total == 1 {
		noun = "entry"
	}
	msg := fmt.Sprintf("%d %s checked (%s)", total, noun, strings.Join(parts, ", "))
	if len(unchecked) > 0 {
		var not []string
		n := 0
		for _, t := range sortedKeys(unchecked) {
			not = append(not, fmt.Sprintf("%s %d", t, unchecked[t]))
			n += unchecked[t]
		}
		msg += fmt.Sprintf("; %d entries NOT checked — no rule for their type (%s)", n, strings.Join(not, ", "))
	}
	return msg
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---------- Policy files ----------

// PolicySyntax parses every rule in both policy files and reports the ones that
// do not. Includes the semantic pass (duplicates and contradictions).
//
// A missing policy file yields no warnings: a site that has declared no rules
// has nothing wrong with its rules.
func PolicySyntax(siteDir string) []PolicyWarning {
	var warnings []PolicyWarning
	for _, path := range policyFilePaths(siteDir) {
		relPath, _ := filepath.Rel(siteDir, path)
		warnings = append(warnings, validatePolicyFile(path, relPath)...)
	}
	warnings = append(warnings, policySemantics(siteDir)...)
	return warnings
}

func policyFilePaths(siteDir string) []string {
	return []string{
		filepath.Join(siteDir, "policies", "rules.jsonl"),
		filepath.Join(siteDir, ".polis", "policies", "rules.jsonl"),
	}
}

func validatePolicyFile(path, relPath string) []PolicyWarning {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return ParsePolicyLines(string(data), relPath)
}

// ParsePolicyLines checks every rule in a rules.jsonl body. Takes the BODY, not
// a path, so a remote validator checking a served policy file runs the same
// parser Patrol runs over the file on disk.
func ParsePolicyLines(body, relPath string) []PolicyWarning {
	var warnings []PolicyWarning
	scanner := bufio.NewScanner(strings.NewReader(body))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue // Not valid JSON, skip (IndexJSONL already validates)
		}
		// Skip metadata lines (version/generator headers)
		if _, hasVersion := raw["version"]; hasVersion {
			if _, hasPolicy := raw["policy"]; !hasPolicy {
				continue
			}
		}
		rule, ok := raw["policy"].(string)
		if !ok {
			continue
		}
		if _, err := policy.Parse(rule); err != nil {
			warnings = append(warnings, PolicyWarning{
				File:  relPath,
				Line:  lineNum,
				Rule:  rule,
				Error: err.Error(),
			})
		}
	}
	return warnings
}

// policySemantics detects duplicate rules and contradictions.
func policySemantics(siteDir string) []PolicyWarning {
	var warnings []PolicyWarning

	type ruleEntry struct {
		rule string
		file string
		line int
	}
	seen := make(map[string]ruleEntry)  // exact dedup
	byKey := make(map[string]ruleEntry) // type+source → first action for contradiction detection

	for _, path := range policyFilePaths(siteDir) {
		relPath, _ := filepath.Rel(siteDir, path)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var raw map[string]interface{}
			if err := json.Unmarshal([]byte(line), &raw); err != nil {
				continue
			}
			rule, ok := raw["policy"].(string)
			if !ok {
				continue
			}
			entry := ruleEntry{rule: rule, file: relPath, line: lineNum}

			// Check for exact duplicates
			if prev, exists := seen[rule]; exists {
				warnings = append(warnings, PolicyWarning{
					File:  relPath,
					Line:  lineNum,
					Rule:  rule,
					Error: fmt.Sprintf("duplicate of %s:%d", prev.file, prev.line),
				})
			} else {
				seen[rule] = entry
			}

			// Check for contradictions (same type+source, different allow/deny)
			parsed, err := policy.Parse(rule)
			if err != nil {
				continue
			}
			k := parsed.Type + "|" + parsed.Source
			if prev, exists := byKey[k]; exists {
				prevParsed, _ := policy.Parse(prev.rule)
				if prevParsed != nil && isContradiction(parsed.Action, prevParsed.Action) {
					warnings = append(warnings, PolicyWarning{
						File:  relPath,
						Line:  lineNum,
						Rule:  rule,
						Error: fmt.Sprintf("contradicts %s at %s:%d", prev.rule, prev.file, prev.line),
					})
				}
			} else {
				byKey[k] = entry
			}
		}
		_ = f.Close()
	}
	return warnings
}

func isContradiction(a, b string) bool {
	return (a == "allow" && b == "deny") || (a == "deny" && b == "allow")
}

// ---------- Licence integrity ----------

// LicenseUncheckable prefixes the Message of a LicenseIntegrity result that
// could not be checked at all — today, a licence signed over a field set this
// build does not declare (SIGNET epic 47).
//
// ⚠️ It is a sentinel rather than a third CheckStatus state because CheckStatus
// is a published JSON shape that Patrol and Medic both read; widening it for
// one check would be a bigger change than the check. The pair of string
// constants is matched at exactly two places — here and the report in local.go
// — and "no terms stated" already works the same way.
const LicenseUncheckable = "licence could not be checked: "

// LicenseIntegrity verifies that a site's stated terms are intact: the pointer
// resolves, the document parses, its terms are valid, and the signature is the
// site's own.
//
// The licence is the one artifact whose VALUE is that nobody can quietly change
// it. A host that rewrote `train-ai: n` to `y`, or swapped the pointer to a
// permissive document, would be granting rights the author never granted, in
// her name. This is the thing that looks.
//
// ⚠️ It deliberately does NOT report a site with no terms. Absent means
// unstated, most sites have said nothing, and that is not a defect.
//
// pubKey is passed in rather than read here because the right key differs by
// caller: Patrol uses the local key FILE, while a validator checking a site it
// does not own uses the PUBLISHED key — the one a third party would fetch. An
// empty pubKey yields the "cannot read public key" message, in the same
// position the file read used to occupy.
func LicenseIntegrity(siteDir string, pubKey []byte) CheckStatus {
	return LicenseIntegrityWithHistory(siteDir, pubKey, nil)
}

// LicenseIntegrityWithHistory is LicenseIntegrity for a site that may have
// rotated: the current key first, then the retired key the site's published
// history resolves for the licence's claimed signing time, `updated` (SIGNET
// epic 44 E5). chain must come from ResolvingChain; nil is LicenseIntegrity.
//
// ⚠️ pkg/license cannot import pkg/site (site imports license), so the history is
// consulted here, around license.Verify, rather than inside the package.
func LicenseIntegrityWithHistory(siteDir string, pubKey []byte, chain *site.KeyHistoryBlock) CheckStatus {
	pointer := site.LicensePointer(siteDir)
	if pointer == "" {
		// No pointer, no claim. Silence is a defined state.
		return CheckStatus{OK: true, Message: "no terms stated"}
	}

	path := filepath.Join(siteDir, filepath.FromSlash(strings.TrimLeft(pointer, "/")))
	if _, err := os.Stat(path); err != nil {
		// A dangling pointer is NOT benign: the site advertises terms and
		// serves nothing, so a consumer sees "unstated" for an author who
		// stated something. That is a lost claim of intent.
		return CheckStatus{OK: false, Message: fmt.Sprintf("licence pointer %s resolves to nothing", pointer)}
	}

	f, err := license.Load(path)
	if err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("licence at %s is unreadable: %v", pointer, err)}
	}
	if f.Terms == nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("licence at %s carries no terms", pointer)}
	}
	if err := f.Terms.Validate(); err != nil {
		return CheckStatus{OK: false, Message: fmt.Sprintf("licence at %s is invalid: %v", pointer, err)}
	}

	if len(pubKey) == 0 {
		return CheckStatus{OK: false, Message: "cannot read public key to verify licence"}
	}
	ok, retired, err := site.VerifyWithHistory(func(key []byte) (bool, error) {
		return license.Verify(f, key)
	}, pubKey, chain, f.Updated, "updated")

	// SIGNET epic 47 — the tolerance rule, through the package's own decision
	// point. The walk has to live out here (pkg/license cannot import pkg/site)
	// but the RULE must not: license.Status is the one place this type maps an
	// outcome onto a status.
	switch status, why := license.Status(f, ok); status {
	case license.StatusUnknown:
		// ⚠️ `OK: true` because having failed to look is not the same as having
		// looked and found a problem — the posture Judge takes on items 9 and
		// 10, and what keeps Patrol from alerting on it.
		//
		// ⛔ BUT OK IS NOT "FINE", AND A REPORT MUST NOT RENDER IT AS A PASS.
		// CheckStatus has two states and the third answer has nowhere else to
		// live, so the message carries it: `polis validate` matches this prefix
		// and files the check as NOT-APPLICABLE, which is what the remote form
		// already does. Without that, local and remote disagree about the same
		// site and the parity guard is right to complain.
		return CheckStatus{OK: true, Message: LicenseUncheckable + why}
	case license.StatusInvalid:
		if chain != nil && len(chain.History) > 0 {
			// The history was consulted; its reason says what was tried.
			return CheckStatus{OK: false, Message: "licence signature does not verify — terms may have been altered (" + errText(err) + ")"}
		}
		if err != nil {
			return CheckStatus{OK: false, Message: fmt.Sprintf("licence signature check failed: %v", err)}
		}
		// The headline case. Either the terms were edited without re-signing,
		// or they were signed by a different key.
		return CheckStatus{OK: false, Message: "licence signature does not verify — terms may have been altered"}
	}
	if note := signing.UncoveredNote(f.UnrecognisedFields()); note != "" {
		if retired != nil {
			return CheckStatus{OK: true, Message: "licence " + KeyUsedFor(chain, retired, f.Updated).Describe() + " — " + note}
		}
		return CheckStatus{OK: true, Message: "the site's stated terms are intact — " + note}
	}
	if retired != nil {
		return CheckStatus{OK: true, Message: "licence " + KeyUsedFor(chain, retired, f.Updated).Describe()}
	}
	return CheckStatus{OK: true}
}

// PublishedKey returns the identity key a site publishes in .well-known/polis —
// the key a third party on the network would fetch and use.
//
// Every site-level verifier in this codebase (follow file, blessing list,
// attestations) checks against the PUBLISHED key rather than .polis/keys, so
// they answer the question the network asks instead of a locally convenient
// approximation. This is that lookup, once.
func PublishedKey(siteDir string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis"))
	if err != nil {
		return nil, fmt.Errorf("no .well-known/polis to verify against")
	}
	var wk struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal(data, &wk); err != nil {
		return nil, fmt.Errorf("unparseable .well-known/polis")
	}
	if wk.PublicKey == "" {
		return nil, fmt.Errorf("no public_key in .well-known/polis")
	}
	return []byte(wk.PublicKey), nil
}

// jwkX projects an OpenSSH Ed25519 public key into the base64url `x` value an
// RFC 8037 JWK carries, which is how a DID document publishes it.
//
// Both projections come from pkg/did and pkg/signing rather than being
// recomputed here: the DID document's encoding is the did package's business,
// and a second expression of it is a second thing to keep in step.
func jwkX(publicKeySSH []byte) (string, error) {
	pub, err := signing.ParsePublicKey(publicKeySSH)
	if err != nil {
		return "", err
	}
	jwk, err := did.PublicKeyJWK(pub)
	if err != nil {
		return "", err
	}
	return jwk.X, nil
}
