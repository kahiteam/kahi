package process

import (
	"sync"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/events"
)

var allStateEvents = []events.EventType{
	events.ProcessStateStopped, events.ProcessStateStarting, events.ProcessStateRunning,
	events.ProcessStateBackoff, events.ProcessStateStopping, events.ProcessStateExited,
	events.ProcessStateFatal,
}

// recordStates subscribes once to every state event, so the events keep
// their publish order, and records each event's data. Delivery is
// asynchronous (ADR-041): callers Flush the bus before reading.
func recordStates(bus *events.Bus, _ **Process) (*sync.Mutex, *[]map[string]string) {
	var mu sync.Mutex
	var got []map[string]string
	bus.SubscribeQueue("test", allStateEvents, events.QueueSize, func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		d := map[string]string{}
		for k, v := range e.Data {
			d[k] = v
		}
		got = append(got, d)
	}, nil)
	return &mu, &got
}

// Each event carries its state; from_state chains each event to the previous one; EXITED carries
// expected and exitstatus (FUNC-050, ADR-032).
func TestStateEventsPayload(t *testing.T) {
	bus := events.NewBus(testLogger())
	var p *Process
	mu, got := recordStates(bus, &p)
	cfg := defaultProgramConfig()
	cfg.Exitcodes = []int{0}
	cfg.Autorestart = "false"
	p = NewProcess("web", "web", cfg, &MockSpawner{}, bus, testLogger())

	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	p.HandleExitStatus(3, "")

	bus.Flush()
	mu.Lock()
	defer mu.Unlock()
	want := []struct{ state, from string }{{"STARTING", "STOPPED"}, {"RUNNING", "STARTING"}, {"EXITED", "RUNNING"}}
	if len(*got) != len(want) {
		t.Fatalf("events %v, want %d", *got, len(want))
	}
	for i, w := range want {
		e := (*got)[i]
		if e["state"] != w.state || e["from_state"] != w.from {
			t.Errorf("event %d = %v, want state %s from %s", i, e, w.state, w.from)
		}
	}
	if pid := (*got)[0]["pid"]; pid != "0" {
		t.Errorf("STARTING pid %q, want 0 (published before the spawn)", pid)
	}
	exited := (*got)[2]
	if exited["expected"] != "false" || exited["exitstatus"] != "3" {
		t.Errorf("EXITED = %v, want expected false and exitstatus 3", exited)
	}
	if _, ok := exited["exitsignal"]; ok {
		t.Errorf("EXITED has exitsignal without a signal death: %v", exited)
	}
}

func TestStateEventsSignalDeath(t *testing.T) {
	bus := events.NewBus(testLogger())
	var p *Process
	mu, got := recordStates(bus, &p)
	cfg := defaultProgramConfig()
	cfg.Autorestart = "false"
	p = NewProcess("web", "web", cfg, &MockSpawner{}, bus, testLogger())
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	p.HandleExitStatus(-1, "SIGKILL")

	bus.Flush()
	mu.Lock()
	defer mu.Unlock()
	exited := (*got)[len(*got)-1]
	if exited["state"] != "EXITED" || exited["exitsignal"] != "SIGKILL" || exited["expected"] != "false" || exited["exitstatus"] != "-1" {
		t.Errorf("EXITED = %v, want exitsignal SIGKILL, expected false, exitstatus -1", exited)
	}
}

// The startsecs watcher of a child that already exited must not publish for
// a later spawn: it used to re-publish STARTING with from_state STARTING
// (ADR-032). The state machine itself keeps the new child STARTING.
func TestStaleStartWatcherIgnored(t *testing.T) {
	bus := events.NewBus(testLogger())
	var p *Process
	mu, got := recordStates(bus, &p)
	cfg := defaultProgramConfig()
	cfg.Startsecs = 2
	p = NewProcess("web", "web", cfg, &MockSpawner{}, bus, testLogger())
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	// The first child dies at once: BACKOFF, a retry after 1s starts a second
	// child (its watcher fires at about 3s); the first watcher fires at 2s.
	p.HandleExitStatus(1, "")
	time.Sleep(2500 * time.Millisecond)
	_ = p.Stop()

	bus.Flush()
	mu.Lock()
	defer mu.Unlock()
	var seq []string
	for _, e := range *got {
		seq = append(seq, e["from_state"]+">"+e["state"])
		if e["from_state"] == e["state"] {
			t.Errorf("event %s repeats its from_state", e["state"])
		}
	}
	if len(seq) < 3 || seq[0] != "STOPPED>STARTING" || seq[1] != "STARTING>BACKOFF" || seq[2] != "BACKOFF>STARTING" {
		t.Errorf("events %v, want STOPPED>STARTING, STARTING>BACKOFF, BACKOFF>STARTING first", seq)
	}
}
