package process

import (
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/api"
	"github.com/kahiteam/kahi/internal/config"
)

// Signal and spawn behaviours for bulkHarness processes.
const (
	exitOnSignal = iota // exit on any signal
	ignoreTerm          // exit only on SIGKILL
	ignoreAll           // never exit
	delayedExit         // exit 200ms after a signal
	failSpawning        // Spawn returns an error
)

// bulkHarness builds a Manager over a MockSpawner whose processes are keyed by
// Command. It records spawn and signal order and exits processes through
// HandleExit from a goroutine: Signal runs under p.mu, so a synchronous
// HandleExit would deadlock. Processes are looked up once after LoadConfig and
// never from inside the spawn or signal callbacks.
type bulkHarness struct {
	t       *testing.T
	m       *Manager
	mu      sync.Mutex
	procs   map[string]*Process
	mode    map[string]int
	spawned []string
	signals []string
	// unsequenced lists processes signalled while the previously signalled
	// process had not yet reached STOPPED.
	unsequenced []string
	onSpawn     func(name string)
}

type bulkProgram struct {
	name     string
	priority int
	mode     int
	edit     func(*config.ProgramConfig)
}

func newBulkHarness(t *testing.T, programs ...bulkProgram) *bulkHarness {
	t.Helper()
	h := &bulkHarness{t: t, procs: map[string]*Process{}, mode: map[string]int{}}
	byCommand := map[string]string{}
	cfg := &config.Config{Programs: map[string]config.ProgramConfig{}}
	for _, bp := range programs {
		pc := defaultProgramConfig()
		pc.Command = "/bin/" + bp.name
		pc.Priority = bp.priority
		pc.Startsecs = 0
		pc.Startretries = 0
		pc.Stopasgroup = false
		pc.Killasgroup = false
		pc.Stopwaitsecs = 1
		if bp.edit != nil {
			bp.edit(&pc)
		}
		cfg.Programs[bp.name] = pc
		byCommand[pc.Command] = bp.name
		h.mode[bp.name] = bp.mode
	}

	spawner := &MockSpawner{SpawnFn: func(sc SpawnConfig) (SpawnedProcess, error) {
		name := byCommand[sc.Command]
		h.mu.Lock()
		h.spawned = append(h.spawned, name)
		mode := h.mode[name]
		hook := h.onSpawn
		h.mu.Unlock()
		if hook != nil {
			hook(name)
		}
		if mode == failSpawning {
			return nil, errors.New("command not found")
		}
		mp := NewMockProcess(4000 + len(name))
		mp.signalFn = func(sig os.Signal) error { return h.signal(name, sig) }
		return mp, nil
	}}
	h.m = NewManager(spawner, testBus(), testLogger())
	h.m.LoadConfig(cfg)
	for name := range cfg.Programs {
		p, err := h.m.GetProcess(name)
		if err != nil {
			t.Fatal(err)
		}
		h.procs[name] = p
	}
	return h
}

func (h *bulkHarness) signal(name string, sig os.Signal) error {
	h.mu.Lock()
	if n := len(h.signals); n > 0 {
		if last := h.signals[n-1]; last != name && h.procs[last].State() != Stopped {
			h.unsequenced = append(h.unsequenced, name)
		}
	}
	h.signals = append(h.signals, name)
	mode := h.mode[name]
	p := h.procs[name]
	h.mu.Unlock()
	switch {
	case mode == ignoreAll:
	case mode == ignoreTerm && sig != syscall.SIGKILL:
	case mode == delayedExit:
		go func() { time.Sleep(200 * time.Millisecond); p.HandleExit(0) }()
	default:
		go p.HandleExit(0)
	}
	return nil
}

func (h *bulkHarness) setMode(name string, mode int) {
	h.mu.Lock()
	h.mode[name] = mode
	h.mu.Unlock()
}

func (h *bulkHarness) snapshot() (spawned, signals, unsequenced []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.spawned...), append([]string(nil), h.signals...),
		append([]string(nil), h.unsequenced...)
}

func (h *bulkHarness) count(list []string, name string) int {
	n := 0
	for _, s := range list {
		if s == name {
			n++
		}
	}
	return n
}

func (h *bulkHarness) running(names ...string) {
	h.t.Helper()
	for _, n := range names {
		if err := h.procs[n].Start(); err != nil {
			h.t.Fatalf("start %s: %v", n, err)
		}
		h.waitState(n, Running)
	}
}

func (h *bulkHarness) waitState(name string, want State) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.procs[name].State() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h.t.Fatalf("%s: state %s, want %s", name, h.procs[name].State(), want)
}

func resultNames(results []api.ActionResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Name
	}
	return out
}

func resultFor(t *testing.T, results []api.ActionResult, name string) api.ActionResult {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no result for %s in %+v", name, results)
	return api.ActionResult{}
}

func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func expectResult(t *testing.T, results []api.ActionResult, name, status, errText string) {
	t.Helper()
	r := resultFor(t, results, name)
	if r.Status != status || r.Error != errText {
		t.Errorf("%s: got {status %q, error %q}, want {status %q, error %q}", name, r.Status, r.Error, status, errText)
	}
}

// fourPrograms is p10 (priority 10), a and b (priority 20) and p30 (30).
func fourPrograms() []bulkProgram {
	return []bulkProgram{
		{name: "p30", priority: 30}, {name: "p10", priority: 10},
		{name: "b", priority: 20}, {name: "a", priority: 20},
	}
}

func TestBulkStartOrder(t *testing.T) {
	h := newBulkHarness(t, fourPrograms()...)
	results := h.m.StartAllOrdered()

	want := []string{"p10", "a", "b", "p30"}
	if spawned, _, _ := h.snapshot(); !equalNames(spawned, want) {
		t.Errorf("spawn order %v, want %v", spawned, want)
	}
	if got := resultNames(results); !equalNames(got, want) {
		t.Errorf("result order %v, want %v", got, want)
	}
	for _, n := range want {
		expectResult(t, results, n, api.ActionOK, "")
	}
}

func TestBulkStopOrder(t *testing.T) {
	h := newBulkHarness(t, fourPrograms()...)
	h.running("p10", "a", "b", "p30")

	results := h.m.StopAllOrdered()

	want := []string{"p30", "b", "a", "p10"}
	_, signals, unsequenced := h.snapshot()
	if !equalNames(signals, want) {
		t.Errorf("signal order %v, want %v", signals, want)
	}
	if len(unsequenced) > 0 {
		t.Errorf("signalled before the previous process stopped: %v", unsequenced)
	}
	if got := resultNames(results); !equalNames(got, want) {
		t.Errorf("result order %v, want %v", got, want)
	}
	for _, n := range want {
		expectResult(t, results, n, api.ActionOK, "")
		if st := h.procs[n].State(); st != Stopped {
			t.Errorf("%s: state %s, want STOPPED", n, st)
		}
	}
}

func TestBulkStartSkips(t *testing.T) {
	h := newBulkHarness(t,
		bulkProgram{name: "stp", priority: 1, mode: ignoreAll},
		bulkProgram{name: "run", priority: 2},
		bulkProgram{name: "sting", priority: 3, edit: func(c *config.ProgramConfig) { c.Startsecs = 60 }},
		bulkProgram{name: "boff", priority: 4, mode: failSpawning, edit: func(c *config.ProgramConfig) { c.Startretries = 100 }},
		bulkProgram{name: "stopped", priority: 5},
		bulkProgram{name: "exited", priority: 6},
		bulkProgram{name: "fatal", priority: 7, mode: failSpawning},
	)
	h.running("stp", "run", "exited")
	_ = h.procs["stp"].Stop() // ignores signals: stays STOPPING
	h.waitState("stp", Stopping)
	_ = h.procs["sting"].Start()
	h.waitState("sting", Starting)
	_ = h.procs["boff"].Start()
	h.waitState("boff", Backoff)
	h.procs["exited"].HandleExit(0)
	h.waitState("exited", Exited)
	_ = h.procs["fatal"].Start()
	h.waitState("fatal", Fatal)
	h.setMode("fatal", exitOnSignal)
	t.Cleanup(func() { _ = h.procs["boff"].Stop() })

	before, _, _ := h.snapshot()
	results := h.m.StartAllOrdered()
	after, _, _ := h.snapshot()
	spawnedDuring := after[len(before):]

	for _, n := range []string{"run", "sting", "boff"} {
		expectResult(t, results, n, api.ActionSkipped, "")
		if h.count(spawnedDuring, n) != 0 {
			t.Errorf("%s was spawned although it was skipped", n)
		}
	}
	expectResult(t, results, "stp", api.ActionError, "process is stopping")
	for _, n := range []string{"stopped", "exited", "fatal"} {
		expectResult(t, results, n, api.ActionOK, "")
	}
}

func TestBulkStopSkips(t *testing.T) {
	h := newBulkHarness(t,
		bulkProgram{name: "delayed", priority: 900, mode: delayedExit},
		bulkProgram{name: "sting", priority: 800, edit: func(c *config.ProgramConfig) { c.Startsecs = 60 }},
		bulkProgram{name: "boff", priority: 700, mode: failSpawning, edit: func(c *config.ProgramConfig) { c.Startretries = 100 }},
		bulkProgram{name: "stopped", priority: 600},
		bulkProgram{name: "exited", priority: 500},
		bulkProgram{name: "fatal", priority: 400, mode: failSpawning},
		bulkProgram{name: "stuck", priority: 1, mode: ignoreAll},
	)
	h.running("delayed", "exited", "stuck")
	_ = h.procs["sting"].Start()
	h.waitState("sting", Starting)
	_ = h.procs["boff"].Start()
	h.waitState("boff", Backoff)
	h.procs["exited"].HandleExit(0)
	h.waitState("exited", Exited)
	_ = h.procs["fatal"].Start()
	h.waitState("fatal", Fatal)
	_ = h.procs["delayed"].Stop() // exits 200ms after the signal
	_ = h.procs["stuck"].Stop()   // never exits
	h.waitState("stuck", Stopping)

	results := h.m.StopAllOrdered()
	_, signals, _ := h.snapshot()

	for _, n := range []string{"stopped", "exited", "fatal"} {
		expectResult(t, results, n, api.ActionSkipped, "")
	}
	expectResult(t, results, "boff", api.ActionOK, "")
	h.waitState("boff", Stopped)
	expectResult(t, results, "sting", api.ActionOK, "")
	h.waitState("sting", Stopped)
	expectResult(t, results, "delayed", api.ActionOK, "")
	if got := h.count(signals, "delayed"); got != 1 {
		t.Errorf("delayed (already STOPPING) was signalled %d times, want 1", got)
	}
	expectResult(t, results, "stuck", api.ActionError, "stop timed out after 6s")
}

func TestBulkStartIncludesNoAutostart(t *testing.T) {
	off := false
	h := newBulkHarness(t, bulkProgram{name: "manual", priority: 1, edit: func(c *config.ProgramConfig) { c.Autostart = &off }})
	results := h.m.StartAllOrdered()
	expectResult(t, results, "manual", api.ActionOK, "")
	h.waitState("manual", Running)
}

func TestBulkStartContinuesOnError(t *testing.T) {
	h := newBulkHarness(t,
		bulkProgram{name: "first", priority: 1},
		bulkProgram{name: "broken", priority: 2, mode: failSpawning},
		bulkProgram{name: "last", priority: 3},
	)
	results := h.m.StartAllOrdered()
	expectResult(t, results, "first", api.ActionOK, "")
	expectResult(t, results, "last", api.ActionOK, "")
	if r := resultFor(t, results, "broken"); r.Status != api.ActionError || r.Error == "" {
		t.Errorf("broken: %+v, want an error entry with a message", r)
	}
}

func TestBulkStopWaitTimeout(t *testing.T) {
	h := newBulkHarness(t,
		bulkProgram{name: "stubborn", priority: 2, mode: ignoreTerm},
		bulkProgram{name: "next", priority: 1},
	)
	h.running("stubborn", "next")

	start := time.Now()
	results := h.m.StopAllOrdered()
	if took := time.Since(start); took > 6*time.Second {
		t.Errorf("StopAllOrdered took %v; stubborn should be killed after stopwaitsecs", took)
	}
	expectResult(t, results, "stubborn", api.ActionOK, "")
	expectResult(t, results, "next", api.ActionOK, "")

	h2 := newBulkHarness(t, bulkProgram{name: "immortal", priority: 1, mode: ignoreAll})
	h2.running("immortal")
	expectResult(t, h2.m.StopAllOrdered(), "immortal", api.ActionError, "stop timed out after 6s")
}

func TestBulkStopDoesNotShutdown(t *testing.T) {
	h := newBulkHarness(t, fourPrograms()...)
	h.running("p10", "a", "b", "p30")
	h.m.StopAllOrdered()
	if h.m.ShuttingDown() {
		t.Fatal("StopAllOrdered put the Manager into shutdown mode")
	}
	for _, r := range h.m.StartAllOrdered() {
		if r.Status != api.ActionOK {
			t.Errorf("%s: %+v, want ok after a bulk stop", r.Name, r)
		}
	}
}

func TestBulkStartDuringShutdown(t *testing.T) {
	h := newBulkHarness(t, fourPrograms()...)
	h.m.StopAll()
	if !h.m.ShuttingDown() {
		t.Fatal("ShuttingDown() is false after StopAll")
	}
	for _, r := range h.m.StartAllOrdered() {
		if r.Status != api.ActionError || r.Error != "daemon is shutting down" {
			t.Errorf("%s: %+v, want the shutdown error", r.Name, r)
		}
	}
	if spawned, _, _ := h.snapshot(); len(spawned) != 0 {
		t.Errorf("spawned during shutdown: %v", spawned)
	}

	// Shutdown begins while p10 is being started: p10 is stopped again and
	// the rest are never spawned. This Manager gets no StopAll call or
	// cleanup, because shutdownCh is closed directly.
	h2 := newBulkHarness(t, fourPrograms()...)
	h2.onSpawn = func(name string) {
		if name == "p10" {
			close(h2.m.shutdownCh)
		}
	}
	results := h2.m.StartAllOrdered()
	for _, n := range []string{"p10", "a", "b", "p30"} {
		expectResult(t, results, n, api.ActionError, "daemon is shutting down")
	}
	h2.waitState("p10", Stopped)
	if spawned, _, _ := h2.snapshot(); !equalNames(spawned, []string{"p10"}) {
		t.Errorf("spawned %v, want only p10", spawned)
	}
}

func TestBulkRemovedProcess(t *testing.T) {
	h := newBulkHarness(t, fourPrograms()...)
	h.onSpawn = func(name string) {
		if name == "p10" {
			h.m.RemoveProcess("a")
		}
	}
	results := h.m.StartAllOrdered()
	expectResult(t, results, "a", api.ActionError, "no such process: a")
	expectResult(t, results, "b", api.ActionOK, "")
	expectResult(t, results, "p30", api.ActionOK, "")
	if spawned, _, _ := h.snapshot(); h.count(spawned, "a") != 0 {
		t.Error("a removed process was spawned")
	}
}

func TestBulkEmpty(t *testing.T) {
	h := newBulkHarness(t)
	for name, results := range map[string][]api.ActionResult{
		"StartAllOrdered":   h.m.StartAllOrdered(),
		"StopAllOrdered":    h.m.StopAllOrdered(),
		"RestartAllOrdered": h.m.RestartAllOrdered(),
	} {
		if results == nil || len(results) != 0 {
			t.Errorf("%s() = %#v, want a non-nil empty slice", name, results)
		}
	}
}

func TestBulkRestartSkipsFailedStop(t *testing.T) {
	h := newBulkHarness(t, bulkProgram{name: "x", priority: 1}, bulkProgram{name: "y", priority: 2})
	results := h.m.startAllOrdered(map[string]bool{"x": true})
	if got := resultNames(results); !equalNames(got, []string{"y"}) {
		t.Errorf("start pass results %v, want only y", got)
	}
	expectResult(t, results, "y", api.ActionOK, "")
	if spawned, _, _ := h.snapshot(); h.count(spawned, "x") != 0 {
		t.Error("x was spawned although it was in skip")
	}

	h2 := newBulkHarness(t, bulkProgram{name: "x", priority: 1, mode: ignoreAll})
	h2.running("x")
	results = h2.m.RestartAllOrdered()
	expectResult(t, results, "x", api.ActionError, "stop timed out after 6s")
	if spawned, _, _ := h2.snapshot(); h2.count(spawned, "x") != 1 {
		t.Errorf("x spawned %d times, want 1 (never restarted after a failed stop)", h2.count(spawned, "x"))
	}

	h3 := newBulkHarness(t, bulkProgram{name: "p10", priority: 10}, bulkProgram{name: "a", priority: 20}, bulkProgram{name: "b", priority: 30})
	h3.running("p10", "b")
	results = h3.m.RestartAllOrdered()
	if got := resultNames(results); !equalNames(got, []string{"p10", "a", "b"}) {
		t.Errorf("restart results %v, want [p10 a b] in start order", got)
	}
	for _, n := range []string{"p10", "a", "b"} {
		expectResult(t, results, n, api.ActionOK, "")
	}
}
