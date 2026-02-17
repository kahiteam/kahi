# Feature Specification: Kahi

## Overview

**Project:** Kahi -- Lightweight process supervisor for modern infrastructure
**Version:** 1.0.0
**Last Updated:** 2026-02-16
**Status:** Draft

### Summary

Kahi is a modern process supervisor for POSIX systems, written in Go. It manages long-running processes with automatic restart, health monitoring, structured logging, and a REST API. Designed as a container-first replacement for Python's supervisord, it ships as a single static binary with zero runtime dependencies.

### Scope

- Process lifecycle management (start, stop, restart, autorestart, backoff)
- TOML-based configuration with hot reload
- REST/JSON API with SSE streaming, Prometheus metrics, optional Web UI
- Event system with pub/sub, event listeners, and webhook notifications
- CLI control, health probes, structured logging
- supervisord.conf migration tool
- FastCGI process management

---

## Infrastructure Features

Infrastructure features have NO dependencies. They establish the foundation.

### INFRA-001: Go Module and Project Structure

**Description:** Initialize the Go module and establish the canonical directory layout for the Kahi project.

**Acceptance Criteria:**

- [ ] `go.mod` exists with module path `github.com/<org>/kahi` and Go 1.26.0 minimum
- [ ] `cmd/kahi/main.go` exists as the single binary entry point
- [ ] `internal/` directory contains packages: `config`, `process`, `supervisor`, `api`, `events`, `logging`, `ctl`, `migrate`, `fcgi`
- [ ] `go build ./cmd/kahi` produces a working binary
- [ ] `go vet ./...` passes with zero findings

**Dependencies:** None

---

### INFRA-002: Taskfile Development Workflow

**Description:** Configure Taskfile for local development tasks: build, test, lint, format, coverage, vet.

**Acceptance Criteria:**

- [ ] `Taskfile.yml` exists in project root
- [ ] `task build` compiles the binary to `./bin/kahi`
- [ ] `task test` runs all tests with race detector (`-race`)
- [ ] `task lint` runs `golangci-lint run ./...`
- [ ] `task fmt` runs `gofmt -w .` and reports if files changed
- [ ] `task vet` runs `go vet ./...`
- [ ] `task coverage` generates coverage report and fails if below 85%
- [ ] `task all` runs fmt, vet, lint, test, build in sequence

**Dependencies:** None

---

### INFRA-003: GoReleaser Configuration

**Description:** Configure GoReleaser for cross-platform release builds with FIPS support.

**Acceptance Criteria:**

- [ ] `.goreleaser.yml` exists in project root
- [ ] Builds for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
- [ ] FIPS build variant produced with `GOFIPS140=v1.0.0` environment variable
- [ ] Builds use CGO_ENABLED=0 for static binaries
- [ ] Version info injected via ldflags (version, commit, date)
- [ ] `goreleaser check` validates the config
- [ ] Checksum file generated for all artifacts

**Dependencies:** None

---

### INFRA-004: CLI Framework with Subcommands

**Description:** Establish the CLI entry point with subcommand routing for daemon, ctl, migrate, version, and init.

**Acceptance Criteria:**

- [ ] `kahi daemon` prints "daemon mode" and exits (placeholder)
- [ ] `kahi ctl` prints "control mode" and exits (placeholder)
- [ ] `kahi migrate` prints "migrate mode" and exits (placeholder)
- [ ] `kahi version` prints version, commit hash, build date, Go version, OS/arch, and FIPS status
- [ ] `kahi init` prints "init mode" and exits (placeholder)
- [ ] `kahi hash-password` prints "hash-password mode" and exits (placeholder)
- [ ] `kahi completion` prints "completion mode" and exits (placeholder)
- [ ] `kahi` with no arguments prints usage showing all subcommands
- [ ] `kahi --help` prints usage with descriptions for each subcommand
- [ ] Unknown subcommands print error and exit with code 1

**Dependencies:** None

---

### INFRA-005: TOML Config Parser Foundation

**Description:** Implement TOML config file loading, parsing, and structural validation.

**Acceptance Criteria:**

- [ ] Parses a valid kahi.toml file into a typed Go struct
- [ ] Returns structured errors with file path, line number, and field name for invalid config
- [ ] Supports all TOML types: string, integer, float, boolean, datetime, array, table, array of tables
- [ ] Validates required fields are present
- [ ] Validates field types match expected types (e.g., port is integer, path is string)
- [ ] Validates value ranges (e.g., port 1-65535, priority 0-999)
- [ ] Unknown fields produce warnings, not errors (forward compatibility)

**Dependencies:** None

---

### INFRA-006: Structured Logging Foundation

**Description:** Establish the logging subsystem using Go's slog package with JSON and text output formats.

**Acceptance Criteria:**

- [ ] Default output is structured JSON to stdout
- [ ] Text format available via config (`log_format = "text"`)
- [ ] Log levels: debug, info, warn, error (mapped to slog levels)
- [ ] Each log entry includes: timestamp (RFC3339), level, message, and structured fields
- [ ] Logger is configurable at startup (level, format, output destination)
- [ ] Child package code can obtain a logger with additional context fields (e.g., `process=foo`)
- [ ] No third-party logging library; stdlib slog only

**Dependencies:** None

---

## Functional Features

### FUNC-001: Process State Machine

**Description:** Implement the process state machine with enforced state transitions.

**Acceptance Criteria:**

- **Given** a process in STOPPED state
  **When** a start command is issued
  **Then** the process transitions to STARTING state

- **Given** a process in STARTING state
  **When** the process has been running for `startsecs` seconds
  **Then** the process transitions to RUNNING state

- **Given** a process in STARTING state
  **When** the process exits before `startsecs` seconds
  **Then** the process transitions to BACKOFF state

- **Given** a process in BACKOFF state with retries remaining
  **When** the backoff delay expires
  **Then** the process transitions to STARTING state

- **Given** a process in BACKOFF state with no retries remaining
  **When** the retry limit is reached
  **Then** the process transitions to FATAL state

- **Given** a process in RUNNING state
  **When** a stop command is issued
  **Then** the process transitions to STOPPING state

- **Given** a process in STOPPING state
  **When** the process exits
  **Then** the process transitions to STOPPED state

- **Given** a process in RUNNING state
  **When** the process exits on its own
  **Then** the process transitions to EXITED state

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Invalid state transition attempted | Transition rejected, error logged | "cannot transition from {current} to {target}" |
| State query for unknown process | Return error | "no such process: {name}" |

**Edge Cases:**

- Transition from STOPPING to EXITED must work even if stop signal was not the cause of exit
- BACKOFF retry counter resets when process successfully reaches RUNNING state
- Clock rollback during STARTING state must not cause premature transition to RUNNING

**Dependencies:** INFRA-001

---

### FUNC-002: Process Start

**Description:** Start a child process with proper isolation, pipe capture, and configurable environment inheritance.

**Acceptance Criteria:**

- **Given** a valid process configuration
  **When** a start command is issued
  **Then** the child process is exec'd directly (no shell), stdout/stderr pipes are created, and the process gets its own process group via setpgid

- **Given** a process with `environment` config (default mode)
  **When** the process starts
  **Then** the child inherits all parent environment variables, plus SUPERVISOR_ENABLED=1, SUPERVISOR_PROCESS_NAME, SUPERVISOR_GROUP_NAME, and process-specific `environment` config overrides

- **Given** a process with `clean_environment = true`
  **When** the process starts
  **Then** only Kahi-set vars (SUPERVISOR_ENABLED, SUPERVISOR_PROCESS_NAME, SUPERVISOR_GROUP_NAME) and explicitly configured `environment` vars are present in the child environment (whitelist-only mode for sensitive environments)

- **Given** a process with `autostart = true` (default)
  **When** Kahi starts
  **Then** the process is started automatically

- **Given** a process with `autostart = false`
  **When** Kahi starts
  **Then** the process remains in STOPPED state until manually started

- **Given** a process with `directory` config
  **When** the process starts
  **Then** the child process working directory is set to the configured path

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Command binary not found | Process goes to FATAL, spawn error recorded | "spawn error: {command}: no such file" |
| Command not executable | Process goes to FATAL | "spawn error: {command}: permission denied" |
| Process already running | Reject start, return error | "process already started: {name}" |
| Directory does not exist | Process goes to FATAL | "spawn error: directory {path} does not exist" |

**Edge Cases:**

- Binary path is resolved via PATH lookup at exec time
- File descriptor inheritance is limited to stdin/stdout/stderr pipes only
- If pipe creation fails, process goes to FATAL with spawn error

**Dependencies:** FUNC-001, INFRA-006

---

### FUNC-003: Process Stop

**Description:** Stop a running process with configurable signal, wait period, and SIGKILL escalation.

**Acceptance Criteria:**

- **Given** a process in RUNNING state with stopsignal=TERM and stopwaitsecs=10
  **When** a stop command is issued
  **Then** SIGTERM is sent, process transitions to STOPPING, and after 10 seconds without exit, SIGKILL is sent

- **Given** a process with stopasgroup=true
  **When** a stop command is issued
  **Then** the stop signal is sent to the process group (-pid) instead of just the process

- **Given** a process with killasgroup=true
  **When** SIGKILL escalation triggers
  **Then** SIGKILL is sent to the process group (-pid)

- **Given** a process in STOPPING state
  **When** the process exits before stopwaitsecs
  **Then** the process transitions to STOPPED without SIGKILL

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Process not running | Return error | "process not running: {name}" |
| Signal send fails (ESRCH) | Log warning, treat as exited | "process {name} already gone" |
| killasgroup=false with stopasgroup=true | Config validation error | "killasgroup cannot be false when stopasgroup is true" |

**Edge Cases:**

- Process that exits during the stop signal send (race condition) must not cause errors
- Multiple stop commands on the same process are idempotent (second is ignored)
- Stopwaitsecs=0 sends SIGKILL immediately after stop signal

**Dependencies:** FUNC-001, FUNC-002

---

### FUNC-004: Autorestart

**Description:** Automatically restart processes based on exit behavior.

**Acceptance Criteria:**

- **Given** a process with autorestart=true
  **When** the process exits with any exit code
  **Then** the process is restarted

- **Given** a process with autorestart=unexpected and exitcodes=[0]
  **When** the process exits with code 1
  **Then** the process is restarted

- **Given** a process with autorestart=unexpected and exitcodes=[0]
  **When** the process exits with code 0
  **Then** the process is NOT restarted (stays in EXITED state)

- **Given** a process with autorestart=false
  **When** the process exits
  **Then** the process is NOT restarted

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Invalid autorestart value in config | Config validation error | "autorestart must be true, false, or unexpected" |

**Edge Cases:**

- Autorestart applies only after process has reached RUNNING state (not during STARTING/BACKOFF)
- Process manually stopped (via stop command) is never autorestarted regardless of setting
- Autorestart during shutdown is suppressed

**Dependencies:** FUNC-001, FUNC-002

---

### FUNC-005: Backoff with Retries

**Description:** Implement exponential backoff for processes that fail to start, with configurable retry limits.

**Acceptance Criteria:**

- **Given** a process with startsecs=1 and startretries=3
  **When** the process exits within 1 second (three times)
  **Then** backoff delays increase (1s, 2s, 4s) and after 3 failures the process goes to FATAL

- **Given** a process in BACKOFF state
  **When** the backoff delay expires
  **Then** the process is restarted (transitions to STARTING)

- **Given** a process that reaches RUNNING state
  **When** it subsequently fails
  **Then** the backoff counter is reset to 0

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| startretries=0 | Process goes directly to FATAL on first failed start | "entered FATAL state, too many start retries" |

**Edge Cases:**

- Backoff delay is capped at a maximum of 60 seconds
- System clock rollback during backoff delay does not cause negative waits

**Dependencies:** FUNC-001, FUNC-002

---

### FUNC-006: Process Reaping

**Description:** Reap child processes on SIGCHLD, collect exit status, and trigger state transitions.

**Acceptance Criteria:**

- **Given** a managed child process exits
  **When** SIGCHLD is received
  **Then** waitpid collects the exit status and the process state machine is updated

- **Given** Kahi is running as PID 1 in a container
  **When** an orphaned process (not directly managed) exits
  **Then** Kahi reaps the zombie via waitpid(-1, WNOHANG)

- **Given** multiple children exit simultaneously
  **When** a single SIGCHLD is delivered
  **Then** all exited children are reaped (loop until ECHILD)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| waitpid returns unexpected PID | Log warning, ignore | "reaped unknown pid {pid}" |
| waitpid returns ECHILD | Stop reaping loop | (internal, no user message) |

**Edge Cases:**

- SIGCHLD can be coalesced by the kernel; must drain all exited children per signal
- Orphaned grandchild processes are reaped when running as PID 1
- Exit status includes both normal exit codes and signal-killed status

**Dependencies:** FUNC-002

---

### FUNC-007: numprocs Instances

**Description:** Launch multiple instances of a process from a single definition using numprocs and template variables.

**Acceptance Criteria:**

- **Given** a program with numprocs=3 and process_name="worker-%(process_num)d"
  **When** the program starts
  **Then** three processes are created: worker-0, worker-1, worker-2

- **Given** numprocs=3 and numprocs_start=10
  **When** the program starts
  **Then** processes are: worker-10, worker-11, worker-12

- **Given** numprocs > 1
  **When** process_name does not contain %(process_num)
  **Then** config validation fails

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| numprocs < 1 | Config validation error | "numprocs must be >= 1" |
| Duplicate process names after template expansion | Config validation error | "duplicate process name: {name}" |

**Edge Cases:**

- numprocs=1 with no %(process_num) in process_name is valid (default behavior)
- Template variables: %(process_num)d, %(program_name)s, %(group_name)s, %(numprocs)d
- Individual instances can be started/stopped independently

**Dependencies:** FUNC-002

---

### FUNC-008: Priority-Based Ordering

**Description:** Start processes in ascending priority order and stop in descending priority order.

**Acceptance Criteria:**

- **Given** processes with priorities 100, 200, 300
  **When** "start all" is issued
  **Then** processes start in order: 100, 200, 300

- **Given** processes with priorities 100, 200, 300
  **When** "stop all" is issued
  **Then** processes stop in order: 300, 200, 100

- **Given** processes with the same priority
  **When** ordered operations execute
  **Then** processes with equal priority start/stop in lexicographic order by name

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Priority outside 0-999 | Config validation error | "priority must be between 0 and 999" |

**Edge Cases:**

- Priority ordering applies to group-level operations as well
- Shutdown uses the same reverse-priority ordering

**Dependencies:** FUNC-002

---

### FUNC-009: Homogeneous Process Groups

**Description:** Each program definition implicitly creates a group with the same name containing all its instances.

**Acceptance Criteria:**

- **Given** a [programs.web] section with numprocs=2
  **When** config is loaded
  **Then** a group named "web" exists containing processes web-0 and web-1

- **Given** a group "web"
  **When** "kahi ctl start web:*" is issued
  **Then** all processes in the web group are started

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Reference to nonexistent group | Return error | "no such group: {name}" |

**Edge Cases:**

- A program with numprocs=1 creates a group with a single process
- Group name collisions between programs are caught at config validation time

**Dependencies:** FUNC-007

---

### FUNC-010: Heterogeneous Process Groups

**Description:** Explicitly defined groups that combine processes from multiple program definitions under one name.

**Acceptance Criteria:**

- **Given** a [groups.services] section with programs=["web", "api"]
  **When** config is loaded
  **Then** a group "services" exists containing all processes from web and api programs

- **Given** a heterogeneous group
  **When** a group operation is issued (start/stop/restart)
  **Then** it applies to all member processes respecting priority ordering

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Group references nonexistent program | Config validation error | "group {name}: unknown program {prog}" |
| Program already in another explicit group | Config validation error | "program {prog} already in group {other}" |

**Edge Cases:**

- When a heterogeneous group exists, the implicit homogeneous groups for its member programs are suppressed
- Empty groups (no programs) are rejected at config validation

**Dependencies:** FUNC-009

---

### FUNC-011: Group Lifecycle Management

**Description:** Add and remove process groups at runtime via API/CLI.

**Acceptance Criteria:**

- **Given** a new program added to config and config reloaded
  **When** "kahi ctl update" is issued
  **Then** the new group is added and its autostart processes begin

- **Given** a group with all processes in STOPPED state
  **When** "kahi ctl remove {group}" is issued
  **Then** the group is removed from active management

- **Given** a group with running processes
  **When** "kahi ctl remove {group}" is issued
  **Then** the command fails with "group has running processes"

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Add already-active group | Return error | "group already active: {name}" |
| Remove group with running processes | Return error | "group {name} has running processes, stop first" |
| Remove nonexistent group | Return error | "no such group: {name}" |

**Edge Cases:**

- Removing a group does not delete its config; it just deactivates it
- Adding a group that was previously removed re-reads config for that group

**Dependencies:** FUNC-009, FUNC-010

---

### FUNC-012: TOML Variable Expansion

**Description:** Expand template variables in config values.

**Acceptance Criteria:**

- **Given** config with `directory = "%(here)s/data"`
  **When** config is loaded from /etc/kahi/kahi.toml
  **Then** directory resolves to "/etc/kahi/data"

- **Given** config with `command = "${APP_BIN}/server"`
  **When** environment variable APP_BIN=/usr/local/bin
  **Then** command resolves to "/usr/local/bin/server"

- **Given** process config with `stdout_logfile = "/var/log/%(program_name)s-%(process_num)d.log"`
  **When** program is "web" with process_num=0
  **Then** path resolves to "/var/log/web-0.log"

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Undefined environment variable | Config error | "undefined environment variable: {name}" |
| Unknown template variable | Config error | "unknown variable: {name}" |

**Edge Cases:**

- Recursive expansion is not supported (a variable cannot reference another variable)
- Literal `%` and `$` can be escaped as `%%` and `$$`
- Expansion happens at config load time, not at process start time

**Dependencies:** INFRA-005

---

### FUNC-013: Config Include System

**Description:** Include additional TOML config files via glob patterns.

**Acceptance Criteria:**

- **Given** a main config with `include = ["conf.d/*.toml"]`
  **When** config is loaded
  **Then** all matching .toml files are parsed and merged, sorted alphabetically

- **Given** an include path with no matches
  **When** config is loaded
  **Then** a warning is logged but loading continues

- **Given** relative include paths
  **When** config is loaded
  **Then** paths are resolved relative to the directory containing the main config file

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Included file has syntax error | Config load fails | "error in {file}: {parse_error}" |
| Circular include | Config load fails | "circular include detected: {file}" |
| Include glob matches no files | Warning logged | "include pattern matched no files: {pattern}" |

**Edge Cases:**

- Included files can define programs but not override [supervisor] settings
- Duplicate program names across included files are caught at validation
- Included files are re-read on config reload (SIGHUP)

**Dependencies:** INFRA-005, FUNC-012

---

### FUNC-014: Config Hot Reload

**Description:** Re-read config on SIGHUP, diff against running state, apply changes.

**Acceptance Criteria:**

- **Given** a running Kahi instance
  **When** SIGHUP is received
  **Then** config is re-read, and a diff is computed showing added/changed/removed programs

- **Given** a new program in reloaded config
  **When** update is applied
  **Then** the new program group is added and autostart processes begin

- **Given** a removed program in reloaded config
  **When** update is applied
  **Then** the program's processes are stopped and the group is removed

- **Given** a changed program config
  **When** update is applied
  **Then** the program's processes are stopped, config is updated, and processes are restarted

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Reloaded config has syntax error | Reload rejected, existing config retained | "config reload failed: {error}" |
| Reloaded config has validation error | Reload rejected | "config reload failed: {validation_error}" |

**Edge Cases:**

- Programs not changed in the reload are untouched (processes keep running)
- Reload during shutdown is ignored
- Concurrent reload requests are serialized (second waits for first to complete)

**Dependencies:** FUNC-012, FUNC-013, FUNC-011

---

### FUNC-015: Config File Search Paths

**Description:** Search predefined paths for config file when not explicitly specified.

**Acceptance Criteria:**

- **Given** no `-c` flag
  **When** Kahi starts
  **Then** it searches in order: `./kahi.toml`, `/etc/kahi/kahi.toml`, `/etc/kahi.toml`

- **Given** a `-c /path/to/config.toml` flag
  **When** Kahi starts
  **Then** only the specified path is used

- **Given** no config found in any search path
  **When** Kahi starts
  **Then** it exits with error code 1

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| No config file found | Exit with error | "no config file found (searched: ./kahi.toml, /etc/kahi/kahi.toml, /etc/kahi.toml)" |
| Config file not readable | Exit with error | "cannot read config: {path}: {error}" |

**Edge Cases:**

- Symlinks to config files are followed
- Config file specified via KAHI_CONFIG environment variable takes precedence over search paths

**Dependencies:** INFRA-005

---

### FUNC-016: Default Config Generation

**Description:** Generate a sample kahi.toml with annotated defaults via `kahi init`.

**Acceptance Criteria:**

- **Given** `kahi init` is run
  **When** stdout is a terminal
  **Then** a complete, commented sample config is printed to stdout

- **Given** `kahi init --output /etc/kahi/kahi.toml`
  **When** the path is writable
  **Then** the sample config is written to the file

- **Given** the generated config
  **When** used to start Kahi
  **Then** it is valid and Kahi starts (though with no programs defined)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Output file already exists | Refuse to overwrite | "file already exists: {path} (use --force to overwrite)" |
| Output path not writable | Exit with error | "cannot write to {path}: {error}" |

**Edge Cases:**

- Generated config includes all sections with their defaults commented out
- TOML comments explain each option

**Dependencies:** INFRA-005

---

### FUNC-017: Daemon Log

**Description:** Kahi daemon's own log output with configurable format, level, and optional file output.

**Acceptance Criteria:**

- **Given** default config
  **When** Kahi starts
  **Then** daemon logs are written to stdout in JSON format

- **Given** `log_format = "text"` in config
  **When** Kahi starts
  **Then** daemon logs use human-readable text format

- **Given** `log_level = "debug"` in config
  **When** Kahi starts
  **Then** debug-level messages are included in output

- **Given** `logfile = "/var/log/kahi.log"` in config
  **When** Kahi starts
  **Then** daemon logs are written to the specified file (in addition to stdout if in foreground mode)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Log file path not writable | Exit with error | "cannot open log file: {path}: {error}" |
| Invalid log level | Config validation error | "invalid log level: {level}" |

**Edge Cases:**

- Log file is opened with append mode
- Log level change on config reload takes effect immediately

**Dependencies:** INFRA-006

---

### FUNC-018: Process Output Capture

**Description:** Capture child process stdout and stderr via pipes and route to configured destinations.

**Acceptance Criteria:**

- **Given** default config (no file logging)
  **When** a child process writes to stdout/stderr
  **Then** output is forwarded to Kahi stdout/stderr as JSON lines: `{"time":"<RFC3339>","process":"<name>","stream":"stdout","log":"<line>"}`

- **Given** `stdout_logfile = "/var/log/web-stdout.log"` in process config
  **When** the child writes to stdout
  **Then** output is written to the specified file

- **Given** `redirect_stderr = true` in process config
  **When** the child writes to stderr
  **Then** stderr output is merged into the stdout stream

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Log file path not writable | Process goes to FATAL | "cannot open log file for {name}: {error}" |
| Pipe read error | Log warning, close pipe | "pipe read error for {name}: {error}" |

**Edge Cases:**

- Output is line-buffered when forwarding to container stdout
- Large output bursts (>64KB/s) must not block the child process (non-blocking pipe reads)
- Pipe is closed on process exit; remaining buffered data is flushed

**Dependencies:** FUNC-002, INFRA-006

---

### FUNC-019: Log Rotation

**Description:** Rotate process log files based on file size.

**Acceptance Criteria:**

- **Given** `stdout_logfile_maxbytes = "50MB"` and `stdout_logfile_backups = 10`
  **When** the log file exceeds 50MB
  **Then** the file is rotated: current becomes .1, .1 becomes .2, etc., up to .10

- **Given** `stdout_logfile_backups = 0`
  **When** the log file exceeds maxbytes
  **Then** the file is truncated (no rotation, no backups)

- **Given** `stdout_logfile_maxbytes = 0`
  **When** the process writes output
  **Then** the log file grows without bound (rotation disabled)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Rotation rename fails | Log error, continue writing to current file | "log rotation failed for {name}: {error}" |

**Edge Cases:**

- Rotation is atomic (rename, not copy+truncate)
- Rotation check happens after every write batch
- Byte size parsing supports: B, KB, MB, GB suffixes

**Dependencies:** FUNC-018

---

### FUNC-020: Syslog Forwarding

**Description:** Optionally forward process stdout/stderr to syslog.

**Acceptance Criteria:**

- **Given** `stdout_syslog = true` in process config
  **When** the child writes to stdout
  **Then** each line is sent to syslog with process name as tag

- **Given** syslog forwarding enabled
  **When** output contains multi-line messages
  **Then** each line is sent as a separate syslog message

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Syslog connection fails | Log error, continue without syslog | "syslog connection failed: {error}" |

**Edge Cases:**

- Syslog forwarding works alongside file logging and console passthrough
- Syslog facility and priority are configurable (default: LOCAL0, INFO)

**Dependencies:** FUNC-018

---

### FUNC-021: ANSI Escape Stripping

**Description:** Optionally remove ANSI escape sequences from process output before logging.

**Acceptance Criteria:**

- **Given** `strip_ansi = true` in process config
  **When** the child outputs `\033[31mERROR\033[0m: failed`
  **Then** the logged output is "ERROR: failed"

- **Given** `strip_ansi = false` (default)
  **When** the child outputs ANSI sequences
  **Then** sequences are preserved in log output

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- Stripping handles all CSI sequences (colors, cursor movement, etc.)
- Partial escape sequences at buffer boundaries are handled correctly

**Dependencies:** FUNC-018

---

### FUNC-022: Log Reopen on SIGUSR2

**Description:** Reopen all log files on SIGUSR2 for external log rotation tools.

**Acceptance Criteria:**

- **Given** Kahi is writing to log files
  **When** SIGUSR2 is received
  **Then** all log file handles are closed and reopened (to pick up rotated files)

- **Given** syslog is configured
  **When** SIGUSR2 is received
  **Then** syslog connections are not affected (only file handles)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| File cannot be reopened | Log error, continue without that file | "cannot reopen log file: {path}: {error}" |

**Edge Cases:**

- Log reopen is safe to call concurrently with log writes
- SIGUSR2 during shutdown is ignored

**Dependencies:** FUNC-018

---

### FUNC-023: Log Cleanup on Startup

**Description:** Remove stale auto-generated child log files from previous runs on startup.

**Acceptance Criteria:**

- **Given** auto-generated log files from a previous run exist in the child log directory
  **When** Kahi starts with `nocleanup = false` (default)
  **Then** stale auto-generated log files are removed

- **Given** `nocleanup = true` in config
  **When** Kahi starts
  **Then** existing log files are preserved

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Cannot delete stale file | Log warning, continue | "cannot remove stale log: {path}: {error}" |

**Edge Cases:**

- Only files matching the auto-generated naming pattern are cleaned; user-specified log files are never touched
- Cleanup happens before any processes are started

**Dependencies:** FUNC-018

---

### FUNC-024: Unix Socket Server

**Description:** Serve the control API over a Unix domain socket.

**Acceptance Criteria:**

- **Given** default config
  **When** Kahi starts
  **Then** a Unix socket is created at the configured path (default: /var/run/kahi.sock or /tmp/kahi.sock)

- **Given** `socket_chmod = "0770"` in config
  **When** the socket is created
  **Then** the socket file has permissions 0770

- **Given** the socket file already exists from a previous crashed run
  **When** Kahi starts
  **Then** the stale socket file is removed and a new one is created

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Cannot create socket (permission denied) | Exit with error | "cannot create socket: {path}: {error}" |
| Socket path too long (>108 chars) | Exit with error | "socket path too long: {path}" |

**Edge Cases:**

- Socket is cleaned up on graceful shutdown
- Socket is not cleaned up on SIGKILL (handled by stale detection on next start)
- chown only works when running as root; otherwise, a warning is logged and chown is skipped

**Dependencies:** INFRA-004

---

### FUNC-025: TCP HTTP Server

**Description:** Optionally serve the API over TCP with HTTP.

**Acceptance Criteria:**

- **Given** `[server.http] enabled = true` and `listen = "127.0.0.1:9876"`
  **When** Kahi starts
  **Then** an HTTP server listens on 127.0.0.1:9876

- **Given** TCP server enabled without auth
  **When** a request is made
  **Then** the request is rejected with 401 Unauthorized

- **Given** TCP server with auth configured
  **When** valid Basic Auth credentials are provided
  **Then** the request is processed

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Port already in use | Exit with error | "cannot bind {address}: {error}" |
| Listen address invalid | Config validation error | "invalid listen address: {address}" |

**Edge Cases:**

- TCP server requires authentication; Unix socket does not by default
- Binding to 0.0.0.0 logs a security warning
- TLS is not included in initial release but the architecture supports adding it

**Dependencies:** FUNC-024

---

### FUNC-026: REST API Endpoints

**Description:** JSON API for process management, status, and log access.

**Acceptance Criteria:**

- **Given** the API server is running
  **When** `GET /api/v1/processes` is requested
  **Then** a JSON array of all process info objects is returned (name, group, state, pid, uptime, exit_status, logfile)

- **Given** a process named "web"
  **When** `POST /api/v1/processes/web/start` is requested
  **Then** the process is started and 200 is returned with process info

- **Given** a process named "web"
  **When** `POST /api/v1/processes/web/stop` is requested
  **Then** the process is stopped and 200 is returned

- **Given** a process named "web"
  **When** `POST /api/v1/processes/web/restart` is requested
  **Then** the process is stopped then started

- **Given** a process named "web"
  **When** `POST /api/v1/processes/web/signal` with body `{"signal": "HUP"}` is requested
  **Then** SIGHUP is sent to the process

- **Given** the API server
  **When** `GET /api/v1/processes/web/log/stdout?offset=-1000&length=1000` is requested
  **Then** the last 1000 bytes of stdout log are returned

- **Given** the API server
  **When** `POST /api/v1/reload` is requested
  **Then** config is re-read and a diff of added/changed/removed is returned

- **Given** the API server
  **When** `POST /api/v1/shutdown` is requested
  **Then** graceful shutdown is initiated

- **Given** the API server
  **When** `GET /api/v1/config` is requested
  **Then** all process config info is returned

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Unknown process name | 404 with JSON error | `{"error": "no such process: web"}` |
| Invalid signal name | 400 with JSON error | `{"error": "invalid signal: FOO"}` |
| Method not allowed | 405 with JSON error | `{"error": "method not allowed"}` |
| Server in shutdown state | 503 with JSON error | `{"error": "server shutting down"}` |

**Edge Cases:**

- All endpoints return `Content-Type: application/json`
- Group operations use `/api/v1/groups/{name}/start` etc.
- The `wait` query parameter controls whether the response waits for the action to complete (default: true)

**Dependencies:** FUNC-024, FUNC-025, FUNC-001

---

### FUNC-027: SSE Streaming

**Description:** Server-Sent Events for real-time log tailing and event streaming.

**Acceptance Criteria:**

- **Given** a running process "web"
  **When** `GET /api/v1/processes/web/log/stdout/stream` is requested with `Accept: text/event-stream`
  **Then** new stdout lines are streamed as SSE events in real time

- **Given** the event system is enabled
  **When** `GET /api/v1/events/stream?types=process_state,process_exit` is requested
  **Then** matching events are streamed as SSE events

- **Given** an SSE client disconnects
  **When** the connection drops
  **Then** the server cleans up the subscription (no resource leak)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Process not found | 404 before stream starts | `{"error": "no such process: web"}` |
| Process not logging to file | 404 | `{"error": "no log file configured for web"}` |

**Edge Cases:**

- SSE includes `X-Accel-Buffering: no` header for nginx reverse proxy compatibility
- Reconnection: SSE `id` field uses byte offset for log streams, allowing resume
- Multiple clients can stream the same log simultaneously
- When no log file is configured, SSE streams from the in-memory ring buffer (FUNC-082)

**Dependencies:** FUNC-026

---

### FUNC-028: HTTP Basic Auth

**Description:** Authenticate API requests using HTTP Basic Authentication with bcrypt-hashed passwords.

**Acceptance Criteria:**

- **Given** `[server.http]` with `username` and a bcrypt-hashed `password_hash` field
  **When** a request with matching Basic Auth is received
  **Then** the request is processed

- **Given** auth configured
  **When** a request without credentials or with wrong credentials is received
  **Then** 401 Unauthorized is returned with `WWW-Authenticate: Basic` header

- **Given** auth configured
  **When** `kahi ctl` connects
  **Then** credentials from the ctl config section or command-line flags are sent

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Plaintext password in config | Config validation error | "password must be bcrypt-hashed (use 'kahi hash-password' to generate)" |

**Edge Cases:**

- Auth is required for TCP server, optional for Unix socket
- `kahi hash-password` subcommand generates a bcrypt hash from stdin
- bcrypt cost factor is not configurable (use the library default, typically 10)

**Dependencies:** FUNC-025

---

### FUNC-029: Health Check Endpoint

**Description:** Liveness probe endpoint that returns daemon health status.

**Acceptance Criteria:**

- **Given** Kahi is running and responsive
  **When** `GET /healthz` is requested
  **Then** 200 OK is returned with `{"status": "ok"}`

- **Given** Kahi is shutting down
  **When** `GET /healthz` is requested
  **Then** 503 is returned with `{"status": "shutting_down"}`

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None specific | N/A | N/A |

**Edge Cases:**

- /healthz does not require authentication (it's a probe endpoint)
- Response includes no process-level information (that's /readyz's job)

**Dependencies:** FUNC-024

---

### FUNC-030: Readiness Check Endpoint

**Description:** Readiness probe endpoint that reports whether managed processes have stabilized.

**Acceptance Criteria:**

- **Given** all autostart processes are in RUNNING or FATAL state
  **When** `GET /readyz` is requested
  **Then** 200 OK is returned with `{"status": "ready", "processes": {...}}`

- **Given** some autostart processes are still in STARTING or BACKOFF state
  **When** `GET /readyz` is requested
  **Then** 503 is returned with `{"status": "not_ready", "pending": ["web", "api"]}`

- **Given** `GET /readyz?process=web,api` is requested
  **When** web is RUNNING and api is RUNNING
  **Then** 200 OK is returned

- **Given** `GET /readyz?process=web,api` is requested
  **When** web is RUNNING and api is STOPPED
  **Then** 503 is returned with `{"status": "not_ready", "failing": ["api"]}`

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Unknown process in ?process= filter | 400 | `{"error": "unknown process: foo"}` |

**Edge Cases:**

- /readyz does not require authentication
- FATAL processes count as "stabilized" (the system has settled, even if unhealthy)
- Processes with autostart=false are excluded from default readiness check

**Dependencies:** FUNC-029

---

### FUNC-031: CLI Process Control Commands

**Description:** `kahi ctl` subcommands for starting, stopping, restarting, and signaling processes.

**Acceptance Criteria:**

- **Given** a stopped process "web"
  **When** `kahi ctl start web` is issued
  **Then** the process is started and status is printed

- **Given** a running process "web"
  **When** `kahi ctl stop web` is issued
  **Then** the process is stopped and status is printed

- **Given** `kahi ctl restart all` is issued
  **When** processes are running
  **Then** all processes are restarted

- **Given** `kahi ctl signal HUP web` is issued
  **When** web is running
  **Then** SIGHUP is sent to the web process

- **Given** `kahi ctl start web:*` is issued
  **When** web is a group
  **Then** all processes in the web group are started

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Daemon not running | Exit code 1 | "cannot connect to kahi daemon (is it running?)" |
| Process name not found | Exit code 1 | "no such process: {name}" |
| Invalid signal | Exit code 1 | "invalid signal: {signal}" |

**Edge Cases:**

- `all` keyword applies to all processes
- `group:*` syntax targets all processes in a group
- Multiple process names can be specified: `kahi ctl start web api worker`

**Dependencies:** FUNC-026, INFRA-004

---

### FUNC-032: CLI Status Display

**Description:** Show process status with state, PID, uptime, and description.

**Acceptance Criteria:**

- **Given** `kahi ctl status` is issued
  **When** processes exist
  **Then** a formatted table is printed with: name, state, PID, uptime, description

- **Given** `kahi ctl status web` is issued
  **When** web exists
  **Then** detailed status for web is printed

- **Given** `kahi ctl status --json` is issued
  **When** processes exist
  **Then** JSON output is printed (machine-readable)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| No processes configured | Print empty table | (empty table with headers) |

**Edge Cases:**

- State is color-coded in terminal output (green=RUNNING, red=FATAL, yellow=STARTING)
- Uptime is formatted as human-readable duration (1d 2h 30m)
- Exit status shown for EXITED processes (e.g., "exit code 1")

**Dependencies:** FUNC-031

---

### FUNC-033: CLI Log Tailing

**Description:** Tail process stdout/stderr logs from the CLI.

**Acceptance Criteria:**

- **Given** `kahi ctl tail web` is issued
  **When** web is writing to stdout
  **Then** the last 1600 bytes of stdout are printed

- **Given** `kahi ctl tail -f web` is issued
  **When** web is writing to stdout
  **Then** new output is streamed in real time (like tail -f)

- **Given** `kahi ctl tail web stderr` is issued
  **When** web is writing to stderr
  **Then** stderr log is tailed

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| No log file configured | Error message | "no log file configured for {name}" |
| Process does not exist | Error message | "no such process: {name}" |

**Edge Cases:**

- `tail -f` uses SSE streaming from the API
- Ctrl+C cleanly terminates the tail stream
- Default byte count (1600) is configurable via `--bytes` flag
- When no log file is configured, tail reads from the ring buffer (FUNC-082)

**Dependencies:** FUNC-027, FUNC-031

---

### FUNC-034: CLI Foreground Attach

**Description:** Attach to a running process's stdin/stdout/stderr.

**Acceptance Criteria:**

- **Given** `kahi ctl fg web` is issued
  **When** web is running
  **Then** CLI stdin is connected to the process stdin, and process stdout/stderr are streamed to the terminal

- **Given** the user presses Ctrl+C in fg mode
  **When** attached to a process
  **Then** the fg session ends but the process continues running

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Process not running | Error | "process not running: {name}" |
| Stdin not configured for process | Error | "process {name} does not accept stdin" |

**Edge Cases:**

- Terminal raw mode is set during fg session and restored on exit
- Multiple fg sessions to the same process are rejected
- Process exit during fg session cleanly terminates the session

**Dependencies:** FUNC-031

---

### FUNC-035: CLI Config Operations

**Description:** Reread config, update running state, add and remove groups via CLI.

**Acceptance Criteria:**

- **Given** `kahi ctl reread` is issued
  **When** config has changed
  **Then** prints added/changed/removed programs (does not apply changes)

- **Given** `kahi ctl update` is issued
  **When** config has changed
  **Then** applies changes: stops removed, restarts changed, adds new

- **Given** `kahi ctl update web` is issued
  **When** web config has changed
  **Then** only web group is updated

- **Given** `kahi ctl add web` is issued
  **When** web exists in config but is not active
  **Then** web group is added to active management

- **Given** `kahi ctl remove web` is issued
  **When** web is stopped
  **Then** web group is removed from active management

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Config file has errors | Error message | "config error: {details}" |

**Edge Cases:**

- `kahi ctl avail` shows all configured programs with active/available status

**Dependencies:** FUNC-031, FUNC-014

---

### FUNC-036: CLI Daemon Operations

**Description:** Shutdown, reload, version, and PID queries via CLI.

**Acceptance Criteria:**

- **Given** `kahi ctl shutdown` is issued
  **When** daemon is running
  **Then** graceful shutdown is initiated and CLI waits for completion

- **Given** `kahi ctl reload` is issued
  **When** daemon is running
  **Then** SIGHUP is sent (equivalent to config reload)

- **Given** `kahi ctl version` is issued
  **When** daemon is running
  **Then** remote daemon version is printed

- **Given** `kahi ctl pid` is issued
  **When** daemon is running
  **Then** daemon PID is printed

- **Given** `kahi ctl pid web` is issued
  **When** web is running
  **Then** web's PID is printed

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Daemon not running | Exit code 1 | "cannot connect to kahi daemon" |

**Edge Cases:**

- `kahi ctl pid all` prints PIDs for all running processes

**Dependencies:** FUNC-031

---

### FUNC-037: CLI Tab Completion

**Description:** Shell completion for bash and zsh with process name and command completion.

**Acceptance Criteria:**

- **Given** `kahi completion bash` is run
  **When** output is sourced in bash
  **Then** tab completion works for subcommands and process names

- **Given** `kahi completion zsh` is run
  **When** output is sourced in zsh
  **Then** tab completion works for subcommands and process names

- **Given** the user types `kahi ctl start <TAB>`
  **When** completion is active
  **Then** available process names are listed

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Daemon not running during completion | Fallback to static command completion | (no process names suggested) |

**Edge Cases:**

- Completion dynamically queries the daemon for current process names
- Group names are also completed (e.g., `web:*`)
- Completion for signals in `kahi ctl signal <TAB>`

**Dependencies:** INFRA-004

---

### FUNC-038: Signal SIGTERM/SIGINT/SIGQUIT Shutdown

**Description:** Graceful shutdown on termination signals.

**Acceptance Criteria:**

- **Given** Kahi receives SIGTERM
  **When** processes are running
  **Then** all process groups are stopped in reverse priority order, then the daemon exits with code 0

- **Given** Kahi receives SIGINT
  **When** processes are running
  **Then** same behavior as SIGTERM

- **Given** a second SIGTERM during shutdown
  **When** processes are still stopping
  **Then** SIGKILL is sent to all remaining processes and daemon exits immediately

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Process doesn't stop within stopwaitsecs | SIGKILL escalation | "force-killing {name}" |

**Edge Cases:**

- Autorestart is suppressed during shutdown
- New process starts are rejected during shutdown
- API continues to serve status requests during shutdown

**Dependencies:** FUNC-003, FUNC-008

---

### FUNC-039: Signal SIGHUP Reload

**Description:** Hot reload config on SIGHUP.

**Acceptance Criteria:**

- **Given** Kahi receives SIGHUP
  **When** config file has changed
  **Then** config is re-read and diff is applied (same as `kahi ctl update`)

- **Given** SIGHUP received
  **When** config file has errors
  **Then** reload is rejected and current config is retained

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Config parse error | Log error, keep running | "SIGHUP reload failed: {error}" |

**Edge Cases:**

- SIGHUP during startup is ignored
- SIGHUP during shutdown is ignored
- Multiple rapid SIGHUPs are coalesced (only one reload executes)

**Dependencies:** FUNC-014

---

### FUNC-040: Signal SIGCHLD Reaping

**Description:** Reap child processes on SIGCHLD.

**Acceptance Criteria:**

- **Given** a child process exits
  **When** SIGCHLD is delivered
  **Then** waitpid is called in a loop until ECHILD, collecting all exit statuses

- **Given** Kahi is PID 1
  **When** an orphaned grandchild exits
  **Then** it is reaped (zombie prevented)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None specific | N/A | N/A |

**Edge Cases:**

- SIGCHLD is handled in the main goroutine via signal.Notify channel (not a signal handler)
- Multiple exits between polls are all collected

**Dependencies:** FUNC-006

---

### FUNC-041: Signal SIGUSR2 Log Reopen

**Description:** Reopen log files on SIGUSR2.

**Acceptance Criteria:**

- **Given** Kahi receives SIGUSR2
  **When** log files are open
  **Then** all log file handles are closed and reopened

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| File reopen fails | Log error, continue | "cannot reopen {path}: {error}" |

**Edge Cases:**

- Only affects file-based logs, not console or syslog

**Dependencies:** FUNC-022

---

### FUNC-042: Signal Queuing

**Description:** Queue signals for deferred processing in the main loop.

**Acceptance Criteria:**

- **Given** a signal is received
  **When** the main loop is busy
  **Then** the signal is queued via Go's signal.Notify channel and processed on the next loop iteration

- **Given** multiple signals queue up
  **When** the main loop processes them
  **Then** all queued signals are handled in order

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Signal channel buffer full | Signal is dropped (Go behavior) | (none -- buffer should be large enough) |

**Edge Cases:**

- Channel buffer size: 16
- Signal processing is always in the main goroutine (no concurrent handlers)

**Dependencies:** INFRA-001

---

### FUNC-043: Child Process Isolation

**Description:** Ensure child processes get their own process group to avoid receiving signals meant for the supervisor.

**Acceptance Criteria:**

- **Given** a child process is started
  **When** setpgid is called
  **Then** the child gets a new process group (pgid = child pid)

- **Given** SIGTERM is sent to Kahi
  **When** children are in their own process groups
  **Then** children do NOT receive SIGTERM (only Kahi does)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| setpgid fails | Log warning, process still starts | "setpgid failed for {name}: {error}" |

**Edge Cases:**

- Process group isolation enables stop-as-group behavior (signal the entire child tree)

**Dependencies:** FUNC-002

---

### FUNC-044: Unprivileged Operation

**Description:** Kahi runs as non-root by default with no root-required functionality in the default configuration.

**Acceptance Criteria:**

- **Given** Kahi is started as a non-root user
  **When** default config is used
  **Then** Kahi starts and operates normally (socket in /tmp, no chown)

- **Given** Kahi is started as a non-root user
  **When** config specifies user-accessible paths
  **Then** all operations succeed without privilege errors

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Path not writable by current user | Exit with error | "cannot write to {path}: permission denied" |

**Edge Cases:**

- Default socket path is /tmp/kahi-{uid}.sock when running as non-root
- Default socket path is /var/run/kahi.sock when running as root

**Dependencies:** FUNC-024

---

### FUNC-045: Optional Privilege Dropping

**Description:** When running as root, optionally drop to a configured user after startup.

**Acceptance Criteria:**

- **Given** Kahi runs as root with `user = "kahi"` in config
  **When** startup completes (socket created, ports bound)
  **Then** process drops to uid/gid of "kahi" user with supplementary groups

- **Given** Kahi runs as non-root with `user` config
  **When** startup begins
  **Then** the user setting is ignored with a warning

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Specified user does not exist | Exit with error | "user not found: {name}" |
| setuid/setgid fails | Exit with error | "privilege drop failed: {error}" |

**Edge Cases:**

- Supplementary groups are computed from /etc/group
- Privilege drop happens after socket creation but before processing any requests

**Dependencies:** FUNC-044

---

### FUNC-046: Per-Process User Switching

**Description:** When running as root, start child processes as specified users.

**Acceptance Criteria:**

- **Given** Kahi runs as root and a process has `user = "www-data"`
  **When** the process starts
  **Then** the child runs as uid/gid of www-data with supplementary groups

- **Given** Kahi runs as non-root and a process has `user` config
  **When** the process starts
  **Then** warning logged, user setting ignored, process runs as current user

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Specified user does not exist | Process goes to FATAL | "user not found: {name}" |
| Running as non-root with per-process user | Warning logged, setting ignored | "user switching unavailable (not running as root), running as current user" |

**Edge Cases:**

- User switching sets uid, gid, and supplementary groups before exec

**Dependencies:** FUNC-045

---

### FUNC-047: Process Umask

**Description:** Configure file creation umask per-process and daemon-level.

**Acceptance Criteria:**

- **Given** `umask = "0027"` in daemon config
  **When** Kahi starts
  **Then** the daemon umask is set to 0027

- **Given** `umask = "0077"` in process config
  **When** the child starts
  **Then** the child umask is set to 0077 before exec

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Invalid umask value | Config validation error | "invalid umask: {value}" |

**Edge Cases:**

- Umask is specified as a 4-digit octal string
- Default daemon umask is 0022

**Dependencies:** FUNC-002

---

### FUNC-048: Root Detection Warning

**Description:** Warn when running as root without explicit user configuration.

**Acceptance Criteria:**

- **Given** Kahi starts as root
  **When** no `user` is configured in [supervisor] section
  **Then** a warning is logged: "running as root without user config; consider setting user for privilege dropping"

- **Given** Kahi starts as non-root
  **When** any config
  **Then** no warning is logged

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- Warning is logged once at startup, not repeated

**Dependencies:** INFRA-006

---

### FUNC-049: Event Bus (Pub/Sub)

**Description:** Internal publish-subscribe system for event distribution. The event bus is core infrastructure, always active with zero overhead when no subscribers exist.

**Acceptance Criteria:**

- **Given** a subscriber registered for PROCESS_STATE_RUNNING events
  **When** a process transitions to RUNNING
  **Then** the subscriber callback receives the event

- **Given** multiple subscribers for the same event type
  **When** the event fires
  **Then** all subscribers are notified

- **Given** a subscriber unsubscribes
  **When** subsequent events fire
  **Then** the unsubscribed callback is not called

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Subscriber callback panics | Panic recovered, logged as error, other subscribers still notified | "event subscriber panic: {error}" |

**Edge Cases:**

- Event notification is synchronous in the main loop (not concurrent)
- Subscriber registration/unregistration is safe from any goroutine

**Dependencies:** INFRA-001

---

### FUNC-050: Process State Events

**Description:** Emit events on every process state transition.

**Acceptance Criteria:**

- **Given** the event system is enabled
  **When** a process transitions to any state
  **Then** a PROCESS_STATE_{STATE} event is emitted with process name, group, from_state, pid (if applicable), and expected flag (for EXITED)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- Events are emitted after the state transition is committed
- All 8 states have corresponding event types

**Dependencies:** FUNC-049, FUNC-001

---

### FUNC-051: Process Log Events

**Description:** Emit events for process output when enabled per-process.

**Acceptance Criteria:**

- **Given** `stdout_events_enabled = true` in process config
  **When** the process writes to stdout
  **Then** PROCESS_LOG_STDOUT events are emitted with the output data

- **Given** `stdout_events_enabled = false` (default)
  **When** the process writes to stdout
  **Then** no log events are emitted

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- Log events contain raw output data (before any ANSI stripping)
- High-throughput processes can generate many events; subscribers must handle backpressure

**Dependencies:** FUNC-049, FUNC-018

---

### FUNC-052: Supervisor State Events

**Description:** Emit events when the supervisor itself changes state.

**Acceptance Criteria:**

- **Given** event system is enabled
  **When** Kahi finishes startup and enters RUNNING state
  **Then** SUPERVISOR_STATE_RUNNING event is emitted

- **Given** event system is enabled
  **When** Kahi begins shutdown
  **Then** SUPERVISOR_STATE_STOPPING event is emitted

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- These events fire once per state change, not periodically

**Dependencies:** FUNC-049

---

### FUNC-053: Process Group Events

**Description:** Emit events when process groups are added or removed.

**Acceptance Criteria:**

- **Given** event system is enabled
  **When** a group is added to active management
  **Then** PROCESS_GROUP_ADDED event is emitted with group name

- **Given** event system is enabled
  **When** a group is removed from active management
  **Then** PROCESS_GROUP_REMOVED event is emitted with group name

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- Group events fire after the operation completes, not before

**Dependencies:** FUNC-049, FUNC-011

---

### FUNC-054: Tick Events

**Description:** Emit periodic tick events at configurable intervals.

**Acceptance Criteria:**

- **Given** event system is enabled
  **When** 5 seconds have elapsed
  **Then** TICK_5 event is emitted

- **Given** event system is enabled
  **When** 60 seconds have elapsed
  **Then** TICK_60 event is emitted

- **Given** event system is enabled
  **When** 3600 seconds have elapsed
  **Then** TICK_3600 event is emitted

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- Tick events include the timestamp of the tick
- Ticks are not emitted during shutdown
- Clock drift does not cause missed ticks (use monotonic time)

**Dependencies:** FUNC-049

---

### FUNC-055: Event Listener Pools

**Description:** Managed processes that subscribe to events via a stdin/stdout protocol.

**Acceptance Criteria:**

- **Given** an event listener configured with `events = ["PROCESS_STATE_EXITED"]` and `buffer_size = 10`
  **When** a process exits
  **Then** the event is queued in the listener's buffer and dispatched when the listener is READY

- **Given** an event listener writes "READY\n" to stdout
  **When** an event is in the buffer
  **Then** the event envelope (headers + payload) is written to the listener's stdin

- **Given** the listener responds with "RESULT 2\nOK"
  **When** processing completes
  **Then** the event is acknowledged and the listener returns to READY state

- **Given** the listener responds with "RESULT 4\nFAIL"
  **When** processing completes
  **Then** the event is re-queued for retry

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Listener crashes | Restart per autorestart policy | "event listener {name} exited unexpectedly" |
| Buffer overflow | Oldest event dropped | "event buffer overflow for {name}, dropping oldest event" |
| Invalid result format | Log error, re-queue event | "invalid result from listener {name}: {data}" |

**Edge Cases:**

- Multiple listener instances in a pool; events dispatched round-robin to READY listeners
- Listener pool with numprocs=3 has 3 listener processes
- Event envelope format: `ver:3.0 server:kahi serial:{n} pool:{pool} poolserial:{n} eventname:{type} len:{n}\n{payload}`

**Dependencies:** FUNC-049, FUNC-050, FUNC-002

---

### FUNC-056: Webhook Notifications

**Description:** Send HTTP POST notifications to configured URLs on specified events.

**Acceptance Criteria:**

- **Given** a webhook configured for PROCESS_STATE_FATAL events
  **When** a process enters FATAL state
  **Then** an HTTP POST is sent to the configured URL with event data as JSON body

- **Given** a webhook with `template = "slack"`
  **When** an event fires
  **Then** the POST body is formatted as a Slack incoming webhook payload

- **Given** a webhook with `template = "generic"`
  **When** an event fires
  **Then** the POST body is a standard JSON object with event type, process name, timestamp, details

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Webhook POST fails (timeout/5xx) | Retry up to configured max_retries (default: 3) with exponential backoff | "webhook delivery failed for {url}: {error}, retrying" |
| All retries exhausted | Log error, move on | "webhook delivery failed permanently for {url}" |
| Webhook URL unreachable | Circuit breaker opens after 5 consecutive failures | "webhook circuit breaker open for {url}" |

**Edge Cases:**

- Webhook delivery is async and never blocks the main supervision loop
- Webhook timeout default is 5 seconds
- Multiple webhooks can subscribe to the same event
- Webhook failures do not affect process management

**Dependencies:** FUNC-049

---

### FUNC-057: Webhook Templates

**Description:** Built-in payload templates for common services.

**Acceptance Criteria:**

- **Given** `template = "slack"`
  **When** a PROCESS_STATE_FATAL event fires
  **Then** payload matches Slack incoming webhook format: `{"text": "Process {name} entered FATAL state on {hostname}"}`

- **Given** `template = "pagerduty"`
  **When** a critical event fires
  **Then** payload matches PagerDuty Events API v2 format

- **Given** `template = "generic"` (default)
  **When** an event fires
  **Then** payload is: `{"event": "{type}", "process": "{name}", "group": "{group}", "timestamp": "{iso8601}", "details": {...}}`

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Unknown template name | Config validation error | "unknown webhook template: {name}" |

**Edge Cases:**

- Templates support variable substitution from event data
- Custom template support (Go template string in config) is a future enhancement, not in v1

**Dependencies:** FUNC-056

---

### FUNC-058: Webhook Environment Variable Expansion

**Description:** Support environment variable references in webhook URLs and headers.

**Acceptance Criteria:**

- **Given** `url = "${SLACK_WEBHOOK_URL}"` in webhook config
  **When** config is loaded
  **Then** the URL is resolved from the SLACK_WEBHOOK_URL environment variable

- **Given** `headers = { "Authorization" = "Bearer ${API_TOKEN}" }`
  **When** config is loaded
  **Then** the header value is resolved from the API_TOKEN environment variable

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Undefined environment variable | Config error | "undefined environment variable in webhook config: {name}" |

**Edge Cases:**

- Expansion happens at config load time, not at delivery time
- Webhook URLs/headers containing secrets are not logged (redacted in debug output)

**Dependencies:** FUNC-056, FUNC-012

---

### FUNC-059: Webhook TLS Requirement

**Description:** Enforce TLS for webhook destinations by default.

**Acceptance Criteria:**

- **Given** a webhook with `url = "http://example.com/hook"` (plain HTTP)
  **When** config is validated
  **Then** config error: "webhook URL must use HTTPS (set allow_insecure = true to override)"

- **Given** `allow_insecure = true` on the webhook
  **When** config is validated
  **Then** plain HTTP URL is accepted with a warning logged

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Invalid URL format | Config validation error | "invalid webhook URL: {url}" |

**Edge Cases:**

- localhost/127.0.0.1 URLs are exempt from the TLS requirement (for local development)
- TLS certificate verification uses the system trust store

**Dependencies:** FUNC-056

---

### FUNC-060: Prometheus /metrics Endpoint

**Description:** Expose Prometheus-compatible metrics endpoint.

**Acceptance Criteria:**

- **Given** `[server.metrics] enabled = true`
  **When** `GET /metrics` is requested
  **Then** Prometheus text exposition format is returned

- **Given** metrics enabled
  **When** processes are running
  **Then** the following metrics are present:
  - `kahi_process_state{name="web",group="web"}` (gauge: STOPPED=0, STARTING=10, RUNNING=20, BACKOFF=30, STOPPING=40, EXITED=100, FATAL=200, UNKNOWN=-1)
  - `kahi_process_start_total{name="web"}` (counter)
  - `kahi_process_exit_total{name="web",expected="true"}` (counter)
  - `kahi_process_uptime_seconds{name="web"}` (gauge)
  - `kahi_supervisor_uptime_seconds` (gauge)
  - `kahi_supervisor_processes{state="running"}` (gauge)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Metrics disabled | /metrics returns 404 | `{"error": "metrics not enabled"}` |

**Edge Cases:**

- Go runtime metrics (goroutines, memory, GC) included via default collectors
- Metrics endpoint does not require authentication
- Metric names follow Prometheus naming conventions (snake_case, _total for counters)

**Dependencies:** FUNC-025

---

### FUNC-061: Process Metrics

**Description:** Per-process Prometheus metrics for state, starts, exits, and uptime.

**Acceptance Criteria:**

- **Given** a process "web" in RUNNING state
  **When** /metrics is scraped
  **Then** `kahi_process_state{name="web",group="web"} 20` is present

- **Given** a process has been started 5 times
  **When** /metrics is scraped
  **Then** `kahi_process_start_total{name="web"} 5` is present

- **Given** a process exited with unexpected code
  **When** /metrics is scraped
  **Then** `kahi_process_exit_total{name="web",expected="false"}` is incremented

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- Metrics survive config reload (counters are not reset)
- Removed processes have their metrics cleaned up after a configurable retention period (default: 5 minutes after removal)

**Dependencies:** FUNC-060

---

### FUNC-062: Supervisor Metrics

**Description:** Daemon-level Prometheus metrics.

**Acceptance Criteria:**

- **Given** Kahi is running
  **When** /metrics is scraped
  **Then** the following are present:
  - `kahi_supervisor_uptime_seconds` (gauge)
  - `kahi_supervisor_processes{state="running"}` (gauge, per state)
  - `kahi_supervisor_config_reload_total` (counter)
  - `kahi_supervisor_config_reload_errors_total` (counter)
  - `kahi_info{version="...",go_version="...",fips="true"}` (constant gauge = 1)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- The `kahi_info` metric is a constant 1 gauge with labels for build metadata

**Dependencies:** FUNC-060

---

### FUNC-063: Foreground Mode (Default)

**Description:** Kahi runs in the foreground by default, suitable for containers and systemd.

**Acceptance Criteria:**

- **Given** `kahi daemon` is run
  **When** no daemonize flag is set
  **Then** Kahi stays in the foreground, logs to stdout, and does not fork

- **Given** Kahi is running as PID 1 in a container
  **When** started normally
  **Then** it handles signals, reaps zombies, and manages processes

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- Ctrl+C in foreground mode triggers graceful shutdown
- Foreground mode is the default; no flag needed to enable it

**Dependencies:** INFRA-004

---

### FUNC-064: Optional Daemonization

**Description:** Optionally daemonize via double-fork for bare-metal deployments.

**Acceptance Criteria:**

- **Given** `kahi daemon --daemonize`
  **When** started
  **Then** Kahi double-forks, redirects stdio to /dev/null, creates a new session, and writes PID file

- **Given** daemonize mode
  **When** PID file path is configured
  **Then** PID file contains the daemon PID

- **Given** daemonize mode on exit
  **When** daemon stops
  **Then** PID file is removed

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| PID file path not writable | Exit with error | "cannot write PID file: {path}: {error}" |
| Another daemon already running (PID file exists with live process) | Exit with error | "another instance running (pid {pid})" |

**Edge Cases:**

- Stale PID file (process not running) is detected and overwritten with a warning
- PID file is not removed on SIGKILL (handled by stale detection)

**Dependencies:** FUNC-063

---

### FUNC-065: PID 1 Zombie Reaping

**Description:** When running as PID 1, reap all orphaned child processes.

**Acceptance Criteria:**

- **Given** Kahi is PID 1
  **When** any process on the system becomes an orphan
  **Then** Kahi reaps it via waitpid(-1, WNOHANG) in the main loop

- **Given** Kahi is NOT PID 1
  **When** orphaned processes appear
  **Then** the OS init system handles them (Kahi only reaps its own children)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- PID 1 detection at startup: `os.Getpid() == 1`
- Reaping loop runs as part of the main supervision tick, not as a separate goroutine

**Dependencies:** FUNC-006

---

### FUNC-066: Graceful Shutdown Orchestration

**Description:** Stop all process groups in reverse priority order during shutdown.

**Acceptance Criteria:**

- **Given** shutdown is initiated
  **When** groups have priorities 100, 200, 300
  **Then** group 300 is stopped first, then 200, then 100

- **Given** shutdown is initiated
  **When** a process does not stop within stopwaitsecs
  **Then** SIGKILL is sent

- **Given** all processes have stopped
  **When** shutdown completes
  **Then** Kahi exits with code 0

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Process stuck (doesn't respond to SIGKILL) | Log error, exit anyway after timeout | "process {name} stuck, giving up" |

**Edge Cases:**

- Shutdown timeout is configurable (default: 30 seconds total)
- If total shutdown exceeds the timeout, remaining processes are killed and daemon exits

**Dependencies:** FUNC-003, FUNC-008

---

### FUNC-067: Working Directory

**Description:** Configure working directory for the daemon and per-process.

**Acceptance Criteria:**

- **Given** `directory = "/opt/app"` in daemon config
  **When** Kahi starts
  **Then** working directory is changed to /opt/app

- **Given** `directory = "/opt/app/web"` in process config
  **When** the process starts
  **Then** child working directory is set to /opt/app/web

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Directory does not exist | Config validation error / FATAL for process | "directory does not exist: {path}" |
| Directory not accessible | Config validation error / FATAL | "cannot access directory: {path}: {error}" |

**Edge Cases:**

- Default working directory is the directory containing the config file
- Per-process directory supports variable expansion

**Dependencies:** FUNC-002, FUNC-012

---

### FUNC-068: Resource Limits

**Description:** Configure minimum file descriptors and process limits; attempt to raise rlimits at startup.

**Acceptance Criteria:**

- **Given** `minfds = 4096` in daemon config
  **When** Kahi starts and current NOFILE < 4096
  **Then** Kahi attempts to raise RLIMIT_NOFILE to 4096

- **Given** `minprocs = 512` in daemon config
  **When** Kahi starts and current NPROC < 512
  **Then** Kahi attempts to raise RLIMIT_NPROC to 512

- **Given** rlimit raise fails (permission denied)
  **When** Kahi starts
  **Then** Kahi exits with error

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Cannot raise rlimit | Exit with error | "cannot set NOFILE to {n}: {error} (current soft={s}, hard={h})" |

**Edge Cases:**

- Default minfds=1024, minprocs=200 (matching supervisord defaults)
- On containers, rlimits may be set by the container runtime; raise only if needed

**Dependencies:** INFRA-001

---

### FUNC-069: Config Migration Tool

**Description:** Convert supervisord.conf to kahi.toml.

**Acceptance Criteria:**

- **Given** `kahi migrate supervisord.conf`
  **When** the file is a valid supervisord config
  **Then** equivalent kahi.toml content is printed to stdout

- **Given** `kahi migrate supervisord.conf --output kahi.toml`
  **When** the file is valid
  **Then** kahi.toml is written to disk

- **Given** supervisord config with unsupported options
  **When** migration runs
  **Then** warnings are printed for each unsupported option, and they are added as TOML comments

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Input file not found | Exit with error | "file not found: {path}" |
| Input file not valid INI | Exit with error | "parse error: {details}" |
| Output file exists | Refuse unless --force | "output file exists: {path} (use --force)" |

**Edge Cases:**

- Handles [include] sections by expanding and inlining
- Maps environment variables from supervisord format to Kahi format
- Maps signal names (e.g., TERM -> SIGTERM)
- Preserves comments as TOML comments where possible

**Dependencies:** INFRA-005

---

### FUNC-070: Migration INI Parser

**Description:** Parse supervisord INI config format including all section types.

**Acceptance Criteria:**

- **Given** a supervisord.conf file
  **When** parsed
  **Then** all section types are recognized: [supervisord], [program:name], [group:name], [eventlistener:name], [fcgi-program:name], [include], [unix_http_server], [inet_http_server]

- **Given** INI file with inline comments (`;` prefixed)
  **When** parsed
  **Then** comments are stripped from values

- **Given** INI file with continuation lines (leading whitespace)
  **When** parsed
  **Then** values are properly joined

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Malformed INI syntax | Error with line number | "parse error at line {n}: {details}" |
| Unknown section type | Warning | "unknown section type: {section}" |

**Edge Cases:**

- supervisord.conf supports `%(ENV_X)s` syntax, which must be mapped to Kahi's `${X}` syntax
- Boolean values in supervisord (true/false, yes/no, on/off, 1/0) map to TOML booleans

**Dependencies:** INFRA-001

---

### FUNC-071: Migration Option Mapping

**Description:** Map all supervisord config options to Kahi TOML equivalents.

**Acceptance Criteria:**

- **Given** supervisord option `startsecs=10`
  **When** migrated
  **Then** TOML contains `startsecs = 10`

- **Given** supervisord option `autorestart=unexpected`
  **When** migrated
  **Then** TOML contains `autorestart = "unexpected"`

- **Given** unsupported option (e.g., XML-RPC specific settings)
  **When** migrated
  **Then** a TOML comment is generated: `# UNSUPPORTED: xmlrpc_timeout = 30`

- **Given** option with renamed equivalent
  **When** migrated
  **Then** the Kahi name is used with a comment noting the original name

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- Byte size values (e.g., `50MB`) are preserved in human-readable format
- Signal names are normalized to uppercase

**Dependencies:** FUNC-070

---

### FUNC-072: Migration Dry Run

**Description:** Preview migration output without writing files.

**Acceptance Criteria:**

- **Given** `kahi migrate supervisord.conf --dry-run`
  **When** run
  **Then** the generated TOML is printed to stdout with warnings, nothing is written

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| None | N/A | N/A |

**Edge Cases:**

- Dry run is the default behavior when no --output is specified (stdout output)

**Dependencies:** FUNC-069

---

### FUNC-073: Migration Validation

**Description:** Validate the generated kahi.toml after migration.

**Acceptance Criteria:**

- **Given** migration produces a TOML file
  **When** validation runs
  **Then** the output is parsed and validated as a valid Kahi config

- **Given** validation finds errors
  **When** migration completes
  **Then** errors are reported to the user with the generated file still available for manual correction

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Generated config fails validation | Warning with details | "generated config has validation errors: {details}" |

**Edge Cases:**

- Validation catches issues like missing required fields that couldn't be inferred from supervisord config

**Dependencies:** FUNC-069, INFRA-005

---

### FUNC-074: FastCGI Program Definition

**Description:** Define FastCGI programs with managed socket pools.

**Acceptance Criteria:**

- **Given** an `[fcgi_programs.php]` section with `socket = "unix:///tmp/php-fpm.sock"`
  **When** config is loaded
  **Then** the FastCGI program is recognized with socket management metadata

- **Given** an `[fcgi_programs.php]` section with `socket = "tcp://127.0.0.1:9000"`
  **When** config is loaded
  **Then** TCP socket binding is configured

- **Given** a FastCGI program
  **When** it starts
  **Then** the socket is passed to the child process via file descriptor inheritance

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Missing socket config | Config validation error | "fcgi_program {name}: socket is required" |
| Invalid socket format | Config validation error | "fcgi_program {name}: invalid socket: {value}" |

**Edge Cases:**

- FastCGI programs support all standard program options (autorestart, numprocs, etc.)
- Socket is created by Kahi, not by the child process

**Dependencies:** FUNC-002

---

### FUNC-075: FastCGI Socket Management

**Description:** Create, bind, and manage FastCGI sockets with reference counting.

**Acceptance Criteria:**

- **Given** a FastCGI program with numprocs=3
  **When** the first process starts
  **Then** the socket is created and bound

- **Given** a FastCGI socket is active
  **When** more processes start
  **Then** the same socket is shared (reference count incremented)

- **Given** all FastCGI processes for a socket have stopped
  **When** the last process exits
  **Then** the socket is closed and cleaned up (reference count reaches 0)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Socket bind fails (address in use) | Process goes to FATAL | "cannot bind socket: {address}: {error}" |
| Socket permission error | Process goes to FATAL | "cannot set socket permissions: {error}" |

**Edge Cases:**

- Unix sockets support configurable owner and mode (socket_owner, socket_mode)
- Socket file is removed when the socket is closed
- Socket is re-created on process restart if it was cleaned up

**Dependencies:** FUNC-074

---

### FUNC-076: CLI Health Check

**Description:** CLI commands for liveness and readiness checks (for exec probes).

**Acceptance Criteria:**

- **Given** `kahi ctl health` is run
  **When** daemon is responsive
  **Then** prints "OK" and exits with code 0

- **Given** `kahi ctl health` is run
  **When** daemon is not responsive
  **Then** prints error and exits with code 1

- **Given** `kahi ctl ready` is run
  **When** all autostart processes have stabilized
  **Then** prints "READY" and exits with code 0

- **Given** `kahi ctl ready --process web,api` is run
  **When** web and api are RUNNING
  **Then** prints "READY" and exits with code 0

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Cannot connect to daemon | Exit code 1 | "cannot connect to kahi daemon" |
| Processes not ready | Exit code 1 | "NOT READY: web (STARTING), api (BACKOFF)" |

**Edge Cases:**

- Exit code 0 = healthy/ready, exit code 1 = not healthy/not ready
- Designed for Kubernetes exec probes

**Dependencies:** FUNC-029, FUNC-030, FUNC-031

---

### FUNC-077: Process stdin Writing

**Description:** Write data to a process's standard input via API/CLI.

**Acceptance Criteria:**

- **Given** a process with stdin configured
  **When** `POST /api/v1/processes/web/stdin` with body `{"data": "quit\n"}` is sent
  **Then** "quit\n" is written to the process's stdin pipe

- **Given** `kahi ctl send web "quit"` is issued
  **When** web has stdin enabled
  **Then** "quit\n" is written to stdin

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Process not running | Return error | "process not running: {name}" |
| Process has no stdin | Return error | "process {name} does not accept stdin" |
| Stdin pipe broken | Return error | "stdin pipe broken for {name}" |

**Edge Cases:**

- stdin is optional per-process (default: no stdin pipe)
- Data must include explicit newlines if needed

**Dependencies:** FUNC-026, FUNC-002

---

### FUNC-078: Web UI Status Page

**Description:** HTML page showing all processes with state, PID, uptime, and action buttons.

**Acceptance Criteria:**

- **Given** Web UI is enabled
  **When** `GET /` is requested in a browser
  **Then** an HTML page lists all processes with: name, group, state (color-coded), PID, uptime, description, and action buttons (Start, Stop, Restart, Clear Log, Tail Stdout, Tail Stderr)

- **Given** the status page
  **When** "Stop All" or "Restart All" button is clicked
  **Then** the corresponding API action is triggered

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| API call fails from UI | Display error notification | "Failed to {action} {name}: {error}" |

**Edge Cases:**

- Page auto-refreshes status every 5 seconds via JavaScript
- Works without JavaScript (static HTML with forms as fallback)
- Responsive layout for mobile viewing

**Dependencies:** FUNC-026

---

### FUNC-079: Web UI Log Viewer

**Description:** Browser-based log tailing for process stdout/stderr.

**Acceptance Criteria:**

- **Given** Web UI is enabled and "Tail Stdout" is clicked for process "web"
  **When** the log viewer page loads
  **Then** real-time stdout output is streamed to the browser via SSE

- **Given** the log viewer
  **When** the user selects stderr
  **Then** stderr is streamed instead

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| No log file configured | Display message | "No log file configured for this process" |
| SSE connection drops | Auto-reconnect | (reconnects silently) |

**Edge Cases:**

- Log viewer shows last 100 lines on initial load, then streams new lines
- ANSI escape codes are rendered as colors in the browser
- Auto-scroll to bottom with manual scroll override

**Dependencies:** FUNC-027, FUNC-078

---

### FUNC-080: Web UI Embedded Assets

**Description:** Serve Web UI as Go-embedded static files for zero-dependency deployment.

**Acceptance Criteria:**

- **Given** Web UI is enabled
  **When** the binary is deployed with no additional files
  **Then** HTML, CSS, and JavaScript are served from embedded assets via `go:embed`

- **Given** `[server.web] static_dir = "/opt/kahi/web"` in config
  **When** the directory exists
  **Then** files from the directory override embedded assets (for customization)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Custom static_dir not found | Fall back to embedded assets with warning | "static_dir not found, using embedded assets" |

**Edge Cases:**

- Embedded assets include a favicon and minimal CSS
- Content-Type headers are set correctly based on file extension
- Caching headers (ETag, Cache-Control) are set for static assets

**Dependencies:** FUNC-078

---

### FUNC-081: Kahi Hash Password Subcommand

**Description:** Generate bcrypt password hashes for use in config files.

**Acceptance Criteria:**

- **Given** `kahi hash-password` is run
  **When** the user types a password at the prompt (not echoed)
  **Then** a bcrypt hash is printed to stdout

- **Given** `echo "mypassword" | kahi hash-password`
  **When** stdin is piped
  **Then** a bcrypt hash is printed to stdout

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --- | --- | --- |
| Empty password | Error | "password cannot be empty" |

**Edge Cases:**

- Password prompt suppresses echo for security
- Output is just the hash string, suitable for copy-paste into config

**Dependencies:** INFRA-004

---

### FUNC-082: Process Output Ring Buffer

**Description:** Maintain a configurable in-memory ring buffer of each process's recent output for log tailing without file logging.

**Acceptance Criteria:**

- **Given** default config (no file logging)
  **When** `kahi ctl tail web` is issued
  **Then** the last data from the ring buffer is returned

- **Given** `stdout_capture_maxbytes = "1MB"` (default)
  **When** a process writes to stdout
  **Then** the buffer holds the last 1MB of stdout per process

- **Given** the ring buffer is full
  **When** new output arrives
  **Then** the oldest data is evicted

**Error Handling:**

None specific.

**Edge Cases:**

- Ring buffer is per-process per-stream (stdout and stderr separate)
- When stdout_capture_maxbytes is set to 0, the ring buffer is disabled and log tailing returns empty results
- Bounds: minimum 0 (disabled), maximum 100MB
- Memory is freed when process config is removed
- Ring buffer feeds SSE streaming when no log file exists

**Dependencies:** FUNC-018

---

## Style Features

### STYLE-001: Web UI Visual Design

**Description:** Clean, functional Web UI with status colors and responsive layout.

**Acceptance Criteria:**

- [ ] Process states use distinct colors: RUNNING=green, FATAL=red, STARTING/BACKOFF=yellow, STOPPED/EXITED=gray, STOPPING=orange
- [ ] Table layout is responsive, collapsing to card layout on screens < 768px
- [ ] Action buttons are clearly labeled and grouped
- [ ] Log viewer uses monospace font with dark background
- [ ] No external CSS/JS dependencies (self-contained)

**Dependencies:** FUNC-078

---

### STYLE-002: CLI Output Formatting

**Description:** Consistent, readable CLI output with color support.

**Acceptance Criteria:**

- [ ] Status table aligns columns with consistent padding
- [ ] State labels are color-coded when stdout is a terminal (RUNNING=green, FATAL=red, etc.)
- [ ] Colors are disabled when stdout is not a terminal (piping to file/grep)
- [ ] `--no-color` flag disables colors explicitly
- [ ] `--json` flag available on status commands for machine-readable output

**Dependencies:** FUNC-032

---

## Testing Features

### TEST-001: Unit Test Infrastructure

**Description:** Establish unit test patterns, mocking strategies, and coverage measurement.

**Acceptance Criteria:**

- [ ] `go test ./...` runs all unit tests
- [ ] Test coverage measured via `go test -coverprofile`
- [ ] Coverage threshold enforced at 85% via Taskfile `coverage` target
- [ ] Process management tests use mock process spawning (no real child processes)
- [ ] Config parsing tests use in-memory TOML strings
- [ ] testify assertions used consistently

**Dependencies:** INFRA-002

---

### TEST-002: Integration Test Infrastructure

**Description:** Integration tests that test component interactions with real I/O.

**Acceptance Criteria:**

- [ ] Integration tests tagged with `//go:build integration`
- [ ] Integration tests start a real Kahi daemon on a random Unix socket
- [ ] Integration tests use `kahi ctl` to interact with the daemon
- [ ] Tests clean up all processes and sockets on completion
- [ ] `task test-integration` runs integration tests specifically

**Dependencies:** TEST-001, FUNC-024

---

### TEST-003: End-to-End Test Infrastructure

**Description:** E2E tests that exercise the full system by building the real `kahi` binary, starting it as a subprocess, and communicating via `ctl.NewUnixClient`. All tests use `//go:build e2e` tags and run via `task test-e2e`.

**Architecture:** True black-box testing. `TestMain` builds the binary to a temp directory. Each test creates an isolated temp directory for socket + config, polls for readiness (socket existence -> health endpoint -> process state), and cleans up via shutdown then kill-if-needed.

**Acceptance Criteria:**

- [ ] E2E tests start a real Kahi daemon, configure real programs (e.g., `sleep`, `echo`)
- [ ] Tests verify process start, stop, restart, autorestart, and shutdown behavior
- [ ] Tests verify API endpoints return correct status
- [ ] Tests verify CLI commands produce expected output
- [ ] E2E tests run in isolated temp directories
- [ ] `task test-e2e` runs end-to-end tests specifically
- [ ] E2E tests have a timeout to prevent hanging on failures

**Dependencies:** TEST-002

#### E2E Test Suite: 11 files, 56 tests, 9 domains

| File | Coverage area | Tests |
| --- | --- | --- |
| `e2e/helpers_test.go` | TestMain (build binary), shared startDaemon/waitForState/getProcessInfo helpers | -- |
| `e2e/daemon_lifecycle_test.go` | Startup, health, version, PID, readiness, shutdown, daemonize flag | 8 |
| `e2e/process_ctl_test.go` | Start/stop/restart, status, signal, error paths (bad name, already running) | 15 |
| `e2e/process_state_test.go` | State transitions: autorestart, backoff->fatal, expected/unexpected exit codes | 10 |
| `e2e/group_ctl_test.go` | Group start/stop/restart, group:* syntax, priority ordering | 6 |
| `e2e/config_reload_test.go` | SIGHUP reload, add/remove/change programs, reread preview | 6 |
| `e2e/logging_test.go` | Tail stdout/stderr, tail -f (SSE), log rotation, ANSI stripping | 6 |
| `e2e/stdin_attach_test.go` | WriteStdin, attach (if feasible in test) | 3 |
| `e2e/env_test.go` | env passthrough, clean_environment, program-overrides-global, variable expansion | 5 |
| `e2e/auth_test.go` | TCP mode with basic auth, rejected without creds | 3 |
| `e2e/regression_test.go` | Ported supervisord regressions: Unicode tail, literal %, numprocs, redirect_stderr | 8 |

#### Shared Helpers (`e2e/helpers_test.go`)

- `startDaemon(t, config string) (*ctl.Client, func())` -- Writes config to temp dir, starts daemon subprocess, polls until healthy, returns client and cleanup function.
- `waitForState(t, client, name, state string, timeout time.Duration)` -- Polls process state until condition met or test fails.
- `getProcessInfo(t, client, name string) ProcessInfo` -- Fetches current process status via client.

#### File-by-File Test Inventory

**`e2e/daemon_lifecycle_test.go`** (8 tests):
- `TestDaemon_Health` -- Verify health endpoint returns ok
- `TestDaemon_Version` -- Verify version map contains expected keys
- `TestDaemon_PID` -- Daemon PID is valid (> 1)
- `TestDaemon_Ready` -- Ready endpoint returns after all autostart processes running
- `TestDaemon_Shutdown` -- Clean shutdown exits with code 0
- `TestDaemon_ShutdownTimeout` -- Processes killed after stopwaitsecs
- `TestDaemon_Daemonize` -- With -d flag, parent exits, daemon continues
- `TestDaemon_PIDFile` -- PID file created and contains correct PID

**`e2e/process_ctl_test.go`** (15 tests):
- `TestProcess_Start` -- Start a stopped process, verify RUNNING
- `TestProcess_Stop` -- Stop a running process, verify STOPPED
- `TestProcess_Restart` -- Restart produces new PID
- `TestProcess_Status` -- Status shows all configured processes
- `TestProcess_StatusWithOptions` -- Filter by name/group
- `TestProcess_Signal` -- Send USR1, process survives
- `TestProcess_SignalTerm` -- Send TERM, process stops
- `TestProcess_StartAlreadyRunning` -- Returns appropriate error
- `TestProcess_StopAlreadyStopped` -- Returns appropriate error
- `TestProcess_StartBadName` -- Returns NOT_FOUND error
- `TestProcess_StartFails` -- Command not found -> FATAL state
- `TestProcess_StopWaitSecs` -- Process killed after timeout
- `TestProcess_StopSignal` -- Custom stopsignal (SIGINT)
- `TestProcess_StartRetries` -- Respects startretries count
- `TestProcess_StartSecs` -- Process must survive startsecs to be RUNNING

**`e2e/process_state_test.go`** (10 tests):
- `TestState_AutorestartTrue` -- Always restarts after exit
- `TestState_AutorestartFalse` -- Never restarts
- `TestState_AutorestartUnexpected` -- Restarts only on unexpected exit codes
- `TestState_BackoffToFatal` -- Exceeds startretries -> FATAL
- `TestState_ExpectedExitCode` -- exitcodes=[0,2], exit 2 -> EXITED (expected)
- `TestState_UnexpectedExitCode` -- exitcodes=[0], exit 1 -> EXITED (unexpected)
- `TestState_KilledDuringBackoff` -- Stop during BACKOFF -> STOPPED
- `TestState_ConcurrentStartStop` -- Rapid start/stop does not deadlock
- `TestState_NumprocsExpansion` -- numprocs=3 creates program_00, program_01, program_02
- `TestState_Priority` -- Higher priority processes start first

**`e2e/group_ctl_test.go`** (6 tests):
- `TestGroup_StartAll` -- Start group:* starts all members
- `TestGroup_StopAll` -- Stop group:* stops all members
- `TestGroup_RestartAll` -- Restart group:* restarts all members
- `TestGroup_StartSingle` -- Start group:name starts one member
- `TestGroup_PriorityOrder` -- Members start in priority order
- `TestGroup_Heterogeneous` -- Group with mixed program configs

**`e2e/config_reload_test.go`** (6 tests):
- `TestReload_AddProgram` -- Add new program section, reload, process appears
- `TestReload_RemoveProgram` -- Remove program section, reload, process stopped and removed
- `TestReload_ChangeProgram` -- Change command, reload, process restarted with new command
- `TestReload_Reread` -- Reread returns diff without applying
- `TestReload_NoChange` -- Reload with no changes returns empty diff
- `TestReload_InvalidConfig` -- Reload with bad config returns error, keeps running config

**`e2e/logging_test.go`** (6 tests):
- `TestLog_TailStdout` -- Tail returns recent stdout lines
- `TestLog_TailStderr` -- Tail returns recent stderr lines
- `TestLog_TailBytes` -- Tail with byte limit
- `TestLog_TailFollow` -- Follow mode receives live output
- `TestLog_Rotation` -- Log file rotated after size threshold
- `TestLog_ANSIStrip` -- ANSI escape codes stripped from captured output

**`e2e/stdin_attach_test.go`** (3 tests):
- `TestStdin_Write` -- Write to process stdin, verify output
- `TestStdin_WriteStopped` -- Write to stopped process returns error
- `TestStdin_Attach` -- Attach bidirectional pipe (if feasible)

**`e2e/env_test.go`** (5 tests):
- `TestEnv_Passthrough` -- Parent env vars visible in child
- `TestEnv_CleanEnvironment` -- Only configured vars visible
- `TestEnv_ProgramOverridesGlobal` -- Program env overrides [supervisord] env
- `TestEnv_ProgramNameExpansion` -- %(program_name)s and %(process_num)d in env values
- `TestEnv_HereExpansion` -- %(here)s expands to config file directory

**`e2e/auth_test.go`** (3 tests):
- `TestAuth_TCPWithCreds` -- Connect with valid credentials succeeds
- `TestAuth_TCPNoCreds` -- Connect without credentials returns 401
- `TestAuth_TCPBadCreds` -- Connect with wrong credentials returns 401

**`e2e/regression_test.go`** (8 tests, ported from supervisord issues):
- `TestRegression_UnicodeTail` -- Tail output with Unicode characters
- `TestRegression_InvalidUTF8` -- Process outputs invalid UTF-8 bytes
- `TestRegression_LiteralPercent` -- Command containing literal % character
- `TestRegression_KahiInit` -- `kahi init` generates valid config file
- `TestRegression_HelpFlag` -- `kahi --help` exits 0
- `TestRegression_PipedTail` -- Tail output piped through process
- `TestRegression_NumprocsNames` -- numprocs naming convention matches spec
- `TestRegression_RedirectStderr` -- redirect_stderr merges stderr into stdout log

#### Supervisord E2E Coverage Mapping

| supervisord E2E test | Kahi disposition |
| --- | --- |
| test_stdout_capturemode | Ported -> TestLog_TailStdout |
| test_stderr_capturemode | Ported -> TestLog_TailStderr |
| test_tail_follow | Ported -> TestLog_TailFollow |
| test_unicode_stdout | Ported -> TestRegression_UnicodeTail |
| test_start_stop | Ported -> TestProcess_Start, TestProcess_Stop |
| test_restart | Ported -> TestProcess_Restart |
| test_signal | Ported -> TestProcess_Signal |
| test_autorestart | Ported -> TestState_AutorestartTrue |
| test_environment | Ported -> TestEnv_Passthrough |
| test_update (add/remove) | Ported -> TestReload_AddProgram, TestReload_RemoveProgram |
| test_shutdown | Ported -> TestDaemon_Shutdown |
| test_eventlistener_* (6 tests) | Deferred -- event listener pool not yet E2E-testable |
| test_xmlrpc_* (5 tests) | Skipped -- Python XML-RPC specific, not applicable to REST API |

---

## Non-Functional Requirements

### Performance

- Daemon startup time (cold start, no processes): < 100ms
- Process start latency (time from API call to child exec): < 50ms
- API response time for status query (100 processes): < 10ms
- Memory overhead per managed process: < 1MB
- SSE streaming latency (log line to client): < 100ms
- Config reload time (100 program definitions): < 500ms

### Security

- No shell execution for child processes (direct exec only)
- Bcrypt for password hashing (cost factor >= 10)
- Unix socket default with restrictive permissions (0700)
- TCP server requires authentication
- FIPS 140 compliant cryptography via GOFIPS140=v1.0.0
- Health/readiness endpoints exempt from auth (standard probe practice)
- Webhook secrets in env vars, not plaintext in config

### Platform Support

- Linux: amd64, arm64 (kernel 4.18+)
- macOS: amd64, arm64 (12.0+)
- Go: 1.26.0+
- Static binary, no shared library dependencies
