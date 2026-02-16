package events

import (
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSubscribeAndPublish(t *testing.T) {
	bus := NewBus(testLogger())
	var received Event
	bus.Subscribe(ProcessStateRunning, func(e Event) {
		received = e
	})

	bus.Publish(Event{
		Type: ProcessStateRunning,
		Data: map[string]string{"name": "web", "group": "web"},
	})

	if received.Type != ProcessStateRunning {
		t.Fatalf("expected %s, got %s", ProcessStateRunning, received.Type)
	}
	if received.Data["name"] != "web" {
		t.Fatalf("expected name=web, got %s", received.Data["name"])
	}
	if received.Timestamp.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	bus := NewBus(testLogger())
	var count int
	bus.Subscribe(ProcessStateFatal, func(e Event) { count++ })
	bus.Subscribe(ProcessStateFatal, func(e Event) { count++ })
	bus.Subscribe(ProcessStateFatal, func(e Event) { count++ })

	bus.Publish(Event{Type: ProcessStateFatal})

	if count != 3 {
		t.Fatalf("expected 3 notifications, got %d", count)
	}
}

func TestUnsubscribe(t *testing.T) {
	bus := NewBus(testLogger())
	var count int
	id := bus.Subscribe(ProcessStateExited, func(e Event) { count++ })

	bus.Publish(Event{Type: ProcessStateExited})
	if count != 1 {
		t.Fatalf("expected 1, got %d", count)
	}

	bus.Unsubscribe(id)
	bus.Publish(Event{Type: ProcessStateExited})
	if count != 1 {
		t.Fatalf("expected 1 after unsubscribe, got %d", count)
	}
}

func TestUnsubscribeNonexistent(t *testing.T) {
	bus := NewBus(testLogger())
	// Should not panic.
	bus.Unsubscribe(9999)
}

func TestPanicRecovery(t *testing.T) {
	bus := NewBus(testLogger())
	var afterPanic bool

	bus.Subscribe(ProcessStateFatal, func(e Event) {
		panic("test panic")
	})
	bus.Subscribe(ProcessStateFatal, func(e Event) {
		afterPanic = true
	})

	bus.Publish(Event{Type: ProcessStateFatal})

	if !afterPanic {
		t.Fatal("handler after panic was not called")
	}
}

func TestNoSubscribersNoAlloc(t *testing.T) {
	bus := NewBus(testLogger())

	// Publish to an event type with no subscribers.
	// Should return immediately without allocating.
	bus.Publish(Event{Type: ProcessStateRunning})
	// If we get here without panic, the test passes.
}

func TestDifferentEventTypes(t *testing.T) {
	bus := NewBus(testLogger())
	var runningCount, stoppedCount int

	bus.Subscribe(ProcessStateRunning, func(e Event) { runningCount++ })
	bus.Subscribe(ProcessStateStopped, func(e Event) { stoppedCount++ })

	bus.Publish(Event{Type: ProcessStateRunning})
	bus.Publish(Event{Type: ProcessStateRunning})
	bus.Publish(Event{Type: ProcessStateStopped})

	if runningCount != 2 {
		t.Fatalf("expected 2 running events, got %d", runningCount)
	}
	if stoppedCount != 1 {
		t.Fatalf("expected 1 stopped event, got %d", stoppedCount)
	}
}

func TestOrderedDelivery(t *testing.T) {
	bus := NewBus(testLogger())
	var order []int

	for i := range 1000 {
		bus.Subscribe(ProcessStateRunning, func(e Event) {
			order = append(order, i)
		})
	}

	bus.Publish(Event{Type: ProcessStateRunning})

	if len(order) != 1000 {
		t.Fatalf("expected 1000, got %d", len(order))
	}
	for i, v := range order {
		if v != i {
			t.Fatalf("out of order at index %d: got %d", i, v)
		}
	}
}

func TestConcurrentSubscribeUnsubscribe(t *testing.T) {
	bus := NewBus(testLogger())
	var wg sync.WaitGroup

	// Concurrent subscribe/unsubscribe from multiple goroutines.
	for range 50 {
		wg.Go(func() {
			id := bus.Subscribe(ProcessStateRunning, func(e Event) {})
			bus.Publish(Event{Type: ProcessStateRunning})
			bus.Unsubscribe(id)
		})
	}
	wg.Wait()
}

func TestSubscriberCount(t *testing.T) {
	bus := NewBus(testLogger())
	if bus.SubscriberCount(ProcessStateRunning) != 0 {
		t.Fatal("expected 0 subscribers")
	}

	id1 := bus.Subscribe(ProcessStateRunning, func(e Event) {})
	id2 := bus.Subscribe(ProcessStateRunning, func(e Event) {})
	if bus.SubscriberCount(ProcessStateRunning) != 2 {
		t.Fatalf("expected 2, got %d", bus.SubscriberCount(ProcessStateRunning))
	}

	bus.Unsubscribe(id1)
	if bus.SubscriberCount(ProcessStateRunning) != 1 {
		t.Fatalf("expected 1, got %d", bus.SubscriberCount(ProcessStateRunning))
	}

	bus.Unsubscribe(id2)
	if bus.SubscriberCount(ProcessStateRunning) != 0 {
		t.Fatalf("expected 0, got %d", bus.SubscriberCount(ProcessStateRunning))
	}
}

func TestAllStateEventTypes(t *testing.T) {
	types := []EventType{
		ProcessStateStopped, ProcessStateStarting, ProcessStateRunning,
		ProcessStateBackoff, ProcessStateStopping, ProcessStateExited,
		ProcessStateFatal,
	}

	bus := NewBus(testLogger())
	received := make(map[EventType]bool)
	var mu sync.Mutex

	for _, et := range types {
		bus.Subscribe(et, func(e Event) {
			mu.Lock()
			received[e.Type] = true
			mu.Unlock()
		})
	}

	for _, et := range types {
		bus.Publish(Event{Type: et, Data: map[string]string{"name": "test"}})
	}

	for _, et := range types {
		if !received[et] {
			t.Errorf("event type %s not received", et)
		}
	}
}

func TestSupervisorStateEvents(t *testing.T) {
	bus := NewBus(testLogger())
	var running, stopping bool

	bus.Subscribe(SupervisorStateRunning, func(e Event) { running = true })
	bus.Subscribe(SupervisorStateStopping, func(e Event) { stopping = true })

	bus.Publish(Event{Type: SupervisorStateRunning})
	bus.Publish(Event{Type: SupervisorStateStopping})

	if !running {
		t.Fatal("expected SUPERVISOR_STATE_RUNNING event")
	}
	if !stopping {
		t.Fatal("expected SUPERVISOR_STATE_STOPPING event")
	}
}

func TestProcessGroupEvents(t *testing.T) {
	bus := NewBus(testLogger())
	var added, removed bool

	bus.Subscribe(ProcessGroupAdded, func(e Event) {
		added = true
		if e.Data["group"] != "web" {
			t.Errorf("expected group=web, got %s", e.Data["group"])
		}
	})
	bus.Subscribe(ProcessGroupRemoved, func(e Event) {
		removed = true
	})

	bus.Publish(Event{
		Type: ProcessGroupAdded,
		Data: map[string]string{"group": "web"},
	})
	bus.Publish(Event{Type: ProcessGroupRemoved})

	if !added {
		t.Fatal("expected PROCESS_GROUP_ADDED event")
	}
	if !removed {
		t.Fatal("expected PROCESS_GROUP_REMOVED event")
	}
}

func TestTickerStops(t *testing.T) {
	bus := NewBus(testLogger())
	var count atomic.Int64
	bus.Subscribe(Tick5, func(e Event) {
		count.Add(1)
	})

	ticker := NewTicker(bus)
	// Let it run briefly, then stop.
	time.Sleep(50 * time.Millisecond)
	ticker.Stop()

	// After stop, no more events should fire.
	before := count.Load()
	time.Sleep(100 * time.Millisecond)
	after := count.Load()
	if after != before {
		t.Fatal("ticker continued after Stop()")
	}
}

func TestEventTimestampAutoSet(t *testing.T) {
	bus := NewBus(testLogger())
	var received Event
	bus.Subscribe(ProcessStateRunning, func(e Event) { received = e })

	before := time.Now()
	bus.Publish(Event{Type: ProcessStateRunning})

	if received.Timestamp.Before(before) {
		t.Fatal("timestamp should not be before publish time")
	}
}

func TestEventTimestampPreserved(t *testing.T) {
	bus := NewBus(testLogger())
	var received Event
	bus.Subscribe(ProcessStateRunning, func(e Event) { received = e })

	ts := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bus.Publish(Event{Type: ProcessStateRunning, Timestamp: ts})

	if !received.Timestamp.Equal(ts) {
		t.Fatalf("expected preserved timestamp, got %v", received.Timestamp)
	}
}
