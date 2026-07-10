package process

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

// differingUser returns a "uid:gid" string whose uid is guaranteed to differ
// from the test runner's own uid, so buildEnv treats the child as
// privilege-differentiated.
func differingUser(t *testing.T) string {
	t.Helper()
	uid := 12345
	if os.Getuid() == uid {
		uid = 12346
	}
	return fmt.Sprintf("%d:%d", uid, uid)
}

func newBuildEnvProcess(t *testing.T, cfg config.ProgramConfig) *Process {
	t.Helper()
	return NewProcess("worker-0", "worker", cfg, &MockSpawner{}, testBus(), testLogger())
}

func TestBuildEnvDifferingUserOmitsSupervisorSecrets(t *testing.T) {
	t.Setenv("ROOT_SECRET", "leak-me")

	cfg := defaultProgramConfig()
	cfg.User = differingUser(t)
	cfg.Environment = map[string]string{"APP_KEY": "explicit"}
	p := newBuildEnvProcess(t, cfg)

	envMap := envToMap(p.buildEnv())

	if _, ok := envMap["ROOT_SECRET"]; ok {
		t.Fatal("privilege-differentiated child inherited ROOT_SECRET")
	}
	if envMap["APP_KEY"] != "explicit" {
		t.Fatalf("APP_KEY = %q, want explicit", envMap["APP_KEY"])
	}
}

func TestBuildEnvDifferingUserMinimalBase(t *testing.T) {
	cfg := defaultProgramConfig()
	cfg.User = differingUser(t)
	p := newBuildEnvProcess(t, cfg)

	env := p.buildEnv()
	envMap := envToMap(env)

	if envMap["PATH"] == "" {
		t.Fatal("minimal base missing PATH")
	}
	if _, ok := envMap["HOME"]; !ok {
		t.Fatal("minimal base missing HOME")
	}
	if envMap["SUPERVISOR_ENABLED"] != "1" {
		t.Fatal("minimal base missing SUPERVISOR_ENABLED")
	}
	if envMap["SUPERVISOR_PROCESS_NAME"] != "worker-0" {
		t.Fatalf("SUPERVISOR_PROCESS_NAME = %q, want worker-0", envMap["SUPERVISOR_PROCESS_NAME"])
	}

	// The base is minimal: only PATH, HOME and SUPERVISOR_* keys (plus any
	// explicit environment, none here).
	allowed := map[string]bool{
		"PATH": true, "HOME": true,
		"SUPERVISOR_ENABLED": true, "SUPERVISOR_PROCESS_NAME": true, "SUPERVISOR_GROUP_NAME": true,
	}
	for k := range envMap {
		if !allowed[k] {
			t.Fatalf("unexpected key %q in minimal base", k)
		}
	}
}

func TestBuildEnvDefaultPathWhenSupervisorHasNone(t *testing.T) {
	t.Setenv("PATH", "")

	cfg := defaultProgramConfig()
	cfg.User = differingUser(t)
	p := newBuildEnvProcess(t, cfg)

	envMap := envToMap(p.buildEnv())
	if envMap["PATH"] != defaultCleanPath {
		t.Fatalf("PATH = %q, want %q", envMap["PATH"], defaultCleanPath)
	}
}

func TestBuildEnvNoUserInheritsFullEnvironment(t *testing.T) {
	t.Setenv("ROOT_SECRET", "leak-me")

	cfg := defaultProgramConfig()
	cfg.User = ""
	p := newBuildEnvProcess(t, cfg)

	envMap := envToMap(p.buildEnv())
	if envMap["ROOT_SECRET"] != "leak-me" {
		t.Fatal("same-user program should inherit supervisor environment (backward compatible)")
	}
}

func TestBuildEnvSameUserInheritsFullEnvironment(t *testing.T) {
	t.Setenv("ROOT_SECRET", "leak-me")

	cfg := defaultProgramConfig()
	cfg.User = fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
	p := newBuildEnvProcess(t, cfg)

	envMap := envToMap(p.buildEnv())
	if envMap["ROOT_SECRET"] != "leak-me" {
		t.Fatal("child running as the supervisor uid should inherit the environment")
	}
}

func TestBuildEnvDifferingUserOptInInherits(t *testing.T) {
	t.Setenv("ROOT_SECRET", "leak-me")

	cfg := defaultProgramConfig()
	cfg.User = differingUser(t)
	cfg.InheritEnvironment = true
	p := newBuildEnvProcess(t, cfg)

	envMap := envToMap(p.buildEnv())
	if envMap["ROOT_SECRET"] != "leak-me" {
		t.Fatal("explicit inherit_environment opt-in should receive the full environment")
	}
}

func TestBuildEnvResolveHomeFromPasswd(t *testing.T) {
	u, err := user.LookupId("0")
	if err != nil {
		t.Skip("uid 0 not resolvable in this environment")
	}
	if u.HomeDir == "" {
		t.Skip("uid 0 has no home directory recorded")
	}

	p := newBuildEnvProcess(t, defaultProgramConfig())
	if got := p.resolveHome(0); got != u.HomeDir {
		t.Fatalf("resolveHome(0) = %q, want %q", got, u.HomeDir)
	}
}

func TestBuildEnvResolveHomeFallsBackToRoot(t *testing.T) {
	// Find a uid that the passwd database does not resolve. Some platforms map
	// well-known sentinel uids (e.g. 2^32-2 to "nobody"), so scan for a hole.
	var target uint32
	found := false
	for _, uid := range []uint32{2000000123, 1999999991, 1888888883, 1777777771} {
		if _, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err != nil {
			target = uid
			found = true
			break
		}
	}
	if !found {
		t.Skip("no unresolvable uid available in this environment")
	}

	p := newBuildEnvProcess(t, defaultProgramConfig())
	if got := p.resolveHome(target); got != "/" {
		t.Fatalf("resolveHome(%d) = %q, want /", target, got)
	}
}

func TestBuildEnvDifferingUserHomeMatchesTargetUser(t *testing.T) {
	u, err := user.LookupId("0")
	if err != nil || u.HomeDir == "" || os.Getuid() == 0 {
		t.Skip("cannot exercise a differing resolvable target user here")
	}

	cfg := defaultProgramConfig()
	cfg.User = "0:0"
	p := newBuildEnvProcess(t, cfg)

	envMap := envToMap(p.buildEnv())
	if envMap["HOME"] != u.HomeDir {
		t.Fatalf("HOME = %q, want target user home %q", envMap["HOME"], u.HomeDir)
	}
}

func TestBuildEnvDifferingUserViaStart(t *testing.T) {
	t.Setenv("ROOT_SECRET", "leak-me")

	spawner := &MockSpawner{}
	cfg := defaultProgramConfig()
	cfg.User = differingUser(t)
	p := NewProcess("worker-0", "worker", cfg, spawner, testBus(), testLogger())

	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	if len(spawner.SpawnCalls) != 1 {
		t.Fatalf("expected 1 spawn call, got %d", len(spawner.SpawnCalls))
	}

	for _, e := range spawner.SpawnCalls[0].Env {
		if strings.HasPrefix(e, "ROOT_SECRET=") {
			t.Fatal("spawned child received supervisor secret ROOT_SECRET")
		}
	}
}
