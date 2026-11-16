package process

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kahiteam/kahi/internal/userspec"
)

// The package's tests never read the host's user database: resolveUser is a
// fixed table. Numeric ids resolve without an entry; "www-data" is 33 with
// groups 33 and 4; every other name is not found.
func init() {
	resolveUser = fakeResolve
}

func fakeResolve(spec string) (userspec.Credential, error) {
	name, group, hasGroup := strings.Cut(spec, ":")
	var c userspec.Credential
	switch {
	case name == "www-data":
		c = userspec.Credential{Uid: 33, Gid: 33, Groups: []uint32{4, 33}, Name: "www-data", Home: "/var/www", HasEntry: true}
	case name != "" && strings.Trim(name, "0123456789") == "":
		uid, err := strconv.ParseUint(name, 10, 32)
		if err != nil {
			return c, fmt.Errorf("invalid user %q", spec)
		}
		c = userspec.Credential{Uid: uint32(uid), Gid: uint32(uid)}
	default:
		return c, fmt.Errorf("user not found: %s", name)
	}
	if hasGroup {
		gid, err := strconv.ParseUint(group, 10, 32)
		if err != nil {
			return c, fmt.Errorf("group not found: %s", group)
		}
		c.Gid, c.ExplicitGroup = uint32(gid), true
	}
	if !c.HasEntry {
		c.Groups = []uint32{c.Gid}
	}
	return c, nil
}

func asRoot(t *testing.T, euid int) {
	t.Helper()
	orig := geteuid
	geteuid = func() int { return euid }
	t.Cleanup(func() { geteuid = orig })
}

// FUNC-046 / SEC-010: a name resolves to its uid and primary gid, and the
// child gets the user's supplementary groups instead of none.
func TestSpawnAttachesResolvedGroups(t *testing.T) {
	asRoot(t, 0)
	cfg := defaultProgramConfig()
	cfg.User = "www-data"
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cred := spawner.SpawnCalls[0].SysProcAttr.Credential
	if cred.Uid != 33 || cred.Gid != 33 || !reflect.DeepEqual(cred.Groups, []uint32{4, 33}) || cred.NoSetGroups {
		t.Errorf("credential = %+v, want uid/gid 33 with groups [4 33]", cred)
	}
}

// A numeric uid without a passwd entry gets its gid as its only group.
func TestSpawnNumericUserGroups(t *testing.T) {
	asRoot(t, 0)
	cfg := defaultProgramConfig()
	cfg.User = "4242"
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cred := spawner.SpawnCalls[0].SysProcAttr.Credential
	if cred.Uid != 4242 || cred.Gid != 4242 || !reflect.DeepEqual(cred.Groups, []uint32{4242}) {
		t.Errorf("credential = %+v, want 4242 with groups [4242]", cred)
	}
}

// An unknown user is a spawn error, never a start with root's privileges.
func TestSpawnUnknownUserIsSpawnError(t *testing.T) {
	asRoot(t, 0)
	cfg := defaultProgramConfig()
	cfg.Startretries = 0
	cfg.User = "nosuch"
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	if err := p.Start(); err == nil {
		t.Fatal("Start succeeded with an unknown user")
	}
	if got := p.SpawnError(); got != "spawn error: user not found: nosuch" {
		t.Errorf("SpawnError = %q, want spawn error: user not found: nosuch", got)
	}
	if len(spawner.SpawnCalls) != 0 {
		t.Fatalf("spawned %d times with an unknown user", len(spawner.SpawnCalls))
	}
	if p.State() != Fatal {
		t.Errorf("state = %s, want FATAL", p.State())
	}
}

func bufLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// A non-root daemon (for example after -u) warns for a user it cannot
// switch to, but not for one that is already its own identity.
func TestSpawnNonRootUserWarning(t *testing.T) {
	asRoot(t, 1000)
	for spec, wantWarn := range map[string]bool{
		strconv.Itoa(os.Getuid()):                                     false,
		strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()):   false,
		strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()+1): true,
		"www-data": os.Getuid() != 33,
		"nosuch":   true,
	} {
		logger, buf := bufLogger()
		cfg := defaultProgramConfig()
		cfg.User = spec
		spawner := &MockSpawner{}
		p := NewProcess("test", "test", cfg, spawner, testBus(), logger)
		if err := p.Start(); err != nil {
			t.Fatalf("%s: Start: %v", spec, err)
		}
		if attr := spawner.SpawnCalls[0].SysProcAttr; attr != nil && attr.Credential != nil {
			t.Errorf("%s: credential attached by a non-root daemon", spec)
		}
		got := strings.Contains(buf.String(), "user switching unavailable (not running as root), running as current user")
		if got != wantWarn {
			t.Errorf("%s: warning logged = %v, want %v; log:\n%s", spec, got, wantWarn, buf.String())
		}
	}
}

// SEC-016: a name resolving to another uid is privilege-differentiated and
// gets the minimal environment with that user's home.
func TestBuildEnvForNamedUser(t *testing.T) {
	if os.Getuid() == 33 {
		t.Skip("running as uid 33")
	}
	cfg := defaultProgramConfig()
	cfg.User = "www-data"
	p := NewProcess("test", "test", cfg, &MockSpawner{}, testBus(), testLogger())
	env := envToMap(p.buildEnv())
	if env["HOME"] != "/var/www" {
		t.Errorf("HOME = %q, want /var/www", env["HOME"])
	}
	if _, ok := env["SHELL"]; ok && os.Getenv("SHELL") != "" {
		t.Error("the supervisor's environment was inherited")
	}
}

// One spawn resolves the user once: the environment and the credential come
// from the same answer. A lookup that fails and then succeeds (a user being
// added) must not start a child as that user with the daemon's environment
// (SEC-016).
func TestSpawnResolvesUserOnce(t *testing.T) {
	asRoot(t, 0)
	orig := resolveUser
	t.Cleanup(func() { resolveUser = orig })
	calls := 0
	resolveUser = func(spec string) (userspec.Credential, error) {
		calls++
		if calls == 1 {
			return userspec.Credential{}, fmt.Errorf("user not found: %s", spec)
		}
		return userspec.Credential{Uid: 1002, Gid: 1002, Groups: []uint32{1002}, HasEntry: true}, nil
	}
	cfg := defaultProgramConfig()
	cfg.Startretries = 0
	cfg.User = "late"
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())
	if err := p.Start(); err == nil {
		t.Fatal("Start succeeded although the user did not resolve")
	}
	if len(spawner.SpawnCalls) != 0 {
		t.Fatalf("spawned %d times", len(spawner.SpawnCalls))
	}
	if calls != 1 {
		t.Errorf("resolved %d times in one spawn, want 1", calls)
	}
}
