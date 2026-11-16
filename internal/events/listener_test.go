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
	_ = stdoutW.Close()
	_ = stdinW.Close()
}

func TestReadReadyTransitionsToReady(t *testing.T) {
	bus := NewBus(testLogger())
	lp := NewListenerPool("test-pool", bus, testLogger(), []EventType{ProcessStateRunning})
	defer lp.Stop()

	stdoutR, stdoutW := io.Pipe()
	_, stdinW := io.Pipe()

	lp.AddListener("listener-0", stdinW, stdoutR)

	// Send READY token through the stdout pipe.
	_, _ = fmt.Fprintln(stdoutW, "READY")

	// Wait for the readReady goroutine to process it.
	waitForState(t, lp, 0, ListenerReady, 2*time.Second)

	_ = stdoutW.Close()
	_ = stdinW.Close()
}

func TestDispatchRoutesEventsToSendToReady(t *testing.T) {
	bus := NewBus(testLogger())
	lp := NewListenerPool("test-pool", bus, testLogger(), []EventType{ProcessStateRunning})
	defer lp.Stop()

	stdoutR, stdoutW := io.Pipe()
	stdinR, stdinW := io.Pipe()

	lp.AddListener("listener-0", stdinW, stdoutR)

	// Signal READY.
	_, _ = fmt.Fprintln(stdoutW, "READY")

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
		if !strings.HasPrefix(f.header, "ver:3.0 server:kahi serial:") || !strings.Contains(f.header, " pool:test-pool poolserial:1 eventname:PROCESS_STATE_RUNNING len:") {
			t.Fatalf("unexpected header %q", f.header)
		}
		if f.body != "processname:web" {
			t.Fatalf("expected framed body processname:web, got %q", f.body)
		}
	case err := <-errCh:
		t.Fatalf("failed to read framed payload: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event payload on listener stdin")
	}

	_ = stdoutW.Close()
	_ = stdinR.Close()
}

// readFramedPayload parses one length-framed listener payload: a header line
// ending in "len:N" followed by exactly N body bytes. It returns the
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
	_, _ = fmt.Fprintln(stdoutW, "READY")
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

	_ = stdoutW.Close()
	_ = stdinR.Close()
}

var testEnv = Envelope{Serial: 7, Pool: "l", PoolSerial: 3}

// FUNC-055 envelope: the supervisord header with serials and pool, then the
// supervisord-compatible body (processname, groupname first, rest sorted).
func TestFormatEventPayload(t *testing.T) {
	event := Event{Type: ProcessStateRunning, Timestamp: time.Now(),
		Data: map[string]string{"name": "web", "group": "g", "pid": "123", "from_state": "STARTING"}}
	body := "processname:web groupname:g from_state:STARTING pid:123"
	want := fmt.Sprintf("ver:3.0 server:kahi serial:7 pool:l poolserial:3 eventname:PROCESS_STATE_RUNNING len:%d\n%s", len(body), body)
	if got := formatEventPayload(event, testEnv); got != want {
		t.Fatalf("payload\n got %q\nwant %q", got, want)
	}
}

// An empty payload body is framed with len:0 and no body bytes.
func TestFormatEventPayloadEmptyData(t *testing.T) {
	got := formatEventPayload(Event{Type: ProcessStateStopped}, testEnv)
	if want := "ver:3.0 server:kahi serial:7 pool:l poolserial:3 eventname:PROCESS_STATE_STOPPED len:0\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// A log event's body is supervisord's: a header line naming the process and
// channel, then the raw output; an empty output line is one newline byte.
func TestFormatEventPayloadLogEvent(t *testing.T) {
	for _, tc := range []struct {
		event Event
		body  string
	}{
		{Event{Type: ProcessLogStdout, Data: map[string]string{"name": "web", "group": "web", "data": "hello\n", "id": "g-6"}},
			"processname:web groupname:web channel:stdout\nhello\n"},
		{Event{Type: ProcessLogStderr, Data: map[string]string{"name": "web", "data": "\n"}},
			"processname:web channel:stderr\n\n"},
	} {
		_, body, err := readFramedPayload(bufio.NewReader(strings.NewReader(formatEventPayload(tc.event, testEnv))))
		if err != nil || body != tc.body {
			t.Errorf("%s: body %q (%v), want %q", tc.event.Type, body, err, tc.body)
		}
	}
}

// TestFormatEventPayloadFramesEmbeddedNewline verifies that output containing
// an embedded newline and header-like text is carried as opaque body bytes
// and cannot be parsed as a forged protocol line (SEC-020).
func TestFormatEventPayloadFramesEmbeddedNewline(t *testing.T) {
	malicious := "x\nver:3.0 server:kahi serial:999 pool:l poolserial:1 eventname:PROCESS_STATE_FATAL len:0\n"
	event := Event{Type: ProcessLogStdout, Data: map[string]string{"name": "web", "data": malicious}}
	payload := formatEventPayload(event, testEnv)

	firstLine := payload[:strings.IndexByte(payload, '\n')]
	if strings.Contains(firstLine, "serial:999") || !strings.Contains(firstLine, "eventname:PROCESS_LOG_STDOUT") {
		t.Fatalf("unexpected header line: %q", firstLine)
	}
	r := bufio.NewReader(strings.NewReader(payload))
	_, body, err := readFramedPayload(r)
	if err != nil {
		t.Fatal(err)
	}
	if body != "processname:web channel:stdout\n"+malicious {
		t.Fatalf("body not recovered verbatim: %q", body)
	}
	if rest, _ := io.ReadAll(r); len(rest) != 0 {
		t.Fatalf("bytes left after the frame: %q", rest)
	}
}

// Two framed payloads back to back are each recoverable: a length-framed
// body cannot desynchronize the stream even with newlines in it.
func TestFormatEventPayloadFrameConcatenation(t *testing.T) {
	first := formatEventPayload(Event{Type: ProcessLogStdout, Data: map[string]string{"name": "a", "data": "a\nb"}}, testEnv)
	second := formatEventPayload(Event{Type: ProcessStateStopped, Data: map[string]string{"name": "web"}}, Envelope{Serial: 8, Pool: "l", PoolSerial: 4})
	r := bufio.NewReader(strings.NewReader(first + second))
	h1, b1, err := readFramedPayload(r)
	if err != nil || !strings.Contains(h1, "serial:7 ") || b1 != "processname:a channel:stdout\na\nb" {
		t.Fatalf("first frame: header=%q body=%q err=%v", h1, b1, err)
	}
	h2, b2, err := readFramedPayload(r)
	if err != nil || !strings.Contains(h2, "serial:8 ") || !strings.Contains(h2, "eventname:PROCESS_STATE_STOPPED") || b2 != "processname:web" {
		t.Fatalf("second frame: header=%q body=%q err=%v", h2, b2, err)
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
	_, _ = fmt.Fprintln(stdoutW0, "READY")
	_, _ = fmt.Fprintln(stdoutW1, "READY")
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
	_ = stdinW0.Close()
	_ = stdinW1.Close()
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

	_ = stdoutW0.Close()
	_ = stdoutW1.Close()
	_ = stdinR0.Close()
	_ = stdinR1.Close()
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

	_ = stdoutW.Close()
	_ = stdinW.Close()
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
