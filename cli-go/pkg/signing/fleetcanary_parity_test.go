package signing_test

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ⭐ PARITY IS SAMPLED, AND A 429 STOPS IT.
//
// The parity pass clones a live site and validates it twice, so it costs about
// twice the site's size in requests. polis.pub allows 3000 requests per hour
// per client address, counted across EVERY tenant (publicStaticContentLimiter
// in the hosted service), and cloning every small site in the fleet used that
// up on its own: four full runs in a row came back throttled and were not
// evidence. So each run compares a SAMPLE, rotating through the fleet.
//
// ⚠️ THE BUDGET, AS ARITHMETIC. polis.pub allows 3000 requests per hour per
// address. A run spends:
//
//   - per site, about 8 fixed requests: .well-known/polis twice, the index,
//     about five JSON artifacts. 40 sites ≈ 320.
//   - one request per index entry VERIFIED. Every site at or under the slice
//     size (POLIS_FLEET_SIGNATURE_SLICE, default 500) is verified in full; a
//     larger one is verified a rotating slice of that size per day. On
//     2026-10-09 the fleet had about 2,750 entries, 2,636 of them on
//     discover.polis.pub, so the pass costs about 110 + 500 ≈ 610 rather than
//     2,750. That is what tripped the limit: before the slice, the signature
//     pass alone sent 3,557 requests and 557 came back 429.
//   - parity, about 2 × (entries + 20) per sampled site. Five small sites add
//     about 700.
//
// So about 320 + 610 + 700 ≈ 1,630, roughly half the limit. The REQUESTS line
// reports the real figure on every run, so the estimate is checked each time.
// The guard does not trust it either: a parity comparison that would take
// polis.pub past POLIS_FLEET_HOSTED_BUDGET (default 2500) is skipped and said.
// Raising the slice raises the second term one-for-one.
//
// ⛔ A 429 IS NOT A FINDING. A throttled remote run reads "no blessed.json
// served" and "does not parse as an attestation" off a rate-limit response,
// and those used to surface as PARITY REGRESSIONs (why.polis.pub, 2026-10-08).
// The watch below sees every response, so a parity comparison made while a 429
// arrived is discarded, and the parity pass stops there and says so.

const (
	defaultParitySample   = 5
	defaultHostedBudget   = 2500
	defaultSignatureSlice = 500
)

// signatureSliceSize is the most index entries one run verifies on one site.
//
// ⛔ It says what it was given or refuses: 0 means NO slicing (every entry, every
// site), and anything that is not a non-negative integer is an error. It used
// to fall back to the default for 0 and for garbage alike, so asking for a full
// run silently got a 500-entry slice.
func signatureSliceSize() (int, error) {
	v := os.Getenv("POLIS_FLEET_SIGNATURE_SLICE")
	if v == "" {
		return defaultSignatureSlice, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("POLIS_FLEET_SIGNATURE_SLICE=%q: want a non-negative integer (0 = no slicing)", v)
	}
	return n, nil
}

// signatureSlice returns the entries one run verifies on one site: all of them
// when there are at most size, otherwise window number (seed mod windows) of
// the index cut into fixed size-long windows from position 0 — the last one
// shorter. lo and hi are its first and last positions (0-based).
//
// ⛔ ANCHORED, so a growing index does not drift. Windows used to start at
// (seed × size) mod len, which moves whenever len does: at +10 posts a day six
// runs covered 1,930 of 2,640 entries. Anchored at multiples of size, growth
// only lengthens the last window or adds one.
//
// ⚠️ WHAT IS GUARANTEED, exactly: while the window count m = ceil(n/size)
// holds, any m consecutive seeds cover every entry. When the index crosses a
// multiple of size the modulus steps to m+1 and a cycle straddling the step
// can miss windows (a 2,995-entry site growing 10 a day missed up to 1,000 in
// six days), so the bound is 2m+1 consecutive seeds, m taken on the last —
// m before the step at most, then m+1 that cover everything. That holds while
// the count steps at most once in the span, i.e. while the index grows by
// less than size in 2m+1 days. A power-of-two window count was tried and is
// worse: it steps rarely, but each cycle is then up to twice as long.
func signatureSlice[T any](entries []T, size int, seed int64) (out []T, lo, hi int) {
	n := len(entries)
	if size <= 0 || n <= size {
		return entries, 0, n - 1
	}
	windows := int64((n + size - 1) / size)
	w := seed % windows
	if w < 0 {
		w += windows
	}
	lo = int(w) * size
	hi = lo + size - 1
	if hi >= n {
		hi = n - 1
	}
	return entries[lo : hi+1], lo, hi
}

// sliceLabel names a slice for the report, 1-based as a reader counts:
// "discover.polis.pub: entries 1001–1500 of 2636".
func sliceLabel(domain string, lo, hi, total int) string {
	if hi >= lo {
		return fmt.Sprintf("%s: entries %d–%d of %d", domain, lo+1, hi+1, total)
	}
	return fmt.Sprintf("%s: entries %d–%d and 1–%d of %d (wrapped)", domain, lo+1, total, hi+1, total)
}

// paritySampleSize is how many sites the parity pass compares; 0 or less
// turns parity off.
func paritySampleSize() int {
	if v := os.Getenv("POLIS_FLEET_PARITY_SAMPLE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultParitySample
}

// paritySeed is the day number, so the sample rotates once a day and two runs
// on the same day compare the same sites. POLIS_FLEET_PARITY_SEED pins it.
func paritySeed() int64 {
	if v := os.Getenv("POLIS_FLEET_PARITY_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return time.Now().UTC().Unix() / 86400
}

func hostedBudget() int {
	if v := os.Getenv("POLIS_FLEET_HOSTED_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultHostedBudget
}

// paritySample picks n consecutive domains, in sorted order and wrapping, from
// a window that moves n places per seed. Successive seeds therefore cover the
// whole fleet before any site comes round again.
func paritySample(domains []string, n int, seed int64) []string {
	if n <= 0 || len(domains) == 0 {
		return nil
	}
	sorted := append([]string(nil), domains...)
	sort.Strings(sorted)
	if n >= len(sorted) {
		return sorted
	}
	size := int64(len(sorted))
	start := (seed % size) * int64(n) % size
	if start < 0 {
		start += size
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, sorted[(int(start)+i)%len(sorted)])
	}
	return out
}

// parityCost is the estimated request count of comparing one site.
func parityCost(entries int) int { return 2 * (entries + 20) }

func isHostedHost(host string) bool {
	return host == "polis.pub" || strings.HasSuffix(host, ".polis.pub")
}

// requestWatch wraps the process's default transport for the canary run. It
// counts what went to polis.pub, which shares one per-address limit across
// tenants, and every 429. The discovery service is excluded from both: it has
// its own limit, and the parity pass never calls it (rr.DSKeys is nil there).
type requestWatch struct {
	inner  http.RoundTripper
	dsHost string

	mu        sync.Mutex
	total     int
	hosted    int
	throttled int
	byHost    map[string]int
}

func newRequestWatch(inner http.RoundTripper, dsBase string) *requestWatch {
	host := ""
	if u, err := url.Parse(dsBase); err == nil {
		host = u.Hostname()
	}
	return &requestWatch{inner: inner, dsHost: host, byHost: map[string]int{}}
}

func (w *requestWatch) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := w.inner.RoundTrip(r)
	host := r.URL.Hostname()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.total++
	if host == w.dsHost {
		return resp, err
	}
	if isHostedHost(host) {
		w.hosted++
	}
	if err == nil && resp.StatusCode == http.StatusTooManyRequests {
		w.throttled++
		w.byHost[host]++
	}
	return resp, err
}

// snapshot returns the polis.pub request count and the 429 count so far.
func (w *requestWatch) snapshot() (hosted, throttled int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hosted, w.throttled
}

func (w *requestWatch) summary() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := fmt.Sprintf("%d total, %d to polis.pub (limit 3000/hour per address; discovery service excluded), %d answered 429", w.total, w.hosted, w.throttled)
	if len(w.byHost) > 0 {
		hosts := make([]string, 0, len(w.byHost))
		for h, n := range w.byHost {
			hosts = append(hosts, fmt.Sprintf("%s ×%d", h, n))
		}
		sort.Strings(hosts)
		s += ": " + strings.Join(hosts, ", ")
	}
	return s
}
