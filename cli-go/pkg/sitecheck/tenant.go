package sitecheck

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// ---------- Tenant-state predicates shared by Patrol and Tailor ----------
//
// Patrol (hosted, report-only) and Tailor (self-hosted, repairs) ask the same
// questions of a site. Each predicate below is the one answer to one of them.
//
// ⛔ THEY RETURN FINDINGS, NEVER A MESSAGE. The two actors render differently —
// Patrol stops at the first finding, Tailor accumulates; their wording differs —
// and Medic heals by string-matching Patrol's wording, so a shared message would
// either change what Patrol says or change what Tailor says. Dedup the predicate,
// never the rendering. Where both actors already render a finding identically
// (BundlePathProblem), the one rendering lives here.

// ---------- Bundle declarations ----------

// MissingDeclarations returns the default core bundle's types, shapes and themes
// that b does not declare, as "type:<name>", "shape:<name>" and "theme:<name>":
// types first, then shapes, then themes, each in map order. A caller that needs a
// stable order sorts.
func MissingDeclarations(b *bundle.Bundle) []string {
	defaults := bundle.DefaultCoreBundle()
	var missing []string
	for name := range defaults.Types {
		if _, ok := b.Types[name]; !ok {
			missing = append(missing, "type:"+name)
		}
	}
	for name := range defaults.Shapes {
		if _, ok := b.Shapes[name]; !ok {
			missing = append(missing, "shape:"+name)
		}
	}
	for name := range defaults.Themes {
		if _, ok := b.Themes[name]; !ok {
			missing = append(missing, "theme:"+name)
		}
	}
	return missing
}

// TypeDrift is one field of one declared type that differs from the default.
type TypeDrift struct {
	Type  string
	Field string // "dir", "mount", "storage.pattern" or "emits"

	// Got and Want are the values for dir, mount and storage.pattern.
	Got, Want string

	// For emits: the type's declared list, the default list, and the defaults
	// the declared list lacks (in the default's order). A site may add emits;
	// it may not drop one.
	GotEmits, WantEmits, MissingEmits []string
}

// TypeFieldDrift compares each default type that b declares against the default
// declaration: Dir, Mount, Storage.Pattern, and Emits (which must be a superset).
// Types are visited in map order and, within a type, fields in that order.
func TypeFieldDrift(b *bundle.Bundle) []TypeDrift {
	var drift []TypeDrift
	for name, want := range bundle.DefaultCoreBundle().Types {
		got, ok := b.Types[name]
		if !ok {
			continue // a missing declaration, not drift
		}
		if got.Dir != want.Dir {
			drift = append(drift, TypeDrift{Type: name, Field: "dir", Got: got.Dir, Want: want.Dir})
		}
		if got.Mount != want.Mount {
			drift = append(drift, TypeDrift{Type: name, Field: "mount", Got: got.Mount, Want: want.Mount})
		}
		if want.Storage != nil && (got.Storage == nil || got.Storage.Pattern != want.Storage.Pattern) {
			gotPattern := ""
			if got.Storage != nil {
				gotPattern = got.Storage.Pattern
			}
			drift = append(drift, TypeDrift{Type: name, Field: "storage.pattern", Got: gotPattern, Want: want.Storage.Pattern})
		}
		have := make(map[string]bool, len(got.Emits))
		for _, e := range got.Emits {
			have[e] = true
		}
		var lacking []string
		for _, e := range want.Emits {
			if !have[e] {
				lacking = append(lacking, e)
			}
		}
		if len(lacking) > 0 {
			drift = append(drift, TypeDrift{Type: name, Field: "emits", GotEmits: got.Emits, WantEmits: want.Emits, MissingEmits: lacking})
		}
	}
	return drift
}

// ---------- blessed.json / following.json structure ----------

// StructureKind names what is wrong with a list file's structure.
type StructureKind int

const (
	StructInvalidJSON     StructureKind = iota // the file is not JSON (Err)
	StructMissingList                          // the top-level list key is absent (Field)
	StructListNotArray                         // the top-level list is not an array (Field)
	StructItemNotObject                        // list[Index] is not an object
	StructItemMissing                          // list[Index] lacks a non-empty string Field
	StructEntriesNotArray                      // blessed only: comments[Index].blessed is not an array
	StructEntryNotObject                       // blessed only: comments[Index].blessed[Entry] is not an object
	StructEntryMissing                         // blessed only: comments[Index].blessed[Entry] lacks Field
)

// StructureProblem is one structural problem, in the order the file is read.
type StructureProblem struct {
	Kind  StructureKind
	Field string
	Index int
	Entry int
	Err   error
}

// BlessedStructure checks blessed.json against the on-disk schema: a top-level
// "comments" array of per-post groups, each with a non-empty "post" and a
// "blessed" array of entries carrying non-empty "url" and "blessed_at". Every
// problem is returned, in reading order; the first is the one a stop-at-first
// reader reports.
func BlessedStructure(data []byte) []StructureProblem {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return []StructureProblem{{Kind: StructInvalidJSON, Err: err}}
	}
	listRaw, ok := raw["comments"]
	if !ok {
		return []StructureProblem{{Kind: StructMissingList, Field: "comments"}}
	}
	arr, ok := listRaw.([]interface{})
	if !ok {
		return []StructureProblem{{Kind: StructListNotArray, Field: "comments"}}
	}
	var problems []StructureProblem
	for i, elem := range arr {
		grp, ok := elem.(map[string]interface{})
		if !ok {
			problems = append(problems, StructureProblem{Kind: StructItemNotObject, Index: i})
			continue
		}
		if s, _ := grp["post"].(string); s == "" {
			problems = append(problems, StructureProblem{Kind: StructItemMissing, Index: i, Field: "post"})
		}
		blessedRaw, ok := grp["blessed"]
		if !ok {
			problems = append(problems, StructureProblem{Kind: StructItemMissing, Index: i, Field: "blessed"})
			continue
		}
		entries, ok := blessedRaw.([]interface{})
		if !ok {
			problems = append(problems, StructureProblem{Kind: StructEntriesNotArray, Index: i})
			continue
		}
		for j, e := range entries {
			em, ok := e.(map[string]interface{})
			if !ok {
				problems = append(problems, StructureProblem{Kind: StructEntryNotObject, Index: i, Entry: j})
				continue
			}
			for _, req := range []string{"url", "blessed_at"} {
				if s, _ := em[req].(string); s == "" {
					problems = append(problems, StructureProblem{Kind: StructEntryMissing, Index: i, Entry: j, Field: req})
				}
			}
		}
	}
	return problems
}

// FollowingStructure checks following.json: a top-level "following" array of
// entries carrying non-empty "url" and "added_at". Every problem is returned, in
// reading order.
func FollowingStructure(data []byte) []StructureProblem {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return []StructureProblem{{Kind: StructInvalidJSON, Err: err}}
	}
	listRaw, ok := raw["following"]
	if !ok {
		return []StructureProblem{{Kind: StructMissingList, Field: "following"}}
	}
	arr, ok := listRaw.([]interface{})
	if !ok {
		return []StructureProblem{{Kind: StructListNotArray, Field: "following"}}
	}
	var problems []StructureProblem
	for i, elem := range arr {
		em, ok := elem.(map[string]interface{})
		if !ok {
			problems = append(problems, StructureProblem{Kind: StructItemNotObject, Index: i})
			continue
		}
		for _, req := range []string{"url", "added_at"} {
			if s, _ := em[req].(string); s == "" {
				problems = append(problems, StructureProblem{Kind: StructItemMissing, Index: i, Field: req})
			}
		}
	}
	return problems
}

// ---------- index.jsonl entries ----------

// IndexProblemKind names what is wrong with one index line.
type IndexProblemKind int

const (
	IndexInvalidJSON  IndexProblemKind = iota // the line is not JSON (Err)
	IndexMissingField                         // a required string field is absent or empty (Field)
	IndexBadVersion                           // current_version lacks the sha256: prefix (Value)
	IndexBadPublished                         // published is not RFC3339 (Value, Err)
)

// IndexProblem is one problem on one line of index.jsonl.
type IndexProblem struct {
	Line  int // 1-based, counting blank lines
	Kind  IndexProblemKind
	Field string
	Value string
	Err   error
}

// IndexScan is what reading an index found. OpenErr is the error opening the
// file (os.IsNotExist when there is none); ScanErr is a read error part-way,
// after which Problems holds what was found before it.
type IndexScan struct {
	Problems []IndexProblem
	OpenErr  error
	ScanErr  error
}

// IndexEntryProblems validates every non-blank line of the index at path: each
// must be JSON with non-empty string type, path, published and current_version,
// a sha256:-prefixed current_version, and an RFC3339 published. Within a line,
// problems come in that order. maxLine bounds one line (bufio's token limit);
// 0 keeps bufio's default.
//
// ⚠️ The two callers pass different bounds on purpose, and that is recorded, not
// fixed: Patrol keeps bufio's 64 KiB default (a longer line is a read error),
// Tailor allows 1 MiB. Aligning them would change what one of them reports.
func IndexEntryProblems(path string, maxLine int) IndexScan {
	f, err := os.Open(path)
	if err != nil {
		return IndexScan{OpenErr: err}
	}
	defer f.Close()

	var scan IndexScan
	scanner := bufio.NewScanner(f)
	if maxLine > 0 {
		scanner.Buffer(make([]byte, 0, 64*1024), maxLine)
	}
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			scan.Problems = append(scan.Problems, IndexProblem{Line: lineNum, Kind: IndexInvalidJSON, Err: err})
			continue
		}
		for _, req := range []string{"type", "path", "published", "current_version"} {
			if s, _ := entry[req].(string); s == "" {
				scan.Problems = append(scan.Problems, IndexProblem{Line: lineNum, Kind: IndexMissingField, Field: req})
			}
		}
		if cv, _ := entry["current_version"].(string); cv != "" && !strings.HasPrefix(cv, "sha256:") {
			scan.Problems = append(scan.Problems, IndexProblem{Line: lineNum, Kind: IndexBadVersion, Value: cv})
		}
		if published, _ := entry["published"].(string); published != "" {
			if _, err := time.Parse(time.RFC3339, published); err != nil {
				scan.Problems = append(scan.Problems, IndexProblem{Line: lineNum, Kind: IndexBadPublished, Value: published, Err: err})
			}
		}
	}
	scan.ScanErr = scanner.Err()
	return scan
}

// ---------- Foreign content under content/ ----------

// FrontmatterAuthor reads the `author:` field of a markdown file's frontmatter:
// the first column-0 `author:` line between the first two `---` lines. "" when
// there is none.
func FrontmatterAuthor(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inFrontmatter := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			if inFrontmatter {
				return "" // end of frontmatter, author not found
			}
			inFrontmatter = true
			continue
		}
		if !inFrontmatter {
			continue
		}
		if strings.HasPrefix(line, "author:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "author:"))
		}
	}
	return ""
}

// ForeignFile is a published markdown file another author wrote.
type ForeignFile struct {
	Path   string // site-relative, OS separators
	Type   string // the content subdirectory it was found under: "post", "comment"
	Author string
}

// ForeignContent walks content/pub.polis.core/<type>/ for each of types, in the
// order given, and returns every .md whose frontmatter author is set and is not
// owner — another author's content under this site's canonical tree. Dot
// directories (.versions/) are skipped. owner "" answers nothing: without the
// site's own identity every authored file would look foreign.
func ForeignContent(siteDir, owner string, types ...string) []ForeignFile {
	if owner == "" {
		return nil
	}
	var found []ForeignFile
	for _, typ := range types {
		root := filepath.Join(siteDir, "content", "pub.polis.core", typ)
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != root && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir // .versions/, etc.
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			author := FrontmatterAuthor(path)
			if author == "" || author == owner {
				return nil
			}
			rel, _ := filepath.Rel(siteDir, path)
			found = append(found, ForeignFile{Path: rel, Type: typ, Author: author})
			return nil
		})
	}
	return found
}

// ---------- registry.json integrity ----------

// FQNIssue is what is wrong with an active_theme or active_shape value.
type FQNIssue int

const (
	FQNFine        FQNIssue = iota
	FQNUnparseable          // not a valid FQN
	FQNUndeclared           // a valid FQN the default core bundle does not declare
)

// RegistryState is registry.json read for integrity. Err is set when the file
// exists and does not parse; Raw is nil when there is no registry.
type RegistryState struct {
	Err error
	Raw map[string]interface{}

	// SchemaNotInteger and SchemaTooNew describe schema_version when it is a
	// number; TooNew is only judged for an integer.
	SchemaVersion    float64
	SchemaNotInteger bool
	SchemaTooNew     bool

	ActiveTheme, ActiveShape string
	// ThemeIssue and ShapeIssue judge a non-empty value; an empty one is FQNFine
	// and the caller decides what empty means.
	ThemeIssue, ShapeIssue FQNIssue
}

// ReadRegistryState reads .polis/bundles/registry.json and judges its schema
// version and active theme and shape against the default core bundle.
func ReadRegistryState(siteDir string) RegistryState {
	raw, err := bundle.LoadRegistryRaw(siteDir)
	if err != nil {
		return RegistryState{Err: err}
	}
	st := RegistryState{Raw: raw}
	if raw == nil {
		return st
	}
	if sv, ok := raw["schema_version"].(float64); ok {
		st.SchemaVersion = sv
		st.SchemaNotInteger = math.Trunc(sv) != sv
		st.SchemaTooNew = !st.SchemaNotInteger && int(sv) > bundle.CurrentRegistrySchemaVersion
	}
	defaults := bundle.DefaultCoreBundle()
	st.ActiveTheme, _ = raw["active_theme"].(string)
	st.ActiveShape, _ = raw["active_shape"].(string)
	st.ThemeIssue = judgeFQN(st.ActiveTheme, func(name string) bool { _, ok := defaults.Themes[name]; return ok })
	st.ShapeIssue = judgeFQN(st.ActiveShape, func(name string) bool { _, ok := defaults.Shapes[name]; return ok })
	return st
}

func judgeFQN(value string, declared func(string) bool) FQNIssue {
	if value == "" {
		return FQNFine
	}
	fqn, err := bundle.ParseFQN(value)
	if err != nil {
		return FQNUnparseable
	}
	if !declared(fqn.Name) {
		return FQNUndeclared
	}
	return FQNFine
}

// ---------- Orphaned theme directories ----------

// OrphanSkip says why OrphanedThemeDirs did not judge. These checks never
// report on no information: a reap based on an unreadable registry would be a
// mass reap.
type OrphanSkip int

const (
	OrphanJudged             OrphanSkip = iota
	OrphanNoThemesDir                   // nothing installed yet
	OrphanRegistryUnreadable            // registry integrity's finding, not this one
	OrphanNoDeclaredThemes              // the registry lists no theme_versions for the core bundle yet
)

// ThemesDir is where the core bundle's themes are installed.
func ThemesDir(siteDir string) string {
	return filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "themes")
}

// OrphanedThemeDirs returns the installed theme directories the registry does
// not list in the core bundle's theme_versions, in directory order.
func OrphanedThemeDirs(siteDir string) ([]string, OrphanSkip) {
	entries, err := os.ReadDir(ThemesDir(siteDir))
	if err != nil {
		return nil, OrphanNoThemesDir
	}
	reg, err := bundle.LoadRegistry(siteDir)
	if err != nil || reg == nil {
		return nil, OrphanRegistryUnreadable
	}
	declared := make(map[string]bool)
	for _, ib := range reg.InstalledBundles {
		if ib.Name != "pub.polis.core" {
			continue
		}
		for name := range ib.ThemeVersions {
			declared[name] = true
		}
	}
	if len(declared) == 0 {
		return nil, OrphanNoDeclaredThemes
	}
	var orphans []string
	for _, e := range entries {
		if e.IsDir() && !declared[e.Name()] {
			orphans = append(orphans, e.Name())
		}
	}
	return orphans, OrphanJudged
}

// ---------- Retired feed state ----------

// StaleScopedFeedFiles returns the retired per-scope feed caches
// (pub.polis.feed.followers.jsonl, pub.polis.feed.me.jsonl) under any
// .polis/ds/<domain>/pub.polis.core/state/, as absolute paths, domains in
// directory order. err is reading .polis/ds itself: no DS state at all.
func StaleScopedFeedFiles(siteDir string) ([]string, error) {
	dsDir := filepath.Join(siteDir, ".polis", "ds")
	entries, err := os.ReadDir(dsDir)
	if err != nil {
		return nil, err
	}
	var found []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		stateDir := filepath.Join(dsDir, entry.Name(), "pub.polis.core", "state")
		for _, scope := range []string{"followers", "me"} {
			p := filepath.Join(stateDir, "pub.polis.feed."+scope+".jsonl")
			if _, err := os.Stat(p); err == nil {
				found = append(found, p)
			}
		}
	}
	return found, nil
}

// StaleCursorFile is a cursors.json still carrying the retired
// pub.polis.feed.viewed_at key.
type StaleCursorFile struct {
	Domain string // the .polis/ds/<domain> it sits under
	Path   string // absolute
}

// StaleFeedViewedAtFiles returns every .polis/ds/<domain>/pub.polis.core/state/
// cursors.json whose "cursors" object still has pub.polis.feed.viewed_at, in
// directory order. A file that does not parse, or has no cursors object, is not
// a finding. err is reading .polis/ds itself.
func StaleFeedViewedAtFiles(siteDir string) ([]StaleCursorFile, error) {
	dsDir := filepath.Join(siteDir, ".polis", "ds")
	entries, err := os.ReadDir(dsDir)
	if err != nil {
		return nil, err
	}
	var found []StaleCursorFile
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dsDir, entry.Name(), "pub.polis.core", "state", "cursors.json")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string]interface{}
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		cursors, ok := raw["cursors"].(map[string]interface{})
		if !ok {
			continue
		}
		if _, has := cursors["pub.polis.feed.viewed_at"]; has {
			found = append(found, StaleCursorFile{Domain: entry.Name(), Path: path})
		}
	}
	return found, nil
}

// ---------- Policy content ----------

// PolicyContentDrift returns which policy files ("private", then "public")
// exist and differ from the canonical defaults, compared trimmed. A missing
// file is not drift — the presence checks report that.
func PolicyContentDrift(siteDir string) []string {
	var drifted []string
	if data, err := os.ReadFile(filepath.Join(siteDir, ".polis", "policies", "rules.jsonl")); err == nil {
		if strings.TrimSpace(string(data)) != strings.TrimSpace(policy.DefaultPrivatePolicyContent()) {
			drifted = append(drifted, "private")
		}
	}
	if data, err := os.ReadFile(filepath.Join(siteDir, "policies", "rules.jsonl")); err == nil {
		if strings.TrimSpace(string(data)) != strings.TrimSpace(policy.DefaultPublicPolicyContent()) {
			drifted = append(drifted, "public")
		}
	}
	return drifted
}

// ---------- Key file vs published key ----------

// KeyConsistencyState compares .polis/keys/id_ed25519.pub with the public_key
// .well-known/polis publishes. KeyErr is reading the key file (and nothing
// further was read); WellKnownErr is loading .well-known/polis.
type KeyConsistencyState struct {
	KeyErr       error
	WellKnownErr error
	Match        bool
}

// KeyConsistency reports whether the local public key file and the published
// key agree, compared trimmed.
func KeyConsistency(siteDir string) KeyConsistencyState {
	data, err := os.ReadFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub"))
	if err != nil {
		return KeyConsistencyState{KeyErr: err}
	}
	wk, err := site.LoadWellKnown(siteDir)
	if err != nil {
		return KeyConsistencyState{WellKnownErr: err}
	}
	return KeyConsistencyState{Match: strings.TrimSpace(string(data)) == strings.TrimSpace(wk.PublicKey)}
}

// ---------- Reference payload bytes ----------

// ReferencePayloadDrift compares every file of the embedded core-bundle
// reference payload with what is installed under .polis/bundles/pub.polis.core/.
// installed is false when that directory does not exist — nothing to compare,
// and the version check reports it.
func ReferencePayloadDrift(siteDir string) (mismatches []string, installed bool, err error) {
	if _, err := os.Stat(filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core")); os.IsNotExist(err) {
		return nil, false, nil
	}
	mismatches, err = bundle.CompareReferencePayload(siteDir, "pub.polis.core")
	return mismatches, true, err
}

// ---------- Bundle paths in .well-known/polis ----------

// BundlePathProblem is a bundle whose declared path does not lead to a file.
// Path "" means the declaration has no path; otherwise Err is the stat error.
type BundlePathProblem struct {
	Bundle string
	Path   string
	Err    error
}

// String is the finding as both Patrol and Tailor have always worded it.
// ⛔ Medic matches this wording ("bundle pub.polis.core", "missing on disk",
// "empty path") to decide between rewriting the core path and flagging.
func (p BundlePathProblem) String() string {
	switch {
	case p.Path == "":
		return fmt.Sprintf("bundle %s has empty path", p.Bundle)
	case os.IsNotExist(p.Err):
		return fmt.Sprintf("bundle %s path %q missing on disk", p.Bundle, p.Path)
	default:
		return fmt.Sprintf("bundle %s path %q: %v", p.Bundle, p.Path, p.Err)
	}
}

// BundlePathProblems returns each bundle declared in wk whose path is empty or
// does not stat, in map order. Both callers report the first.
func BundlePathProblems(siteDir string, wk *site.WellKnown) []BundlePathProblem {
	var problems []BundlePathProblem
	for name, entry := range wk.Bundles {
		if entry.Path == "" {
			problems = append(problems, BundlePathProblem{Bundle: name})
			continue
		}
		if _, err := os.Stat(filepath.Join(siteDir, entry.Path)); err != nil {
			problems = append(problems, BundlePathProblem{Bundle: name, Path: entry.Path, Err: err})
		}
	}
	return problems
}

// ---------- Webapp config ----------

// ViewModeState is what the webapp config says about the retired view_mode key.
type ViewModeState int

const (
	ViewModeUnreadable  ViewModeState = iota // no config, or it cannot be read
	ViewModeUnparseable                      // not a JSON object
	ViewModeAbsent                           // parsed; no view_mode
	ViewModePresent                          // parsed; view_mode present
)

// WebappConfigPath is the webapp's config file.
func WebappConfigPath(siteDir string) string {
	return filepath.Join(siteDir, ".polis", "webapp", "config.json")
}

// WebappViewMode reads the webapp config and reports whether the retired
// view_mode key is present, returning the parsed object for a caller that
// removes it.
func WebappViewMode(siteDir string) (ViewModeState, map[string]interface{}) {
	data, err := os.ReadFile(WebappConfigPath(siteDir))
	if err != nil {
		return ViewModeUnreadable, nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return ViewModeUnparseable, nil
	}
	if _, exists := obj["view_mode"]; exists {
		return ViewModePresent, obj
	}
	return ViewModeAbsent, obj
}

// ---------- DM conversations directory ----------

// DMConversationsDir is where DM conversations are stored; it must be 0700.
func DMConversationsDir(siteDir string) string {
	return filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "dm", "conversations")
}

// DMConversations stats the DM conversations directory.
func DMConversations(siteDir string) (os.FileInfo, error) {
	return os.Stat(DMConversationsDir(siteDir))
}

// ---------- Legacy .well-known/polis author field ----------

// LegacyAuthor is what .well-known/polis says about the retired `author` field.
// The two actors apply different rules to it, deliberately: Patrol reports the
// key whenever it is present; Tailor migrates only a string `author` with no
// string `author_name` beside it.
type LegacyAuthor struct {
	ReadErr  error
	ParseErr error

	HasAuthorKey       bool   // the `author` key is present, whatever its type
	Author             string // its value when a string
	AuthorIsString     bool
	AuthorNameIsString bool
}

// LegacyAuthorField reads .well-known/polis for the retired `author` field.
func LegacyAuthorField(siteDir string) LegacyAuthor {
	data, err := os.ReadFile(filepath.Join(siteDir, ".well-known", "polis"))
	if err != nil {
		return LegacyAuthor{ReadErr: err}
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return LegacyAuthor{ParseErr: err}
	}
	var la LegacyAuthor
	_, la.HasAuthorKey = raw["author"]
	la.Author, la.AuthorIsString = raw["author"].(string)
	_, la.AuthorNameIsString = raw["author_name"].(string)
	return la
}
