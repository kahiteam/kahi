package logging

import (
	"reflect"
	"strings"
	"testing"
)

func splitAll(chunks ...string) []string {
	var got []string
	s := &LineSplitter{Emit: func(l []byte) { got = append(got, string(l)) }}
	for _, c := range chunks {
		s.Write([]byte(c))
	}
	s.Flush()
	return got
}

func TestLineSplitter(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   []string
	}{
		{"one chunk", []string{"a\nb\n"}, []string{"a", "b"}},
		{"line across chunks", []string{"hel", "lo\nwor", "ld\n"}, []string{"hello", "world"}},
		{"crlf", []string{"a\r\nb\r\n"}, []string{"a", "b"}},
		{"empty line", []string{"a\n\nb\n"}, []string{"a", "", "b"}},
		{"partial flushed", []string{"a\npartial"}, []string{"a", "partial"}},
		{"nothing", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := splitAll(tc.chunks...); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("lines = %q, want %q", got, tc.want)
			}
		})
	}
}

// Output without newlines is emitted in maxLineBytes pieces instead of
// growing the buffer without bound.
func TestLineSplitterCapsLongLines(t *testing.T) {
	got := splitAll(strings.Repeat("x", maxLineBytes+10))
	if len(got) != 2 || len(got[0]) != maxLineBytes || len(got[1]) != 10 {
		lens := make([]int, len(got))
		for i, l := range got {
			lens[i] = len(l)
		}
		t.Fatalf("piece lengths = %v, want [%d 10]", lens, maxLineBytes)
	}
}

func TestFormatJSONLineFieldOrder(t *testing.T) {
	got := string(FormatJSONLine("web", "stdout", "hi"))
	if !strings.HasPrefix(got, `{"time":"`) || !strings.HasSuffix(got, `","process":"web","stream":"stdout","log":"hi"}`+"\n") {
		t.Fatalf("line = %q, want the documented field order", got)
	}
}
