package process

import (
	"strings"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
)

// One attach session at a time; stdin writes are refused during it
// (FUNC-034, ADR-044).
func TestManagerAttachLock(t *testing.T) {
	mgr := NewManager(&MockSpawner{SpawnFn: func(SpawnConfig) (SpawnedProcess, error) { return NewMockProcess(4242), nil }}, testBus(), testLogger())
	off := false
	cfg := defaultProgramConfig()
	cfg.Autostart = &off
	mgr.LoadConfig(&config.Config{Programs: map[string]config.ProgramConfig{"p": cfg}})

	if _, err := mgr.Attach("p"); err == nil || err.Error() != "process not running: p" {
		t.Fatalf("stopped: %v", err)
	}
	if _, err := mgr.Attach("nosuch"); err == nil || !strings.Contains(err.Error(), "no such process") {
		t.Fatalf("unknown: %v", err)
	}
	if err := mgr.Start("p"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, _ := mgr.Get("p")
		if info.State == "RUNNING" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not running")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, err := mgr.Attach("p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Attach("p"); err == nil || err.Error() != "process p: already attached" {
		t.Errorf("second: %v", err)
	}
	if err := mgr.WriteStdin("p", []byte("x")); err == nil || err.Error() != "process p: attached" {
		t.Errorf("stdin during attach: %v", err)
	}
	if err := s.Write([]byte("in")); err != nil {
		t.Errorf("session write: %v", err)
	}
	s.Release()
	if err := mgr.WriteStdin("p", []byte("x")); err != nil {
		t.Errorf("stdin after release: %v", err)
	}
}
