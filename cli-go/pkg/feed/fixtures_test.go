package feed

import "time"

// daysAgo returns a UTC timestamp n days in the past, for seeding feed
// fixtures.
//
// ⚠️ ANY FIXTURE THAT PASSES THROUGH MergeItems MUST USE A CLOCK-RELATIVE DATE.
//
// The mechanism, precisely, because the over-broad version of this rule sends
// people to edit tests that are fine:
//
//   - MergeItems calls pruneLocked (cache.go), which drops content older than
//     FeedConfig.MaxAgeDays — default 90.
//   - List does NOT prune. It reads whatever is on disk.
//
// So a test that writes JSONL directly and only calls List is immune to the
// calendar, and TestCacheManager_ListSortsOutOfOrderJSONL deliberately uses
// fixed dates to assert a deterministic ordering. Leave it alone. A test that
// seeds through MergeItems with a literal date is a time bomb: it passes when
// written and starts failing about 90 days later, when the fixture is pruned
// out from under the assertion.
//
// That is not hypothetical. TestList_OversizeLine_Skipped and
// TestE2E_DSStreamToFeedCache seeded 2026-05-18 and 2026-05-21, passed for
// three months, and began failing around 2026-08-16 — with symptoms ("expected
// 2 cached items after sync, got 0") that read exactly like a cache
// regression. Nothing had broken; the calendar had moved, and someone could
// easily have spent a session debugging working code.
//
// Verified at the boundary rather than assumed: reseeding these fixtures to 89
// days passes and 91 days fails.
func daysAgo(n int) time.Time {
	return time.Now().UTC().AddDate(0, 0, -n)
}

// tsDaysAgo is daysAgo formatted the way feed items and DS events carry
// timestamps.
func tsDaysAgo(n int) string {
	return daysAgo(n).Format("2006-01-02T15:04:05Z")
}

// tsDaysAgoPlus offsets tsDaysAgo, for fixtures that need a stable ordering
// between two items without pinning either to the calendar.
func tsDaysAgoPlus(n int, d time.Duration) string {
	return daysAgo(n).Add(d).Format("2006-01-02T15:04:05Z")
}
