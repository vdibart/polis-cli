// Package site provides CLI-compatible polis site operations.
// This package has ZERO webapp dependencies and can be used by standalone tools.
package site

import (
	"context"
	"encoding/json"
	goerrors "errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
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
func Validate(site storage.Site) *ValidationResult {
	result := &ValidationResult{
		Status: StatusValid,
		Errors: []ValidationError{},
	}

	// A zero handle names no site at all, which is the not-found case.
	if !site.Valid() {
		result.Status = StatusNotFound
		result.Errors = append(result.Errors, ValidationError{
			Code:       "SITE_DIR_NOT_FOUND",
			Message:    "Site directory does not exist",
			Suggestion: "Initialize a new site or link to an existing polis site",
		})
		return result
	}

	// ⭐ Whether the site ITSELF exists is asked only where the site HAS a root
	// directory (validateRoot says why it is not a store question). On a store
	// with no filesystem there is no directory to be missing, and a site with
	// nothing in it reports WELLKNOWN_MISSING below.
	if root := siteRoot(site); root != "" {
		if r := validateRoot(root); r != nil {
			return r
		}
	}

	var errors []ValidationError

	// ⭐ KEYS, NOT PATHS, and the diagnostics report the key.
	//
	// ⚠️ THE OUTPUT IS UNCHANGED BY THIS. scrubAbsolutePaths already rewrote
	// every Path to be relative to the site root — it exists for a security
	// reason (R18-12: an absolute Path leaked /data/tenants/<handle>/… to any
	// owner session) — so a key IS what the caller already saw. ⭐⭐ The scrub
	// stays as defence in depth rather than being deleted: it is idempotent,
	// and it is the control that catches an absolute path a future edit adds.
	const wellKnownKey = ".well-known/polis"
	wellKnown, wellKnownErr := validateWellKnown(site, wellKnownKey)
	if wellKnownErr != nil {
		errors = append(errors, *wellKnownErr)
	}

	// Check private key
	const privKeyKey = ".polis/keys/id_ed25519"
	// ⚠️ The error is checked FIRST. This used to test a stale err left over from
	// the root check, which was always nil here, so an unreadable key reported
	// PRIVATE_KEY_MISSING and the error branch never ran.
	if present, err := site.Store().Exists(context.Background(), privKeyKey); err != nil {
		errors = append(errors, ValidationError{
			Code:    "PRIVATE_KEY_ERROR",
			Message: "Cannot access private key: " + err.Error(),
			Path:    privKeyKey,
		})
	} else if !present {
		errors = append(errors, ValidationError{
			Code:       "PRIVATE_KEY_MISSING",
			Message:    "Private key file not found",
			Path:       privKeyKey,
			Suggestion: "Initialize the site to generate keys, or restore your backed-up keys",
		})
	}

	// Check public key
	const pubKeyKey = ".polis/keys/id_ed25519.pub"
	pubKeyData, pubKeyErr := site.Store().Get(context.Background(), pubKeyKey)
	if goerrors.Is(pubKeyErr, fs.ErrNotExist) {
		errors = append(errors, ValidationError{
			Code:       "PUBLIC_KEY_MISSING",
			Message:    "Public key file not found",
			Path:       pubKeyKey,
			Suggestion: "Initialize the site to generate keys, or restore your backed-up keys",
		})
	} else if pubKeyErr != nil {
		errors = append(errors, ValidationError{
			Code:    "PUBLIC_KEY_ERROR",
			Message: "Cannot access public key: " + pubKeyErr.Error(),
			Path:    pubKeyKey,
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
				Path:       wellKnownKey,
				Suggestion: "Update .well-known/polis with the correct public key, or regenerate the site",
			})
		}
	}

	// Check bundle structure. Listing-is-activation: every bundle present
	// in wellKnown.Bundles is active and must have a valid bundle.json.
	if wellKnown != nil {
		for name, entry := range wellKnown.Bundles {
			bundleKey := strings.TrimPrefix(filepath.ToSlash(entry.Path), "/")
			if present, _ := site.Store().Exists(context.Background(), bundleKey); !present {
				errors = append(errors, ValidationError{
					Code:       "BUNDLE_MISSING",
					Message:    "Bundle file not found for " + name,
					Path:       bundleKey,
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
			ActiveTheme: GetActiveTheme(site),
		}
	}

	// R18-12 (2026-05-18): scrub absolute paths from emitted errors.
	// Pre-fix, /api/status returned errors containing hosted-disk paths
	// like /data/tenants/<handle>/.well-known/polis — revealing the
	// hosted filesystem structure to any owner session (and any future
	// XSS-style attack against the SPA). Rewrite each Path to be
	// relative to the site root. The Code + Message + Suggestion already
	// carry enough context to identify the file; the absolute path
	// added nothing the caller needed.
	//
	// SITE_DIR_NOT_FOUND / SITE_DIR_ERROR / NOT_A_DIRECTORY pre-empt
	// this with an early return; their Path is the site root itself,
	// which the caller already knows.
	scrubAbsolutePaths(result, site)
	return result
}

// siteRoot is the site's root directory on a filesystem, or "" when its store
// has none.
func siteRoot(site storage.Site) string {
	st := site.Store()
	if !st.Capabilities().RealPath {
		return ""
	}
	root, err := st.RealPath("")
	if err != nil {
		return ""
	}
	return root
}

// validateRoot reports a site root that is missing, unreadable or not a
// directory, or nil when it is a directory. Its Path is the directory itself:
// these errors return before scrubAbsolutePaths, and the caller named it.
func validateRoot(root string) *ValidationResult {
	result := &ValidationResult{Status: StatusValid, Errors: []ValidationError{}}
	// NOT the storage interface: this asks whether THE SITE ITSELF exists, and a
	// site store cannot answer a question about its own root — there is nothing
	// below it to address.
	info, err := os.Stat(root)
	if goerrors.Is(err, fs.ErrNotExist) {
		result.Status = StatusNotFound
		result.Errors = append(result.Errors, ValidationError{
			Code:       "SITE_DIR_NOT_FOUND",
			Message:    "Site directory does not exist",
			Path:       root,
			Suggestion: "Initialize a new site or link to an existing polis site",
		})
		return result
	}
	if err != nil {
		result.Status = StatusInvalid
		result.Errors = append(result.Errors, ValidationError{
			Code:    "SITE_DIR_ERROR",
			Message: "Cannot access site directory: " + err.Error(),
			Path:    root,
		})
		return result
	}
	if !info.IsDir() {
		result.Status = StatusInvalid
		result.Errors = append(result.Errors, ValidationError{
			Code:       "NOT_A_DIRECTORY",
			Message:    "Path is not a directory",
			Path:       root,
			Suggestion: "Provide a path to a directory, not a file",
		})
		return result
	}
	return nil
}

// scrubAbsolutePaths rewrites Path fields in result.Errors to be
// relative to the site root, dropping the absolute prefix. Idempotent;
// safe to call multiple times.
func scrubAbsolutePaths(result *ValidationResult, site storage.Site) {
	root := ""
	if site.Valid() {
		root = siteRoot(site)
	}
	if result == nil || root == "" {
		return
	}
	for i := range result.Errors {
		p := result.Errors[i].Path
		if p == "" {
			continue
		}
		// Strip the site root prefix when present. If the path IS the
		// the site root, leave it as "." so the caller sees that the error
		// targets the site root rather than an empty string.
		if p == root {
			result.Errors[i].Path = "."
			continue
		}
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			result.Errors[i].Path = rel
		}
	}
}

// validateWellKnown checks the .well-known/polis file.
func validateWellKnown(site storage.Site, key string) (*WellKnown, *ValidationError) {
	data, err := site.Store().Get(context.Background(), key)
	if goerrors.Is(err, fs.ErrNotExist) {
		return nil, &ValidationError{
			Code:       "WELLKNOWN_MISSING",
			Message:    ".well-known/polis file not found",
			Path:       key,
			Suggestion: "Initialize the site to create .well-known/polis",
		}
	}
	if err != nil {
		return nil, &ValidationError{
			Code:    "WELLKNOWN_ERROR",
			Message: "Cannot read .well-known/polis: " + err.Error(),
			Path:    key,
		}
	}

	var wk WellKnown
	if err := json.Unmarshal(data, &wk); err != nil {
		return nil, &ValidationError{
			Code:       "WELLKNOWN_INVALID_JSON",
			Message:    ".well-known/polis contains invalid JSON: " + err.Error(),
			Path:       key,
			Suggestion: "Fix the JSON syntax or regenerate the file",
		}
	}

	if wk.PublicKey == "" {
		return nil, &ValidationError{
			Code:       "WELLKNOWN_MISSING_PUBKEY",
			Message:    ".well-known/polis is missing public_key field",
			Path:       key,
			Suggestion: "Add the public_key field with your site's public key",
		}
	}

	return &wk, nil
}
