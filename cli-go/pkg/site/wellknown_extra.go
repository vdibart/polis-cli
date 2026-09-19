package site

import (
	"encoding/json"
	"reflect"

	"github.com/vdibart/polis-cli/cli-go/pkg/jsonextra"
)

// Lossless JSON for the identity document: every member the struct does not
// declare survives a round-trip, so no writer can drop one by forgetting it.
// The machinery is shared (pkg/jsonextra); what is particular here is the ORDER.
//
// Each type below decodes through a method-less alias of itself — so
// encoding/json does the ordinary work — and then files every member the alias
// does not declare into Extra. Encoding reverses it: the declared fields, then
// every Extra member the declared fields did not already produce, with EVERY
// member in SORTED order (jsonextra.MarshalSorted).
//
// ⭐ Sorted, because that is what SaveWellKnownRaw has always emitted (a Go map
// marshals sorted), and every live tenant's file has been through a raw write.
// A Load → Save of an untouched file therefore reproduces its bytes, and Judge's
// snapshot hash does not move. ⛔ Never switch this file to jsonextra.Marshal
// (declaration order): every tenant's .well-known/polis would re-hash once, and
// TestWellKnownWritersEmitTheBytesTheyAlwaysHave pins the bytes.

type wellKnownFields WellKnown
type avatarFields AvatarConfig
type bundleEntryFields BundleEntry

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (wk *WellKnown) UnmarshalJSON(data []byte) error {
	var f wellKnownFields
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	extra, err := unmodelledMembers(data, reflect.TypeOf(f))
	if err != nil {
		return err
	}
	f.Extra = extra
	*wk = WellKnown(f)
	return nil
}

// MarshalJSON writes the modelled fields and every Extra member.
func (wk WellKnown) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(wellKnownFields(wk), wk.Extra)
}

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (a *AvatarConfig) UnmarshalJSON(data []byte) error {
	var f avatarFields
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	extra, err := unmodelledMembers(data, reflect.TypeOf(f))
	if err != nil {
		return err
	}
	f.Extra = extra
	*a = AvatarConfig(f)
	return nil
}

// MarshalJSON writes the modelled fields and every Extra member.
func (a AvatarConfig) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(avatarFields(a), a.Extra)
}

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (b *BundleEntry) UnmarshalJSON(data []byte) error {
	var f bundleEntryFields
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	extra, err := unmodelledMembers(data, reflect.TypeOf(f))
	if err != nil {
		return err
	}
	f.Extra = extra
	*b = BundleEntry(f)
	return nil
}

// MarshalJSON writes the modelled fields and every Extra member.
func (b BundleEntry) MarshalJSON() ([]byte, error) {
	return marshalWithExtra(bundleEntryFields(b), b.Extra)
}

// unmodelledMembers returns the members of the JSON object data that t does
// not declare, or nil when there are none.
func unmodelledMembers(data []byte, t reflect.Type) (map[string]json.RawMessage, error) {
	return jsonextra.Unmodelled(data, t)
}

// marshalWithExtra encodes fields with every extra member, all sorted.
func marshalWithExtra(fields interface{}, extra map[string]json.RawMessage) ([]byte, error) {
	return jsonextra.MarshalSorted(fields, extra)
}

// declaredMembers lists the JSON member names a struct type declares.
func declaredMembers(t reflect.Type) (exact, folded map[string]bool) {
	return jsonextra.Declared(t)
}
