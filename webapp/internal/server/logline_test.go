package server

import (
	"bytes"
	"log"
	"testing"
)

func TestOneLineWriterKeepsAnEntryOnOneLine(t *testing.T) {
	var buf bytes.Buffer
	l := log.New(OneLineWriter(&buf), "", 0)

	l.Printf("released %s", "https://a.example/c\n{\"source\":\"security\",\"action\":\"forged\"}\r\nx")
	want := `released https://a.example/c\n{"source":"security","action":"forged"}\r\nx` + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	buf.Reset()
	l.Printf("plain %d", 1)
	if got := buf.String(); got != "plain 1\n" {
		t.Fatalf("an entry with no newline changed: %q", got)
	}
}
