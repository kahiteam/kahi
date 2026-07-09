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
	"time"
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
			payload := formatEventPayload(event)
			if _, err := fmt.Fprint(l.stdin, payload); err != nil {
				lp.logger.Error("failed to send event to listener", "listener", l.name, "error", err)
			}
			return
		}
		l.mu.Unlock()
	}

	lp.logger.Warn("no ready listeners for event", "pool", lp.name, "event", string(event.Type))
}

// formatEventPayload renders an event for delivery to a listener's stdin.
//
// The wire format is a length-framed payload, mirroring supervisord: a
// trusted header line "TYPE TIMESTAMP len:N\n" is followed by exactly N
// bytes of body. The header is composed only of supervisor-controlled
// values (event type and timestamp); the body carries the event data,
// whose values may originate from managed process output and are therefore
// untrusted under a process-compromise assumption. Length-framing the body
// ensures an embedded newline or header-like text is carried as opaque data
// and cannot be parsed as a forged protocol line. An empty body is framed
// with len:0. The READY/RESULT handshake framing is unchanged.
func formatEventPayload(event Event) string {
	body := formatEventBody(event)

	var sb strings.Builder
	sb.WriteString(string(event.Type))
	sb.WriteString(" ")
	sb.WriteString(event.Timestamp.Format(time.RFC3339))
	sb.WriteString(" len:")
	sb.WriteString(strconv.Itoa(len(body)))
	sb.WriteString("\n")
	sb.WriteString(body)
	return sb.String()
}

// formatEventBody renders the space-separated key:value data pairs that make
// up the length-framed payload body. Keys are sorted for deterministic
// output. The body carries no trailing newline; the listener reads exactly
// the announced number of bytes.
func formatEventBody(event Event) string {
	if len(event.Data) == 0 {
		return ""
	}

	keys := make([]string, 0, len(event.Data))
	for k := range event.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteString(" ")
		}
		sb.WriteString(k)
		sb.WriteString(":")
		sb.WriteString(event.Data[k])
	}
	return sb.String()
}

// Stop shuts down the listener pool.
func (lp *ListenerPool) Stop() {
	close(lp.stopCh)
	<-lp.done

	for _, id := range lp.subIDs {
		lp.bus.Unsubscribe(id)
	}
}
