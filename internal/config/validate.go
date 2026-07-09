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

// knownRLimits lists the resource-limit environment keys the supervisor
// applies to child processes. Values must be "unlimited", "-1", a single
// unsigned integer, or a "soft:hard" pair of unsigned integers.
var knownRLimits = map[string]bool{
	"KAHI_RLIMIT_NOFILE": true,
	"KAHI_RLIMIT_NPROC":  true,
	"KAHI_RLIMIT_CORE":   true,
	"KAHI_RLIMIT_FSIZE":  true,
	"KAHI_RLIMIT_AS":     true,
	"KAHI_RLIMIT_DATA":   true,
	"KAHI_RLIMIT_STACK":  true,
	"KAHI_RLIMIT_RSS":    true,
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

		errs = append(errs, validateRLimits(prefix, p.Environment)...)
	}

	// Fail-closed TCP authentication (SEC-015): enabling the HTTP listener
	// requires credentials for any bind address, loopback included. Loopback
	// is not a trust boundary (shared across a network namespace), so it gets
	// no exemption. The password-free local path is the Unix socket.
	if cfg.Server.HTTP.Enabled {
		if strings.TrimSpace(cfg.Server.HTTP.Username) == "" || cfg.Server.HTTP.Password == "" {
			errs = append(errs, fmt.Errorf("http listener on %s requires username/password", cfg.Server.HTTP.Listen))
		}
	}

	// bcrypt-only passwords (SEC-018): a configured password must be a bcrypt
	// hash; plaintext is rejected at startup.
	if pw := cfg.Server.HTTP.Password; pw != "" && !strings.HasPrefix(pw, "$2") {
		errs = append(errs, fmt.Errorf("http.password must be a bcrypt hash"))
	}

	return errs
}

// validateRLimits rejects malformed resource-limit entries so a bad value fails
// at config validation rather than being silently dropped at spawn time.
func validateRLimits(prefix string, env map[string]string) []error {
	var errs []error
	for k, v := range env {
		name := strings.ToUpper(k)
		if !strings.HasPrefix(name, "KAHI_RLIMIT_") {
			continue
		}
		if !knownRLimits[name] {
			errs = append(errs, fmt.Errorf("%s: invalid rlimit %s: unknown resource", prefix, name))
			continue
		}
		if !validRLimitValue(v) {
			errs = append(errs, fmt.Errorf("%s: invalid rlimit %s: %s", prefix, name, v))
		}
	}
	return errs
}

// validRLimitValue reports whether s is a valid rlimit value: "unlimited",
// "-1", a single unsigned integer, or a "soft:hard" pair.
func validRLimitValue(s string) bool {
	parts := strings.SplitN(s, ":", 2)
	for _, p := range parts {
		if !validRLimitComponent(p) {
			return false
		}
	}
	return true
}

func validRLimitComponent(s string) bool {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "unlimited") || s == "-1" {
		return true
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}
