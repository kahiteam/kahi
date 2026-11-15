package events

import (
	"bytes"
	"io"
	"log/slog"
	"strconv"
	"strings"
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
	bus.Flush()

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
	var count atomic.Int64
	bus.Subscribe(ProcessStateFatal, func(e Event) { count.Add(1) })
	bus.Subscribe(ProcessStateFatal, func(e Event) { count.Add(1) })
	bus.Subscribe(ProcessStateFatal, func(e Event) { count.Add(1) })

	bus.Publish(Event{Type: ProcessStateFatal})
	bus.Flush()

	if n := count.Load(); n != 3 {
		t.Fatalf("expected 3 notifications, got %d", n)
	}
}

func TestUnsubscribe(t *testing.T) {
	bus := NewBus(testLogger())
	var count int
	id := bus.Subscribe(ProcessStateExited, func(e Event) { count++ })

	bus.Publish(Event{Type: ProcessStateExited})
	bus.Flush()
	if count != 1 {
		t.Fatalf("expected 1, got %d", count)
	}

	bus.Unsubscribe(id)
	bus.Publish(Event{Type: ProcessStateExited})
	bus.Flush()
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
	bus.Flush()

	if !afterPanic {
		t.Fatal("handler after panic was not called")
	}
}

// The panic is logged with the event type and the panic value.
func TestPanicRecoveryLogged(t *testing.T) {
	var buf bytes.Buffer
	bus := NewBus(slog.New(slog.NewTextHandler(&buf, nil)))
	bus.Subscribe(ProcessStateFatal, func(e Event) { panic("boom") })
	bus.Publish(Event{Type: ProcessStateFatal})
	bus.Flush()
	out := buf.String()
	for _, want := range []string{`msg="event handler panicked"`, "event=PROCESS_STATE_FATAL", "panic=boom"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q lacks %q", out, want)
		}
	}
}

func TestNoSubscribersNoAlloc(t *testing.T) {
	bus := NewBus(testLogger())
	ev := Event{Type: ProcessStateRunning, Timestamp: time.Now()}
	if n := testing.AllocsPerRun(1000, func() { bus.Publish(ev) }); n != 0 {
		t.Fatalf("Publish without subscribers allocates %v times per call, want 0", n)
	}
}

// Publish never blocks: a handler that blocks holds up only its own
// subscriber, whose full queue drops the oldest event (ADR-041).
func TestPublishNeverBlocks(t *testing.T) {
	bus := NewBus(testLogger())
	release := make(chan struct{})
	id := bus.SubscribeQueue("stuck", []EventType{ProcessStateRunning}, 2, func(e Event) { <-release }, nil)
	var got []string
	bus.SubscribeQueue("live", []EventType{ProcessStateRunning}, 16, func(e Event) { got = append(got, e.Data["n"]) }, nil)
	done := make(chan struct{})
	go func() {
		for n := range 10 {
			bus.Publish(Event{Type: ProcessStateRunning, Data: map[string]string{"n": strconv.Itoa(n)}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a stuck subscriber")
	}
	close(release)
	bus.Flush()
	if len(got) != 10 {
		t.Fatalf("live subscriber got %d events, want 10", len(got))
	}
	bus.Unsubscribe(id)
}

// A full queue drops its oldest event; the newest survive in order, and the
// overflow callback reports the drops once the queue has room again.
func TestQueueDropsOldest(t *testing.T) {
	old := overflowReportInterval
	overflowReportInterval = 0
	defer func() { overflowReportInterval = old }()
	bus := NewBus(testLogger())
	release := make(chan struct{})
	var mu sync.Mutex
	var got []string
	dropped := 0
	bus.SubscribeQueue("q", []EventType{ProcessStateRunning}, 3, func(e Event) {
		<-release
		mu.Lock()
		got = append(got, e.Data["n"])
		mu.Unlock()
	}, func(n int) {
		mu.Lock()
		dropped += n
		mu.Unlock()
	})
	// The first event is taken by the handler (blocked); 3 more fill the
	// queue; the next 4 each drop the oldest queued one.
	bus.Publish(Event{Type: ProcessStateRunning, Data: map[string]string{"n": "0"}})
	time.Sleep(50 * time.Millisecond)
	for n := 1; n <= 7; n++ {
		bus.Publish(Event{Type: ProcessStateRunning, Data: map[string]string{"n": strconv.Itoa(n)}})
	}
	close(release)
	bus.Flush()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(got, ",") != "0,5,6,7" {
		t.Fatalf("delivered %v, want 0,5,6,7", got)
	}
	if dropped != 4 {
		t.Fatalf("overflow reported %d drops, want 4", dropped)
	}
}

// A consumer that never dequeues still gets its drops reported, by the
// timer, one interval after the last report.
func TestStalledQueueReportsDrops(t *testing.T) {
	old := overflowReportInterval
	overflowReportInterval = 50 * time.Millisecond
	defer func() { overflowReportInterval = old }()
	bus := NewBus(testLogger())
	block := make(chan struct{})
	defer close(block)
	reported := make(chan int, 4)
	bus.SubscribeQueue("stalled", []EventType{ProcessStateRunning}, 2, func(e Event) { <-block }, func(n int) { reported <- n })
	for range 6 {
		bus.Publish(Event{Type: ProcessStateRunning})
	}
	select {
	case n := <-reported:
		if n < 3 {
			t.Fatalf("reported %d drops, want at least 3", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("drops of a stalled queue never reported")
	}
}

// One subscription over several types keeps their publish order.
func TestSubscribeQueueOrderAcrossTypes(t *testing.T) {
	bus := NewBus(testLogger())
	var got []EventType
	bus.SubscribeQueue("multi", []EventType{ProcessStateStarting, ProcessStateRunning}, 16, func(e Event) { got = append(got, e.Type) }, nil)
	bus.Publish(Event{Type: ProcessStateStarting})
	bus.Publish(Event{Type: ProcessStateStopped}) // not subscribed
	bus.Publish(Event{Type: ProcessStateRunning})
	bus.Flush()
	if len(got) != 2 || got[0] != ProcessStateStarting || got[1] != ProcessStateRunning {
		t.Fatalf("got %v, want STARTING then RUNNING", got)
	}
}

// Unsubscribe does not hang on a handler that blocks on Stopped.
func TestUnsubscribeReleasesBlockedHandler(t *testing.T) {
	bus := NewBus(testLogger())
	ids := make(chan uint64, 1)
	entered := make(chan struct{})
	id := bus.SubscribeQueue("blocked", []EventType{ProcessStateRunning}, 4, func(e Event) {
		close(entered)
		<-bus.Stopped(<-ids)
	}, nil)
	ids <- id
	bus.Publish(Event{Type: ProcessStateRunning})
	<-entered
	done := make(chan struct{})
	go func() { bus.Unsubscribe(id); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Unsubscribe hung on a blocked handler")
	}
}

// Every subscriber receives the events of one publisher in publish order.
func TestPublishOrderPerSubscriber(t *testing.T) {
	bus := NewBus(testLogger())
	got := make([][]int, 3)
	for i := range got {
		bus.Subscribe(ProcessStateRunning, func(e Event) {
			n, _ := strconv.Atoi(e.Data["n"])
			got[i] = append(got[i], n)
		})
	}
	for n := range 1000 {
		bus.Publish(Event{Type: ProcessStateRunning, Data: map[string]string{"n": strconv.Itoa(n)}})
		bus.Flush()
	}
	for i, seq := range got {
		if len(seq) != 1000 {
			t.Fatalf("subscriber %d got %d events, want 1000", i, len(seq))
		}
		for n, v := range seq {
			if v != n {
				t.Fatalf("subscriber %d: event %d out of order (got %d)", i, n, v)
			}
		}
	}
}

func TestDifferentEventTypes(t *testing.T) {
	bus := NewBus(testLogger())
	var runningCount, stoppedCount int

	bus.Subscribe(ProcessStateRunning, func(e Event) { runningCount++ })
	bus.Subscribe(ProcessStateStopped, func(e Event) { stoppedCount++ })

	bus.Publish(Event{Type: ProcessStateRunning})
	bus.Publish(Event{Type: ProcessStateRunning})
	bus.Publish(Event{Type: ProcessStateStopped})
	bus.Flush()

	if runningCount != 2 {
		t.Fatalf("expected 2 running events, got %d", runningCount)
	}
	if stoppedCount != 1 {
		t.Fatalf("expected 1 stopped event, got %d", stoppedCount)
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
		bus.Flush()
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
	bus.Flush()

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
	bus.Flush()

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
	bus.Flush()

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
	bus.Flush()

	if !received.Timestamp.Equal(ts) {
		t.Fatalf("expected preserved timestamp, got %v", received.Timestamp)
	}
}

// The three tick types fire at their own periods, carry "when", and stop on
// Stop, which may be called twice (FUNC-054, #147).
func TestTickerPeriods(t *testing.T) {
	bus := NewBus(nil)
	var mu sync.Mutex
	counts := map[EventType]int{}
	var when string
	for _, typ := range []EventType{Tick5, Tick60, Tick3600} {
		bus.Subscribe(typ, func(e Event) {
			mu.Lock()
			defer mu.Unlock()
			counts[e.Type]++
			when = e.Data["when"]
		})
	}
	tk := newTicker(bus, 10*time.Millisecond, 40*time.Millisecond, 160*time.Millisecond)
	time.Sleep(400 * time.Millisecond)
	tk.Stop()
	tk.Stop()
	bus.Flush()

	mu.Lock()
	c5, c60, c3600, w := counts[Tick5], counts[Tick60], counts[Tick3600], when
	mu.Unlock()
	if c3600 < 1 || c60 <= c3600 || c5 <= c60 {
		t.Errorf("counts TICK_5 %d, TICK_60 %d, TICK_3600 %d; want each period's ticks, shortest most", c5, c60, c3600)
	}
	if n, err := strconv.ParseInt(w, 10, 64); err != nil || time.Since(time.Unix(n, 0)) > time.Minute {
		t.Errorf("when = %q, want the tick's Unix time", w)
	}

	mu.Lock()
	after := counts[Tick5]
	mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if counts[Tick5] != after {
		t.Error("ticks after Stop")
	}
}
