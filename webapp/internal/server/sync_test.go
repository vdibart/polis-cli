package server

import (
	"strings"
	"testing"
)

func TestGenerateSyncID_ValidUUIDv4(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := generateSyncID()

		// UUIDv4 format: 8-4-4-4-12 hex chars
		parts := strings.Split(id, "-")
		if len(parts) != 5 {
			t.Fatalf("expected 5 dash-separated parts, got %d: %q", len(parts), id)
		}
		expectedLens := []int{8, 4, 4, 4, 12}
		for j, part := range parts {
			if len(part) != expectedLens[j] {
				t.Errorf("part %d: expected %d chars, got %d in %q", j, expectedLens[j], len(part), id)
			}
		}

		// Version nibble (char index 0 of 3rd segment) must be '4'
		if parts[2][0] != '4' {
			t.Errorf("expected version nibble '4', got %q in %q", string(parts[2][0]), id)
		}

		// Variant nibble (char index 0 of 4th segment) must be 8, 9, a, or b
		v := parts[3][0]
		if v != '8' && v != '9' && v != 'a' && v != 'b' {
			t.Errorf("expected variant nibble in [89ab], got %q in %q", string(v), id)
		}
	}

	// Uniqueness: two calls should not produce the same ID
	a := generateSyncID()
	b := generateSyncID()
	if a == b {
		t.Errorf("two consecutive generateSyncID() calls returned the same value: %q", a)
	}
}
