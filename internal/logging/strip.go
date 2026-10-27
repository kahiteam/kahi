package logging

// StripANSI removes ANSI escape sequences from data.
func StripANSI(data []byte) []byte {
	result := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '[' {
			// Skip ESC [ ... final_byte sequence.
			i += 2
			for i < len(data) {
				b := data[i]
				i++
				// Final byte is in range 0x40-0x7E.
				if b >= 0x40 && b <= 0x7E {
					break
				}
			}
			continue
		}
		result = append(result, data[i])
		i++
	}
	return result
}

// maxPendingEscape bounds how much of an unfinished escape sequence an
// ANSIStripper holds; a longer one is not a real CSI sequence and is dropped.
const maxPendingEscape = 64

// ANSIStripper strips ANSI escape sequences from a stream written in chunks.
// A sequence cut off at the end of one chunk is held back and completed by
// the next, so a sequence split across pipe reads is still removed (#186).
type ANSIStripper struct {
	pending []byte
}

// Strip returns data with escape sequences removed, keeping an unfinished
// trailing sequence for the next call.
func (s *ANSIStripper) Strip(data []byte) []byte {
	if len(s.pending) > 0 {
		data = append(s.pending, data...)
		s.pending = nil
	}
	if cut := unfinishedEscape(data); cut < len(data) {
		if len(data)-cut <= maxPendingEscape {
			s.pending = append([]byte(nil), data[cut:]...)
		}
		data = data[:cut]
	}
	return StripANSI(data)
}

// unfinishedEscape returns where a trailing escape sequence without its final
// byte starts, or len(data) if there is none: a lone ESC, or ESC [ followed
// only by parameter and intermediate bytes (0x20-0x3F).
func unfinishedEscape(data []byte) int {
	for i := len(data) - 1; i >= 0 && len(data)-i <= maxPendingEscape+1; i-- {
		b := data[i]
		if b == 0x1b {
			if i == len(data)-1 || data[i+1] == '[' {
				return i
			}
			return len(data)
		}
		if b == '[' && i > 0 && data[i-1] == 0x1b {
			continue
		}
		if b < 0x20 || b > 0x3F {
			return len(data)
		}
	}
	return len(data)
}
