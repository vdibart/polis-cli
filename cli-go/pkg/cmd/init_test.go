package cmd

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInit_Basic(t *testing.T) {
	dir := t.TempDir()

	// Save and restore global state
	oldDataDir := dataDir
	oldJSON := jsonOutput
	defer func() {
		dataDir = oldDataDir
		jsonOutput = oldJSON
	}()
	dataDir = dir
	jsonOutput = false

	// Capture stdout
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	handleInit([]string{})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify .well-known/polis was created
	wellKnownPath := filepath.Join(dir, ".well-known", "polis")
	if _, err := os.Stat(wellKnownPath); errors.Is(err, fs.ErrNotExist) {
		t.Error("Expected .well-known/polis to be created")
	}

	// Verify keys were created
	privKeyPath := filepath.Join(dir, ".polis", "keys", "id_ed25519")
	if _, err := os.Stat(privKeyPath); errors.Is(err, fs.ErrNotExist) {
		t.Error("Expected .polis/keys/id_ed25519 to be created")
	}

	// Verify "Next steps" output mentions polis register
	if !strings.Contains(output, "polis register") {
		t.Error("Expected 'Next steps' to mention 'polis register'")
	}
	if !strings.Contains(output, "Deploy your site") {
		t.Error("Expected 'Next steps' to mention deploying the site")
	}
}

func TestInit_NoRegisterFlag(t *testing.T) {
	// Verify --register is not a recognized flag by checking help output
	// The init handler uses flag.ExitOnError, so we verify it's not defined
	// by checking the help text doesn't include it
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printUsage()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Check --register does NOT appear in init section
	// Find the init section and verify --register is absent
	initIdx := strings.Index(output, "polis init [options]")
	if initIdx == -1 {
		t.Fatal("Expected to find 'polis init [options]' in help")
	}
	initSection := output[initIdx:]
	// Cut at next command section
	if nextCmd := strings.Index(initSection[1:], "\n  polis "); nextCmd > 0 {
		initSection = initSection[:nextCmd+1]
	}
	if strings.Contains(initSection, "--register") {
		t.Error("--register should NOT appear in init help section")
	}
}
