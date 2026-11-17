package supervisor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

func groupsSupervisor(t *testing.T, body string) (*Supervisor, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kahi.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := config.LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	s := New(SupervisorConfig{Config: cfg, ConfigPath: path, Logger: discardLogger()})
	s.manager.LoadConfig(cfg)
	return s, path
}

func prog(name, env string) string {
	return "[programs." + name + "]\ncommand = \"/bin/sleep 300\"\nautostart = false\nenvironment = { V = \"" + env + "\" }\n\n"
}

// A partial update applies the named groups only; the others keep their old
// config (FUNC-085, ADR-051).
func TestReloadGroups(t *testing.T) {
	s, path := groupsSupervisor(t, prog("web", "1")+prog("worker", "1")+prog("old", "1"))
	write := func(body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(prog("web", "2") + prog("worker", "2") + prog("x", "1"))

	pre, err := s.PreviewReloadGroups([]string{"web"})
	if err != nil || !slices.Equal(pre.Changed, []string{"web"}) || len(pre.Added) != 0 || len(pre.Removed) != 0 {
		t.Fatalf("preview web: %+v %v", pre, err)
	}
	res, err := s.ReloadGroups([]string{"web"})
	if err != nil || !slices.Equal(res.Changed, []string{"web"}) || len(res.Added) != 0 {
		t.Fatalf("update web: %+v %v", res, err)
	}
	full, _ := s.PreviewReload()
	if !slices.Equal(full.Changed, []string{"worker"}) || !slices.Equal(full.Added, []string{"x"}) || !slices.Equal(full.Removed, []string{"old"}) {
		t.Errorf("after update web: %+v", full)
	}
	if _, err := s.ReloadGroups([]string{"nosuch"}); err == nil || err.Error() != "no such group: nosuch" {
		t.Errorf("nosuch: %v", err)
	}
	if res, err := s.ReloadGroups([]string{"old"}); err != nil || !slices.Equal(res.Removed, []string{"old"}) {
		t.Errorf("update old: %+v %v", res, err)
	}

	write(prog("web", "2") + prog("a", "1") + "[groups.g]\nprograms = [\"a\"]\n")
	if _, err := s.ReloadGroups([]string{"a"}); err == nil || !strings.Contains(err.Error(), "a is in group g; use g") {
		t.Errorf("table member: %v", err)
	}
	if _, err := s.PreviewReloadGroups([]string{"a"}); err == nil {
		t.Error("preview of a table member must fail")
	}
	entries, err := s.Avail()
	if err != nil || len(entries) != 2 || entries[0].Name != "a" || entries[0].Group != "g" || entries[0].Autostart || entries[1].Name != "web" || !entries[1].InUse {
		t.Errorf("avail: %+v %v", entries, err)
	}
	if res, err := s.ReloadGroups([]string{"all"}); err != nil || !slices.Contains(res.Added, "g") {
		t.Errorf("update all: %+v %v", res, err)
	}

	write("[programs.bad\n")
	if _, err := s.ReloadGroups([]string{"web"}); err == nil || !strings.HasPrefix(err.Error(), "config error:") {
		t.Errorf("broken file: %v", err)
	}
	if _, err := s.PreviewReloadGroups([]string{"web"}); err == nil {
		t.Error("preview of a broken file")
	}
	if _, err := s.Avail(); err == nil {
		t.Error("avail of a broken file")
	}
}
