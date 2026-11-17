// Package config handles loading and validating Kahi configuration.
package config

import (
	"fmt"
	"strconv"
)

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
	Umask           string `toml:"umask"`
	User            string `toml:"user"` // drop to this user after binding (FUNC-045)
	// SyslogAddress is where process output is forwarded with
	// stdout_syslog/stderr_syslog: "" (local socket), unix:///path,
	// udp://host:port or tcp://host:port (FUNC-020).
	SyslogAddress string `toml:"syslog_address"`

	// MinfdsSet and MinprocsSet report whether the key is in the file: an
	// explicit value that cannot be met is an error, a default only warns
	// (FUNC-068).
	MinfdsSet   bool `toml:"-" json:"-"`
	MinprocsSet bool `toml:"-" json:"-"`
}

// ProgramConfig holds per-program settings.
type ProgramConfig struct {
	Command               string            `toml:"command"`
	ProcessName           string            `toml:"process_name"`
	Numprocs              int               `toml:"numprocs"`
	NumprocsStart         int               `toml:"numprocs_start"`
	Priority              int               `toml:"priority"`
	Autostart             *bool             `toml:"autostart"`
	Autorestart           Autorestart       `toml:"autorestart"`
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
	InheritEnvironment    bool              `toml:"inherit_environment"`
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
	SyslogFacility        string            `toml:"syslog_facility"` // "" = LOCAL0
	SyslogPriority        string            `toml:"syslog_priority"` // "" = INFO
	StdoutEventsEnabled   bool              `toml:"stdout_events_enabled"`
	StderrEventsEnabled   bool              `toml:"stderr_events_enabled"`
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
	Web  WebServerConfig  `toml:"web"`
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
	Username string `toml:"username" json:"-"`
	Password string `toml:"password" json:"-"`
}

// WebServerConfig holds Web UI settings. The UI is served under /ui/ on the
// API listeners and is off by default.
type WebServerConfig struct {
	Enabled   bool   `toml:"enabled"`
	StaticDir string `toml:"static_dir"`
}

// WebhookConfig holds per-webhook settings (FUNC-056..059, ADR-043).
type WebhookConfig struct {
	URL           string            `toml:"url"`
	Events        []string          `toml:"events"`
	Headers       map[string]string `toml:"headers"`
	Timeout       int               `toml:"timeout"`     // seconds per POST, default 5
	MaxRetries    int               `toml:"max_retries"` // retries after the first attempt, default 3
	Retries       int               `toml:"retries"`     // deprecated alias of max_retries
	Template      string            `toml:"template"`    // generic (default), slack, pagerduty
	AllowInsecure bool              `toml:"allow_insecure"`
	RoutingKey    string            `toml:"routing_key"` // PagerDuty integration key

	// RetriesSet reports that the deprecated retries key is in the file.
	RetriesSet bool `toml:"-" json:"-"`
}

// Autorestart is a program's autorestart policy: "true", "false" or
// "unexpected". It accepts the TOML booleans as well as strings, so
// autorestart = true is not a parse error (#181).
type Autorestart string

// UnmarshalTOML implements toml.Unmarshaler.
func (a *Autorestart) UnmarshalTOML(v any) error {
	switch v := v.(type) {
	case bool:
		*a = Autorestart(strconv.FormatBool(v))
	case string:
		*a = Autorestart(v)
	default:
		return fmt.Errorf("autorestart must be true, false, or \"unexpected\", got %v (%T)", v, v)
	}
	return nil
}
