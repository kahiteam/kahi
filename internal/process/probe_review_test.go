package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

// Without a usable [supervisor] directory, relative program paths are not
// probed; absolute ones still are (FUNC-087).
func TestProbeProgramsRelativeSkipped(t *testing.T) {
	cfg := &config.Config{Programs: map[string]config.ProgramConfig{
		"rel": {Command: "bin/web", Directory: "work", StdoutLogfile: "logs/out.log", StderrLogfile: "logs/err.log", Numprocs: 1, ProcessName: "%(program_name)s"},
		"abs": {Command: "/nonexistent/abs", Numprocs: 1, ProcessName: "%(program_name)s"},
		"cmd": {Command: "bin/web", Numprocs: 1, ProcessName: "%(program_name)s"},
	}}
	got := strings.Join(ProbePrograms(cfg, false), "\n")
	if strings.Contains(got, "programs.rel.") || strings.Contains(got, "programs.cmd.") || !strings.Contains(got, "programs.abs.command: /nonexistent/abs") {
		t.Errorf("relOK=false: %q", got)
	}
	got = strings.Join(ProbePrograms(cfg, true), "\n")
	for _, want := range []string{"programs.rel.directory", "programs.cmd.command"} {
		if !strings.Contains(got, want) {
			t.Errorf("relOK=true lacks %s: %q", want, got)
		}
	}
}

func TestLookPathIn(t *testing.T) {
	app := t.TempDir()
	if err := os.WriteFile(filepath.Join(app, "worker"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tool"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := func(p string) []string { return []string{"HOME=/", "PATH=" + p} }
	for _, tc := range []struct {
		name, cmd, path, dir string
		dirOK, relOK, ok     bool
	}{
		{"absolute hit", "tool", bin, app, true, true, true},
		{"absolute hit before a relative one", "tool", bin + ":", app, true, true, true},
		{"empty entry hit is refused (ErrDot)", "worker", "/nonexistent:", app, true, true, false},
		{"name only in the program directory, no relative entry", "worker", "/nonexistent", app, true, true, false},
		{"not anywhere", "nosuch", "/nonexistent:", app, true, true, false},
		{"directory missing: covered by its warning", "nosuch", "/nonexistent:", app, false, true, true},
		{"relative base unusable", "nosuch", ":/nonexistent", "", true, false, true},
		{"daemon directory", "nosuch", "", "", true, true, false},
	} {
		err := lookPathIn(tc.cmd, env(tc.path), tc.dir, tc.dirOK, tc.relOK)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}
