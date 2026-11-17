package events

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ListenerState is an event listener member's protocol state (ADR-042).
type ListenerState int

const (
	// ListenerAcknowledged: started, or answered its last event; waits for
	// the member's READY.
	ListenerAcknowledged ListenerState = iota
	// ListenerReady: the member wrote READY and waits for an event.
	ListenerReady
	// ListenerBusy: the member holds an event and owes a RESULT.
	ListenerBusy
)

// maxFailures is how often an event may fail before it is dropped (FUNC-055).
const maxFailures = 3

// PoolConfig configures a listener pool (FUNC-055, ADR-042).
type PoolConfig struct {
	Name          string
	Types         []EventType
	BufferSize    int
	ResultTimeout time.Duration
	Logger        *slog.Logger
	// OnTimeout stops a member that did not answer within ResultTimeout,
	// with its stopsignal; it is restarted per its autorestart policy. It
	// runs on its own goroutine.
	OnTimeout func(member string)
}

// pending is one buffered or in-flight event with the envelope it got when
// it entered the buffer; the envelope is kept on redelivery.
type pending struct {
	event    Event
	env      Envelope
	failures int
	requeued bool
}

type member struct {
	name  string
	gen   uint64
	stdin io.Writer
	state ListenerState
	busy  *pending
	timer *time.Timer
	dead  bool // timed out or a write failed: no events until it is replaced
}

type msgKind int

const (
	msgAttach msgKind = iota
	msgRelease
	msgReady
	msgResult
	msgInvalid
	msgEOF
	msgTimeout
	msgWriteErr
)

type memberMsg struct {
	kind  msgKind
	name  string
	gen   uint64
	data  string
	stdin io.Writer
	done  chan struct{} // closed when an attach or release is applied
}

// Pool dispatches events to the members of one [eventlisteners.x] pool
// over the supervisord READY/RESULT protocol (FUNC-055, ADR-042). Events of
// the pool's types enter its buffer of BufferSize (the oldest buffered event
// is dropped when it is full, never a re-queued one) and go round-robin to
// READY members. A member answers "RESULT {len}\n" and len bytes: OK
// acknowledges, FAIL, anything else, a timeout or the member exiting
// re-queues the event at the head of the buffer, and the third failure
// drops it.
type Pool struct {
	cfg    PoolConfig
	bus    *Bus
	subID  uint64
	logger *slog.Logger

	mu         sync.Mutex
	buf        []*pending
	poolSerial uint64
	dropped    int           // drops not logged yet
	drops      atomic.Uint64 // every drop, for kahi_event_buffer_drops_total
	lastRep    time.Time
	repTimer   *time.Timer
	stopped    bool

	wake chan struct{}
	msgs chan memberMsg
	stop chan struct{}
	done chan struct{}

	// Owned by run.
	members []*member
	rr      int
	nextGen atomic.Uint64
}

// NewPool subscribes a pool to its event types and starts its dispatcher.
func NewPool(bus *Bus, cfg PoolConfig) *Pool {
	if cfg.BufferSize < 1 {
		cfg.BufferSize = 1
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	p := &Pool{
		cfg:     cfg,
		bus:     bus,
		logger:  cfg.Logger,
		lastRep: time.Now().Add(-overflowReportInterval),
		wake:    make(chan struct{}, 1),
		msgs:    make(chan memberMsg, 64),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go p.run()
	p.subID = bus.SubscribeQueue("listener pool "+cfg.Name, cfg.Types, QueueSize, p.enqueue, nil)
	return p
}

// Reconfigure applies a reload's new settings to a pool that keeps running,
// with its buffered events (FUNC-055): new event types resubscribe (the new
// subscription starts before the old one ends, so no event is lost; one
// published in between may be buffered twice), a smaller buffer drops its
// oldest delivered-never events, and a new result_timeout applies from the
// next delivery.
func (p *Pool) Reconfigure(cfg PoolConfig) {
	if cfg.BufferSize < 1 {
		cfg.BufferSize = 1
	}
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	oldTypes := p.cfg.Types
	p.cfg.BufferSize = cfg.BufferSize
	p.cfg.ResultTimeout = cfg.ResultTimeout
	p.cfg.OnTimeout = cfg.OnTimeout
	for len(p.buf) > p.cfg.BufferSize {
		i := 0
		for i < len(p.buf)-1 && p.buf[i].requeued {
			i++
		}
		p.buf = append(p.buf[:i], p.buf[i+1:]...)
		p.dropped++
		p.drops.Add(1)
	}
	if p.dropped > 0 {
		p.reportDropsLocked()
	}
	p.mu.Unlock()
	if !sameTypes(oldTypes, cfg.Types) {
		id := p.bus.SubscribeQueue("listener pool "+p.cfg.Name, cfg.Types, QueueSize, p.enqueue, nil)
		p.mu.Lock()
		old := p.subID
		p.subID = id
		p.cfg.Types = cfg.Types
		p.mu.Unlock()
		p.bus.Unsubscribe(old)
	}
}

func sameTypes(a, b []EventType) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[EventType]bool, len(a))
	for _, t := range a {
		set[t] = true
	}
	for _, t := range b {
		if !set[t] {
			return false
		}
	}
	return true
}

// enqueue puts an event into the buffer and numbers it (bus goroutine).
func (p *Pool) enqueue(e Event) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.poolSerial++
	pe := &pending{event: e, env: Envelope{Serial: eventSerial.Add(1), Pool: p.cfg.Name, PoolSerial: p.poolSerial}}
	if len(p.buf) >= p.cfg.BufferSize {
		// Drop the oldest event that was never delivered; re-queued events
		// at the head are kept. With only re-queued events buffered, the new
		// one is dropped.
		i := 0
		for i < len(p.buf) && p.buf[i].requeued {
			i++
		}
		if i < len(p.buf) {
			p.buf = append(p.buf[:i], p.buf[i+1:]...)
			p.buf = append(p.buf, pe)
		}
		p.dropped++
		p.drops.Add(1)
		p.reportDropsLocked()
	} else {
		p.buf = append(p.buf, pe)
	}
	p.mu.Unlock()
	p.signal()
}

// reportDropsLocked logs the drops at most once per overflowReportInterval
// (one WARN per burst, FUNC-055).
func (p *Pool) reportDropsLocked() {
	if p.repTimer != nil {
		return
	}
	wait := overflowReportInterval - time.Since(p.lastRep)
	if wait <= 0 {
		p.logDropsLocked()
		return
	}
	p.repTimer = time.AfterFunc(wait, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.repTimer = nil
		if !p.stopped {
			p.logDropsLocked()
		}
	})
}

func (p *Pool) logDropsLocked() {
	if p.dropped == 0 {
		return
	}
	p.logger.Warn(fmt.Sprintf("event buffer overflow for %s, dropped %d oldest events", p.cfg.Name, p.dropped))
	p.dropped = 0
	p.lastRep = time.Now()
}

func (p *Pool) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Attach adds a member, or replaces a member of that name (a restart), with
// the pipes of its new process. The member's stdout carries the protocol and
// is read by the pool until EOF.
func (p *Pool) Attach(name string, stdin io.Writer, stdout io.Reader) {
	gen := p.nextGen.Add(1)
	done := make(chan struct{})
	if !p.send(memberMsg{kind: msgAttach, name: name, gen: gen, stdin: stdin, done: done}) || !p.await(done) {
		return
	}
	go p.read(name, gen, stdout)
}

// Release removes a member whose process is being restarted or removed by
// the supervisor (a reload): its in-flight event is re-queued without
// counting as a failure.
func (p *Pool) Release(name string) {
	done := make(chan struct{})
	if p.send(memberMsg{kind: msgRelease, name: name, done: done}) {
		p.await(done)
	}
}

// await waits for the dispatcher to apply a message; false when the pool
// stopped first.
func (p *Pool) await(done chan struct{}) bool {
	select {
	case <-done:
		return true
	case <-p.stop:
		return false
	}
}

func (p *Pool) send(m memberMsg) bool {
	select {
	case <-p.stop:
		return false
	default:
	}
	select {
	case p.msgs <- m:
		return true
	case <-p.stop:
		return false
	}
}

// read parses a member's stdout: "READY", or "RESULT {len}" followed by
// len bytes, or anything else (an invalid result).
func (p *Pool) read(name string, gen uint64, stdout io.Reader) {
	r := bufio.NewReader(stdout)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if line != "" {
				p.send(memberMsg{kind: msgInvalid, name: name, gen: gen, data: strings.TrimRight(line, "\r\n")})
			}
			p.send(memberMsg{kind: msgEOF, name: name, gen: gen})
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "READY":
			p.send(memberMsg{kind: msgReady, name: name, gen: gen})
		case strings.HasPrefix(line, "RESULT "):
			n, perr := strconv.Atoi(strings.TrimPrefix(line, "RESULT "))
			if perr != nil || n < 0 || n > 1<<20 {
				p.send(memberMsg{kind: msgInvalid, name: name, gen: gen, data: line})
				continue
			}
			body := make([]byte, n)
			if _, err := io.ReadFull(r, body); err != nil {
				p.send(memberMsg{kind: msgEOF, name: name, gen: gen})
				return
			}
			p.send(memberMsg{kind: msgResult, name: name, gen: gen, data: string(body)})
		default:
			p.send(memberMsg{kind: msgInvalid, name: name, gen: gen, data: line})
		}
	}
}

// Stop unsubscribes the pool and ends its dispatcher. Buffered events are
// discarded; members are not touched (the supervisor stops them).
func (p *Pool) Stop() {
	p.mu.Lock()
	id := p.subID
	p.mu.Unlock()
	p.bus.Unsubscribe(id)
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.buf = nil
	if p.repTimer != nil {
		p.repTimer.Stop()
		p.repTimer = nil
	}
	p.mu.Unlock()
	close(p.stop)
	<-p.done
}

// Drops returns the number of events the pool dropped because its buffer
// was full, since it was created (FUNC-060).
func (p *Pool) Drops() uint64 { return p.drops.Load() }

// Buffered returns the number of buffered events (not in flight).
func (p *Pool) Buffered() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.buf)
}

func (p *Pool) run() {
	defer close(p.done)
	defer func() {
		for _, m := range p.members {
			if m.timer != nil {
				m.timer.Stop()
			}
		}
	}()
	for {
		select {
		case <-p.stop:
			return
		case <-p.wake:
		case m := <-p.msgs:
			p.handle(m)
		}
		p.dispatch()
	}
}

func (p *Pool) find(name string) (int, *member) {
	for i, m := range p.members {
		if m.name == name {
			return i, m
		}
	}
	return -1, nil
}

func (p *Pool) handle(msg memberMsg) {
	i, m := p.find(msg.name)
	switch msg.kind {
	case msgAttach:
		if m != nil {
			p.gone(m, false)
			m.gen, m.stdin, m.state, m.dead = msg.gen, msg.stdin, ListenerAcknowledged, false
		} else {
			p.members = append(p.members, &member{name: msg.name, gen: msg.gen, stdin: msg.stdin})
			sort.Slice(p.members, func(a, b int) bool { return p.members[a].name < p.members[b].name })
		}
		close(msg.done)
		return
	case msgRelease:
		if m != nil {
			p.gone(m, true)
			p.members = append(p.members[:i], p.members[i+1:]...)
		}
		close(msg.done)
		return
	}
	if m == nil || m.gen != msg.gen {
		return // a message from a replaced or released member
	}
	switch msg.kind {
	case msgEOF:
		p.gone(m, false)
		p.members = append(p.members[:i], p.members[i+1:]...)
	case msgReady:
		if m.dead {
			return
		}
		if m.state == ListenerBusy {
			p.invalid(m, "READY")
			return
		}
		m.state = ListenerReady
	case msgResult:
		if m.state != ListenerBusy {
			p.logger.Warn(fmt.Sprintf("invalid result from listener %s: %s (no event in flight)", m.name, msg.data))
			return
		}
		switch msg.data {
		case "OK":
			p.finish(m)
		case "FAIL":
			pe := p.finish(m)
			p.fail(pe)
		default:
			p.invalid(m, msg.data)
		}
	case msgInvalid:
		if m.state == ListenerBusy {
			p.invalid(m, msg.data)
			return
		}
		p.logger.Warn(fmt.Sprintf("unexpected output from listener %s: %s", m.name, msg.data))
	case msgTimeout:
		if m.state != ListenerBusy || m.dead {
			return
		}
		p.logger.Error(fmt.Sprintf("listener %s timed out on event %d", m.name, m.busy.env.Serial))
		pe := p.finish(m)
		m.dead = true
		p.fail(pe)
		p.mu.Lock()
		onTimeout := p.cfg.OnTimeout
		p.mu.Unlock()
		if onTimeout != nil {
			go onTimeout(m.name)
		}
	case msgWriteErr:
		if m.state != ListenerBusy {
			return
		}
		p.logger.Error(fmt.Sprintf("cannot write event to listener %s: %s", m.name, msg.data))
		pe := p.finish(m)
		m.dead = true
		p.fail(pe)
	}
}

// invalid handles a malformed or unknown result from a busy member.
func (p *Pool) invalid(m *member, data string) {
	p.logger.Error(fmt.Sprintf("invalid result from listener %s: %s", m.name, data))
	pe := p.finish(m)
	p.fail(pe)
}

// finish ends the member's delivery and returns the event it held.
func (p *Pool) finish(m *member) *pending {
	pe := m.busy
	m.busy = nil
	m.state = ListenerAcknowledged
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	return pe
}

// gone handles a member whose process went away: an event it held fails,
// or, on a release by the supervisor, goes back unchanged.
func (p *Pool) gone(m *member, release bool) {
	if m.state != ListenerBusy {
		return
	}
	pe := p.finish(m)
	if release {
		p.requeue(pe)
		return
	}
	p.logger.Error(fmt.Sprintf("event listener %s exited unexpectedly", m.name))
	p.fail(pe)
}

func (p *Pool) fail(pe *pending) {
	pe.failures++
	if pe.failures >= maxFailures {
		p.logger.Error(fmt.Sprintf("event %d for %s dropped after %d failures", pe.env.Serial, p.cfg.Name, maxFailures))
		return
	}
	p.requeue(pe)
}

// requeue puts an event back at the head of the buffer (ADR-042).
func (p *Pool) requeue(pe *pending) {
	pe.requeued = true
	p.mu.Lock()
	if !p.stopped {
		p.buf = append([]*pending{pe}, p.buf...)
	}
	p.mu.Unlock()
}

// dispatch hands buffered events to READY members, round-robin.
func (p *Pool) dispatch() {
	for {
		p.mu.Lock()
		if len(p.buf) == 0 {
			p.mu.Unlock()
			return
		}
		m := p.nextReady()
		if m == nil {
			p.mu.Unlock()
			return
		}
		pe := p.buf[0]
		p.buf = p.buf[1:]
		timeout := p.cfg.ResultTimeout
		p.mu.Unlock()

		m.state = ListenerBusy
		m.busy = pe
		name, gen, stdin := m.name, m.gen, m.stdin
		if timeout > 0 {
			m.timer = time.AfterFunc(timeout, func() {
				p.send(memberMsg{kind: msgTimeout, name: name, gen: gen})
			})
		}
		payload := formatEventPayload(pe.event, pe.env)
		// Written off the dispatcher, so a member that stops reading its
		// stdin cannot stall the pool; its timeout covers it.
		go func() {
			if _, err := io.WriteString(stdin, payload); err != nil {
				p.send(memberMsg{kind: msgWriteErr, name: name, gen: gen, data: err.Error()})
			}
		}()
	}
}

// nextReady picks the next READY member after the last one served.
func (p *Pool) nextReady() *member {
	n := len(p.members)
	for k := 0; k < n; k++ {
		i := (p.rr + k) % n
		if m := p.members[i]; m.state == ListenerReady && !m.dead {
			p.rr = (i + 1) % n
			return m
		}
	}
	return nil
}

// Envelope is what identifies one delivery of an event to a pool
// (FUNC-055): serial counts events across all pools, poolserial within one.
type Envelope struct {
	Serial     uint64
	Pool       string
	PoolSerial uint64
}

// eventSerial numbers events for pools daemon-wide. A pool assigns a serial
// once per event, when the event enters its buffer, and keeps it on
// redelivery (ADR-042).
var eventSerial atomic.Uint64

// formatEventPayload renders an event for delivery to a listener's stdin
// (FUNC-055, SEC-020, ADR-023, ADR-042).
//
// The wire format is supervisord's: a header line
// "ver:3.0 server:kahi serial:N pool:P poolserial:N eventname:TYPE len:N\n"
// built only from supervisor-controlled values, followed by exactly len bytes
// of body. The body carries event data, whose values may come from managed
// process output and are therefore untrusted; length-framing it keeps an
// embedded newline or header-like text opaque, so it cannot be parsed as a
// forged protocol line. An empty body is framed with len:0. The READY/RESULT
// handshake framing is unchanged.
func formatEventPayload(event Event, env Envelope) string {
	body := formatEventBody(event)
	return fmt.Sprintf("ver:3.0 server:kahi serial:%d pool:%s poolserial:%d eventname:%s len:%d\n%s",
		env.Serial, env.Pool, env.PoolSerial, event.Type, len(body), body)
}

// formatEventBody renders the payload body in supervisord's format, so
// supervisord listeners (superlance and the like) parse it unchanged
// (decided 2026-11-16): space-separated key:value pairs with Kahi's name and
// group as processname and groupname, those two first and the rest sorted.
// A log event's body is "processname:x groupname:y [pid:N] channel:stdout"
// followed by a newline and the raw output. The body carries no trailing
// newline of its own; the listener reads exactly the announced length.
func formatEventBody(event Event) string {
	if event.Type == ProcessLogStdout || event.Type == ProcessLogStderr {
		channel := "stdout"
		if event.Type == ProcessLogStderr {
			channel = "stderr"
		}
		var head []string
		for _, kv := range [][2]string{{"processname", event.Data["name"]}, {"groupname", event.Data["group"]}, {"pid", event.Data["pid"]}} {
			if kv[1] != "" {
				head = append(head, kv[0]+":"+kv[1])
			}
		}
		head = append(head, "channel:"+channel)
		return strings.Join(head, " ") + "\n" + event.Data["data"]
	}
	if len(event.Data) == 0 {
		return ""
	}
	var pairs []string
	for _, kv := range [][2]string{{"processname", "name"}, {"groupname", "group"}} {
		if v, ok := event.Data[kv[1]]; ok {
			pairs = append(pairs, kv[0]+":"+v)
		}
	}
	keys := make([]string, 0, len(event.Data))
	for k := range event.Data {
		if k != "name" && k != "group" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := event.Data[k]
		// supervisord sends expected as 0 or 1; listeners int() it.
		if k == "expected" {
			switch v {
			case "true":
				v = "1"
			case "false":
				v = "0"
			}
		}
		pairs = append(pairs, k+":"+v)
	}
	return strings.Join(pairs, " ")
}
