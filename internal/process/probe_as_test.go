package process

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/userspec"
)

// searchableBase is a temp dir whose parents every user can search (/tmp);
// t.TempDir on macOS sits below a 0700 per-user directory.
func searchableBase(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "kahi-acc-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestAccessAsModeBits(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the fixtures are owned by the test user; as root they would be root's")
	}
	me := userspec.Credential{Uid: uint32(os.Geteuid()), Gid: uint32(os.Getegid())}
	other := userspec.Credential{Uid: 4242, Gid: 4242}
	base := searchableBase(t)
	mk := func(name string, mode os.FileMode) string {
		p := filepath.Join(base, name)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	private, open, grp := mk("private", 0o700), mk("open", 0o755), mk("grp", 0o750)
	// A new directory's group is its parent's on BSD and macOS: use it.
	fi, err := os.Stat(grp)
	if err != nil {
		t.Fatal(err)
	}
	group := userspec.Credential{Uid: 4242, Gid: 4242, Groups: []uint32{fi.Sys().(*syscall.Stat_t).Gid}}
	for _, tc := range []struct {
		name string
		path string
		cred userspec.Credential
		want uint32
		ok   bool
	}{
		{"owner", private, me, permX, true},
		{"other on 0700", private, other, permX, false},
		{"other on 0755 search", open, other, permX, true},
		{"other on 0755 write", open, other, permW | permX, false},
		{"group member on 0750", grp, group, permX, true},
		{"non-member on 0750", grp, other, permX, false},
		{"below a 0700 parent", filepath.Join(private, "x"), other, permX, false},
		{"root", private, userspec.Credential{}, permW, true},
	} {
		err := accessAs(tc.path, tc.cred, tc.want)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}

// As root, the checks as the drop user or the program's user report what
// that user cannot use (#293).
func TestProbeProgramsAs(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the fixtures are owned by the test user")
	}
	base := searchableBase(t)
	locked := filepath.Join(base, "locked")
	logs := filepath.Join(base, "logs")
	for _, d := range []string{locked, logs} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Programs: map[string]config.ProgramConfig{
		"web": {Command: "/bin/sh", Directory: locked, StdoutLogfile: filepath.Join(logs, "web.log"), Numprocs: 1, ProcessName: "%(program_name)s"},
		"own": {Command: "/bin/sh", Directory: locked, User: "app", Numprocs: 1, ProcessName: "%(program_name)s"},
	}}
	drop := userspec.Credential{Uid: 4242, Gid: 4242, Name: "kahi"}
	got := strings.Join(ProbeProgramsAs(cfg, true, ProgramUsers{Drop: &drop}), "\n")
	for _, want := range []string{
		"programs.web.directory: cannot access directory: " + locked + ": permission denied (as kahi (uid 4242))",
		"programs.web.stdout_logfile: " + filepath.Join(logs, "web.log") + ": permission denied (as kahi (uid 4242))",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("drop: lacks %q in:\n%s", want, got)
		}
	}
	app := func(string) (userspec.Credential, error) { return userspec.Credential{Uid: 4343, Gid: 4343}, nil }
	got = strings.Join(ProbeProgramsAs(cfg, true, ProgramUsers{ProgramUser: app}), "\n")
	if !strings.Contains(got, "programs.own.directory: cannot access directory: "+locked+": permission denied (as uid 4343)") {
		t.Errorf("program user: %s", got)
	}
	if strings.Contains(got, "programs.web.") || strings.Contains(got, "stdout_logfile") {
		t.Errorf("without a drop, web runs and logs as root: %s", got)
	}
}
