package storage

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const hostileToken = "vercel_blob_rw_hostile_planted-in-a-body:0123"

// hostileBlob is a Blob API that misbehaves in one chosen way.
func hostileBlob(t *testing.T, h http.HandlerFunc) *VercelBlob {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	v, err := NewVercelBlob(VercelBlobConfig{
		Token: hostileToken, Access: "private",
		APIBaseURL: srv.URL + "/api", BlobBaseURL: srv.URL + "/blob", HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// ⛔ A server answering the same cursor forever made list spin until a context
// that the serving path never sets timed out. It now stops on a repeated or
// missing cursor, and on a page bound.
func TestBlobListStopsOnARepeatedOrMissingCursorAndIsBounded(t *testing.T) {
	for name, page := range map[string]func(n int64) string{
		"repeated cursor": func(int64) string { return `{"blobs":[],"cursor":"same","hasMore":true}` },
		"missing cursor":  func(int64) string { return `{"blobs":[],"hasMore":true}` },
		"endless fresh":   func(n int64) string { return fmt.Sprintf(`{"blobs":[],"cursor":"c%d","hasMore":true}`, n) },
	} {
		var calls int64
		v := hostileBlob(t, func(w http.ResponseWriter, r *http.Request) {
			n := atomic.AddInt64(&calls, 1)
			_, _ = w.Write([]byte(page(n)))
		})
		old := maxListPages
		maxListPages = 50
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := v.List(ctx, "")
		timedOut := ctx.Err() != nil
		cancel()
		maxListPages = old
		if err == nil {
			t.Errorf("%s: a listing that never ends reported success", name)
		}
		if timedOut {
			t.Errorf("%s: stopped only by the context timeout", name)
		}
		if c := atomic.LoadInt64(&calls); c > 51 {
			t.Errorf("%s: %d requests; the bound is 50 pages", name, c)
		}
	}
}

// ⛔ Get reads at most maxBlobObject bytes.
func TestBlobGetIsBounded(t *testing.T) {
	v := hostileBlob(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
	})
	old := maxBlobObject
	maxBlobObject = 1024
	defer func() { maxBlobObject = old }()
	if _, err := v.Get(context.Background(), "a.md"); err == nil {
		t.Error("an object past the bound was read")
	}
	maxBlobObject = 4096
	if b, err := v.Get(context.Background(), "a.md"); err != nil || len(b) != 2048 {
		t.Errorf("an object within the bound was refused: %v", err)
	}
}

// ⛔ Put refuses what Get could not read back, before sending anything.
func TestBlobPutIsBoundedLikeGet(t *testing.T) {
	sent := false
	v := hostileBlob(t, func(w http.ResponseWriter, r *http.Request) {
		sent = true
	})
	old := maxBlobObject
	maxBlobObject = 1024
	defer func() { maxBlobObject = old }()
	if err := v.Put(context.Background(), "a.md", []byte(strings.Repeat("x", 2048))); err == nil || sent {
		t.Errorf("an object past the bound was sent (err %v, sent %v)", err, sent)
	}
}

// ⛔ A failed write carries its status, never the response body.
func TestBlobErrorsNeverEchoTheBody(t *testing.T) {
	v := hostileBlob(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"token ` + hostileToken + ` rejected"}}`))
	})
	err := v.Put(context.Background(), "a.md", []byte("x"))
	if err == nil {
		t.Fatal("a refused put succeeded")
	}
	if strings.Contains(err.Error(), hostileToken) || strings.Contains(err.Error(), "rejected") {
		t.Errorf("the error echoed the body: %v", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("the error lost the status: %v", err)
	}
}
