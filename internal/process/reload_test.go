package process

import (
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kahiteam/kahi/internal/config"
)

// exitingSpawner returns mock children that exit as soon as they receive any
// signal, the way reaping a real child would report it.
func exitingSpawner(mgr **Manager) *MockSpawner {
	var pid atomic.Int64
	pid.Store(2000)
	return &MockSpawner{
		SpawnFn: func(SpawnConfig) (SpawnedProcess, error) {
			mp := NewMockProcess(int(pid.Add(1)))
			mp.signalFn = func(os.Signal) error {
				go func() {
					if p := (*mgr).ProcessByPid(mp.Pid()); p != nil {
						p.HandleExit(0)
					}
				}()
				return nil
			}
			return mp, nil
		},
	}
}

func reloadCfg(programs map[string]config.ProgramConfig) *config.Config {
	return &config.Config{Programs: programs}
}

func prog(command string, autostart bool) config.ProgramConfig {
	cfg := defaultProgramConfig()
	cfg.Command = command
	cfg.Autostart = &autostart
	cfg.Stopwaitsecs = 1
	return cfg
}

func startedManager(t *testing.T, cfg *config.Config) (*Manager, *MockSpawner) {
	t.Helper()
	var mgr *Manager
	spawner := exitingSpawner(&mgr)
	mgr = NewManager(spawner, testBus(), testLogger())
	mgr.LoadConfig(cfg)
	mgr.AutostartAll()
	return mgr, spawner
}

func TestApplyReloadChangedProgramUsesNewConfig(t *testing.T) {
	oldCfg := reloadCfg(map[string]config.ProgramConfig{"web": prog("/bin/echo v1", true)})
	mgr, _ := startedManager(t, oldCfg)
	old, _ := mgr.GetProcess("web")

	newCfg := reloadCfg(map[string]config.ProgramConfig{"web": prog("/bin/echo v2", true)})
	added, changed, removed := ConfigDiff(oldCfg, newCfg)
	if stuck := mgr.ApplyReload(oldCfg, newCfg, added, changed, removed); len(stuck) != 0 {
		t.Fatalf("stuck = %v", stuck)
	}

	cur, err := mgr.GetProcess("web")
	if err != nil {
		t.Fatalf("web missing after reload: %v", err)
	}
	if cur == old {
		t.Fatal("changed program kept its old process")
	}
	if cur.Config().Command != "/bin/echo v2" {
		t.Fatalf("command = %q, want the reloaded /bin/echo v2", cur.Config().Command)
	}
	if s := cur.State(); s != Starting && s != Running {
		t.Fatalf("state = %s, want STARTING or RUNNING", s)
	}
	if err := old.Start(); err == nil {
		t.Fatal("the replaced process could be started again")
	}
}

func TestApplyReloadRemovesProgramAndCaptures(t *testing.T) {
	oldCfg := reloadCfg(map[string]config.ProgramConfig{
		"keep": prog("/bin/echo keep", true),
		"gone": prog("/bin/echo gone", true),
	})
	mgr, _ := startedManager(t, oldCfg)

	newCfg := reloadCfg(map[string]config.ProgramConfig{"keep": prog("/bin/echo keep", true)})
	added, changed, removed := ConfigDiff(oldCfg, newCfg)
	mgr.ApplyReload(oldCfg, newCfg, added, changed, removed)

	if _, err := mgr.GetProcess("gone"); err == nil {
		t.Fatal("removed program still has a process")
	}
	if _, ok := mgr.Groups()["gone"]; ok {
		t.Fatal("removed program still has a group")
	}
	mgr.mu.RLock()
	_, out := mgr.captures["gone:stdout"]
	_, errw := mgr.captures["gone:stderr"]
	mgr.mu.RUnlock()
	if out || errw {
		t.Fatal("removed program's capture writers were not deleted")
	}
	if _, err := mgr.GetProcess("keep"); err != nil {
		t.Fatalf("unchanged program removed: %v", err)
	}
}

func TestApplyReloadHonoursAutostart(t *testing.T) {
	oldCfg := reloadCfg(map[string]config.ProgramConfig{})
	mgr, _ := startedManager(t, oldCfg)

	newCfg := reloadCfg(map[string]config.ProgramConfig{
		"auto":   prog("/bin/echo auto", true),
		"manual": prog("/bin/echo manual", false),
	})
	added, changed, removed := ConfigDiff(oldCfg, newCfg)
	mgr.ApplyReload(oldCfg, newCfg, added, changed, removed)

	auto, _ := mgr.GetProcess("auto")
	if s := auto.State(); s != Starting && s != Running {
		t.Fatalf("auto state = %s, want started", s)
	}
	manual, _ := mgr.GetProcess("manual")
	if s := manual.State(); s != Stopped {
		t.Fatalf("manual state = %s, want STOPPED (autostart = false)", s)
	}
}

func TestApplyReloadSkipsStartsDuringShutdown(t *testing.T) {
	oldCfg := reloadCfg(map[string]config.ProgramConfig{})
	mgr, _ := startedManager(t, oldCfg)
	mgr.StopAll()

	newCfg := reloadCfg(map[string]config.ProgramConfig{"late": prog("/bin/echo late", true)})
	added, changed, removed := ConfigDiff(oldCfg, newCfg)
	mgr.ApplyReload(oldCfg, newCfg, added, changed, removed)

	late, _ := mgr.GetProcess("late")
	if s := late.State(); s != Stopped {
		t.Fatalf("late state = %s, want STOPPED: nothing may start during shutdown", s)
	}
}

func TestConfigDiffDetectsEnvironmentChange(t *testing.T) {
	a := prog("/bin/echo", true)
	a.Environment = map[string]string{"VERSION": "v1"}
	b := prog("/bin/echo", true)
	b.Environment = map[string]string{"VERSION": "v2"}

	_, changed, _ := ConfigDiff(
		reloadCfg(map[string]config.ProgramConfig{"envy": a}),
		reloadCfg(map[string]config.ProgramConfig{"envy": b}))
	if len(changed) != 1 || changed[0] != "envy" {
		t.Fatalf("changed = %v, want [envy]", changed)
	}

	// Equal values in fresh maps and pointers are not a change.
	c := prog("/bin/echo", true)
	c.Environment = map[string]string{"VERSION": "v1"}
	if _, changed, _ := ConfigDiff(
		reloadCfg(map[string]config.ProgramConfig{"envy": a}),
		reloadCfg(map[string]config.ProgramConfig{"envy": c})); len(changed) != 0 {
		t.Fatalf("changed = %v for an identical config", changed)
	}
}

// A retired process must not be restarted by an autorestart queued before
// its removal.
func TestRetiredProcessDoesNotAutorestart(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.Autorestart = "true"
	spawner := &MockSpawner{}
	p := NewProcess("w", "w", cfg, spawner, testBus(), testLogger())
	if err := p.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	p.mu.Lock()
	p.retired = true
	p.mu.Unlock()
	p.HandleExit(1)
	time.Sleep(100 * time.Millisecond)

	if n := spawnCount(spawner); n != 1 {
		t.Fatalf("spawned %d times, want 1: a retired process was restarted", n)
	}
}

// Changing only supervision settings, including autostart = false, updates
// the running process in place: same process, still running.
func TestApplyReloadSupervisionChangeIsInPlace(t *testing.T) {
	oldCfg := reloadCfg(map[string]config.ProgramConfig{"web": prog("/bin/echo web", true)})
	mgr, spawner := startedManager(t, oldCfg)
	before, _ := mgr.GetProcess("web")

	changedProg := prog("/bin/echo web", false)
	changedProg.Stopwaitsecs = 30
	changedProg.Startretries = 9
	newCfg := reloadCfg(map[string]config.ProgramConfig{"web": changedProg})
	added, changed, removed := ConfigDiff(oldCfg, newCfg)
	if len(changed) != 1 {
		t.Fatalf("changed = %v, want [web]", changed)
	}
	mgr.ApplyReload(oldCfg, newCfg, added, changed, removed)

	after, _ := mgr.GetProcess("web")
	if after != before {
		t.Fatal("a supervision-only change replaced the process")
	}
	if s := after.State(); s != Starting && s != Running {
		t.Fatalf("state = %s, want still running: autostart = false must not stop it", s)
	}
	if n := spawnCount(spawner); n != 1 {
		t.Fatalf("spawned %d times, want 1 (no restart)", n)
	}
	if got := after.Config(); got.Stopwaitsecs != 30 || *got.Autostart {
		t.Fatalf("config not updated in place: stopwaitsecs=%d autostart=%v", got.Stopwaitsecs, *got.Autostart)
	}
}

// A spawn-affecting change restarts a running program even if its new
// autostart is false: autostart only governs daemon startup.
func TestApplyReloadRestartsRunningProgramRegardlessOfAutostart(t *testing.T) {
	oldCfg := reloadCfg(map[string]config.ProgramConfig{"web": prog("/bin/echo v1", true)})
	mgr, _ := startedManager(t, oldCfg)

	newCfg := reloadCfg(map[string]config.ProgramConfig{"web": prog("/bin/echo v2", false)})
	added, changed, removed := ConfigDiff(oldCfg, newCfg)
	mgr.ApplyReload(oldCfg, newCfg, added, changed, removed)

	cur, _ := mgr.GetProcess("web")
	if cur.Config().Command != "/bin/echo v2" {
		t.Fatalf("command = %q, want v2", cur.Config().Command)
	}
	if s := cur.State(); s != Starting && s != Running {
		t.Fatalf("state = %s, want restarted: it was running before the reload", s)
	}
}

// A stopped program stays stopped when a spawn-affecting change recreates it.
func TestApplyReloadKeepsStoppedProgramStopped(t *testing.T) {
	oldCfg := reloadCfg(map[string]config.ProgramConfig{"idle": prog("/bin/echo v1", false)})
	mgr, spawner := startedManager(t, oldCfg)

	newCfg := reloadCfg(map[string]config.ProgramConfig{"idle": prog("/bin/echo v2", true)})
	added, changed, removed := ConfigDiff(oldCfg, newCfg)
	mgr.ApplyReload(oldCfg, newCfg, added, changed, removed)

	cur, _ := mgr.GetProcess("idle")
	if cur.Config().Command != "/bin/echo v2" {
		t.Fatalf("command = %q, want v2", cur.Config().Command)
	}
	if s := cur.State(); s != Stopped {
		t.Fatalf("state = %s, want STOPPED: it was stopped before the reload", s)
	}
	if n := spawnCount(spawner); n != 0 {
		t.Fatalf("spawned %d times, want 0", n)
	}
}

func TestNeedsRestart(t *testing.T) {
	base := prog("/bin/echo", true)
	supervision := base
	supervision.Priority = 10
	supervision.Autorestart = "true"
	supervision.Stopsignal = "INT"
	supervision.Description = "x"
	if needsRestart(base, supervision) {
		t.Fatal("supervision-only change needs a restart")
	}
	for name, mutate := range map[string]func(*config.ProgramConfig){
		"command":     func(c *config.ProgramConfig) { c.Command = "/bin/other" },
		"environment": func(c *config.ProgramConfig) { c.Environment = map[string]string{"A": "1"} },
		"numprocs":    func(c *config.ProgramConfig) { c.Numprocs = 2 },
		"logfile":     func(c *config.ProgramConfig) { c.StdoutLogfile = "/tmp/x.log" },
		"directory":   func(c *config.ProgramConfig) { c.Directory = "/tmp" },
	} {
		c := base
		mutate(&c)
		if !needsRestart(base, c) {
			t.Errorf("%s change does not restart", name)
		}
	}
}

// Each instance of a numprocs > 1 program gets its own %(process_num)d in
// every field that may use it, and its own environment map (#128).
func TestExpandNumprocsPerInstanceFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte(`[programs.web]
command = "/bin/run --port 80%(process_num)d"
numprocs = 3
numprocs_start = 1
process_name = "web-%(process_num)d"
stdout_logfile = "/var/log/web-%(process_num)d.log"

[programs.web.environment]
SLOT = "slot-%(process_num)d"
`), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}

	insts := ExpandNumprocs("web", cfg.Programs["web"])
	if len(insts) != 3 {
		t.Fatalf("instances = %d, want 3", len(insts))
	}
	for i, inst := range insts {
		n := i + 1
		want := map[string]string{
			"name":    "web-" + strconv.Itoa(n),
			"command": "/bin/run --port 80" + strconv.Itoa(n),
			"logfile": "/var/log/web-" + strconv.Itoa(n) + ".log",
			"env":     "slot-" + strconv.Itoa(n),
		}
		got := map[string]string{
			"name":    inst.Name,
			"command": inst.Config.Command,
			"logfile": inst.Config.StdoutLogfile,
			"env":     inst.Config.Environment["SLOT"],
		}
		for k := range want {
			if got[k] != want[k] {
				t.Errorf("instance %d %s = %q, want %q", n, k, got[k], want[k])
			}
		}
	}
	insts[0].Config.Environment["SLOT"] = "changed"
	if insts[1].Config.Environment["SLOT"] == "changed" {
		t.Fatal("instances share one environment map")
	}
}
