package process

import (
	"syscall"
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

// TestApplyRLimits exercises ApplyRLimits directly by lowering the current
// process's NOFILE soft limit and reading it back. The spawn-path coverage that
// applies limits to a child lives in TestSpawnAppliesNofileRLimit and
// TestSpawnAppliesNprocRLimit.
func TestApplyRLimits(t *testing.T) {
	var orig syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &orig); err != nil {
		t.Fatalf("getrlimit: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &orig) })

	target := uint64(256)
	if hard := uint64(orig.Max); hard != 0 && hard < target {
		target = hard
	}
	if err := ApplyRLimits([]RLimit{{
		Resource: int(syscall.RLIMIT_NOFILE),
		Cur:      target,
		Max:      target,
	}}); err != nil {
		t.Fatalf("ApplyRLimits: %v", err)
	}

	var got syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &got); err != nil {
		t.Fatalf("getrlimit after apply: %v", err)
	}
	if uint64(got.Cur) != target {
		t.Fatalf("NOFILE soft = %d, want %d", uint64(got.Cur), target)
	}
}

func TestApplyRLimitsEmpty(t *testing.T) {
	if err := ApplyRLimits(nil); err != nil {
		t.Fatalf("ApplyRLimits(nil) = %v, want nil", err)
	}
}

func TestParseRLimitsNofile(t *testing.T) {
	cfg := config.ProgramConfig{
		Environment: map[string]string{
			"KAHI_RLIMIT_NOFILE": "65536",
		},
	}

	limits := ParseRLimits(cfg)
	if len(limits) != 1 {
		t.Fatalf("expected 1 limit, got %d", len(limits))
	}
	if limits[0].Cur != 65536 || limits[0].Max != 65536 {
		t.Fatalf("cur=%d, max=%d, want 65536:65536", limits[0].Cur, limits[0].Max)
	}
}

func TestParseRLimitsSoftHard(t *testing.T) {
	cfg := config.ProgramConfig{
		Environment: map[string]string{
			"KAHI_RLIMIT_NOFILE": "1024:65536",
		},
	}

	limits := ParseRLimits(cfg)
	if len(limits) != 1 {
		t.Fatalf("expected 1 limit, got %d", len(limits))
	}
	if limits[0].Cur != 1024 || limits[0].Max != 65536 {
		t.Fatalf("cur=%d, max=%d, want 1024:65536", limits[0].Cur, limits[0].Max)
	}
}

func TestParseRLimitsUnlimited(t *testing.T) {
	cfg := config.ProgramConfig{
		Environment: map[string]string{
			"KAHI_RLIMIT_CORE": "unlimited",
		},
	}

	limits := ParseRLimits(cfg)
	if len(limits) != 1 {
		t.Fatalf("expected 1 limit, got %d", len(limits))
	}
	if limits[0].Cur != ^uint64(0) {
		t.Fatalf("cur = %d, want RLIM_INFINITY", limits[0].Cur)
	}
}

func TestParseRLimitsEmpty(t *testing.T) {
	cfg := config.ProgramConfig{
		Environment: map[string]string{
			"APP_KEY": "value",
		},
	}

	limits := ParseRLimits(cfg)
	if len(limits) != 0 {
		t.Fatalf("expected 0 limits, got %d", len(limits))
	}
}

func TestParseRLimitsMultiple(t *testing.T) {
	cfg := config.ProgramConfig{
		Environment: map[string]string{
			"KAHI_RLIMIT_NOFILE": "65536",
			"KAHI_RLIMIT_CORE":   "0",
		},
	}

	limits := ParseRLimits(cfg)
	if len(limits) != 2 {
		t.Fatalf("expected 2 limits, got %d", len(limits))
	}
}

func TestParseRLimitsInvalidValue(t *testing.T) {
	cfg := config.ProgramConfig{
		Environment: map[string]string{
			"KAHI_RLIMIT_NOFILE": "notanumber",
		},
	}

	limits := ParseRLimits(cfg)
	if len(limits) != 0 {
		t.Fatalf("expected 0 limits (invalid value), got %d", len(limits))
	}
}
