package process

import "sync"

// logHub fans captured output out to log stream subscribers (ADR-040). It is
// keyed by process name and stream, not by capture writer, so a stream keeps
// receiving when a reload creates the process again. Publish runs under the
// capture writer's lock: subscribers must not block, and the hub lock is a
// leaf (nothing else is locked while it is held).
type logHub struct {
	mu     sync.Mutex
	subs   map[string]map[uint64]func(data []byte, id string)
	nextID uint64
}

func newLogHub() *logHub {
	return &logHub{subs: make(map[string]map[uint64]func([]byte, string))}
}

func (h *logHub) subscribe(name, stream string, fn func(data []byte, id string)) (cancel func()) {
	key := name + ":" + stream
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	if h.subs[key] == nil {
		h.subs[key] = make(map[uint64]func([]byte, string))
	}
	h.subs[key][id] = fn
	h.mu.Unlock()
	return func() {
		h.mu.Lock()
		delete(h.subs[key], id)
		if len(h.subs[key]) == 0 {
			delete(h.subs, key)
		}
		h.mu.Unlock()
	}
}

func (h *logHub) publish(name, stream string, data []byte, id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, fn := range h.subs[name+":"+stream] {
		fn(data, id)
	}
}

func (h *logHub) count(name, stream string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[name+":"+stream])
}
