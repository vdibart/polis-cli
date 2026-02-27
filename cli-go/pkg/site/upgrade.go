package site

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

// KnownFields lists all recognized .well-known/polis field paths.
// true = canonical/current field, false = known but deprecated.
var KnownFields = map[string]bool{
	// Canonical fields (bash CLI)
	"version":                       true,
	"author":                        true,
	"email":                         true,
	"public_key":                    true,
	"site_title":                    true,
	"domain":                        false, // Use POLIS_BASE_URL env var instead
	"created":                       true,
	"config":                        true,
	"config.directories":            true,
	"config.directories.keys":       true,
	"config.directories.posts":      true,
	"config.directories.comments":   true,
	"config.directories.snippets":   true,
	"config.directories.themes":     true,
	"config.directories.versions":   true,
	"config.files":                  true,
	"config.files.public_index":     true,
	"config.files.blessed_comments": true,
	"config.files.following_index":  true,
	// Deprecated fields (removed by upgrade, logged as removable)
	"base_url":        false, // Use POLIS_BASE_URL env var instead (matches bash CLI)
	"subdomain":       false, // Derived from POLIS_BASE_URL at runtime
	"created_at":      false, // Migrated to canonical "created" field
	"public_key_path": false, // Public key content is in "public_key"
	"generator":       false, // Informational, not required for functionality
}

// UpgradeWellKnown checks if .well-known/polis needs upgrading and performs
// graceful migration. Returns the upgraded WellKnown struct.
func UpgradeWellKnown(siteDir string) (*WellKnown, error) {
	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return nil, err
	}

	upgraded := false

	// Migrate created_at to created, or set to current time if neither exists
	if wk.Created == "" {
		if wk.CreatedAt != "" {
			wk.Created = wk.CreatedAt
			wk.CreatedAt = "" // Clear deprecated field
			log.Printf("[upgrade] Migrated created_at to created")
		} else {
			wk.Created = time.Now().UTC().Format(time.RFC3339)
			log.Printf("[upgrade] Added created timestamp: %s", wk.Created)
		}
		upgraded = true
	}

	// Add missing author from git config
	if wk.Author == "" {
		if author := getGitConfig("user.name"); author != "" {
			wk.Author = author
			log.Printf("[upgrade] Added author from git config: %s", author)
			upgraded = true
		}
	}

	// Add missing email from git config
	if wk.Email == "" {
		if email := getGitConfig("user.email"); email != "" {
			wk.Email = email
			log.Printf("[upgrade] Added email from git config: %s", email)
			upgraded = true
		}
	}

	// Add missing config section
	if wk.Config == nil {
		wk.Config = &WellKnownConfig{
			Directories: WellKnownDirectories{
				Keys:     ".polis/keys",
				Posts:    "posts",
				Comments: "comments",
				Snippets: "snippets",
				Themes:   ".polis/themes",
				Versions: ".versions", // Directory name only; path constructed at runtime
			},
			Files: WellKnownFiles{
				PublicIndex:     "metadata/public.jsonl",
				BlessedComments: "metadata/blessed-comments.json",
				FollowingIndex:  "metadata/following.json",
			},
		}
		log.Printf("[upgrade] Added config section with default paths")
		upgraded = true
	}

	// Clear deprecated fields that have been migrated
	if wk.PublicKeyPath != "" {
		wk.PublicKeyPath = ""
		upgraded = true
	}
	if wk.Generator != "" {
		wk.Generator = ""
		upgraded = true
	}

	// Remove domain (runtime config, should use POLIS_BASE_URL env var instead)
	if wk.Domain != "" {
		log.Printf("[upgrade] Removing domain (use POLIS_BASE_URL env var instead)")
		wk.Domain = ""
		upgraded = true
	}

	// Remove base_url (runtime config, should use POLIS_BASE_URL env var instead)
	if wk.BaseURL != "" {
		log.Printf("[upgrade] Removing base_url (use POLIS_BASE_URL env var instead)")
		wk.BaseURL = ""
		upgraded = true
	}

	// Remove subdomain (derived from POLIS_BASE_URL at runtime)
	if wk.Subdomain != "" {
		log.Printf("[upgrade] Removing subdomain (derived from POLIS_BASE_URL)")
		wk.Subdomain = ""
		upgraded = true
	}

	if upgraded {
		if err := SaveWellKnown(siteDir, wk); err != nil {
			return nil, err
		}
		log.Printf("[upgrade] Saved upgraded .well-known/polis")
	}

	return wk, nil
}

// CheckUnrecognizedFields loads raw JSON and logs any unrecognized fields.
func CheckUnrecognizedFields(siteDir string) {
	wellKnownPath := filepath.Join(siteDir, ".well-known", "polis")
	data, err := os.ReadFile(wellKnownPath)
	if err != nil {
		return
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}

	checkFields(raw, "", func(path string) {
		if known, exists := KnownFields[path]; !exists {
			log.Printf("[wellknown] Unrecognized field '%s' - may be safe to remove", path)
		} else if !known {
			log.Printf("[wellknown] Deprecated field '%s' - safe to remove after upgrade", path)
		}
	})
}

// checkFields recursively checks all fields in the JSON.
func checkFields(obj map[string]interface{}, prefix string, report func(string)) {
	for key, value := range obj {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		report(path)

		// Recurse into nested objects
		if nested, ok := value.(map[string]interface{}); ok {
			checkFields(nested, path, report)
		}
	}
}
