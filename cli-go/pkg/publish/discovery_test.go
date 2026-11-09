package publish

import (
	"encoding/json"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

func TestRegisterPost_NotConfigured(t *testing.T) {
	// Reset config
	oldURL, oldBase := DiscoveryURL, BaseURL
	defer func() { DiscoveryURL, BaseURL = oldURL, oldBase }()

	DiscoveryURL = ""
	BaseURL = ""

	result := &PublishResult{
		Success: true,
		Path:    "posts/20260201/test.md",
		Title:   "Test Post",
		Version: "sha256:abc123",
	}

	// Should silently return nil when not configured
	err := RegisterPost(storage.At(t.TempDir()), result, nil, nil)
	if err != nil {
		t.Errorf("expected nil error when not configured, got: %v", err)
	}
}

func TestRegisterPost_NoEmailButHasBaseURL(t *testing.T) {
	oldURL, oldBase := DiscoveryURL, BaseURL
	defer func() { DiscoveryURL, BaseURL = oldURL, oldBase }()

	DiscoveryURL = "https://discovery.example.com"
	BaseURL = "https://test.polis.pub"

	dataDir := t.TempDir()
	discovery.WriteRegistrationMarker(storage.At(dataDir), DiscoveryURL, "test.polis.pub", "")

	// Create .well-known/polis WITHOUT email — author derived from BaseURL
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)
	wk := map[string]interface{}{
		"public_key": "ssh-ed25519 AAAA...",
	}
	data, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), data, 0644)

	result := &PublishResult{
		Success: true,
		Path:    "posts/20260201/test.md",
		Title:   "Test Post",
		Version: "sha256:abc123",
	}

	// Will fail at signing (nil key) but should NOT fail at author resolution
	// because domain is derived from BaseURL
	err := RegisterPost(storage.At(dataDir), result, nil, nil)
	if err == nil {
		t.Error("expected error (nil private key), got nil")
	}
	// The error should be about signing, not about missing author
	if strings.Contains(err.Error(), "no author identity") {
		t.Errorf("should have used BaseURL domain as author, but got: %v", err)
	}
}

func TestRegisterPost_BaseURLDomainFallback(t *testing.T) {
	oldURL, oldBase := DiscoveryURL, BaseURL
	defer func() { DiscoveryURL, BaseURL = oldURL, oldBase }()

	DiscoveryURL = "https://discovery.example.com"
	BaseURL = "https://alice.polis.pub"

	dataDir := t.TempDir()
	discovery.WriteRegistrationMarker(storage.At(dataDir), DiscoveryURL, "alice.polis.pub", "")

	// Create .well-known/polis without email — domain from BaseURL
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)
	wk := map[string]interface{}{
		"public_key": "ssh-ed25519 AAAA...",
	}
	data, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), data, 0644)

	result := &PublishResult{
		Success: true,
		Path:    "posts/20260201/test.md",
		Title:   "Test Post",
		Version: "sha256:abc123",
	}

	// Will fail at signing (nil key) but should NOT fail at author resolution
	err := RegisterPost(storage.At(dataDir), result, nil, nil)
	if err == nil {
		t.Error("expected error (nil private key), got nil")
	}
	// The error should be about signing, not about missing author
	if strings.Contains(err.Error(), "no author identity") {
		t.Errorf("should have used BaseURL domain as author, but got: %v", err)
	}
}

func TestRegisterPost_EmailPreferredOverDomain(t *testing.T) {
	oldURL, oldBase := DiscoveryURL, BaseURL
	defer func() { DiscoveryURL, BaseURL = oldURL, oldBase }()

	DiscoveryURL = "https://discovery.example.com"
	BaseURL = "https://alice.polis.pub"

	dataDir := t.TempDir()
	discovery.WriteRegistrationMarker(storage.At(dataDir), DiscoveryURL, "alice.polis.pub", "")

	// Create .well-known/polis with email (email preferred over BaseURL-derived domain)
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)
	wk := map[string]interface{}{
		"public_key": "ssh-ed25519 AAAA...",
		"email":      "alice@example.com",
	}
	data, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), data, 0644)

	result := &PublishResult{
		Success: true,
		Path:    "posts/20260201/test.md",
		Title:   "Test Post",
		Version: "sha256:abc123",
	}

	// Will fail at signing (nil key) but should NOT fail at author resolution
	err := RegisterPost(storage.At(dataDir), result, nil, nil)
	if err == nil {
		t.Error("expected error (nil private key), got nil")
	}
	// The error should be about signing, not about missing author
	if strings.Contains(err.Error(), "no author identity") {
		t.Errorf("should have used email as author, but got: %v", err)
	}
}

func TestRegisterPost_PartialConfig(t *testing.T) {
	oldURL, oldBase := DiscoveryURL, BaseURL
	defer func() { DiscoveryURL, BaseURL = oldURL, oldBase }()

	tests := []struct {
		name string
		url  string
		base string
	}{
		{"no URL", "", "https://test.polis.pub"},
		{"no base URL", "https://discovery.example.com", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			DiscoveryURL = tt.url
			BaseURL = tt.base

			err := RegisterPost(storage.At(t.TempDir()), &PublishResult{}, nil, nil)
			if err != nil {
				t.Errorf("expected nil when partially configured, got: %v", err)
			}
		})
	}
}

func TestRegisterPost_WithExplicitConfig(t *testing.T) {
	// Ensure package globals are empty — the explicit config should be used
	oldURL, oldBase := DiscoveryURL, BaseURL
	defer func() { DiscoveryURL, BaseURL = oldURL, oldBase }()

	DiscoveryURL = ""
	BaseURL = ""

	cfg := &DiscoveryConfig{
		DiscoveryURL: "https://ds.polis.pub",
		BaseURL:      "https://alice.polis.pub",
	}

	dataDir := t.TempDir()
	discovery.WriteRegistrationMarker(storage.At(dataDir), cfg.DiscoveryURL, "alice.polis.pub", "")

	// Create .well-known/polis with email
	os.MkdirAll(filepath.Join(dataDir, ".well-known"), 0755)
	wk := map[string]interface{}{
		"public_key": "ssh-ed25519 AAAA...",
		"email":      "alice@example.com",
	}
	data, _ := json.MarshalIndent(wk, "", "  ")
	os.WriteFile(filepath.Join(dataDir, ".well-known", "polis"), data, 0644)

	result := &PublishResult{
		Success: true,
		Path:    "posts/20260201/test.md",
		Title:   "Test Post",
		Version: "sha256:abc123",
	}

	// This will attempt to reach ds.polis.pub which won't work in tests,
	// but it should NOT silently skip (which would happen if globals were used)
	err := RegisterPost(storage.At(dataDir), result, nil, cfg)
	// We expect a signing error (nil privateKey) or network error — not nil
	if err == nil {
		t.Error("expected error when using explicit config (can't sign with nil key), got nil")
	}
}
