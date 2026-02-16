// Package config handles loading and validating Kahi configuration.
package config

// Config is the top-level Kahi configuration.
type Config struct {
	Supervisor SupervisorConfig         `toml:"supervisor"`
	Programs   map[string]ProgramConfig `toml:"programs"`
	Groups     map[string]GroupConfig   `toml:"groups"`
	Server     ServerConfig             `toml:"server"`
	Webhooks   map[string]WebhookConfig `toml:"webhooks"`
	Include    []string                 `toml:"include"`
}

// SupervisorConfig holds daemon-level settings.
type SupervisorConfig struct {
	Logfile         string `toml:"logfile"`
	LogLevel        string `toml:"log_level"`
	LogFormat       string `toml:"log_format"`
	Directory       string `toml:"directory"`
	Identifier      string `toml:"identifier"`
	Minfds          int    `toml:"minfds"`
	Minprocs        int    `toml:"minprocs"`
	Nocleanup       bool   `toml:"nocleanup"`
	ShutdownTimeout int    `toml:"shutdown_timeout"`
}

// ProgramConfig holds per-program settings.
type ProgramConfig struct {
	Command               string            `toml:"command"`
	ProcessName           string            `toml:"process_name"`
	Numprocs              int               `toml:"numprocs"`
	NumprocsStart         int               `toml:"numprocs_start"`
	Priority              int               `toml:"priority"`
	Autostart             *bool             `toml:"autostart"`
	Autorestart           string            `toml:"autorestart"`
	Startsecs             int               `toml:"startsecs"`
	Startretries          int               `toml:"startretries"`
	Exitcodes             []int             `toml:"exitcodes"`
	Stopsignal            string            `toml:"stopsignal"`
	Stopwaitsecs          int               `toml:"stopwaitsecs"`
	Stopasgroup           bool              `toml:"stopasgroup"`
	Killasgroup           bool              `toml:"killasgroup"`
	User                  string            `toml:"user"`
	Directory             string            `toml:"directory"`
	Umask                 string            `toml:"umask"`
	Environment           map[string]string `toml:"environment"`
	CleanEnvironment      bool              `toml:"clean_environment"`
	StdoutLogfile         string            `toml:"stdout_logfile"`
	StdoutLogfileMaxbytes string            `toml:"stdout_logfile_maxbytes"`
	StdoutLogfileBackups  int               `toml:"stdout_logfile_backups"`
	StdoutCaptureMaxbytes string            `toml:"stdout_capture_maxbytes"`
	StdoutSyslog          bool              `toml:"stdout_syslog"`
	StderrLogfile         string            `toml:"stderr_logfile"`
	StderrLogfileMaxbytes string            `toml:"stderr_logfile_maxbytes"`
	StderrLogfileBackups  int               `toml:"stderr_logfile_backups"`
	StderrCaptureMaxbytes string            `toml:"stderr_capture_maxbytes"`
	StderrSyslog          bool              `toml:"stderr_syslog"`
	RedirectStderr        bool              `toml:"redirect_stderr"`
	StripAnsi             bool              `toml:"strip_ansi"`
	Description           string            `toml:"description"`
}

// GroupConfig holds per-group settings.
type GroupConfig struct {
	Programs []string `toml:"programs"`
	Priority int      `toml:"priority"`
}

// ServerConfig holds server listener settings.
type ServerConfig struct {
	Unix UnixServerConfig `toml:"unix"`
	HTTP HTTPServerConfig `toml:"http"`
}

// UnixServerConfig holds Unix domain socket settings.
type UnixServerConfig struct {
	File  string `toml:"file"`
	Chmod string `toml:"chmod"`
	Chown string `toml:"chown"`
}

// HTTPServerConfig holds HTTP server settings.
type HTTPServerConfig struct {
	Enabled  bool   `toml:"enabled"`
	Listen   string `toml:"listen"`
	Username string `toml:"username"`
	Password string `toml:"password"`
}

// WebhookConfig holds per-webhook settings.
type WebhookConfig struct {
	URL     string            `toml:"url"`
	Events  []string          `toml:"events"`
	Headers map[string]string `toml:"headers"`
	Timeout int               `toml:"timeout"`
	Retries int               `toml:"retries"`
}
