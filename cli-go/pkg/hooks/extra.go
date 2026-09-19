package hooks

import "github.com/vdibart/polis-cli/cli-go/pkg/jsonextra"

type hookConfigFields HookConfig

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (h *HookConfig) UnmarshalJSON(data []byte) error {
	f := hookConfigFields(*h)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*h = HookConfig(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (h HookConfig) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(hookConfigFields(h), h.Extra)
}
