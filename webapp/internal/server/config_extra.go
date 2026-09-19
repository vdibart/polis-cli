package server

import "github.com/vdibart/polis-cli/cli-go/pkg/jsonextra"

// Lossless JSON for .polis/webapp/config.json. Declared members keep struct
// declaration order, so a config with nothing unmodelled saves byte-identically
// to before; unmodelled members follow, sorted. Hooks keep theirs in
// hooks.HookConfig.Extra.

type configFields Config

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (c *Config) UnmarshalJSON(data []byte) error {
	f := configFields(*c)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*c = Config(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (c Config) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(configFields(c), c.Extra)
}
