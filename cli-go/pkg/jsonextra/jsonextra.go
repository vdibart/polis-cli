// Package jsonextra keeps the members of a JSON object that a struct does not
// model, so an UNSIGNED file loaded into that struct and saved again loses
// nothing a newer (or older) build wrote into it.
//
// ⭐ THE RULE (docs/signet/spec/signing-base.md, §6.2): an unsigned document PRESERVES what it
// does not model; a signed artifact REFUSES to rewrite (signing.GuardRewrite).
// This package is the preserve half. ⛔ Never use it on a signed type — carrying
// a member through and then signing it asserts bytes this build cannot read.
//
// The pattern, per type:
//
//	type fooFields Foo // method-less alias: encoding/json does the real work
//
//	func (f *Foo) UnmarshalJSON(data []byte) error {
//		a := fooFields(*f)
//		extra, err := jsonextra.Unmarshal(data, &a)
//		if err != nil {
//			return err
//		}
//		a.Extra = extra
//		*f = Foo(a)
//		return nil
//	}
//
//	func (f Foo) MarshalJSON() ([]byte, error) {
//		return jsonextra.Marshal(fooFields(f), f.Extra)
//	}
//
// ⚠️ Never give these methods to a type another struct EMBEDS: the embedder
// inherits them and its own fields vanish from its JSON. Use Unmodelled and
// Marshal directly instead (dm.MailboxMessage is the case).
//
// ⚠️ Marshal HTML-escapes declared fields (encoding/json does) but writes
// extras as they were read, only compacted. That asymmetry is invisible
// wherever the result passes back through encoding/json — a MarshalJSON
// method's output is re-escaped by the encoder — and shows only where a caller
// writes Marshal's bytes directly (dm's writeMessages). It is deliberate: an
// extra is carried, not re-encoded. Do not "fix" it by escaping extras here.
//
// ⛔ Not a schema system: one rule, a handful of functions.
package jsonextra

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Unmarshal decodes data into fields — a pointer to a method-less alias of the
// struct — and returns the members the struct does not declare, or nil.
func Unmarshal(data []byte, fields interface{}) (map[string]json.RawMessage, error) {
	if err := json.Unmarshal(data, fields); err != nil {
		return nil, err
	}
	t := reflect.TypeOf(fields)
	if t.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("jsonextra: want a pointer to a struct, got %s", t)
	}
	return Unmodelled(data, t.Elem())
}

// Unmodelled returns the members of the JSON object data that struct type t
// does not declare, or nil when there are none (or data is null).
//
// ⚠️ MATCH THE WAY encoding/json MATCHES: exact first, then case-insensitive. A
// member spelled "Author_Name" IS decoded into AuthorName, so keeping it as an
// extra as well would write the value out twice under two spellings.
func Unmodelled(data []byte, t reflect.Type) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	exact, folded := Declared(t)
	var extra map[string]json.RawMessage
	for name, value := range obj {
		if exact[name] || folded[strings.ToLower(name)] {
			continue
		}
		if extra == nil {
			extra = map[string]json.RawMessage{}
		}
		extra[name] = value
	}
	return extra, nil
}

// Marshal encodes fields (a method-less alias, so this does not recurse) and
// appends every extra member after them.
//
// ⭐ DECLARED FIELDS KEEP STRUCT DECLARATION ORDER — exactly what json.Marshal
// of the struct has always produced — so a file with no extras comes out
// byte-identical to what the pre-jsonextra writer wrote. Extras follow, in
// SORTED order: a map cannot remember where in the original object they sat.
//
// ⚠️ An extra that shares a DECLARED name is never written, even when the
// declared field is empty and omitted. Otherwise clearing an omitempty field
// could resurrect it from a stale extra.
func Marshal(fields interface{}, extra map[string]json.RawMessage) ([]byte, error) {
	b, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	names := undeclaredNames(reflect.TypeOf(fields), extra)
	if len(names) == 0 {
		return b, nil
	}
	if len(b) < 2 || b[0] != '{' || b[len(b)-1] != '}' {
		return nil, fmt.Errorf("jsonextra: %T did not encode as a JSON object", fields)
	}
	var buf bytes.Buffer
	buf.Write(b[:len(b)-1])
	first := len(b) == 2 // "{}"
	for _, name := range names {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		key, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		if err := json.Compact(&buf, extra[name]); err != nil {
			return nil, fmt.Errorf("jsonextra: member %q: %w", name, err)
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// MarshalSorted is Marshal with EVERY member in sorted order, declared ones
// included. It exists for .well-known/polis alone, whose writers have always
// emitted sorted members (a raw map marshals sorted, and every live tenant's
// file has been through one). Anything else wants Marshal.
func MarshalSorted(fields interface{}, extra map[string]json.RawMessage) ([]byte, error) {
	b, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	// Re-encode through a map even with no extras, so member order is sorted
	// either way.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for _, name := range undeclaredNames(reflect.TypeOf(fields), extra) {
		if _, present := m[name]; present {
			continue
		}
		m[name] = extra[name]
	}
	return json.Marshal(m)
}

// undeclaredNames lists, sorted, the extra members t does not declare.
func undeclaredNames(t reflect.Type, extra map[string]json.RawMessage) []string {
	if len(extra) == 0 {
		return nil
	}
	exact, folded := Declared(t)
	var names []string
	for name := range extra {
		if exact[name] || folded[strings.ToLower(name)] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Declared lists the JSON member names a struct type declares, exactly and
// lower-cased. Fields tagged `json:"-"` (Extra itself) are not members.
//
// ⚠️ Embedded structs are not walked: no type this package serves embeds one.
// Add that before using it on a type that does.
func Declared(t reflect.Type) (exact, folded map[string]bool) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	exact = map[string]bool{}
	folded = map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		exact[name] = true
		folded[strings.ToLower(name)] = true
	}
	return exact, folded
}
