// Package medic provides auto-remediation for patrol findings on polis tenant sites.
//
// It consumes patrol.CheckResult output and applies safe, reversible fixes:
// chmod corrections for permissions issues, and quarantine (move-to-airlock)
// for suspicious files. Non-correctable issues are counted and skipped.
package medic

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/patrol"
	"github.com/vdibart/polis-cli/cli-go/pkg/policy"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// FixAction describes a single remediation action taken (or planned).
type FixAction struct {
	Path   string `json:"path"`   // relative to siteDir
	Action string `json:"action"` // "chmod" or "quarantine"
	Detail string `json:"detail"` // e.g. "0755 → 0600" or "suspicious extension: .php"
}

// TenantResult holds the remediation outcome for a single tenant.
type TenantResult struct {
	Handle      string      `json:"handle"`
	Fixed       []FixAction `json:"fixed,omitempty"`
	Quarantined []FixAction `json:"quarantined,omitempty"`
	Provisioned []FixAction `json:"provisioned,omitempty"`
	Skipped     int         `json:"skipped"` // non-correctable failure count
}

// SweepResult holds the aggregated remediation outcome for all tenants.
type SweepResult struct {
	Tenants          int            `json:"tenants"`
	TotalFixed       int            `json:"total_fixed"`
	TotalQuarantined int            `json:"total_quarantined"`
	TotalProvisioned int            `json:"total_provisioned"`
	TotalSkipped     int            `json:"total_skipped"`
	Results          []TenantResult `json:"results"`
	DurationMs       int64          `json:"duration_ms"`
}

// ManifestEntry records a quarantined file for potential restoration.
type ManifestEntry struct {
	OriginalPath string      `json:"original_path"` // relative to siteDir
	Reason       string      `json:"reason"`
	Size         int64       `json:"size"`
	Mode         os.FileMode `json:"mode"`
	QuarantinedAt string    `json:"quarantined_at"`
}

// sensitiveDirs matches patrol's list.
var sensitiveDirs = []string{".polis", ".git"}

// nonCorrectableChecks are patrol check fields that we cannot auto-fix.
func countNonCorrectable(cr *patrol.CheckResult) int {
	count := 0
	checks := []bool{
		cr.WellKnown.OK,
		cr.PublicKey.OK,
		cr.PrivateKey.OK,
		cr.KeyMatch.OK,
		cr.KeyLeak.OK,
		cr.Structure.OK,
		cr.IndexJSONL.OK,
		cr.BlessedJSON.OK,
		cr.FollowingJSON.OK,
	}
	for _, ok := range checks {
		if !ok {
			count++
		}
	}
	count += cr.PostsFailed
	return count
}

// HealTenant applies safe fixes for a single tenant based on patrol results.
func HealTenant(siteDir, quarantineDir string, cr *patrol.CheckResult, dryRun bool) *TenantResult {
	result := &TenantResult{Handle: cr.Handle}

	// Count non-correctable issues
	result.Skipped = countNonCorrectable(cr)

	// Fix key permissions: chmod 0600
	if !cr.KeyPerms.OK {
		privKeyPath := filepath.Join(".polis", "keys", "id_ed25519")
		detail := fmt.Sprintf("%s → 0600", cr.KeyPerms.Message)
		if !dryRun {
			os.Chmod(filepath.Join(siteDir, privKeyPath), 0600)
		}
		result.Fixed = append(result.Fixed, FixAction{
			Path:   privKeyPath,
			Action: "chmod",
			Detail: detail,
		})
	}

	// Fix directory exposure: chmod 0700
	if !cr.DirExposure.OK {
		for _, dir := range sensitiveDirs {
			p := filepath.Join(siteDir, dir)
			info, err := os.Stat(p)
			if err != nil || !info.IsDir() {
				continue
			}
			mode := info.Mode().Perm()
			if mode&0007 != 0 {
				detail := fmt.Sprintf("%04o → 0700", mode)
				if !dryRun {
					os.Chmod(p, 0700)
				}
				result.Fixed = append(result.Fixed, FixAction{
					Path:   dir,
					Action: "chmod",
					Detail: detail,
				})
			}
		}
	}

	// Process suspicious files
	for _, sf := range cr.Suspicious {
		if strings.Contains(sf.Reason, "executable permissions") {
			// Strip execute bit
			fullPath := filepath.Join(siteDir, sf.Path)
			info, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			newMode := info.Mode().Perm() &^ 0111
			detail := fmt.Sprintf("%04o → %04o", info.Mode().Perm(), newMode)
			if !dryRun {
				os.Chmod(fullPath, newMode)
			}
			result.Fixed = append(result.Fixed, FixAction{
				Path:   sf.Path,
				Action: "chmod",
				Detail: detail,
			})
		} else {
			// Quarantine: symlinks, suspicious extensions, oversized files, unexpected ownership
			if !dryRun {
				quarantineFile(siteDir, quarantineDir, cr.Handle, sf.Path, sf.Reason)
			}
			result.Quarantined = append(result.Quarantined, FixAction{
				Path:   sf.Path,
				Action: "quarantine",
				Detail: sf.Reason,
			})
		}
	}

	// Provision missing public policies
	if !cr.PublicPolicies.OK {
		relPath := filepath.Join("policies", "rules.jsonl")
		if !dryRun {
			os.MkdirAll(filepath.Join(siteDir, "policies"), 0755)
			os.WriteFile(filepath.Join(siteDir, relPath), []byte(policy.DefaultPublicPolicyContent()), 0644)
		}
		result.Provisioned = append(result.Provisioned, FixAction{
			Path: relPath, Action: "provision", Detail: "created default public policies",
		})
	}

	// Provision missing private policies
	if !cr.PrivatePolicies.OK {
		relPath := filepath.Join(".polis", "policies", "rules.jsonl")
		if !dryRun {
			os.MkdirAll(filepath.Join(siteDir, ".polis", "policies"), 0700)
			os.WriteFile(filepath.Join(siteDir, relPath), []byte(policy.DefaultPrivatePolicyContent()), 0600)
		}
		result.Provisioned = append(result.Provisioned, FixAction{
			Path: relPath, Action: "provision", Detail: "created default private policies",
		})
	} else if !cr.DMPolicies.OK {
		// Private policies exist but missing DM rules
		relPath := filepath.Join(".polis", "policies", "rules.jsonl")
		if !dryRun {
			site.EnsureDMPolicies(filepath.Join(siteDir, relPath))
		}
		result.Provisioned = append(result.Provisioned, FixAction{
			Path: relPath, Action: "provision", Detail: "appended DM acceptance rules",
		})
	}

	// Ensure private policies have required rules (feed self-omit and deny-all catch-all).
	// These are appended to existing files in the correct order: feed-omit before deny-all.
	if cr.PrivatePolicies.OK {
		relPath := filepath.Join(".polis", "policies", "rules.jsonl")
		fullPath := filepath.Join(siteDir, relPath)

		if !cr.PolicyFeedSelfOmit.OK {
			if !dryRun {
				appendPolicyRule(fullPath, `{"active":true,"policy":"omit pub.polis.feed from self"}`)
			}
			result.Provisioned = append(result.Provisioned, FixAction{
				Path: relPath, Action: "provision", Detail: "appended feed self-omit rule",
			})
		}

		if !cr.PolicyDenyAll.OK {
			if !dryRun {
				appendPolicyRule(fullPath, `{"active":true,"policy":"deny all from all"}`)
			}
			result.Provisioned = append(result.Provisioned, FixAction{
				Path: relPath, Action: "provision", Detail: "appended default-deny catch-all",
			})
		}
	}

	// Provision storage salt
	if !cr.StorageSalt.OK {
		if !dryRun {
			dm.EnsureSalt(siteDir)
		}
		result.Provisioned = append(result.Provisioned, FixAction{
			Path: filepath.Join(".polis", "storage-salt"), Action: "provision", Detail: "created storage salt",
		})
	}

	// Provision DM directories or fix permissions
	if !cr.DMDirectories.OK {
		relPath := filepath.Join(".polis", "content", "pub.polis.core", "dm", "conv")
		fullPath := filepath.Join(siteDir, relPath)
		if strings.Contains(cr.DMDirectories.Message, "unsafe permissions") {
			// Item 4: Fix DM directory permissions
			if !dryRun {
				os.Chmod(fullPath, 0700)
			}
			result.Fixed = append(result.Fixed, FixAction{
				Path: relPath, Action: "chmod", Detail: cr.DMDirectories.Message + " → 0700",
			})
		} else {
			// Missing — create it
			if !dryRun {
				os.MkdirAll(fullPath, 0700)
			}
			result.Provisioned = append(result.Provisioned, FixAction{
				Path: relPath, Action: "provision", Detail: "created DM directories",
			})
		}
	}

	// Provision tag directory
	if !cr.TagDirectory.OK {
		relPath := filepath.Join("content", "pub.polis.core", "tag")
		if !dryRun {
			os.MkdirAll(filepath.Join(siteDir, relPath), 0755)
		}
		result.Provisioned = append(result.Provisioned, FixAction{
			Path: relPath, Action: "provision", Detail: "created tag directory",
		})
	}

	// Provision bundle.json
	if !cr.BundleJSON.OK && cr.BundleJSON.Message == "missing" {
		relPath := filepath.Join("content", "pub.polis.core", "bundle.json")
		if !dryRun {
			bundle.SaveBundle(filepath.Join(siteDir, relPath), bundle.DefaultCoreBundle())
		}
		result.Provisioned = append(result.Provisioned, FixAction{
			Path: relPath, Action: "provision", Detail: "created default bundle.json",
		})
	}

	// Normalize mixed-case domains in DM conversation files
	if !cr.DMDomainCase.OK {
		if !dryRun {
			healDMDomainCase(siteDir)
		}
		result.Fixed = append(result.Fixed, FixAction{
			Path:   filepath.Join(".polis", "content", "pub.polis.core", "dm"),
			Action: "normalize",
			Detail: "lowercased domains in DM conversations: " + cr.DMDomainCase.Message,
		})
	}

	// Encrypt plaintext DM previews at rest
	if !cr.DMPreviewEncryption.OK {
		relPath := filepath.Join(".polis", "content", "pub.polis.core", "dm", "conversations.json")
		if !dryRun {
			if err := healDMPreviewEncryption(siteDir); err != nil {
				result.Skipped++
			} else {
				result.Fixed = append(result.Fixed, FixAction{
					Path:   relPath,
					Action: "encrypt",
					Detail: "encrypted plaintext DM previews: " + cr.DMPreviewEncryption.Message,
				})
			}
		} else {
			result.Fixed = append(result.Fixed, FixAction{
				Path:   relPath,
				Action: "encrypt",
				Detail: "would encrypt plaintext DM previews: " + cr.DMPreviewEncryption.Message,
			})
		}
	}

	// Provision _base theme directory
	if !cr.BaseTheme.OK {
		relPath := filepath.Join("site", "themes", "_base")
		if !dryRun {
			provisionBaseTheme(siteDir)
		}
		result.Provisioned = append(result.Provisioned, FixAction{
			Path: relPath, Action: "provision", Detail: "installed _base theme directory",
		})
	}

	// Clean stale theme files that duplicate _base
	if !cr.ThemeStaleFiles.OK {
		baseDir := filepath.Join(siteDir, "site", "themes", "_base")
		themesDir := filepath.Join(siteDir, "site", "themes")
		if entries, err := os.ReadDir(themesDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() || entry.Name() == "_base" {
					continue
				}
				themeDir := filepath.Join(themesDir, entry.Name())
				staleFiles := patrol.StaleThemeFiles(themeDir, baseDir)
				for _, sf := range staleFiles {
					fullPath := filepath.Join(themeDir, sf)
					relPath := filepath.Join("site", "themes", entry.Name(), sf)
					if !dryRun {
						os.Remove(fullPath)
					}
					result.Fixed = append(result.Fixed, FixAction{
						Path:   relPath,
						Action: "remove_stale_theme_file",
						Detail: "_base duplicate",
					})
				}
				// Clean up empty snippets directory
				if !dryRun {
					snippetsDir := filepath.Join(themeDir, "snippets")
					if entries, err := os.ReadDir(snippetsDir); err == nil && len(entries) == 0 {
						os.Remove(snippetsDir)
					}
				}
			}
		}
	}

	// Provision missing avatar
	if !cr.Avatar.OK {
		relPath := filepath.Join(".well-known", "polis")
		if !dryRun {
			wk, err := site.LoadWellKnown(siteDir)
			if err == nil {
				wk.Avatar = site.GenerateDefaultAvatar()
				site.SaveWellKnown(siteDir, wk)
			}
		}
		result.Provisioned = append(result.Provisioned, FixAction{
			Path: relPath, Action: "provision", Detail: "generated default avatar",
		})
	}

	// Strip deprecated view_mode from webapp config
	if !cr.WebappViewMode.OK {
		relPath := filepath.Join(".polis", "webapp", "config.json")
		if !dryRun {
			stripWebappViewMode(filepath.Join(siteDir, relPath))
		}
		result.Fixed = append(result.Fixed, FixAction{
			Path: relPath, Action: "remove_key", Detail: "removed deprecated view_mode",
		})
	}

	// Upgrade bundle.json with missing content types
	if !cr.BundleTypes.OK {
		relPath := filepath.Join("content", "pub.polis.core", "bundle.json")
		fullPath := filepath.Join(siteDir, relPath)
		if !dryRun {
			if b, err := bundle.LoadBundle(fullPath); err == nil {
				b.MergeDefaults(bundle.DefaultCoreBundle())
				bundle.SaveBundle(fullPath, b)
			}
		}
		result.Provisioned = append(result.Provisioned, FixAction{
			Path: relPath, Action: "provision", Detail: "merged " + cr.BundleTypes.Message,
		})
	}

	return result
}

// HealAll runs patrol on all tenants and applies fixes.
func HealAll(tenantsDir, quarantineDir string, dryRun bool) *SweepResult {
	start := time.Now()
	sweep := patrol.CheckAllTenants(tenantsDir, patrol.CheckOptions{})

	result := &SweepResult{
		Tenants: sweep.Tenants,
	}

	for _, cr := range sweep.Results {
		tr := HealTenant(filepath.Join(tenantsDir, cr.Handle), quarantineDir, &cr, dryRun)
		result.TotalFixed += len(tr.Fixed)
		result.TotalQuarantined += len(tr.Quarantined)
		result.TotalProvisioned += len(tr.Provisioned)
		result.TotalSkipped += tr.Skipped
		result.Results = append(result.Results, *tr)
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result
}

// quarantineFile moves a file from the tenant site to the quarantine directory.
// Creates a timestamped batch dir and writes/appends to manifest.json.
func quarantineFile(siteDir, quarantineDir, handle, relPath, reason string) error {
	srcPath := filepath.Join(siteDir, relPath)

	// Get file info before moving
	info, err := os.Lstat(srcPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", relPath, err)
	}

	// Create batch directory: quarantineDir/handle/timestamp/
	timestamp := time.Now().UTC().Format(time.RFC3339)
	batchDir := filepath.Join(quarantineDir, handle, timestamp)
	filesDir := filepath.Join(batchDir, "files")
	if err := os.MkdirAll(filesDir, 0700); err != nil {
		return fmt.Errorf("mkdir quarantine: %w", err)
	}

	// Flatten path: content/hack.php → content__hack.php
	flatName := strings.ReplaceAll(relPath, string(filepath.Separator), "__")
	destPath := filepath.Join(filesDir, flatName)

	// Move file
	if err := os.Rename(srcPath, destPath); err != nil {
		return fmt.Errorf("move to quarantine: %w", err)
	}

	// Write/append manifest entry
	entry := ManifestEntry{
		OriginalPath:  relPath,
		Reason:        reason,
		Size:          info.Size(),
		Mode:          info.Mode(),
		QuarantinedAt: timestamp,
	}

	manifestPath := filepath.Join(batchDir, "manifest.json")
	var entries []ManifestEntry

	// Read existing manifest if present
	if data, err := os.ReadFile(manifestPath); err == nil {
		json.Unmarshal(data, &entries)
	}
	entries = append(entries, entry)

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	return nil
}

// healDMDomainCase normalizes mixed-case domains to lowercase in DM conversation
// files and the conversations index. This fixes conversation ID mismatches caused
// by case-varying domain names (DNS hostnames are case-insensitive per RFC 1123).
func healDMDomainCase(siteDir string) {
	dmBase := filepath.Join(siteDir, ".polis", "content", "pub.polis.core", "dm")
	convDir := filepath.Join(dmBase, "conv")

	entries, err := os.ReadDir(convDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") || entry.IsDir() {
			continue
		}
		path := filepath.Join(convDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var conv struct {
			PeerDomain string          `json:"peer_domain"`
			PeerURL    string          `json:"peer_url"`
			Messages   json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}

		changed := false

		// Normalize peer_domain
		lower := strings.ToLower(conv.PeerDomain)
		if lower != conv.PeerDomain {
			conv.PeerDomain = lower
			changed = true
		}

		// Normalize from/to in messages
		var messages []map[string]interface{}
		if err := json.Unmarshal(conv.Messages, &messages); err == nil {
			for i, msg := range messages {
				if from, ok := msg["from"].(string); ok {
					if lf := strings.ToLower(from); lf != from {
						messages[i]["from"] = lf
						changed = true
					}
				}
				if to, ok := msg["to"].(string); ok {
					if lt := strings.ToLower(to); lt != to {
						messages[i]["to"] = lt
						changed = true
					}
				}
			}
		}

		if !changed {
			continue
		}

		// Rebuild and write
		msgBytes, err := json.Marshal(messages)
		if err != nil {
			continue
		}
		out := map[string]interface{}{
			"peer_domain": conv.PeerDomain,
			"peer_url":    conv.PeerURL,
			"messages":    json.RawMessage(msgBytes),
		}
		outData, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			continue
		}
		outData = append(outData, '\n')
		os.WriteFile(path, outData, 0600)
	}

	// Also fix the conversations index
	idxPath := filepath.Join(dmBase, "conversations.json")
	idxData, err := os.ReadFile(idxPath)
	if err != nil {
		return
	}
	var idx struct {
		Conversations []map[string]interface{} `json:"conversations"`
	}
	if err := json.Unmarshal(idxData, &idx); err != nil {
		return
	}
	changed := false
	for i, c := range idx.Conversations {
		if pd, ok := c["peer_domain"].(string); ok {
			if lp := strings.ToLower(pd); lp != pd {
				idx.Conversations[i]["peer_domain"] = lp
				changed = true
			}
		}
	}
	if changed {
		outData, err := json.MarshalIndent(idx, "", "  ")
		if err != nil {
			return
		}
		outData = append(outData, '\n')
		os.WriteFile(idxPath, outData, 0600)
	}
}

// healDMPreviewEncryption encrypts any plaintext previews in the DM conversations
// index. Requires access to the tenant's private key to derive the storage key.
func healDMPreviewEncryption(siteDir string) error {
	privPEM, err := os.ReadFile(filepath.Join(siteDir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		return fmt.Errorf("read private key: %w", err)
	}
	privKey, err := signing.ParsePrivateKey(privPEM)
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}

	store, err := dm.NewStore(siteDir, privKey.Seed())
	if err != nil {
		return fmt.Errorf("create dm store: %w", err)
	}

	return store.EncryptPlaintextPreviews()
}

// provisionBaseTheme creates the _base theme directory with default templates.
// In a standalone binary context (no embedded FS), this creates a minimal set.
// In the hosted webapp, InstallThemes() handles this from the embedded FS.
func provisionBaseTheme(siteDir string) {
	baseDir := filepath.Join(siteDir, "site", "themes", "_base")
	snippetsDir := filepath.Join(baseDir, "snippets")
	os.MkdirAll(snippetsDir, 0755)

	// Minimal templates — the hosted binary's InstallThemes() provides the real
	// templates from the embedded FS. This is a fallback for standalone patrol/medic.
	templates := map[string]string{
		"index.html":          "<!DOCTYPE html><html><head><title>{{site_title}}</title><link rel=\"stylesheet\" href=\"styles.css\"></head><body>{{> theme:about}}{{#recent_posts}}{{> theme:post-item}}{{/recent_posts}}</body></html>",
		"post.html":           "<!DOCTYPE html><html><head><title>{{title}}</title><link rel=\"stylesheet\" href=\"{{css_path}}\"></head><body><article>{{content}}</article></body></html>",
		"posts.html":          "<!DOCTYPE html><html><head><title>All Posts</title><link rel=\"stylesheet\" href=\"../styles.css\"></head><body>{{#posts}}{{> theme:post-item}}{{/posts}}</body></html>",
		"comment.html":        "<!DOCTYPE html><html><head><title>Comment</title><link rel=\"stylesheet\" href=\"{{css_path}}\"></head><body><article>{{content}}</article></body></html>",
		"comment-inline.html": "<div class=\"comment\"><div class=\"comment-header\"><span class=\"comment-author\">{{author_name}}</span><span class=\"comment-date\">{{published_human}}</span></div><div class=\"comment-body\">{{content}}</div></div>",
		"tag.html":            "<!DOCTYPE html><html><head><title>Tag: {{tag_name}}</title><link rel=\"stylesheet\" href=\"{{css_path}}\"></head><body>{{#targets}}<div>{{uri}}</div>{{/targets}}</body></html>",
		"tag-index.html":      "<!DOCTYPE html><html><head><title>Tags</title><link rel=\"stylesheet\" href=\"{{css_path}}\"></head><body>{{#tags}}<div>{{tag_name}}</div>{{/tags}}</body></html>",
	}

	snippets := map[string]string{
		"about.html":           "<section class=\"about\"><div class=\"container\"><div class=\"about-content\">{{> global:about}}</div></div></section>",
		"also-reading.html":    "{{#following}}<a href=\"{{url}}\" class=\"also-reading-item\">{{author_name}}</a>{{/following}}",
		"blessed-comment.html": "<div class=\"comment\"><div class=\"comment-header\"><a href=\"{{url}}\" class=\"comment-author\">{{author_name}}</a><span class=\"comment-date\">{{published_human}}</span></div><div class=\"comment-body\">{{content}}</div></div>",
		"comment-item.html":    "<a href=\"{{url}}\" class=\"comment-item\"><span class=\"comment-meta\"><span class=\"comment-author\">{{target_author}}</span><span class=\"comment-date\">{{published_human}}</span></span><span class=\"comment-preview\">{{preview}}</span></a>",
		"polis-widget.html":    "<div id=\"polis-widget\" data-page-type=\"{{page_type}}\" data-post-url=\"{{url}}\" data-author=\"{{author_domain}}\"><noscript><a href=\"https://polis.pub/follow?author={{author_domain}}\">Follow {{author_name}}</a></noscript></div><script src=\"https://polis.pub/widget-{{widget_version}}.js\" crossorigin=\"anonymous\" defer></script>",
		"post-item.html":       "<a href=\"{{url}}\" class=\"post-item\"><span class=\"post-date\">{{published_human}}</span><span class=\"post-title\">{{title}} <span class=\"post-comments\">({{comment_count}} comments)</span></span></a>",
	}

	for name, content := range templates {
		path := filepath.Join(baseDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.WriteFile(path, []byte(content), 0644)
		}
	}

	for name, content := range snippets {
		path := filepath.Join(snippetsDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			os.WriteFile(path, []byte(content), 0644)
		}
	}
}

// appendPolicyRule appends a JSONL rule line to a policy file.
// Ensures the file ends with a newline before appending.
func appendPolicyRule(path, ruleLine string) error {
	// Read existing content to check if it ends with a newline
	existing, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	// Ensure there's a newline before our rule
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	_, err = f.WriteString(ruleLine + "\n")
	return err
}

// stripWebappViewMode removes the deprecated view_mode key from webapp config.
func stripWebappViewMode(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return
	}
	delete(obj, "view_mode")
	out, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return
	}
	out = append(out, '\n')
	os.WriteFile(configPath, out, 0644)
}
