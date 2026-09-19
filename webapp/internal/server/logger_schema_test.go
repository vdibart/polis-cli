package server

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// captureLoggerJSON runs fn against a JSON-emitting Logger and returns the
// records it printed.
func captureLoggerJSON(t *testing.T, fn func(l *Logger)) []map[string]interface{} {
	t.Helper()
	logsDir := filepath.Join(t.TempDir(), "logs")
	os.MkdirAll(logsDir, 0755)
	logger := NewLogger(LogLevelBasic, logsDir)
	logger.jsonOutput = true

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan []map[string]interface{})
	go func() {
		var out []map[string]interface{}
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			var rec map[string]interface{}
			if json.Unmarshal(sc.Bytes(), &rec) == nil {
				out = append(out, rec)
			}
		}
		done <- out
	}()
	func() {
		defer func() { os.Stdout = old }()
		fn(logger)
	}()
	w.Close()
	return <-done
}

// C19: `action` is the event name across the stack. A caller's own `action`
// field used to overwrite it, so the record vanished from every query on its
// name. The caller's value is kept, renamed.
func TestLoggerEventNameIsNeverOverwrittenByACallerAction(t *testing.T) {
	recs := captureLoggerJSON(t, func(l *Logger) {
		l.Event("pub.polis.post.publish", map[string]interface{}{"action": "republish", "path": "p.md"})
	})
	if len(recs) != 1 {
		t.Fatalf("records = %v", recs)
	}
	rec := recs[0]
	if rec["action"] != "pub.polis.post.publish" {
		t.Errorf("action = %v, want the event name", rec["action"])
	}
	if rec["context_action"] != "republish" {
		t.Errorf("context_action = %v, want the caller's value", rec["context_action"])
	}
	if rec["path"] != "p.md" {
		t.Errorf("path = %v", rec["path"])
	}
}

// R1-6: `source` and `ts` are schema fields too. A caller field of either name
// used to overwrite them — a record claiming to come from somewhere it did not.
func TestLoggerSchemaFieldsAreNeverOverwrittenByCallerFields(t *testing.T) {
	recs := captureLoggerJSON(t, func(l *Logger) {
		l.Event("pub.polis.post.publish", map[string]interface{}{
			"source": "security",
			"ts":     "1999-01-01T00:00:00Z",
			"path":   "p.md",
		})
	})
	if len(recs) != 1 {
		t.Fatalf("records = %v", recs)
	}
	rec := recs[0]
	if rec["source"] != "webapp" {
		t.Errorf("source = %v, want webapp", rec["source"])
	}
	if rec["ts"] == "1999-01-01T00:00:00Z" {
		t.Error("a caller field overwrote the timestamp")
	}
	if rec["context_source"] != "security" || rec["context_ts"] != "1999-01-01T00:00:00Z" {
		t.Errorf("caller values not kept: %v", rec)
	}
	if rec["path"] != "p.md" {
		t.Errorf("path = %v", rec["path"])
	}
}

// R24-18: security events belong on the security stream. They were logged on
// `source: webapp`, so every `source == "security"` dashboard missed them.
func TestLoggerSecurityEventsUseSourceSecurity(t *testing.T) {
	recs := captureLoggerJSON(t, func(l *Logger) {
		l.Event("pub.polis.security.path_traversal", map[string]interface{}{"path": "../x"})
		l.Event("pub.polis.post.publish", map[string]interface{}{})
	})
	if len(recs) != 2 {
		t.Fatalf("records = %v", recs)
	}
	if recs[0]["source"] != "security" {
		t.Errorf("security event source = %v, want security", recs[0]["source"])
	}
	if recs[1]["source"] != "webapp" {
		t.Errorf("ordinary event source = %v, want webapp", recs[1]["source"])
	}
}
