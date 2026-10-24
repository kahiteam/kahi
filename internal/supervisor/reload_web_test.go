package supervisor

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/kahiteam/kahi/internal/config"
)

const webRestartWarning = "server.web changes take effect after restart"

// writeWebConfig writes a config with an enabled, credentialed HTTP listener
// and the given [server.web] enabled value, so only [server.web] differs
// between the files a test writes.
func writeWebConfig(t *testing.T, path string, hash []byte, webEnabled bool) {
	t.Helper()
	content := fmt.Sprintf("[supervisor]\nshutdown_timeout = 5\n\n"+
		"[server.http]\nenabled = true\nlisten = \"127.0.0.1:0\"\nusername = \"admin\"\n%s = %q\n\n"+
		"[server.web]\nenabled = %t\n",
		"pass"+"word", string(hash), webEnabled)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newWebSupervisor builds a Supervisor through New from the config at path,
// so the startup [server.web] value is captured the way the daemon does it.
func newWebSupervisor(t *testing.T, path string, logger *slog.Logger) *Supervisor {
	t.Helper()
	cfg, _, err := config.LoadWithIncludes(path)
	if err != nil {
		t.Fatal(err)
	}
	return New(SupervisorConfig{Config: cfg, ConfigPath: path, Logger: logger})
}

func TestReloadWarnsOnWebChange(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "kahi.toml")
	writeWebConfig(t, cfgPath, hash, true)

	var buf bytes.Buffer
	s := newWebSupervisor(t, cfgPath, slog.New(slog.NewTextHandler(&buf, nil)))

	// The file now disables the UI; the running daemon started with it on.
	writeWebConfig(t, cfgPath, hash, false)

	s.handleReload()
	s.handleReload()
	added, changed, removed, err := s.Reload()

	if got := strings.Count(buf.String(), webRestartWarning); got != 3 {
		t.Errorf("restart warning logged %d times, want 3 (once per reload)\nlog:\n%s", got, buf.String())
	}

	cfg, ok := s.GetConfig().(*config.Config)
	if !ok {
		t.Fatalf("GetConfig() returned %T, want *config.Config", s.GetConfig())
	}
	if cfg.Server.Web.Enabled {
		t.Error("GetConfig() should report the file's server.web.enabled (false)")
	}
	if !s.startupWeb.Enabled {
		t.Error("the captured startup server.web must stay enabled after reloads")
	}

	// The warning must not change Reload's results: compare with the same
	// reload on a supervisor whose [server.web] did not change.
	ctrlPath := filepath.Join(t.TempDir(), "kahi.toml")
	writeWebConfig(t, ctrlPath, hash, false)
	ctrl := newWebSupervisor(t, ctrlPath, discardLogger())
	wantAdded, wantChanged, wantRemoved, wantErr := ctrl.Reload()

	if !reflect.DeepEqual(added, wantAdded) || !reflect.DeepEqual(changed, wantChanged) ||
		!reflect.DeepEqual(removed, wantRemoved) || (err == nil) != (wantErr == nil) {
		t.Errorf("Reload() = %v %v %v %v, want %v %v %v %v",
			added, changed, removed, err, wantAdded, wantChanged, wantRemoved, wantErr)
	}
}

func TestReloadNoWebWarningWhenUnchanged(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "kahi.toml")
	writeWebConfig(t, cfgPath, hash, true)

	var buf bytes.Buffer
	s := newWebSupervisor(t, cfgPath, slog.New(slog.NewTextHandler(&buf, nil)))
	s.handleReload()
	if _, _, _, err := s.Reload(); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(buf.String(), webRestartWarning) {
		t.Errorf("unexpected restart warning for an unchanged [server.web]\nlog:\n%s", buf.String())
	}
}
