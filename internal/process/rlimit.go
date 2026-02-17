package process

import (
	"strconv"
	"strings"
	"syscall"

	"github.com/kahiteam/kahi/internal/config"
)

// ParseRLimits extracts resource limits from a ProgramConfig.
// Resource limits are specified in the config as environment-style entries.
func ParseRLimits(cfg config.ProgramConfig) []RLimit {
	var limits []RLimit

	// Check for well-known resource limit config keys in the environment.
	for k, v := range cfg.Environment {
		k = strings.ToUpper(k)
		var resource int
		switch k {
		case "KAHI_RLIMIT_NOFILE":
			resource = int(syscall.RLIMIT_NOFILE)
		case "KAHI_RLIMIT_NPROC":
			resource = rlimitNproc
		case "KAHI_RLIMIT_CORE":
			resource = int(syscall.RLIMIT_CORE)
		case "KAHI_RLIMIT_FSIZE":
			resource = int(syscall.RLIMIT_FSIZE)
		case "KAHI_RLIMIT_AS":
			resource = rlimitAS
		case "KAHI_RLIMIT_DATA":
			resource = int(syscall.RLIMIT_DATA)
		case "KAHI_RLIMIT_STACK":
			resource = int(syscall.RLIMIT_STACK)
		case "KAHI_RLIMIT_RSS":
			resource = rlimitRSS
		default:
			continue
		}

		cur, max, ok := parseRLimitValue(v)
		if !ok {
			continue
		}
		limits = append(limits, RLimit{
			Resource: resource,
			Cur:      cur,
			Max:      max,
		})
	}

	return limits
}

// parseRLimitValue parses "soft:hard" or "value" into cur and max.
func parseRLimitValue(s string) (cur, max uint64, ok bool) {
	parts := strings.SplitN(s, ":", 2)
	if len(parts) == 2 {
		c, err1 := parseUint64(parts[0])
		m, err2 := parseUint64(parts[1])
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return c, m, true
	}

	val, err := parseUint64(parts[0])
	if err != nil {
		return 0, 0, false
	}
	return val, val, true
}

func parseUint64(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if strings.ToLower(s) == "unlimited" || s == "-1" {
		return ^uint64(0), nil // RLIM_INFINITY
	}
	return strconv.ParseUint(s, 10, 64)
}

// ApplyRLimits sets resource limits on the current process.
// This should be called in the child process before exec.
func ApplyRLimits(limits []RLimit) error {
	for _, rl := range limits {
		lim := syscall.Rlimit{
			Cur: rl.Cur,
			Max: rl.Max,
		}
		if err := syscall.Setrlimit(rl.Resource, &lim); err != nil {
			return err
		}
	}
	return nil
}
