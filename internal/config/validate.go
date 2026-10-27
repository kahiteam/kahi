package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/kahiteam/kahi/internal/logging"
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

		if !validAutorestartValues[string(p.Autorestart)] {
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

		// A stop timeout of 0 or less sends SIGKILL right after the stop
		// signal, with no grace period (#138).
		if p.Stopwaitsecs < 1 {
			errs = append(errs, fmt.Errorf("%s: stopwaitsecs must be >= 1, got %d; use stopsignal = \"KILL\" for an immediate kill", prefix, p.Stopwaitsecs))
		}
		for _, f := range []struct {
			key string
			v   int
		}{
			{"startsecs", p.Startsecs},
			{"startretries", p.Startretries},
			{"stdout_logfile_backups", p.StdoutLogfileBackups},
			{"stderr_logfile_backups", p.StderrLogfileBackups},
		} {
			if f.v < 0 {
				errs = append(errs, fmt.Errorf("%s: %s must be >= 0, got %d", prefix, f.key, f.v))
			}
		}

		for _, f := range []struct{ key, v string }{
			{"stdout_logfile_maxbytes", p.StdoutLogfileMaxbytes},
			{"stderr_logfile_maxbytes", p.StderrLogfileMaxbytes},
		} {
			// An unparseable size used to mean no limit (#187).
			if err := logging.CheckSize(f.v); err != nil {
				errs = append(errs, fmt.Errorf("%s: %s %v", prefix, f.key, err))
			}
		}

		errs = append(errs, validateRLimits(prefix, p.Environment)...)
	}

	// shutdown_timeout 0 or less would SIGKILL every process the moment
	// shutdown begins (#138).
	if cfg.Supervisor.ShutdownTimeout < 1 {
		errs = append(errs, fmt.Errorf("supervisor.shutdown_timeout must be >= 1, got %d", cfg.Supervisor.ShutdownTimeout))
	}
	if cfg.Supervisor.Minfds < 0 {
		errs = append(errs, fmt.Errorf("supervisor.minfds must be >= 0, got %d", cfg.Supervisor.Minfds))
	}
	if cfg.Supervisor.Minprocs < 0 {
		errs = append(errs, fmt.Errorf("supervisor.minprocs must be >= 0, got %d", cfg.Supervisor.Minprocs))
	}

	// Fail-closed TCP authentication (SEC-015): enabling the HTTP listener
	// requires credentials for any bind address, loopback included. Loopback
	// is not a trust boundary (shared across a network namespace), so it gets
	// no exemption. The password-free local path is the Unix socket.
	if cfg.Server.HTTP.Enabled {
		if strings.TrimSpace(cfg.Server.HTTP.Username) == "" || cfg.Server.HTTP.Password == "" {
			errs = append(errs, fmt.Errorf("http listener on %s requires username/password", cfg.Server.HTTP.Listen))
		}
		if err := validateListen(cfg.Server.HTTP.Listen); err != nil {
			errs = append(errs, err)
		}
	}

	// bcrypt-only passwords (SEC-018): a configured password must be a bcrypt
	// hash; plaintext is rejected at startup.
	if pw := cfg.Server.HTTP.Password; pw != "" && !strings.HasPrefix(pw, "$2") {
		errs = append(errs, fmt.Errorf("http.password must be a bcrypt hash"))
	}

	errs = append(errs, validateUnixServer(&cfg.Server.Unix)...)

	// Browsers cannot reach the Unix socket, so a Web UI without the TCP
	// listener would be unreachable configuration.
	if cfg.Server.Web.Enabled && !cfg.Server.HTTP.Enabled {
		errs = append(errs, fmt.Errorf("server.web.enabled requires server.http.enabled"))
	}

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

// validateListen checks [server.http] listen as host:port, so a malformed
// address is a config error instead of a bind failure (FUNC-025).
func validateListen(listen string) error {
	_, port, err := net.SplitHostPort(listen)
	if err == nil {
		_, err = net.LookupPort("tcp", port)
	}
	if err != nil {
		return fmt.Errorf("invalid listen address: %q: %v", listen, err)
	}
	return nil
}

// listenWarnings flags an HTTP listener bound to all interfaces. It is a load
// warning, so it is printed even when validation then refuses the config
// (SEC-015).
func listenWarnings(cfg *Config) []string {
	if !cfg.Server.HTTP.Enabled {
		return nil
	}
	host, _, err := net.SplitHostPort(cfg.Server.HTTP.Listen)
	if err != nil || (host != "" && host != "0.0.0.0" && host != "::") {
		return nil
	}
	return []string{fmt.Sprintf("server.http.listen %s binds all interfaces; the API is reachable from the network", cfg.Server.HTTP.Listen)}
}
