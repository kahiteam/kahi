package process

import (
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/events"
)

func poolCfg(events []string, buffer int) config.EventListenerConfig {
	p := defaultProgramConfig()
	off := false
	p.Autostart = &off
	return config.EventListenerConfig{ProgramConfig: p, Events: events, BufferSize: buffer, ResultTimeout: 60}
}

func TestPoolConfigHelpers(t *testing.T) {
	a := &config.Config{
		Programs:       map[string]config.ProgramConfig{"web": defaultProgramConfig()},
		EventListeners: map[string]config.EventListenerConfig{"l": poolCfg([]string{"PROCESS_STATE_EXITED"}, 10)},
	}
	if unitConfig(a, "web").Command != "/bin/echo hello" || unitConfig(a, "l").Stopsignal != "TERM" {
		t.Error("unitConfig")
	}
	b := &config.Config{
		Programs:       map[string]config.ProgramConfig{"web": defaultProgramConfig()},
		EventListeners: map[string]config.EventListenerConfig{"l": poolCfg([]string{"PROCESS_STATE_EXITED"}, 5)},
	}
	if !unitChanged(a, b, "l") || !poolKeysChanged(a, b, "l") || unitChanged(a, b, "web") {
		t.Error("buffer_size change not seen as a pool change")
	}
	got := poolTypes(poolCfg([]string{"BOGUS", "PROCESS_STATE_EXITED", "PROCESS_STATE_EXITED", "PROCESS_STATE_FATAL"}, 1))
	want := []events.EventType{events.ProcessStateExited, events.ProcessStateFatal}
	if !slices.Equal(got, want) {
		t.Errorf("poolTypes = %v, want %v", got, want)
	}
}

// A pool is created with its members, survives a change (Reconfigure) and
// is stopped when it is removed.
func TestManagerPoolLifecycle(t *testing.T) {
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger())
	cfg1 := &config.Config{EventListeners: map[string]config.EventListenerConfig{"l": poolCfg([]string{"PROCESS_STATE_EXITED"}, 10)}}
	mgr.LoadConfig(cfg1)
	mgr.mu.RLock()
	pool := mgr.pools["l"]
	_, member := mgr.processes["l"]
	mgr.mu.RUnlock()
	if pool == nil || !member {
		t.Fatalf("pool %v, member %v", pool, member)
	}
	if err := mgr.WriteStdin("l", []byte("x")); err == nil || !strings.Contains(err.Error(), "stdin carries the event listener protocol") {
		t.Errorf("WriteStdin: %v", err)
	}

	cfg2 := &config.Config{EventListeners: map[string]config.EventListenerConfig{"l": poolCfg([]string{"PROCESS_STATE_EXITED", "PROCESS_STATE_FATAL"}, 5)}}
	out := mgr.ApplyReload(cfg1, cfg2)
	if !slices.Equal(out.Changed, []string{"l"}) {
		t.Fatalf("changed %v", out.Changed)
	}
	mgr.mu.RLock()
	same := mgr.pools["l"] == pool
	mgr.mu.RUnlock()
	if !same {
		t.Error("a changed pool must keep its dispatcher and buffer")
	}

	out = mgr.ApplyReload(cfg2, &config.Config{})
	if !slices.Equal(out.Removed, []string{"l"}) {
		t.Fatalf("removed %v", out.Removed)
	}
	mgr.mu.RLock()
	_, left := mgr.pools["l"]
	mgr.mu.RUnlock()
	if left {
		t.Error("removed pool still registered")
	}
}

// A member that misses result_timeout gets its stopsignal, then SIGKILL
// after stopwaitsecs if it still runs.
func TestListenerTimedOutSignals(t *testing.T) {
	var mu sync.Mutex
	var sigs []string
	mp := NewMockProcess(4321)
	mp.signalFn = func(s os.Signal) error {
		mu.Lock()
		sigs = append(sigs, s.String())
		mu.Unlock()
		return nil
	}
	mgr := NewManager(&MockSpawner{SpawnFn: func(SpawnConfig) (SpawnedProcess, error) { return mp, nil }}, testBus(), testLogger())
	l := poolCfg([]string{"PROCESS_STATE_EXITED"}, 10)
	l.Stopwaitsecs = 1
	mgr.LoadConfig(&config.Config{EventListeners: map[string]config.EventListenerConfig{"l": l}})
	if err := mgr.Start("l"); err != nil {
		t.Fatal(err)
	}
	mgr.listenerTimedOut("l")
	mgr.listenerTimedOut("nosuch") // gone: nothing to do
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(sigs)
		got := append([]string(nil), sigs...)
		mu.Unlock()
		if n >= 2 {
			if got[0] != "terminated" || got[1] != "killed" {
				t.Errorf("signals %v, want TERM then KILL", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("signals %v", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
