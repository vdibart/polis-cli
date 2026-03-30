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
	Domain       string `json:"domain"`
	DSURL        string `json:"ds_url"`
	RegisteredAt string `json:"registered_at"`
}

// registrationMarkerPath returns the path to the registration marker file
// for a given DS URL: .polis/ds/{ds-domain}/registered.json
func registrationMarkerPath(dataDir, dsURL string) string {
	dsDomain := ExtractDomainFromURL(dsURL)
	if dsDomain == "" {
		return ""
	}
	return filepath.Join(dataDir, ".polis", "ds", dsDomain, "registered.json")
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
func WriteRegistrationMarker(dataDir, dsURL, siteDomain string) error {
	path := registrationMarkerPath(dataDir, dsURL)
	if path == "" {
		return fmt.Errorf("could not determine marker path (empty dsURL?)")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create marker directory: %w", err)
	}

	marker := RegistrationMarker{
		Domain:       siteDomain,
		DSURL:        dsURL,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
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

// migrationCheckedPath returns the path to the one-time migration flag file.
func migrationCheckedPath(dataDir, dsURL string) string {
	dsDomain := ExtractDomainFromURL(dsURL)
	if dsDomain == "" {
		return ""
	}
	return filepath.Join(dataDir, ".polis", "ds", dsDomain, "registration_checked")
}

// MigrateRegistrationState is a one-time migration for sites that registered
// before the local marker file was introduced. On first run, it calls
// CheckSiteRegistration on the DS. If the site is registered, it writes the
// marker file. A flag file prevents repeating the check on subsequent startups.
//
// This is the one allowed DS network call for registration state — it runs
// once per site per DS and is a no-op after that.
func MigrateRegistrationState(dataDir, dsURL, baseURL string) {
	if dataDir == "" || dsURL == "" || baseURL == "" {
		return
	}
	if IsRegisteredLocally(dataDir, dsURL) {
		return // already has marker
	}

	checkedPath := migrationCheckedPath(dataDir, dsURL)
	if checkedPath == "" {
		return
	}
	if _, err := os.Stat(checkedPath); err == nil {
		return // already checked
	}

	domain := ExtractDomainFromURL(baseURL)
	if domain == "" {
		return
	}

	client := NewClient(dsURL, "")
	result, err := client.CheckSiteRegistration(domain)
	if err == nil && result.IsRegistered {
		if err := WriteRegistrationMarker(dataDir, dsURL, domain); err != nil {
			fmt.Printf("[!] Migration: could not write registration marker: %v\n", err)
		} else {
			fmt.Printf("[i] Migration: site already registered with DS, marker file created\n")
		}
	}

	// Write checked-flag regardless of result to avoid repeating
	os.MkdirAll(filepath.Dir(checkedPath), 0700)
	os.WriteFile(checkedPath, []byte("1"), 0600)
}
