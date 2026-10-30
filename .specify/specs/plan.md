# Technical Plan: Kahi

## Overview

**Project:** Kahi -- Lightweight process supervisor for modern infrastructure
**Spec Version:** 1.1.0
**Plan Version:** 1.1.0
**Last Updated:** 2026-07-09
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
│   ├── metrics/               # Prometheus collectors
│   ├── web/                   # Web UI templates, embedded assets
│   │   └── static/            # HTML, CSS, JS (go:embed)
│   ├── testutil/              # Shared test helpers
│   └── version/               # Build metadata (version, commit, FIPS)
├── Taskfile.yml               # Dev workflow (build, test, lint, coverage)
├── .goreleaser.yml            # Release config (cross-compile, FIPS, GitHub)
├── .golangci.yml              # Linter config
├── go.mod                     # Module definition (pins the Go version)
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

| Component          | Choice                     | Version | Rationale                                                                                       |
| ------------------ | -------------------------- | ------- | ----------------------------------------------------------------------------------------------- |
| Language           | Go                         | go.mod  | Constitution requirement. Static binary, cross-compilation, goroutines for process supervision. |
| HTTP Server        | net/http (stdlib)          | go.mod  | Go 1.22+ enhanced routing (method+path patterns). No framework needed.                          |
| Structured Logging | log/slog (stdlib)          | go.mod  | Native structured logging with JSON/text handlers. Zero dependency.                             |
| CLI Framework      | spf13/cobra                | latest  | Subcommand routing, help generation, bash/zsh completion.                                       |
| TOML Parser        | BurntSushi/toml            | latest  | De facto Go TOML library. Full TOML v1.0 support.                                               |
| Metrics            | prometheus/client_golang   | latest  | Prometheus exposition format. Industry standard.                                                |
| Password Hashing   | golang.org/x/crypto/bcrypt | latest  | bcrypt not in stdlib. FIPS-compatible via GOFIPS140.                                            |
| Terminal I/O       | golang.org/x/term          | latest  | Raw mode for `kahi ctl fg`.                                                                     |
| Process Exec       | os/exec + syscall (stdlib) | go.mod  | Direct exec with SysProcAttr for setpgid, setuid, umask.                                        |
| Signal Handling    | os/signal (stdlib)         | go.mod  | signal.Notify for queued signal processing.                                                     |

### Frontend (Web UI)

| Component     | Choice                 | Version | Rationale                                         |
| ------------- | ---------------------- | ------- | ------------------------------------------------- |
| Templates     | html/template (stdlib) | go.mod  | Server-rendered HTML. No build step.              |
| Interactivity | Vanilla JavaScript     | ES2020  | SSE streaming, auto-refresh. No framework.        |
| Styling       | Custom CSS             | N/A     | ~200 lines. Responsive. No framework.             |
| Embedding     | go:embed (stdlib)      | go.mod  | Zero-dependency static file serving. ~50KB total. |

### Data Storage

| Component     | Choice                | Version | Rationale                                                                 |
| ------------- | --------------------- | ------- | ------------------------------------------------------------------------- |
| Configuration | TOML files            | v1.0    | Constitution requirement. No database.                                    |
| Process State | In-memory             | N/A     | State machine lives in process structs. No persistence needed.            |
| Log Buffer    | In-memory ring buffer | N/A     | Configurable per-process (default 1MB). For tailing without file logging. |
| Process Logs  | File or stdout        | N/A     | Container-first: JSON lines to stdout. Optional: file with rotation.      |

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

| Type        | Framework         | Coverage Target | Command                 | Build Tag                |
| ----------- | ----------------- | --------------- | ----------------------- | ------------------------ |
| Unit        | go test + testify | 85% (combined)  | `task test`             | (none)                   |
| Integration | go test + testify | Included in 85% | `task test-integration` | `//go:build integration` |
| E2E         | go test + testify | N/A             | `task test-e2e`         | `//go:build e2e`         |

### Coverage

- **Minimum threshold:** 85% (from constitution)
- **Coverage tool:** `go test -coverprofile=coverage.out` + `go tool cover -func`
- **Excluded paths:** `cmd/kahi/main.go`, `internal/web/static/*`, `internal/version/`

### Mocking Approach

Interfaces at OS boundaries enable testable code without real processes:

| Boundary     | Interface        | Real Implementation                       | Mock Implementation                   |
| ------------ | ---------------- | ----------------------------------------- | ------------------------------------- |
| Process exec | `ProcessSpawner` | `os/exec.Command` + `syscall.SysProcAttr` | In-memory process simulation          |
| Filesystem   | `FileSystem`     | `os.OpenFile`, `os.Rename`                | In-memory filesystem                  |
| Time         | `Clock`          | `time.Now()`, `time.After()`              | Controllable clock (advance manually) |
| Syscall      | `SyscallWrapper` | `syscall.Setpgid`, `syscall.Setuid`       | No-op or recorded calls               |

### Test Helpers (internal/testutil)

- `TempDir()` -- create isolated temp directory, auto-cleanup
- `FreeSocket()` -- generate unique Unix socket path in temp dir
- `WaitFor(condition func() bool, timeout time.Duration)` -- poll-based async assertion
- `StartTestDaemon(config string)` -- launch Kahi daemon with in-memory config for integration tests
- `MustParseConfig(toml string)` -- parse TOML string into config struct, fatal on error

---

## Deployment Architecture

| Component           | Platform                         | Rationale                                                                                                                                                   |
| ------------------- | -------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Binary distribution | GitHub Releases (via GoReleaser) | Tarballs for darwin/amd64, darwin/arm64 and linux/amd64, arm, arm64, ppc64le, riscv64, s390x. FIPS 140-3 mode on in every binary; no separate FIPS variant. |
| Container image     | Multi-arch OCI (ghcr.io)         | `FROM scratch`, USER 65534, ~10-15MB. Built via Docker buildx for linux/amd64, arm64, arm/v7, riscv64, ppc64le, s390x.                                      |
| CI/CD               | GitHub Actions                   | Standard for Go open source. Matrix builds for all target platforms.                                                                                        |

### Dockerfile

```dockerfile
# Build stage
FROM golang:1.27.3 AS builder   # abbreviated; the Dockerfile in the repository root is authoritative
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

**System requirements:** Go (the version in go.mod), git, bash
**No database or Docker required for development.**

### Taskfile Targets

| Target                  | Description                                            |
| ----------------------- | ------------------------------------------------------ |
| `task setup`            | Install golangci-lint, goreleaser, download Go modules |
| `task build`            | Compile binary to `./bin/kahi`                         |
| `task test`             | Run unit tests with race detector                      |
| `task test-integration` | Run integration tests (tag: integration)               |
| `task test-e2e`         | Run E2E tests (tag: e2e)                               |
| `task lint`             | Run golangci-lint                                      |
| `task fmt`              | Run gofmt, report changes                              |
| `task vet`              | Run go vet                                             |
| `task coverage`         | Generate coverage report, fail if < 85%                |
| `task all`              | Run fmt, vet, lint, test, build in sequence            |
| `task clean`            | Remove build artifacts                                 |

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

**Decision:** TOML as the sole configuration format. Named table syntax: `[programs.web]`, `[groups.services]`, `[webhooks.slack]`.

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

---

### ADR-009: Keyless Cosign Signing via GitHub OIDC

**Date:** 2026-04-20
**Status:** Accepted

**Context:** Release artifacts need tamper-evidence and verifiable provenance. Managing PGP or cosign key pairs in CI is error-prone: keys must be rotated, can leak via secret exposure, and require a secure place to live. The constitution favors zero long-lived secrets.

**Decision:** Use Sigstore keyless signing via GitHub Actions OIDC for all release artifacts (GoReleaser archives, `checksums.txt`, container images) and attestations. Signing identity is the GitHub workflow path pinned to a tag ref. Transparency is provided by Rekor. No long-lived keys exist at any point.

**Alternatives Considered:**

1. **Managed cosign key pair (`COSIGN_PRIVATE_KEY` in repo secrets):** Requires rotation, risk of leakage, must be stored somewhere. Rejected.
2. **PGP key signing (`gpg --sign`):** Requires key management, hardware tokens for safety, higher operational burden. Rejected.
3. **Notary v2 / OCI 1.1 referrers only (no cosign):** Supported in modern registries, but cosign remains the broader ecosystem standard (kubectl, tekton-chains, kyverno). Used together with cosign -- see ADR-011.

**Consequences:**

- `release.yml` requires `permissions: id-token: write`.
- Verification requires `cosign >= 2.x` and network access to Rekor + Fulcio.
- Identity is pinned to the tag ref (`refs/tags/<tag>`); deleting and recreating a tag invalidates identity verification.
- Supply-chain attacks against the signing path require compromising the GitHub OIDC issuer, not theft of a key at rest.

---

### ADR-010: CycloneDX 1.5 via syft for SBOMs

**Date:** 2026-04-20
**Status:** Accepted

**Context:** CycloneDX (OWASP) and SPDX (Linux Foundation) are the two mainstream SBOM formats. The ecosystem is split. CycloneDX has strong OWASP tooling, trivy/grype native support, and broader vendor adoption in enterprise pipelines.

**Decision:** Produce SBOMs in CycloneDX 1.5 JSON using syft. Attach per-archive SBOM as GitHub Release asset (SEC-004) and as a cosign attestation with `--type cyclonedx` on the container image (SEC-006). Single format; no dual emission.

**Alternatives Considered:**

1. **SPDX 2.3 via syft (syft default):** Broader LF tooling, but lower vendor penetration in the current pipeline targets. Rejected per user directive.
2. **Dual emission (SPDX + CycloneDX):** Doubles artifact count and release-asset noise; most consumers only want one. Rejected.
3. **go.mod-derived SBOM (`go list -m -json`):** Only Go-level view, misses filesystem and layer data for the container image. Rejected as primary source.

**Consequences:**

- Consumers using SPDX-only tooling convert with `cyclonedx-cli convert --output-format spdxjson`.
- Cosign attestation predicate type URI is `https://cyclonedx.org/bom`.
- CycloneDX 1.5 is current as of 2026; upgrade path to 1.6+ is additive.

---

### ADR-011: Two-Layer Signing -- BuildKit Attestations + Cosign

**Date:** 2026-04-20
**Status:** Accepted

**Context:** Modern container supply chain uses BuildKit-native SBOM/provenance attestations (OCI 1.1 referrers) and cosign signatures (Sigstore standard). They are complementary. BuildKit attestations are discoverable via registry APIs and are the path for `docker buildx imagetools inspect`; cosign attestations are discoverable via `cosign tree` and `cosign verify-attestation`.

**Decision:** Use both. `docker/build-push-action@v7` emits `sbom: true, provenance: mode=max` BuildKit attestations in-band. A subsequent step signs the image digest with `cosign sign`, and a further step attaches a CycloneDX SBOM predicate via `cosign attest`. Sign by digest, not by tag.

**Alternatives Considered:**

1. **BuildKit-only attestations, no cosign:** Loses the cosign verify workflow and certificate-identity guarantees; loses interop with Sigstore-native consumers.
2. **Cosign-only, no BuildKit attestations:** Loses OCI 1.1 referrer interop with non-cosign tooling (e.g. `docker buildx imagetools inspect --format attestation`).
3. **Sign by tag, not digest:** Tags are mutable; a retag would invalidate the signature. Rejected.

**Consequences:**

- Images are signed by digest (`ghcr.io/kahiteam/kahi@sha256:...`); verify commands accept either tag or digest.
- Two signing paths means two possible failure points; both are gated by the verify-signatures job (ADR-012).
- Registry storage is slightly larger (BuildKit attestation manifests + cosign signature manifests).

---

### ADR-012: Verify-Signatures Gate Before Release Publication

**Date:** 2026-04-20
**Status:** Accepted

**Context:** Signing failures can be missed in CI logs. Without an explicit verification gate, a release can be "published" with missing, malformed, or wrong-identity signatures. Once a tag is public, consumers may have already pulled the artifact. Per user directive, any CI failure in SBOM/signing/attestation must fail the release as a whole; no partial publication is acceptable.

**Decision:** A dedicated `verify-signatures` job with `needs: [goreleaser, docker]` re-verifies every signature and attestation produced. Any failure aborts the release workflow. GitHub Release creation (and, if later added, semver tag family promotion such as `latest`) runs only after `verify-signatures` succeeds. Earlier failures in sign/sbom/attest steps already fail the release; the gate is the final defense-in-depth check, not the only one.

**Alternatives Considered:**

1. **Trust cosign/goreleaser exit codes only:** A false-positive exit in the signing step would publish unsigned artifacts. Rejected.
2. **Post-publish periodic verify (nightly cron):** Too late; consumers have already pulled. Useful for long-term drift detection, not for release gating.
3. **Separate workflow triggered by Release event:** Same latency issue as (2), and decouples verification from publication.

**Consequences:**

- Release runtime increases by ~30-60 seconds for the verify job.
- A verify failure requires manual cleanup (delete partial GitHub Release, leave signed images orphaned in GHCR) -- acceptable trade-off versus publishing unverified artifacts.
- `verify-signatures` is a hard gate. No bypass flag. If verify tooling has a bug, the fix is to patch the tooling, not skip the check.

### ADR-013: Fail-Closed Runtime Privilege Drop

**Date:** 2026-05-26
**Status:** Accepted

**Context:** A full-codebase security review on 2026-05-26 found two gaps between the privilege-handling spec (FUNC-045, FUNC-046) and the implementation. (1) Per-process `user` was resolved into a credential but never attached to the spawned child, so a configured low-privilege user was silently ignored and children ran with the supervisor's inherited privileges (root). (2) `DropPrivileges` set the primary gid and uid but never called `setgroups`, leaving root's supplementary groups (e.g. `docker`, root-equivalent) active after the drop and inherited by children. Both are privilege-escalation exposures: an operator who explicitly configured isolation did not get it, with no error.

**Decision:** Privilege handling is fail-closed.

- The spawn path attaches the resolved credential to `SysProcAttr.Credential` whenever `user` is configured. If the credential cannot be built or applied, the process does not start; it goes to FATAL. Silent fallback to inherited privileges is forbidden.
- `DropPrivileges` calls `setgroups` (resetting supplementary groups to the target gid) before `setgid` before `setuid`. A `setgroups` failure aborts startup rather than continuing with a partial drop.
- The syscall sequence in the daemon drop is made testable via an injectable seam so the ordering and the supplementary-group reset are asserted by unit tests that run as a non-root CI user (the seam records calls; real syscalls run only when privileged).

**Alternatives Considered:**

1. **Log a warning and continue when the credential cannot be applied:** Rejected. A warning in a log is not a security boundary; the operator asked for isolation and must get it or a hard failure.
2. **Skip setgroups when the daemon has no supplementary groups:** Rejected. The daemon cannot assume its launch environment; an unconditional reset is cheap and removes the failure mode entirely.
3. **Integration-test the drop by running the suite as root:** Rejected as the primary mechanism. CI does not run privileged; the injectable seam gives deterministic assertions without root, with an optional root-gated integration test on top.

**Consequences:**

- Misconfigured `user` settings now surface as a startup/spawn failure instead of a silent privilege escalation. This is a behavior change: configurations that previously "worked" by ignoring `user` will now fail loudly. This is intended.
- Children no longer inherit the supervisor's supplementary groups after a drop.
- A small syscall-seam abstraction is added to the privilege path to keep it unit-testable; this is wired into the `task test` QA gate so the wiring cannot silently regress.

### ADR-014: Secure-by-Default FastCGI Socket Permissions

**Superseded 2026-10-18:** FastCGI was descoped and `internal/fcgi` removed (see FUNC-074/075, SEC-011). Kept for history.

**Date:** 2026-05-26
**Status:** Accepted

**Context:** The 2026-05-26 security review found `fcgi.Socket.Open` applied `chmod` to a Unix-domain socket only when `socket_mode` was explicitly configured. With `socket_mode` unset, the socket kept the umask-dependent default (often 0755, world-accessible), so any local user could connect to the FastCGI socket and speak the protocol to the backend it fronts. The constitution already requires a 0700 default for sockets, but the FastCGI path did not honor it.

**Decision:** `Open` always applies a mode to Unix sockets. When `socket_mode` is unset (0) it defaults to 0700; an explicit `socket_mode` is honored as-is. A chmod failure closes the listener and returns an error so a socket is never left in service with permissions wider than intended. TCP sockets are unchanged; their exposure is governed by the bind address.

**Alternatives Considered:**

1. **Keep skipping chmod when socket_mode is unset:** Rejected -- this is the vulnerability.
2. **Set a process-global umask around net.Listen:** Rejected -- umask is process-wide and races with other goroutines creating files; an explicit chmod is deterministic.

**Consequences:**

- FastCGI Unix sockets default to owner-only access. Operators who intentionally need a group- or world-accessible socket must set `socket_mode` explicitly. This is a behavior change for configs that relied on the permissive default.
- A negligible window exists between Listen and Chmod; the immediate chmod closes it in practice, matching the daemon control socket approach.

### ADR-015: Log Symlink Hardening and Injection-Safe SSE Framing

**Date:** 2026-05-26
**Status:** Accepted

**Context:** Two defense-in-depth items from the 2026-05-26 security review (issue #39). (1) Process and daemon log files were opened with `O_CREATE|O_WRONLY|O_APPEND` but no `O_NOFOLLOW`; if logs sit in a directory writable by a less-trusted user, that user could plant a symlink and redirect a privileged daemon's log writes to a sensitive file. (2) The log SSE endpoint wrote raw process output via `fmt.Fprintf(w, "data: %s\n\n", data)`, so output with newlines or `event:`/`data:` prefixes could inject SSE frames -- benign in impact (the web client ignores unknown event types and renders via `createTextNode`, so no XSS) but incorrect framing.

**Decision:**

- All log opens use `O_NOFOLLOW` (centralized in a single `logFileOpenFlags` constant). Opening a log path whose final component is a symlink fails rather than following it. `O_NOFOLLOW` constrains only the final component, so symlinked parent directories continue to work.
- The log SSE writer emits each output line as its own `data:` field. Embedded newlines and `event:`/`data:` prefixes are carried as literal data and cannot start a new SSE event. The event-stream endpoint already JSON-encodes its payload and is unchanged.

**Alternatives Considered:**

1. **Resolve and validate the log path with EvalSymlinks instead of O_NOFOLLOW:** Rejected -- TOCTOU between check and open; `O_NOFOLLOW` is atomic at open time.
2. **JSON-encode the SSE log payload like the event stream:** Rejected -- changes the client contract for the log viewer; per-line `data:` framing is the standard SSE mechanism and keeps the client unchanged.

**Consequences:**

- A log file that is intentionally a symlink will no longer open; operators must point the log at a real path. This is the intended hardening.
- Log SSE output is correctly framed; no behavior change for the web UI, which appends its own newline and renders text nodes.

---

### ADR-016: Config API Redaction via a Sanitized View

**Date:** 2026-07-09
**Status:** Accepted

**Context:** The 2026-07-09 security review (SEC-014, HIGH) found `handleGetConfig` serializes the live `*config.Config` directly; the structs carry only `toml:` tags, so `encoding/json` emits `Server.HTTP.Password`, every `Programs[*].Environment` map, and every `Webhooks[*].Headers`/credentialed URL verbatim. Struct tags alone cannot mask map values (env, headers) or URL userinfo.

**Decision:** Return a purpose-built sanitized view from every config-returning endpoint rather than the live struct.

- Add `json:"-"` to `HTTPServerConfig.Password` and `Username` so credentials never serialize anywhere.
- Build an explicit redacted DTO in `handleGetConfig` (and any reload/diff response that echoes config) that replaces `Environment` values and webhook `Headers` values with a fixed mask (`"***"`) while keeping keys, and strips userinfo from webhook URLs.
- Non-secret fields (program names, commands, numprocs) pass through unchanged so the endpoint stays useful.

**Alternatives Considered:**

1. **`json:"-"` tags only:** Rejected -- cannot redact per-entry map values (env vars, headers); would either drop the whole map or leak it.
2. **Reflection-based recursive scrubber keyed on field names:** Rejected -- fragile, hard to test, and easy to miss a new secret field; an explicit DTO is auditable.
3. **Remove the config endpoint entirely:** Rejected -- operators legitimately need to inspect effective config; redaction preserves the capability.

**Consequences:**

- A regression test asserts the response contains none of the seeded secret values (enables SEC-014).
- Adding a new secret-bearing config field requires updating the DTO; this is intentional friction and is documented next to the DTO.

---

### ADR-017: Fail-Closed TCP Authentication; Loopback Is Not a Trust Boundary

**Date:** 2026-07-09
**Status:** Accepted

**Context:** SEC-015 (MEDIUM): `requireAuth` grants access whenever `authUser == ""` and `StartTCP` runs on `HTTP.Enabled` alone, so enabling the HTTP API without a username exposes the full control API unauthenticated. The transport investigation confirmed the password-free local path is the Unix socket (auth-skipped, 0700 owner-only), not loopback TCP -- and loopback is shared across a network namespace (Kubernetes pods share `localhost`; `--network host` shares host loopback), so it cannot be treated as trusted.

**Decision:** Enforce credentials for any TCP bind, loopback included.

- Config validation rejects `http.enabled = true` with no username/password, for every listen address (loopback receives no exemption); the daemon refuses to start.
- A defense-in-depth guard in `StartTCP` refuses to open the listener under the same condition.
- The Unix socket remains the documented password-free local path; the CLI defaults to it and uses TCP only when `--addr` is passed.

**Alternatives Considered:**

1. **Exempt loopback binds from the credential requirement:** Rejected -- unsafe in shared network namespaces (pod sidecars, `--network host`), and unnecessary because the socket already serves local admin.
2. **Runtime 401 only (no startup check):** Rejected -- fails open on misconfiguration; a startup refusal is louder and safer.
3. **Warn-only (current behavior):** Rejected -- the review showed the warning does not fire for a `127.0.0.1` bind and does not prevent serving.

**Consequences:**

- Enabling TCP now requires credentials; this is a deliberate breaking change for any unauthenticated TCP deployment (acceptable pre-1.0), and satisfies constitution Security Requirements 1 and 3 (enables SEC-015).
- Cross-container password-free control is achieved by mounting the socket, not by loopback TCP.

---

### ADR-018: Clean Environment by Default for Privilege-Differentiated Children

**Date:** 2026-07-09
**Status:** Accepted

**Context:** SEC-016 (MEDIUM): `buildEnv` seeds every child with the supervisor's full `os.Environ()` (ADR-006 inheritance modes, `clean_environment` opt-in default false). When the supervisor runs as root and a program drops to a different `user`, the child inherits root's environment secrets. This extends the environment-inheritance model established in ADR-006.

**Decision:** Make a differing per-process `user` imply clean-environment-by-default.

- When a program's resolved `user` differs from the supervisor's identity, the child starts from a minimal base (PATH, HOME for the target user, `SUPERVISOR_*` metadata) plus the program's explicit `environment`, regardless of the `clean_environment` flag's default.
- Programs with no per-process `user` keep the existing inheritance behavior (backward compatible).
- An explicit opt-in still allows full inheritance for a privilege-differentiated child when an operator truly wants it.

**Alternatives Considered:**

1. **Keep opt-out (status quo):** Rejected -- secrets leak downward by default, the exact finding.
2. **Always clean for every child:** Rejected -- breaks same-user programs that rely on inherited environment; too broad.
3. **Require operators to set `clean_environment` per program:** Rejected -- security-by-remembering; the safe default should not depend on operator diligence.

**Consequences:**

- Satisfies constitution Security Requirement 6 (enables SEC-016); refines ADR-006.
- `HOME` for the target user is resolved from the passwd database, falling back to `/`.

---

### ADR-019: Control Socket Locked to the Service Identity

**Date:** 2026-07-09
**Status:** Accepted

**Context:** SEC-022: `isUnixConn` authorizes by transport, not peer identity (no `SO_PEERCRED` check), and `server.unix.chown` is dead config (parsed, templated, never applied at bind). At the default 0700 this is safe, but a loosened socket (group/other access) would grant unrestricted control to any connecting process. The clarify decision is to lock the socket to the service identity and not support ownership/group sharing. Related to ADR-013 (privilege drop) and ADR-014 (FastCGI socket permissions).

**Decision:** Owner-only, no sharing path.

- Keep the default mode 0700 owned by the service UID; a chmod failure closes the listener (existing behavior).
- Reject `server.unix.chown` at config validation with a clear error rather than silently ignoring it.
- Reject a `server.unix.chmod` that grants group or other access (the socket authorizes by transport, so a shared mode would grant unrestricted control).
- The migrator must not emit `chown`, and the generated template drops the commented `chown` example, so migration output stays loadable.

**Alternatives Considered:**

1. **Wire `chown` and add `SO_PEERCRED`/`getpeereid` uid/gid authorization now:** Rejected for this pass -- real value only if controlled multi-user local admin is required, which it is not; recorded as a future extension (prerequisite before any shared mode is permitted).
2. **Leave `chown` as a silent no-op:** Rejected -- false sense of control is a security footgun.

**Consequences:**

- Enforces constitution Security Requirement 2 (enables SEC-022); "lock to the service id" is realized by refusing any non-owner-only mode.
- Operators wanting another local user to run the CLI must use `sudo`/run-as the service account, or (future) the deferred peer-credential feature.

---

### ADR-020: bcrypt-Only Passwords with Constant-Time Comparison

**Date:** 2026-07-09
**Status:** Accepted

**Context:** SEC-018: `checkPassword` falls back to `plain == hash` for any non-`$2` prefix (plaintext, "testing only" but reachable), and both the username check and the plaintext branch are non-constant-time (username-enumeration timing oracle). The clarify decision is to hard-reject non-bcrypt passwords at startup.

**Decision:**

- Config validation rejects an `http.password` that is not a bcrypt hash (`$2` prefix); the daemon refuses to start. No plaintext branch remains.
- Username and password comparisons use `subtle.ConstantTimeCompare`/`hmac.Equal` so timing does not distinguish a wrong username from a wrong password.
- Test helpers migrate to bcrypt hashes.

**Alternatives Considered:**

1. **Deprecation window (warn then reject):** Rejected by the user -- clean immediate rejection preferred; acceptable breaking change pre-1.0.
2. **Keep plaintext for tests only, gated by a build tag:** Rejected -- a reachable plaintext path is a liability; bcrypt in tests is cheap enough at low cost factor.

**Consequences:**

- Satisfies constitution Security Requirement 3 (enables SEC-018); a plaintext password that previously "worked" now fails at startup with a clear message.

---

### ADR-021: Daemon Config Search Excludes the Current Directory

**Date:** 2026-07-09
**Status:** Accepted

**Context:** SEC-017: `DefaultSearchPaths` lists `./kahi.toml` before `/etc/kahi/kahi.toml`, so a root daemon started without `-c`/`KAHI_CONFIG` from an attacker-writable CWD loads a planted config and runs its commands as root -- local privilege escalation that also shadows the system config.

**Decision:** Remove the CWD-relative entry from the daemon's default search order; the daemon searches system paths only. An explicit `-c ./kahi.toml` or `KAHI_CONFIG` is still honored (explicit intent is trusted). Non-daemon convenience lookups, if any, are out of scope of this change.

**Alternatives Considered:**

1. **Honor `./kahi.toml` only when euid != 0 and the file is owned by the invoking user:** Viable and noted as an acceptable equivalent, but more code and more edge cases than simply dropping it from the daemon default.
2. **Keep current order (status quo):** Rejected -- the LPE vector.

**Consequences:**

- A deployment that relied on an implicit CWD config for the daemon must pass `-c` explicitly (enables SEC-017).

---

### ADR-022: Log Open Hardening Beyond the Final Path Component

**Date:** 2026-07-09
**Status:** Accepted

**Context:** SEC-019 extends ADR-015. `O_NOFOLLOW` guards only the final component, so a swapped parent-directory symlink can still redirect a root daemon's writes. Log paths come from trusted config, so this is defense-in-depth.

**Decision:** Open log files by walking the path with per-component `O_NOFOLLOW` (an `openat`-based traversal from a verified base directory) on platforms that support it, so no intermediate symlink is followed; on platforms lacking the primitive, fall back to the ADR-015 final-component guard and document the residual trusted-path assumption. The choice between full hardening and documented-assumption is recorded here so implementation is unambiguous.

**Alternatives Considered:**

1. **`EvalSymlinks` + validate then open:** Rejected -- TOCTOU (consistent with ADR-015's reasoning); `openat`/`O_NOFOLLOW` per component is atomic.
2. **Accept the final-component guard and only document the assumption:** Retained as the explicit fallback for platforms without `openat` semantics.

**Consequences:**

- Root daemon log writes cannot be redirected through a swapped parent dir on supported platforms (enables SEC-019); behavior for non-symlinked paths is unchanged from ADR-015.

---

### ADR-023: Length-Prefixed Event Listener Payloads

**Date:** 2026-07-09
**Status:** Accepted

**Context:** SEC-020: `formatEventPayload` emits an unframed newline-terminated `TYPE ts key:value` line into a listener's stdin. Process output containing a newline (untrusted under a process-compromise assumption) could inject a forged protocol line. supervisord length-prefixes its payload for exactly this reason.

**Decision:** Length-prefix the payload body -- announce the byte length, then write exactly that many bytes -- so embedded newlines and header-like text are carried as opaque data. The READY/RESULT handshake framing is unchanged; only the payload body gains a length prefix, keeping the documented listener protocol compatible.

**Alternatives Considered:**

1. **Escape/strip newlines in payload values:** Rejected -- lossy and error-prone; length-framing is the standard and matches supervisord.
2. **Status quo:** Rejected -- the injection vector.

**Consequences:**

- Enables SEC-020; conforming listeners parse identically. Empty payloads are framed with length 0.

---

### ADR-024: Apply RLimits and Umask in the Spawn Path

**Date:** 2026-07-09
**Status:** Accepted

**Context:** SEC-021: `ExecSpawner.Spawn` silently ignores `SpawnConfig.RLimits`, and per-process `Umask` is never applied (`ApplyRLimits`/`ApplyUmask` have no non-test callers). Operators setting `NOFILE`/`NPROC` limits or a restrictive umask get no enforcement.

**Decision:** Apply configured rlimits and umask in the child (post-fork, pre-exec), consistent with the existing credential/umask handling via `SysProcAttr`/child init. Invalid rlimit values are rejected at config validation. The platform split follows the existing `rlimit_linux.go`/`rlimit_darwin.go` convention.

**Alternatives Considered:**

1. **Apply in the parent before fork:** Rejected -- would alter the supervisor's own limits/umask.
2. **Status quo (ignore):** Rejected -- silent non-enforcement of a security-relevant control.

**Consequences:**

- Configured limits/umask take effect (enables SEC-021); a program that implicitly relied on limits NOT being applied may see a behavior change -- this is the intended fix.

---

### ADR-025: PR Test-Result Tiles -- Label Opt-In, GitHub-Only

**Date:** 2026-07-09
**Status:** Accepted

**Context:** TEST-005: the sticky PR tiles (PR #51) intentionally show only failures/skipped in the twisty to stay compact for the ~600-test suite. Reviewers want opt-in access to the full per-test list with color-coded status. Kahi's CI is GitHub Actions; `ci/gitlab` and `ci/jenkins` are documentation mapping guides.

**Decision:**

- The full per-test table is gated by a `full-test-report` PR label, evaluated at tile-render time from the `pull_request` event's label set; adding the label (`pull_request: labeled` trigger) re-runs the workflow and updates the sticky tiles in place.
- Status color uses the existing emoji indicators (no arbitrary markdown color): ✅ passed, ⚠️ skipped, ❌ failed; rows ordered failures → skipped → passed.
- The renderer truncates deterministically if the comment would exceed GitHub's 64 KB limit (all failures/skipped kept; passed rows capped with an explicit omission note).
- Scope is GitHub Actions only.

**Alternatives Considered:**

1. **`workflow_dispatch` input:** Rejected -- manual dispatch, not tied to a PR push.
2. **Repo/org variable:** Rejected -- global, noisy on every PR for a large suite.
3. **Always include the full table in a nested collapsed `<details>`:** Rejected -- still ships ~600 rows in every comment payload, the bloat the opt-in exists to avoid.
4. **Specify GitLab/Jenkins equivalents now:** Rejected -- speculative; Kahi does not run those CIs.

**Consequences:**

- Enables TEST-005; adding the label re-runs the suites. Rendering the full table from a retained JUnit artifact without re-running is a possible future optimization, out of scope here.

---

### ADR-026: Create Unix Sockets Under a Restrictive Umask

**Date:** 2026-10-18
**Status:** Accepted

**Context:** SEC-023. `net.Listen("unix", ...)` creates the socket inode at `0777 &^ umask`, and the mode is narrowed only afterwards by `os.Chmod`. Kahi inherits its umask from the launcher and never sets one, so under a permissive inherited umask the control and FastCGI sockets are briefly connectable by other users. A connection accepted in that window survives the chmod, and Unix-socket peers skip API authentication. Defense-in-depth: the common umask `022` already defeats it, because connecting requires write permission.

**Decision:** Add a leaf package `internal/sockutil` with `ListenUnix(path)`. On Unix it takes a package-level mutex and then:

1. reads the inherited umask with `old := syscall.Umask(0o777)` -- POSIX has no way to read the umask without setting it, so the read sets the strictest possible mask rather than a looser one;
2. sets `syscall.Umask(old | 0o077)`, which adds owner-only restriction to the inherited mask and never removes any of its bits;
3. calls `net.Listen("unix", path)`;
4. restores `old` with `defer` before releasing the lock.

The socket is therefore created at `0700 &^ old`: exactly `0700` under any umask that leaves owner bits alone, narrower under a stricter one. `api.StartUnix` calls it in place of `net.Listen` and keeps its own chmod and fail-closed close, so the configured mode is applied afterwards as today. (As first implemented it also served `fcgi.Socket.Open`; FastCGI was descoped on 2026-10-18 and that caller removed with `internal/fcgi`.) A `!unix` build falls back to plain `net.Listen`, matching the `open_unix.go` / `open_other.go` precedent from ADR-022.

At no instant is the effective umask less strict than the inherited one: it is `0o777` during the read, `old | 0o077` during the bind, and `old` again afterwards. That is what makes the residual below safe. A fixed `Umask(0o077)` would loosen a stricter inherited mask (e.g. `0277`) and let concurrently created files come out wider than intended.

**Alternatives Considered:**

1. **Bind inside a private `0700` directory, then rename into place:** Rejected -- Go's `UnixListener` unlinks its original bound path on close, so the renamed socket would be left behind and the original path removed; more moving parts for the same property.
2. **`fchmod` on the socket fd before `listen()`:** Rejected -- `net.Listen` performs bind and listen together with no hook in between, and `fchmod` on a socket fd does not reliably change the bound inode's mode across platforms.
3. **Unguarded set/restore without a mutex:** Rejected -- two concurrent creations interleave: the second saves the first's temporary mask as its "previous" umask and restores it last, leaving the daemon permanently at the temporary mask.
4. **Place the helper in `internal/process` (which already wraps `syscall.Umask`):** Rejected -- `process` imports `api` (it implements `api`'s interfaces), so `api` cannot import it. A dependency-free leaf package is cycle-safe for both callers.
5. **A fixed `Umask(0o077)` instead of `old | 0o077`:** Rejected -- it loosens any inherited umask stricter than `077`, so a concurrently created file could be born wider than the daemon's own umask allows.

**Consequences:**

- Unix sockets are owner-only from creation regardless of the inherited umask (enables SEC-023).
- **Accepted residual:** the umask is process-wide, so a file created by another goroutine during the bind window may be created more restrictively than requested (a log file opened `0644` under inherited `022` is created `0600`). It is never created more permissively than the inherited umask allows, because the mask is only ever tightened. Its only caller now is the control socket, which is bound once at startup before `sup.Run()` starts any children or log captures, so in practice no other file is being created during the window.
- The mutex serializes callers of `sockutil` only. The one other `umask` call site, `process.ApplyUmask`, runs in the re-exec'd `__spawn` child (`childinit.go`), a separate process, so it cannot interleave with the daemon's socket creation.

---

### ADR-027: Probe Socket-Directory Writability With an Exclusive Temp File

**Date:** 2026-10-18
**Status:** Accepted

**Context:** SEC-024. `ValidateSocketPermissions` runs before the privilege drop and probes with `os.Create(dir + "/.kahi_perm_check")`: a fixed name opened with `O_TRUNC` and without `O_EXCL` or `O_NOFOLLOW`, so a planted symlink is followed and its target truncated by root. The socket path is trusted config and the default directory is not attacker-writable, so this is defense-in-depth extending the ADR-022 principle.

**Decision:** Probe with `os.CreateTemp(dir, ".kahi_perm_check*")` and remove the returned file by name. A failed remove is not fatal: the directory is already proven writable, and the leftover file has a random name. Removing by name is safe even if the path were swapped for a symlink, because `unlink(2)` removes the link and never follows it. `os.CreateTemp` opens with `O_CREATE|O_EXCL` under a random name: `O_EXCL` fails on any existing path, symlinks included, and the random name cannot be predicted to plant one. Replace the hand-written parent-directory extraction with `filepath.Dir`, factored into a small `socketDir(path)` helper so the mapping is testable without writing into `/`. It fixes two bugs: `/kahi.sock` now maps to `/` (the old loop produced `.` and probed the working directory), and a bare `kahi.sock` now maps to `.` (the old loop left the socket path itself as the "directory", so a valid relative path failed validation).

**Alternatives Considered:**

1. **`os.OpenFile` with `O_CREATE|O_EXCL|O_NOFOLLOW` at the fixed name:** Rejected -- safe against following, but a planted file or symlink at the fixed name makes validation fail spuriously, which an attacker able to write the directory could use to block startup.
2. **`unix.Access(dir, W_OK)`:** Rejected -- `access(2)` checks the real rather than the effective UID and does not exercise the create path itself (e.g. quota or filesystem-specific refusals), so it can disagree with what `bind()` will do.
3. **Keep the fixed name and rely on `fs.protected_symlinks`:** Rejected -- Linux-only, defeated in non-sticky attacker-writable directories, absent on macOS.

**Consequences:**

- The probe cannot truncate or follow any pre-existing path, on any platform (enables SEC-024).
- **Recorded residual, out of scope:** `internal/logging/rotation.go` truncates with `os.Truncate(path, 0)`, which follows symlinks, when `backups == 0`. That branch is unreachable from config (`ApplyDefaults` replaces `0` with `10`) and the path is trusted, so it is not changed here.

---

### ADR-028: Cross-Origin Protection via the Standard Library

**Date:** 2026-10-18
**Status:** Accepted

**Context:** SEC-025. The REST API authenticates with HTTP Basic credentials, which browsers cache per origin and attach to cross-site requests, and it performs no `Origin` or `Sec-Fetch-Site` check. Process and group `start`/`stop`/`restart`, `config/reload` and `shutdown` need no body, and `signal`/`stdin` decode JSON without a `Content-Type` check, so a cross-site form triggers all of them, up to shutting the daemon down. Over TCP, exploitation needs the listener enabled (non-default) and browser-cached credentials. Browsers cannot reach the Unix socket directly, but behind a reverse proxy the socket path skips authentication entirely.

**Decision:** Build the protected handler once in `NewServer` as `s.handler = csrf.Handler(s.mux)`, where `csrf` is an `http.CrossOriginProtection` from the Go standard library. Both `StartUnix` and `StartTCP` serve `s.handler`; neither serves `s.mux` directly. `SetDenyHandler` installs a handler that calls `writeError(w, http.StatusForbidden, "cross-origin request rejected", errorCode(http.StatusForbidden))`, and `errorCode` gains a `403 -> "FORBIDDEN"` case. The message is a fixed string: the deny handler never sees `Check`'s error, which differs between its two rejection paths. `s.mux` stays as the route table, so existing handler-level tests that call `s.mux.ServeHTTP` keep testing handlers. New tests go through `s.handler`, and one sends a cross-site request over a real connection to each server and expects `403`. That has to be behavioural: the protected handler is an `http.HandlerFunc`, and comparing func-typed handlers with `==` panics at runtime. That guards against a later change wiring `s.mux` back in and silently dropping the protection.

`CrossOriginProtection` rejects unsafe-method requests whose `Sec-Fetch-Site` is anything but `same-origin` or `none`, or, when that header is absent, whose `Origin` host[:port] differs from `Host`. Requests carrying neither header are allowed as non-browser or same-origin. So `kahi ctl`, `curl` and scripts are unaffected, and no API client needs to change.

**Alternatives Considered:**

1. **Synchronizer tokens:** Rejected -- Basic auth has no session to bind a token to, so this needs new server-side state, token issuance, and a token in every form and XHR, all for the same property.
2. **Require a custom request header (e.g. `X-Kahi-Request`) on unsafe methods:** Rejected -- it breaks every existing API client that does not send it, which would be an incompatible change on a patch release.
3. **A hand-written `Origin` / `Referer` check:** Rejected -- it reimplements `CrossOriginProtection` with more edge cases (missing `Origin`, `null` origins, `Referer` stripping) and no maintenance from upstream.
4. **A configurable trusted-origin allowlist now:** Deferred -- nothing in 0.1.x needs cross-origin browser access. `AddTrustedOrigin` is available if the 0.2.x Web UI does.

**Consequences:**

- Cross-site browser requests cannot trigger state-changing API calls (enables SEC-025). The planned 0.2.x Web UI, served same-origin, inherits this with no separate CSRF design.
- Safe methods are always allowed, which makes "no safe-method handler changes state" a standing invariant of the API.

---

### Implementation Sequencing (Security Hardening + Test Tiles)

This batch (ADR-016..025) has no cross-feature code dependencies that force a strict order; the natural sequencing by blast radius and shared surface is:

1. **Config-validation gate (do first, shared surface):** ADR-017 (fail-closed TCP), ADR-019 (socket owner-only + reject chown/chmod), ADR-020 (bcrypt-only), ADR-021 (drop CWD search), ADR-024 config validation for rlimits -- all add rejections in `internal/config` validation and are low-risk, high-value.
2. **API response layer:** ADR-016 (config redaction DTO) -- isolated to `internal/api` + config struct tags.
3. **Process spawn layer:** ADR-018 (clean env for differing user), ADR-024 (apply rlimits/umask) -- both touch `internal/process` spawn.
4. **Logging/events hardening:** ADR-022 (log open hardening), ADR-023 (listener framing) -- isolated to `internal/logging` and `internal/events`.
5. **CI tooling (independent):** ADR-025 (test-tile label opt-in) -- `.github/` only, no Go coupling.

Per-feature testing steps and the machine-readable dependency graph are generated in `feature_list.json` by `/cpf:specforge features`.

---

### ADR-029: Web UI Mounted as One Authenticated Subtree of the API Server

**Date:** 2026-10-18
**Status:** Accepted

**Context:** FUNC-078..080, FUNC-084, SEC-026, STYLE-001. `internal/web` existed with templates, assets and package tests, but nothing imported it: the daemon never served it, and its feature_list entries had been marked passing on package tests alone. Its `RegisterRoutes(mux)` registers bare handlers with no authentication; `app.js` builds buttons with `innerHTML` and inline `onclick` handlers; `log.html` holds an inline `<script>`; `static_dir` replaces the embedded assets wholesale; and the ETag hashes `name+ModTime`, which is constant for embedded files, so browsers keep stale assets after an upgrade. The API server already serves one handler on both the Unix socket and TCP, wrapped in `http.CrossOriginProtection` (ADR-028); `requireAuth` skips authentication for Unix-socket peers and requires Basic credentials on TCP (ADR-017).

**Decision:**

1. **Dependency direction.** `internal/web` does not import `internal/api`, and `internal/api` does not import `internal/web`. `api.Config` gains `UI http.Handler`; `nil` means the UI is disabled. `NewServer` stores it on `Server` (`buildMux` takes no config today). When it is set, `buildMux` registers `GET /ui/` as `s.requireAuth(cfg.UI.ServeHTTP)` and `GET /{$}` as a `302` to `/ui/`. When it is `nil`, neither route exists and both paths `404` as before. The `/` redirect is wrapped by `requireAuth` too. `cmd/kahi/webui.go` (new) holds the wiring outside the daemon's run function, so it is unit-testable and counts toward coverage: a `uiLister` adapter from `api.ProcessManager.List()` to `web.ProcessLister` (it maps every field and computes `StateLower` and `UptimeStr`), and `buildUIHandler(cfg config.WebServerConfig, pm api.ProcessManager, newHandler func(web.ProcessLister, web.Config, *slog.Logger) (*web.Handler, error), logger *slog.Logger) http.Handler`. The constructor is injected so a test can make it fail. When `cfg.Enabled` is false it returns an untyped `nil` `http.Handler`, never a nil `*web.Handler` wrapped in the interface (that would be non-nil, mount `/ui/` and panic). `web.Handler` sets SEC-026's CSP and `nosniff` headers on every response it writes through an exported `web.SetSecurityHeaders(http.Header)` (so SEC-026 is testable inside `internal/web`, before mounting), and the `503` fallback calls the same function. The `401` from `requireAuth` and the mux's `307` for `/ui` are not UI responses and are exempt. `daemon.go` calls `buildUIHandler(..., web.NewHandler, ...)` and passes the result in. The adapter's `StateLower` rule: the seven known states lowercased, any other state `unknown`. Content-Type for assets comes from a fixed four-entry extension map set before `http.ServeContent`, so it does not depend on the host's MIME files. `daemon.go` also bounds `server.Stop` with a 5-second context, and `Stop` calls `Close` on each `http.Server` when `Shutdown` returns a context error, so open SSE streams (which a UI page always holds) cannot block daemon exit. One auth wrap covers the whole subtree, so a UI route added later cannot be registered without authentication; SEC-026's "every UI route returns 401 without credentials" holds by construction, and the test exists to catch a refactor that breaks the construction.
2. **`web.Handler` implements `http.Handler`.** It owns an internal `ServeMux` with `GET /ui/{$}` (status page), `GET /ui/log/{name}/{stream}` (log viewer) and `GET /ui/static/` (assets). `RegisterRoutes` is removed.
3. **Graceful degradation (Principle 5).** If `web.NewHandler` returns an error, the daemon logs it at error level and passes a handler that answers every request with `503`, `Content-Type: text/plain; charset=utf-8`, body "web UI unavailable". It still sits behind `requireAuth`. Supervision and the API are unaffected.
4. **Static assets.** An overlay `fs.FS` tries `os.OpenRoot(static_dir).FS()` first and falls back to the embedded FS only when a file is missing (`fs.ErrNotExist`). `os.Root` refuses `..` and symlinks that leave the directory; that error, and any other non-`ErrNotExist` open error, yields `404` with a debug log and no fallback. Embedded assets are `app.js`, `log.js`, `style.css` and a new `favicon.svg`; the templates link `/ui/static/favicon.svg` and the inline `data:` favicon is removed, since the CSP blocks `data:`. ETag is the hex sha256 of the file content: computed once at startup for embedded files, per request for overlay files (assets are a few KB). Files are served with `http.ServeContent` after the `ETag` header is set, so the standard library evaluates `If-None-Match` and answers `304`; the current `strings.Contains` check is removed. Templates are never read from `static_dir`.
5. **Front end.** `app.js` (status page) and a new `log.js` (log viewer). Templates carry no inline `<script>`, `<style>`, event-handler or `style=` attributes; server data reaches scripts only through `data-*` attributes on a server-rendered element. DOM is built with `createElement`, `textContent` and `replaceChildren`. Every name placed in a URL goes through `encodeURIComponent`. The SGR subset parser (FUNC-079) lives in `log.js`. Every response written by `web.Handler` carries the CSP and `nosniff` headers from SEC-026; with `script-src 'self'` and no `'unsafe-inline'`, an inline script that slips in is blocked by the browser.
6. **Config.** `config.ServerConfig` gains `Web WebServerConfig` (`toml:"web"`) with `Enabled bool` and `StaticDir string`. `config.Validate(cfg) []error` rejects `web.enabled` without `http.enabled`. `supervisor.New` captures `Server.Web` from the config it is given. Both reload paths in `internal/supervisor/runloop.go` (`handleReload` and `Reload`) compare the new `Server.Web` with that captured value, not with the previous config (both paths assign `s.config = newCfg`, so comparing with the previous config would lose the warning after one reload). They log "server.web changes take effect after restart" when the values differ. `Reload` currently discards config warnings, so it logs this one directly. `DefaultConfigTOML` gains a commented `[server.web]` block.
7. **Stream subscription order.** `handleStreamLog` and `handleEventStream` (`internal/api/api.go`) move `bus.Subscribe` before the header write and `Flush`. A browser's `EventSource` `open` event therefore guarantees the subscription exists. `log.js` issues the backlog read from its first `open` handler, so no chunk published after `open` can fall between the read and the stream (FUNC-079).

**Alternatives Considered:**

1. **`web` imports `api` and registers itself on the API mux:** Rejected -- it couples the UI to API internals and still leaves each route responsible for its own auth.
2. **`api` imports `web`:** Rejected -- the API package would carry templates and assets, and could not be built or tested without them.
3. **Keep `RegisterRoutes(mux)` and pass in an auth-wrap function:** Rejected -- every route has to remember to use it, which is the defect being fixed.
4. **A separate `http.Server` or listener for the UI:** Rejected -- it needs its own auth and cross-origin wiring and its own config, and the UI's XHRs to `/api/v1/` would become cross-origin.
5. **A JavaScript test runner for the front end:** Rejected -- it adds a Node toolchain that has to be pinned and checked for drift. Front-end safety is enforced by a Go test that scans the embedded assets for forbidden patterns (`onclick=`, `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `eval(`, `new Function`, `<script>` without `src`, `style=`), plus the CSP.
6. **Cache overlay ETags by size and mtime:** Deferred -- more code for no measurable gain at asset sizes of a few KB.

**Consequences:**

- The UI inherits authentication and cross-origin protection from the API without a separate design (enables FUNC-084, SEC-026).
- The UI is served on the Unix socket as well, unauthenticated as the whole API already is there; the docs state that a reverse proxy in front of the socket is unauthenticated.
- Assets get new ETags whenever their content changes, so upgrades reach browsers (enables FUNC-080).
- All 12 `web_test.go` requests to `/static/` and `/log/` move to `/ui/static/` and `/ui/log/`. `TestHandleIndexNonRootPath` is renamed `TestStatusPageNonRootPath`.
- Serving the UI under a different proxy prefix is not supported, because the page uses absolute paths.

---

### ADR-030: Ordered Bulk Process Actions in the Manager

**Date:** 2026-10-18
**Status:** Accepted

**Context:** FUNC-083. The Web UI's "Start/Stop/Restart All" controls need a server-side bulk action, because only the Manager knows priority order. The Manager has `AutostartAll` (startup) and `StopAll` (shutdown), and neither fits. `AutostartAll` skips processes with `autostart = false` and returns nothing. `StopAll` closes `shutdownCh`, which puts the Manager into shutdown mode. It also sends stops without waiting; `Supervisor.waitForShutdown` waits for the whole set afterwards. `Process.Start` on a RUNNING or STARTING process returns `cannot transition ... to STARTING`, and `Process.Stop` on a STOPPED, EXITED, FATAL or STOPPING process returns `not running`. The only per-process wait, `waitForStopped`, takes a timeout parameter; its two callers, `Restart` and `RestartGroup`, pass a fixed 30 seconds. `StopAll` closes `shutdownCh` without holding `m.mu` (`manager.go:252`).

**Decision:**

1. **Two new Manager methods** return a result per process. `StartAllOrdered() []api.ActionResult` and `StopAllOrdered() []api.ActionResult` both take their order from a snapshot of `sortedProcesses` when the call begins and re-resolve each process by name with `GetProcess` immediately before acting on it; a process removed since the snapshot is recorded as `error` "no such process: {name}" and not acted on, so a detached `*Process` is never started (start: priority ascending, ties by name ascending; stop: priority descending, ties by name descending). The type lives in `internal/api`, because `internal/process` already imports `internal/api` and the reverse import would be a cycle: `type ActionResult struct { Name string \`json:"name"\`; Status string \`json:"status"\`; Error string \`json:"error,omitempty"\` }`, with `Status`one of`ok`, `skipped`, `error`. Both methods return a non-nil slice, so an empty result marshals as `[]`.
2. **Skips come from a state check before the call**, so no error string is ever parsed.
   - Start skips RUNNING, STARTING and BACKOFF (BACKOFF is already retrying, and starting it would race the retry timer). A STOPPING process is an `error` ("process is stopping").
   - Stop skips STOPPED, EXITED and FATAL. BACKOFF goes to STOPPED through the existing `Process.Stop` path (`ok`). A STOPPING process is waited for, not signalled again.
3. **Start includes processes with `autostart = false`**, as `supervisorctl start all` does. It returns once each process has been asked to start, without waiting for `startsecs`. For each process the start pass checks `ShuttingDown()`, calls `p.Start`, then checks `ShuttingDown()` again; if shutdown began in between, it stops that process at once and records `error` "daemon is shutting down", which wins over any start or stop error for that process. `StopAll` takes its snapshot after closing `shutdownCh`, so a process started after the second check is in that snapshot and is stopped by shutdown. No lock is held across `p.Start`: `m.mu` is a `sync.RWMutex`, and a read lock held across the start path would deadlock against a waiting writer (`LoadConfig` on reload) if anything on that path read through the Manager. Once shutdown has begun, each remaining entry is `error` "daemon is shutting down" and is not started. `StopAll` is unchanged.
4. **Stop is sequential and waits.** Each process is stopped and waited for before the next begins, so dependents are down before the processes they depend on. `waitForStopped` already takes a timeout; the bulk stop passes that process's `stopwaitsecs` plus 5 seconds, which covers the SIGKILL escalation, and `Restart` keeps its 30 seconds. If the process is still not stopped after the wait, its entry is `error` "stop timed out after {n}s"; that needs `waitForStopped` to report whether it timed out.
5. **Restart** is a third Manager method, `RestartAllOrdered() []api.ActionResult`. It runs the stop pass, then the unexported start pass `startAllOrdered(skip map[string]bool)` with the names whose stop failed. A process whose stop failed keeps the stop error and is never started, even if it finishes stopping before the start pass reaches it. Every other process gets its start outcome, including one that was skipped on stop because it was already stopped. Both passes use the one snapshot taken when the call begins, and the result has exactly one entry per process in start order; a failed-stop entry sits at its start-order position. `StartAllOrdered()` is `startAllOrdered(nil)`.
6. **One bulk call at a time; none during shutdown.** One `sync.Mutex` field on `api.Server`, shared by the three bulk routes and both listeners, is taken with `TryLock`; a call that cannot take it gets `409 CONFLICT` "bulk action in progress". A call that arrives when `s.daemon.IsShuttingDown()` or `pm.ShuttingDown()` is true gets `409` with code `SHUTTING_DOWN` "daemon is shutting down", the response the per-process start route already gives (`api.go`, `handleStartProcess`). Both signals are checked: the supervisor's flag is set by API-triggered `Shutdown()` before `StopAll` runs, but on signal-driven shutdown (SIGTERM, SIGINT, SIGQUIT) it is never set today, and only the Manager's `shutdownCh` (closed by `StopAll`) reports it. The shutdown check runs before `TryLock`, so `SHUTTING_DOWN` wins over `CONFLICT`. The Manager exposes `ShuttingDown() bool` (`shutdownCh` closed). The action does not use the request context, so a client disconnect does not abort it.
7. **API.** `api.ProcessManager` gains `StartAllOrdered`, `StopAllOrdered`, `RestartAllOrdered` and `ShuttingDown`. All three implementers are updated: `process.Manager`, the mock in `internal/api/api_test.go` (also embedded by `cross_origin_test.go`), and the one in `internal/testutil/integration_test.go`. `POST /api/v1/all/start`, `/stop` and `/restart` sit behind `requireAuth` and return `200 {"results": [...]}` in the order processes were acted on. The routes are POST-only, so ADR-028's safe-method invariant holds. The API servers set no `WriteTimeout`, so a long sequential stop is not cut off.

**Alternatives Considered:**

1. **Client-side iteration in the browser:** Rejected -- parallel XHRs lose priority order, and every API client would have to repeat the logic.
2. **Reserve the process name `all`:** Rejected -- names are not validated, so an existing program called `all` would silently change meaning.
3. **Send all stops, then wait for the set (the shutdown pattern):** Rejected -- it orders only the delivery of signals, not when processes have actually stopped.
4. **Reuse `AutostartAll` / `StopAll`:** Rejected -- wrong `autostart` semantics, no results, and `StopAll` triggers shutdown mode.
5. **Treat the transition errors as skips:** Rejected -- matching on error strings is brittle, and a real failure could be hidden as a skip.

**Consequences:**

- Bulk stop can take up to the sum over processes of `stopwaitsecs` plus 5 seconds. The HTTP response arrives when the last process has stopped or timed out.
- Bulk calls and config reloads are not serialized against each other. A process removed by a concurrent reload yields an `error` "no such process" entry; one added during the call is not acted on.
- Known gap, pre-existing: `StopAll` ignores BACKOFF processes and `retryAfterBackoff` does not check for shutdown, so a BACKOFF process can respawn during or after shutdown. Bulk start adds one more way to reach BACKOFF just before shutdown. Tracked as a separate fix, not part of 0.2.0.
- The browser shows one notification per failed process (FUNC-078).
- `kahi ctl` can use the endpoint later; that is not part of FUNC-083.

---

### Implementation Sequencing (0.2.0 Web UI)

Ordered by dependency. Each step lands with its tests red first; the important guards are mutation-checked.

1. **Config:** `WebServerConfig`, validation, `DefaultConfigTOML` block, reload warning (FUNC-084, config part). Isolated to `internal/config` and `internal/supervisor`.
2. **Manager bulk methods:** `api.ActionResult`, `StartAllOrdered`/`StopAllOrdered`/`RestartAllOrdered`/`ShuttingDown` (re-resolve, check, start, re-check); the four methods are added to `api.ProcessManager` in this step and stubbed in both test mocks (`internal/api/api_test.go`, `internal/testutil/integration_test.go`) so the step compiles and passes on its own; `MockSpawner.Spawn` gains a mutex around `SpawnCalls` (`spawner.go`), the timed-out report from `waitForStopped` (FUNC-083 core, ADR-030). Touches `internal/process` and adds the type to `internal/api`. Mutation check: making `StopAllOrdered` call `sortedProcesses(true)` must fail a test. `TestBulkStopWaitTimeout` and `TestBulkRestartSkipsFailedStop` each take about 6 seconds by design (`stopwaitsecs` 1 plus the 5-second margin); do not shorten them by changing the margin.
3. **API:** the bulk routes with the `SHUTTING_DOWN` check before the `TryLock` mutex; `Stop` calling `Close` after a context error; the `UI` field, the `/ui/` and `/` routes behind `requireAuth`; the subscribe-before-flush reorder in `handleStreamLog` and `handleEventStream` (FUNC-083, FUNC-084, FUNC-079). Step 3 mounts only a stub UI handler in tests; nothing serves the real UI until step 5, after SEC-026 is met in step 4. Mutation check: removing the `requireAuth` wrap on `/ui/` must fail a test.
4. **Web package:** `ServeHTTP` with the `/ui/` routes; overlay FS, content ETag and `favicon.svg` (FUNC-080); template and JS rewrite, `log.js`, SGR subset, `pathEscape` template func, CSP and `nosniff` headers set by `web.Handler`, the forbidden-pattern asset scan, `data-label` cells, `state-unknown` (FUNC-078, FUNC-079, SEC-026, STYLE-001). Mutation check: reverting the ETag to name+mtime must fail a test.
5. **Daemon wiring:** `cmd/kahi/webui.go` with `uiLister`, `buildUIHandler` and the `503` fallback (with the SEC-026 headers), called from `daemon.go`; the 5-second context for `server.Stop` in `daemon.go`; README and `kahi init` documentation (FUNC-084).
6. **End to end:** TEST-006 in `e2e/` (`//go:build e2e`, run by `integration.yml` via `task test-e2e`), using `FreeTCPPort`, not a fixed `listen` address. Then a fresh XSS review of `internal/web` now that it is reachable (SEC-026).

Only after step 6 passes are the Web UI feature_list entries marked `passes: true`. Where a feature_list entry spans several steps (`web-ui-config-mounting` covers steps 1, 3 and 5), this order wins over the entry order in `feature_list.json`.

---

### ADR-031: Ordered Shutdown by Priority Level in the Supervisor

**Date:** 2026-10-29
**Status:** Accepted

**Context:** FUNC-066, #151, pulled into v0.2.3 on 2026-10-29. `Manager.StopAll` closes `shutdownCh` and signals every stoppable process back to back; `Supervisor.waitForShutdown` then waits for the whole set up to `shutdown_timeout`, reaping on SIGCHLD and on a 100 ms tick, and SIGKILLs everything on timeout or a second signal (#91). Reaping happens only in the supervisor's goroutine, so waiting for a level must happen there too.

**Decision:**

1. `config.OrderKey(cfg, program) (group, own int)` is the one definition of the FUNC-008 ordering key without the name, used by the Manager's start order, the bulk API, the shutdown levels and the config warning. `Manager.ShutdownLevels() [][]string` returns process names grouped by that key (`[groups]` priority or own priority, then own priority), levels in descending order, names within a level descending. It closes `shutdownCh` first, exactly as `StopAll` does, so nothing starts, restarts or retries afterwards (#92); both close it through a `sync.Once`, so calling either twice does not panic.
2. `Supervisor.shutdownProcesses()` replaces the `StopAll` + `waitForShutdown` pair: for each level it logs "stopping priority level {key}" with the names ({key} is the own priority, or "{group}/{own}" for members of a `[groups]` table), calls `Stop` on each stoppable process (BACKOFF goes to STOPPED without a signal), then runs the existing wait loop until every process of the level is stopped or the global deadline passes. One deadline, `shutdown_timeout` from the start of shutdown, covers all levels. Timeout or a second TERM/INT/QUIT SIGKILLs every remaining process in every level (the existing kill path) and waits up to 1 s to reap, logging "process {name} stuck, giving up" for any left. Only this path logs "stuck"; a process stuck after its own stopwaitsecs SIGKILL holds its level until the deadline.
3. A level's own SIGKILL escalation is each process's `stopwaitsecs` (FUNC-003), unchanged.
4. Config: the per-program "stopwaitsecs exceeds shutdown_timeout" warning is replaced by one warning when the sum over levels of the largest `stopwaitsecs` in each level exceeds `shutdown_timeout` (computed in `internal/config` with the same key, so `[groups]` membership counts). The sample configs carry the tuning comments of FUNC-066.

**Alternatives Considered:**

1. **Wait inside the Manager:** Rejected -- the Manager does not reap; waiting there would need a second reaper or a callback into the supervisor.
2. **A per-level timeout:** Rejected -- the spec bounds the whole shutdown, and per-level budgets would let a slow level starve later ones in hard-to-predict ways.

**Consequences:**

- A full shutdown can take up to the per-level sum; the warning says so at load.
- The e2e test bounds FUNC-066's three-level example at 3-5 s from the signal to the daemon's exit.
- `StopAll` remains for callers that must stop everything at once (it is no longer used by the daemon's shutdown).

### ADR-032: State and Group Event Payloads

**Date:** 2026-10-29
**Status:** Accepted

**Context:** FUNC-050, FUNC-053, #154. `Process.doPublishState` sends `{name, group, state, pid}`. `PROCESS_GROUP_ADDED` is never sent by the binary (`Manager.AddGroup` has no caller); `PROCESS_GROUP_REMOVED` is sent by `removeProgram` on reload with `{group}` only. Event data is `map[string]string`.

**Decision:**

1. Transitions and their publishes are serialized per process: `watchStart` today transitions to RUNNING and publishes without `p.mu` (`process.go`, `watchStart`), so it moves under `p.mu` and uses the locked publish path. `Process` keeps `lastPublished State`; every publish adds `from_state` (the last published state, so a STARTING -> BACKOFF -> FATAL step in one transition reports from STARTING) and updates it.
2. PROCESS_STATE_EXITED adds `expected` ("true"/"false", the `exitcodes` match used by autorestart), `exitstatus`, and `exitsignal` when set (#201), named in the `<signal.h>` form (ADR-038). STARTING is published before the spawn, with pid "0".
3. Group events carry `group` and `processes` (names, sorted, comma-separated). A `[groups]` table is one group: adding or dropping it publishes one event listing all member processes. The Manager publishes ADDED from one place, `activateGroup`, used by reload for added programs and `[groups]` tables and by `kahi ctl add` (ADR-034); REMOVED from `deactivateGroup`, used by reload removals and `kahi ctl remove`. `LoadConfig` at startup calls neither publish path. A changed program restarted by reload publishes neither.

**Alternatives Considered:**

1. **Typed event structs instead of string maps:** Rejected for v0.2.3 -- every subscriber and the SSE encoder use the map; a typed payload is a 0.3.0 change with listeners and webhooks (#106).

**Consequences:** SSE clients gain fields; existing fields are unchanged.

### ADR-033: Log Stream Offsets and Resume

**Date:** 2026-10-29
**Status:** Accepted

**Context:** FUNC-027, #155. Each capture writer keeps a 64 KB ring buffer. The Manager's stdout/stderr handler closures (`manager.go`, `LoadConfig`) call `cw.Capture(data)` and publish the returned chunk on the bus; with `redirect_stderr` no stderr capture writer exists, so no stderr closure is installed, and the process routes its stderr pipe to the stdout handler (`process.go`); the `redirect` branch inside the stderr closure is dead. Two pipe goroutines can then call the stdout closure concurrently. `handleStreamLog` forwards bus chunks live, without ids, and drops a chunk when 64 are queued. `ReadTail` does not take `cw.mu`.

**Decision:** (user decision 2026-10-29: close + reset + gap event)

1. `CaptureWriter` gets a `generation` (decimal Unix nanoseconds at its creation, unique per process creation and across daemon restarts) and counts the bytes it has captured (after ANSI stripping) in `total int64`. `CaptureWriter.CaptureAndPublish(data, publish func(out []byte, end int64))` strips, stores, advances `total` and calls `publish` while holding `cw.mu`, so two goroutines writing the same stream publish in offset order. The closures publish with `id` = "{generation}-{end}" in the event data. Because `Bus.Publish` runs subscriber callbacks synchronously under `cw.mu`, a log-event callback must not call into the capture writer (`ReadFrom`, `ReadTail`); `handleStreamLog` reads the replay outside its callback. The dead `redirect` branch is removed; with `redirect_stderr` the stderr stream stays empty, as today.
2. `CaptureWriter.ReadFrom(offset int64) (data []byte, start, total int64)` returns, under `cw.mu`, what the ring buffer holds after `offset`, the offset it starts at, and `total`. `api.ProcessManager` gains `ReadLogFrom(name, stream, offset)`. `ReadTail` takes `cw.mu` as well.
3. `handleStreamLog` parses `Last-Event-ID`: absent or empty = live only; not `{generation}-{offset}` with a non-negative offset = 400 "invalid resume position: {value}". It subscribes first, then reads the replay:
   - another generation, or `offset > total`: the process was created again: `event: reset`, `data: {}`, then replay everything held.
   - `start > offset`: `event: gap`, `data: {"bytes": start-offset}`, then replay from `start`.
   - otherwise replay from `offset` (nothing when `offset == total`). The replay is one unnamed event with `id: {generation}-{start + len(data)}`; live chunks follow, skipping any whose `end` is not past the replayed end. Every log event gets `id: {generation}-{end}`; `gap` and `reset` events get none.
4. When the subscriber queue is full the handler ends the response instead of dropping a chunk. A browser EventSource reconnects with its last id, and step 3 replays the missed bytes (or announces a gap). No chunk is dropped silently from an open stream.
5. The generation, counter and buffer belong to the capture writer, so a recreated process (reload, remove+add, daemon restart) gets a new generation and offsets from 0. A plain offset could not tell a recreated process that already wrote past the old offset from the same process (user decision 2026-10-29).

**Alternatives Considered:**

1. **Offsets into the log file:** Rejected -- programs without a log file stream too, and rotation would invalidate offsets.
2. **Replay on every connect:** Rejected -- the Web UI reads the backlog itself and would show it twice.
3. **Drop on overflow and announce a gap:** Rejected by the user -- the data is still in the buffer, and closing lets the client fetch it.
4. **Gap as an SSE comment:** Rejected -- EventSource never surfaces comments, so the Web UI could not show it.

**Consequences:** A client that reconnects within the 64 KB window loses nothing; beyond it, the gap is announced. `log.js` handles `gap` and `reset` events (FUNC-079).

### ADR-034: Group Add and Remove

**Date:** 2026-10-29
**Status:** Accepted

**Context:** FUNC-010, FUNC-011, FUNC-035, #212. `kahi ctl add` is reload plus start; `kahi ctl remove` only stops. `Manager.AddGroup`/`RemoveGroup` exist without callers. The supervisor holds the loaded config; the Manager does not. `daemon.go` wires the API's `GroupManager` to the Manager and its `ConfigManager` to the supervisor. `ApplyReload` calls `LoadConfig(newCfg)`, which re-creates every program of the new config, removed groups included. Members of a `[groups]` table also keep their implicit per-program group today.

**Decision:**

1. A `[groups]` table owns its members (user decision 2026-10-29): `LoadConfig` builds no implicit group for a member program, and a member's `Process.Group()` is the table name (status, API, events). `Process` gains `Program()`, the program name; `programProcesses`, `removeProgram` and the FUNC-008 key lookup (`groupPriority`) are keyed on `Program()`, so a member keeps its table priority and reload still finds its processes. `{name}:*` resolves a group first, then a program of that name (`programProcesses`), so `web:*` keeps working for members. add/remove of a member program fail with "{prog} is in group {table}; use {table}" (404). The supervisor keeps `inactive map[string]bool`, the groups removed at runtime. `Supervisor.AddGroup(name)` fails with "no such group: {name}" if the loaded config defines no program or `[groups]` table of that name, with "group already active: {name}" if it is active; otherwise it loads that group's programs from the loaded config into the Manager (`activateGroup`), starts the autostart ones, publishes ADDED and clears the mark. `Supervisor.RemoveGroup(name)` fails with "no such group" or "group {name} has running processes, stop first" (any process not STOPPED, EXITED or FATAL); otherwise the Manager retires each process first (`Retire`, so a pending restart or backoff retry cannot fire), then drops the group's processes, capture writers and group entry (`deactivateGroup`), publishes REMOVED and the supervisor marks it inactive. Both hold the supervisor's `reloadMu` and fail with "daemon is shutting down" (409 SHUTTING_DOWN, as the other APIs) once shutdown has begun.
2. `LoadConfig` runs at startup only. Reload no longer calls it. `ConfigDiff` diffs groups as well as programs: the unit is the effective group (a table, or a program outside any table), with its member list and its settings. `ApplyReload` keeps today's handling of changed programs: supervision-only changes are applied in place, spawn-affecting ones retire, stop, wait stopwaitsecs plus margin and recreate the processes (restarting them only if they were running, starting or in backoff, #119), and stuck programs keep their old config. `deactivateGroup` and `activateGroup` only drop or create groups, after their processes are stopped: for removed and added groups and for the split and merge of tables (where autostart applies, as for any added group). Then the group priorities are rebuilt. A table dropped while its programs stay is a removed table plus added program groups; a table added over active programs is removed program groups plus an added table; a changed member list is both. So a group removed at runtime is not re-created behind the supervisor's back. `applyReload` and `PreviewReload` move an inactive group that the new config still defines from "changed"/unchanged to "added" (activated, mark cleared); one the new config drops is forgotten.
3. API: `POST /api/v1/groups/{name}/add` and `/remove` behind `requireAuth`, 409 CONFLICT for the two state errors, 404 NOT_FOUND for unknown groups; `api.ConfigManager` (wired to the supervisor) gains `AddGroup`/`RemoveGroup`; both API test mocks get stubs.
4. `kahi ctl add|remove` call these routes, print "{group}: added" / "{group}: removed", exit 1 on error. The unused `Manager.AddGroup`/`RemoveGroup` are replaced.

**Alternatives Considered:**

1. **Remove by stopping only (today):** Rejected by the user decision (supervisorctl semantics).
2. **Re-read the config file on add:** Rejected -- supervisorctl adds from the configuration the daemon has; `reread`/`update` exist to load a changed file.

**Consequences:** `status` no longer lists a removed group; a reload brings it back.

### ADR-035: kahi ctl Exit Codes and `all`

**Date:** 2026-10-29
**Status:** Accepted

**Context:** FUNC-031, #213. `ctl start|stop|restart` print per-target errors and exit 0; `all` is sent as a process name.

**Decision:** The three commands attempt every target, collect failures, and return an error after the loop ("{n} of {m} failed", printed on stderr by `main`) so the binary exits 1; the per-target lines are unchanged. `all` must be the only argument: combined with other names it is a usage error, "all cannot be combined with other names", and nothing is sent. Every usage error of every `kahi` command exits 2 (user decision 2026-10-29): a `usageError` type that `main` maps to 2, returned by the `all` check, by the cobra `Args` validators (wrapped) and by `SetFlagErrorFunc`. cobra rejects unknown subcommands only on the root; a non-runnable parent such as `kahi ctl foo` prints help and exits 0, so parent commands get an `Args` validator that returns a `usageError` when arguments are present. Runtime failures stay 1. The single argument `all` calls `/api/v1/all/{action}` and prints one line per result ("{name}: ok|skipped|error: {message}"), failing when any result is an error. `name:*` keeps the group route.

**Alternatives Considered:** A `--keep-going` flag -- rejected; every target is attempted already, only the exit code changes.

**Consequences:** Scripts that ignored errors now see exit 1. Release-note item.

### ADR-036: Group Name Collision Check

**Date:** 2026-10-29
**Status:** Accepted

**Context:** FUNC-009, FUNC-010, #214. The "multiple groups" error names the two groups in map order; it will name them sorted. `LoadConfig` builds the implicit group of each program, then `[groups]` tables, overwriting an implicit group of the same name. `internal/process/group.go` holds `ValidateGroupNameCollisions` and two builders that nothing calls.

**Decision:** `config.validateGroupReferences` (which runs after includes) also refuses a `[groups]` key equal to a program key: "groups.{x}: name collides with program {x}". The dead functions in `group.go` and their tests are removed.

**Consequences:** A config that relied on the overwrite now fails to load; the error names both.

### ADR-037: Root-Only E2E Steps in a Container

**Date:** 2026-10-29
**Status:** Accepted

**Context:** #162, decided 2026-10-29. Privilege drop, the clean environment for a different user, the root warning and process user switching need root, which the test user lacks. `e2e/pid1_test.go` already builds Linux binaries into a `FROM scratch` image and runs it with podman or docker as root.

**Decision:** A shared helper in `e2e/` builds the image (kahi plus the static helper `e2e/testdata/orphaner`, extended with `env`, `id`, `limits` and `umask` subcommands that print to stdout), takes a config and optional extra files (an `/etc/passwd` for HOME lookups, a 1777 directory), runs the container as root and returns its logs. Root-only tests use it. As in TestPID1_ReapsOrphans, they skip without a running runtime and fail on a Linux CI runner without one.

**Consequences:** These tests run on the Linux CI runner (and locally with a podman machine); they never need sudo.

### ADR-038: Signal Names Follow signal.h

**Date:** 2026-10-29
**Status:** Accepted

**Context:** User decision 2026-10-29: signal names follow the `<signal.h>` definitions. Input is already lenient: `config` validation and `process.ParseSignal` strip a `SIG` prefix and ignore case; `ParseSignal` also accepts STOP and CONT for the signal API. Output is not: the default and `kahi init` write `TERM` (`generate.go` lists bare names), the runloop strips `SIG` from `unix.SignalName` for `exitsignal` and the reaped-orphan log and logs received signals with `sig.String()` ("terminated"), `kahi migrate` strips `SIG` and its warning says `stopsignal = KILL`, and the signal API echoes the name it received. `exitsignal` (#204) is not in a release yet.

**Decision:**

1. Input stays as lenient as today (optional `SIG` prefix, any case). `process.ParseSignal` returns the canonical `SIG` name as well; the signal API and `kahi ctl signal` keep its nine signals (TERM, HUP, INT, QUIT, KILL, USR1, USR2, STOP, CONT), `stopsignal` keeps its seven (no STOP/CONT). The error text is unchanged ("invalid signal").
2. Every output uses `unix.SignalName` unmodified: `exitsignal` in the API, status and events, the signal API's `signal` field, ctl lines, the reaped-orphan log, the "received signal" log (instead of `sig.String()`), and the migrate warning text.
3. The default `stopsignal` is `SIGTERM`; `kahi init` (including the comment listing the allowed names), the sample configs and the README show the `SIG` form; `kahi migrate` writes it.

**Alternatives Considered:** SIG form only on input -- rejected, it breaks every existing `stopsignal = "TERM"` config and supervisord habits.

**Consequences:** API consumers see `"SIGHUP"` instead of `"HUP"` in the signal response. Release-note item.

---

### Implementation Sequencing (v0.2.3: #149 and #162 in full)

Each step is one PR, tested red first, with e2e proof against the binary and the feature_list entries it completes; `GOOS=linux go vet -tags=e2e ./e2e/` before pushing.

1. **Spec, plan, features** (this change): clarify, ADR-031..038, feature_list steps.
2. **Signal names** (#215, ADR-038): canonical names, outputs, defaults, migrate. First, so every later step asserts `SIG` names (cli-process-control's "web: signaled SIGHUP", EXITED's `SIGKILL`).
3. **#213 ctl exit codes and `all`** (ADR-035, all usage errors exit 2) and the cli-process-control e2e; before the collision check because the homogeneous-groups e2e asserts exit codes.
4. **#214 collision check** (ADR-036) and the homogeneous-groups e2e.
5. **#154 event payloads** (ADR-032, the state part) and the event-bus / process-state-events e2e (one stream step through `/api/v1/events/stream`).
6. **#212 group add/remove** (ADR-034, with the group events of ADR-032) and the heterogeneous-groups / group-lifecycle / process-group-events / cli-config-operations e2e (config-hot-reload's reload steps that these entries need are covered here; the entry itself stays with #136).
7. **E2E for capture:** process-output-capture and process-output-ring-buffer, which sse-streaming depends on.
8. **#155 log stream resume** (ADR-033, `log.js` gap/reset handling) and the sse-streaming / web-ui-log-viewer e2e.
9. **#151 ordered shutdown** (ADR-031) and the graceful-shutdown / signal-shutdown / priority-ordering / signal-queuing e2e.
10. **E2E only:** config-search-paths, daemon-config-search-excludes-cwd, config-migration-tool and migration-validation, and the security entries (config-api-secret-redaction, log-open-symlink-hardening, control-socket-owner-only-lockdown, unix-socket-owner-only-at-bind, socket-dir-probe-no-follow, api-cross-origin-protection, web-ui-auth-output-encoding); child-process-isolation.
11. **Root steps in a container** (ADR-037, the shared helper is part of root-detection-warning): root-detection-warning, spawn-rlimit-umask-application, clean-env-privilege-differentiated-children.

**Dependencies on entries outside these steps.** v0.2.3 also needs the #136 re-check (heterogeneous-groups is scheduled above; config-hot-reload, signal-sighup-reload, log-reopen-sigusr2, signal-sigusr2-log-reopen, http-basic-auth and config-include-system stay with #136) and the root entries' own dependencies, which step 11 proves together with them in the container: privilege-dropping (root-detection-warning), resource-limits and process-umask (spawn-rlimit-umask-application), per-process-user-switching (clean-env-privilege-differentiated-children). Passing entries that depend on failing ones (25 pairs at this change), by the step that resolves the failing side:

- step 3 (cli-process-control): cli-status-display, cli-log-tailing, cli-daemon-operations, cli-health-check
- step 5 (event-bus): tick-events
- step 7 (process-output-capture, process-output-ring-buffer): log-rotation, ansi-escape-stripping, web-ui-log-viewer, api-input-bounds-validation
- step 8 (sse-streaming): cli-log-tailing, web-ui-status-page, web-ui-log-viewer
- step 9 (priority-ordering, graceful-shutdown): process-bulk-actions
- step 10 (config-migration-tool, api-cross-origin-protection, web-ui-auth-output-encoding): migration-dry-run, process-bulk-actions, web-ui-config-mounting, web-ui-e2e-reachability
- #136 (http-basic-auth, config-hot-reload): tcp-listener-fail-closed-auth, bcrypt-only-password-constant-time, process-bulk-actions, web-ui-config-mounting
- #136 must also be merged before the flips in step 6 (cli-config-operations -> config-hot-reload) and step 10 (config-api-secret-redaction, api-cross-origin-protection, web-ui-auth-output-encoding -> http-basic-auth); it runs alongside steps 2-5
- group-lifecycle uses `kahi ctl update`, but config-hot-reload depends on group-lifecycle, so no reverse dependency is recorded; step 6 covers the reload steps it needs
- accepted, infrastructure on infrastructure: e2e-test-infrastructure -> integration-test-infrastructure, release-pipeline-hardening -> semver-alpha-releases

v0.2.3 is tagged only when every pair outside the accepted line is resolved.
