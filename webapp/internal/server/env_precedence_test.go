package server

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// ⛔⛔ THE PROCESS ENVIRONMENT WINS OVER .env.
//
// A platform (Vercel, Fly) configures a process with environment variables and
// never writes a .env. LoadEnv used to read the FILE only, so a remote instance
// booted with no base URL: publishing built URLs from an empty string, and the
// owner-recovery challenge lost its cross-instance binding — "domain":"" —
// while every unit test passed, because they construct with an explicit domain.
//
// ⭐ The rule is not new: both CLIs already follow it (bash's load_env_file and
// the Go CLI's loadEnvFile set a key only if it is not already in the
// environment). The webapp was the one place that diverged.

func clearEnvKeys(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "") // ⚠️ the developer's own shell may export these
	}
}

// With NO .env at all — the platform case — every key still applies.
func TestLoadEnvHonoursTheProcessEnvironmentWithNoFile(t *testing.T) {
	clearEnvKeys(t)
	t.Setenv("POLIS_BASE_URL", "https://from-env.example/")
	t.Setenv("DISCOVERY_SERVICE_URL", "https://ds-from-env.example")
	t.Setenv("LOG_LEVEL", "2")
	t.Setenv("LOG_RETENTION_DAYS", "9")
	t.Chdir(t.TempDir()) // ⚠️ no .env in the working directory either
	t.Setenv("HOME", t.TempDir())

	s := newTestServer(t)
	s.LoadEnv()

	if s.BaseURL != "https://from-env.example" {
		t.Errorf("BaseURL = %q, want the process environment's (trailing slash trimmed)", s.BaseURL)
	}
	if s.DiscoveryURL != "https://ds-from-env.example" {
		t.Errorf("DiscoveryURL = %q", s.DiscoveryURL)
	}
	if s.LogLevel != 2 || !s.logLevelSet {
		t.Errorf("LogLevel = %d (set=%v), want 2", s.LogLevel, s.logLevelSet)
	}
	if s.LogRetentionDays != 9 {
		t.Errorf("LogRetentionDays = %d, want 9", s.LogRetentionDays)
	}
}

// Both set and different: the process environment wins, a line says so, and
// the line names the KEY and never either value.
func TestTheProcessEnvironmentWinsOverDotEnvAndSaysSo(t *testing.T) {
	clearEnvKeys(t)
	s := newTestServer(t)
	os.WriteFile(filepath.Join(s.DataDir, ".env"),
		[]byte("POLIS_BASE_URL=https://from-file.example\nDISCOVERY_SERVICE_URL=https://ds-file.example\n"), 0o600)
	t.Setenv("POLIS_BASE_URL", "https://from-env.example")

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	s.LoadEnv()

	if s.BaseURL != "https://from-env.example" {
		t.Errorf("BaseURL = %q, want the process environment to win", s.BaseURL)
	}
	// ⭐ A key only in the file still comes from the file.
	if s.DiscoveryURL != "https://ds-file.example" {
		t.Errorf("DiscoveryURL = %q, want the .env value when the environment has none", s.DiscoveryURL)
	}
	out := buf.String()
	if !strings.Contains(out, "POLIS_BASE_URL is set in both") {
		t.Errorf("no line naming the conflict; log was:\n%s", out)
	}
	if strings.Contains(out, "from-env.example") || strings.Contains(out, "from-file.example") {
		t.Errorf("⛔ the log carries a VALUE; it must name only the key:\n%s", out)
	}
}

// The other direction: unset in the environment, the file is honoured — so an
// existing laptop install behaves exactly as before.
func TestDotEnvStillAppliesWhenTheEnvironmentIsSilent(t *testing.T) {
	clearEnvKeys(t)
	s := newTestServer(t)
	os.WriteFile(filepath.Join(s.DataDir, ".env"), []byte("POLIS_BASE_URL=https://from-file.example\n"), 0o600)

	s.LoadEnv()

	if s.BaseURL != "https://from-file.example" {
		t.Errorf("BaseURL = %q, want the .env value", s.BaseURL)
	}
}

// ⛔⛔ WITH AN OPERATOR, the process environment is shared by every tenant, so
// it must never override a site's own setting — one POLIS_BASE_URL in
// polis.pub's environment would otherwise re-point every tenant at one domain.
func TestWithAnOperatorTheSitesOwnDotEnvIsTheOnlySource(t *testing.T) {
	clearEnvKeys(t)
	s := newTestServer(t)
	s.Mode = mode.Custodied
	os.WriteFile(filepath.Join(s.DataDir, ".env"), []byte("POLIS_BASE_URL=https://alice.polis.pub\n"), 0o600)
	t.Setenv("POLIS_BASE_URL", "https://operator-wide.example")
	t.Setenv("DISCOVERY_SERVICE_URL", "https://operator-ds.example")

	s.LoadEnv()

	if s.BaseURL != "https://alice.polis.pub" {
		t.Errorf("BaseURL = %q: the operator's environment overrode the tenant's own site", s.BaseURL)
	}
	if s.DiscoveryURL == "https://operator-ds.example" {
		t.Error("DiscoveryURL came from the operator's environment, not the tenant's .env")
	}
}
