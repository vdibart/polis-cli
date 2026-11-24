package server

import (
	"bytes"
	"io"
	"log"
	"os"
)

// OneLinePerLogEntry points the standard logger at stderr through a writer
// that keeps every entry on one line. Each entry point calls it first.
//
// ⛔ Why: the standard logger writes a message as it is, so a value carrying a
// newline (a comment URL, a domain, anything a remote party supplied) starts a
// second line. Fly ships stderr to Axiom line by line and indexes a line that
// is valid JSON as a structured event, so that second line could pass for one
// of ours, a security event included. The structured loggers encode JSON and
// escape newlines already; this covers every log.Printf.
func OneLinePerLogEntry() {
	log.SetOutput(OneLineWriter(os.Stderr))
}

// OneLineWriter wraps w so that, within each Write (the standard logger makes
// one per entry), every CR and LF except a final LF is written as the two
// characters \r or \n.
func OneLineWriter(w io.Writer) io.Writer { return oneLine{w} }

type oneLine struct{ w io.Writer }

func (o oneLine) Write(p []byte) (int, error) {
	body := p
	final := len(body) > 0 && body[len(body)-1] == '\n'
	if final {
		body = body[:len(body)-1]
	}
	if bytes.IndexAny(body, "\r\n") < 0 {
		return o.w.Write(p)
	}
	out := make([]byte, 0, len(p)+8)
	for _, c := range body {
		switch c {
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		default:
			out = append(out, c)
		}
	}
	if final {
		out = append(out, '\n')
	}
	if _, err := o.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
