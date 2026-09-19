package discovery

import "github.com/vdibart/polis-cli/cli-go/pkg/jsonextra"

// Lossless JSON for a witness record. Declared members keep struct declaration
// order, so a witness with nothing unmodelled encodes exactly as before.

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (w *Witness) UnmarshalJSON(data []byte) error {
	f := witnessMembers(*w)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*w = Witness(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (w Witness) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(witnessMembers(w), w.Extra)
}
