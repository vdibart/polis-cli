package dm

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/jsonextra"
)

// Lossless JSON for keyring.json, per nesting level (Keyring → Epoch →
// KDFParams). keyring.json is UNSIGNED, so what this build does not model is
// PRESERVED, never refused. Declared members keep struct declaration order,
// so a keyring with nothing unmodelled saves byte-identically to before.

type keyringFields Keyring
type epochFields Epoch
type kdfParamsFields KDFParams

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (k *Keyring) UnmarshalJSON(data []byte) error {
	f := keyringFields(*k)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*k = Keyring(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (k Keyring) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(keyringFields(k), k.Extra)
}

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (e *Epoch) UnmarshalJSON(data []byte) error {
	f := epochFields(*e)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*e = Epoch(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (e Epoch) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(epochFields(e), e.Extra)
}

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (p *KDFParams) UnmarshalJSON(data []byte) error {
	f := kdfParamsFields(*p)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*p = KDFParams(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (p KDFParams) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(kdfParamsFields(p), p.Extra)
}
