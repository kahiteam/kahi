package config

// ApplyDefaults fills in zero-value fields with their default values.
func ApplyDefaults(cfg *Config) {
	// Supervisor defaults.
	if cfg.Supervisor.LogLevel == "" {
		cfg.Supervisor.LogLevel = "info"
	}
	if cfg.Supervisor.LogFormat == "" {
		cfg.Supervisor.LogFormat = "json"
	}
	if cfg.Supervisor.Minfds == 0 {
		cfg.Supervisor.Minfds = 1024
	}
	if cfg.Supervisor.Minprocs == 0 {
		cfg.Supervisor.Minprocs = 200
	}
	if cfg.Supervisor.ShutdownTimeout == 0 {
		cfg.Supervisor.ShutdownTimeout = 30
	}

	// Server defaults.
	if cfg.Server.Unix.File == "" {
		cfg.Server.Unix.File = "/var/run/kahi.sock"
	}
	if cfg.Server.Unix.Chmod == "" {
		cfg.Server.Unix.Chmod = "0700"
	}

	// Program defaults.
	for name, p := range cfg.Programs {
		if p.Numprocs == 0 {
			p.Numprocs = 1
		}
		if p.Priority == 0 {
			p.Priority = 999
		}
		if p.Autostart == nil {
			t := true
			p.Autostart = &t
		}
		if p.Autorestart == "" {
			p.Autorestart = "unexpected"
		}
		if p.Startsecs == 0 {
			p.Startsecs = 1
		}
		if p.Startretries == 0 {
			p.Startretries = 3
		}
		if len(p.Exitcodes) == 0 {
			p.Exitcodes = []int{0}
		}
		if p.Stopsignal == "" {
			p.Stopsignal = "TERM"
		}
		if p.Stopwaitsecs == 0 {
			p.Stopwaitsecs = 10
		}
		if p.StdoutLogfileMaxbytes == "" {
			p.StdoutLogfileMaxbytes = "50MB"
		}
		if p.StdoutLogfileBackups == 0 {
			p.StdoutLogfileBackups = 10
		}
		if p.StderrLogfileMaxbytes == "" {
			p.StderrLogfileMaxbytes = "50MB"
		}
		if p.StderrLogfileBackups == 0 {
			p.StderrLogfileBackups = 10
		}
		cfg.Programs[name] = p
	}
}
