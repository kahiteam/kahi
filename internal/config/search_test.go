package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveExplicitPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Errorf("got %q, want %q", got, path)
	}
}

func TestResolveExplicitPathNotFound(t *testing.T) {
	_, err := Resolve("/nonexistent/kahi.toml")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "cannot read config") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestResolveEnvVar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KAHI_CONFIG", path)
	got, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Errorf("got %q, want %q", got, path)
	}
}

func TestResolveNoConfigFound(t *testing.T) {
	t.Setenv("KAHI_CONFIG", "")
	// Save and restore search paths
	orig := DefaultSearchPaths
	DefaultSearchPaths = []string{"/nonexistent/a.toml", "/nonexistent/b.toml"}
	defer func() { DefaultSearchPaths = orig }()

	_, err := Resolve("")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no config file found") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestResolveSearchPathOrder(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.toml")
	second := filepath.Join(dir, "second.toml")
	if err := os.WriteFile(first, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("KAHI_CONFIG", "")
	orig := DefaultSearchPaths
	DefaultSearchPaths = []string{first, second}
	defer func() { DefaultSearchPaths = orig }()

	got, err := Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Errorf("got %q, want %q (should pick first match)", got, first)
	}
}

func TestResolveDaemonSearchPathsExcludeCWD(t *testing.T) {
	// The daemon default search order must never include a current-directory
	// entry (SEC-017): a planted ./kahi.toml in an attacker-writable CWD must
	// not be reachable without explicit intent.
	for _, p := range DaemonSearchPaths {
		if p == "./kahi.toml" || strings.HasPrefix(p, "./") || strings.HasPrefix(p, "kahi.toml") {
			t.Errorf("DaemonSearchPaths contains CWD-relative entry %q", p)
		}
		if !filepath.IsAbs(p) {
			t.Errorf("DaemonSearchPaths entry %q is not absolute", p)
		}
	}
}

func TestResolveDaemonIgnoresCWDConfig(t *testing.T) {
	// Root starts the daemon from a directory that contains ./kahi.toml, with
	// no -c and no KAHI_CONFIG. The CWD file must not be selected; with no
	// system config present, resolution fails.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kahi.toml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("KAHI_CONFIG", "")

	orig := DaemonSearchPaths
	DaemonSearchPaths = []string{"/nonexistent/a.toml", "/nonexistent/b.toml"}
	defer func() { DaemonSearchPaths = orig }()

	got, err := ResolveDaemon("")
	if err == nil {
		t.Fatalf("expected error, got path %q (CWD config was selected)", got)
	}
	if !strings.Contains(err.Error(), "no config file found") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestResolveDaemonUsesSystemPaths(t *testing.T) {
	// A ./kahi.toml exists in CWD, but a system path also exists. The daemon
	// must resolve from the system search paths only.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kahi.toml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("KAHI_CONFIG", "")

	sysDir := t.TempDir()
	sysPath := filepath.Join(sysDir, "system.toml")
	if err := os.WriteFile(sysPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	orig := DaemonSearchPaths
	DaemonSearchPaths = []string{sysPath}
	defer func() { DaemonSearchPaths = orig }()

	got, err := ResolveDaemon("")
	if err != nil {
		t.Fatal(err)
	}
	if got != sysPath {
		t.Errorf("got %q, want %q (should use system path, not CWD)", got, sysPath)
	}
}

func TestResolveDaemonExplicitCWDHonored(t *testing.T) {
	// Explicit -c ./kahi.toml is trusted intent and must still be honored.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kahi.toml"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("KAHI_CONFIG", "")

	orig := DaemonSearchPaths
	DaemonSearchPaths = []string{"/nonexistent/a.toml"}
	defer func() { DaemonSearchPaths = orig }()

	got, err := ResolveDaemon("./kahi.toml")
	if err != nil {
		t.Fatal(err)
	}
	if got != "./kahi.toml" {
		t.Errorf("got %q, want %q (explicit path must be honored)", got, "./kahi.toml")
	}
}

func TestResolveDaemonEnvHonored(t *testing.T) {
	// KAHI_CONFIG pointing at any path is still honored by the daemon.
	dir := t.TempDir()
	path := filepath.Join(dir, "kahi.toml")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KAHI_CONFIG", path)

	orig := DaemonSearchPaths
	DaemonSearchPaths = []string{"/nonexistent/a.toml"}
	defer func() { DaemonSearchPaths = orig }()

	got, err := ResolveDaemon("")
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Errorf("got %q, want %q (KAHI_CONFIG must be honored)", got, path)
	}
}

func TestResolveDaemonNoConfigError(t *testing.T) {
	// No explicit path, no KAHI_CONFIG, no system config: exit non-zero with the
	// searched paths listed.
	t.Setenv("KAHI_CONFIG", "")
	orig := DaemonSearchPaths
	DaemonSearchPaths = []string{"/nonexistent/a.toml", "/nonexistent/b.toml"}
	defer func() { DaemonSearchPaths = orig }()

	_, err := ResolveDaemon("")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no config file found") {
		t.Errorf("error = %q", err.Error())
	}
	if !strings.Contains(err.Error(), "/nonexistent/a.toml") {
		t.Errorf("error should list searched paths, got %q", err.Error())
	}
}
