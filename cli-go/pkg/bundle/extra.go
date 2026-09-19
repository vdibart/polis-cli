package bundle

import "github.com/vdibart/polis-cli/cli-go/pkg/jsonextra"

// Lossless JSON for bundle.json and registry.json, at every nesting level.
//
// Both files are UNSIGNED, so what this build does not model is PRESERVED,
// never refused: a member another build (newer or older) wrote survives
// SaveBundle and SaveRegistry. Declared members keep struct declaration order,
// so a file with nothing unmodelled saves byte-identically to before;
// unmodelled members follow, sorted (jsonextra.Marshal).
//
// ⭐ NotificationRule is shared by both files, so one Extra serves both.
// ⚠️ Preserving is what it says: a legacy member a migration means to REMOVE
// (the old installed_bundles[].active) is removed by that migration's own
// raw-map writer, never by a struct round-trip dropping it by accident.

type bundleFields Bundle

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (b *Bundle) UnmarshalJSON(data []byte) error {
	f := bundleFields(*b)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*b = Bundle(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (b Bundle) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(bundleFields(b), b.Extra)
}

type shapeFields Shape

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (s *Shape) UnmarshalJSON(data []byte) error {
	f := shapeFields(*s)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*s = Shape(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (s Shape) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(shapeFields(s), s.Extra)
}

type themeFields Theme

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (t *Theme) UnmarshalJSON(data []byte) error {
	f := themeFields(*t)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*t = Theme(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (t Theme) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(themeFields(t), t.Extra)
}

type handlerFields Handler

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (h *Handler) UnmarshalJSON(data []byte) error {
	f := handlerFields(*h)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*h = Handler(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (h Handler) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(handlerFields(h), h.Extra)
}

type dsConfigFields DSConfig

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (d *DSConfig) UnmarshalJSON(data []byte) error {
	f := dsConfigFields(*d)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*d = DSConfig(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (d DSConfig) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(dsConfigFields(d), d.Extra)
}

type contentTypeFields ContentType

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (c *ContentType) UnmarshalJSON(data []byte) error {
	f := contentTypeFields(*c)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*c = ContentType(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (c ContentType) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(contentTypeFields(c), c.Extra)
}

type storageConfigFields StorageConfig

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (s *StorageConfig) UnmarshalJSON(data []byte) error {
	f := storageConfigFields(*s)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*s = StorageConfig(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (s StorageConfig) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(storageConfigFields(s), s.Extra)
}

type notificationRuleFields NotificationRule

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (n *NotificationRule) UnmarshalJSON(data []byte) error {
	f := notificationRuleFields(*n)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*n = NotificationRule(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (n NotificationRule) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(notificationRuleFields(n), n.Extra)
}

type bundleRegistryFields BundleRegistry

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (r *BundleRegistry) UnmarshalJSON(data []byte) error {
	f := bundleRegistryFields(*r)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*r = BundleRegistry(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (r BundleRegistry) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(bundleRegistryFields(r), r.Extra)
}

type installedBundleFields InstalledBundle

// UnmarshalJSON decodes the modelled fields and keeps the rest in Extra.
func (i *InstalledBundle) UnmarshalJSON(data []byte) error {
	f := installedBundleFields(*i)
	extra, err := jsonextra.Unmarshal(data, &f)
	if err != nil {
		return err
	}
	f.Extra = extra
	*i = InstalledBundle(f)
	return nil
}

// MarshalJSON writes the modelled fields, then every Extra member.
func (i InstalledBundle) MarshalJSON() ([]byte, error) {
	return jsonextra.Marshal(installedBundleFields(i), i.Extra)
}
