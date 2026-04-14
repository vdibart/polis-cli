package notification

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
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

func TestManagerAppend(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, "test.supabase.co")

	entries := []StateEntry{
		{
			ID:        "blessing-requested:https://bob.com/comments/reply.md",
			RuleID:    "blessing-requested",
			Actor:     "bob.com",
			Icon:      "\U0001F514",
			Message:   "bob.com requested a blessing on welcome",
			EventIDs:  []int{4521},
			CreatedAt: "2025-01-15T10:30:00Z",
		},
	}

	written, err := mgr.Append(entries)
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if written != 1 {
		t.Errorf("Expected 1 written, got %d", written)
	}

	// List back
	listed, err := mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("Expected 1 entry, got %d", len(listed))
	}
	if listed[0].ID != entries[0].ID {
		t.Errorf("ID = %q, want %q", listed[0].ID, entries[0].ID)
	}
	if listed[0].Icon != "\U0001F514" {
		t.Errorf("Icon = %q, want bell emoji", listed[0].Icon)
	}
}

func TestManagerAppend_Dedup(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, "test.supabase.co")

	entry := StateEntry{
		ID:        "dedupe-key",
		RuleID:    "test",
		Actor:     "alice.com",
		Icon:      "i",
		Message:   "test",
		EventIDs:  []int{1},
		CreatedAt: "2025-01-15T10:30:00Z",
	}

	// First append
	written, _ := mgr.Append([]StateEntry{entry})
	if written != 1 {
		t.Errorf("First append: expected 1 written, got %d", written)
	}

	// Second append with same ID
	written, _ = mgr.Append([]StateEntry{entry})
	if written != 0 {
		t.Errorf("Duplicate append: expected 0 written, got %d", written)
	}

	entries, _ := mgr.List()
	if len(entries) != 1 {
		t.Errorf("Expected 1 entry after dedup, got %d", len(entries))
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

	mgr.Append([]StateEntry{
		{ID: "n1", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{1}, CreatedAt: "2025-01-15T10:30:00Z"},
		{ID: "n2", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{2}, CreatedAt: "2025-01-15T10:31:00Z"},
	})

	count, _ = mgr.CountUnread()
	if count != 2 {
		t.Errorf("Expected 2, got %d", count)
	}
}

func TestMarkRead(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, "test.supabase.co")

	mgr.Append([]StateEntry{
		{ID: "n1", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{1}, CreatedAt: "2025-01-15T10:30:00Z"},
		{ID: "n2", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{2}, CreatedAt: "2025-01-15T10:31:00Z"},
		{ID: "n3", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{3}, CreatedAt: "2025-01-15T10:32:00Z"},
	})

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

	mgr.Append([]StateEntry{
		{ID: "n1", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{1}, CreatedAt: "2025-01-15T10:30:00Z"},
		{ID: "n2", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{2}, CreatedAt: "2025-01-15T10:31:00Z"},
		{ID: "n3", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{3}, CreatedAt: "2025-01-15T10:32:00Z"},
		{ID: "n4", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{4}, CreatedAt: "2025-01-15T10:33:00Z"},
		{ID: "n5", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{5}, CreatedAt: "2025-01-15T10:34:00Z"},
	})

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

func TestPrune_MaxItems(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, "test.supabase.co")

	// Append 10 entries
	var entries []StateEntry
	for i := 0; i < 10; i++ {
		entries = append(entries, StateEntry{
			ID:        fmt.Sprintf("n%d", i),
			RuleID:    "test",
			Actor:     "a",
			Icon:      "i",
			Message:   "m",
			EventIDs:  []int{i},
			CreatedAt: fmt.Sprintf("2025-01-15T10:%02d:00Z", i),
		})
	}
	mgr.Append(entries)

	// Prune to 5
	removed, err := mgr.Prune(PruneConfig{MaxItems: 5})
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if removed != 5 {
		t.Errorf("Expected 5 removed, got %d", removed)
	}

	remaining, _ := mgr.List()
	if len(remaining) != 5 {
		t.Fatalf("Expected 5 remaining, got %d", len(remaining))
	}
	// Newest 5 should remain (n5..n9)
	if remaining[0].ID != "n5" {
		t.Errorf("Expected n5 first, got %s", remaining[0].ID)
	}
	if remaining[4].ID != "n9" {
		t.Errorf("Expected n9 last, got %s", remaining[4].ID)
	}
}

func TestPrune_MaxAgeDays(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, "test.supabase.co")

	now := time.Now().UTC()
	entries := []StateEntry{
		{ID: "old1", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{1},
			CreatedAt: now.AddDate(0, 0, -120).Format("2006-01-02T15:04:05Z")},
		{ID: "old2", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{2},
			CreatedAt: now.AddDate(0, 0, -100).Format("2006-01-02T15:04:05Z")},
		{ID: "recent1", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{3},
			CreatedAt: now.AddDate(0, 0, -30).Format("2006-01-02T15:04:05Z")},
		{ID: "recent2", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{4},
			CreatedAt: now.AddDate(0, 0, -1).Format("2006-01-02T15:04:05Z")},
	}
	mgr.Append(entries)

	removed, err := mgr.Prune(PruneConfig{MaxAgeDays: 90})
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if removed != 2 {
		t.Errorf("Expected 2 removed, got %d", removed)
	}

	remaining, _ := mgr.List()
	if len(remaining) != 2 {
		t.Fatalf("Expected 2 remaining, got %d", len(remaining))
	}
	if remaining[0].ID != "recent1" {
		t.Errorf("Expected recent1 first, got %s", remaining[0].ID)
	}
}

func TestPrune_NoChanges(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, "test.supabase.co")

	mgr.Append([]StateEntry{
		{ID: "n1", RuleID: "test", Actor: "a", Icon: "i", Message: "m", EventIDs: []int{1},
			CreatedAt: time.Now().UTC().Format("2006-01-02T15:04:05Z")},
	})

	removed, err := mgr.Prune(PruneConfig{MaxItems: 500, MaxAgeDays: 90})
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if removed != 0 {
		t.Errorf("Expected 0 removed, got %d", removed)
	}
}

func TestPrune_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	mgr := NewManager(tmpDir, "test.supabase.co")

	removed, err := mgr.Prune(DefaultPruneConfig())
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if removed != 0 {
		t.Errorf("Expected 0 removed, got %d", removed)
	}
}

