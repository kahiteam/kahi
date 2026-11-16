// Package events provides a publish-subscribe event bus for process
// lifecycle notifications within the Kahi supervisor.
package events

import (
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// EventType identifies a specific event category.
type EventType string

// Process state events.
const (
	ProcessStateStopped  EventType = "PROCESS_STATE_STOPPED"
	ProcessStateStarting EventType = "PROCESS_STATE_STARTING"
	ProcessStateRunning  EventType = "PROCESS_STATE_RUNNING"
	ProcessStateBackoff  EventType = "PROCESS_STATE_BACKOFF"
	ProcessStateStopping EventType = "PROCESS_STATE_STOPPING"
	ProcessStateExited   EventType = "PROCESS_STATE_EXITED"
	ProcessStateFatal    EventType = "PROCESS_STATE_FATAL"
)

// Process log events.
const (
	ProcessLogStdout EventType = "PROCESS_LOG_STDOUT"
	ProcessLogStderr EventType = "PROCESS_LOG_STDERR"
)

// Supervisor state events.
const (
	SupervisorStateRunning  EventType = "SUPERVISOR_STATE_RUNNING"
	SupervisorStateStopping EventType = "SUPERVISOR_STATE_STOPPING"
)

// Process group events.
const (
	ProcessGroupAdded   EventType = "PROCESS_GROUP_ADDED"
	ProcessGroupRemoved EventType = "PROCESS_GROUP_REMOVED"
)

// Periodic tick events.
const (
	Tick5    EventType = "TICK_5"
	Tick60   EventType = "TICK_60"
	Tick3600 EventType = "TICK_3600"
)

// AllEventTypes lists every event type the daemon publishes, for config
// validation of subscribers.
var AllEventTypes = []EventType{
	ProcessStateStopped, ProcessStateStarting, ProcessStateRunning, ProcessStateBackoff,
	ProcessStateStopping, ProcessStateExited, ProcessStateFatal,
	ProcessLogStdout, ProcessLogStderr,
	SupervisorStateRunning, SupervisorStateStopping,
	ProcessGroupAdded, ProcessGroupRemoved,
	Tick5, Tick60, Tick3600,
}

// Event carries data from a published event.
type Event struct {
	Type      EventType
	Timestamp time.Time
	Data      map[string]string
}

// HandlerFunc processes an event.
type HandlerFunc func(Event)

// QueueSize is the queue length Subscribe gives a subscriber that names no
// size.
const QueueSize = 1024

// overflowReportInterval is how often a subscriber whose queue stays full
// reports its drops (ADR-041). Tests shorten it.
var overflowReportInterval = 10 * time.Second

// subscription is one subscriber: the event types it wants, a bounded queue
// and the goroutine that runs its handler (ADR-041).
type subscription struct {
	id         uint64
	name       string
	types      map[EventType]bool
	handler    HandlerFunc
	onOverflow func(dropped int)

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []Event // ring of len cap
	head    int
	n       int
	closed  bool
	busy    bool // the handler is running
	dropped int  // drops not reported yet
	lastRep time.Time
	timer   *time.Timer // pending report of drops while the queue stays full
	done    chan struct{}
	stop    chan struct{} // closed by Unsubscribe; handlers that block select on it
}

// Bus is the central event dispatcher. It is safe for concurrent use.
// Publish never blocks: every subscriber has its own bounded queue that
// drops its oldest entry when full, and its own goroutine (ADR-041).
type Bus struct {
	mu     sync.RWMutex
	subs   map[uint64]*subscription
	nextID uint64
	logger *slog.Logger
}

// NewBus creates a new event bus.
func NewBus(logger *slog.Logger) *Bus {
	return &Bus{
		subs:   make(map[uint64]*subscription),
		logger: logger,
	}
}

// Subscribe registers a handler for one event type with the default queue
// size and no overflow report. It is SubscribeQueue for the common case.
func (b *Bus) Subscribe(eventType EventType, handler HandlerFunc) uint64 {
	return b.SubscribeQueue("", []EventType{eventType}, QueueSize, handler, nil)
}

// SubscribeQueue registers one subscriber for several event types (ADR-041).
// Events reach handler in publish order, on the subscriber's own goroutine,
// after Publish returns. When the queue holds size events the oldest is
// dropped; onOverflow, if set, receives the number of drops when the queue
// next has room and every overflowReportInterval while it stays full, never
// once per event. It runs outside the bus lock.
func (b *Bus) SubscribeQueue(name string, types []EventType, size int, handler HandlerFunc, onOverflow func(dropped int)) uint64 {
	if size < 1 {
		size = 1
	}
	s := &subscription{
		name:       name,
		types:      make(map[EventType]bool, len(types)),
		handler:    handler,
		onOverflow: onOverflow,
		queue:      make([]Event, size),
		done:       make(chan struct{}),
		stop:       make(chan struct{}),
		lastRep:    time.Now(),
	}
	s.cond = sync.NewCond(&s.mu)
	for _, t := range types {
		s.types[t] = true
	}
	b.mu.Lock()
	b.nextID++
	s.id = b.nextID
	b.subs[s.id] = s
	b.mu.Unlock()
	go b.run(s)
	return s.id
}

// Stopped returns a channel that is closed when the subscription is removed.
// A handler that blocks (handing an event to an HTTP writer, say) selects on
// it so Unsubscribe never waits on it forever.
func (b *Bus) Stopped(id uint64) <-chan struct{} {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if s, ok := b.subs[id]; ok {
		return s.stop
	}
	c := make(chan struct{})
	close(c)
	return c
}

// Unsubscribe removes a subscription. Queued events are discarded. It waits
// for a running handler to return; a handler that may block must select on
// Stopped(id).
func (b *Bus) Unsubscribe(id uint64) {
	b.mu.Lock()
	s, ok := b.subs[id]
	delete(b.subs, id)
	b.mu.Unlock()
	if !ok {
		return
	}
	s.mu.Lock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	close(s.stop)
	s.cond.Broadcast()
	s.mu.Unlock()
	<-s.done
}

// Publish queues an event for every subscriber of its type. It never blocks
// on a handler and is O(subscribers).
func (b *Bus) Publish(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, s := range b.subs {
		if !s.types[event.Type] {
			continue
		}
		s.mu.Lock()
		if s.n == len(s.queue) {
			// Full: drop the oldest. A stalled consumer never dequeues, so a
			// timer reports the drops one interval after the last report.
			s.head = (s.head + 1) % len(s.queue)
			s.n--
			s.dropped++
			if s.onOverflow != nil && s.timer == nil && !s.closed {
				wait := max(overflowReportInterval-time.Since(s.lastRep), 0)
				s.timer = time.AfterFunc(wait, s.reportDrops)
			}
		}
		s.queue[(s.head+s.n)%len(s.queue)] = event
		s.n++
		s.cond.Signal()
		s.mu.Unlock()
	}
}

// reportDrops hands the drops counted so far to onOverflow (timer path).
func (s *subscription) reportDrops() {
	s.mu.Lock()
	n := s.dropped
	s.dropped = 0
	s.lastRep = time.Now()
	s.timer = nil
	closed := s.closed
	s.mu.Unlock()
	if n > 0 && !closed {
		s.onOverflow(n)
	}
}

func (b *Bus) run(s *subscription) {
	defer close(s.done)
	for {
		s.mu.Lock()
		for s.n == 0 && !s.closed {
			s.cond.Wait()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		e := s.queue[s.head]
		s.queue[s.head] = Event{}
		s.head = (s.head + 1) % len(s.queue)
		s.n--
		// The queue has room again: a burst of drops ends. Report it at most
		// once per interval, so a slow consumer does not log once per event.
		report := 0
		if s.dropped > 0 && s.onOverflow != nil && time.Since(s.lastRep) >= overflowReportInterval {
			report = s.dropped
			s.dropped = 0
			s.lastRep = time.Now()
			if s.timer != nil {
				s.timer.Stop()
				s.timer = nil
			}
		}
		s.busy = true
		s.mu.Unlock()
		if report > 0 {
			s.onOverflow(report)
		}
		b.safeCall(s.handler, e)
		s.mu.Lock()
		s.busy = false
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

// Flush waits until every subscriber has handled every event queued so far.
// Tests use it in place of the synchronous delivery the bus had before
// ADR-041.
func (b *Bus) Flush() {
	b.mu.RLock()
	subs := make([]*subscription, 0, len(b.subs))
	for _, s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.RUnlock()
	for _, s := range subs {
		s.mu.Lock()
		for (s.n > 0 || s.busy) && !s.closed {
			s.cond.Wait()
		}
		s.mu.Unlock()
	}
}

func (b *Bus) safeCall(handler HandlerFunc, event Event) {
	defer func() {
		if r := recover(); r != nil {
			if b.logger != nil {
				b.logger.Error("event handler panicked",
					"event", string(event.Type),
					"panic", r,
				)
			}
		}
	}()
	handler(event)
}

// SubscriberCount returns the number of subscribers for an event type.
func (b *Bus) SubscriberCount(eventType EventType) int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n := 0
	for _, s := range b.subs {
		if s.types[eventType] {
			n++
		}
	}
	return n
}

// Ticker emits periodic TICK events. Call Stop to shut it down.
type Ticker struct {
	bus      *Bus
	stopCh   chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

// NewTicker starts emitting TICK_5, TICK_60, and TICK_3600 events.
func NewTicker(bus *Bus) *Ticker {
	return newTicker(bus, 5*time.Second, 60*time.Second, 3600*time.Second)
}

// newTicker takes the three periods, so tests can run them in milliseconds.
// time.Ticker runs on the monotonic clock, so a wall-clock jump neither skips
// nor doubles a tick (FUNC-054).
func newTicker(bus *Bus, p5, p60, p3600 time.Duration) *Ticker {
	t := &Ticker{
		bus:    bus,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go t.run(p5, p60, p3600)
	return t
}

func (t *Ticker) run(p5, p60, p3600 time.Duration) {
	defer close(t.done)

	tick5 := time.NewTicker(p5)
	tick60 := time.NewTicker(p60)
	tick3600 := time.NewTicker(p3600)
	defer tick5.Stop()
	defer tick60.Stop()
	defer tick3600.Stop()

	for {
		select {
		case <-t.stopCh:
			return
		case now := <-tick5.C:
			t.publish(Tick5, now)
		case now := <-tick60.C:
			t.publish(Tick60, now)
		case now := <-tick3600.C:
			t.publish(Tick3600, now)
		}
	}
}

// publish sends a tick with its time, as "when" in Unix seconds like
// supervisord's TICK events.
func (t *Ticker) publish(typ EventType, now time.Time) {
	t.bus.Publish(Event{Type: typ, Timestamp: now, Data: map[string]string{"when": strconv.FormatInt(now.Unix(), 10)}})
}

// Stop terminates the ticker goroutine and waits for it to finish. It may be
// called more than once: the supervisor stops ticks when shutdown begins and
// again when Run returns.
func (t *Ticker) Stop() {
	t.stopOnce.Do(func() { close(t.stopCh) })
	<-t.done
}
