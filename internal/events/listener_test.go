package events

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNewListenerPoolSubscribesAndDispatches(t *testing.T) {
	bus := NewBus(testLogger())
	lp := NewListenerPool("test-pool", bus, testLogger(), []EventType{ProcessStateRunning, ProcessStateStopped})

	// Should have subscribed to both event types.
	if bus.SubscriberCount(ProcessStateRunning) != 1 {
		t.Fatalf("expected 1 subscriber for ProcessStateRunning, got %d", bus.SubscriberCount(ProcessStateRunning))
	}
	if bus.SubscriberCount(ProcessStateStopped) != 1 {
		t.Fatalf("expected 1 subscriber for ProcessStateStopped, got %d", bus.SubscriberCount(ProcessStateStopped))
	}

	lp.Stop()
}

func TestAddListenerRegistersAcknowledged(t *testing.T) {
	bus := NewBus(testLogger())
	lp := NewListenerPool("test-pool", bus, testLogger(), []EventType{ProcessStateRunning})
	defer lp.Stop()

	// Create pipes. AddListener takes (stdin io.Writer, stdout io.Reader).
	// stdin: we write to stdinW, listener reads from stdinR -- pass stdinW as stdin arg.
	// stdout: listener writes to stdoutW, we read from stdoutR -- pass stdoutR as stdout arg.
	stdoutR, stdoutW := io.Pipe()
	_, stdinW := io.Pipe()

	lp.AddListener("listener-0", stdinW, stdoutR)

	lp.mu.Lock()
	if len(lp.listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(lp.listeners))
	}
	l := lp.listeners[0]
	lp.mu.Unlock()

	l.mu.Lock()
	state := l.state
	l.mu.Unlock()

	if state != ListenerAcknowledged {
		t.Fatalf("expected state Acknowledged (0), got %d", state)
	}

	// Close pipes to let readReady goroutine exit.
	stdoutW.Close()
	stdinW.Close()
}

func TestReadReadyTransitionsToReady(t *testing.T) {
	bus := NewBus(testLogger())
	lp := NewListenerPool("test-pool", bus, testLogger(), []EventType{ProcessStateRunning})
	defer lp.Stop()

	stdoutR, stdoutW := io.Pipe()
	_, stdinW := io.Pipe()

	lp.AddListener("listener-0", stdinW, stdoutR)

	// Send READY token through the stdout pipe.
	fmt.Fprintln(stdoutW, "READY")

	// Wait for the readReady goroutine to process it.
	waitForState(t, lp, 0, ListenerReady, 2*time.Second)

	stdoutW.Close()
	stdinW.Close()
}

func TestDispatchRoutesEventsToSendToReady(t *testing.T) {
	bus := NewBus(testLogger())
	lp := NewListenerPool("test-pool", bus, testLogger(), []EventType{ProcessStateRunning})
	defer lp.Stop()

	stdoutR, stdoutW := io.Pipe()
	stdinR, stdinW := io.Pipe()

	lp.AddListener("listener-0", stdinW, stdoutR)

	// Signal READY.
	fmt.Fprintln(stdoutW, "READY")

	// Wait for Ready state.
	waitForState(t, lp, 0, ListenerReady, 2*time.Second)

	// Publish an event through the bus.
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	bus.Publish(Event{
		Type:      ProcessStateRunning,
		Timestamp: ts,
		Data:      map[string]string{"name": "web"},
	})

	// Read the framed payload from the listener's stdin.
	type framed struct {
		header string
		body   string
	}
	done := make(chan framed, 1)
	errCh := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(stdinR)
		header, body, err := readFramedPayload(reader)
		if err != nil {
			errCh <- err
			return
		}
		done <- framed{header: header, body: body}
	}()

	select {
	case f := <-done:
		if !strings.HasPrefix(f.header, "PROCESS_STATE_RUNNING") {
			t.Fatalf("expected header starting with PROCESS_STATE_RUNNING, got %q", f.header)
		}
		if f.body != "name:web" {
			t.Fatalf("expected framed body name:web, got %q", f.body)
		}
	case err := <-errCh:
		t.Fatalf("failed to read framed payload: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event payload on listener stdin")
	}

	stdoutW.Close()
	stdinR.Close()
}

// readFramedPayload parses one length-framed listener payload: a header line
// "TYPE TIMESTAMP len:N" followed by exactly N body bytes. It returns the
// header (without its trailing newline) and the N-byte body.
func readFramedPayload(r *bufio.Reader) (header string, body string, err error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", "", err
	}
	header = strings.TrimSuffix(line, "\n")

	idx := strings.LastIndex(header, " len:")
	if idx < 0 {
		return "", "", fmt.Errorf("header missing len prefix: %q", header)
	}
	n, err := strconv.Atoi(header[idx+len(" len:"):])
	if err != nil {
		return "", "", fmt.Errorf("invalid length in header %q: %w", header, err)
	}

	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", "", err
	}
	return header, string(buf), nil
}

func TestSendToReadyTransitionsToBusy(t *testing.T) {
	bus := NewBus(testLogger())
	lp := NewListenerPool("test-pool", bus, testLogger(), []EventType{ProcessStateRunning})
	defer lp.Stop()

	stdoutR, stdoutW := io.Pipe()
	stdinR, stdinW := io.Pipe()

	lp.AddListener("listener-0", stdinW, stdoutR)

	// Signal READY.
	fmt.Fprintln(stdoutW, "READY")
	waitForState(t, lp, 0, ListenerReady, 2*time.Second)

	// Drain stdin in background so sendToReady doesn't block.
	go func() { _, _ = io.Copy(io.Discard, stdinR) }()

	// Send event directly.
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	lp.sendToReady(Event{
		Type:      ProcessStateRunning,
		Timestamp: ts,
		Data:      map[string]string{"pid": "123"},
	})

	// Listener should now be Busy.
	lp.mu.Lock()
	l := lp.listeners[0]
	lp.mu.Unlock()

	l.mu.Lock()
	state := l.state
	l.mu.Unlock()

	if state != ListenerBusy {
		t.Fatalf("expected state Busy (2), got %d", state)
	}

	stdoutW.Close()
	stdinR.Close()
}

func TestFormatEventPayload(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	event := Event{
		Type:      ProcessStateRunning,
		Timestamp: ts,
		Data:      map[string]string{"name": "web"},
	}

	// Payload format: "TYPE TIMESTAMP len:N\n" followed by exactly N body bytes.
	expected := fmt.Sprintf("PROCESS_STATE_RUNNING %s len:8\nname:web", ts.Format(time.RFC3339))
	if payload := formatEventPayload(event); payload != expected {
		t.Fatalf("expected %q, got %q", expected, payload)
	}
}

func TestFormatEventPayloadEmptyData(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	event := Event{
		Type:      ProcessStateStopped,
		Timestamp: ts,
		Data:      nil,
	}

	// An empty payload body is framed with len:0 and no body bytes.
	payload := formatEventPayload(event)
	expected := fmt.Sprintf("PROCESS_STATE_STOPPED %s len:0\n", ts.Format(time.RFC3339))
	if payload != expected {
		t.Fatalf("expected %q, got %q", expected, payload)
	}
}

// TestFormatEventPayloadAnnouncesBodyLength verifies the header announces the
// exact byte length of the body and that the body follows the header line.
func TestFormatEventPayloadAnnouncesBodyLength(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	event := Event{
		Type:      ProcessStateRunning,
		Timestamp: ts,
		Data:      map[string]string{"name": "web", "pid": "123"},
	}

	header, body, err := readFramedPayload(bufio.NewReader(strings.NewReader(formatEventPayload(event))))
	if err != nil {
		t.Fatalf("failed to parse framed payload: %v", err)
	}

	// Keys are emitted in sorted order for deterministic framing.
	if body != "name:web pid:123" {
		t.Fatalf("unexpected body %q", body)
	}
	if want := fmt.Sprintf("PROCESS_STATE_RUNNING %s len:%d", ts.Format(time.RFC3339), len(body)); header != want {
		t.Fatalf("expected header %q, got %q", want, header)
	}
}

// TestFormatEventPayloadFramesEmbeddedNewline verifies that a payload value
// containing an embedded newline and header-like text is carried as opaque
// body bytes and cannot be parsed as a forged protocol line.
func TestFormatEventPayloadFramesEmbeddedNewline(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	// A compromised process could emit output containing a newline followed
	// by a line that mimics a protocol header.
	malicious := "line1\nPROCESS_STATE_FATAL 2026-01-01T00:00:00Z injected:1"
	event := Event{
		Type:      ProcessStateRunning,
		Timestamp: ts,
		Data:      map[string]string{"data": malicious},
	}

	payload := formatEventPayload(event)

	// The header is the only content up to the first newline. The injected
	// text must not appear on the header line.
	firstLine := payload[:strings.IndexByte(payload, '\n')]
	if strings.Contains(firstLine, "PROCESS_STATE_FATAL") {
		t.Fatalf("injected header leaked into protocol line: %q", firstLine)
	}
	if !strings.HasPrefix(firstLine, "PROCESS_STATE_RUNNING ") {
		t.Fatalf("unexpected header line: %q", firstLine)
	}

	// A conforming listener reads exactly len bytes and recovers the value
	// verbatim, embedded newline included.
	header, body, err := readFramedPayload(bufio.NewReader(strings.NewReader(payload)))
	if err != nil {
		t.Fatalf("failed to parse framed payload: %v", err)
	}
	idx := strings.LastIndex(header, " len:")
	n, err := strconv.Atoi(header[idx+len(" len:"):])
	if err != nil {
		t.Fatalf("invalid announced length in header %q: %v", header, err)
	}
	if n != len(body) {
		t.Fatalf("announced length %d does not match body length %d", n, len(body))
	}
	if body != "data:"+malicious {
		t.Fatalf("body not recovered verbatim: got %q", body)
	}
}

// TestFormatEventPayloadFrameConcatenation verifies two framed payloads
// written back-to-back are each recoverable, proving a length-framed body
// cannot desynchronize the stream even when it contains newlines.
func TestFormatEventPayloadFrameConcatenation(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	first := formatEventPayload(Event{
		Type:      ProcessStateRunning,
		Timestamp: ts,
		Data:      map[string]string{"data": "a\nb"},
	})
	second := formatEventPayload(Event{
		Type:      ProcessStateStopped,
		Timestamp: ts,
		Data:      map[string]string{"name": "web"},
	})

	r := bufio.NewReader(strings.NewReader(first + second))

	h1, b1, err := readFramedPayload(r)
	if err != nil {
		t.Fatalf("failed to read first frame: %v", err)
	}
	if !strings.HasPrefix(h1, "PROCESS_STATE_RUNNING ") || b1 != "data:a\nb" {
		t.Fatalf("unexpected first frame: header=%q body=%q", h1, b1)
	}

	h2, b2, err := readFramedPayload(r)
	if err != nil {
		t.Fatalf("failed to read second frame: %v", err)
	}
	if !strings.HasPrefix(h2, "PROCESS_STATE_STOPPED ") || b2 != "name:web" {
		t.Fatalf("unexpected second frame: header=%q body=%q", h2, b2)
	}
}

func TestMultipleListenersOnlyFirstReadyGetsEvent(t *testing.T) {
	bus := NewBus(testLogger())
	lp := NewListenerPool("test-pool", bus, testLogger(), []EventType{ProcessStateRunning})
	defer lp.Stop()

	// Set up two listeners.
	stdoutR0, stdoutW0 := io.Pipe()
	stdinR0, stdinW0 := io.Pipe()
	stdoutR1, stdoutW1 := io.Pipe()
	stdinR1, stdinW1 := io.Pipe()

	lp.AddListener("listener-0", stdinW0, stdoutR0)
	lp.AddListener("listener-1", stdinW1, stdoutR1)

	// Make both ready.
	fmt.Fprintln(stdoutW0, "READY")
	fmt.Fprintln(stdoutW1, "READY")
	waitForState(t, lp, 0, ListenerReady, 2*time.Second)
	waitForState(t, lp, 1, ListenerReady, 2*time.Second)

	// Read from both stdin pipes concurrently.
	var received0, received1 bool
	var mu sync.Mutex
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdinR0)
		if scanner.Scan() {
			mu.Lock()
			received0 = true
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdinR1)
		if scanner.Scan() {
			mu.Lock()
			received1 = true
			mu.Unlock()
		}
	}()

	// Send one event.
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	lp.sendToReady(Event{
		Type:      ProcessStateRunning,
		Timestamp: ts,
		Data:      map[string]string{"name": "web"},
	})

	// Give time for delivery.
	time.Sleep(100 * time.Millisecond)

	// Close pipes to unblock scanners.
	stdinW0.Close()
	stdinW1.Close()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()

	// Only the first ready listener (listener-0) should have received the event.
	if !received0 {
		t.Fatal("expected listener-0 to receive the event")
	}
	if received1 {
		t.Fatal("listener-1 should not have received the event")
	}

	// listener-0 should be Busy, listener-1 should still be Ready.
	lp.mu.Lock()
	l0 := lp.listeners[0]
	l1 := lp.listeners[1]
	lp.mu.Unlock()

	l0.mu.Lock()
	state0 := l0.state
	l0.mu.Unlock()

	l1.mu.Lock()
	state1 := l1.state
	l1.mu.Unlock()

	if state0 != ListenerBusy {
		t.Fatalf("expected listener-0 to be Busy, got %d", state0)
	}
	if state1 != ListenerReady {
		t.Fatalf("expected listener-1 to still be Ready, got %d", state1)
	}

	stdoutW0.Close()
	stdoutW1.Close()
	stdinR0.Close()
	stdinR1.Close()
}

func TestNoReadyListenersDoesNotCrash(t *testing.T) {
	bus := NewBus(testLogger())
	lp := NewListenerPool("test-pool", bus, testLogger(), []EventType{ProcessStateRunning})
	defer lp.Stop()

	// Add a listener but never send READY.
	stdoutR, stdoutW := io.Pipe()
	_, stdinW := io.Pipe()
	lp.AddListener("listener-0", stdinW, stdoutR)

	// Send event with no ready listeners -- should not panic or block.
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	lp.sendToReady(Event{
		Type:      ProcessStateRunning,
		Timestamp: ts,
	})

	// Also test with zero listeners at all.
	bus2 := NewBus(testLogger())
	lp2 := NewListenerPool("empty-pool", bus2, testLogger(), []EventType{ProcessStateRunning})
	defer lp2.Stop()

	lp2.sendToReady(Event{
		Type:      ProcessStateRunning,
		Timestamp: ts,
	})

	stdoutW.Close()
	stdinW.Close()
}

func TestStopShutsDownCleanly(t *testing.T) {
	bus := NewBus(testLogger())
	lp := NewListenerPool("test-pool", bus, testLogger(), []EventType{ProcessStateRunning, ProcessStateStopped})

	// Verify subscriptions exist before stop.
	if bus.SubscriberCount(ProcessStateRunning) != 1 {
		t.Fatalf("expected 1 subscriber before stop, got %d", bus.SubscriberCount(ProcessStateRunning))
	}

	lp.Stop()

	// After stop, subscriptions should be removed.
	if bus.SubscriberCount(ProcessStateRunning) != 0 {
		t.Fatalf("expected 0 subscribers after stop, got %d", bus.SubscriberCount(ProcessStateRunning))
	}
	if bus.SubscriberCount(ProcessStateStopped) != 0 {
		t.Fatalf("expected 0 subscribers after stop, got %d", bus.SubscriberCount(ProcessStateStopped))
	}

	// done channel should be closed (non-blocking read).
	select {
	case <-lp.done:
		// Expected.
	default:
		t.Fatal("done channel not closed after Stop")
	}
}

func TestEventQueueFullDoesNotBlockPublisher(t *testing.T) {
	bus := NewBus(testLogger())

	// Create pool but do not start any listeners or drain eventCh.
	lp := &ListenerPool{
		name:    "full-pool",
		bus:     bus,
		logger:  testLogger(),
		eventCh: make(chan Event, 2), // Small buffer for testing.
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}

	// Subscribe manually to route events into the small channel.
	id := bus.Subscribe(ProcessStateRunning, func(e Event) {
		select {
		case lp.eventCh <- e:
		default:
			// Queue full; drop event. This matches NewListenerPool behavior.
		}
	})
	lp.subIDs = append(lp.subIDs, id)

	// Fill the queue.
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)
	bus.Publish(Event{Type: ProcessStateRunning, Timestamp: ts})
	bus.Publish(Event{Type: ProcessStateRunning, Timestamp: ts})

	// This publish should not block even though the queue is full.
	done := make(chan struct{})
	go func() {
		bus.Publish(Event{Type: ProcessStateRunning, Timestamp: ts})
		close(done)
	}()

	select {
	case <-done:
		// Publisher returned without blocking.
	case <-time.After(1 * time.Second):
		t.Fatal("publisher blocked on full event queue")
	}

	// Cleanup.
	bus.Unsubscribe(id)
	close(lp.stopCh)
	close(lp.done)
}

// waitForState polls until the listener at the given index reaches the expected state,
// or fails the test after the timeout.
func waitForState(t *testing.T, lp *ListenerPool, index int, expected ListenerState, timeout time.Duration) {
	t.Helper()

	deadline := time.After(timeout)
	for {
		lp.mu.Lock()
		if index >= len(lp.listeners) {
			lp.mu.Unlock()
			t.Fatalf("listener index %d out of range", index)
		}
		l := lp.listeners[index]
		lp.mu.Unlock()

		l.mu.Lock()
		state := l.state
		l.mu.Unlock()

		if state == expected {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("timed out waiting for listener %d to reach state %d (current: %d)", index, expected, state)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}
