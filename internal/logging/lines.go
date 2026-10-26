package logging

import "bytes"

// maxLineBytes caps a line without a newline: longer output is emitted in
// pieces of this size, so a child that never prints a newline cannot grow
// the buffer without bound.
const maxLineBytes = 64 * 1024

// LineSplitter turns a stream of output chunks into lines. Emit receives
// each line without its trailing newline (or "\r\n"); the slice is only
// valid during the call.
type LineSplitter struct {
	Emit func(line []byte)
	buf  []byte
}

// Write splits data at newlines and emits every complete line, keeping a
// trailing partial line for the next call.
func (s *LineSplitter) Write(data []byte) {
	s.buf = append(s.buf, data...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		s.Emit(bytes.TrimSuffix(s.buf[:i], []byte("\r")))
		s.buf = s.buf[i+1:]
	}
	for len(s.buf) >= maxLineBytes {
		s.Emit(s.buf[:maxLineBytes])
		s.buf = s.buf[maxLineBytes:]
	}
	// Reuse the backing array once it is drained.
	if len(s.buf) == 0 {
		s.buf = s.buf[:0:0]
	}
}

// Flush emits a remaining partial line, for when the stream ends.
func (s *LineSplitter) Flush() {
	if len(s.buf) > 0 {
		s.Emit(s.buf)
		s.buf = nil
	}
}
