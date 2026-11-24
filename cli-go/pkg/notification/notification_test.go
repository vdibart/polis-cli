package notification

import (
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"testing"
)

func TestStateFileKey(t *testing.T) {
	if got, want := StateFileKey("example.supabase.co"), ".polis/ds/example.supabase.co/pub.polis.core/state/pub.polis.notification.jsonl"; got != want {
		t.Errorf("StateFileKey() = %q, want %q", got, want)
	}
}

func TestStateDirKey(t *testing.T) {
	if got, want := StateDirKey("example.supabase.co"), ".polis/ds/example.supabase.co/pub.polis.core/state"; got != want {
		t.Errorf("StateDirKey() = %q, want %q", got, want)
	}
}

func TestManagerList_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(storage.At(tmpDir), "test.supabase.co")

	entries, err := mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries, got %d", len(entries))
	}
}

func TestCountUnread(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(storage.At(tmpDir), "test.supabase.co")

	// Empty = 0
	count, _ := mgr.CountUnread()
	if count != 0 {
		t.Errorf("Expected 0, got %d", count)
	}

	if err := mgr.writeAll([]StateEntry{
		{ID: "n1", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{1}, CreatedAt: "2025-01-15T10:30:00Z"},
		{ID: "n2", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{2}, CreatedAt: "2025-01-15T10:31:00Z"},
	}); err != nil {
		t.Fatalf("writeAll failed: %v", err)
	}

	count, _ = mgr.CountUnread()
	if count != 2 {
		t.Errorf("Expected 2, got %d", count)
	}
}

func TestMarkRead(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(storage.At(tmpDir), "test.supabase.co")

	if err := mgr.writeAll([]StateEntry{
		{ID: "n1", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{1}, CreatedAt: "2025-01-15T10:30:00Z"},
		{ID: "n2", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{2}, CreatedAt: "2025-01-15T10:31:00Z"},
		{ID: "n3", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{3}, CreatedAt: "2025-01-15T10:32:00Z"},
	}); err != nil {
		t.Fatalf("writeAll failed: %v", err)
	}

	// Mark specific IDs
	marked, err := mgr.MarkRead([]string{"n1", "n2"}, false)
	if err != nil {
		t.Fatalf("MarkRead failed: %v", err)
	}
	if marked != 2 {
		t.Errorf("Expected 2 marked, got %d", marked)
	}

	unread, _ := mgr.CountUnread()
	if unread != 1 {
		t.Errorf("Expected 1 unread, got %d", unread)
	}

	// Mark all
	marked, _ = mgr.MarkRead(nil, true)
	if marked != 1 {
		t.Errorf("Expected 1 marked, got %d", marked)
	}

	unread, _ = mgr.CountUnread()
	if unread != 0 {
		t.Errorf("Expected 0 unread, got %d", unread)
	}
}

func TestListPaginated(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(storage.At(tmpDir), "test.supabase.co")

	if err := mgr.writeAll([]StateEntry{
		{ID: "n1", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{1}, CreatedAt: "2025-01-15T10:30:00Z"},
		{ID: "n2", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{2}, CreatedAt: "2025-01-15T10:31:00Z"},
		{ID: "n3", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{3}, CreatedAt: "2025-01-15T10:32:00Z"},
		{ID: "n4", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{4}, CreatedAt: "2025-01-15T10:33:00Z"},
		{ID: "n5", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{5}, CreatedAt: "2025-01-15T10:34:00Z"},
	}); err != nil {
		t.Fatalf("writeAll failed: %v", err)
	}

	// Mark some as read
	mgr.MarkRead([]string{"n1", "n2"}, false)

	// Unread only, first page
	items, total, err := mgr.ListPaginated(0, 2, false)
	if err != nil {
		t.Fatalf("ListPaginated failed: %v", err)
	}
	if total != 3 {
		t.Errorf("Expected total 3 unread, got %d", total)
	}
	if len(items) != 2 {
		t.Errorf("Expected 2 items, got %d", len(items))
	}
	// Newest first — n5 should be first
	if items[0].ID != "n5" {
		t.Errorf("Expected n5 first, got %s", items[0].ID)
	}

	// Second page
	items, _, _ = mgr.ListPaginated(2, 2, false)
	if len(items) != 1 {
		t.Errorf("Expected 1 item on second page, got %d", len(items))
	}

	// Include read
	items, total, _ = mgr.ListPaginated(0, 10, true)
	if total != 5 {
		t.Errorf("Expected total 5 with read, got %d", total)
	}
	if len(items) != 5 {
		t.Errorf("Expected 5 items, got %d", len(items))
	}

	// Offset beyond range
	items, _, _ = mgr.ListPaginated(100, 10, true)
	if len(items) != 0 {
		t.Errorf("Expected 0 items for large offset, got %d", len(items))
	}
}

// TestPublishedRFC3339ReadsEveryFormAnEntryWasWrittenIn: the state file holds
// entries from every era of its writers, so each form is read as RFC 3339 UTC.
func TestPublishedRFC3339ReadsEveryFormAnEntryWasWrittenIn(t *testing.T) {
	for in, want := range map[string]string{
		"2026-11-10T10:00:00Z":      "2026-11-10T10:00:00Z",
		"2026-11-10T05:00:00-05:00": "2026-11-10T10:00:00Z",
		"2026-11-10T10:00:00.123Z":  "2026-11-10T10:00:00Z",
		"Wed Mar 04 2026 20:22:19 GMT+0000 (Coordinated Universal Time)": "2026-03-04T20:22:19Z",
		"Wed Mar 04 2026 15:22:19 GMT-0500 (Eastern Standard Time)":      "2026-03-04T20:22:19Z",
		"Wed, 04 Mar 2026 20:22:19 GMT":                                  "2026-03-04T20:22:19Z",
		"1772655739":                                                     "2026-03-04T20:22:19Z",
		"1772655739000":                                                  "2026-03-04T20:22:19Z",
		"":                                                               "",
		"not a time":                                                     "",
	} {
		if got := PublishedRFC3339(in); got != want {
			t.Errorf("PublishedRFC3339(%q) = %q, want %q", in, got, want)
		}
	}
}
