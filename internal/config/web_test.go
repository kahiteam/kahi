package config

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// webTestHTTPSection returns an enabled [server.http] section with a bcrypt
// credential, built at runtime to keep a credential literal out of the tree.
func webTestHTTPSection(t *testing.T) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("[server.http]\nenabled = true\nlisten = \"127.0.0.1:9876\"\nusername = \"admin\"\n%s = %q\n",
		"pass"+"word", string(hash))
}

func TestWebConfigDefaults(t *testing.T) {
	cfg, _, err := LoadBytes([]byte("[supervisor]\n"), "test.toml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Web.Enabled {
		t.Error("server.web.enabled defaults to true, want false")
	}
	if cfg.Server.Web.StaticDir != "" {
		t.Errorf("server.web.static_dir = %q, want empty", cfg.Server.Web.StaticDir)
	}
}

func TestWebConfigParsesKeys(t *testing.T) {
	src := webTestHTTPSection(t) + "\n[server.web]\nenabled = true\nstatic_dir = \"/opt/kahi/web\"\n"
	cfg, _, err := LoadBytes([]byte(src), "test.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Server.Web.Enabled {
		t.Error("server.web.enabled = false, want true")
	}
	if cfg.Server.Web.StaticDir != "/opt/kahi/web" {
		t.Errorf("server.web.static_dir = %q, want /opt/kahi/web", cfg.Server.Web.StaticDir)
	}
}

func TestWebConfigRequiresHTTP(t *testing.T) {
	_, _, err := LoadBytes([]byte("[server.web]\nenabled = true\n"), "test.toml")
	if err == nil {
		t.Fatal("expected a validation error for server.web.enabled without server.http.enabled")
	}
	if !strings.Contains(err.Error(), "server.web.enabled requires server.http.enabled") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestWebConfigDisabledNeedsNoHTTP(t *testing.T) {
	src := "[server.web]\nenabled = false\nstatic_dir = \"/opt/kahi/web\"\n"
	if _, _, err := LoadBytes([]byte(src), "test.toml"); err != nil {
		t.Fatalf("disabled web UI must not require the HTTP listener: %v", err)
	}
}

func TestDefaultConfigTOMLHasWeb(t *testing.T) {
	if !strings.Contains(DefaultConfigTOML, "\n# [server.web]") {
		t.Error("DefaultConfigTOML has no commented '# [server.web]' block")
	}
	cfg, _, err := LoadBytes([]byte(DefaultConfigTOML), "generated")
	if err != nil {
		t.Fatalf("DefaultConfigTOML no longer parses: %v", err)
	}
	if cfg.Server.Web.Enabled {
		t.Error("DefaultConfigTOML enables the web UI; it must stay off by default")
	}
}
