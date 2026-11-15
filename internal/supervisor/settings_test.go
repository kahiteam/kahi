package supervisor

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
	"golang.org/x/sys/unix"
)

// fakeLimits is an rlimit table; capRaise allows raising the hard limit.
type fakeLimits struct {
	lim      map[int]unix.Rlimit
	capRaise bool
	sets     int
}

func (f *fakeLimits) ops(goos string) rlimitOps {
	return rlimitOps{
		get: func(r int, l *unix.Rlimit) error { *l = f.lim[r]; return nil },
		set: func(r int, l *unix.Rlimit) error {
			f.sets++
			if l.Max > f.lim[r].Max && !f.capRaise {
				return syscall.EPERM
			}
			f.lim[r] = *l
			return nil
		},
		goos:     goos,
		maxFiles: func() (uint64, bool) { return 10240, true },
	}
}

func logBuf() (*slog.Logger, *bytes.Buffer) {
	var b bytes.Buffer
	return slog.New(slog.NewTextHandler(&b, nil)), &b
}

func TestRaiseLimitsSoftUpToHard(t *testing.T) {
	f := &fakeLimits{lim: map[int]unix.Rlimit{unix.RLIMIT_NOFILE: {Cur: 1024, Max: 8192}, unix.RLIMIT_NPROC: {Cur: 100, Max: 1024}}}
	logger, _ := logBuf()
	s := config.SupervisorConfig{Minfds: 4096, MinfdsSet: true, Minprocs: 512, MinprocsSet: true}
	if err := raiseLimits(f.ops("linux"), s, logger); err != nil {
		t.Fatal(err)
	}
	if got := f.lim[unix.RLIMIT_NOFILE]; got.Cur != 4096 || got.Max != 8192 {
		t.Errorf("NOFILE %+v, want 4096/8192", got)
	}
	if got := f.lim[unix.RLIMIT_NPROC]; got.Cur != 512 || got.Max != 1024 {
		t.Errorf("NPROC %+v, want 512/1024", got)
	}
}

func TestRaiseLimitsUntouchedWhenEnough(t *testing.T) {
	f := &fakeLimits{lim: map[int]unix.Rlimit{unix.RLIMIT_NOFILE: {Cur: 8192, Max: 8192}, unix.RLIMIT_NPROC: {Cur: 4096, Max: 4096}}}
	logger, _ := logBuf()
	if err := raiseLimits(f.ops("linux"), config.SupervisorConfig{Minfds: 1024, Minprocs: 200}, logger); err != nil || f.sets != 0 {
		t.Fatalf("err %v, sets %d; want nil and no setrlimit", err, f.sets)
	}
}

func TestRaiseLimitsExplicitAboveHardFails(t *testing.T) {
	f := &fakeLimits{lim: map[int]unix.Rlimit{unix.RLIMIT_NOFILE: {Cur: 1024, Max: 2048}, unix.RLIMIT_NPROC: {Cur: 4096, Max: 4096}}}
	logger, _ := logBuf()
	err := raiseLimits(f.ops("linux"), config.SupervisorConfig{Minfds: 4096, MinfdsSet: true, Minprocs: 200}, logger)
	want := "cannot set NOFILE to 4096: hard limit is 2048 (current soft=1024, hard=2048)"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestRaiseLimitsDefaultAboveHardWarns(t *testing.T) {
	f := &fakeLimits{lim: map[int]unix.Rlimit{unix.RLIMIT_NOFILE: {Cur: 8192, Max: 8192}, unix.RLIMIT_NPROC: {Cur: 50, Max: 100}}}
	logger, out := logBuf()
	if err := raiseLimits(f.ops("linux"), config.SupervisorConfig{Minfds: 1024, Minprocs: 200}, logger); err != nil {
		t.Fatal(err)
	}
	if got := f.lim[unix.RLIMIT_NPROC]; got.Cur != 100 {
		t.Errorf("NPROC soft %d, want raised to the hard limit 100", got.Cur)
	}
	if !strings.Contains(out.String(), "NPROC is 100, below the default minprocs 200; set supervisor.minprocs = 100 or lower to silence this") {
		t.Errorf("log %q lacks the default-value warning", out)
	}
}

func TestRaiseLimitsRootRaisesHard(t *testing.T) {
	f := &fakeLimits{capRaise: true, lim: map[int]unix.Rlimit{unix.RLIMIT_NOFILE: {Cur: 1024, Max: 2048}, unix.RLIMIT_NPROC: {Cur: 4096, Max: 4096}}}
	logger, _ := logBuf()
	if err := raiseLimits(f.ops("linux"), config.SupervisorConfig{Minfds: 4096, MinfdsSet: true, Minprocs: 200}, logger); err != nil {
		t.Fatal(err)
	}
	if got := f.lim[unix.RLIMIT_NOFILE]; got.Cur != 4096 || got.Max != 4096 {
		t.Errorf("NOFILE %+v, want 4096/4096", got)
	}
}

func TestRaiseLimitsDarwin(t *testing.T) {
	f := &fakeLimits{lim: map[int]unix.Rlimit{unix.RLIMIT_NOFILE: {Cur: 256, Max: unix.RLIM_INFINITY}, unix.RLIMIT_NPROC: {Cur: 100, Max: 100}}}
	logger, out := logBuf()
	err := raiseLimits(f.ops("darwin"), config.SupervisorConfig{Minfds: 20000, MinfdsSet: true, Minprocs: 200}, logger)
	if err == nil || !strings.Contains(err.Error(), "cannot set NOFILE to 20000: exceeds kern.maxfilesperproc (10240)") {
		t.Fatalf("err = %v, want the kern.maxfilesperproc error", err)
	}
	f2 := &fakeLimits{lim: map[int]unix.Rlimit{unix.RLIMIT_NOFILE: {Cur: 8192, Max: 8192}, unix.RLIMIT_NPROC: {Cur: 100, Max: 100}}}
	if err := raiseLimits(f2.ops("darwin"), config.SupervisorConfig{Minfds: 1024, Minprocs: 512, MinprocsSet: true}, logger); err != nil {
		t.Fatalf("macOS NPROC refusal exits: %v", err)
	}
	if !strings.Contains(out.String(), "cannot set NPROC to 512") || !strings.Contains(out.String(), "continuing (macOS)") {
		t.Errorf("log %q lacks the macOS NPROC warning", out)
	}
}

func TestRaiseLimitsSetFailure(t *testing.T) {
	ops := rlimitOps{
		get: func(r int, l *unix.Rlimit) error { *l = unix.Rlimit{Cur: 100, Max: 1000}; return nil },
		set: func(int, *unix.Rlimit) error { return errors.New("denied") },
	}
	logger, out := logBuf()
	err := raiseLimits(ops, config.SupervisorConfig{Minfds: 500, MinfdsSet: true}, logger)
	if err == nil || err.Error() != "cannot set NOFILE to 500: denied (current soft=100, hard=1000)" {
		t.Fatalf("explicit: err = %v", err)
	}
	if err := raiseLimits(ops, config.SupervisorConfig{Minfds: 500, Minprocs: 200}, logger); err != nil {
		t.Fatalf("default: err = %v, want a warning only", err)
	}
	if !strings.Contains(out.String(), "cannot set NOFILE to 500: denied") {
		t.Errorf("log %q lacks the warning", out)
	}
}

func TestChangeDirectory(t *testing.T) {
	if err := ChangeDirectory(""); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "nope")
	if err := ChangeDirectory(missing); err == nil || err.Error() != "directory does not exist: "+missing {
		t.Fatalf("err = %v", err)
	}
	wd, _ := os.Getwd()
	defer func() { _ = os.Chdir(wd) }()
	d := t.TempDir()
	if err := ChangeDirectory(d); err != nil {
		t.Fatal(err)
	}
	got, _ := os.Getwd()
	if r, _ := filepath.EvalSymlinks(d); got != r && got != d {
		t.Fatalf("cwd %s, want %s", got, d)
	}
}

func TestSetUmask(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	if err := SetUmask("0027"); err != nil {
		t.Fatal(err)
	}
	if got := syscall.Umask(0o022); got != 0o027 {
		t.Fatalf("umask %04o, want 0027", got)
	}
	if err := SetUmask(""); err != nil {
		t.Fatal(err)
	}
	if got := syscall.Umask(0o022); got != 0o022 {
		t.Fatalf("empty umask changed it to %04o", got)
	}
}
