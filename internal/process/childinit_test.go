package process

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// TestMain lets the test binary act as the spawn trampoline. ExecSpawner
// re-execs os.Executable() (the test binary under `go test`) with the
// ChildInitArg sentinel; without this hook those re-execs would fall through to
// the normal test runner instead of applying rlimits/umask and exec'ing the
// real target.
func TestMain(m *testing.M) {
	if IsChildInit(os.Args) {
		if err := RunChildInit(os.Args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(127)
		}
		return // unreachable: RunChildInit execs on success
	}
	os.Exit(m.Run())
}

// spawnCapture spawns cfg through the real ExecSpawner, returns the child's
// stdout, and fails the test unless the child exits successfully.
func spawnCapture(t *testing.T, cfg SpawnConfig) string {
	t.Helper()
	s := &ExecSpawner{}
	sp, err := s.Spawn(cfg)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	out, err := io.ReadAll(sp.StdoutPipe())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	st, err := sp.Wait()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !st.Success() {
		t.Fatalf("child exit code = %d, want 0 (stdout=%q)", st.ExitCode(), out)
	}
	return string(out)
}

// clampToHard keeps a desired soft/hard target at or below the current hard
// limit so an unprivileged setrlimit in the child cannot fail with EPERM.
func clampToHard(t *testing.T, resource int, desired uint64) uint64 {
	t.Helper()
	var rl syscall.Rlimit
	if err := syscall.Getrlimit(resource, &rl); err != nil {
		t.Fatalf("getrlimit(%d): %v", resource, err)
	}
	hard := uint64(rl.Max)
	if hard != 0 && hard < desired {
		return hard
	}
	return desired
}

// SEC-021: a configured NOFILE limit must take effect in the spawned child.
func TestSpawnAppliesNofileRLimit(t *testing.T) {
	target := clampToHard(t, int(syscall.RLIMIT_NOFILE), 512)
	out := spawnCapture(t, SpawnConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "ulimit -n"},
		Umask:   -1,
		RLimits: []RLimit{{Resource: int(syscall.RLIMIT_NOFILE), Cur: target, Max: target}},
	})
	got := strings.TrimSpace(out)
	if got != strconv.FormatUint(target, 10) {
		t.Fatalf("child ulimit -n = %q, want %d", got, target)
	}
}

// SEC-021: a configured NPROC limit must take effect in the spawned child.
func TestSpawnAppliesNprocRLimit(t *testing.T) {
	// Use a high-but-below-hard soft limit: it is still an observable change
	// (asserted via ulimit -u) yet stays well above the live per-uid process
	// count, so the child can spawn. A low absolute value (e.g. 137) fails on
	// busy CI runners whose uid already exceeds it.
	target := clampToHard(t, rlimitNproc, 65535)
	out := spawnCapture(t, SpawnConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "ulimit -u"},
		Umask:   -1,
		RLimits: []RLimit{{Resource: rlimitNproc, Cur: target, Max: target}},
	})
	got := strings.TrimSpace(out)
	if got != strconv.FormatUint(target, 10) {
		t.Fatalf("child ulimit -u = %q, want %d", got, target)
	}
}

// SEC-021: a configured umask must shape the mode of files the child creates.
func TestSpawnAppliesUmask(t *testing.T) {
	dir := t.TempDir()
	created := filepath.Join(dir, "file")
	spawnCapture(t, SpawnConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", `: > "$1"`, "sh", created},
		Umask:   0o077,
	})
	info, err := os.Stat(created)
	if err != nil {
		t.Fatalf("stat created file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("created file mode = %#o, want 0600 (umask 077)", perm)
	}
}

// SEC-021: with neither rlimit nor umask configured, the child takes the direct
// spawn path and inherits the supervisor's umask.
func TestSpawnInheritsUmaskWhenUnconfigured(t *testing.T) {
	prev := syscall.Umask(0o025)
	defer syscall.Umask(prev)

	out := spawnCapture(t, SpawnConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", "umask"},
		Umask:   -1,
	})
	got := strings.TrimSpace(out)
	// sh prints the umask in octal; assert it reflects the inherited 0025.
	if v, err := strconv.ParseInt(got, 8, 32); err != nil || int(v) != 0o025 {
		t.Fatalf("inherited umask = %q, want octal 025", got)
	}
}

func TestEncodeDecodeRLimitsRoundTrip(t *testing.T) {
	limits := []RLimit{
		{Resource: int(syscall.RLIMIT_NOFILE), Cur: 1024, Max: 65536},
		{Resource: rlimitNproc, Cur: 137, Max: 137},
		{Resource: int(syscall.RLIMIT_CORE), Cur: ^uint64(0), Max: ^uint64(0)},
	}
	decoded, err := decodeRLimits(encodeRLimits(limits))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(decoded, limits) {
		t.Fatalf("round trip = %+v, want %+v", decoded, limits)
	}
}

func TestEncodeRLimitsEmpty(t *testing.T) {
	if s := encodeRLimits(nil); s != "" {
		t.Fatalf("encodeRLimits(nil) = %q, want empty", s)
	}
	decoded, err := decodeRLimits("")
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if decoded != nil {
		t.Fatalf("decodeRLimits(\"\") = %+v, want nil", decoded)
	}
}

func TestDecodeRLimitsInvalid(t *testing.T) {
	for _, in := range []string{"1:2", "a:2:3", "1:2:3:4"} {
		if _, err := decodeRLimits(in); err == nil {
			t.Fatalf("decodeRLimits(%q): expected error", in)
		}
	}
}

func TestParseChildInit(t *testing.T) {
	argv := []string{"kahi", ChildInitArg, "18", "7:100:200", "--", "/bin/sh", "-c", "true"}
	umask, limits, real, err := parseChildInit(argv)
	if err != nil {
		t.Fatalf("parseChildInit: %v", err)
	}
	if umask != 18 {
		t.Fatalf("umask = %d, want 18", umask)
	}
	want := []RLimit{{Resource: 7, Cur: 100, Max: 200}}
	if !reflect.DeepEqual(limits, want) {
		t.Fatalf("limits = %+v, want %+v", limits, want)
	}
	if !reflect.DeepEqual(real, []string{"/bin/sh", "-c", "true"}) {
		t.Fatalf("real = %v", real)
	}
}

func TestParseChildInitMalformed(t *testing.T) {
	cases := [][]string{
		{"kahi", ChildInitArg, "18", "", "--"},                    // missing target
		{"kahi", ChildInitArg, "18", "", "NOTSEP", "/bin/sh"},     // missing separator
		{"kahi", ChildInitArg, "notint", "", "--", "/bin/sh"},     // bad umask
		{"kahi", ChildInitArg, "18", "bad:spec", "--", "/bin/sh"}, // bad rlimit
	}
	for _, argv := range cases {
		if _, _, _, err := parseChildInit(argv); err == nil {
			t.Fatalf("parseChildInit(%v): expected error", argv)
		}
	}
}

func TestIsChildInit(t *testing.T) {
	if !IsChildInit([]string{"kahi", ChildInitArg, "x"}) {
		t.Fatal("expected IsChildInit true for sentinel argv")
	}
	if IsChildInit([]string{"kahi", "status"}) {
		t.Fatal("expected IsChildInit false for normal argv")
	}
	if IsChildInit([]string{"kahi"}) {
		t.Fatal("expected IsChildInit false for bare argv")
	}
}
