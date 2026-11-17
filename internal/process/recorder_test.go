package process

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
)

type fakeSink struct {
	mu      sync.Mutex
	starts  map[string]int
	exits   map[string][2]int // [unexpected, expected]
	removed []string
}

func newFakeSink() *fakeSink {
	return &fakeSink{starts: map[string]int{}, exits: map[string][2]int{}}
}

func (f *fakeSink) ProcessStarted(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts[name]++
}

func (f *fakeSink) ProcessExited(name string, expected bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.exits[name]
	if expected {
		e[1]++
	} else {
		e[0]++
	}
	f.exits[name] = e
}

func (f *fakeSink) RemoveProcess(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, name)
}

// A spawn that fails before exec counts one start and one unexpected exit;
// a reload that removes a program deletes its counters (FUNC-061).
func TestManagerRecordsSpawnsAndRemovals(t *testing.T) {
	sink := newFakeSink()
	mgr := NewManager(&MockSpawner{SpawnFn: func(SpawnConfig) (SpawnedProcess, error) { return nil, errors.New("no such file") }}, testBus(), testLogger())
	mgr.SetMetrics(sink)
	p := defaultProgramConfig()
	p.Startretries = 0
	cfg := &config.Config{Programs: map[string]config.ProgramConfig{"bad": p}}
	mgr.LoadConfig(cfg)
	_ = mgr.Start("bad")
	deadline := time.Now().Add(5 * time.Second)
	for {
		sink.mu.Lock()
		s, e := sink.starts["bad"], sink.exits["bad"]
		sink.mu.Unlock()
		if s >= 1 && e[0] >= 1 {
			if s != e[0] || e[1] != 0 {
				t.Errorf("starts %d, exits %v", s, e)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("starts %d, exits %v", s, e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mgr.ApplyReload(cfg, &config.Config{})
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.removed) != 1 || sink.removed[0] != "bad" {
		t.Errorf("removed %v", sink.removed)
	}
}
