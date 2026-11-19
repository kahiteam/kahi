package supervisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/process"
)

// A reload that removes the last program while its process does not stop
// (stuck) keeps the old config for it; the new config has no [programs]
// table, so its map is nil and the restore used to panic the daemon.
func TestReloadStuckLastProgramNoPanic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte("[programs.web]\ncommand = \"/bin/sleep 300\"\nstartsecs = 0\nstopwaitsecs = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(SupervisorConfig{Config: cfg, ConfigPath: path, Logger: discardLogger()})
	s.manager = process.NewManager(&process.MockSpawner{}, s.bus, discardLogger()) // children never exit
	s.manager.LoadConfig(cfg)
	if err := s.manager.Start("web"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[supervisor]\nshutdown_timeout = 5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	s.mu.Lock()
	_, kept := s.config.Programs["web"]
	s.mu.Unlock()
	if !kept {
		t.Error("the stuck program's old config was not kept")
	}
}

// A partial update of a new [groups] table that takes over a program that
// is active as its own group applies it, as a full reload does (the
// program's group is removed with the update). The program is stopped here:
// unit tests have no reaper to see a real child exit.
func TestReloadGroupsTableOverActiveProgram(t *testing.T) {
	s, path := groupsSupervisor(t, prog("web", "1"))
	if err := os.WriteFile(path, []byte(prog("web", "1")+"[groups.app]\nprograms = [\"web\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := s.ReloadGroups([]string{"app"})
	if err != nil {
		t.Fatalf("update app: %v", err)
	}
	if len(res.Added) != 1 || res.Added[0] != "app" || len(res.Removed) != 1 || res.Removed[0] != "web" {
		t.Errorf("result %+v, want added [app], removed [web]", res)
	}
	p, err := s.manager.GetProcess("web")
	if err != nil || p.Group() != "app" {
		t.Errorf("web after update app: group %q, %v; want app", func() string {
			if p == nil {
				return ""
			}
			return p.Group()
		}(), err)
	}
	full, err := s.PreviewReload()
	if err != nil || len(full.Added)+len(full.Changed)+len(full.Removed) != 0 {
		t.Errorf("a full reread after update app still reports changes: %+v %v", full, err)
	}
}
