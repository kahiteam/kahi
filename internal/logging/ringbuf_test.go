package logging

import "testing"

func TestRingBufferBasic(t *testing.T) {
	rb := NewRingBuffer(16)

	rb.Write([]byte("hello"))
	data := rb.Read(5)
	if string(data) != "hello" {
		t.Fatalf("got %q, want 'hello'", string(data))
	}
}

func TestRingBufferWrap(t *testing.T) {
	rb := NewRingBuffer(8)

	rb.Write([]byte("12345678")) // fills buffer
	rb.Write([]byte("AB"))       // wraps around

	data := rb.Read(8)
	if string(data) != "345678AB" {
		t.Fatalf("got %q, want '345678AB'", string(data))
	}
}

func TestRingBufferReadMoreThanAvailable(t *testing.T) {
	rb := NewRingBuffer(16)
	rb.Write([]byte("hi"))

	data := rb.Read(100)
	if string(data) != "hi" {
		t.Fatalf("got %q, want 'hi'", string(data))
	}
}

func TestRingBufferReadExceedsMaxAlloc(t *testing.T) {
	rb := NewRingBuffer(16)
	rb.Write([]byte("hello"))

	data := rb.Read(1 << 17) // 128KB, exceeds maxReadAlloc
	if data != nil {
		t.Fatalf("expected nil for oversized read, got %d bytes", len(data))
	}
}

func TestRingBufferLen(t *testing.T) {
	rb := NewRingBuffer(8)

	if rb.Len() != 0 {
		t.Fatalf("len = %d, want 0", rb.Len())
	}

	rb.Write([]byte("abc"))
	if rb.Len() != 3 {
		t.Fatalf("len = %d, want 3", rb.Len())
	}

	rb.Write([]byte("12345678")) // overflows
	if rb.Len() != 8 {
		t.Fatalf("len = %d, want 8", rb.Len())
	}
}

func TestRingBufferReset(t *testing.T) {
	rb := NewRingBuffer(8)
	rb.Write([]byte("test"))
	rb.Reset()

	if rb.Len() != 0 {
		t.Fatalf("len = %d, want 0 after reset", rb.Len())
	}
}

func TestRingBufferEmpty(t *testing.T) {
	rb := NewRingBuffer(8)
	data := rb.Read(10)
	if data != nil {
		t.Fatalf("expected nil for empty buffer, got %q", string(data))
	}
}
