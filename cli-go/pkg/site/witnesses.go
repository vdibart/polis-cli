package site

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/atomicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/jsonextra"
)

// The site's published witness set — SIGNET epic 32.
//
// A discovery service countersigns each content registration it verifies; the
// site publishes those countersignatures so a stranger's verifier can read
// them. One served file, keyed by artifact URL, a LIST per artifact (D2),
// discovered only through the `witnesses` pointer in .well-known/polis.
//
// ⛔ WHAT THIS FILE IS NOT.
//   - Not a declared bundle content type. A witness is a third party's
//     testimony, not the site's speech; declaring it would make it enumerable
//     as the site's own content and invite someone to SIGN it, which is the
//     circularity epic 32's D3 forbids. It joins did.json and index.jsonl as a
//     published file that is not a content type.
//   - Not signed. Every record inside carries its own DS signature; a site
//     signature over the collection would add nothing and claim authorship of
//     somebody else's testimony.
//   - Not a projection that may be rebuilt by replacement. A DS holds only the
//     CURRENT version's witness (its rows are upserted), so for every superseded
//     version THIS FILE IS THE ONLY COPY IN EXISTENCE. Writes merge; nothing
//     here ever removes a record. See discovery.MergeWitnesses.
//
// ⚠️ Not inside .well-known/polis: rotations are bounded and content is not, and
// that document is fetched on every verification. (A ROTATION's witness does
// ride inside public_key_history — the witness lives beside the thing witnessed.)

// witnessesPointerField is the .well-known/polis key naming this file.
const witnessesPointerField = "witnesses"

// DefaultWitnessesPointer is where polis writes the file when a site publishes
// none yet. ⚠️ Readers never assume it: discovery is by pointer.
const DefaultWitnessesPointer = "/content/witness/witnesses.json"

// WitnessFile is the published document.
type WitnessFile struct {
	// Witnesses maps an artifact's registered URL to every witness of it, in
	// witnessed order. encoding/json sorts the map keys, so the file is
	// byte-stable for the same contents.
	Witnesses map[string][]discovery.Witness `json:"witnesses"`

	// Extra carries members of the file this build does not model, so a merge
	// keeps them (each witness record keeps its own: discovery.Witness.Extra).
	// ⭐ The file is unsigned, so the answer is PRESERVE, never refuse — and
	// for superseded versions it is the only copy of the testimony it holds.
	Extra map[string]json.RawMessage `json:"-"`
}

type witnessFileFields WitnessFile

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (f *WitnessFile) UnmarshalJSON(data []byte) error {
	a := witnessFileFields(*f)
	extra, err := jsonextra.Unmarshal(data, &a)
	if err != nil {
		return err
	}
	a.Extra = extra
	*f = WitnessFile(a)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (f WitnessFile) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(witnessFileFields(f), f.Extra)
}

// For returns the witness set for one artifact URL, or nil.
func (f *WitnessFile) For(url string) []discovery.Witness {
	if f == nil {
		return nil
	}
	return f.Witnesses[url]
}

// WitnessesPointer returns the site's `witnesses` pointer, or "" when it
// publishes none. Absent is a defined state — every site before epic 32, and
// any site never witnessed — and not an error.
func WitnessesPointer(siteDir string) string {
	raw, err := LoadWellKnownRaw(siteDir)
	if err != nil || raw == nil {
		return ""
	}
	v, _ := raw[witnessesPointerField].(string)
	return v
}

// WitnessesPointerFromWellKnown reads the pointer from already-fetched
// .well-known/polis bytes, for a verifier working over HTTP.
func WitnessesPointerFromWellKnown(data []byte) string {
	var raw map[string]interface{}
	if json.Unmarshal(data, &raw) != nil {
		return ""
	}
	v, _ := raw[witnessesPointerField].(string)
	return v
}

// WitnessesFromBytes parses a witness file.
func WitnessesFromBytes(data []byte) (*WitnessFile, error) {
	var f WitnessFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("witness file does not parse: %w", err)
	}
	if f.Witnesses == nil {
		f.Witnesses = map[string][]discovery.Witness{}
	}
	return &f, nil
}

// witnessesPath resolves a pointer to a path inside siteDir, refusing one that
// escapes the site.
func witnessesPath(siteDir, pointer string) (string, error) {
	rel := filepath.Clean(strings.TrimPrefix(pointer, "/"))
	if rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("witnesses pointer %q does not name a file inside the site", pointer)
	}
	return filepath.Join(siteDir, rel), nil
}

// LoadWitnesses reads the site's published witness set through its pointer.
// Returns (nil, nil) when the site publishes no pointer.
func LoadWitnesses(siteDir string) (*WitnessFile, error) {
	pointer := WitnessesPointer(siteDir)
	if pointer == "" {
		return nil, nil
	}
	path, err := witnessesPath(siteDir, pointer)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return WitnessesFromBytes(data)
}

// RecordWitness adds witnesses of one artifact to the site's published set, and
// publishes the `witnesses` pointer if the site has none. Reports whether
// anything changed.
//
// ⛔ MERGE, NEVER REPLACE. See discovery.MergeWitnesses.
//
// ⚠️ The first call on a site adds a key to .well-known/polis, which Judge
// snapshots: expect one judge.alert.wellknown_change per site, once, and then
// silence. A repeat for the same site is the finding.
func RecordWitness(siteDir, artifactURL string, witnesses ...discovery.Witness) (bool, error) {
	if artifactURL == "" {
		return false, fmt.Errorf("no artifact URL to record a witness under")
	}
	if len(witnesses) == 0 {
		return false, nil
	}

	pointer := WitnessesPointer(siteDir)
	setPointer := pointer == ""
	if setPointer {
		pointer = DefaultWitnessesPointer
	}
	path, err := witnessesPath(siteDir, pointer)
	if err != nil {
		return false, err
	}

	file := &WitnessFile{Witnesses: map[string][]discovery.Witness{}}
	if data, rerr := os.ReadFile(path); rerr == nil {
		parsed, perr := WitnessesFromBytes(data)
		if perr != nil {
			// ⛔ Never overwrite a file we cannot read: it may hold the only copy
			// of testimony for superseded versions.
			return false, fmt.Errorf("refusing to rewrite %s: %w", path, perr)
		}
		file = parsed
	} else if !os.IsNotExist(rerr) {
		return false, rerr
	}

	merged, changed := discovery.MergeWitnesses(file.Witnesses[artifactURL], witnesses...)
	if changed {
		file.Witnesses[artifactURL] = merged
		data, merr := encodeWitnessFile(file)
		if merr != nil {
			return false, merr
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return false, err
		}
		if err := atomicfile.WriteFile(path, data, 0644); err != nil {
			return false, err
		}
	}
	if setPointer {
		if err := setWellKnownPointer(siteDir, witnessesPointerField, pointer); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

// encodeWitnessFile is the file's one encoding: indented, newline-terminated.
func encodeWitnessFile(f *WitnessFile) ([]byte, error) {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// WitnessedURLs returns every artifact URL the file carries witnesses for,
// sorted — for reports that walk the set.
func (f *WitnessFile) WitnessedURLs() []string {
	if f == nil {
		return nil
	}
	out := make([]string, 0, len(f.Witnesses))
	for u := range f.Witnesses {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}
