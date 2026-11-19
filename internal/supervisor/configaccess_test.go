package supervisor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

// unreadable makes path unreadable for the test user and restores it at
// cleanup; as root, permissions do not apply and the test is skipped.
func unreadable(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode().Perm()) })
}

func writeConf(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const accessProg = "[programs.web]\ncommand = \"/bin/sleep 300\"\nautostart = false\n"

func loadStarted(t *testing.T, path string) *config.Config {
	t.Helper()
	cfg, _, err := config.LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// The config reads the same after the drop: no warning (FUNC-045).
func TestCheckConfigAfterDropReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	writeConf(t, path, accessProg)
	if w := CheckConfigAfterDrop(path, loadStarted(t, path)); w != "" {
		t.Errorf("warning for a readable config: %q", w)
	}
}

// An unreadable main file or include: the WARN names the user, the file
// and both ways out.
func TestCheckConfigAfterDropUnreadable(t *testing.T) {
	for _, tc := range []struct{ name, unreadableFile string }{{"main", "kahi.toml"}, {"include", "inc.toml"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "kahi.toml")
			writeConf(t, path, "include = [\"inc.toml\"]\n"+accessProg)
			writeConf(t, filepath.Join(dir, "inc.toml"), "[programs.api]\ncommand = \"/bin/sleep 300\"\nautostart = false\n")
			started := loadStarted(t, path)
			bad := filepath.Join(dir, tc.unreadableFile)
			unreadable(t, bad)
			w := CheckConfigAfterDrop(path, started)
			who := runningAs()
			for _, want := range []string{
				"config not readable as " + who + " after the privilege drop: ",
				"permission denied",
				"; reload, update, reread and avail will fail until the daemon is restarted or the file is readable by " + who,
				"(for example chgrp " + runningGroup() + " " + bad + "; chmod 0640 " + bad + ")",
			} {
				if !strings.Contains(w, want) {
					t.Errorf("warning %q lacks %q", w, want)
				}
			}
		})
	}
}

// An include directory the user cannot list: glob skips it without an
// error, so the warning names what a reload would lose.
func TestCheckConfigAfterDropMissingInclude(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kahi.toml")
	if err := os.Mkdir(filepath.Join(dir, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeConf(t, path, "include = [\"conf.d/*.toml\"]\n"+accessProg)
	writeConf(t, filepath.Join(dir, "conf.d", "a.toml"), "[programs.api]\ncommand = \"/bin/sleep 300\"\nautostart = false\n\n[groups.g]\nprograms = [\"api\"]\n")
	started := loadStarted(t, path)
	unreadable(t, filepath.Join(dir, "conf.d"))
	who := runningAs()
	want := "config reads differently as " + who + " after the privilege drop (missing: api, g); an include directory is probably not readable by " + who
	if w := CheckConfigAfterDrop(path, started); w != want {
		t.Errorf("warning %q, want %q", w, want)
	}
}

// A config error that is not about access is left to the next reload.
func TestCheckConfigAfterDropOtherError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kahi.toml")
	writeConf(t, path, accessProg)
	started := loadStarted(t, path)
	writeConf(t, path, "[programs.bad\n")
	if w := CheckConfigAfterDrop(path, started); w != "" {
		t.Errorf("warning for a syntax error: %q", w)
	}
}

func TestMissingNames(t *testing.T) {
	a := &config.Config{
		Programs:       map[string]config.ProgramConfig{"web": {}, "api": {}},
		EventListeners: map[string]config.EventListenerConfig{"l": {}},
		Groups:         map[string]config.GroupConfig{"g": {}},
	}
	b := &config.Config{Programs: map[string]config.ProgramConfig{"web": {}}}
	if got := missingNames(a, b); !slices.Equal(got, []string{"api", "g", "l"}) {
		t.Errorf("missing %v", got)
	}
	if got := missingNames(b, a); len(got) != 0 {
		t.Errorf("missing %v", got)
	}
}

// Every read after start (reload, update, reread, avail) carries the hint
// on a permission error; the running config is unchanged.
func TestLoadConfigPermissionHint(t *testing.T) {
	s, path := groupsSupervisor(t, accessProg)
	unreadable(t, path)
	who := runningAs()
	hint := "permission denied; the daemon runs as " + who + ": restart it to apply config changes, or make the file readable by " + who
	_, reloadErr := s.Reload()
	_, previewErr := s.PreviewReload()
	_, groupsErr := s.ReloadGroups([]string{"web"})
	_, previewGroupsErr := s.PreviewReloadGroups([]string{"web"})
	_, availErr := s.Avail()
	for name, err := range map[string]error{"reload": reloadErr, "preview": previewErr, "update web": groupsErr, "reread web": previewGroupsErr, "avail": availErr} {
		if err == nil || !strings.HasSuffix(err.Error(), hint) || !strings.Contains(err.Error(), "cannot read config: "+path) {
			t.Errorf("%s: %v, want ...%q", name, err, hint)
		}
	}
	if _, ok := s.config.Programs["web"]; !ok {
		t.Error("running config changed")
	}
}
