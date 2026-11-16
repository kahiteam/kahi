package events

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// ListenerState tracks event listener process state.
type ListenerState int

const (
	ListenerAcknowledged ListenerState = iota
	ListenerReady
	ListenerBusy
)

// ListenerPool manages a pool of event listener processes that follow
// the READY/RESULT handshake protocol.
type ListenerPool struct {
	mu        sync.Mutex
	name      string
	bus       *Bus
	logger    *slog.Logger
	listeners []*Listener
	eventCh   chan Event
	stopCh    chan struct{}
	done      chan struct{}
	subIDs    []uint64

	poolSerial atomic.Uint64 // deliveries to this pool
}

// Listener represents a single event listener process.
type Listener struct {
	mu     sync.Mutex
	state  ListenerState
	stdin  io.Writer
	stdout *bufio.Scanner
	name   string
}

// NewListenerPool creates a pool that dispatches events to listener processes.
func NewListenerPool(name string, bus *Bus, logger *slog.Logger, eventTypes []EventType) *ListenerPool {
	lp := &ListenerPool{
		name:    name,
		bus:     bus,
		logger:  logger,
		eventCh: make(chan Event, 64),
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}

	// Subscribe to requested event types.
	for _, et := range eventTypes {
		id := bus.Subscribe(et, func(e Event) {
			select {
			case lp.eventCh <- e:
			default:
				logger.Warn("listener pool event queue full", "pool", name)
			}
		})
		lp.subIDs = append(lp.subIDs, id)
	}

	go lp.dispatch()
	return lp
}

// AddListener registers a listener process with its stdin/stdout pipes.
func (lp *ListenerPool) AddListener(name string, stdin io.Writer, stdout io.Reader) {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	l := &Listener{
		state:  ListenerAcknowledged,
		stdin:  stdin,
		stdout: bufio.NewScanner(stdout),
		name:   name,
	}

	lp.listeners = append(lp.listeners, l)

	// Start reading READY tokens from the listener.
	go lp.readReady(l)
}

func (lp *ListenerPool) readReady(l *Listener) {
	for l.stdout.Scan() {
		line := strings.TrimSpace(l.stdout.Text())
		if line == "READY" {
			l.mu.Lock()
			l.state = ListenerReady
			l.mu.Unlock()
			lp.logger.Debug("listener ready", "listener", l.name)
		}
	}
}

func (lp *ListenerPool) dispatch() {
	defer close(lp.done)

	for {
		select {
		case <-lp.stopCh:
			return
		case event := <-lp.eventCh:
			lp.sendToReady(event)
		}
	}
}

func (lp *ListenerPool) sendToReady(event Event) {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	for _, l := range lp.listeners {
		l.mu.Lock()
		if l.state == ListenerReady {
			l.state = ListenerBusy
			l.mu.Unlock()

			// Format event as protocol payload.
			payload := formatEventPayload(event, lp.nextEnvelope())
			if _, err := fmt.Fprint(l.stdin, payload); err != nil {
				lp.logger.Error("failed to send event to listener", "listener", l.name, "error", err)
			}
			return
		}
		l.mu.Unlock()
	}

	lp.logger.Warn("no ready listeners for event", "pool", lp.name, "event", string(event.Type))
}

// Envelope is what identifies one delivery of an event to a pool
// (FUNC-055): serial counts events across all pools, poolserial within one.
type Envelope struct {
	Serial     uint64
	Pool       string
	PoolSerial uint64
}

// eventSerial is the daemon-wide serial of events delivered to pools.
var eventSerial atomic.Uint64

// nextEnvelope numbers the pool's next delivery.
func (lp *ListenerPool) nextEnvelope() Envelope {
	return Envelope{Serial: eventSerial.Add(1), Pool: lp.name, PoolSerial: lp.poolSerial.Add(1)}
}

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
		pairs = append(pairs, k+":"+event.Data[k])
	}
	return strings.Join(pairs, " ")
}

// Stop shuts down the listener pool.
func (lp *ListenerPool) Stop() {
	close(lp.stopCh)
	<-lp.done

	for _, id := range lp.subIDs {
		lp.bus.Unsubscribe(id)
	}
}
