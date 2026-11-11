package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/events"
)

// streamFor runs the log stream handler with Last-Event-ID lastID until
// publish has run and the stream has had time to write, and returns the body.
func streamFor(t *testing.T, srv *Server, lastID string, publish func()) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/v1/processes/web/log/stdout/stream", nil).WithContext(ctx)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	w := newLockedRecorder()
	done := make(chan struct{})
	go func() {
		srv.mux.ServeHTTP(w, req)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	if publish != nil {
		publish()
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	return w.body()
}

// lockedRecorder is a ResponseWriter and Flusher safe to read while the
// handler writes.
type lockedRecorder struct {
	mu  sync.Mutex
	rec *httptest.ResponseRecorder
}

func newLockedRecorder() *lockedRecorder { return &lockedRecorder{rec: httptest.NewRecorder()} }

func (l *lockedRecorder) Header() http.Header { return l.rec.Header() }
func (l *lockedRecorder) WriteHeader(code int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rec.WriteHeader(code)
}
func (l *lockedRecorder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec.Write(p)
}
func (l *lockedRecorder) Flush() {}
func (l *lockedRecorder) body() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rec.Body.String()
}

func publishLog(srv *Server, id, data string) {
	srv.bus.Publish(events.Event{Type: events.ProcessLogStdout, Data: map[string]string{"name": "web", "id": id, "data": data}})
}

func TestStreamLogResumeInvalidID(t *testing.T) {
	srv, _, _ := testServer()
	for _, id := range []string{"abc", "1-x", "1--1", "-5", "g-"} {
		req := httptest.NewRequest("GET", "/api/v1/processes/web/log/stdout/stream", nil)
		req.Header.Set("Last-Event-ID", id)
		w := httptest.NewRecorder()
		srv.mux.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"invalid resume position: `+id+`"`) {
			t.Errorf("Last-Event-ID %q: %d %s, want 400 invalid resume position", id, w.Code, w.Body)
		}
	}
}

func TestStreamLogResume(t *testing.T) {
	srv, pm, _ := testServer()
	pm.logHeld, pm.logTotal = []byte("hello world"), 11

	// Same generation: only the bytes after the offset, id = its end.
	if got := streamFor(t, srv, "g-6", nil); got != "id: g-11\ndata: world\n\n" {
		t.Errorf("resume g-6: %q", got)
	}
	// At the current offset nothing is replayed.
	if got := streamFor(t, srv, "g-11", nil); got != "" {
		t.Errorf("resume g-11: %q, want nothing", got)
	}
	// Another generation: reset, then everything held.
	if got := streamFor(t, srv, "old-3", nil); got != "event: reset\ndata: {}\n\nid: g-11\ndata: hello world\n\n" {
		t.Errorf("resume old-3: %q", got)
	}
	// An offset beyond the total means the counter restarted.
	if got := streamFor(t, srv, "g-99", nil); !strings.HasPrefix(got, "event: reset\n") {
		t.Errorf("resume g-99: %q, want a reset", got)
	}

	// Only "world" (offsets 6-11) is still held: a gap of 4 bytes from 2.
	pm.logHeld = []byte("world")
	if got := streamFor(t, srv, "g-2", nil); got != "event: gap\ndata: {\"bytes\": 4}\n\nid: g-11\ndata: world\n\n" {
		t.Errorf("resume g-2: %q", got)
	}
}

// Live chunks already covered by the replay are skipped; later ones follow
// with their ids.
func TestStreamLogResumeSkipsReplayed(t *testing.T) {
	srv, pm, _ := testServer()
	pm.logHeld, pm.logTotal = []byte("hello world"), 11
	got := streamFor(t, srv, "g-6", func() {
		publishLog(srv, "g-11", "world")
		publishLog(srv, "g-15", " ok\n")
	})
	if got != "id: g-11\ndata: world\n\nid: g-15\ndata:  ok\ndata: \n\n" {
		t.Errorf("stream %q", got)
	}
}

// Without Last-Event-ID the stream is live only, with ids.
func TestStreamLogLiveHasIDs(t *testing.T) {
	srv, pm, _ := testServer()
	pm.logHeld, pm.logTotal = []byte("old"), 3
	if got := streamFor(t, srv, "", func() { publishLog(srv, "g-7", "new\n") }); got != "id: g-7\ndata: new\ndata: \n\n" {
		t.Errorf("stream %q", got)
	}
}

// blockingRecorder blocks every body write until released.
type blockingRecorder struct {
	*lockedRecorder
	release chan struct{}
}

func (b *blockingRecorder) Write(p []byte) (int, error) {
	<-b.release
	return b.lockedRecorder.Write(p)
}

// A client that falls 64 chunks behind has its stream closed instead of
// losing chunks; it resumes with its last id (ADR-033).
func TestStreamLogOverflowCloses(t *testing.T) {
	srv, _, _ := testServer()
	w := &blockingRecorder{lockedRecorder: newLockedRecorder(), release: make(chan struct{})}
	req := httptest.NewRequest("GET", "/api/v1/processes/web/log/stdout/stream", nil)
	done := make(chan struct{})
	go func() {
		srv.mux.ServeHTTP(w, req)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	for i := range 70 {
		publishLog(srv, "g-"+strings.Repeat("1", i+1), "x")
	}
	close(w.release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream still open after its queue overflowed")
	}
}

// A client that disconnects leaves no subscription behind.
func TestStreamLogUnsubscribesOnDisconnect(t *testing.T) {
	srv, _, _ := testServer()
	before := srv.bus.SubscriberCount(events.ProcessLogStdout)
	streamFor(t, srv, "", nil)
	if after := srv.bus.SubscriberCount(events.ProcessLogStdout); after != before {
		t.Fatalf("subscribers %d after the client left, want %d", after, before)
	}
}
