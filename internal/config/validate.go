package config

import (
	"fmt"
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

	// Fail-closed TCP authentication (SEC-015): enabling the HTTP listener
	// requires credentials for any bind address, loopback included. Loopback
	// is not a trust boundary (shared across a network namespace), so it gets
	// no exemption. The password-free local path is the Unix socket.
	if cfg.Server.HTTP.Enabled {
		if strings.TrimSpace(cfg.Server.HTTP.Username) == "" || cfg.Server.HTTP.Password == "" {
			errs = append(errs, fmt.Errorf("http listener on %s requires username/password", cfg.Server.HTTP.Listen))
		}
	}

	return errs
}
