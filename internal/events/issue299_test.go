package events

import (
	"bufio"
	"errors"
	"strings"
	"testing"
)

// A protocol line longer than maxProtocolLine is reported as invalid and
// skipped without being buffered whole; the next line reads normally.
func TestReadProtocolLineBounded(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(strings.Repeat("x", 100000) + "\nREADY\n"))
	line, err := readProtocolLine(r)
	if !errors.Is(err, errLineTooLong) || len(line) > maxProtocolLine+3 {
		t.Fatalf("long line: %d bytes, %v", len(line), err)
	}
	line, err = readProtocolLine(r)
	if err != nil || line != "READY\n" {
		t.Errorf("next line %q %v", line, err)
	}
}
