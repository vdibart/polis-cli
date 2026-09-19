package metadata

import "github.com/vdibart/polis-cli/cli-go/pkg/jsonextra"

// Lossless JSON for an index.jsonl line (IndexEntry → InReplyToEntry).
// Declared members keep struct declaration order, so a line with nothing
// unmodelled encodes exactly as before; unmodelled members follow, sorted.

type indexEntryFields IndexEntry
type inReplyToEntryFields InReplyToEntry

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (e *IndexEntry) UnmarshalJSON(data []byte) error {
	f := indexEntryFields(*e)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*e = IndexEntry(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (e IndexEntry) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(indexEntryFields(e), e.Extra)
}

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (r *InReplyToEntry) UnmarshalJSON(data []byte) error {
	f := inReplyToEntryFields(*r)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*r = InReplyToEntry(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (r InReplyToEntry) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(inReplyToEntryFields(r), r.Extra)
}
