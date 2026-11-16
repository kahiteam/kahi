package logging

import (
	"bytes"
	"testing"
)

// FUNC-086 / ADR-048: the buffer size comes from the capture config, 64 KB
// when unset, up to 16 MB.

func TestRingBufferHolds16MB(t *testing.T) {
	const max = 16 << 20
	rb := NewRingBuffer(max)
	rb.Write(bytes.Repeat([]byte("a"), max))
	rb.Write([]byte("bbbbbbbbbb"))
	got := rb.Read(max)
	if len(got) != max {
		t.Fatalf("read %d bytes, want %d", len(got), max)
	}
	if !bytes.HasSuffix(got, []byte("abbbbbbbbbb")) {
		t.Errorf("buffer ends %q, want the 10 b bytes last", got[len(got)-11:])
	}
}

func TestRingBufferClampedTo16MB(t *testing.T) {
	rb := NewRingBuffer(32 << 20)
	rb.Write(bytes.Repeat([]byte("a"), 17<<20))
	if n := rb.Len(); n != 16<<20 {
		t.Errorf("Len = %d, want %d", n, 16<<20)
	}
}

func TestCaptureWriterBufferSize(t *testing.T) {
	tests := []struct {
		name string
		size int
		want int
	}{
		{"unset is 64 KB", 0, 64 << 10},
		{"4 KB", 4 << 10, 4 << 10},
		{"128 KB", 128 << 10, 128 << 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cw, err := NewCaptureWriter(CaptureConfig{ProcessName: "p", Stream: "stdout", BufferSize: tt.size})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := cw.Write(bytes.Repeat([]byte("x"), 200<<10)); err != nil {
				t.Fatal(err)
			}
			// A read longer than the buffer returns the whole buffer.
			if got := cw.ReadTail(16 << 20); len(got) != tt.want {
				t.Errorf("ReadTail returned %d bytes, want %d", len(got), tt.want)
			}
		})
	}
}

// The SSE replay reads everything after an offset in one call; with a
// buffer above 64 KB that read is above the old 64 KB allocation cap.
func TestCaptureWriterReadFromLargeBuffer(t *testing.T) {
	cw, err := NewCaptureWriter(CaptureConfig{ProcessName: "p", Stream: "stdout", BufferSize: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cw.Write(bytes.Repeat([]byte("y"), 100<<10)); err != nil {
		t.Fatal(err)
	}
	data, start, total := cw.ReadFrom(0)
	if len(data) != 100<<10 || start != 0 || total != 100<<10 {
		t.Errorf("ReadFrom(0) = %d bytes, start %d, total %d; want %d, 0, %d", len(data), start, total, 100<<10, 100<<10)
	}
}

// Tail reports whether the bytes it returns start after the beginning of the
// captured output, where the first line may be cut.
func TestCaptureWriterTailTruncated(t *testing.T) {
	cw, err := NewCaptureWriter(CaptureConfig{ProcessName: "p", Stream: "stdout", BufferSize: 4 << 10})
	if err != nil {
		t.Fatal(err)
	}
	if data, truncated := cw.Tail(100); len(data) != 0 || truncated {
		t.Errorf("empty: %d bytes, truncated %v; want 0, false", len(data), truncated)
	}
	if _, err := cw.Write([]byte("one\ntwo\n")); err != nil {
		t.Fatal(err)
	}
	if data, truncated := cw.Tail(65536); string(data) != "one\ntwo\n" || truncated {
		t.Errorf("all held: %q, truncated %v; want everything, false", data, truncated)
	}
	if data, truncated := cw.Tail(4); string(data) != "two\n" || !truncated {
		t.Errorf("shorter read: %q, truncated %v; want two, true", data, truncated)
	}
	if _, err := cw.Write(bytes.Repeat([]byte("z"), 5000)); err != nil {
		t.Fatal(err)
	}
	if data, truncated := cw.Tail(65536); len(data) != 4096 || !truncated {
		t.Errorf("wrapped: %d bytes, truncated %v; want 4096, true", len(data), truncated)
	}
}
