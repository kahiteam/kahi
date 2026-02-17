# Technical Plan: Kahi

## Overview

**Project:** Kahi -- Lightweight process supervisor for modern infrastructure
**Spec Version:** 1.0.0
**Plan Version:** 1.0.0
**Last Updated:** 2026-02-16
**Status:** Draft

---

## Project Structure

```text
kahi/
├── cmd/kahi/                  # Single binary entry point
│   └── main.go
├── internal/                  # Private packages
│   ├── config/                # TOML parsing, validation, variable expansion
│   ├── process/               # State machine, start/stop, reaping, groups
│   ├── supervisor/            # Main run loop, signal handling, shutdown
│   ├── api/                   # REST handlers, SSE streaming, auth middleware
│   ├── events/                # Pub/sub bus, event types, listener pools
│   ├── logging/               # Log handlers, rotation, ring buffer, syslog
│   ├── ctl/                   # CLI control client logic
│   ├── migrate/               # supervisord.conf parser and converter
│   ├── fcgi/                  # FastCGI socket management
│   ├── metrics/               # Prometheus collectors
│   ├── web/                   # Web UI templates, embedded assets
│   │   └── static/            # HTML, CSS, JS (go:embed)
│   ├── testutil/              # Shared test helpers
│   └── version/               # Build metadata (version, commit, FIPS)
├── Taskfile.yml               # Dev workflow (build, test, lint, coverage)
├── .goreleaser.yml            # Release config (cross-compile, FIPS, GitHub)
├── .golangci.yml              # Linter config
├── go.mod                     # Module definition (Go 1.26.0)
├── go.sum                     # Dependency checksums
├── kahi.example.toml          # Annotated sample config
├── Dockerfile                 # Multi-stage build (scratch base)
├── init.sh                    # One-time bootstrap (installs Task CLI)
├── feature_list.json          # Spec-driven feature tracking
├── .specify/                  # Specification artifacts
├── .github/                   # GitHub Actions workflows
│   └── workflows/
│       ├── ci.yml             # Unit tests + lint (matrix)
│       ├── integration.yml    # Integration + E2E tests
│       ├── release.yml        # GoReleaser + Docker build
│       └── security.yml       # CodeQL + govulncheck
└── CLAUDE.md                  # Claude Code project instructions
```

---

## Tech Stack

### Backend

| Component | Choice | Version | Rationale |
| --- | --- | --- | --- |
| Language | Go | 1.26.0+ | Constitution requirement. Static binary, cross-compilation, goroutines for process supervision. |
| HTTP Server | net/http (stdlib) | 1.26.0 | Go 1.22+ enhanced routing (method+path patterns). No framework needed. |
| Structured Logging | log/slog (stdlib) | 1.26.0 | Native structured logging with JSON/text handlers. Zero dependency. |
| CLI Framework | spf13/cobra | latest | Subcommand routing, help generation, bash/zsh completion. |
| TOML Parser | BurntSushi/toml | latest | De facto Go TOML library. Full TOML v1.0 support. |
| Metrics | prometheus/client_golang | latest | Prometheus exposition format. Industry standard. |
| Password Hashing | golang.org/x/crypto/bcrypt | latest | bcrypt not in stdlib. FIPS-compatible via GOFIPS140. |
| Terminal I/O | golang.org/x/term | latest | Raw mode for `kahi ctl fg`. |
| Process Exec | os/exec + syscall (stdlib) | 1.26.0 | Direct exec with SysProcAttr for setpgid, setuid, umask. |
| Signal Handling | os/signal (stdlib) | 1.26.0 | signal.Notify for queued signal processing. |

### Frontend (Web UI)

| Component | Choice | Version | Rationale |
| --- | --- | --- | --- |
| Templates | html/template (stdlib) | 1.26.0 | Server-rendered HTML. No build step. |
| Interactivity | Vanilla JavaScript | ES2020 | SSE streaming, auto-refresh. No framework. |
| Styling | Custom CSS | N/A | ~200 lines. Responsive. No framework. |
| Embedding | go:embed (stdlib) | 1.26.0 | Zero-dependency static file serving. ~50KB total. |

### Data Storage

| Component | Choice | Version | Rationale |
| --- | --- | --- | --- |
| Configuration | TOML files | v1.0 | Constitution requirement. No database. |
| Process State | In-memory | N/A | State machine lives in process structs. No persistence needed. |
| Log Buffer | In-memory ring buffer | N/A | Configurable per-process (default 1MB). For tailing without file logging. |
| Process Logs | File or stdout | N/A | Container-first: JSON lines to stdout. Optional: file with rotation. |

### API Design

- **Style:** REST/JSON with SSE for streaming
- **Base Path:** `/api/v1`
- **Authentication:** HTTP Basic Auth (bcrypt passwords). Required on TCP, optional on Unix socket.
- **Error Format:** `{"error": "message", "code": "NOT_FOUND"}`
- **Error Codes:** BAD_REQUEST, NOT_FOUND, CONFLICT, UNAUTHORIZED, SERVER_ERROR, SHUTTING_DOWN
- **Streaming:** Server-Sent Events (SSE) for log tailing and event streams
- **Probe Endpoints:** `/healthz`, `/readyz`, `/metrics` -- no auth, outside `/api/v1` prefix

**Endpoint Map:**

```text
GET    /api/v1/processes                         # List all process info
GET    /api/v1/processes/{name}                  # Get single process info
POST   /api/v1/processes/{name}/start            # Start process
POST   /api/v1/processes/{name}/stop             # Stop process
POST   /api/v1/processes/{name}/restart          # Restart process
POST   /api/v1/processes/{name}/signal           # Send signal (body: {"signal":"HUP"})
POST   /api/v1/processes/{name}/stdin            # Write to stdin (body: {"data":"..."})
GET    /api/v1/processes/{name}/log/{stream}     # Read log (stream: stdout|stderr)
GET    /api/v1/processes/{name}/log/{stream}/stream  # SSE log tail

GET    /api/v1/groups                            # List groups
POST   /api/v1/groups/{name}/start               # Start all in group
POST   /api/v1/groups/{name}/stop                # Stop all in group
POST   /api/v1/groups/{name}/restart             # Restart all in group

GET    /api/v1/config                            # Get all config
POST   /api/v1/config/reload                     # Reload config (reread)
POST   /api/v1/config/update                     # Apply config changes

POST   /api/v1/shutdown                          # Graceful shutdown
GET    /api/v1/version                           # Daemon version info

GET    /api/v1/events/stream                     # SSE event stream (?types= filter)

GET    /healthz                                  # Liveness probe (no auth)
GET    /readyz                                   # Readiness probe (no auth, ?process= filter)
GET    /metrics                                  # Prometheus metrics (no auth)
```

---

## Testing Strategy

| Type | Framework | Coverage Target | Command | Build Tag |
| --- | --- | --- | --- | --- |
| Unit | go test + testify | 85% (combined) | `task test` | (none) |
| Integration | go test + testify | Included in 85% | `task test-integration` | `//go:build integration` |
| E2E | go test + testify | N/A | `task test-e2e` | `//go:build e2e` |

### Coverage

- **Minimum threshold:** 85% (from constitution)
- **Coverage tool:** `go test -coverprofile=coverage.out` + `go tool cover -func`
- **Excluded paths:** `cmd/kahi/main.go`, `internal/web/static/*`, `internal/version/`

### Mocking Approach

Interfaces at OS boundaries enable testable code without real processes:

| Boundary | Interface | Real Implementation | Mock Implementation |
| --- | --- | --- | --- |
| Process exec | `ProcessSpawner` | `os/exec.Command` + `syscall.SysProcAttr` | In-memory process simulation |
| Filesystem | `FileSystem` | `os.OpenFile`, `os.Rename` | In-memory filesystem |
| Time | `Clock` | `time.Now()`, `time.After()` | Controllable clock (advance manually) |
| Syscall | `SyscallWrapper` | `syscall.Setpgid`, `syscall.Setuid` | No-op or recorded calls |

### Test Helpers (internal/testutil)

- `TempDir()` -- create isolated temp directory, auto-cleanup
- `FreeSocket()` -- generate unique Unix socket path in temp dir
- `WaitFor(condition func() bool, timeout time.Duration)` -- poll-based async assertion
- `StartTestDaemon(config string)` -- launch Kahi daemon with in-memory config for integration tests
- `MustParseConfig(toml string)` -- parse TOML string into config struct, fatal on error

---

## Deployment Architecture

| Component | Platform | Rationale |
| --- | --- | --- |
| Binary distribution | GitHub Releases (via GoReleaser) | Tarballs for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64. FIPS variants. |
| Container image | Multi-arch OCI (ghcr.io) | `FROM scratch`, USER 65534, ~10-15MB. Built via Docker buildx for linux/amd64 + linux/arm64. |
| CI/CD | GitHub Actions | Standard for Go open source. Matrix builds for all target platforms. |

### Dockerfile

```dockerfile
# Build stage
FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOFIPS140=v1.0.0 go build -ldflags="-s -w" -o /kahi ./cmd/kahi

# Runtime stage
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /kahi /kahi
USER 65534:65534
ENTRYPOINT ["/kahi", "daemon"]
```

Notes:
- CA certificates copied for webhook TLS verification
- Binary stripped (`-s -w` ldflags) for smaller size
- scratch base: no shell, no package manager, no attack surface
- USER 65534 (nobody): unprivileged by default

---

## Development Environment

### init.sh (One-Time Bootstrap)

Installs the Task CLI only. Everything else is managed by Taskfile targets.

**System requirements:** Go 1.26+, git, bash
**No database or Docker required for development.**

### Taskfile Targets

| Target | Description |
| --- | --- |
| `task setup` | Install golangci-lint, goreleaser, download Go modules |
| `task build` | Compile binary to `./bin/kahi` |
| `task test` | Run unit tests with race detector |
| `task test-integration` | Run integration tests (tag: integration) |
| `task test-e2e` | Run E2E tests (tag: e2e) |
| `task lint` | Run golangci-lint |
| `task fmt` | Run gofmt, report changes |
| `task vet` | Run go vet |
| `task coverage` | Generate coverage report, fail if < 85% |
| `task all` | Run fmt, vet, lint, test, build in sequence |
| `task clean` | Remove build artifacts |

### Verification

After `init.sh && task setup && task all`:
- Binary exists at `./bin/kahi`
- `./bin/kahi version` prints version info
- All tests pass
- Linter reports zero findings
- Coverage >= 85%

---

## Architectural Decisions

### ADR-001: Single Binary with Subcommand Routing

**Date:** 2026-02-16
**Status:** Accepted

**Context:** Process supervisors need both a daemon and a control client. Supervisord uses two separate binaries (supervisord, supervisorctl). Distributing and versioning two binaries adds operational complexity.

**Decision:** Ship a single `kahi` binary that routes via subcommands: `kahi daemon`, `kahi ctl`, `kahi migrate`, `kahi init`, `kahi version`, `kahi hash-password`, `kahi completion`.

**Alternatives Considered:**

1. **Two binaries (kahid + kahictl):** Simpler per-binary, but doubles distribution artifacts and risks version mismatch.
2. **Symlink-based detection:** Like BusyBox. Binary detects mode from argv[0]. Fragile and confusing.

**Consequences:**

- Single artifact to distribute, install, and version
- Cobra handles subcommand routing, help, and completion
- Binary size is slightly larger (~1MB for ctl code in daemon builds)

---

### ADR-002: TOML Configuration Format

**Date:** 2026-02-16
**Status:** Accepted

**Context:** supervisord uses INI format. Modern projects use YAML, JSON, or TOML. The configuration format must be unambiguous, well-specified, and support nested structures.

**Decision:** TOML as the sole configuration format. Named table syntax: `[programs.web]`, `[groups.services]`, `[fcgi_programs.php]`, `[webhooks.slack]`.

**Alternatives Considered:**

1. **YAML:** Widely used but has parsing pitfalls (Norway problem, implicit typing). Not well-specified.
2. **JSON:** No comments. Verbose. Not human-friendly for config files.
3. **INI + TOML:** Dual support adds parser complexity and testing burden.

**Consequences:**

- One parser to maintain and test
- TOML is strict about types (no implicit boolean from "yes"/"no")
- Migration tool (`kahi migrate`) handles the INI-to-TOML conversion for existing supervisord users

---

### ADR-003: Container-First Design

**Date:** 2026-02-16
**Status:** Accepted

**Context:** supervisord was designed for bare-metal servers. Modern workloads primarily run in containers where different defaults are appropriate.

**Decision:** Container-first defaults: foreground mode, JSON structured logging to stdout, unprivileged operation, PID 1 zombie reaping, in-memory ring buffer for log tailing.

**Alternatives Considered:**

1. **Bare-metal-first:** Match supervisord defaults (daemonize, file logging). Would feel dated for the primary use case.
2. **Auto-detect:** Detect container environment and switch defaults. Fragile and surprising.

**Consequences:**

- Default experience is optimized for Docker/Kubernetes
- Bare-metal features (daemonize, file logging, privilege drop) are opt-in via config flags
- PID 1 zombie reaping adds a few lines of code to the main loop

---

### ADR-004: REST/JSON API with SSE Streaming

**Date:** 2026-02-16
**Status:** Accepted

**Context:** supervisord uses XML-RPC. Modern tools prefer REST/JSON. Real-time log tailing and event streaming require a push mechanism.

**Decision:** REST/JSON API at `/api/v1/*` with Server-Sent Events (SSE) for log tailing and event streaming. No gRPC in initial release.

**Alternatives Considered:**

1. **gRPC:** Typed contracts, native streaming. But adds protobuf toolchain, ~5MB to binary, and most users interact via curl.
2. **GraphQL:** Flexible queries, but overkill for a flat API with well-defined operations.
3. **WebSocket:** Bidirectional. But SSE is simpler for server-push use cases and works with curl.

**Consequences:**

- curl-friendly API for debugging and scripting
- SSE handles log tailing and event streaming without WebSocket complexity
- CLI uses the same REST API over Unix socket (like Docker CLI)
- gRPC can be added in a future release as an optional feature

---

### ADR-005: Event Bus Always Active

**Date:** 2026-02-16
**Status:** Accepted

**Context:** The event system powers webhooks, event listeners, SSE streaming, and metrics. Making it toggleable adds conditional logic throughout the codebase.

**Decision:** The event bus is core infrastructure, always active. Zero overhead when no subscribers exist (empty subscriber list, no allocation per event).

**Alternatives Considered:**

1. **Config toggle:** `[events] enabled = true/false`. Adds conditional checks at every publish site.

**Consequences:**

- Simpler code: always publish events, subscribers come and go
- Webhooks and event listeners just subscribe when configured
- No "events disabled" error path to test

---

### ADR-006: Environment Variable Inheritance Modes

**Date:** 2026-02-16
**Status:** Accepted

**Context:** supervisord passes a nearly empty environment to child processes. Container environments inject critical vars (PATH, HOME, HOSTNAME). The sanitization approach must work for both contexts.

**Decision:** Two modes controlled by `clean_environment` (boolean, default false):
- `false` (default): Inherit all parent environment vars. Kahi vars and `environment` config overrides are added on top.
- `true`: Whitelist-only. Only Kahi vars (SUPERVISOR_ENABLED, etc.) and explicitly configured `environment` vars are passed.

**Alternatives Considered:**

1. **Always inherit:** Simple but no way to sanitize for sensitive environments.
2. **Always whitelist:** supervisord-compatible but breaks most container programs out of the box.

**Consequences:**

- Container-friendly default (inherit everything)
- Security-conscious environments opt in to `clean_environment = true`
- Same config file works in both modes (non-root ignores root-only settings with warnings)

---

### ADR-007: In-Memory Ring Buffer for Log Tailing

**Date:** 2026-02-16
**Status:** Accepted

**Context:** Container-first means file logging is disabled by default. Log tailing (`kahi ctl tail -f`, SSE streaming) needs a data source even without files.

**Decision:** Each process maintains an in-memory ring buffer (default 1MB) for stdout and stderr. Tailing reads from the buffer. File logging, when enabled, is a separate destination.

**Alternatives Considered:**

1. **Require file logging for tail:** Forces users to enable file logging in containers, defeating the purpose.
2. **Kernel ring buffer (dmesg-style):** Overcomplicated for a process supervisor.

**Consequences:**

- ~1MB memory per process per stream (stdout + stderr = ~2MB per process)
- 100 processes = ~200MB overhead for ring buffers (acceptable)
- Configurable via `stdout_capture_maxbytes`
- Ring buffer also feeds the Web UI log viewer

---

### ADR-008: CodeQL BarrierGuard Pattern for Allocation Bounds

**Date:** 2026-02-17
**Status:** Accepted (revised after PR #7 and PR #8 failed to close alert)

**Context:** CodeQL's `go/uncontrolled-allocation-size` query uses interprocedural taint tracking from HTTP parameters to `make()` calls. Two prior approaches failed:

- **PR #7 (constant guards):** Clamp pattern `if n > maxReadAlloc { n = maxReadAlloc }` merges both branches at a phi node. CodeQL's `BarrierGuard` does not propagate sanitization through phi-node merges.
- **PR #8 (`min()` in `make()`):** Go's `min()` builtin has a value-flow model (`builtin.model.yml`) that propagates taint from arguments to return value. It is not a `RelationalComparisonNode` and cannot match the `allocationSizeCheck` barrier predicate.

CodeQL's `BarrierGuard` recognizes only relational comparisons (`<`, `<=`, `>`, `>=`) where the **unsafe branch terminates** (early return or panic). After such a guard, the only surviving branch has the tainted value provably within bounds.

**Decision:** Use early-return guards at two layers:

1. **Ring buffer (`Read()`):** Replace the clamp with `if n <= 0 || n > maxReadAlloc { return nil }`. This terminates the unsafe branch. The `make([]byte, n)` call uses the sanitized value on the surviving branch.
2. **API handler (`handleReadLog()`):** Replace `min(v, maxReadLength)` with an explicit HTTP 400 response for out-of-bounds length. This validates user input at the system boundary.

**Alternatives Considered:**

1. **Clamp pattern (`if n > X { n = X }`):** Failed -- phi-node merge defeats `BarrierGuard`.
2. **`min()` in `make()` expression:** Failed -- `min()` propagates taint, not a barrier.
3. **Derive allocation from non-tainted `rb.size`:** Uncertain -- `if n < allocSize { allocSize = n }` reintroduces tainted value via clamp merge.
4. **CodeQL query suppression (`// lgtm`):** Rejected -- hides the alert without fixing the pattern.

**Consequences:**

- `Read()` returns nil instead of clamping for `n > 64KB`. This is unreachable from the API path (handler already bounds length) but guards against direct callers.
- API handler returns HTTP 400 for `length > 64KB` instead of silently clamping. Explicit validation at the boundary.
- Pattern applies to any future `make()` call where the size flows from user input: use early-return guards, not clamps or `min()`.
- Go's `min()`/`max()` builtins are NOT CodeQL barriers as of CodeQL 2.24.1.
