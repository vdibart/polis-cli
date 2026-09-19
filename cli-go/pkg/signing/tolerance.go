package signing

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Tolerant verification — SIGNET epic 47.
//
// ⛔ A SHIPPED VERIFIER IS FROZEN. When a writer adds a signed field, every
// verifier already out there rebuilds the OLD field set, gets different bytes,
// and reports `invalid` for a file its author signed correctly. Nobody can
// patch those verifiers: they are on self-hosters' machines, in other
// operators' deployments, and in archives read years from now. Two things
// follow, and the second is worse than the first — a reader who has seen
// `invalid` on a good file learns to ignore `invalid`, and the format ossifies
// because adding a field breaks the network.
//
// The rule, in one line: A VERIFIER THAT MEETS A FIELD IT DOES NOT RECOGNISE
// REPORTS `unknown`, NEVER `invalid`.
//
//	 rebuild verifies | unrecognised fields | status
//	------------------+---------------------+---------
//	 yes              | no                  | valid
//	 yes              | yes                 | valid — and it says the extra
//	                  |                     | fields are NOT covered
//	 no               | yes                 | unknown
//	 no               | no                  | invalid
//
// ⭐ It locks nothing in. There is no format marker, no dispatch table and no
// schema registry — the recognised field set is READ OFF THE STRUCT the package
// already declares, so it is exactly "what this build understands". No existing
// signature changes.
//
// ⚠️ THE COST, AND WHY IT IS ACCEPTED. A tamperer can add a junk field and turn
// `invalid` into `unknown`, quieting an alarm. Nothing tampered is ever
// ACCEPTED — `unknown` is never `valid`, and Law 2 leaves the judgment with the
// consumer — but a downgrade is real. The mitigation is consumer policy, not
// verifier behaviour: Judge treats an unrecognised signed field on a site WE
// write as a finding, because our own writer never emits a field our own
// verifier lacks (Judge's check 15; docs/general/concepts/actors.md).

// The status vocabulary, as plain strings.
//
// Every artifact package declares its own `SignatureStatus` string enum with
// these exact four values (following, metadata, tag, attestation, actor, and —
// added by this epic — license). They are deliberately NOT hoisted into one
// type here: `pkg/metadata` must not import `pkg/following`, and a shared enum
// would drag the whole dependency graph together for four constants. Resolve
// returns a plain string and each caller converts.
//
// ⛔ NO NEW STATUS. `unknown` already means "could not check" in all six; an
// unrecognised field is one more reason a verifier could not check, and the
// reason belongs in the explanation, never in the vocabulary (epic 47 D3).
const (
	StatusUnsigned = "unsigned"
	StatusValid    = "valid"
	StatusInvalid  = "invalid"
	StatusUnknown  = "unknown"
)

// Outcome is what a verifier reports once the tolerance rule is applied.
type Outcome struct {
	// Status is one of the constants above — never StatusUnsigned, which is
	// decided before a signature is rebuilt at all.
	Status string
	// Unrecognised are the JSON paths this build does not declare, in sorted
	// order. Non-empty on both the `valid` and the `unknown` rows.
	Unrecognised []string
}

// Resolve applies the rule. verified is whether the rebuilt signing base
// verified against the key; unrecognised is UnrecognisedFields' answer for the
// same document.
//
// ⛔ THIS IS THE ONE PLACE THE TABLE LIVES. Six packages call it; none of them
// re-derives the mapping. A second copy is how the rows drift apart.
func Resolve(verified bool, unrecognised []string) Outcome {
	switch {
	case verified:
		return Outcome{Status: StatusValid, Unrecognised: unrecognised}
	case len(unrecognised) > 0:
		return Outcome{Status: StatusUnknown, Unrecognised: unrecognised}
	default:
		return Outcome{Status: StatusInvalid}
	}
}

// Explain is the sentence that goes with the outcome, and it is the whole of
// D3: the REASON lives here, never in the status.
//
// Empty for a plain valid or a plain invalid — those two rows say everything in
// the status alone, and the packages already have their own wording for
// invalid.
func (o Outcome) Explain() string {
	if len(o.Unrecognised) == 0 {
		return ""
	}
	if o.Status == StatusValid {
		return "signature verifies; " + fieldList(o.Unrecognised) + " not covered by it"
	}
	return "signed with fields this verifier does not understand: " + strings.Join(o.Unrecognised, ", ")
}

// UncoveredNote is the `valid`-row sentence on its own, for a report surface
// that has the field list but not an Outcome — the third row of the table says
// a verifier must SAY the extra fields are not covered, and a status of "valid"
// does not say it.
//
// Empty when there is nothing to say, so a caller can append it unconditionally.
func UncoveredNote(unrecognised []string) string {
	if len(unrecognised) == 0 {
		return ""
	}
	return fieldList(unrecognised) + " not covered by the signature"
}

func fieldList(fields []string) string {
	verb := "are"
	if len(fields) == 1 {
		verb = "is"
	}
	return strings.Join(fields, ", ") + " " + verb
}

// UnrecognisedFields lists the JSON members present in raw that shape's type
// does not declare — at EVERY nesting level, including list entries (epic 47
// D1). Paths look like `comments[0].blessed[1].endorsed_by`.
//
// ⚠️ shape must be the FULL FILE struct, not the signable one. `signature` and
// `current_version` are in the document and are perfectly recognised; they are
// merely outside the signing base, which is a different question.
//
// ⭐ The recognised set is reflected off the struct rather than hand-listed, and
// that is the point: it can never drift from what the package actually parses.
// A field added to the struct is recognised by the same commit that adds it.
// (Epic 47 D2 — one helper, six callers, ⛔ not six hand-rolled key lists.)
//
// Unparseable or type-mismatched input yields no findings rather than an error:
// this function answers "which members are new to me", and a document that does
// not parse has already failed elsewhere. It never reports a member twice and
// the result is sorted, so a message is stable.
func UnrecognisedFields(raw []byte, shape any) []string {
	if len(raw) == 0 || shape == nil {
		return nil
	}
	t := reflect.TypeOf(shape)
	var found []string
	walkUnrecognised(json.RawMessage(raw), t, "", &found)
	if len(found) == 0 {
		return nil
	}
	sort.Strings(found)
	return found
}

func walkUnrecognised(raw json.RawMessage, t reflect.Type, path string, out *[]string) {
	for t != nil && (t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface) {
		if t.Kind() == reflect.Interface {
			// `any` accepts anything, so nothing below it is unrecognised.
			return
		}
		t = t.Elem()
	}
	if t == nil {
		return
	}

	switch t.Kind() {
	case reflect.Struct:
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return
		}
		known, folded := knownFields(t)
		for name, member := range obj {
			// ⚠️ MATCH THE WAY encoding/json MATCHES: exact first, then
			// case-insensitive. A document writing "Version" IS parsed into a
			// field tagged `json:"version"`, so reporting it as unrecognised
			// would be a lie — the member is read, and on a tampered file it
			// would downgrade `invalid` to `unknown` for no reason at all.
			// "Recognised" has to mean "this build parses it", exactly.
			field, ok := known[name]
			if !ok {
				field, ok = folded[strings.ToLower(name)]
			}
			if !ok {
				*out = append(*out, path+name)
				continue
			}
			walkUnrecognised(member, field.Type, path+name+".", out)
		}

	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			// []byte is a base64 string, not a list.
			return
		}
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return
		}
		// Trim the trailing "." so the index binds to the member name:
		// "comments[0]." rather than "comments.[0].".
		base := strings.TrimSuffix(path, ".")
		for i, item := range items {
			walkUnrecognised(item, t.Elem(), base+"["+strconv.Itoa(i)+"].", out)
		}

	case reflect.Map:
		// ⚠️ A map's KEYS are data, never schema — the attestation `payload` is
		// the only one, and an unrecognised payload key is exactly what the
		// type is for. Recurse into the values so a struct-valued map would
		// still be checked; today every value is a string and this is a no-op.
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			return
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			walkUnrecognised(obj[k], t.Elem(), path+k+".", out)
		}
	}
}

// knownFields maps the JSON member names a struct declares to their fields,
// flattening anonymous embedding the way encoding/json does.
//
// ⚠️ A field tagged `json:"-"` is NOT registered: the package does not parse
// that member, so a document carrying it really is saying something this build
// cannot read. None of the six types has one today; getting it right costs a
// branch and getting it wrong would be silent.
func knownFields(t reflect.Type) (exact, folded map[string]reflect.StructField) {
	known := make(map[string]reflect.StructField, t.NumField())
	lower := make(map[string]reflect.StructField, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() && !f.Anonymous {
			continue
		}
		tag := f.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if tag == "-" {
			continue
		}
		if name == "" {
			if f.Anonymous {
				et := f.Type
				for et.Kind() == reflect.Pointer {
					et = et.Elem()
				}
				if et.Kind() == reflect.Struct {
					embedded, embeddedFolded := knownFields(et)
					for n, ef := range embedded {
						if _, clash := known[n]; !clash {
							known[n] = ef
						}
					}
					for n, ef := range embeddedFolded {
						if _, clash := lower[n]; !clash {
							lower[n] = ef
						}
					}
					continue
				}
			}
			name = f.Name
		}
		known[name] = f
		if _, clash := lower[strings.ToLower(name)]; !clash {
			lower[strings.ToLower(name)] = f
		}
	}
	return known, lower
}

// Describe renders a field list for a log line or a finding message.
func Describe(unrecognised []string) string {
	if len(unrecognised) == 0 {
		return ""
	}
	return fmt.Sprintf("%d unrecognised signed field(s): %s", len(unrecognised), strings.Join(unrecognised, ", "))
}
