package signing

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type guardShape struct {
	Name  string `json:"name"`
	Items []struct {
		URL string `json:"url"`
	} `json:"items"`
}

func TestGuardRewriteAllowsCreationAndAModelledFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.json")
	if err := GuardRewrite("x", path, guardShape{}); err != nil {
		t.Fatalf("a missing file is a creation: %v", err)
	}
	_ = os.WriteFile(path, []byte(`{"name":"a","items":[{"url":"u"}]}`), 0644)
	if err := GuardRewrite("x", path, guardShape{}); err != nil {
		t.Fatalf("a fully modelled file must be rewritable: %v", err)
	}
}

func TestGuardRewriteRefusesAndNamesEveryUnrecognisedMember(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	_ = os.WriteFile(path, []byte(`{"name":"a","future":1,"items":[{"url":"u","note":"n"}]}`), 0644)
	err := GuardRewrite("x file", path, guardShape{})
	var refusal *RewriteRefusedError
	if !errors.As(err, &refusal) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if strings.Join(refusal.Fields, ",") != "future,items[0].note" {
		t.Fatalf("fields = %v", refusal.Fields)
	}
	for _, want := range []string{"x file", "future", "items[0].note", "Upgrade polis", "polis site rewrite-unsigned " + path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must say %q: %v", want, err)
		}
	}
}
