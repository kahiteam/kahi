package logging

import "testing"

func TestStripANSI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"\033[31mERROR\033[0m", "ERROR"},
		{"\033[1;32mOK\033[0m", "OK"},
		{"no ansi here", "no ansi here"},
		{"\033[2J\033[H", ""},
		{"\033[38;5;196mred\033[0m text", "red text"},
	}

	for _, tt := range tests {
		got := string(StripANSI([]byte(tt.input)))
		if got != tt.want {
			t.Errorf("StripANSI(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// A sequence split across chunks is still removed (#186).
func TestANSIStripperSplitSequences(t *testing.T) {
	for _, chunks := range [][]string{
		{"red \x1b[3", "1mtext\x1b[0m\n"},
		{"red \x1b", "[31mtext\x1b[0m\n"},
		{"red \x1b[", "31mtext\x1b[0", "m\n"},
		{"red ", "\x1b[31m", "text", "\x1b[0m", "\n"},
	} {
		var s ANSIStripper
		var out []byte
		for _, c := range chunks {
			out = append(out, s.Strip([]byte(c))...)
		}
		if string(out) != "red text\n" {
			t.Errorf("chunks %q: got %q, want %q", chunks, out, "red text\n")
		}
	}

	// Text that only looks like the start of a sequence is not held back.
	var s ANSIStripper
	if got := string(s.Strip([]byte("a [31m b\n"))); got != "a [31m b\n" {
		t.Errorf("plain brackets: %q", got)
	}
}
