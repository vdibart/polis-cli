package ops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommentActionsIncludesUpdate(t *testing.T) {
	h := NewBuiltinCoreHandler()
	actions := h.Actions("pub.polis.comment")
	found := false
	for _, a := range actions {
		if a == "update" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("pub.polis.comment actions missing %q: %v", "update", actions)
	}
}

func TestDispatchCommentUpdate_RequiresPath(t *testing.T) {
	engine, _ := newTestEngine(t)
	_, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "update",
		ContentType: "pub.polis.comment",
		Payload:     map[string]any{"markdown": "body"},
	})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected 'path is required' error, got %v", err)
	}
}

func TestDispatchCommentUpdate_RepublishesNewVersion(t *testing.T) {
	engine, siteDir := newTestEngine(t)

	rel := "content/pub.polis.core/comment/20260301/test-comment.md"
	full := filepath.Join(siteDir, rel)
	os.MkdirAll(filepath.Dir(full), 0755)
	original := `---
title: Re: hello
type: comment
published: 2026-03-01T00:00:00Z
author: test.example.com
generator: polis-cli-go/dev
in-reply-to:
  url: https://alice.polis.pub/posts/20260301/hello.md
  root-post: https://alice.polis.pub/posts/20260301/hello.md
current-version: sha256:deadbeef
version-history:
  - sha256:deadbeef (2026-03-01T00:00:00Z)
signature: fake
---

Original comment body.`
	if err := os.WriteFile(full, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := engine.Dispatch(context.Background(), ActionRequest{
		Action:      "update",
		ContentType: "pub.polis.comment",
		Payload: map[string]any{
			"path":     rel,
			"markdown": "A substantially edited comment body.",
		},
	})
	if err != nil {
		t.Fatalf("dispatch update: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("status = %q, want success", result.Status)
	}
	version, _ := result.Data["version"].(string)
	if version == "" || version == "sha256:deadbeef" {
		t.Errorf("version = %q, want a new sha256 hash", version)
	}
	if state, _ := result.Data["blessing_state"].(string); state != "blessed" {
		t.Errorf("blessing_state = %q, want blessed (no pending/denied copy)", state)
	}

	// The on-disk comment must carry the new version and the edited body.
	data, _ := os.ReadFile(full)
	if !strings.Contains(string(data), "A substantially edited comment body.") {
		t.Error("comment file not updated with new body")
	}
	if strings.Contains(string(data), "current-version: sha256:deadbeef") {
		t.Error("current-version not advanced")
	}
}
