package process

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// ChildInitArg is the sentinel first argument that marks a spawn-trampoline
// invocation. When a program configures resource limits or a umask, the
// supervisor re-execs itself with this argument so the limits/umask can be
// applied in the child (post-fork, pre-exec) before the real target is exec'd.
// This mirrors credential handling, which is applied to the child rather than
// the supervisor, and avoids mutating the supervisor's own process state.
const ChildInitArg = "__spawn"

// IsChildInit reports whether argv marks a spawn-trampoline invocation. The
// caller (main and the process package's TestMain) checks this before normal
// command dispatch so cobra never parses the trampoline arguments.
func IsChildInit(argv []string) bool {
	return len(argv) > 1 && argv[1] == ChildInitArg
}

// RunChildInit applies the rlimits and umask encoded in argv and then execs the
// real target, replacing the current process image. It returns only on error;
// on success syscall.Exec never returns. The pid is preserved across the exec,
// so the supervisor's recorded pid is the real target's pid.
func RunChildInit(argv []string) error {
	umask, limits, real, err := parseChildInit(argv)
	if err != nil {
		return err
	}
	if err := ApplyRLimits(limits); err != nil {
		return fmt.Errorf("cannot apply resource limits: %w", err)
	}
	ApplyUmask(umask)

	path := real[0]
	if !strings.Contains(path, "/") {
		// syscall.Exec does not search $PATH; resolve bare names the way
		// exec.Command would in the direct-spawn path.
		if resolved, lookErr := exec.LookPath(path); lookErr == nil {
			path = resolved
		}
	}
	return syscall.Exec(path, real, os.Environ())
}

// parseChildInit decodes the trampoline argv layout:
//
//	[self, ChildInitArg, umask, rlimits, "--", command, args...]
func parseChildInit(argv []string) (umask int, limits []RLimit, real []string, err error) {
	if len(argv) < 6 || argv[1] != ChildInitArg || argv[4] != "--" {
		return 0, nil, nil, fmt.Errorf("malformed spawn trampoline invocation")
	}
	umask, err = strconv.Atoi(argv[2])
	if err != nil {
		return 0, nil, nil, fmt.Errorf("invalid trampoline umask %q: %w", argv[2], err)
	}
	limits, err = decodeRLimits(argv[3])
	if err != nil {
		return 0, nil, nil, err
	}
	real = argv[5:]
	if len(real) == 0 {
		return 0, nil, nil, fmt.Errorf("spawn trampoline missing target command")
	}
	return umask, limits, real, nil
}

// encodeRLimits serializes limits as "resource:cur:max" entries joined by
// commas. An empty slice encodes to the empty string.
func encodeRLimits(limits []RLimit) string {
	if len(limits) == 0 {
		return ""
	}
	parts := make([]string, len(limits))
	for i, rl := range limits {
		parts[i] = fmt.Sprintf("%d:%d:%d", rl.Resource, rl.Cur, rl.Max)
	}
	return strings.Join(parts, ",")
}

// decodeRLimits parses the encoding produced by encodeRLimits.
func decodeRLimits(s string) ([]RLimit, error) {
	if s == "" {
		return nil, nil
	}
	entries := strings.Split(s, ",")
	limits := make([]RLimit, 0, len(entries))
	for _, entry := range entries {
		fields := strings.Split(entry, ":")
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid rlimit spec %q", entry)
		}
		resource, err1 := strconv.Atoi(fields[0])
		cur, err2 := strconv.ParseUint(fields[1], 10, 64)
		max, err3 := strconv.ParseUint(fields[2], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("invalid rlimit spec %q", entry)
		}
		limits = append(limits, RLimit{Resource: resource, Cur: cur, Max: max})
	}
	return limits, nil
}
