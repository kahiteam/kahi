package process

import (
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

// A process left stuck by a reload keeps its old config and is tracked
// again: once its child exits it can be started (#300).
func TestStuckReloadNotRetired(t *testing.T) {
	cfg := prog("/bin/echo v1", true)
	cfg.Stopwaitsecs = 1
	oldCfg := reloadCfg(map[string]config.ProgramConfig{"web": cfg})
	mgr := NewManager(&MockSpawner{}, testBus(), testLogger()) // mock children never exit
	mgr.LoadConfig(oldCfg)
	mgr.AutostartAll()
	cfg2 := cfg
	cfg2.Command = "/bin/echo v2"
	out := mgr.ApplyReload(oldCfg, reloadCfg(map[string]config.ProgramConfig{"web": cfg2}))
	if len(out.Stuck) != 1 {
		t.Fatalf("stuck %v, want web", out.Stuck)
	}
	p, _ := mgr.GetProcess("web")
	p.HandleExit(0) // the stuck child finally exits
	if err := p.Start(); err != nil {
		t.Errorf("Start after the stuck child exited: %v", err)
	}
}
