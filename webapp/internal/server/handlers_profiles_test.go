// Tests for the /api/profiles endpoint and the buildProfilesList helper
// (06-profiles Phase 2). Coverage focuses on the contract pieces the
// frontend depends on:
//   - response shape (type, fields, JSON keys)
//   - relationship derivation (mutual / follows-you / you-follow / none)
//   - sort behavior under both modifiers
//   - recent-post enrichment from the network feed cache
//   - scope=all-polis returning 501 (Phase 3 placeholder)
//   - method/scope/sort validation
package server

import (
	"testing"
)

// TestProfiles_DeriveRelationshipTable pins the helper's truth table
// directly so refactors to the storage path can't silently flip the
// enum. Same matrix as TestProfiles_RelationshipEnum but at the unit
// level.
func TestProfiles_DeriveRelationshipTable(t *testing.T) {
	cases := []struct {
		isOutbound, isInbound bool
		want                  string
	}{
		{true, true, "mutual"},
		{true, false, "you-follow"},
		{false, true, "follows-you"},
		{false, false, "none"},
	}
	for _, c := range cases {
		got := deriveRelationship(c.isOutbound, c.isInbound)
		if got != c.want {
			t.Errorf("deriveRelationship(out=%v, in=%v): want %q, got %q",
				c.isOutbound, c.isInbound, c.want, got)
		}
	}
}
