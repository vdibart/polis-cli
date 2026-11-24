package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// ⭐⭐ EVERY /v1 ERROR IS ONE ENVELOPE.
//
//	{"status": "error", "error": {"code": "<machine-readable>", "message": "<for a person>"}}
//
// The engine's answers already had it. Two other shapes reached callers: a
// host route's {"error": msg}, and plain text from every browser-app handler a
// /v1 operation forwards to (http.Error). A client had to handle three. The
// outermost /v1 wrapper now holds an error answer back, and one that is not
// the envelope is rewritten into it: the message kept, the code derived from
// the status. Headers the handler set (Retry-After, WWW-Authenticate) stay.
// A success answer passes straight through, unbuffered.

// codeForStatus is the envelope code for an error answer that named none.
func codeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusServiceUnavailable:
		return "unavailable"
	}
	if status >= 500 {
		return "internal_error"
	}
	return "invalid_request"
}

// envelopeWriter holds back an error answer so it can be put in the envelope.
type envelopeWriter struct {
	http.ResponseWriter
	status    int
	holding   bool
	buf       bytes.Buffer
	wroteHead bool
}

func (e *envelopeWriter) WriteHeader(code int) {
	if e.wroteHead || e.holding {
		return
	}
	if code >= 400 {
		e.holding, e.status = true, code
		return
	}
	e.wroteHead = true
	e.ResponseWriter.WriteHeader(code)
}

func (e *envelopeWriter) Write(b []byte) (int, error) {
	if !e.wroteHead && !e.holding {
		e.WriteHeader(http.StatusOK)
	}
	if e.holding {
		return e.buf.Write(b)
	}
	return e.ResponseWriter.Write(b)
}

// Flush passes through for a success answer; an error is sent whole at finish.
func (e *envelopeWriter) Flush() {
	if e.holding {
		return
	}
	if f, ok := e.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack lets a handler that takes over the connection do so.
func (e *envelopeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := e.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("hijack not supported")
}

// noteErrorCode passes writeError's code on to an inner writer that records it.
func (e *envelopeWriter) noteErrorCode(code string) {
	if c, ok := e.ResponseWriter.(errorCoder); ok {
		c.noteErrorCode(code)
	}
}

// finish sends a held error, in the envelope.
func (e *envelopeWriter) finish() {
	if !e.holding {
		return
	}
	body := envelopeFor(e.status, e.buf.Bytes())
	h := e.ResponseWriter.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	e.ResponseWriter.WriteHeader(e.status)
	_, _ = e.ResponseWriter.Write(body)
}

// envelopeFor is body as the envelope: unchanged when it already is one, the
// message of {"error": msg} or of plain text otherwise.
func envelopeFor(status int, body []byte) []byte {
	var parsed map[string]any
	if json.Unmarshal(body, &parsed) == nil {
		if inner, ok := parsed["error"].(map[string]any); ok && parsed["status"] == "error" {
			if code, _ := inner["code"].(string); code != "" {
				return body
			}
		}
	}
	message := ""
	if parsed != nil {
		switch m := parsed["error"].(type) {
		case string:
			message = m
		case map[string]any:
			message, _ = m["message"].(string)
		}
		if message == "" {
			message, _ = parsed["message"].(string)
		}
	} else {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = http.StatusText(status)
	}
	inner := map[string]any{"code": codeForStatus(status), "message": message}
	// ⭐ What else the answer said (the reasons a change was refused, which
	// rule failed) is kept, inside error, so nothing a caller could use is lost.
	for k, v := range parsed {
		if k != "error" && k != "status" && k != "message" && k != "code" {
			inner[k] = v
		}
	}
	out, _ := json.Marshal(map[string]any{"status": "error", "error": inner})
	return append(out, '\n')
}

// enveloped wraps a /v1 handler so every error it answers is the envelope.
func enveloped(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ew := &envelopeWriter{ResponseWriter: w}
		defer ew.finish()
		next(ew, r)
	}
}
