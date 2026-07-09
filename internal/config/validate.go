package config

import (
	"fmt"
	"strconv"
	"strings"
)

// validSignals lists the supported stop signals.
var validSignals = map[string]bool{
	"TERM": true, "HUP": true, "INT": true, "QUIT": true,
	"KILL": true, "USR1": true, "USR2": true,
}

// validAutorestartValues lists the allowed autorestart values.
var validAutorestartValues = map[string]bool{
	"true": true, "false": true, "unexpected": true,
}

// Validate checks the config for semantic errors and returns all of them.
func Validate(cfg *Config) []error {
	var errs []error

	for name, p := range cfg.Programs {
		prefix := fmt.Sprintf("programs.%s", name)

		if strings.TrimSpace(p.Command) == "" {
			errs = append(errs, fmt.Errorf("%s: command is required", prefix))
		}

		if p.Priority < 0 || p.Priority > 999 {
			errs = append(errs, fmt.Errorf("%s: priority must be between 0 and 999, got %d", prefix, p.Priority))
		}

		if !validAutorestartValues[p.Autorestart] {
			errs = append(errs, fmt.Errorf("%s: autorestart must be true, false, or unexpected, got %q", prefix, p.Autorestart))
		}

		sig := strings.TrimPrefix(strings.ToUpper(p.Stopsignal), "SIG")
		if !validSignals[sig] {
			errs = append(errs, fmt.Errorf("%s: invalid stopsignal %q", prefix, p.Stopsignal))
		}

		if p.Stopasgroup && !p.Killasgroup {
			errs = append(errs, fmt.Errorf("%s: killasgroup cannot be false when stopasgroup is true", prefix))
		}

		if p.Numprocs < 1 {
			errs = append(errs, fmt.Errorf("%s: numprocs must be >= 1, got %d", prefix, p.Numprocs))
		}
	}

	errs = append(errs, validateUnixServer(&cfg.Server.Unix)...)

	return errs
}

// validateUnixServer enforces the owner-only control socket policy (SEC-022).
// The Unix socket authorizes by transport, not peer identity, so any mode that
// grants group or other access would hand unrestricted control to every process
// that can reach the socket. The chown field is dead config that was never
// applied at bind, so it is rejected rather than silently ignored.
func validateUnixServer(unix *UnixServerConfig) []error {
	var errs []error

	if strings.TrimSpace(unix.Chown) != "" {
		errs = append(errs, fmt.Errorf("server.unix.chown is not supported; socket is owner-only"))
	}

	if mode := strings.TrimSpace(unix.Chmod); mode != "" {
		perm, err := strconv.ParseUint(mode, 8, 32)
		if err != nil {
			errs = append(errs, fmt.Errorf("server.unix.chmod must be an octal mode, got %q", mode))
		} else if perm&0o077 != 0 {
			errs = append(errs, fmt.Errorf("control socket must be owner-only (0700); got %s", mode))
		}
	}

	return errs
}
