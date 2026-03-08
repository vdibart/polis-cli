package stream

import (
	"testing"
	"time"
)

func TestVerificationState_RecordDSFailure(t *testing.T) {
	v := &VerificationState{}

	v.RecordDSFailure()
	if v.DSFailures.Total != 1 {
		t.Errorf("expected total=1, got %d", v.DSFailures.Total)
	}
	if v.DSFailures.Consecutive != 1 {
		t.Errorf("expected consecutive=1, got %d", v.DSFailures.Consecutive)
	}
	if v.DSFailures.LastFailure == nil {
		t.Error("expected last_failure to be set")
	}

	v.RecordDSFailure()
	if v.DSFailures.Consecutive != 2 {
		t.Errorf("expected consecutive=2, got %d", v.DSFailures.Consecutive)
	}
}

func TestVerificationState_RecordDSSuccess_ResetsConsecutive(t *testing.T) {
	v := &VerificationState{}
	v.RecordDSFailure()
	v.RecordDSFailure()
	v.RecordDSSuccess()

	if v.DSFailures.Consecutive != 0 {
		t.Errorf("expected consecutive=0 after success, got %d", v.DSFailures.Consecutive)
	}
	if v.DSFailures.Total != 2 {
		t.Errorf("total should remain 2, got %d", v.DSFailures.Total)
	}
}

func TestVerificationState_ShouldSuspendSync(t *testing.T) {
	v := &VerificationState{}
	if v.ShouldSuspendSync(3) {
		t.Error("should not suspend with 0 failures")
	}

	v.RecordDSFailure()
	v.RecordDSFailure()
	if v.ShouldSuspendSync(3) {
		t.Error("should not suspend with 2 failures (threshold 3)")
	}

	v.RecordDSFailure()
	if !v.ShouldSuspendSync(3) {
		t.Error("should suspend with 3 consecutive failures")
	}
}

func TestVerificationState_RecordAuthorFailure(t *testing.T) {
	v := &VerificationState{}

	v.RecordAuthorFailure("evil.com", "pub.polis.follow.announced")
	v.RecordAuthorFailure("evil.com", "pub.polis.follow.announced") // duplicate type
	v.RecordAuthorFailure("evil.com", "pub.polis.post.published")

	stat := v.AuthorFailures["evil.com"]
	if stat == nil {
		t.Fatal("expected stat for evil.com")
	}
	if stat.Total != 3 {
		t.Errorf("expected total=3, got %d", stat.Total)
	}
	if len(stat.EventTypes) != 2 {
		t.Errorf("expected 2 event types, got %d: %v", len(stat.EventTypes), stat.EventTypes)
	}
}

func TestVerificationState_RecentAuthorFailures(t *testing.T) {
	v := &VerificationState{}
	v.RecordAuthorFailure("recent.com", "pub.polis.follow.announced")

	// Add an old failure
	old := time.Now().UTC().Add(-48 * time.Hour)
	v.AuthorFailures["old.com"] = &AuthorFailureStat{
		Total:       1,
		LastFailure: &old,
		EventTypes:  []string{"pub.polis.post.published"},
	}

	recent := v.RecentAuthorFailures(24 * time.Hour)
	if _, ok := recent["recent.com"]; !ok {
		t.Error("expected recent.com in recent failures")
	}
	if _, ok := recent["old.com"]; ok {
		t.Error("old.com should not be in recent failures (>24h ago)")
	}
}

func TestVerificationState_Persistence(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir, "ds.example.com", "pub.polis.core")

	v := &VerificationState{}
	v.RecordDSFailure()
	v.RecordAuthorFailure("evil.com", "pub.polis.follow.announced")

	if err := v.Save(store); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded := &VerificationState{}
	if err := loaded.Load(store); err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if loaded.DSFailures.Total != 1 {
		t.Errorf("expected DS total=1, got %d", loaded.DSFailures.Total)
	}
	if loaded.AuthorFailures["evil.com"] == nil {
		t.Error("expected author failure for evil.com after reload")
	}
}
