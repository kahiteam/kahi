package process

import (
	"testing"

	"github.com/kahiteam/kahi/internal/config"
)

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
