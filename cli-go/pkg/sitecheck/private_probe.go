package sitecheck

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/privatepath"
)

// The private-file probe: ask a site, from outside, for every name it must
// never serve, and report what comes back.
//
// Every other key-safety check reads the filesystem: a key file's mode, a
// directory's mode, a key header pasted into a served directory. None of them
// knows how the site is SERVED, so a handler, rewrite or store that serves
// .polis passes all of them. This asks the server itself.
//
// ⛔ THE PROBE NEVER KEEPS A BODY. If it succeeds it has just fetched a private
// key. It reads at most probeReadLimit bytes, looks for a key header, and
// drops the buffer; PrivateExposure has no field that could carry the bytes,
// and a test holds it that way. Report the URL and status, nothing else.

// probeReadLimit bounds how much of any response the probe reads. A private
// key header sits at the start of the file.
const probeReadLimit = 64 << 10

// PrivateExposure is one request for a private name that came back looking
// served: a 2xx that is not the site's home page, or any response carrying a
// private key header.
type PrivateExposure struct {
	URL       string
	Status    int
	KeyHeader bool
}

// PrivateUnchecked is one request for a private name the probe could not
// answer: no HTTP response (Status 0), a refusal (429), or a server error
// (5xx). ⛔ It is NEITHER an exposure NOR clean. A 429 says the server would
// not answer, not that it would not serve the file — counting it as "not
// served" is how a rate-limited sweep once reported 871 names as safe that it
// had never checked.
type PrivateUnchecked struct {
	URL    string
	Status int
}

// PrivateProbeResult is what one probe run found. Unchecked names every
// request that could not be answered; Unanswered counts the subset that got no
// HTTP response at all.
type PrivateProbeResult struct {
	Exposures  []PrivateExposure
	Unchecked  []PrivateUnchecked
	Asked      int
	Unanswered int
}

// Checked is how many names got an answer the probe can read either way.
func (r PrivateProbeResult) Checked() int { return r.Asked - len(r.Unchecked) }

// inconclusiveStatus reports whether an HTTP status says nothing about the
// resource: the server refused to answer (429) or failed (5xx).
func inconclusiveStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// ProbePrivate requests each target from baseURL and reports the responses
// that look served, and every name it could not check. host, when set, is sent as the Host header, so a probe can
// address one tenant through a shared listener (Judge over the loopback).
// client may be nil. Redirects are never followed: a 3xx is reported as
// itself, and never chased into a body.
//
// A 2xx whose body is the site's own home page, or what it answers for a page
// that cannot exist, is not counted: a host whose fallback answers 200 with
// index.html or an app page for any path is not serving the file that was
// asked for. The comparison masks nonce attributes (sameAsHome). A body carrying a private key header is counted whatever
// the status.
func ProbePrivate(baseURL, host string, client *http.Client, targets []string) PrivateProbeResult {
	base := strings.TrimRight(baseURL, "/")
	c := noRedirectClient(client)

	home, homeOK := probeGet(c, base+"/", host)
	// ⭐ And what the server answers for a page that cannot exist: a host whose
	// home page is the rendered site but whose unknown paths reach an app
	// (polis's own remote instance on Vercel) falls back to a page that is not
	// the home page. ⚠️ Never a private name, so a server refusing those still
	// answers this one as it answers any unknown path.
	fallback, fallbackOK := probeGet(c, base+"/polis-validate-no-such-page-"+randomHex(8), host)
	excused := func(body []byte) bool {
		return (homeOK && home.status >= 200 && home.status < 300 && sameAsHome(body, home.prefix)) ||
			(fallbackOK && fallback.status >= 200 && fallback.status < 300 && sameAsHome(body, fallback.prefix))
	}

	var res PrivateProbeResult
	for _, t := range targets {
		res.Asked++
		u := base + t
		got, ok := probeGet(c, u, host)
		if !ok {
			res.Unanswered++
			res.Unchecked = append(res.Unchecked, PrivateUnchecked{URL: u})
			continue
		}
		key := hasPrivateKeyHeader(got.prefix)
		if !key && inconclusiveStatus(got.status) {
			// A key in an error body is still an exposure (above); otherwise
			// the server did not answer the question.
			res.Unchecked = append(res.Unchecked, PrivateUnchecked{URL: u, Status: got.status})
			continue
		}
		served := got.status >= 200 && got.status < 300 && !excused(got.prefix)
		if key || served {
			res.Exposures = append(res.Exposures, PrivateExposure{URL: u, Status: got.status, KeyHeader: key})
		}
	}
	return res
}

// randomHex returns n random bytes as hex, for a path no site has.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// cspNonce matches a nonce attribute: a server may write a fresh one into
// every page it serves (polis's own SPA fallback does), so no two responses
// are byte-identical.
var cspNonce = regexp.MustCompile(`nonce="[A-Za-z0-9+/=_-]{0,128}"`)

// sameAsHome reports whether body is the home page, nonce attributes aside.
//
// ⛔ ONLY the nonce is masked, and the rest must match byte for byte: loosening
// this to "looks like HTML" would excuse a real file served as text/html. The
// two prefixes are cut at the same length, and a nonce has a fixed length, so
// the cut falls at the same point in both.
func sameAsHome(body, home []byte) bool {
	if bytes.Equal(body, home) {
		return true
	}
	mask := []byte(`nonce=""`)
	return bytes.Equal(cspNonce.ReplaceAll(body, mask), cspNonce.ReplaceAll(home, mask))
}

type probeResponse struct {
	status int
	prefix []byte
}

func probeGet(c *http.Client, u, host string) (probeResponse, bool) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return probeResponse{}, false
	}
	if host != "" {
		req.Host = host
	}
	resp, err := c.Do(req)
	if err != nil {
		return probeResponse{}, false
	}
	defer resp.Body.Close()
	prefix, _ := io.ReadAll(io.LimitReader(resp.Body, probeReadLimit))
	return probeResponse{status: resp.StatusCode, prefix: prefix}, true
}

// hasPrivateKeyHeader reports whether b holds a PEM or OpenSSH private key
// header ("-----BEGIN OPENSSH PRIVATE KEY-----", "-----BEGIN RSA PRIVATE
// KEY-----", "-----BEGIN PRIVATE KEY-----", ...).
func hasPrivateKeyHeader(b []byte) bool {
	for {
		i := bytes.Index(b, []byte("-----BEGIN "))
		if i < 0 {
			return false
		}
		b = b[i+len("-----BEGIN "):]
		end := bytes.Index(b, []byte("-----"))
		if end < 0 {
			return false
		}
		if bytes.HasSuffix(b[:end], []byte("PRIVATE KEY")) {
			return true
		}
	}
}

func noRedirectClient(c *http.Client) *http.Client {
	var out http.Client
	if c != nil {
		out = *c
	} else {
		out.Timeout = 15 * time.Second
	}
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// PrivateProbeTargets is the full target list, from the declared list of
// private names.
func PrivateProbeTargets() []string { return privatepath.ProbeTargets() }
