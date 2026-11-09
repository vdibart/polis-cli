package hooks

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunHook_AutoDiscover(t *testing.T) {
	dir := t.TempDir()

	// Create conventional hook location
	hookDir := filepath.Join(dir, ".polis", "webapp", "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(hookDir, "post-publish.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho hook-fired\n"), 0755); err != nil {
		t.Fatal(err)
	}

	payload := &HookPayload{
		Event:   EventPostPublish,
		Path:    "posts/20250101/test.md",
		Title:   "Test Post",
		Version: "1",
	}

	result, err := RunHook(storage.At(dir), nil, payload)
	if err != nil {
		t.Fatalf("RunHook failed: %v", err)
	}
	if !result.Executed {
		t.Error("Expected hook to be executed via auto-discovery")
	}
	if result.Output == "" {
		t.Error("Expected hook output")
	}
}

func TestRunHook_ExplicitOverridesConvention(t *testing.T) {
	dir := t.TempDir()

	// Create conventional hook (should NOT be called)
	hookDir := filepath.Join(dir, ".polis", "webapp", "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		t.Fatal(err)
	}
	conventionalPath := filepath.Join(hookDir, "post-publish.sh")
	if err := os.WriteFile(conventionalPath, []byte("#!/bin/sh\necho conventional\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Create explicit hook at a custom path
	customDir := filepath.Join(dir, "my-hooks")
	if err := os.MkdirAll(customDir, 0755); err != nil {
		t.Fatal(err)
	}
	explicitPath := filepath.Join(customDir, "publish.sh")
	if err := os.WriteFile(explicitPath, []byte("#!/bin/sh\necho explicit\n"), 0755); err != nil {
		t.Fatal(err)
	}

	config := &HookConfig{
		PostPublish: "my-hooks/publish.sh",
	}

	payload := &HookPayload{
		Event:   EventPostPublish,
		Path:    "posts/20250101/test.md",
		Title:   "Test Post",
		Version: "1",
	}

	result, err := RunHook(storage.At(dir), config, payload)
	if err != nil {
		t.Fatalf("RunHook failed: %v", err)
	}
	if !result.Executed {
		t.Error("Expected hook to be executed")
	}
	if result.Output != "explicit\n" {
		t.Errorf("Expected explicit hook output, got %q", result.Output)
	}
}

func TestRunHook_NilConfigNoConventionalFile(t *testing.T) {
	dir := t.TempDir()

	payload := &HookPayload{
		Event:   EventPostPublish,
		Path:    "posts/20250101/test.md",
		Title:   "Test Post",
		Version: "1",
	}

	result, err := RunHook(storage.At(dir), nil, payload)
	if err != nil {
		t.Fatalf("RunHook should not error: %v", err)
	}
	if result.Executed {
		t.Error("Expected hook to NOT be executed when no config and no conventional file")
	}
}

func TestGetHookPathWithDiscovery_AutoDiscover(t *testing.T) {
	dir := t.TempDir()

	// Create conventional hook
	hookDir := filepath.Join(dir, ".polis", "webapp", "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "post-publish.sh"), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	path := GetHookPathWithDiscovery(nil, EventPostPublish, storage.At(dir))
	if path != filepath.Join(".polis", "webapp", "hooks", "post-publish.sh") {
		t.Errorf("Expected conventional path, got %q", path)
	}

	// Non-existent event should return empty
	path = GetHookPathWithDiscovery(nil, EventPostRepublish, storage.At(dir))
	if path != "" {
		t.Errorf("Expected empty for non-existent hook, got %q", path)
	}
}

func TestRunHook_PathContainment(t *testing.T) {
	dir := t.TempDir()

	config := &HookConfig{
		PostPublish: "../../etc/passwd",
	}
	payload := &HookPayload{
		Event: EventPostPublish,
		Path:  "posts/20250101/test.md",
		Title: "Test Post",
	}

	_, err := RunHook(storage.At(dir), config, payload)
	if err == nil {
		t.Fatal("Expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "outside site directory") {
		t.Errorf("Expected 'outside site directory' error, got: %v", err)
	}
}

func TestRunHook_AbsolutePathOutsideSite(t *testing.T) {
	dir := t.TempDir()

	config := &HookConfig{
		PostPublish: "/usr/bin/true",
	}
	payload := &HookPayload{
		Event: EventPostPublish,
		Path:  "posts/20250101/test.md",
		Title: "Test Post",
	}

	_, err := RunHook(storage.At(dir), config, payload)
	if err == nil {
		t.Fatal("Expected error for absolute path outside site")
	}
	if !strings.Contains(err.Error(), "outside site directory") {
		t.Errorf("Expected 'outside site directory' error, got: %v", err)
	}
}

// writeScript writes an executable script that leaves a marker file behind, so
// a test can tell whether it ran rather than trusting the returned error.
func writeScript(t *testing.T, path, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0755); err != nil {
		t.Fatal(err)
	}
}

func ran(marker string) bool {
	_, err := os.Stat(marker)
	return err == nil
}

// The containment check compares paths, and a symlink's own path is inside the
// site while the file it names is not. Both ways a hook is found are covered.
func TestRunHook_SymlinkOutsideSiteIsRefused(t *testing.T) {
	payload := &HookPayload{Event: EventPostPublish, Path: "posts/20250101/test.md", Title: "Test Post"}

	cases := []struct {
		name   string
		link   string // relative to the site
		config func(link string) *HookConfig
	}{
		{"conventional path", filepath.Join(".polis", "webapp", "hooks", "post-publish.sh"), func(string) *HookConfig { return nil }},
		{"configured path", filepath.Join("scripts", "deploy.sh"), func(link string) *HookConfig { return &HookConfig{PostPublish: link} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			outside := t.TempDir()
			marker := filepath.Join(outside, "ran")
			target := filepath.Join(outside, "evil.sh")
			writeScript(t, target, marker)

			link := filepath.Join(dir, tc.link)
			if err := os.MkdirAll(filepath.Dir(link), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}

			_, err := RunHook(storage.At(dir), tc.config(tc.link), payload)
			if err == nil || !strings.Contains(err.Error(), "outside site directory") {
				t.Errorf("want an 'outside site directory' error, got: %v", err)
			}
			if ran(marker) {
				t.Error("the script outside the site was executed")
			}
		})
	}
}

// A directory inside the site that links out is the same escape one level up.
func TestRunHook_SymlinkedDirectoryOutsideSiteIsRefused(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	marker := filepath.Join(outside, "ran")
	writeScript(t, filepath.Join(outside, "post-publish.sh"), marker)

	if err := os.MkdirAll(filepath.Join(dir, ".polis", "webapp"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".polis", "webapp", "hooks")); err != nil {
		t.Fatal(err)
	}

	_, err := RunHook(storage.At(dir), nil, &HookPayload{Event: EventPostPublish})
	if err == nil || !strings.Contains(err.Error(), "outside site directory") {
		t.Errorf("want an 'outside site directory' error, got: %v", err)
	}
	if ran(marker) {
		t.Error("the script outside the site was executed")
	}
}

func TestRunHook_SymlinkInsideSiteRuns(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	writeScript(t, filepath.Join(dir, "scripts", "deploy.sh"), marker)

	hookDir := filepath.Join(dir, ".polis", "webapp", "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "scripts", "deploy.sh"), filepath.Join(hookDir, "post-publish.sh")); err != nil {
		t.Fatal(err)
	}

	result, err := RunHook(storage.At(dir), nil, &HookPayload{Event: EventPostPublish})
	if err != nil {
		t.Fatalf("RunHook failed: %v", err)
	}
	if !result.Executed || !ran(marker) {
		t.Error("a hook linked to a script inside the site did not run")
	}
}

// The site directory is resolved too, or a site reached through a link (macOS
// puts every temp dir behind one) would refuse all of its own hooks.
func TestRunHook_SiteDirReachedThroughSymlink(t *testing.T) {
	realSite := t.TempDir()
	marker := filepath.Join(realSite, "ran")
	writeScript(t, filepath.Join(realSite, ".polis", "webapp", "hooks", "post-publish.sh"), marker)

	linkedSite := filepath.Join(t.TempDir(), "site")
	if err := os.Symlink(realSite, linkedSite); err != nil {
		t.Fatal(err)
	}

	result, err := RunHook(storage.At(linkedSite), nil, &HookPayload{Event: EventPostPublish})
	if err != nil {
		t.Fatalf("RunHook failed: %v", err)
	}
	if !result.Executed || !ran(marker) {
		t.Error("the hook of a symlinked site directory did not run")
	}
}

func TestRunHook_DirectoryIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0755); err != nil {
		t.Fatal(err)
	}

	_, err := RunHook(storage.At(dir), &HookConfig{PostPublish: "scripts"}, &HookPayload{Event: EventPostPublish})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("want a 'not a regular file' error, got: %v", err)
	}
}

func TestRunHook_MissingHookIsNotFound(t *testing.T) {
	dir := t.TempDir()

	_, err := RunHook(storage.At(dir), &HookConfig{PostPublish: "scripts/gone.sh"}, &HookPayload{Event: EventPostPublish})
	if err == nil || !strings.Contains(err.Error(), "hook not found") {
		t.Errorf("want a 'hook not found' error, got: %v", err)
	}
}

func TestRunHook_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}

	dir := t.TempDir()

	// Create a hook that sleeps for 60 seconds
	hookDir := filepath.Join(dir, ".polis", "webapp", "hooks")
	os.MkdirAll(hookDir, 0755)
	scriptPath := filepath.Join(hookDir, "post-publish.sh")
	os.WriteFile(scriptPath, []byte("#!/bin/sh\nsleep 60\n"), 0755)

	payload := &HookPayload{
		Event: EventPostPublish,
		Path:  "posts/20250101/test.md",
		Title: "Test Post",
	}

	start := time.Now()
	_, err := RunHook(storage.At(dir), nil, payload)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Expected error from timeout")
	}
	// Should complete well under 60s (the hook's sleep time)
	if elapsed > 35*time.Second {
		t.Errorf("Hook took too long (%v), timeout may not be working", elapsed)
	}
}
