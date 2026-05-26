package process

import "testing"

// SEC-009: a configured per-process user must be attached to the spawn as a
// credential when running as root, must be ignored (with a warning) when not
// root, and an unparseable user must fail the spawn closed.

func TestSpawnAttachesCredentialWhenRoot(t *testing.T) {
	orig := geteuid
	geteuid = func() int { return 0 }
	defer func() { geteuid = orig }()

	cfg := defaultProgramConfig()
	cfg.User = "1000:2000"
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if len(spawner.SpawnCalls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawner.SpawnCalls))
	}
	attr := spawner.SpawnCalls[0].SysProcAttr
	if attr == nil || attr.Credential == nil {
		t.Fatal("credential not attached to spawn config")
	}
	if attr.Credential.Uid != 1000 || attr.Credential.Gid != 2000 {
		t.Fatalf("uid:gid = %d:%d, want 1000:2000", attr.Credential.Uid, attr.Credential.Gid)
	}
}

func TestSpawnIgnoresUserWhenNotRoot(t *testing.T) {
	orig := geteuid
	geteuid = func() int { return 1000 }
	defer func() { geteuid = orig }()

	cfg := defaultProgramConfig()
	cfg.User = "1000:2000"
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if len(spawner.SpawnCalls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(spawner.SpawnCalls))
	}
	if attr := spawner.SpawnCalls[0].SysProcAttr; attr != nil && attr.Credential != nil {
		t.Fatal("credential must not be attached when not running as root")
	}
}

func TestSpawnFailsClosedOnInvalidUser(t *testing.T) {
	orig := geteuid
	geteuid = func() int { return 0 }
	defer func() { geteuid = orig }()

	cfg := defaultProgramConfig()
	cfg.Startretries = 0 // go straight to FATAL, no lingering retry goroutine
	cfg.User = "not-a-uid"
	spawner := &MockSpawner{}
	p := NewProcess("test", "test", cfg, spawner, testBus(), testLogger())

	if err := p.Start(); err == nil {
		t.Fatal("Start must fail closed on an unparseable user")
	}
	if len(spawner.SpawnCalls) != 0 {
		t.Fatalf("must not spawn with invalid user, got %d spawn calls", len(spawner.SpawnCalls))
	}
	if p.State() == Running {
		t.Fatal("process must not be RUNNING with an invalid user")
	}
}
