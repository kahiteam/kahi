package process

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

func TestProbePrograms(t *testing.T) {
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(bin, "tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "plain"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Programs: map[string]config.ProgramConfig{
		"ok":        {Command: "sleep 1"},
		"abs":       {Command: "/nonexistent/web --flag"},
		"rel":       {Command: "bin/tool", Directory: base},
		"relmiss":   {Command: "./missing", Directory: base},
		"noexec":    {Command: filepath.Join(bin, "plain")},
		"bare":      {Command: "kahi-no-such-command-xyz"},
		"dir":       {Command: "sleep 1", Directory: filepath.Join(base, "gone")},
		"dirrel":    {Command: "./x", Directory: filepath.Join(base, "gone2")},
		"tramp":     {Command: "tool", Umask: "022", Environment: map[string]string{"PATH": bin}},
		"trampmiss": {Command: "sleep", Umask: "022", CleanEnvironment: true},
		"logs":      {Command: "sleep 1", StdoutLogfile: filepath.Join(base, "nope", "out.log"), StderrLogfile: filepath.Join(base, "nope", "err.log"), RedirectStderr: true},
		"many":      {Command: "/nonexistent/w%(process_num)d", Numprocs: 2, NumprocsStart: 0, ProcessName: "many_%(process_num)d"},
		"same":      {Command: "/nonexistent/same", Numprocs: 3, ProcessName: "same_%(process_num)d"},
	}}
	got := ProbePrograms(cfg, true)
	want := []string{
		"programs.abs.command: /nonexistent/web: no such file or directory",
		"programs.bare.command: exec: \"kahi-no-such-command-xyz\": executable file not found in $PATH",
		"programs.dir.directory: directory does not exist: " + filepath.Join(base, "gone"),
		"programs.dirrel.directory: directory does not exist: " + filepath.Join(base, "gone2"),
		"programs.logs.stdout_logfile: open " + filepath.Join(base, "nope", "out.log") + ": no such file or directory",
		"programs.many.command: /nonexistent/w0: no such file or directory",
		"programs.many.command: /nonexistent/w1: no such file or directory",
		"programs.noexec.command: " + filepath.Join(bin, "plain") + ": permission denied",
		"programs.relmiss.command: ./missing: no such file or directory",
		"programs.same.command: /nonexistent/same: no such file or directory",
		"programs.trampmiss.command: exec: \"sleep\": executable file not found in $PATH",
	}
	// As root access(X_OK) still needs an execute bit, so "plain" (0644)
	// fails there too.
	if !slices.Equal(got, want) {
		t.Errorf("ProbePrograms:\n got %q\nwant %q", got, want)
	}
}

func TestProbeProgramDir(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := probeProgramDir(""); err != nil {
		t.Errorf("empty: %v", err)
	}
	if err := probeProgramDir(base); err != nil {
		t.Errorf("ok: %v", err)
	}
	if err := probeProgramDir(file); err == nil || err.Error() != "directory does not exist: "+file {
		t.Errorf("file: %v", err)
	}
	if err := probeProgramDir(filepath.Join(file, "x")); err == nil || !strings.HasPrefix(err.Error(), "cannot access directory: ") {
		t.Errorf("below a file: %v", err)
	}
	if os.Geteuid() != 0 {
		locked := filepath.Join(base, "locked")
		if err := os.Mkdir(locked, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := probeProgramDir(locked); err == nil || err.Error() != "cannot access directory: "+locked+": permission denied" {
			t.Errorf("no search permission: %v", err)
		}
	}
}

// Pool members are probed like programs, under eventlisteners.x.
func TestProbeProgramsPools(t *testing.T) {
	cfg := &config.Config{
		Programs:       map[string]config.ProgramConfig{"p": {Command: "/nonexistent/p"}},
		EventListeners: map[string]config.EventListenerConfig{"l": {ProgramConfig: config.ProgramConfig{Command: "/nonexistent/l", StdoutLogfile: "/nonexistent/dir/out.log"}}},
	}
	got := strings.Join(ProbePrograms(cfg, true), "\n")
	want := "programs.p.command: /nonexistent/p: no such file or directory\neventlisteners.l.command: /nonexistent/l: no such file or directory"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
