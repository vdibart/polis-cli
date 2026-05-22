package notification

import (
	"path/filepath"
	"testing"
)

func TestStateFile(t *testing.T) {
	path := StateFile("/data", "example.supabase.co")
	expected := filepath.Join("/data", ".polis", "ds", "example.supabase.co", "pub.polis.core", "state", "pub.polis.notification.jsonl")
	if path != expected {
		t.Errorf("StateFile() = %q, want %q", path, expected)
	}
}

func TestStateDir(t *testing.T) {
	dir := StateDir("/data", "example.supabase.co")
	expected := filepath.Join("/data", ".polis", "ds", "example.supabase.co", "pub.polis.core", "state")
	if dir != expected {
		t.Errorf("StateDir() = %q, want %q", dir, expected)
	}
}

func TestManagerList_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, "test.supabase.co")

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
	mgr := NewManager(tmpDir, "test.supabase.co")

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
	mgr := NewManager(tmpDir, "test.supabase.co")

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
	mgr := NewManager(tmpDir, "test.supabase.co")

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
