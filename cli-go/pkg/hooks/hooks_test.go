package hooks

import (
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

	result, err := RunHook(dir, nil, payload)
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

	result, err := RunHook(dir, config, payload)
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

	result, err := RunHook(dir, nil, payload)
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

	path := GetHookPathWithDiscovery(nil, EventPostPublish, dir)
	if path != filepath.Join(".polis", "webapp", "hooks", "post-publish.sh") {
		t.Errorf("Expected conventional path, got %q", path)
	}

	// Non-existent event should return empty
	path = GetHookPathWithDiscovery(nil, EventPostRepublish, dir)
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

	_, err := RunHook(dir, config, payload)
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

	_, err := RunHook(dir, config, payload)
	if err == nil {
		t.Fatal("Expected error for absolute path outside site")
	}
	if !strings.Contains(err.Error(), "outside site directory") {
		t.Errorf("Expected 'outside site directory' error, got: %v", err)
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
	_, err := RunHook(dir, nil, payload)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Expected error from timeout")
	}
	// Should complete well under 60s (the hook's sleep time)
	if elapsed > 35*time.Second {
		t.Errorf("Hook took too long (%v), timeout may not be working", elapsed)
	}
}

