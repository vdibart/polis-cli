package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// .polis/webapp/config.json is UNSIGNED, so a settings save PRESERVES what this
// build does not model — another build's setting must not vanish the next time
// this one saves. The golden was written by the pre-preservation SaveConfig.

func seedWebappConfig(t *testing.T, fixture string) (*Server, string, []byte) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".polis", "webapp", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	s := &Server{DataDir: dir}
	s.LoadConfig()
	if s.Config == nil {
		t.Fatal("LoadConfig did not load the fixture")
	}
	return s, path, data
}

func TestAnUntouchedWebappConfigRoundTripIsByteIdentical(t *testing.T) {
	s, path, golden := seedWebappConfig(t, "webapp-config.golden.json")
	if err := s.SaveConfig(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, golden) {
		t.Fatalf("an untouched round-trip changed config.json:\n--- before\n%s\n--- after\n%s", golden, after)
	}
}

func TestSaveConfigPreservesUnmodelledMembersAtEveryLevel(t *testing.T) {
	s, path, before := seedWebappConfig(t, "webapp-config.future.json")
	s.Config.WebappTheme = "dark"
	if err := s.SaveConfig(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	var got struct {
		WebappTheme string                     `json:"webapp_theme"`
		FutureTop   json.RawMessage            `json:"future_top"`
		Hooks       map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(after, &got); err != nil {
		t.Fatal(err)
	}
	if got.WebappTheme != "dark" {
		t.Fatalf("the mutation itself was lost:\n%s", after)
	}
	var want struct {
		FutureTop json.RawMessage            `json:"future_top"`
		Hooks     map[string]json.RawMessage `json:"hooks"`
	}
	_ = json.Unmarshal(before, &want)
	if compact(t, got.FutureTop) != compact(t, want.FutureTop) {
		t.Errorf("future_top dropped or changed:\n%s", after)
	}
	if compact(t, got.Hooks["future_hook"]) != compact(t, want.Hooks["future_hook"]) {
		t.Errorf("hooks.future_hook dropped or changed:\n%s", after)
	}
}

func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	if raw == nil {
		return "<absent>"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
