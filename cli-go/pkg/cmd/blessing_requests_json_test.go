package cmd

import (
	"encoding/json"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/blessing"
)

// The `blessing requests` JSON contract is consumed by docs/cli/user/json-mode.md,
// the polis skill references, and the bash e2e suite. It previously drifted —
// this command emitted a bare {"requests": ...} while the rest of the CLI used
// the {status, command, data} envelope, and nothing asserted the shape, so the
// divergence went unnoticed. These tests lock both halves of the contract.

func marshalPayload(t *testing.T, requests []blessing.IncomingRequest) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(blessingRequestsPayload(requests))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return out
}

func TestBlessingRequestsPayload_Envelope(t *testing.T) {
	out := marshalPayload(t, []blessing.IncomingRequest{{ID: "42"}})

	if out["status"] != "success" {
		t.Errorf("status = %v, want success", out["status"])
	}
	if out["command"] != "blessing-requests" {
		t.Errorf("command = %v, want blessing-requests", out["command"])
	}

	data, ok := out["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not an object: %T", out["data"])
	}
	if got := data["count"].(float64); got != 1 {
		t.Errorf("data.count = %v, want 1", got)
	}
	if _, ok := data["requests"].([]interface{}); !ok {
		t.Fatalf("data.requests is not an array: %T", data["requests"])
	}
	// The DS wire name must never surface in the CLI contract.
	if _, leaked := data["records"]; leaked {
		t.Error("data.records present — DS wire key leaked into the CLI contract")
	}
}

func TestBlessingRequestsPayload_ItemKeys(t *testing.T) {
	out := marshalPayload(t, []blessing.IncomingRequest{{
		ID:             "42",
		CommentURL:     "https://alice.com/comments/reply.md",
		CommentVersion: "sha256:abc",
		InReplyTo:      "https://bob.com/posts/hello.md",
		Author:         "alice.com",
		CreatedAt:      "2026-01-05T12:00:00Z",
	}})

	data := out["data"].(map[string]interface{})
	item := data["requests"].([]interface{})[0].(map[string]interface{})

	want := []string{
		"id", "comment_url", "comment_version", "in_reply_to",
		"root_post", "author", "timestamp", "created_at",
	}
	for _, key := range want {
		if _, ok := item[key]; !ok {
			t.Errorf("item is missing key %q", key)
		}
	}
	if len(item) != len(want) {
		t.Errorf("item has %d keys, want %d: %v", len(item), len(want), item)
	}

	// id is a string, not a number — both doc copies used to show `1`.
	if _, ok := item["id"].(string); !ok {
		t.Errorf("item.id = %T, want string", item["id"])
	}
	if item["comment_version"] != "sha256:abc" {
		t.Errorf("item.comment_version = %v", item["comment_version"])
	}
}

func TestBlessingRequestsPayload_EmptyIsArrayNotNull(t *testing.T) {
	out := marshalPayload(t, nil)
	data := out["data"].(map[string]interface{})

	if got := data["count"].(float64); got != 0 {
		t.Errorf("data.count = %v, want 0", got)
	}
	arr, ok := data["requests"].([]interface{})
	if !ok {
		t.Fatalf("data.requests = %T, want [] (consumers index into it)", data["requests"])
	}
	if len(arr) != 0 {
		t.Errorf("data.requests has %d entries, want 0", len(arr))
	}
}
