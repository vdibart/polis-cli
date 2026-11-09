package signing_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fleetOf(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = string(rune('a'+i)) + ".polis.pub"
	}
	return out
}

func TestParitySampleIsDeterministicForASeed(t *testing.T) {
	fleet := fleetOf(12)
	a := paritySample(fleet, 5, 20369)
	b := paritySample(append([]string(nil), fleet...), 5, 20369)
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("same seed, different samples: %v vs %v", a, b)
	}
	if len(a) != 5 {
		t.Fatalf("sample size %d, want 5", len(a))
	}
}

// Successive seeds walk the whole fleet before any site comes round again.
func TestParitySampleRotatesAcrossSeeds(t *testing.T) {
	fleet := fleetOf(10)
	seen := map[string]int{}
	for seed := int64(0); seed < 2; seed++ {
		for _, d := range paritySample(fleet, 5, seed) {
			seen[d]++
		}
	}
	if len(seen) != 10 {
		t.Fatalf("two seeds of 5 over 10 sites covered %d sites, want all 10: %v", len(seen), seen)
	}
	if strings.Join(paritySample(fleet, 5, 0), ",") == strings.Join(paritySample(fleet, 5, 1), ",") {
		t.Fatal("consecutive seeds picked the same sample")
	}
}

func TestParitySampleIsCapped(t *testing.T) {
	if got := paritySample(fleetOf(3), 5, 7); len(got) != 3 {
		t.Fatalf("a sample larger than the fleet is the fleet: got %v", got)
	}
	if got := paritySample(fleetOf(8), 0, 7); got != nil {
		t.Fatalf("size 0 turns parity off: got %v", got)
	}
	if got := paritySample(fleetOf(8), 3, -4); len(got) != 3 {
		t.Fatalf("a negative seed still samples: got %v", got)
	}
}

// The watch counts every 429 except the discovery service's, which has its own
// limit and is never called by the parity pass.
func TestRequestWatchCountsThrottles(t *testing.T) {
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3460")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	}))
	defer limited.Close()

	w := newRequestWatch(http.DefaultTransport, "https://ds.example")
	hc := &http.Client{Transport: w}
	resp, err := hc.Get(limited.URL + "/.well-known/polis")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if _, n := w.snapshot(); n != 1 {
		t.Fatalf("429s counted = %d, want 1", n)
	}

	ds := newRequestWatch(http.DefaultTransport, limited.URL)
	resp, err = (&http.Client{Transport: ds}).Get(limited.URL + "/v1/sites/public-key")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if _, n := ds.snapshot(); n != 0 {
		t.Fatalf("a discovery-service 429 was counted against parity: %d", n)
	}
}

// ⛔ The why.polis.pub case: a site answering 429 must stop the parity pass,
// and nothing about it may be reported as a parity regression.
func TestAThrottledSiteStopsParityAndIsNeverARegression(t *testing.T) {
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	}))
	defer limited.Close()

	watch := newRequestWatch(http.DefaultTransport, "https://ds.example")
	orig := http.DefaultTransport
	http.DefaultTransport = watch
	t.Cleanup(func() { http.DefaultTransport = orig })

	ft := &fleetTally{byType: map[string]*typeTally{}, watch: watch, paritySampled: map[string]bool{}}
	checkFleetParity(ft, limited.URL, 3)

	if ft.parityStopped == "" {
		t.Fatalf("a 429 did not stop the parity pass; notes: %v", ft.notes)
	}
	if len(ft.failures) != 0 {
		t.Fatalf("a throttled site was reported as a failure: %v", ft.failures)
	}
	if ft.paritySites != 0 {
		t.Fatalf("a throttled comparison was counted as made: %d", ft.paritySites)
	}

	// Once stopped, later sites are not compared at all.
	checkFleetParity(ft, limited.URL, 3)
	if !strings.Contains(strings.Join(ft.notes, "\n"), "parity pass stopped after a 429") {
		t.Fatalf("the next site was compared after the stop; notes: %v", ft.notes)
	}
}

// A comparison that would take polis.pub past the request budget is skipped
// and said, not attempted.
func TestParityRespectsTheHostedBudget(t *testing.T) {
	t.Setenv("POLIS_FLEET_HOSTED_BUDGET", "100")
	watch := newRequestWatch(http.DefaultTransport, "https://ds.example")
	ft := &fleetTally{byType: map[string]*typeTally{}, watch: watch, paritySampled: map[string]bool{}}
	checkFleetParity(ft, "https://big.polis.pub", 400)
	if len(ft.parityOverBudget) != 1 || ft.paritySites != 0 {
		t.Fatalf("over-budget comparison: overBudget=%v sites=%d", ft.parityOverBudget, ft.paritySites)
	}
}

func TestASmallSiteIsVerifiedInFull(t *testing.T) {
	entries := make([]int, 40)
	got, lo, hi := signatureSlice(entries, 500, 20735)
	if len(got) != 40 || lo != 0 || hi != 39 {
		t.Fatalf("a 40-entry site: got %d entries (%d–%d), want all 40", len(got), lo, hi)
	}
}

// A large site is verified one slice a day, and successive days walk the
// whole index.
func TestALargeSiteIsSlicedAndTheSlicesCoverIt(t *testing.T) {
	entries := make([]int, 2636)
	for i := range entries {
		entries[i] = i
	}
	seen := map[int]bool{}
	days := (2636 + 499) / 500
	for seed := int64(0); seed < int64(days); seed++ {
		got, _, _ := signatureSlice(entries, 500, seed)
		if len(got) == 0 || len(got) > 500 {
			t.Fatalf("seed %d: slice of %d, want 1..500", seed, len(got))
		}
		for _, e := range got {
			seen[e] = true
		}
	}
	if len(seen) != 2636 {
		t.Fatalf("%d days of 500 covered %d of 2636 entries", days, len(seen))
	}
	a, _, _ := signatureSlice(entries, 500, 20735)
	b, _, _ := signatureSlice(entries, 500, 20735)
	if a[0] != b[0] {
		t.Fatal("the same seed picked a different slice")
	}
}

func TestSliceLabelNamesTheEntries(t *testing.T) {
	if got := sliceLabel("discover.polis.pub", 1000, 1499, 2636); got != "discover.polis.pub: entries 1001–1500 of 2636" {
		t.Fatalf("label = %q", got)
	}
	if got := sliceLabel("discover.polis.pub", 2400, 263, 2636); !strings.Contains(got, "wrapped") {
		t.Fatalf("a wrapped slice must say so: %q", got)
	}
}

// ⛔ A growing index does not drift: with ten entries appended a day, any
// 2m+1 consecutive days (m = the window count on the last of them) verify every
// entry present on the first, and with no step in the count any m days do.
// The starts sit just below the boundaries that broke the claim before: 2,640
// (the unanchored window covered 1,930), and 2,995, 3,995 and 4,990, where the
// count steps mid-cycle and m days missed up to 1,000 and 1,500 entries. Every
// seed phase is tried, so the cycle meets the step on each of its days.
func TestSlicesCoverAGrowingIndexWithoutDrift(t *testing.T) {
	const size, perDay = 500, 10
	windowsAt := func(n int) int { return (n + size - 1) / size }
	run := func(start, days int, first int64) (missed int) {
		n := start
		seen := make([]bool, start)
		for d := 0; d < days; d++ {
			_, lo, hi := signatureSlice(make([]int, n), size, first+int64(d))
			for i := lo; i <= hi && i < start; i++ {
				seen[i] = true
			}
			n += perDay
		}
		for _, s := range seen {
			if !s {
				missed++
			}
		}
		return missed
	}
	for _, start := range []int{2640, 2995, 3995, 4990, 7990} {
		for first := int64(20000); first < 20040; first++ {
			days := 2*windowsAt(start) + 1
			for 2*windowsAt(start+perDay*(days-1))+1 > days {
				days = 2*windowsAt(start+perDay*(days-1)) + 1
			}
			if missed := run(start, days, first); missed > 0 {
				t.Errorf("%d entries, starting on day %d: %d never verified in %d days", start, first, missed, days)
			}
		}
	}
	// With no step in the count, m days are enough: 2,640 stays at six windows.
	for first := int64(20000); first < 20012; first++ {
		if missed := run(2640, windowsAt(2640), first); missed > 0 {
			t.Errorf("2640 entries, starting on day %d: %d never verified in %d days", first, missed, windowsAt(2640))
		}
	}
}

// ⛔ A slice of 0 means no slicing, and a bad value is refused, never replaced
// by the default.
func TestASliceOfZeroMeansEverythingAndGarbageIsRefused(t *testing.T) {
	t.Setenv("POLIS_FLEET_SIGNATURE_SLICE", "0")
	if n, err := signatureSliceSize(); err != nil || n != 0 {
		t.Fatalf("0 = (%d, %v), want (0, nil)", n, err)
	}
	if got, _, _ := signatureSlice(make([]int, 3000), 0, 7); len(got) != 3000 {
		t.Errorf("a slice of 0 verified %d of 3000", len(got))
	}
	for _, v := range []string{"-1", "lots", "5x"} {
		t.Setenv("POLIS_FLEET_SIGNATURE_SLICE", v)
		if _, err := signatureSliceSize(); err == nil {
			t.Errorf("%q was accepted", v)
		}
	}
	t.Setenv("POLIS_FLEET_SIGNATURE_SLICE", "")
	if n, _ := signatureSliceSize(); n != defaultSignatureSlice {
		t.Errorf("unset = %d, want the default %d", n, defaultSignatureSlice)
	}
}
