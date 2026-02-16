// Package events provides a publish-subscribe event bus for process
// lifecycle notifications within the Kahi supervisor.
package events

import (
	"log/slog"
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

// Event carries data from a published event.
type Event struct {
	Type      EventType
	Timestamp time.Time
	Data      map[string]string
}

// HandlerFunc processes an event.
type HandlerFunc func(Event)

// subscription tracks a single subscriber.
type subscription struct {
	id      uint64
	handler HandlerFunc
}

// Bus is the central event dispatcher. It is safe for concurrent use.
// When no subscribers exist, Publish is a no-op with zero allocations.
type Bus struct {
	mu     sync.RWMutex
	subs   map[EventType][]subscription
	nextID uint64
	logger *slog.Logger
}

// NewBus creates a new event bus.
func NewBus(logger *slog.Logger) *Bus {
	return &Bus{
		subs:   make(map[EventType][]subscription),
		logger: logger,
	}
}

// Subscribe registers a handler for the given event type.
// Returns a subscription ID that can be used to unsubscribe.
func (b *Bus) Subscribe(eventType EventType, handler HandlerFunc) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := b.nextID
	b.subs[eventType] = append(b.subs[eventType], subscription{
		id:      id,
		handler: handler,
	})
	return id
}

// Unsubscribe removes a subscription by ID.
func (b *Bus) Unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for eventType, subs := range b.subs {
		for i, s := range subs {
			if s.id == id {
				b.subs[eventType] = append(subs[:i], subs[i+1:]...)
				if len(b.subs[eventType]) == 0 {
					delete(b.subs, eventType)
				}
				return
			}
		}
	}
}

// Publish dispatches an event to all subscribers of the event type.
// Handlers are called synchronously in registration order.
// A panicking handler is recovered and logged; remaining handlers
// still execute.
func (b *Bus) Publish(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	b.mu.RLock()
	subs := b.subs[event.Type]
	if len(subs) == 0 {
		b.mu.RUnlock()
		return
	}
	// Copy the slice so we can release the lock before calling handlers.
	handlers := make([]subscription, len(subs))
	copy(handlers, subs)
	b.mu.RUnlock()

	for _, s := range handlers {
		b.safeCall(s.handler, event)
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
	return len(b.subs[eventType])
}

// Ticker emits periodic TICK events. Call Stop to shut it down.
type Ticker struct {
	bus    *Bus
	stopCh chan struct{}
	done   chan struct{}
}

// NewTicker starts emitting TICK_5, TICK_60, and TICK_3600 events.
func NewTicker(bus *Bus) *Ticker {
	t := &Ticker{
		bus:    bus,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	go t.run()
	return t
}

func (t *Ticker) run() {
	defer close(t.done)

	tick5 := time.NewTicker(5 * time.Second)
	tick60 := time.NewTicker(60 * time.Second)
	tick3600 := time.NewTicker(3600 * time.Second)
	defer tick5.Stop()
	defer tick60.Stop()
	defer tick3600.Stop()

	for {
		select {
		case <-t.stopCh:
			return
		case now := <-tick5.C:
			t.bus.Publish(Event{Type: Tick5, Timestamp: now})
		case now := <-tick60.C:
			t.bus.Publish(Event{Type: Tick60, Timestamp: now})
		case now := <-tick3600.C:
			t.bus.Publish(Event{Type: Tick3600, Timestamp: now})
		}
	}
}

// Stop terminates the ticker goroutine and waits for it to finish.
func (t *Ticker) Stop() {
	close(t.stopCh)
	<-t.done
}
