package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintUsage(t *testing.T) {
	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printUsage()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify essential commands are listed
	expectedCommands := []string{
		"init",
		"render",
		"post",
		"republish",
		"comment",
		"preview",
		"extract",
		"blessing",
		"follow",
		"unfollow",
		"discover",
		"notifications",
		"register",
		"unregister",
		"clone",
		"rebuild",
		"index",
		"version",
		"about",
		"rotate-key",
		"serve",
	}

	for _, cmd := range expectedCommands {
		if !strings.Contains(output, cmd) {
			t.Errorf("Expected usage to contain command %q", cmd)
		}
	}

	// Verify global flags are mentioned
	if !strings.Contains(output, "--data-dir") {
		t.Error("Expected usage to mention --data-dir flag")
	}
	if !strings.Contains(output, "--json") {
		t.Error("Expected usage to mention --json flag")
	}

	// Verify init options are listed
	initOptions := []string{
		"--site-title",
		"--keys-dir",
		"--posts-dir",
		"--comments-dir",
		"--snippets-dir",
		"--versions-dir",
	}
	for _, opt := range initOptions {
		if !strings.Contains(output, opt) {
			t.Errorf("Expected usage to contain init option %q", opt)
		}
	}

	// Regression: --register must NOT appear in help text
	if strings.Contains(output, "--register") {
		t.Error("--register should not appear in help text (removed from init)")
	}
}

func TestGetDataDirDefault(t *testing.T) {
	// Save original dataDir
	oldDataDir := dataDir
	defer func() { dataDir = oldDataDir }()

	dataDir = ""
	cwd, _ := os.Getwd()
	result := getDataDir()

	if result != cwd {
		t.Errorf("Expected getDataDir() to return cwd %q, got %q", cwd, result)
	}
}

func TestGetDataDirOverride(t *testing.T) {
	// Save original dataDir
	oldDataDir := dataDir
	defer func() { dataDir = oldDataDir }()

	dataDir = "/custom/path"
	result := getDataDir()

	if result != "/custom/path" {
		t.Errorf("Expected getDataDir() to return %q, got %q", "/custom/path", result)
	}
}

func TestLoadEnvFile_Basic(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	os.WriteFile(envFile, []byte("TEST_LOAD_ENV_A=hello\nTEST_LOAD_ENV_B=world\n"), 0644)

	// Clear env vars first
	os.Unsetenv("TEST_LOAD_ENV_A")
	os.Unsetenv("TEST_LOAD_ENV_B")
	defer os.Unsetenv("TEST_LOAD_ENV_A")
	defer os.Unsetenv("TEST_LOAD_ENV_B")

	loaded := loadEnvFile(envFile)
	if !loaded {
		t.Fatal("expected loadEnvFile to return true")
	}
	if got := os.Getenv("TEST_LOAD_ENV_A"); got != "hello" {
		t.Errorf("TEST_LOAD_ENV_A = %q, want %q", got, "hello")
	}
	if got := os.Getenv("TEST_LOAD_ENV_B"); got != "world" {
		t.Errorf("TEST_LOAD_ENV_B = %q, want %q", got, "world")
	}
}

func TestLoadEnvFile_NoOverride(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	os.WriteFile(envFile, []byte("TEST_LOAD_ENV_C=from-file\n"), 0644)

	os.Setenv("TEST_LOAD_ENV_C", "from-env")
	defer os.Unsetenv("TEST_LOAD_ENV_C")

	loadEnvFile(envFile)
	if got := os.Getenv("TEST_LOAD_ENV_C"); got != "from-env" {
		t.Errorf("TEST_LOAD_ENV_C = %q, want %q (should not override)", got, "from-env")
	}
}

func TestLoadEnvFile_QuotedValues(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	os.WriteFile(envFile, []byte("TEST_LOAD_ENV_D=\"quoted value\"\nTEST_LOAD_ENV_E='single quoted'\n"), 0644)

	os.Unsetenv("TEST_LOAD_ENV_D")
	os.Unsetenv("TEST_LOAD_ENV_E")
	defer os.Unsetenv("TEST_LOAD_ENV_D")
	defer os.Unsetenv("TEST_LOAD_ENV_E")

	loadEnvFile(envFile)
	if got := os.Getenv("TEST_LOAD_ENV_D"); got != "quoted value" {
		t.Errorf("TEST_LOAD_ENV_D = %q, want %q", got, "quoted value")
	}
	if got := os.Getenv("TEST_LOAD_ENV_E"); got != "single quoted" {
		t.Errorf("TEST_LOAD_ENV_E = %q, want %q", got, "single quoted")
	}
}

func TestLoadEnvFile_CommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	os.WriteFile(envFile, []byte("# comment\n\nTEST_LOAD_ENV_F=value\n# another comment\n"), 0644)

	os.Unsetenv("TEST_LOAD_ENV_F")
	defer os.Unsetenv("TEST_LOAD_ENV_F")

	loadEnvFile(envFile)
	if got := os.Getenv("TEST_LOAD_ENV_F"); got != "value" {
		t.Errorf("TEST_LOAD_ENV_F = %q, want %q", got, "value")
	}
}

func TestLoadEnvFile_Missing(t *testing.T) {
	loaded := loadEnvFile("/nonexistent/.env")
	if loaded {
		t.Error("expected loadEnvFile to return false for missing file")
	}
}

func TestGenerateRequestID_ValidUUIDv4(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := generateRequestID()

		// UUIDv4 format: 8-4-4-4-12 hex chars
		parts := strings.Split(id, "-")
		if len(parts) != 5 {
			t.Fatalf("expected 5 dash-separated parts, got %d: %q", len(parts), id)
		}
		expectedLens := []int{8, 4, 4, 4, 12}
		for j, part := range parts {
			if len(part) != expectedLens[j] {
				t.Errorf("part %d: expected %d chars, got %d in %q", j, expectedLens[j], len(part), id)
			}
		}

		// Version nibble (char index 0 of 3rd segment) must be '4'
		if parts[2][0] != '4' {
			t.Errorf("expected version nibble '4', got %q in %q", string(parts[2][0]), id)
		}

		// Variant nibble (char index 0 of 4th segment) must be 8, 9, a, or b
		v := parts[3][0]
		if v != '8' && v != '9' && v != 'a' && v != 'b' {
			t.Errorf("expected variant nibble in [89ab], got %q in %q", string(v), id)
		}
	}

	// Uniqueness: two calls should not produce the same ID
	a := generateRequestID()
	b := generateRequestID()
	if a == b {
		t.Errorf("two consecutive generateRequestID() calls returned the same value: %q", a)
	}
}

func TestLogCLIAction_WritesJSON(t *testing.T) {
	// Save and restore global state
	oldLogFile := cliLogFile
	oldRequestID := requestID
	defer func() {
		cliLogFile = oldLogFile
		requestID = oldRequestID
	}()

	tmpFile := filepath.Join(t.TempDir(), "cli.log")
	f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cliLogFile = f
	requestID = "test-request-id-1234"

	logCLIAction("post", map[string]interface{}{
		"args_count": 2,
		"extra":      "value",
	})

	f.Close()

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(data))
	if line == "" {
		t.Fatal("expected log line, got empty file")
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		t.Fatalf("log line is not valid JSON: %v\nline: %s", err, line)
	}

	// Check required fields
	if obj["source"] != "cli" {
		t.Errorf("expected source=cli, got %v", obj["source"])
	}
	if obj["action"] != "post" {
		t.Errorf("expected action=post, got %v", obj["action"])
	}
	if obj["request_id"] != "test-request-id-1234" {
		t.Errorf("expected request_id=test-request-id-1234, got %v", obj["request_id"])
	}
	if obj["ts"] == nil || obj["ts"] == "" {
		t.Error("expected ts field to be present")
	}
	// Check custom fields were merged
	if obj["extra"] != "value" {
		t.Errorf("expected extra=value, got %v", obj["extra"])
	}
}

func TestLogCLIAction_NoOpWhenLogFileNil(t *testing.T) {
	// Save and restore global state
	oldLogFile := cliLogFile
	defer func() { cliLogFile = oldLogFile }()

	cliLogFile = nil

	// Should not panic or write anything
	logCLIAction("post", map[string]interface{}{"key": "val"})
}

func TestOutputJSON_InjectsRequestID(t *testing.T) {
	// Save and restore global state
	oldRequestID := requestID
	defer func() { requestID = oldRequestID }()
	requestID = "injected-uuid-5678"

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	data := map[string]interface{}{
		"status":  "success",
		"command": "version",
	}
	outputJSON(data)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, buf.String())
	}

	if result["request_id"] != "injected-uuid-5678" {
		t.Errorf("expected request_id=injected-uuid-5678, got %v", result["request_id"])
	}
	// Original fields should still be present
	if result["status"] != "success" {
		t.Errorf("expected status=success, got %v", result["status"])
	}
	if result["command"] != "version" {
		t.Errorf("expected command=version, got %v", result["command"])
	}
}
