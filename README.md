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
- Prometheus metrics endpoint
- FIPS 140 build variant for regulated environments

### Features

- Process lifecycle management with 7-state machine, autorestart, backoff
- TOML configuration with variable expansion, glob-based includes, hot reload
- REST/JSON API with HTTP Basic Auth, SSE streaming for logs and events
- CLI control client (`kahi ctl`) with all standard operations
- Prometheus metrics, liveness and readiness probes
- Event system with pub/sub, event listeners, webhook notifications
- Structured logging with file rotation, ring buffer, syslog forwarding, ANSI stripping
- Process groups with priority-ordered startup and shutdown
- Per-process uid/gid, umask, resource limits, environment control
- FastCGI socket management (Unix and TCP)
- supervisord.conf migration tool
- Shell completion (bash, zsh, fish, powershell)
- Web UI (optional)
- FIPS 140 enforcing build (`kahi-fips`)

## Usage Scenarios

**Container init process.**
Run Kahi as PID 1 in a Docker/OCI container to supervise multiple services.
Stdout/stderr passthrough integrates with container log drivers.

**Traditional server process management.**
Replace supervisord on bare-metal or VM deployments. Use `kahi daemon -d` to
daemonize, communicate via Unix socket with `kahi ctl`. Priority-ordered
startup sequences long-running services.

**Development environment.**
Run a project's background services (API server, worker, file watcher) from a
single `kahi.toml`. Use `kahi ctl tail -f <process>` to stream logs and
`kahi ctl attach <process>` to connect stdin/stdout for interactive debugging.

**Supervised worker pools.**
Use `numprocs` to spawn multiple instances of the same program (e.g.,
`numprocs = 4` creates `worker_0` through `worker_3`). Autorestart replaces
crashed workers automatically.

**Migrating from supervisord.**
Run `kahi migrate supervisord.conf -o kahi.toml` to convert existing
configurations. The tool maps INI sections and directives to their TOML
equivalents; unsupported directives produce warnings.

**Regulated environments.**
The FIPS 140 build variant (`kahi-fips`) uses Go's FIPS-validated
cryptographic module for password hashing and TLS.

## Installation

### Prebuilt binaries

Download from [GitHub Releases](https://github.com/kahiteam/kahi/releases).
Archives are available for linux (amd64, arm, arm64, ppc64le, riscv64, s390x)
and darwin (amd64, arm64). A FIPS variant (`kahi-fips`) is available for linux.
Every archive, `checksums.txt`, and SBOM is signed (see [Verifying releases](#verifying-releases)).

### go install

```sh
go install github.com/kahiteam/kahi/cmd/kahi@latest
```

### Build from source

Requires Go 1.26.5+ and [Task](https://taskfile.dev).

```sh
git clone https://github.com/kahiteam/kahi.git
cd kahi
task build
```

The binary is written to `./bin/kahi`.

## Building

### Prerequisites

- Go 1.26.5 or later
- [Task](https://taskfile.dev) -- task runner
- golangci-lint (optional, for linting)

Run `task setup` to install golangci-lint and goreleaser.

### Task targets

| Target                  | Description                                 |
| ----------------------- | ------------------------------------------- |
| `task build`            | Compile binary to `./bin/kahi`              |
| `task test`             | Run unit tests with race detector           |
| `task lint`             | Run golangci-lint                           |
| `task vet`              | Run `go vet`                                |
| `task fmt`              | Check formatting                            |
| `task coverage`         | Generate coverage report (threshold: 85%)   |
| `task all`              | Run fmt, vet, lint, test, build in sequence |
| `task clean`            | Remove build artifacts                      |
| `task test-integration` | Run integration tests                       |
| `task test-e2e`         | Run end-to-end tests                        |

### Release builds

[GoReleaser](https://goreleaser.com/) produces cross-compiled archives for
linux (amd64, arm, arm64, ppc64le, riscv64, s390x) and darwin (amd64, arm64).
The FIPS variant is built with `GOFIPS140=v1.0.0` for linux only.

```sh
# Local snapshot build (no publish)
goreleaser release --snapshot --clean
```

Tagged releases are published automatically via GitHub Actions.

## CLI

### Top-level commands

| Command              | Description                                                |
| -------------------- | ---------------------------------------------------------- |
| `kahi daemon`        | Run the supervisor daemon                                  |
| `kahi ctl`           | Control a running daemon                                   |
| `kahi init`          | Generate a sample `kahi.toml`                              |
| `kahi migrate`       | Convert `supervisord.conf` to `kahi.toml`                  |
| `kahi version`       | Print version, commit, build date, Go version, FIPS status |
| `kahi hash-password` | Generate bcrypt password hash for config                   |
| `kahi completion`    | Generate shell completion scripts                          |

### daemon

```sh
kahi daemon [flags]
```

| Flag              | Description                                    |
| ----------------- | ---------------------------------------------- |
| `-c, --config`    | Config file path (default: search order below) |
| `-p, --pidfile`   | PID file path                                  |
| `-d, --daemonize` | Run in background (double-fork)                |
| `-u, --user`      | Drop privileges to user (`uid` or `uid:gid`)   |

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
| `start`    | `process...`       | Start processes (supports `group:*` syntax)  |
| `stop`     | `process...`       | Stop processes                               |
| `restart`  | `process...`       | Restart processes                            |
| `status`   | `[process...]`     | Show process status (`--json`, `--no-color`) |
| `signal`   | `signal process`   | Send a signal to a process                   |
| `tail`     | `process [stream]` | Tail process log output (`-f` to follow)     |
| `send`     | `process data`     | Write data to a process stdin                |
| `shutdown` |                    | Initiate daemon shutdown                     |
| `reload`   |                    | Reload daemon configuration                  |
| `reread`   |                    | Preview config changes without applying      |
| `update`   |                    | Reload config and apply all changes          |
| `add`      | `group`            | Activate a new group from config             |
| `remove`   | `group`            | Stop and remove a group                      |
| `attach`   | `process`          | Attach to a process (stdin/stdout)           |
| `health`   |                    | Check daemon liveness                        |
| `ready`    |                    | Check daemon readiness (`--process` filter)  |
| `pid`      | `[process]`        | Show daemon or process PID                   |
| `version`  |                    | Show remote daemon version                   |

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
with all available options:

```sh
kahi init                     # print to stdout
kahi init -o kahi.toml        # write to file
```

### Sample config

```toml
[supervisor]
# logfile = ""                  # daemon log file path (default: stdout)
# log_level = "info"            # debug, info, warn, error
# log_format = "json"           # json, text
# directory = ""                # daemon working directory
# identifier = "kahi"           # daemon identifier
# minfds = 1024                 # minimum file descriptors
# minprocs = 200                # minimum process count
# nocleanup = false             # preserve stale log files on startup
# shutdown_timeout = 30         # seconds to wait for graceful shutdown

[server.unix]
# file = "/var/run/kahi.sock"   # Unix socket path
# chmod = "0700"                # socket permissions; must be owner-only -- group/other access is rejected

[server.http]
# enabled = false               # enable TCP HTTP server (requires username + password below)
# listen = "127.0.0.1:9876"    # TCP listen address; loopback is NOT exempt from auth
# username = ""                 # HTTP Basic Auth username (required when enabled)
# password = ""                 # bcrypt hash from `kahi hash-password` -- plaintext is rejected

# Process definitions
# [programs.example]
# command = "/usr/bin/example"  # REQUIRED: command to run
# process_name = "example"     # name template (supports %(process_num)d)
# numprocs = 1                 # number of instances
# numprocs_start = 0           # starting instance number
# priority = 999               # start order (0=first, 999=last)
# autostart = true             # start on daemon startup
# autorestart = "unexpected"   # true, false, unexpected
# startsecs = 1                # seconds before considered started
# startretries = 3             # max retries before FATAL
# exitcodes = [0]              # expected exit codes
# stopsignal = "TERM"          # stop signal (TERM, HUP, INT, QUIT, KILL, USR1, USR2)
# stopwaitsecs = 10            # seconds to wait before SIGKILL
# stopasgroup = false          # send stop signal to process group
# killasgroup = false          # send SIGKILL to process group
# user = ""                    # run as user
# directory = ""               # working directory
# umask = ""                   # file creation mask
# clean_environment = false    # pass only supervisor + explicit env vars (no inheritance)
# inherit_environment = false  # for a process whose `user` differs from the supervisor, opt back
#                              # into full env inheritance (differing-user children are clean by default)
# redirect_stderr = false      # merge stderr into stdout
# strip_ansi = false           # remove ANSI escape sequences
# stdout_logfile = ""          # stdout log file (default: container stdout)
# stdout_logfile_maxbytes = "50MB"
# stdout_logfile_backups = 10
# stderr_logfile = ""          # stderr log file
# stderr_logfile_maxbytes = "50MB"
# stderr_logfile_backups = 10
# description = ""             # process description
# [programs.example.environment]
# KEY = "value"

# Group definitions
# [groups.services]
# programs = ["web", "api"]    # group member programs
# priority = 999               # group priority

# Webhook definitions
# [webhooks.slack]
# url = "https://hooks.slack.com/..."
# events = ["process_state"]
# timeout = 5
# retries = 3
# [webhooks.slack.headers]
# Authorization = "Bearer token"
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

## Security

Kahi is secure by default:

- **Control plane.** The Unix socket is the default transport, created owner-only
  (`0700`) and bound to the service identity; the daemon rejects a group/other
  `chmod` or a `chown`. The TCP API is disabled by default; enabling it requires
  an HTTP Basic Auth username and a bcrypt-hashed password (loopback included) --
  the daemon refuses to start otherwise. Plaintext passwords are rejected.
- **Config secrets.** `GET /api/v1/config` returns a redacted view: passwords,
  process environment values, and webhook auth headers are masked.
- **Privilege separation.** The daemon can drop privileges (`-u`), and per-process
  `user`/`umask`/resource limits are enforced at spawn. A process that runs as a
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
