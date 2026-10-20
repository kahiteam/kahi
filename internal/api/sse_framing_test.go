package api

import (
	"bytes"
	"strings"
	"testing"
)

// SEC-013: the log SSE writer emits one data field per line, so process output
// containing newlines or an "event:" prefix cannot inject SSE frames.

func TestWriteSSEDataFramesEachLine(t *testing.T) {
	var buf bytes.Buffer
	writeSSEData(&buf, "line1\nevent: injected\nline3")

	want := "data: line1\ndata: event: injected\ndata: line3\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("framing mismatch:\n got: %q\nwant: %q", got, want)
	}

	// The injected "event:" text must appear only as data, never as an SSE
	// event field at the start of a line.
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if strings.HasPrefix(line, "event:") {
			t.Fatalf("injected text started an SSE event field: %q", line)
		}
	}
}

// SSE also ends a line at a bare CR, so CR and CRLF in output must be framed
// like LF or they could start an event:, id: or retry: field.
func TestWriteSSEDataCarriageReturns(t *testing.T) {
	var buf bytes.Buffer
	writeSSEData(&buf, "x\revent: foo\r\rretry: 99999\r\ny")

	want := "data: x\ndata: event: foo\ndata: \ndata: retry: 99999\ndata: y\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("framing mismatch:\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(buf.String(), "\r") {
		t.Fatalf("a carriage return reached the stream: %q", buf.String())
	}
}

func TestWriteSSEDataSingleLine(t *testing.T) {
	var buf bytes.Buffer
	writeSSEData(&buf, "hello")
	if want := "data: hello\n\n"; buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestWriteSSEDataBlankLineIsNotTerminator(t *testing.T) {
	var buf bytes.Buffer
	writeSSEData(&buf, "a\n\nb")
	// The middle blank line becomes an empty data field, not an event end.
	if want := "data: a\ndata: \ndata: b\n\n"; buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}
