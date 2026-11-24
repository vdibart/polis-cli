package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// invoke calls one of the browser app's handlers in-process and decodes its
// JSON answer. A non-2xx answer is an error carrying the handler's own text.
func (s *Server) invoke(ctx context.Context, h http.HandlerFunc, method, path string, body any) (map[string]any, error) {
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	rec := &bufferedResponse{header: http.Header{}, status: http.StatusOK}
	h(rec, req)
	if rec.status < 200 || rec.status > 299 {
		// A raw body is cut short; a handler's own error message is kept
		// whole up to a larger cap, because it may name what would be
		// accepted (a refused PQL sentence carries the vocabulary).
		msg, limit := strings.TrimSpace(rec.body.String()), 300
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(rec.body.Bytes(), &e) == nil && e.Error != "" {
			msg, limit = e.Error, 1200
		}
		if len(msg) > limit {
			msg = msg[:limit]
		}
		return nil, fmt.Errorf("the site refused: %s", msg)
	}
	out := map[string]any{}
	if err := json.Unmarshal(rec.body.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("the site answered something that is not JSON")
	}
	return out, nil
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
	wrote  bool
}

func (b *bufferedResponse) Header() http.Header { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) {
	b.wrote = true
	return b.body.Write(p)
}
func (b *bufferedResponse) WriteHeader(code int) {
	if !b.wrote {
		b.status = code
		b.wrote = true
	}
}
