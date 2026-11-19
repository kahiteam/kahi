package process

import (
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

// Once process shutdown has begun, start, restart and group start/restart
// start nothing: a process started then would be orphaned when the daemon
// exits (FUNC-038).
func TestStartsRefusedDuringShutdown(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Startsecs = 0
	off := false
	cfg.Autostart = &off
	m := NewManager(&MockSpawner{}, testBus(), testLogger())
	m.LoadConfig(reloadCfg(map[string]config.ProgramConfig{"web": cfg}))
	m.ShutdownLevels() // begins shutdown
	for name, call := range map[string]func() error{
		"start":         func() error { return m.Start("web") },
		"restart":       func() error { return m.Restart("web") },
		"start group":   func() error { return m.StartGroup("web") },
		"restart group": func() error { return m.RestartGroup("web") },
	} {
		if err := call(); err == nil || err.Error() != "daemon is shutting down" {
			t.Errorf("%s: %v", name, err)
		}
	}
	p, _ := m.GetProcess("web")
	if s := p.State(); s != Stopped {
		t.Errorf("web %s, want STOPPED", s)
	}
}
