package site

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// Version is set at startup by the cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// InitOptions contains options for initializing a new polis site.
type InitOptions struct {
	SiteTitle string // Optional site title
	Author    string // Optional author name (falls back to git config user.name)
	Email     string // Optional email address — private by default, only written if explicitly provided
	Theme     string // Optional initial theme (default: empty, selected randomly on first render)
	Generator string // e.g. "polis-cli-go/0.59.0" — used in metadata; falls back to package default if empty
}

// InitResult contains the result of site initialization.
type InitResult struct {
	Success      bool     `json:"success"`
	SiteDir      string   `json:"site_dir"`
	PublicKey    string   `json:"public_key"`
	DirsCreated  []string `json:"directories_created,omitempty"`
	FilesCreated []string `json:"files_created,omitempty"`
	KeyPaths     struct {
		Private string `json:"private"`
		Public  string `json:"public"`
	} `json:"key_paths"`
	Error string `json:"error,omitempty"`
}

// Init creates a new polis site with the bundle-based layout.
// This function has no webapp dependencies and can be used by CLI tools.
// It will NOT overwrite existing keys or .well-known/polis — safety feature.
func Init(siteDir string, opts InitOptions) (*InitResult, error) {
	gen := opts.Generator
	if gen == "" {
		gen = GetGenerator()
	}

	result := &InitResult{
		Success: false,
		SiteDir: siteDir,
	}

	// SAFETY: Check if keys already exist — refuse to overwrite
	privKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519")
	pubKeyPath := filepath.Join(siteDir, ".polis", "keys", "id_ed25519.pub")
	wellKnownPath := filepath.Join(siteDir, ".well-known", "polis")

	if _, err := os.Stat(privKeyPath); err == nil {
		return nil, fmt.Errorf("private key already exists at %s — refusing to overwrite", privKeyPath)
	}
	if _, err := os.Stat(pubKeyPath); err == nil {
		return nil, fmt.Errorf("public key already exists at %s — refusing to overwrite", pubKeyPath)
	}
	if _, err := os.Stat(wellKnownPath); err == nil {
		return nil, fmt.Errorf(".well-known/polis already exists at %s — refusing to overwrite", wellKnownPath)
	}

	// Create directory structure
	dirs := []string{
		siteDir,
		// Identity
		filepath.Join(siteDir, ".well-known"),
		// Private state
		filepath.Join(siteDir, ".polis"),
		filepath.Join(siteDir, ".polis", "keys"),
		filepath.Join(siteDir, ".polis", "logs"),
		filepath.Join(siteDir, ".polis", "policies"),
		filepath.Join(siteDir, ".polis", "webapp"),
		filepath.Join(siteDir, ".polis", "webapp", "hooks"),
		// Private content state (pub.polis.core)
		filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "posts", "drafts"),
		filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "comments", "drafts"),
		filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "comments", "pending"),
		filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "comments", "denied"),
		// DM storage (encrypted conversations)
		filepath.Join(siteDir, ".polis", "bundles", "pub.polis.core", "dm", "conversations"),
		// Site resources
		filepath.Join(siteDir, "site", "snippets"),
		// Public content (pub.polis.core)
		filepath.Join(siteDir, "content", "pub.polis.core", "post"),
		filepath.Join(siteDir, "content", "pub.polis.core", "comment"),
		filepath.Join(siteDir, "content", "pub.polis.core", "follow"),
		filepath.Join(siteDir, "content", "pub.polis.core", "tag"),
		// Policies
		filepath.Join(siteDir, "policies"),
	}

	var dirsCreated []string
	for _, dir := range dirs {
		// .polis directory and its children use restrictive permissions (private data)
		perm := os.FileMode(0755)
		rel, _ := filepath.Rel(siteDir, dir)
		if rel == ".polis" || strings.HasPrefix(rel, ".polis"+string(filepath.Separator)) {
			perm = 0700
		}
		if err := os.MkdirAll(dir, perm); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
		if rel != "." {
			dirsCreated = append(dirsCreated, rel)
		}
	}
	result.DirsCreated = dirsCreated

	// Generate keypair
	privKey, pubKey, err := signing.GenerateKeypair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate keypair: %w", err)
	}

	if err := os.WriteFile(privKeyPath, privKey, 0600); err != nil {
		return nil, fmt.Errorf("failed to write private key: %w", err)
	}
	if err := os.WriteFile(pubKeyPath, pubKey, 0644); err != nil {
		return nil, fmt.Errorf("failed to write public key: %w", err)
	}

	result.KeyPaths.Private = ".polis/keys/id_ed25519"
	result.KeyPaths.Public = ".polis/keys/id_ed25519.pub"

	// Create storage salt for DM encryption
	if err := dm.EnsureSalt(siteDir); err != nil {
		return nil, fmt.Errorf("failed to create storage salt: %w", err)
	}

	// Create .well-known/polis
	setupTime := time.Now().UTC()
	author := opts.Author
	if author == "" {
		author = getGitConfig("user.name")
	}

	// Default site title to author name when not supplied — avoids
	// rendered <title></title> + leading-space description artifacts
	// on fresh tenants where the operator didn't set --site-title.
	siteTitle := opts.SiteTitle
	if siteTitle == "" {
		siteTitle = author
	}

	wk := &WellKnown{
		Version:    gen,
		AuthorName: author,
		Email:      opts.Email,
		PublicKey:  strings.TrimSpace(string(pubKey)),
		SiteTitle:  siteTitle,
		Created:    setupTime.Format(time.RFC3339),
		// active_theme intentionally not set here — moved to
		// .polis/bundles/registry.json (step-01/1e).
		Bundles: map[string]BundleEntry{
			"pub.polis.core": {
				Path: "content/pub.polis.core/bundle.json",
			},
		},
	}

	wk.Avatar = GenerateDefaultAvatar()

	if err := SaveWellKnown(siteDir, wk); err != nil {
		return nil, fmt.Errorf("failed to create .well-known/polis: %w", err)
	}

	// Provision the DM keyring (bootstrap epoch) and publish its signed
	// public_key_messages block into .well-known/polis, so the site can receive
	// encrypted DMs from message #1. Medic performs the same on existing tenants.
	if err := ProvisionAndPublishMessagesKey(siteDir, privKey); err != nil {
		return nil, fmt.Errorf("failed to provision DM keys: %w", err)
	}

	// Generate favicon from avatar config
	if err := WriteFavicon(siteDir); err != nil {
		return nil, fmt.Errorf("failed to create favicon.svg: %w", err)
	}

	// Create bundle.json
	var filesCreated []string
	coreBundle := bundle.DefaultCoreBundle()
	bundlePath := filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json")
	if err := bundle.SaveBundle(bundlePath, coreBundle); err != nil {
		return nil, fmt.Errorf("failed to create bundle.json: %w", err)
	}
	filesCreated = append(filesCreated, "content/pub.polis.core/bundle.json")

	// Install the reference bundle payload (shapes, themes) into .polis/bundles/.
	// This makes a brand-new site immediately renderable without any background
	// actors (Patrol/Medic/Tailor). EnsureReferencePayload also writes
	// registry.json with per-shape/per-theme version stamps that drive
	// Patrol's NeedsRefresh check.
	if err := bundle.EnsureReferencePayload(siteDir, "pub.polis.core"); err != nil {
		return nil, fmt.Errorf("failed to install bundle reference payload: %w", err)
	}
	filesCreated = append(filesCreated, ".polis/bundles/pub.polis.core/")
	filesCreated = append(filesCreated, ".polis/bundles/registry.json")

	// Set active theme on the registry that EnsureReferencePayload just stamped.
	// Both branches use load+modify+save (via bundle.SetActiveThemeName /
	// theme.SelectRandomTheme → SetActiveTheme → bundle.SetActiveThemeName)
	// so the version stamps are preserved.
	if opts.Theme != "" {
		if err := bundle.SetActiveThemeName(siteDir, opts.Theme); err != nil {
			return nil, fmt.Errorf("failed to set active theme: %w", err)
		}
	} else {
		// No theme requested — pick a random one from the installed bundle and
		// persist it so the registry is fully initialized after init (rather
		// than half-initialized until first render).
		if _, err := theme.SelectRandomTheme(siteDir, ""); err != nil {
			return nil, fmt.Errorf("failed to select default theme: %w", err)
		}
	}

	// Create content files
	if err := initContentFiles(siteDir, &filesCreated, gen); err != nil {
		return nil, fmt.Errorf("failed to create content files: %w", err)
	}

	// Create default about snippet
	aboutPath := filepath.Join(siteDir, "site", "snippets", "about.md")
	if _, err := os.Stat(aboutPath); os.IsNotExist(err) {
		aboutContent := "Hi \u2014 I'm just getting set up here. A real about page is coming soon.\n\nThis site runs on *polis*: signed markdown on my own domain. No platform, no middleman.\n"
		if err := os.WriteFile(aboutPath, []byte(aboutContent), 0644); err != nil {
			return nil, fmt.Errorf("failed to create default about snippet: %w", err)
		}
		filesCreated = append(filesCreated, "site/snippets/about.md")
	}

	// Create default public policy file (DM rules + blessing emit rules + catch-all deny)
	policyPath := filepath.Join(siteDir, "policies", "rules.jsonl")
	if _, err := os.Stat(policyPath); os.IsNotExist(err) {
		if err := os.WriteFile(policyPath, []byte(policy.DefaultPublicPolicyContent()), 0644); err != nil {
			return nil, fmt.Errorf("failed to create default policy file: %w", err)
		}
		filesCreated = append(filesCreated, "policies/rules.jsonl")
	}

	// Create default private policy file (empty — overrides only)
	privatePolicyPath := filepath.Join(siteDir, ".polis", "policies", "rules.jsonl")
	if _, err := os.Stat(privatePolicyPath); os.IsNotExist(err) {
		if err := os.WriteFile(privatePolicyPath, []byte(policy.DefaultPrivatePolicyContent()), 0600); err != nil {
			return nil, fmt.Errorf("failed to create default private policy file: %w", err)
		}
		filesCreated = append(filesCreated, ".polis/policies/rules.jsonl")
	}

	// Create webapp config
	webappConfigPath := filepath.Join(siteDir, ".polis", "webapp", "config.json")
	if _, err := os.Stat(webappConfigPath); os.IsNotExist(err) {
		webappConfig := map[string]interface{}{
			"setup_at":         setupTime.Format(time.RFC3339),
			"show_frontmatter": false,
		}
		data, _ := json.MarshalIndent(webappConfig, "", "  ")
		data = append(data, '\n')
		if err := os.WriteFile(webappConfigPath, data, 0644); err != nil {
			return nil, fmt.Errorf("failed to create webapp config: %w", err)
		}
		filesCreated = append(filesCreated, ".polis/webapp/config.json")
	}

	// Add well-known and key files to the list
	filesCreated = append(filesCreated, ".well-known/polis")
	filesCreated = append(filesCreated, result.KeyPaths.Private, result.KeyPaths.Public)
	result.FilesCreated = filesCreated

	// Create .gitignore
	if err := initGitignore(siteDir); err != nil {
		fmt.Printf("warning: failed to create .gitignore: %v\n", err)
	}

	result.Success = true
	result.PublicKey = strings.TrimSpace(string(pubKey))
	return result, nil
}

// EnsureDMPolicies appends default DM acceptance policies to the private rules file
// if no pub.polis.dm rules exist yet. This is idempotent — safe to call on every init.
func EnsureDMPolicies(privatePolicyPath string) error {
	dmRules := `{"active":true,"policy":"allow pub.polis.dm from following"}` + "\n" +
		`{"active":true,"policy":"deny pub.polis.dm from all"}` + "\n"

	// Check if DM rules already exist
	if data, err := os.ReadFile(privatePolicyPath); err == nil {
		if strings.Contains(string(data), "pub.polis.dm") {
			return nil // DM rules already present
		}
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(privatePolicyPath), 0700); err != nil {
		return err
	}

	// Append DM rules
	f, err := os.OpenFile(privatePolicyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(dmRules)
	return err
}

// initContentFiles creates the initial content files for pub.polis.core.
func initContentFiles(siteDir string, filesCreated *[]string, gen string) error {
	// Create empty content index
	indexPath := filepath.Join(siteDir, "content", "pub.polis.core", "index.jsonl")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		if err := os.WriteFile(indexPath, []byte{}, 0644); err != nil {
			return err
		}
		*filesCreated = append(*filesCreated, "content/pub.polis.core/index.jsonl")
	}

	// Create following.json
	followingPath := filepath.Join(siteDir, "content", "pub.polis.core", "follow", "following.json")
	if _, err := os.Stat(followingPath); os.IsNotExist(err) {
		following := map[string]interface{}{
			"version":   gen,
			"following": []interface{}{},
		}
		data, _ := json.MarshalIndent(following, "", "  ")
		data = append(data, '\n')
		if err := os.WriteFile(followingPath, data, 0644); err != nil {
			return err
		}
		*filesCreated = append(*filesCreated, "content/pub.polis.core/follow/following.json")
	}

	// Create blessed.json
	blessedPath := filepath.Join(siteDir, "content", "pub.polis.core", "comment", "blessed.json")
	if _, err := os.Stat(blessedPath); os.IsNotExist(err) {
		blessed := map[string]interface{}{
			"version":  gen,
			"comments": []interface{}{},
		}
		data, _ := json.MarshalIndent(blessed, "", "  ")
		data = append(data, '\n')
		if err := os.WriteFile(blessedPath, data, 0644); err != nil {
			return err
		}
		*filesCreated = append(*filesCreated, "content/pub.polis.core/comment/blessed.json")
	}

	return nil
}

// initGitignore creates the .gitignore file for the new layout.
func initGitignore(siteDir string) error {
	gitignorePath := filepath.Join(siteDir, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		content := `# Polis internals and secrets
.polis/
.env*

# Binaries
/polis
/polis-server
/polis-full
`
		return os.WriteFile(gitignorePath, []byte(content), 0644)
	}
	return nil
}

// getGitConfig retrieves a git config value.
// Returns empty string if git is not available or the config key is not set.
func getGitConfig(key string) string {
	cmd := exec.Command("git", "config", key)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
