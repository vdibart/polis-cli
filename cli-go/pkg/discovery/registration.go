package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// RegistrationMarker is written to disk when a site successfully registers
// with a discovery service. Its presence is the local source of truth for
// "is this site registered?" — avoiding any network call to check.
type RegistrationMarker struct {
	Domain             string `json:"domain"`
	DSURL              string `json:"ds_url"`
	RegisteredAt       string `json:"registered_at"`
	ServiceAttestation string `json:"service_attestation,omitempty"`
}

// registrationMarkerKey returns the registration marker's key for a given DS
// URL, relative to the site root: .polis/ds/{ds-domain}/registration.json. It
// is "" when the URL names no domain.
//
// ⭐ A KEY, NOT A PATH: every read, write and delete of the marker goes through
// the site's store with it.
func registrationMarkerKey(dsURL string) string {
	dsDomain := ExtractDomainFromURL(dsURL)
	if dsDomain == "" {
		return ""
	}
	return ".polis/ds/" + dsDomain + "/registration.json"
}

// IsRegisteredLocally checks whether the site has a local registration marker
// for the given discovery service. This is a pure filesystem check — no network
// call is made. Returns false if the site handle is unusable or dsURL is empty, if the marker file
// does not exist, or on any error.
func IsRegisteredLocally(site storage.Site, dsURL string) bool {
	if !site.Valid() || dsURL == "" {
		return false
	}
	key := registrationMarkerKey(dsURL)
	if key == "" {
		return false
	}
	present, _ := site.Store().Exists(context.Background(), key)
	return present
}

// WriteRegistrationMarker writes the registration marker file after a
// successful registration with the discovery service.
func WriteRegistrationMarker(site storage.Site, dsURL, siteDomain, attestation string) error {
	key := registrationMarkerKey(dsURL)
	if key == "" {
		return fmt.Errorf("could not determine marker path (empty dsURL?)")
	}

	marker := RegistrationMarker{
		Domain:             siteDomain,
		DSURL:              dsURL,
		RegisteredAt:       time.Now().UTC().Format(time.RFC3339),
		ServiceAttestation: attestation,
	}

	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal marker: %w", err)
	}

	// ⛔ A SITE STORE, so .polis stays 0700. The marker lives at
	// .polis/ds/<domain>/registration.json, and the MkdirAll that used to carry
	// that mode by hand is gone — storage.WriteAtPath would have created the
	// parents at 0755, which is the mistake this detach made three times
	// elsewhere.
	if err := site.Store().Put(context.Background(), key,
		append(data, '\n'), storage.WithMode(0600)); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}

	return nil
}

// ReadRegistrationMarker reads and parses a registration marker file.
// Returns nil and no error if the file does not exist.
func ReadRegistrationMarker(path string) (*RegistrationMarker, error) {
	data, err := storage.ReadAtPath(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var marker RegistrationMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, fmt.Errorf("parse marker: %w", err)
	}
	return &marker, nil
}

// RemoveRegistrationMarker deletes the registration marker file after a
// successful unregistration. Returns nil if the file does not exist (idempotent).
func RemoveRegistrationMarker(site storage.Site, dsURL string) error {
	key := registrationMarkerKey(dsURL)
	if key == "" {
		return nil
	}
	return site.Store().Delete(context.Background(), key, storage.IgnoreMissing())
}
