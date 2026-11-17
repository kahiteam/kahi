package events

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"
)

func (s *syncBuf) wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(s.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("log never contained %q:\n%s", want, s.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// fakeMember is a listener process: the pool writes to its stdin and reads
// its stdout.
type fakeMember struct {
	name    string
	in      *bufio.Reader // what the pool wrote
	out     io.WriteCloser
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	frames  chan [2]string
}

func newMember(t *testing.T, p *Pool, name string) *fakeMember {
	t.Helper()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	m := &fakeMember{name: name, in: bufio.NewReader(stdinR), out: stdoutW, stdinW: stdinW, stdoutR: stdoutR, frames: make(chan [2]string, 16)}
	go func() {
		for {
			h, b, err := readFramedPayload(m.in)
			if err != nil {
				close(m.frames)
				return
			}
			m.frames <- [2]string{h, b}
		}
	}()
	p.Attach(name, stdinW, stdoutR)
	t.Cleanup(func() { _ = stdinW.Close(); _ = stdoutW.Close() })
	return m
}

func (m *fakeMember) say(t *testing.T, s string) {
	t.Helper()
	if _, err := io.WriteString(m.out, s); err != nil {
		t.Fatalf("%s: write %q: %v", m.name, s, err)
	}
}

func (m *fakeMember) ready(t *testing.T) { t.Helper(); m.say(t, "READY\n") }

// next returns the next event header the member received.
func (m *fakeMember) next(t *testing.T) (header, body string) {
	t.Helper()
	select {
	case f, ok := <-m.frames:
		if !ok {
			t.Fatalf("%s: stdin closed", m.name)
		}
		return f[0], f[1]
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: no event within 5s", m.name)
	}
	return "", ""
}

// quiet asserts the member gets no event for a short while.
func (m *fakeMember) quiet(t *testing.T) {
	t.Helper()
	select {
	case f := <-m.frames:
		t.Fatalf("%s: unexpected event %q", m.name, f[0])
	case <-time.After(100 * time.Millisecond):
	}
}

func field(header, key string) string {
	for _, f := range strings.Fields(header) {
		if v, ok := strings.CutPrefix(f, key+":"); ok {
			return v
		}
	}
	return ""
}

func newTestPool(t *testing.T, cfg PoolConfig) (*Pool, *Bus, *syncBuf) {
	t.Helper()
	logs := &syncBuf{}
	cfg.Logger = slog.New(slog.NewTextHandler(logs, nil))
	if cfg.Name == "" {
		cfg.Name = "l"
	}
	if cfg.Types == nil {
		cfg.Types = []EventType{ProcessStateExited}
	}
	if cfg.BufferSize == 0 {
		cfg.BufferSize = 10
	}
	bus := NewBus(testLogger())
	p := NewPool(bus, cfg)
	t.Cleanup(p.Stop)
	return p, bus, logs
}

func exited(name string) Event {
	return Event{Type: ProcessStateExited, Data: map[string]string{"name": name, "group": name}}
}

func TestPoolDeliversAndAcknowledges(t *testing.T) {
	p, bus, _ := newTestPool(t, PoolConfig{})
	m := newMember(t, p, "l")
	m.ready(t)
	bus.Publish(exited("a"))
	h, body := m.next(t)
	if !strings.HasPrefix(h, "ver:3.0 server:kahi serial:") || field(h, "pool") != "l" || field(h, "poolserial") != "1" || field(h, "eventname") != "PROCESS_STATE_EXITED" {
		t.Fatalf("header %q", h)
	}
	if body != "processname:a groupname:a" {
		t.Fatalf("body %q", body)
	}
	first, _ := strconv.Atoi(field(h, "serial"))
	m.say(t, "RESULT 2\nOK")
	m.ready(t)
	bus.Publish(exited("b"))
	h, _ = m.next(t)
	if n, _ := strconv.Atoi(field(h, "serial")); n != first+1 || field(h, "poolserial") != "2" {
		t.Fatalf("second header %q, first serial %d", h, first)
	}
}

// Only the pool's event types are delivered.
func TestPoolFiltersTypes(t *testing.T) {
	p, bus, _ := newTestPool(t, PoolConfig{})
	m := newMember(t, p, "l")
	m.ready(t)
	bus.Publish(Event{Type: ProcessStateRunning, Data: map[string]string{"name": "x"}})
	m.quiet(t)
}

func TestPoolFailRedeliversThenDrops(t *testing.T) {
	p, bus, logs := newTestPool(t, PoolConfig{})
	m := newMember(t, p, "l")
	m.ready(t)
	bus.Publish(exited("a"))
	bus.Publish(exited("b"))
	var serial string
	for i := 0; i < 3; i++ {
		h, _ := m.next(t)
		if i == 0 {
			serial = field(h, "serial")
		} else if field(h, "serial") != serial {
			t.Fatalf("delivery %d: serial %s, want %s (kept on redelivery)", i+1, field(h, "serial"), serial)
		}
		m.say(t, "RESULT 4\nFAIL")
		m.ready(t)
	}
	logs.wait(t, fmt.Sprintf("event %s for l dropped after 3 failures", serial))
	h, body := m.next(t)
	if field(h, "serial") == serial || !strings.Contains(body, "processname:b") {
		t.Fatalf("after the drop: %q %q", h, body)
	}
}

func TestPoolInvalidResultRedelivers(t *testing.T) {
	p, bus, logs := newTestPool(t, PoolConfig{})
	m := newMember(t, p, "l")
	m.ready(t)
	bus.Publish(exited("a"))
	h, _ := m.next(t)
	m.say(t, "garbage\n")
	logs.wait(t, "invalid result from listener l: garbage")
	m.ready(t)
	h2, _ := m.next(t)
	if field(h2, "serial") != field(h, "serial") {
		t.Fatalf("redelivered %q, first %q", h2, h)
	}
	m.say(t, "RESULT 3\nBAD")
	logs.wait(t, "invalid result from listener l: BAD")
}

func TestPoolTimeoutStopsMemberAndRedelivers(t *testing.T) {
	timedOut := make(chan string, 1)
	p, bus, logs := newTestPool(t, PoolConfig{ResultTimeout: 50 * time.Millisecond, OnTimeout: func(m string) { timedOut <- m }})
	m := newMember(t, p, "l")
	m.ready(t)
	bus.Publish(exited("a"))
	h, _ := m.next(t)
	select {
	case got := <-timedOut:
		if got != "l" {
			t.Fatalf("OnTimeout(%q)", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnTimeout not called")
	}
	logs.wait(t, "listener l timed out on event "+field(h, "serial"))
	// A late READY from the timed-out member is ignored.
	m.ready(t)
	m.quiet(t)
	// Its restart gets the event again.
	m2 := newMember(t, p, "l")
	m2.ready(t)
	h2, _ := m2.next(t)
	if field(h2, "serial") != field(h, "serial") {
		t.Fatalf("restart got %q, want serial %s", h2, field(h, "serial"))
	}
}

func TestPoolMemberExitRequeues(t *testing.T) {
	p, bus, logs := newTestPool(t, PoolConfig{})
	m := newMember(t, p, "l")
	m.ready(t)
	bus.Publish(exited("a"))
	h, _ := m.next(t)
	_ = m.out.Close() // the process exits: EOF on its stdout
	logs.wait(t, "event listener l exited unexpectedly")
	m2 := newMember(t, p, "l")
	m2.ready(t)
	h2, _ := m2.next(t)
	if field(h2, "serial") != field(h, "serial") {
		t.Fatalf("new member got %q, want serial %s", h2, field(h, "serial"))
	}
}

func TestPoolRoundRobin(t *testing.T) {
	p, bus, _ := newTestPool(t, PoolConfig{})
	ms := []*fakeMember{newMember(t, p, "l_0"), newMember(t, p, "l_1"), newMember(t, p, "l_2")}
	for _, m := range ms {
		m.ready(t)
	}
	time.Sleep(50 * time.Millisecond) // all three READY before the events
	for _, n := range []string{"a", "b", "c"} {
		bus.Publish(exited(n))
	}
	for _, m := range ms {
		m.next(t)
		m.say(t, "RESULT 2\nOK")
		m.ready(t)
	}
	time.Sleep(50 * time.Millisecond)
	bus.Publish(exited("d"))
	_, body := ms[0].next(t)
	if !strings.Contains(body, "processname:d") {
		t.Fatalf("4th event body %q", body)
	}
	ms[1].quiet(t)
	ms[2].quiet(t)
}

func TestPoolBufferOverflowDropsOldest(t *testing.T) {
	p, bus, logs := newTestPool(t, PoolConfig{BufferSize: 2})
	for _, n := range []string{"a", "b", "c"} {
		bus.Publish(exited(n))
	}
	bus.Flush()
	logs.wait(t, "event buffer overflow for l, dropped 1 oldest events")
	if n := p.Buffered(); n != 2 {
		t.Fatalf("buffered %d, want 2", n)
	}
	m := newMember(t, p, "l")
	m.ready(t)
	_, b1 := m.next(t)
	m.say(t, "RESULT 2\nOK")
	m.ready(t)
	_, b2 := m.next(t)
	if !strings.Contains(b1, "processname:b") || !strings.Contains(b2, "processname:c") {
		t.Fatalf("delivered %q then %q, want b then c", b1, b2)
	}
}

// A re-queued event is never the one dropped on overflow.
func TestPoolRequeuedEventSurvivesOverflow(t *testing.T) {
	p, bus, _ := newTestPool(t, PoolConfig{BufferSize: 2})
	m := newMember(t, p, "l")
	m.ready(t)
	bus.Publish(exited("a"))
	m.next(t)
	m.say(t, "RESULT 4\nFAIL") // a goes back to the head; m is not READY
	time.Sleep(50 * time.Millisecond)
	for _, n := range []string{"b", "c", "d"} {
		bus.Publish(exited(n))
	}
	bus.Flush()
	m.ready(t)
	_, body := m.next(t)
	if !strings.Contains(body, "processname:a") {
		t.Fatalf("first after overflow %q, want the re-queued a", body)
	}
	m.say(t, "RESULT 2\nOK")
	m.ready(t)
	_, body = m.next(t)
	if !strings.Contains(body, "processname:d") {
		t.Fatalf("second %q, want d (b and c dropped)", body)
	}
}

// A member released by the supervisor (reload) gives its event back without
// a failure.
func TestPoolReleaseRequeuesWithoutFailure(t *testing.T) {
	p, bus, logs := newTestPool(t, PoolConfig{})
	m := newMember(t, p, "l")
	m.ready(t)
	bus.Publish(exited("a"))
	h, _ := m.next(t)
	m.say(t, "RESULT 4\nFAIL")
	m.ready(t)
	m.next(t) // second delivery
	p.Release("l")
	m2 := newMember(t, p, "l")
	m2.ready(t)
	h2, _ := m2.next(t) // third delivery, still not dropped
	if field(h2, "serial") != field(h, "serial") {
		t.Fatalf("after release %q", h2)
	}
	if strings.Contains(logs.String(), "dropped") || strings.Contains(logs.String(), "exited unexpectedly") {
		t.Fatalf("release counted as a failure:\n%s", logs.String())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// A failed stdin write marks the member failed, logs and re-queues (SEC-020).
func TestPoolWriteErrorRequeues(t *testing.T) {
	p, bus, logs := newTestPool(t, PoolConfig{})
	stdoutR, stdoutW := io.Pipe()
	t.Cleanup(func() { _ = stdoutW.Close() })
	p.Attach("bad", failWriter{}, stdoutR)
	if _, err := io.WriteString(stdoutW, "READY\n"); err != nil {
		t.Fatal(err)
	}
	bus.Publish(exited("a"))
	logs.wait(t, "cannot write event to listener bad: broken pipe")
	m := newMember(t, p, "good")
	m.ready(t)
	_, body := m.next(t)
	if !strings.Contains(body, "processname:a") {
		t.Fatalf("body %q", body)
	}
}

func TestPoolStopDiscardsAndUnsubscribes(t *testing.T) {
	logs := &syncBuf{}
	bus := NewBus(testLogger())
	p := NewPool(bus, PoolConfig{Name: "l", Types: []EventType{ProcessStateExited}, BufferSize: 5, Logger: slog.New(slog.NewTextHandler(logs, nil))})
	bus.Publish(exited("a"))
	bus.Flush()
	if p.Buffered() != 1 {
		t.Fatalf("buffered %d", p.Buffered())
	}
	p.Stop()
	p.Stop() // idempotent
	if n := bus.SubscriberCount(ProcessStateExited); n != 0 {
		t.Fatalf("%d subscribers after Stop", n)
	}
	if p.Buffered() != 0 {
		t.Fatal("buffer kept after Stop")
	}
	// Attach and Release after Stop return.
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	p.Attach("x", io.Discard, r)
	p.Release("x")
}

// readFramedPayload parses one length-framed listener payload: a header line
// ending in "len:N" followed by exactly N body bytes.
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

// supervisord sends expected as 0 or 1, and listeners int() it.
func TestFormatEventBodyExpected(t *testing.T) {
	for v, want := range map[string]string{"true": "expected:1", "false": "expected:0"} {
		body := formatEventBody(Event{Type: ProcessStateExited, Data: map[string]string{"name": "w", "expected": v}})
		if !strings.Contains(body, want) {
			t.Errorf("expected=%s: body %q, want %s", v, body, want)
		}
	}
}

// A reload's new settings keep the buffered events (FUNC-055).
func TestPoolReconfigureKeepsBuffer(t *testing.T) {
	p, bus, _ := newTestPool(t, PoolConfig{})
	bus.Publish(exited("a"))
	bus.Publish(exited("b"))
	bus.Flush()
	p.Reconfigure(PoolConfig{Name: "l", Types: []EventType{ProcessStateExited, ProcessStateFatal}, BufferSize: 5, ResultTimeout: time.Minute})
	bus.Publish(Event{Type: ProcessStateFatal, Data: map[string]string{"name": "c"}})
	bus.Flush()
	if n := p.Buffered(); n != 3 {
		t.Fatalf("buffered %d, want 3", n)
	}
	p.Reconfigure(PoolConfig{Name: "l", Types: []EventType{ProcessStateExited, ProcessStateFatal}, BufferSize: 2})
	m := newMember(t, p, "l")
	m.ready(t)
	_, body := m.next(t)
	if !strings.Contains(body, "processname:b") {
		t.Fatalf("after shrinking to 2: %q, want b (a dropped)", body)
	}
}
