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
