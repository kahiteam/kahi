package supervisor

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
	"github.com/kahiteam/kahi/internal/userspec"
	"golang.org/x/sys/unix"
)

// errnoOf returns the syscall errno an error wraps, or 0.
func errnoOf(err error) syscall.Errno {
	var e syscall.Errno
	if errors.As(err, &e) {
		return e
	}
	return 0
}

// sameFailure asserts the ADR-054 parity rule: the probe fails exactly where
// the step fails, with the same message up to and including path and the
// same errno.
func sameFailure(t *testing.T, name, path string, probe, step error) {
	t.Helper()
	if (probe == nil) != (step == nil) {
		t.Fatalf("%s: probe %v, step %v", name, probe, step)
	}
	if probe == nil {
		return
	}
	head := func(err error) string {
		s := err.Error()
		if i := strings.Index(s, path); i >= 0 {
			return s[:i+len(path)]
		}
		return s
	}
	if head(probe) != head(step) {
		t.Errorf("%s: probe %q, step %q", name, probe, step)
	}
	if errnoOf(probe) != errnoOf(step) {
		t.Errorf("%s: probe errno %v, step errno %v (%v / %v)", name, errnoOf(probe), errnoOf(step), probe, step)
	}
}

func TestProbeDirectory_MatchesChangeDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	base := t.TempDir()
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(base, "locked")
	if err := os.Mkdir(locked, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"empty":    "",
		"missing":  filepath.Join(base, "missing"),
		"file":     file,
		"ok":       base,
		"noSearch": locked,
	}
	for name, dir := range cases {
		if name == "noSearch" && os.Geteuid() == 0 {
			continue
		}
		probe := ProbeDirectory(dir)
		step := ChangeDirectory(dir)
		if (probe == nil) != (step == nil) || (probe != nil && probe.Error() != step.Error()) {
			t.Errorf("%s: probe %v, step %v", name, probe, step)
		}
	}
}

func TestProbeSocketDir_MatchesPrepareSocketDir(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"parentMissing": filepath.Join(base, "a", "b", "k.sock"),
		"notDir":        filepath.Join(file, "k.sock"),
		"created":       filepath.Join(base, "new", "k.sock"),
		"ok":            filepath.Join(base, "k.sock"),
	}
	if os.Geteuid() != 0 {
		ro := filepath.Join(base, "ro")
		if err := os.Mkdir(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		cases["unwritable"] = filepath.Join(ro, "k.sock")
		cases["parentUnwritable"] = filepath.Join(ro, "sub", "k.sock")
	}
	for name, sock := range cases {
		probe := ProbeSocketDir(sock)
		if name == "created" {
			if _, err := os.Lstat(filepath.Dir(sock)); err == nil {
				t.Fatalf("%s: the probe created %s", name, filepath.Dir(sock))
			}
		}
		_, step := PrepareSocketDir(sock)
		sameFailure(t, name, filepath.Dir(sock), probe, step)
	}
}

func TestProbePrivateDir_MatchesEnsurePrivateDir(t *testing.T) {
	base := t.TempDir()
	open := filepath.Join(base, "open")
	if err := os.Mkdir(open, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(base, link); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"missing":       filepath.Join(base, "private"),
		"parentMissing": filepath.Join(base, "x", "private"),
		"groupBits":     open,
		"symlink":       link,
	}
	for name, dir := range cases {
		probe := probePrivateDir(dir)
		if name == "missing" {
			if _, err := os.Lstat(dir); err == nil {
				t.Fatalf("probe created %s", dir)
			}
		}
		_, step := ensurePrivateDir(dir)
		sameFailure(t, name, dir, probe, step)
	}
}

func TestProbePIDFile_MatchesCheckAndWrite(t *testing.T) {
	base := t.TempDir()
	stale := filepath.Join(base, "stale.pid")
	if err := os.WriteFile(stale, []byte("999999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(base, "live.pid")
	if err := os.WriteFile(live, []byte(strconv.Itoa(os.Getppid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirAtPath := filepath.Join(base, "dir.pid")
	if err := os.Mkdir(dirAtPath, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"missingDir": filepath.Join(base, "nope", "k.pid"),
		"new":        filepath.Join(base, "k.pid"),
		"stale":      stale,
		"live":       live,
		"directory":  dirAtPath,
	}
	for name, path := range cases {
		var pb, sb bytes.Buffer
		pid, probe := ProbePIDFile(path, slog.New(slog.NewTextHandler(&pb, nil)))
		if name == "new" {
			if _, err := os.Lstat(path); err == nil {
				t.Fatalf("probe created %s", path)
			}
		}
		step := CheckPIDFile(path, slog.New(slog.NewTextHandler(&sb, nil)))
		if name == "live" {
			if pid != os.Getppid() || step == nil {
				t.Errorf("live: probe pid %d, step %v", pid, step)
			}
			continue
		}
		if step == nil {
			step = WritePIDFile(path)
		}
		sameFailure(t, name, path, probe, step)
		if strings.Contains(sb.String(), "stale") != strings.Contains(pb.String(), "stale") {
			t.Errorf("%s: stale warning differs: probe %q, step %q", name, pb.String(), sb.String())
		}
	}
}

func TestProbeLimits_MatchesRaiseLimits(t *testing.T) {
	limits := []map[int]unix.Rlimit{
		{unix.RLIMIT_NOFILE: {Cur: 256, Max: 256}, unix.RLIMIT_NPROC: {Cur: 100, Max: 100}},
		{unix.RLIMIT_NOFILE: {Cur: 1024, Max: 8192}, unix.RLIMIT_NPROC: {Cur: 100, Max: 1024}},
	}
	configs := []config.SupervisorConfig{
		{Minfds: 1024, MinfdsSet: true},
		{Minfds: 1024},
		{Minprocs: 512, MinprocsSet: true},
		{Minprocs: 512},
		{Minfds: 20000, MinfdsSet: true},
	}
	for _, goos := range []string{"linux", "darwin"} {
		for _, root := range []bool{false, true} {
			for li, lim := range limits {
				for ci, s := range configs {
					copyLim := func() map[int]unix.Rlimit {
						m := map[int]unix.Rlimit{}
						for k, v := range lim {
							m[k] = v
						}
						return m
					}
					step := &fakeLimits{lim: copyLim(), capRaise: root}
					probe := &fakeLimits{lim: copyLim()}
					sl, sb := logBuf()
					pl, pbuf := logBuf()
					old := geteuid
					geteuid = func() int {
						if root {
							return 0
						}
						return 1000
					}
					perr := probeLimits(probe.ops(goos), s, pl)
					geteuid = old
					serr := raiseLimits(step.ops(goos), s, sl)
					if (perr == nil) != (serr == nil) || (perr != nil && perr.Error() != serr.Error()) {
						t.Errorf("%s root=%v lim%d cfg%d: probe %v, step %v", goos, root, li, ci, perr, serr)
					}
					if probe.lim[unix.RLIMIT_NOFILE] != lim[unix.RLIMIT_NOFILE] || probe.lim[unix.RLIMIT_NPROC] != lim[unix.RLIMIT_NPROC] {
						t.Errorf("probe changed a limit")
					}
					if strings.Contains(sb.String(), "WARN") != strings.Contains(pbuf.String(), "WARN") {
						t.Errorf("%s root=%v lim%d cfg%d: warnings differ: probe %q, step %q", goos, root, li, ci, pbuf.String(), sb.String())
					}
				}
			}
		}
	}
}

func TestCheckDrop(t *testing.T) {
	oldResolve, oldUID := resolveDropUser, getuid
	t.Cleanup(func() { resolveDropUser, getuid = oldResolve, oldUID })
	resolveDropUser = func(name string) (userspec.Credential, error) {
		if name == "app" {
			return userspec.Credential{Uid: 1000, Gid: 1000}, nil
		}
		return userspec.Credential{}, errors.New("user not found: " + name)
	}

	getuid = func() int { return 1000 }
	if dc, err := CheckDrop("", ""); err != nil || dc.Drop || dc.Note != "" {
		t.Errorf("no user: %+v %v", dc, err)
	}
	if _, err := CheckDrop("nosuch", ""); err == nil || err.Error() != "user not found: nosuch" {
		t.Errorf("unknown -u: %v", err)
	}
	if _, err := CheckDrop("", "nosuch"); err == nil {
		t.Error("unknown [supervisor] user as non-root must fail the check")
	}
	dc, err := CheckDrop("app", "other")
	if err != nil || !dc.Drop || !dc.Override || dc.Note != "not root; the privilege drop was not checked" {
		t.Errorf("non-root -u: %+v %v", dc, err)
	}

	getuid = func() int { return 0 }
	if dc, err := CheckDrop("", "app"); err != nil || !dc.Drop || dc.Override || dc.Note != "" {
		t.Errorf("root [supervisor] user: %+v %v", dc, err)
	}
}

// ProbeLimits uses the real getrlimit and changes nothing.
func TestProbeLimits_Real(t *testing.T) {
	var before, after unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &before); err != nil {
		t.Fatal(err)
	}
	logger, _ := logBuf()
	if err := ProbeLimits(config.SupervisorConfig{Minfds: int(before.Cur), MinfdsSet: true}, logger); err != nil {
		t.Errorf("minfds at the soft limit: %v", err)
	}
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Errorf("limits changed: %+v -> %+v", before, after)
	}
}
