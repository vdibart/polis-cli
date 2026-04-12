package discovery

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
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

// registrationMarkerPath returns the path to the registration marker file
// for a given DS URL: .polis/ds/{ds-domain}/registration.json
func registrationMarkerPath(dataDir, dsURL string) string {
	dsDomain := ExtractDomainFromURL(dsURL)
	if dsDomain == "" {
		return ""
	}
	return filepath.Join(dataDir, ".polis", "ds", dsDomain, "registration.json")
}

// IsRegisteredLocally checks whether the site has a local registration marker
// for the given discovery service. This is a pure filesystem check — no network
// call is made. Returns false if dataDir or dsURL are empty, if the marker file
// does not exist, or on any error.
func IsRegisteredLocally(dataDir, dsURL string) bool {
	if dataDir == "" || dsURL == "" {
		return false
	}
	path := registrationMarkerPath(dataDir, dsURL)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// WriteRegistrationMarker writes the registration marker file after a
// successful registration with the discovery service.
func WriteRegistrationMarker(dataDir, dsURL, siteDomain, attestation string) error {
	path := registrationMarkerPath(dataDir, dsURL)
	if path == "" {
		return fmt.Errorf("could not determine marker path (empty dsURL?)")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create marker directory: %w", err)
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

	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}

	return nil
}

// ReadRegistrationMarker reads and parses a registration marker file.
// Returns nil and no error if the file does not exist.
func ReadRegistrationMarker(path string) (*RegistrationMarker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
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
func RemoveRegistrationMarker(dataDir, dsURL string) error {
	path := registrationMarkerPath(dataDir, dsURL)
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

