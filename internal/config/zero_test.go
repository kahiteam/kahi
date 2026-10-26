package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const zeroProgram = "[programs.web]\ncommand = \"/bin/true\"\n"

// An explicit 0 is kept where 0 means something; a missing key still gets
// its default (#138).
func TestExplicitZeroSettingsKept(t *testing.T) {
	cfg, _, err := LoadBytes([]byte(zeroProgram+`priority = 0
startsecs = 0
startretries = 0
exitcodes = []
stdout_logfile_backups = 0
stderr_logfile_backups = 0
`), "test.toml")
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Programs["web"]
	got := []int{p.Priority, p.Startsecs, p.Startretries, len(p.Exitcodes), p.StdoutLogfileBackups, p.StderrLogfileBackups}
	if want := []int{0, 0, 0, 0, 0, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("priority, startsecs, startretries, len(exitcodes), backups = %v, want %v", got, want)
	}
}

func TestMissingSettingsDefaulted(t *testing.T) {
	cfg, _, err := LoadBytes([]byte(zeroProgram), "test.toml")
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Programs["web"]
	got := []int{p.Numprocs, p.Priority, p.Startsecs, p.Startretries, p.Stopwaitsecs, p.StdoutLogfileBackups, p.StderrLogfileBackups, cfg.Supervisor.ShutdownTimeout}
	if want := []int{1, 999, 1, 3, 10, 10, 10, 30}; !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(p.Exitcodes, []int{0}) {
		t.Fatalf("exitcodes = %v, want [0]", p.Exitcodes)
	}
}

// Code-built configs have no TOML metadata: zero values keep their old
// meaning of "unset".
func TestApplyDefaultsWithoutMetadata(t *testing.T) {
	cfg := &Config{Programs: map[string]ProgramConfig{"web": {Command: "/bin/true"}}}
	ApplyDefaults(cfg, emptyMetadata())
	if p := cfg.Programs["web"]; p.Stopwaitsecs != 10 || p.Priority != 999 || cfg.Supervisor.ShutdownTimeout != 30 {
		t.Fatalf("stopwaitsecs, priority, shutdown_timeout = %d, %d, %d, want 10, 999, 30", p.Stopwaitsecs, p.Priority, cfg.Supervisor.ShutdownTimeout)
	}
}

func TestUnsafeOrNegativeSettingsRejected(t *testing.T) {
	for _, tc := range []struct {
		name, toml, want string
	}{
		{"stopwaitsecs 0", zeroProgram + "stopwaitsecs = 0\n", `programs.web: stopwaitsecs must be >= 1, got 0; use stopsignal = "KILL" for an immediate kill`},
		{"stopwaitsecs negative", zeroProgram + "stopwaitsecs = -5\n", "programs.web: stopwaitsecs must be >= 1, got -5"},
		{"shutdown_timeout 0", "[supervisor]\nshutdown_timeout = 0\n\n" + zeroProgram, "supervisor.shutdown_timeout must be >= 1, got 0"},
		{"shutdown_timeout negative", "[supervisor]\nshutdown_timeout = -1\n\n" + zeroProgram, "supervisor.shutdown_timeout must be >= 1, got -1"},
		{"numprocs 0", zeroProgram + "numprocs = 0\n", "programs.web: numprocs must be >= 1, got 0"},
		{"startsecs negative", zeroProgram + "startsecs = -1\n", "programs.web: startsecs must be >= 0, got -1"},
		{"startretries negative", zeroProgram + "startretries = -1\n", "programs.web: startretries must be >= 0, got -1"},
		{"stdout backups negative", zeroProgram + "stdout_logfile_backups = -1\n", "programs.web: stdout_logfile_backups must be >= 0, got -1"},
		{"stderr backups negative", zeroProgram + "stderr_logfile_backups = -1\n", "programs.web: stderr_logfile_backups must be >= 0, got -1"},
		{"minfds negative", "[supervisor]\nminfds = -1\n\n" + zeroProgram, "supervisor.minfds must be >= 0, got -1"},
		{"minprocs negative", "[supervisor]\nminprocs = -1\n\n" + zeroProgram, "supervisor.minprocs must be >= 0, got -1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := LoadBytes([]byte(tc.toml), "test.toml")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func emptyMetadata() toml.MetaData { return toml.MetaData{} }
