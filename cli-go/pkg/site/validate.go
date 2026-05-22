// Package site provides CLI-compatible polis site operations.
// This package has ZERO webapp dependencies and can be used by standalone tools.
package site

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ValidationStatus represents the result of site validation.
type ValidationStatus string

const (
	StatusValid      ValidationStatus = "valid"
	StatusNotFound   ValidationStatus = "not_found"
	StatusIncomplete ValidationStatus = "incomplete"
	StatusInvalid    ValidationStatus = "invalid"
)

// ValidationError represents a specific validation error.
type ValidationError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Path       string `json:"path,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

// SiteInfo contains information about a valid polis site.
type SiteInfo struct {
	SiteTitle   string `json:"site_title,omitempty"`
	PublicKey   string `json:"public_key,omitempty"`
	Version     string `json:"version,omitempty"`
	Created     string `json:"created,omitempty"`
	ActiveTheme string `json:"active_theme,omitempty"`
}

// ValidationResult contains the full validation result.
type ValidationResult struct {
	Status   ValidationStatus  `json:"status"`
	Errors   []ValidationError `json:"errors,omitempty"`
	SiteInfo *SiteInfo         `json:"site_info,omitempty"`
}

// Validate checks if a directory is a valid polis site.
func Validate(siteDir string) *ValidationResult {
	result := &ValidationResult{
		Status: StatusValid,
		Errors: []ValidationError{},
	}

	// Check if directory exists
	info, err := os.Stat(siteDir)
	if os.IsNotExist(err) {
		result.Status = StatusNotFound
		result.Errors = append(result.Errors, ValidationError{
			Code:       "SITE_DIR_NOT_FOUND",
			Message:    "Site directory does not exist",
			Path:       siteDir,
			Suggestion: "Initialize a new site or link to an existing polis site",
		})
		return result
	}
	if err != nil {
		result.Status = StatusInvalid
		result.Errors = append(result.Errors, ValidationError{
			Code:    "SITE_DIR_ERROR",
			Message: "Cannot access site directory: " + err.Error(),
			Path:    siteDir,
		})
		return result
	}
	if !info.IsDir() {
		result.Status = StatusInvalid
		result.Errors = append(result.Errors, ValidationError{
			Code:       "NOT_A_DIRECTORY",
			Message:    "Path is not a directory",
			Path:       siteDir,
			Suggestion: "Provide a path to a directory, not a file",
		})
		return result
	}

	var errors []ValidationError

	// Check .well-known/polis
	wellKnownPath := filepath.Join(siteDir, ".well-known", "polis")
	wellKnown, wellKnownErr := validateWellKnown(wellKnownPath)
	if wellKnownErr != nil {
		errors = append(errors, *wellKnownErr)
	}

	// Check private key
	privKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519")
	if _, err := os.Stat(privKeyPath); os.IsNotExist(err) {
		errors = append(errors, ValidationError{
			Code:       "PRIVATE_KEY_MISSING",
			Message:    "Private key file not found",
			Path:       privKeyPath,
			Suggestion: "Initialize the site to generate keys, or restore your backed-up keys",
		})
	} else if err != nil {
		errors = append(errors, ValidationError{
			Code:    "PRIVATE_KEY_ERROR",
			Message: "Cannot access private key: " + err.Error(),
			Path:    privKeyPath,
		})
	}

	// Check public key
	pubKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub")
	pubKeyData, pubKeyErr := os.ReadFile(pubKeyPath)
	if os.IsNotExist(pubKeyErr) {
		errors = append(errors, ValidationError{
			Code:       "PUBLIC_KEY_MISSING",
			Message:    "Public key file not found",
			Path:       pubKeyPath,
			Suggestion: "Initialize the site to generate keys, or restore your backed-up keys",
		})
	} else if pubKeyErr != nil {
		errors = append(errors, ValidationError{
			Code:    "PUBLIC_KEY_ERROR",
			Message: "Cannot access public key: " + pubKeyErr.Error(),
			Path:    pubKeyPath,
		})
	}

	// Verify public key match
	if wellKnown != nil && pubKeyErr == nil && len(pubKeyData) > 0 {
		pubKeyStr := strings.TrimSpace(string(pubKeyData))
		wellKnownPubKey := strings.TrimSpace(wellKnown.PublicKey)
		if pubKeyStr != wellKnownPubKey {
			errors = append(errors, ValidationError{
				Code:       "PUBLIC_KEY_MISMATCH",
				Message:    "Public key in .well-known/polis does not match key file",
				Path:       wellKnownPath,
				Suggestion: "Update .well-known/polis with the correct public key, or regenerate the site",
			})
		}
	}

	// Check bundle structure. Listing-is-activation: every bundle present
	// in wellKnown.Bundles is active and must have a valid bundle.json.
	if wellKnown != nil {
		for name, entry := range wellKnown.Bundles {
			bundlePath := filepath.Join(siteDir, entry.Path)
			if _, err := os.Stat(bundlePath); os.IsNotExist(err) {
				errors = append(errors, ValidationError{
					Code:       "BUNDLE_MISSING",
					Message:    "Bundle file not found for " + name,
					Path:       bundlePath,
					Suggestion: "Create the bundle.json or remove the bundle from .well-known/polis",
				})
			}
		}
	}

	// Determine final status
	if len(errors) > 0 {
		result.Errors = errors
		hasWellKnown := wellKnown != nil
		hasMissingKeys := false
		for _, e := range errors {
			if e.Code == "PRIVATE_KEY_MISSING" || e.Code == "PUBLIC_KEY_MISSING" {
				hasMissingKeys = true
			}
		}
		if !hasWellKnown && hasMissingKeys {
			result.Status = StatusNotFound
		} else {
			result.Status = StatusIncomplete
		}
	} else {
		result.SiteInfo = &SiteInfo{
			SiteTitle:   wellKnown.SiteTitle,
			PublicKey:   wellKnown.PublicKey,
			Version:     wellKnown.Version,
			Created:     wellKnown.Created,
			ActiveTheme: GetActiveTheme(siteDir),
		}
	}

	// R18-12 (2026-05-18): scrub absolute paths from emitted errors.
	// Pre-fix, /api/status returned errors containing hosted-disk paths
	// like /data/tenants/<handle>/.well-known/polis — revealing the
	// hosted filesystem structure to any owner session (and any future
	// XSS-style attack against the SPA). Rewrite each Path to be
	// relative to siteDir. The Code + Message + Suggestion already
	// carry enough context to identify the file; the absolute path
	// added nothing the caller needed.
	//
	// SITE_DIR_NOT_FOUND / SITE_DIR_ERROR / NOT_A_DIRECTORY pre-empt
	// this with an early return; their Path is the siteDir itself,
	// which the caller already knows.
	scrubAbsolutePaths(result, siteDir)
	return result
}

// scrubAbsolutePaths rewrites Path fields in result.Errors to be
// relative to siteDir, dropping the absolute prefix. Idempotent;
// safe to call multiple times.
func scrubAbsolutePaths(result *ValidationResult, siteDir string) {
	if result == nil || siteDir == "" {
		return
	}
	for i := range result.Errors {
		p := result.Errors[i].Path
		if p == "" {
			continue
		}
		// Strip the siteDir prefix when present. If the path IS the
		// siteDir, leave it as "." so the caller sees that the error
		// targets the site root rather than an empty string.
		if p == siteDir {
			result.Errors[i].Path = "."
			continue
		}
		if rel, err := filepath.Rel(siteDir, p); err == nil && !strings.HasPrefix(rel, "..") {
			result.Errors[i].Path = rel
		}
	}
}

// validateWellKnown checks the .well-known/polis file.
func validateWellKnown(path string) (*WellKnown, *ValidationError) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, &ValidationError{
			Code:       "WELLKNOWN_MISSING",
			Message:    ".well-known/polis file not found",
			Path:       path,
			Suggestion: "Initialize the site to create .well-known/polis",
		}
	}
	if err != nil {
		return nil, &ValidationError{
			Code:    "WELLKNOWN_ERROR",
			Message: "Cannot read .well-known/polis: " + err.Error(),
			Path:    path,
		}
	}

	var wk WellKnown
	if err := json.Unmarshal(data, &wk); err != nil {
		return nil, &ValidationError{
			Code:       "WELLKNOWN_INVALID_JSON",
			Message:    ".well-known/polis contains invalid JSON: " + err.Error(),
			Path:       path,
			Suggestion: "Fix the JSON syntax or regenerate the file",
		}
	}

	if wk.PublicKey == "" {
		return nil, &ValidationError{
			Code:       "WELLKNOWN_MISSING_PUBKEY",
			Message:    ".well-known/polis is missing public_key field",
			Path:       path,
			Suggestion: "Add the public_key field with your site's public key",
		}
	}

	return &wk, nil
}

