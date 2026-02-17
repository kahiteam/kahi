package logging

import "sync"

// RingBuffer is a fixed-size circular buffer for process output.
type RingBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
	pos  int
	full bool
}

// NewRingBuffer creates a ring buffer with the given capacity.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		buf:  make([]byte, size),
		size: size,
	}
}

// Write appends data to the ring buffer.
func (rb *RingBuffer) Write(p []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	for _, b := range p {
		rb.buf[rb.pos] = b
		rb.pos = (rb.pos + 1) % rb.size
		if rb.pos == 0 {
			rb.full = true
		}
	}
}

// Read returns the last n bytes from the buffer.
// If n exceeds available data, returns all available data.
func (rb *RingBuffer) Read(n int) []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if n > rb.size {
		n = rb.size
	}

	available := rb.pos
	if rb.full {
		available = rb.size
	}

	if n > available {
		n = available
	}
	if n == 0 {
		return nil
	}

	result := make([]byte, n)
	start := rb.pos - n
	if start < 0 {
		start += rb.size
	}

	for i := 0; i < n; i++ {
		result[i] = rb.buf[(start+i)%rb.size]
	}

	return result
}

// Len returns the number of bytes stored.
func (rb *RingBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if rb.full {
		return rb.size
	}
	return rb.pos
}

// Reset clears the buffer.
func (rb *RingBuffer) Reset() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.pos = 0
	rb.full = false
}
