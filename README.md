![Kahi](assets/kahi_logo.svg)

# Kahi

A modern process supervisor for POSIX systems, written in Go.

## What is Kahi

Kahi manages long-running processes on Linux and macOS. It handles process
lifecycle (start, stop, restart, autorestart with exponential backoff), health
monitoring, structured logging with rotation, and exposes a REST/JSON API for
programmatic control. It ships as a single static binary with zero runtime
dependencies.

Kahi is modelled after Python's [supervisord](http://supervisord.org/). It
supports the same process lifecycle states (STOPPED, STARTING, RUNNING,
BACKOFF, STOPPING, EXITED, FATAL), similar configuration concepts (programs,
groups, priorities, numprocs), and includes a migration tool that converts
`supervisord.conf` files to Kahi's TOML format. The CLI control interface
(`kahi ctl`) mirrors supervisorctl's command vocabulary.

Key differences from supervisord:

- Single static binary -- no Python runtime required
- TOML configuration instead of INI (typed values, nested tables)
- Structured JSON logging via Go's slog
- REST/JSON API with SSE event streaming (replaces XML-RPC)
- FIPS 140-3 mode in every build, for regulated environments

### Features

- Process lifecycle management with 7-state machine, autorestart, backoff ([how it works](docs/process-lifecycle.md))
- TOML configuration with variable expansion, glob-based includes, hot reload
- REST/JSON API with HTTP Basic Auth, SSE streaming for logs and events
- CLI control client (`kahi ctl`) with all standard operations
- Liveness and readiness probes (`/healthz`, `/readyz`)
- Event system with pub/sub, an SSE event stream and webhook notifications (Slack, PagerDuty and generic JSON templates, retries, circuit breaker); event listener pools are planned for 0.3.0: [#106](https://github.com/kahiteam/kahi/issues/106)
- Structured logging with file rotation, an in-memory output buffer (64 KB per stream, up to 16 MB with `*_capture_maxbytes`), ANSI stripping (syslog forwarding is planned for 0.3.0: [#152](https://github.com/kahiteam/kahi/issues/152))
- Process groups with priority-ordered startup and shutdown: shutdown stops one priority level at a time, in reverse order, within one `shutdown_timeout`
- Per-process uid/gid, umask, resource limits (see [Resource limits](#resource-limits)), environment control
- supervisord.conf migration tool
- Shell completion for commands and flags (bash, zsh, fish, powershell)
- Web UI under `/ui/`: process status, actions and live log tailing (off by default; see [Web UI](#web-ui))
- FIPS 140-3 mode on by default in every binary (`GOFIPS140=v1.0.0`)

## Usage Scenarios

**Container init process.**
Run Kahi as PID 1 in a Docker/OCI container to supervise multiple services.
Stdout/stderr passthrough integrates with container log drivers.

**Traditional server process management.**
Replace supervisord on bare-metal or VM deployments. Use `kahi daemon -d` to
daemonize, communicate via Unix socket with `kahi ctl`. Priority order
sequences long-running services at startup and stops them in reverse at
shutdown.

**Development environment.**
Run a project's background services (API server, worker, file watcher) from a
single `kahi.toml`. Use `kahi ctl tail -f <process>` to stream logs and
`kahi ctl attach <process>` to work with a process interactively: on a terminal
your keystrokes go to its stdin raw (^C included) and Ctrl+] detaches, with the
process left running (supervisorctl calls this `fg`).

**Supervised worker pools.**
Use `numprocs` to spawn multiple instances of the same program (e.g.,
`numprocs = 4` creates `worker_0` through `worker_3`). Autorestart replaces
crashed workers automatically.

**Migrating from supervisord.**
Run `kahi migrate supervisord.conf -o kahi.toml` to convert existing
configurations. The tool maps INI sections and directives to their TOML
equivalents; unsupported directives produce warnings.

**Regulated environments.**
Every Kahi binary is built against Go's FIPS 140-3 cryptographic module
(v1.0.0) with FIPS mode on by default (`fips140=on`), covering password hashing
and TLS. There is no separate FIPS variant. `fips140=on` keeps non-approved
algorithms available; set `GODEBUG=fips140=only` to make them fail instead.

## Installation

### Prebuilt binaries

Download from [GitHub Releases](https://github.com/kahiteam/kahi/releases).
Archives are available for linux (amd64, arm, arm64, ppc64le, riscv64, s390x)
and darwin (amd64, arm64). Every archive is a FIPS 140-3 build.
Every archive, `checksums.txt`, and SBOM is signed (see [Verifying releases](#verifying-releases)).

### go install

```sh
go install github.com/kahiteam/kahi/cmd/kahi@latest
```

### Build from source

Requires Go 1.27.4+ and [Task](https://taskfile.dev).

```sh
git clone https://github.com/kahiteam/kahi.git
cd kahi
task build
```

The binary is written to `./bin/kahi`.

## Building

### Prerequisites

- Go 1.27.4 or later
- [Task](https://taskfile.dev) -- task runner
- golangci-lint and shellcheck (optional, for linting; `task setup` installs both)

Run `task setup` to install golangci-lint and shellcheck at their pinned,
checksum-verified versions. goreleaser, gotestsum, and govulncheck are pinned in
`tools/go.mod`, and actionlint in `tools/actionlint/go.mod`; they run via
`go tool` and need no install step.

### Task targets

| Target                  | Description                                 |
| ----------------------- | ------------------------------------------- |
| `task build`            | Compile binary to `./bin/kahi`              |
| `task test`             | Run unit tests with race detector           |
| `task lint`             | Run golangci-lint and actionlint            |
| `task lint-workflows`   | Lint workflows (actionlint + shellcheck)    |
| `task vet`              | Run `go vet`                                |
| `task fmt`              | Check formatting                            |
| `task coverage`         | Generate coverage report (threshold: 85%)   |
| `task all`              | Run fmt, vet, lint, test, build in sequence |
| `task clean`            | Remove build artifacts                      |
| `task test-integration` | Run integration tests                       |
| `task test-e2e`         | Run end-to-end tests                        |
| `task setup`            | Install golangci-lint and shellcheck        |
| `task tools-parity`     | Fail if a tool on PATH differs from its pin |
| `task snapshot`         | Local release snapshot (pinned goreleaser)  |

### Release builds

[GoReleaser](https://goreleaser.com/) produces cross-compiled archives for
linux (amd64, arm, arm64, ppc64le, riscv64, s390x) and darwin (amd64, arm64).
Every target is built with `GOFIPS140=v1.0.0`.

```sh
# Local snapshot build with the pinned goreleaser (no publish, no signing)
task snapshot
```

Tagged releases are published automatically via GitHub Actions.

## CLI

### Top-level commands

| Command              | Description                                                |
| -------------------- | ---------------------------------------------------------- |
| `kahi daemon`        | Run the supervisor daemon                                  |
| `kahi ctl`           | Control a running daemon                                   |
| `kahi config check`  | Check that `kahi daemon` would start, without starting it  |
| `kahi init`          | Generate a sample `kahi.toml`                              |
| `kahi migrate`       | Convert `supervisord.conf` to `kahi.toml`                  |
| `kahi version`       | Print version, commit, build date, Go version, FIPS status |
| `kahi hash-password` | Generate bcrypt password hash for config                   |
| `kahi completion`    | Generate shell completion scripts                          |

### daemon

```sh
kahi daemon [flags]
```

| Flag              | Description                                                                                                                     |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `-c, --config`    | Config file path (default: search order below)                                                                                  |
| `-p, --pidfile`   | PID file path; a start is refused while the PID in the file is alive, a stale file is replaced                                  |
| `-d, --daemonize` | Run in the background; returns once the daemon runs, or with its startup error. Set `[supervisor] logfile`: stdout is /dev/null |
| `-u, --user`      | Drop privileges to user (name or `uid`, optionally `:group`)                                                                    |

### config check

```sh
kahi config check [-c FILE] [-u USER] [-p PIDFILE]
```

Reports whether `kahi daemon` with the same flags would start, without
starting it: run it in CI, in an image build, in an init container, or before
restarting a running daemon. It loads and validates the config as the daemon
does (search order, includes, warnings, user lookup) and then checks the
startup steps without changing anything: `[supervisor] directory`, the daemon
log, `minfds`/`minprocs` against the hard limits, the socket directory and the
PID file. Program commands, directories and log files that would make a
program go FATAL are printed as warnings. Nothing is created, bound or spawned.

- stdout: `config OK: <path>`; warnings, notes and `error: ...` lines go to
  stderr.
- Exit 0 when the daemon would start (with or without warnings), 1 on errors,
  2 on a usage error.
- A running daemon (live PID file or socket) is noted, not an error.
- Run it as the user that starts the daemon: the checks use that user's
  rights. As non-root, `-u` and `[supervisor] user` are only looked up
  (`note: not root; the privilege drop was not checked`), so a CI job can
  check a production config. A port already in use is not detected.

### ctl

```sh
kahi ctl [command] [flags]
```

**Connection flags** (apply to all subcommands):

| Flag             | Description                               |
| ---------------- | ----------------------------------------- |
| `-s, --socket`   | Unix socket path (overrides config)       |
| `-c, --config`   | Config file path (to resolve socket path) |
| `--addr`         | TCP address (`host:port`)                 |
| `-u, --username` | HTTP Basic Auth username                  |
| `-p, --password` | HTTP Basic Auth password                  |

**Subcommands:**

| Subcommand | Arguments          | Description                                  |
| ---------- | ------------------ | -------------------------------------------- |
| `start`    | `process...`       | Start processes (`group:*`, or `all`)        |
| `stop`     | `process...`       | Stop processes (`group:*`, or `all`)         |
| `restart`  | `process...`       | Restart processes (`group:*`, or `all`)      |
| `status`   | `[process...]`     | Show process status (`--json`, `--no-color`) |
| `signal`   | `signal process`   | Send a signal to a process                   |
| `tail`     | `process [stream]` | Tail process log output (`-f` to follow)     |
| `send`     | `process data`     | Write data to a process stdin                |
| `shutdown` |                    | Initiate daemon shutdown                     |
| `reload`   |                    | Reload daemon configuration                  |
| `reread`   |                    | Preview config changes without applying      |
| `update`   |                    | Reload config and apply all changes          |
| `add`      | `group`            | Add back a removed group (`--reload`)        |
| `remove`   | `group`            | Remove a stopped group (`--stop`)            |
| `attach`   | `process`          | Attach to a process; Ctrl+] detaches         |
| `health`   |                    | Check daemon liveness                        |
| `ready`    |                    | Check daemon readiness (`--process` filter)  |
| `pid`      | `[process]`        | Show daemon or process PID                   |
| `version`  |                    | Show remote daemon version                   |

`start`, `stop` and `restart` try every target and exit 1 if any of them
failed, after printing `{n} of {m} failed`. `all` acts on every process in
priority order through the bulk API, prints one line per process (`ok`,
`skipped` or `error: ...`), and must be the only target; a program named `all`
is reachable as `all:*`. They wait as long as the daemon works on the action
(each process is bounded by its `stopwaitsecs`); `--timeout 30s` limits the
wait, and ctl then exits 1 with `daemon still working after 30s` while the
daemon finishes the action. Queries such as `status` give up after 30s.
Every `kahi` command exits 0 on success, 1 on a runtime failure and 2 on a
usage error (wrong number of arguments, unknown flag or subcommand).

### migrate

```sh
kahi migrate <supervisord.conf> [flags]
```

| Flag           | Description                          |
| -------------- | ------------------------------------ |
| `-o, --output` | Write TOML to file instead of stdout |
| `--force`      | Overwrite existing output file       |
| `--dry-run`    | Preview output without writing files |

### Config search order

When no explicit config path is given, Kahi resolves the config in this order:

1. `-c` flag value
2. `KAHI_CONFIG` environment variable
3. `/etc/kahi/kahi.toml`
4. `/etc/kahi.toml`

`kahi daemon` does **not** search the current directory: a root daemon started
without `-c`/`KAHI_CONFIG` will not load a `./kahi.toml` that an unprivileged
user could plant in the working directory. The `kahi ctl` client, for local
convenience, additionally checks `./kahi.toml` ahead of the system paths.

## Configuration

Kahi uses [TOML](https://toml.io/) for configuration. Generate a sample config
with every supported option:

```sh
kahi init                     # write ./kahi.toml (--force to overwrite)
kahi init -o /etc/kahi.toml   # write to another path
kahi init --stdout            # print to stdout
```

### Sample config

```toml
[supervisor]
# logfile = ""                  # daemon log file path (default: stdout)
# log_level = "info"            # debug, info, warn, error
# log_format = "json"           # json, text
# directory = ""                # daemon working directory, applied at startup (default: inherited)
# identifier = "kahi"           # daemon identifier (not implemented yet)
# minfds = 1024                 # raise the NOFILE soft limit to at least this at startup
# minprocs = 200                # raise the NPROC soft limit to at least this at startup
# umask = ""                    # daemon umask, e.g. "0027" (default: inherited)
# user = ""                    # drop to this user after binding (name or uid, optionally :group);
#                               # needs root; kahi daemon -u takes precedence
# nocleanup = false             # no effect: kahi does not auto-name child logs
# shutdown_timeout = 30         # total seconds for an ordered shutdown, then SIGKILL; levels stop one after
#                               # another, so keep it >= the sum of the largest stopwaitsecs in each level,
#                               # and below your service manager's stop timeout (TimeoutStopSec, docker stop -t)

[server.unix]
# file = "/var/run/kahi.sock"   # Unix socket path. Default: this as root; else
#                               # $XDG_RUNTIME_DIR/kahi.sock or <tmp>/kahi-<uid>/kahi.sock
# chmod = "0700"                # socket permissions; must be owner-only -- group/other access is rejected

[server.http]
# enabled = false               # enable TCP HTTP server (requires username + password below)
# listen = "127.0.0.1:9876"    # TCP listen address; loopback is NOT exempt from auth
# username = ""                 # HTTP Basic Auth username (required when enabled)
# password = ""                 # bcrypt hash from "kahi hash-password" -- plaintext is rejected

# [server.web]                  # Web UI under /ui/ (requires [server.http])
# enabled = false               # serve the Web UI
# static_dir = ""               # optional per-file overlay for /ui/static/

# Process definitions
# [programs.example]
# command = "/usr/bin/example"  # REQUIRED: command to run
# process_name = "example"     # name template (supports %(process_num)d)
# numprocs = 1                 # number of instances
# numprocs_start = 0           # starting instance number
# priority = 999               # start order (0=first, 999=last); shutdown runs in reverse, and programs with
#                               # the same priority stop together, so sharing one shortens shutdown
# autostart = true             # start on daemon startup
# autorestart = "unexpected"   # true, false, unexpected
# startsecs = 1                # seconds before considered started
# startretries = 3             # max retries before FATAL
# exitcodes = [0]              # expected exit codes
# stopsignal = "SIGTERM"       # stop signal (SIGTERM, SIGHUP, SIGINT, SIGQUIT, SIGKILL, SIGUSR1, SIGUSR2)
# stopwaitsecs = 10            # seconds to wait before SIGKILL; a slow stop holds up the next priority level
# stopasgroup = false          # send stop signal to process group
# killasgroup = false          # send SIGKILL to process group
# user = ""                    # name or uid, optionally :group; needs a root daemon
# directory = ""               # working directory
# umask = ""                   # file creation mask
# clean_environment = false    # pass only supervisor + explicit env vars (no inheritance)
# inherit_environment = false  # for a process whose user differs from the supervisor, opt back
#                              # into full env inheritance (differing-user children are clean by default)
# redirect_stderr = false      # merge stderr into stdout
# strip_ansi = false           # remove ANSI escape sequences
# stdout_events_enabled = false # publish PROCESS_LOG_STDOUT events (events API, listeners)
# stderr_events_enabled = false # publish PROCESS_LOG_STDERR events
# stdout_logfile = ""          # stdout log file (default: container stdout)
# stdout_logfile_maxbytes = "50MB"
# stdout_logfile_backups = 10
# stderr_logfile = ""          # stderr log file
# stderr_logfile_maxbytes = "50MB"
# stderr_logfile_backups = 10
# stdout_capture_maxbytes = "64KB" # in-memory buffer for tail and the log viewer, 1KB..16MB
# stderr_capture_maxbytes = "64KB"
# description = ""             # process description
# [programs.example.environment]
# KEY = "value"

# Group definitions
# [groups.services]
# programs = ["web", "api"]    # group member programs
# priority = 999               # group priority

# Event listener pools: supervisord's event listener protocol on stdin/stdout
# [eventlisteners.notify]
# command = "/usr/local/bin/notify-listener"
# events = ["PROCESS_STATE_EXITED", "PROCESS_STATE_FATAL"]
# buffer_size = 10              # queued events per pool
# result_timeout = 60           # seconds a listener may take to answer RESULT
#                               # every [programs.x] key applies, except redirect_stderr
#                               # and the stdout_* output keys (stdout carries the protocol)

# Webhook notifications: an HTTP POST per listed event (not PROCESS_LOG_*)
# [webhooks.slack]
# url = "${SLACK_WEBHOOK_URL}"  # https; plain http only for localhost or with allow_insecure
# events = ["PROCESS_STATE_FATAL"]
# template = "slack"            # generic (default), slack, pagerduty
# timeout = 5                   # seconds per POST
# max_retries = 3               # retries after the first attempt (5xx, 408, 429, network errors)
# allow_insecure = false        # accept plain http:// to a non-loopback host
# routing_key = ""              # PagerDuty integration key; required with template = "pagerduty"
# [webhooks.slack.headers]
# Authorization = "Bearer ${API_TOKEN}"
```

### Variable expansion

Configuration values support these expansion patterns:

| Pattern            | Expands to                              |
| ------------------ | --------------------------------------- |
| `%(here)s`         | Directory containing the config file    |
| `%(program_name)s` | Program section name                    |
| `%(process_num)d`  | Process instance number (from numprocs) |
| `${ENV_VAR}`       | Environment variable value              |

### Config inclusion

Use the top-level `include` array to merge additional config files:

```toml
include = ["/etc/kahi/conf.d/*.toml"]
```

Glob patterns are supported. Circular includes are detected and rejected.

### Resource limits

Per-process resource limits are set as `KAHI_RLIMIT_<RESOURCE>` keys in the
program's environment table, as one value (soft and hard) or `soft:hard`, with
`unlimited` (or `-1`) for no limit. They are applied to the child before it
starts, and the keys stay visible in its environment.

```toml
[programs.web.environment]
KAHI_RLIMIT_NOFILE = "4096:8192"
KAHI_RLIMIT_CORE = "0"
```

Supported resources: `NOFILE`, `NPROC`, `CORE`, `FSIZE`, `AS`, `DATA`,
`STACK`, `RSS`. An unknown resource or an invalid value is a config error.
Daemon-wide `[supervisor] minfds` and `minprocs` set the limits programs
inherit. `minprocs` raises the NPROC soft limit at startup when it is lower.
`minfds`, when set, makes the NOFILE soft limit exactly `minfds` (raising the
hard limit too when it is lower and the daemon may), so every program gets that
many open files; set it higher to give programs more. Without `minfds`, NOFILE
is left alone and programs keep the limit they were started with. A value you
set that cannot be met stops the daemon; the defaults (1024, 200) only warn.

### Web UI

The Web UI is off by default. Enable it together with the TCP listener, which
it requires (browsers cannot reach the Unix socket). Set `username` and
`password` under `[server.http]` as in the sample config above; the password is
a bcrypt hash printed by `kahi hash-password`.

```toml
[server.http]
enabled = true
listen = "127.0.0.1:9876"

[server.web]
enabled = true
# static_dir = "/opt/kahi/web"  # optional: files here replace embedded assets one by one
```

Then open `http://127.0.0.1:9876/ui/` (`/` redirects there) and sign in with the
HTTP credentials. The status page lists every process with Start, Stop,
Restart and Tail controls, plus Start/Stop/Restart All, which call
`POST /api/v1/all/{start,stop,restart}` and act in priority order. The log
viewer at `/ui/log/{name}/{stdout,stderr}` shows the last 100 lines of the
in-memory buffer, then streams new output. Actions need JavaScript; without
it the page is read-only. Changes to `[server.web]` take effect after a daemon
restart, not on reload.

With `strip_ansi = true` the backlog and the live stream are both stripped of
colour codes.

The log stream (`GET /api/v1/processes/{name}/log/{stream}/stream`) gives every
event an id, `{generation}-{offset}`. A client that reconnects with that id in
`Last-Event-ID`, as a browser does on its own, gets what it missed from the
in-memory buffer: an `event: gap` first if part of it is gone, or an
`event: reset` if the process was started anew. A client too slow to keep up
has its stream closed and resumes the same way.

## Security

Kahi is secure by default:

- **Control plane.** The Unix socket is the default transport, created owner-only
  (`0700`) and bound to the service identity; the daemon rejects a group/other
  `chmod` or a `chown`. The TCP API is disabled by default; enabling it requires
  an HTTP Basic Auth username and a bcrypt-hashed password (loopback included) --
  the daemon refuses to start otherwise. Plaintext passwords are rejected.
- **Unix socket is unauthenticated.** Peers on the Unix socket skip
  authentication for the whole API and the Web UI: socket permissions are the
  access control. Do not put a reverse proxy in front of the Unix socket unless
  the proxy authenticates; such a proxy would expose every control action
  without credentials.
- **Web UI.** Every `/ui/` route requires the TCP credentials. Pages carry a
  strict Content-Security-Policy (no inline script or style) and
  `X-Content-Type-Options: nosniff`, and state-changing requests from other
  sites are rejected.
- **Config secrets.** `GET /api/v1/config` returns a redacted view: passwords,
  process environment values, webhook header values and routing keys are masked,
  and a webhook URL shows only its scheme and host. Daemon logs name a webhook
  by its config table name, never by URL.
- **Privilege separation.** Started as root, the daemon can drop to a user after
  binding (`[supervisor] user`, or `kahi daemon -u`, which wins). Before the
  drop it hands the files it created as root (the control socket, a socket
  directory it created, the PID file and its log) to that user, so `kahi ctl`
  works as the user. As non-root, `[supervisor] user` is ignored with a warning
  and `-u` is refused. Per-process `umask` and resource limits are applied at
  spawn. Users are names or numeric
  ids, optionally with `:group`, and get their supplementary groups from
  `/etc/group` (Linux release binaries read `/etc/passwd` and `/etc/group` only:
  no NSS or LDAP). The daemon's own inherited groups are never kept. Per-process
  `user` is applied only when the daemon runs as root; otherwise it is ignored
  with a warning in the daemon log. A user that does not exist is a spawn error:
  the program backs off and goes FATAL, it never runs as root. A process that runs as a
  different user starts from a minimal environment by default, so supervisor
  secrets are not inherited downward (opt in with `inherit_environment`).
- **No shell.** Commands are `exec`'d directly, never via `sh -c`.
- **Config trust.** `kahi daemon` does not load a config from the current
  directory (see [Config search order](#config-search-order)).
- **Signed releases.** Artifacts and images are signed (keyless cosign) and ship
  CycloneDX SBOMs -- see below.

To report a vulnerability, see [SECURITY.md](SECURITY.md).

## Verifying releases

Release artifacts and container images are signed with cosign (keyless OIDC) and ship with CycloneDX SBOMs. Each archive, `checksums.txt`, and per-archive SBOM is paired with a single-file Sigstore bundle (`<artifact>.sigstore.json`) containing the signature, Fulcio certificate, and Rekor inclusion proof. The identity is pinned to the tag ref `refs/tags/<tag>` from `github.com/kahiteam/kahi`. Verification requires cosign v3.0.0 or later. See [docs/verifying-releases.md](docs/verifying-releases.md) for the full verification commands for archives, checksums, and container images.

## License

MIT. See [LICENSE](LICENSE).
