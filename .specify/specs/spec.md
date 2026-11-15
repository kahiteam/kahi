# Feature Specification: Kahi

## Overview

**Project:** Kahi -- Lightweight process supervisor for modern infrastructure
**Version:** 1.0.0
**Last Updated:** 2026-11-14
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

### Conventions

- **Signal names follow `<signal.h>`** (decided 2026-10-29): `SIGTERM`, `SIGKILL`, `SIGHUP`. Every output uses this form: `exitsignal` in the API, status and EXITED events, the `signal` field of the signal API response, `kahi ctl` output, log lines and error texts. Inputs (`stopsignal`, `kahi ctl signal`, the signal API body) accept the name with or without the `SIG` prefix (`TERM` or `SIGTERM`) and normalize it to the `SIG` form, so supervisord configs and existing kahi.toml files keep working. The default `stopsignal`, `kahi init` and the docs show the `SIG` form; `kahi migrate` writes it. Release-note item for v0.2.4.

---

## Infrastructure Features

Infrastructure features have NO dependencies. They establish the foundation.

### INFRA-001: Go Module and Project Structure

**Description:** Initialize the Go module and establish the canonical directory layout for the Kahi project.

**Acceptance Criteria:**

- [ ] `go.mod` exists with module path `github.com/<org>/kahi` and Go 1.26.2 minimum
- [ ] `cmd/kahi/main.go` exists as the single binary entry point
- [ ] `internal/` directory contains packages: `config`, `process`, `supervisor`, `api`, `events`, `logging`, `ctl`, `migrate`
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

**Edge Cases:**

- A key that is absent gets its default. A key that is set keeps its value, including 0 (#138): `priority = 0` starts first, `startsecs = 0` counts a process as RUNNING at once, `startretries = 0` never retries, `*_logfile_backups = 0` keeps no backups, and `exitcodes = []` expects no exit status.
- Zero or negative values that would kill processes without a grace period are config errors: `stopwaitsecs` and `supervisor.shutdown_timeout` must be >= 1, and `numprocs` must be >= 1. Negative `startsecs`, `startretries`, `*_logfile_backups`, `minfds` and `minprocs` are config errors too.

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

### INFRA-007: Module Path Migration (kahidev -> kahiteam)

**Description:** Rename the Go module from `github.com/kahidev/kahi` to `github.com/kahiteam/kahi` and update all references across the codebase. The `kahiteam` GitHub organization is the permanent home for this project.

**Scope:** 68 occurrences across 32 files. The Go toolchain handles import rewrites automatically via `go.mod`; remaining references in build configs, documentation, and spec artifacts require manual update.

**Acceptance Criteria:**

- [ ] `go.mod` declares `module github.com/kahiteam/kahi`
- [ ] All Go source files import from `github.com/kahiteam/kahi/...`
- [ ] `Taskfile.yml` ldflags reference `github.com/kahiteam/kahi/internal/version`
- [ ] `.goreleaser.yml` ldflags reference `github.com/kahiteam/kahi/internal/version`
- [ ] `.goreleaser.yml` release owner is `kahiteam`
- [ ] `README.md` URLs point to `github.com/kahiteam/kahi`
- [ ] `CLAUDE.md` repository field is `github.com/kahiteam/kahi`
- [ ] `constitution.md` Project Identity includes canonical URL `github.com/kahiteam/kahi`
- [ ] `cmd/kahi/kahi.toml` comment URL points to `github.com/kahiteam/kahi`
- [ ] `go build ./cmd/kahi` succeeds with the new module path
- [ ] `go vet ./...` produces zero findings
- [ ] All unit tests pass (`task test`)
- [ ] All E2E tests pass (`task test-e2e`)
- [ ] No occurrences of `kahidev` remain anywhere in the repository (verified via `grep -r`)

**Error Handling:**

| Scenario                          | Behavior                                                              |
| --------------------------------- | --------------------------------------------------------------------- |
| Import path mismatch after rename | `go build` fails with unresolved import; fix by running `go mod tidy` |
| GoReleaser owner mismatch         | Release upload fails; verify `.goreleaser.yml` owner field            |
| Stale go.sum entries              | `go mod tidy` removes stale checksums                                 |

**Dependencies:** None

---

### INFRA-008: Repository Migration to kahiteam Organization

**Description:** Push the repository to `github.com/kahiteam/kahi` as the canonical home. Configure the new repository with branch protection, CI workflows, and release infrastructure. This feature depends on INFRA-007 (module rename) being complete first.

**Acceptance Criteria:**

- [ ] Repository exists at `github.com/kahiteam/kahi` with full commit history
- [ ] Default branch is `main` with branch protection enabled
- [ ] All GitHub Actions workflows run successfully on the new repository
- [ ] GoReleaser can publish releases to `kahiteam/kahi`
- [ ] `go install github.com/kahiteam/kahi/cmd/kahi@latest` works
- [ ] GitHub Topics set: `process-supervisor`, `go`, `posix`, `supervisord`

**Error Handling:**

| Scenario                       | Behavior                                                           |
| ------------------------------ | ------------------------------------------------------------------ |
| Push rejected (no permissions) | Verify org membership and repo create permissions                  |
| CI fails on new repo           | Check secrets, permissions, and workflow trigger conditions        |
| `go install` fails             | Verify module proxy has indexed the new path; wait for proxy cache |

**Dependencies:** INFRA-007

---

### INFRA-009: Semantic Versioning and Alpha Releases

**Description:** Implement SemVer 2.0.0 release strategy with alpha/beta/rc pre-release support. GoReleaser `prerelease: auto` detects pre-release tags automatically. Pre-releases must not be marked as "Latest" on GitHub. Release archives include README, LICENSE, and example config.

**Acceptance Criteria:**

- [ ] GoReleaser config sets `make_latest: false`: the release is created as a draft, and the release workflow's promote step decides "Latest" when it publishes it, true only for a non-prerelease tag that is the highest released version, so neither a pre-release nor a patch on an older line (a 0.1.x after 0.2.x) takes "Latest" (#150)
- [ ] Tag `v0.1.0-alpha.1` produces a GitHub pre-release (not marked "Latest")
- [ ] Tag `v0.1.0` produces a stable GitHub release (marked "Latest")
- [ ] Release archives include `README.md`, `LICENSE`, and `cmd/kahi/kahi.toml` as example config
- [ ] GoReleaser generates a changelog from conventional commits between tags
- [ ] GoReleaser injects `GoVersion` into ldflags (currently missing, falls back to runtime detection)
- [ ] `kahi version` output shows correct version, commit, date, Go version, and FIPS status for release builds

**Error Handling:**

| Scenario                   | Behavior                                                      |
| -------------------------- | ------------------------------------------------------------- |
| Tag without `v` prefix     | GoReleaser rejects non-semver tags; push fails cleanly        |
| Duplicate tag              | Git rejects duplicate tag push; no partial release            |
| Changelog generation fails | GoReleaser falls back to commit list; release still publishes |

**Dependencies:** None

---

### INFRA-010: FIPS-Everywhere Build Simplification

**Description:** Eliminate the separate `kahi-fips` build variant. All binaries are built with `GOFIPS140=v1.0.0` -- every release is FIPS 140 compliant. This simplifies the build matrix, release artifacts, and user experience (no choice to make, no confusion). Fixes the existing bug where `GOFIPS140` was set globally in `release.yml` affecting both builds inconsistently.

**Acceptance Criteria:**

- [ ] `.goreleaser.yml` has a single build ID (`kahi`), not two (`kahi` + `kahi-fips`)
- [ ] All builds set `GOFIPS140=v1.0.0` in the build env
- [ ] `kahi version` reports `fips: true` for all release binaries
- [ ] Archive naming uses `kahi_VERSION_OS_ARCH` (no `-fips` suffix)
- [ ] `release.yml` does not set `GOFIPS140` at the workflow level (it is in `.goreleaser.yml` per-build)
- [ ] `FIPS=true` is injected via ldflags for all builds
- [ ] README documents that all binaries are FIPS 140 compliant

**Error Handling:**

| Scenario                              | Behavior                                                           |
| ------------------------------------- | ------------------------------------------------------------------ |
| Go version does not support GOFIPS140 | Build fails at compile time with clear error                       |
| FIPS module validation fails          | Go runtime panics at startup; documented in README troubleshooting |

**Dependencies:** None

---

### INFRA-011: Extended Architecture Support

**Description:** Expand release build targets beyond amd64/arm64 to cover enterprise and embedded platforms. Add linux/arm (v7), linux/riscv64, linux/ppc64le, and linux/s390x. macOS remains amd64/arm64 only (Apple does not support other architectures).

**Build matrix:**

| GOOS   | GOARCH  | Description                              |
| ------ | ------- | ---------------------------------------- |
| linux  | amd64   | 64-bit x86 (Intel/AMD)                   |
| linux  | arm64   | 64-bit ARM (AArch64)                     |
| linux  | arm     | 32-bit ARM v7 (Raspberry Pi, embedded)   |
| linux  | riscv64 | 64-bit RISC-V                            |
| linux  | ppc64le | 64-bit PowerPC little-endian (IBM POWER) |
| linux  | s390x   | 64-bit IBM System z (mainframes)         |
| darwin | amd64   | macOS Intel                              |
| darwin | arm64   | macOS Apple Silicon                      |

**Acceptance Criteria:**

- [ ] `.goreleaser.yml` builds for all 8 targets listed above
- [ ] `linux/arm` build sets `GOARM=7` for ARMv7 compatibility
- [ ] All 8 archives appear in a GitHub release
- [ ] SHA-256 checksums cover all 8 archives
- [ ] `goreleaser check` passes with the expanded config
- [ ] Cross-compilation succeeds for all targets (`goreleaser build --snapshot --clean`)

**Error Handling:**

| Scenario                                   | Behavior                                                      |
| ------------------------------------------ | ------------------------------------------------------------- |
| GOFIPS140 not supported on an architecture | Build excluded for that target; documented in release notes   |
| Cross-compilation failure for exotic arch  | CI fails; investigate Go version support for that GOOS/GOARCH |

**Dependencies:** INFRA-010

---

### INFRA-012: OCI Multi-Arch Container Images

**Description:** Build and publish multi-architecture OCI container images to `ghcr.io/kahiteam/kahi`. Uses a minimal `scratch`-based Dockerfile with the pre-built static binary. Multi-arch manifest covers all linux architectures from INFRA-011. Built with buildx (compatible with both Docker and Podman). All container images are FIPS 140 compliant (same binary as release tarballs).

**Container specification:**

- Base image: `scratch` (zero-dependency, smallest possible)
- Binary: `/kahi` (static, CGO_ENABLED=0, FIPS)
- CA certificates: copied from builder stage
- User: `65534:65534` (nobody)
- Entrypoint: `["/kahi", "daemon"]`
- Exposed port: none (Unix socket default; TCP optional via config)

**Tagging strategy:**

- Stable release `v1.0.0`: tags `:1.0.0`, `:1.0`, `:1`, `:latest`
- Pre-release `v1.0.0-alpha.1`: tag `:1.0.0-alpha.1` only (never `:latest`)

**Acceptance Criteria:**

- [ ] `Dockerfile` exists at repo root using multi-stage build (`scratch` final stage)
- [ ] Dockerfile copies CA certificates from a builder stage
- [ ] Dockerfile sets `USER 65534:65534` and `ENTRYPOINT ["/kahi", "daemon"]`
- [ ] `release.yml` docker job pushes to `ghcr.io/kahiteam/kahi` (not `schwichtgit`)
- [ ] Multi-arch manifest includes: `linux/amd64`, `linux/arm64`, `linux/arm/v7`, `linux/riscv64`, `linux/ppc64le`, `linux/s390x`
- [ ] Pre-release tags do NOT receive `:latest` Docker tag
- [ ] Stable releases receive `:VERSION`, `:MAJOR.MINOR`, `:MAJOR`, and `:latest` tags
- [ ] `docker pull ghcr.io/kahiteam/kahi:latest` works and selects the correct platform
- [ ] Container runs as non-root (UID 65534)
- [ ] Container image size is under 20MB (static binary + CA certs only)

**Error Handling:**

| Scenario                                 | Behavior                                                                    |
| ---------------------------------------- | --------------------------------------------------------------------------- |
| GHCR authentication fails                | Release workflow fails at login step; GITHUB_TOKEN must have packages:write |
| buildx not available                     | Setup step installs buildx; fails early if unavailable                      |
| Architecture not supported by base image | `scratch` supports all architectures; no issue                              |
| CA certificates missing                  | TLS connections fail; Dockerfile must copy certs from builder               |

**Dependencies:** INFRA-010, INFRA-011

---

### INFRA-013: Release Pipeline Hardening

**Description:** Fix known bugs and gaps in the release workflow. Consolidate Docker build into the release pipeline. Remove duplicate commit-standards workflow. Add GoVersion to GoReleaser ldflags.

**Known issues to fix:**

1. Docker registry references `schwichtgit` instead of `kahiteam`
2. `GOFIPS140` set at workflow level instead of per-build in GoReleaser
3. `GoVersion` not injected by GoReleaser ldflags (falls back to runtime detection)
4. No archive extras (README, LICENSE, example config not included in tarballs)
5. Duplicate `commit-standards.yml` workflow (already covered by `ci.yml`)

**Acceptance Criteria:**

- [ ] `release.yml` Docker job references `ghcr.io/kahiteam/kahi`
- [ ] `release.yml` does not set `GOFIPS140` at workflow env level
- [ ] GoReleaser ldflags include `-X ...version.GoVersion={{.Env.GOVERSION}}`
- [ ] Duplicate `commit-standards.yml` workflow is removed (logic lives in `ci.yml`)
- [ ] Release workflow sets `permissions: packages: write` for GHCR push
- [ ] A test release via `goreleaser build --snapshot --clean` succeeds locally

**Error Handling:**

| Scenario                        | Behavior                                                                           |
| ------------------------------- | ---------------------------------------------------------------------------------- |
| GOVERSION env var not set       | GoReleaser sets it to empty string; `kahi version` falls back to runtime detection |
| Removed workflow still triggers | GitHub caches workflow files; merge to main clears stale workflows                 |

**Dependencies:** INFRA-009, INFRA-010

---

### INFRA-014: OCI 1.1 Attestations (Superseded)

**Status:** Superseded by SEC-003, SEC-004, SEC-005, SEC-006, SEC-007, SEC-008.

Supply-chain signing and SBOM work is now tracked concretely under the Security Features section. See SEC-005 (container signing), SEC-006 (container SBOM + provenance attestations), SEC-003 (GoReleaser archive signing), SEC-004 (GoReleaser CycloneDX SBOM), SEC-007 (verify gate), SEC-008 (verification docs).

**Dependencies:** INFRA-012

---

### INFRA-015: Security Policy and Vulnerability Reporting

**Description:** Create a SECURITY.md with supported versions, vulnerability reporting instructions, response SLA, and scope. Enable GitHub private vulnerability reporting and secret scanning with push protection on the repository.

**Acceptance Criteria:**

- [ ] `SECURITY.md` exists at repo root
- [ ] SECURITY.md lists supported versions table (latest minor of each major)
- [ ] SECURITY.md directs reporters to GitHub private vulnerability reporting as primary channel
- [ ] SECURITY.md lists `schwicht+kahi+security@gmail.com` as email fallback
- [ ] SECURITY.md states 48-hour acknowledgment and 90-day fix SLA
- [ ] SECURITY.md defines scope (what is and is not a security issue)
- [ ] SECURITY.md describes coordinated disclosure policy
- [ ] GitHub private vulnerability reporting is enabled on the repository
- [ ] GitHub secret scanning is enabled on the repository
- [ ] GitHub secret scanning push protection is enabled on the repository

**Error Handling:**

| Scenario                              | Behavior                                                          |
| ------------------------------------- | ----------------------------------------------------------------- |
| Reporter uses email instead of GitHub | Same SLA applies; maintainer creates private advisory manually    |
| Secret scanning false positive        | Maintainer dismisses alert with reason; does not disable scanning |

**Dependencies:** None

---

### INFRA-016: Code of Conduct

**Description:** Adopt the Contributor Covenant v2.1 as the project code of conduct. This is the industry standard recognized by GitHub's community profile checklist.

**Acceptance Criteria:**

- [ ] `CODE_OF_CONDUCT.md` exists at repo root
- [ ] Document is Contributor Covenant v2.1 (full text, not a link)
- [ ] Contact method is `schwicht+kahi+security@gmail.com`
- [ ] Document contains no emoji
- [ ] GitHub community profile shows Code of Conduct as complete

**Error Handling:**

| Scenario                   | Behavior                                                  |
| -------------------------- | --------------------------------------------------------- |
| Conduct violation reported | Follow enforcement guidelines in the Contributor Covenant |

**Dependencies:** None

---

### INFRA-017: Contributing Guide

**Description:** Create a CONTRIBUTING.md covering development setup, branching model, commit conventions, PR process, code style requirements, and testing expectations. Must align with the project constitution.

**Acceptance Criteria:**

- [ ] `CONTRIBUTING.md` exists at repo root
- [ ] Document covers: prerequisites (Go 1.26+, Task), getting started (clone, init.sh, task test)
- [ ] Document covers: branching model (feature branches off main, PR required)
- [ ] Document covers: conventional commit format with examples
- [ ] Document covers: PR requirements (tests pass, lint clean, description)
- [ ] Document covers: code style (gofmt, golangci-lint, go vet)
- [ ] Document covers: testing requirements (85% coverage, unit + integration)
- [ ] Document references SECURITY.md for vulnerability reports
- [ ] Document references CODE_OF_CONDUCT.md
- [ ] Document contains no emoji or AI-isms

**Error Handling:**

| Scenario                               | Behavior                             |
| -------------------------------------- | ------------------------------------ |
| Contributor skips conventional commits | CI commit-standards job fails the PR |
| Coverage drops below 85%               | CI coverage check fails the PR       |

**Dependencies:** INFRA-015, INFRA-016

---

### INFRA-018: Issue Templates

**Description:** Create YAML-based GitHub issue templates for bug reports and feature requests. Use structured forms (not freeform markdown) to ensure reporters provide required information. Disable blank issues.

**Acceptance Criteria:**

- [ ] `.github/ISSUE_TEMPLATE/bug_report.yml` exists with required fields: Go version, OS/arch, kahi version, steps to reproduce, expected behavior, actual behavior
- [ ] `.github/ISSUE_TEMPLATE/feature_request.yml` exists with required fields: problem statement, proposed solution, alternatives considered
- [ ] `.github/ISSUE_TEMPLATE/config.yml` exists with `blank_issues_enabled: false`
- [ ] Bug report template has `labels: ["bug"]` and feature request has `labels: ["enhancement"]`
- [ ] All templates render correctly in GitHub's new issue picker
- [ ] Templates contain no emoji

**Error Handling:**

| Scenario                                               | Behavior                                                         |
| ------------------------------------------------------ | ---------------------------------------------------------------- |
| User needs to file an issue that doesn't fit templates | config.yml provides contact links (security policy, discussions) |
| Template YAML is invalid                               | GitHub falls back to freeform; CI should lint templates          |

**Dependencies:** None

---

### INFRA-019: PR Test Result Visibility

**Description:** Surface test results (pass/fail/skip counts, coverage percentage) directly on pull request pages. Currently, CI produces JUnit XML via gotestsum and computes coverage inside `task coverage`, but none of this data is visible on the PR without clicking into individual job logs. `dorny/test-reporter@v2` is configured but produces no check runs on PRs.

This feature adds two complementary mechanisms:

1. **Job summaries** (`$GITHUB_STEP_SUMMARY`) -- Markdown tables written to the GitHub Actions job summary showing test counts and coverage percentage for each test suite (unit, integration, e2e).
2. **Test reporter check runs** -- Debug and fix `dorny/test-reporter@v2` so it produces separate check runs ("Unit Test Results", "Integration Test Results", "E2E Test Results") with per-test annotations on the PR.
3. **Coverage status check** -- A dedicated commit status (not just a job step) showing the coverage percentage in the PR checks list.

**Acceptance Criteria:**

- [ ] `ci.yml` golang job writes a step summary table with: total tests, passed, failed, skipped, and coverage % parsed from `coverage.out`
- [ ] `integration.yml` integration job writes a step summary table with: total tests, passed, failed, skipped for both integration and e2e suites
- [ ] Coverage percentage is posted as a commit status via `gh api` with context `coverage` and description showing the percentage (e.g., "87.3% (threshold: 85%)")
- [ ] `dorny/test-reporter@v2` produces a separate check run named "Unit Test Results" on PR commits (visible in the PR checks list)
- [ ] `dorny/test-reporter@v2` produces separate check runs named "Integration Test Results" and "E2E Test Results" on PR commits
- [ ] Job summaries parse JUnit XML using standard shell tools (no new binary dependencies)
- [ ] All summary data is derived from existing artifacts: `unit-results.xml`, `integration-results.xml`, `e2e-results.xml`, `coverage.out`

**Error Handling:**

| Scenario                                                                | Behavior                                                                                                                 |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| JUnit XML file missing (e.g., test step failed before producing output) | Summary step runs with `if: always()` and reports "no results file found"                                                |
| Coverage below threshold                                                | Coverage status check posts as `failure` state with the percentage; the existing `task coverage` step also fails the job |
| `dorny/test-reporter` fails to create check run                         | Job still succeeds (fail-on-error: false already set); summary tables provide fallback visibility                        |
| No Go files changed (golang job skipped)                                | Summary job reports "skipped" for test results; no coverage status posted                                                |

**Edge Cases:**

- `gotestsum --junitfile` always produces valid XML even when tests fail (exit code is non-zero but XML is written)
- Coverage step summary must handle both the `task coverage` path (which runs tests and produces `coverage.out`) and the `task test` path (which does not produce coverage)
- The `dorny/test-reporter` issue is likely caused by the action creating check runs on the wrong commit SHA or the `GITHUB_TOKEN` lacking sufficient scope for the check-run API on PR events -- investigate `token` and `sha` input parameters
- Commit status for coverage uses `gh api /repos/{owner}/{repo}/statuses/{sha}` which requires `statuses: write` permission

**Dependencies:** INFRA-002

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

| Error Condition                    | Expected Behavior                 | User-Facing Message                            |
| ---------------------------------- | --------------------------------- | ---------------------------------------------- |
| Invalid state transition attempted | Transition rejected, error logged | "cannot transition from {current} to {target}" |
| State query for unknown process    | Return error                      | "no such process: {name}"                      |

**Edge Cases:**

- A process that exits on its own during STOPPING ends STOPPED, not EXITED: a stop was requested, so the exit completes it (as in supervisord)
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

| Error Condition          | Expected Behavior                                                      | User-Facing Message                             |
| ------------------------ | ---------------------------------------------------------------------- | ----------------------------------------------- |
| Command binary not found | Process goes to FATAL, spawn error recorded                            | "spawn error: {command}: no such file"          |
| Command not executable   | Process goes to FATAL                                                  | "spawn error: {command}: permission denied"     |
| Process already running  | Reject start, return error                                             | "process already started: {name}"               |
| Directory does not exist | Spawn error: BACKOFF, then FATAL after startretries retries (FUNC-067) | "spawn error: directory does not exist: {path}" |

**Edge Cases:**

- Binary path is resolved via PATH lookup at exec time
- File descriptor inheritance is limited to stdin/stdout/stderr pipes only
- If pipe creation fails, process goes to FATAL with spawn error

**Dependencies:** FUNC-001, INFRA-006

---

### FUNC-003: Process Stop

**Description:** Stop a running process with configurable signal, wait period, and SIGKILL escalation.

**Acceptance Criteria:**

- **Given** a process in RUNNING state with stopsignal=SIGTERM and stopwaitsecs=10
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

| Error Condition                         | Expected Behavior            | User-Facing Message                                    |
| --------------------------------------- | ---------------------------- | ------------------------------------------------------ |
| Process not running                     | Return error                 | "process not running: {name}"                          |
| Signal send fails (ESRCH)               | Log warning, treat as exited | "process {name} already gone"                          |
| killasgroup=false with stopasgroup=true | Config validation error      | "killasgroup cannot be false when stopasgroup is true" |

**Edge Cases:**

- Process that exits during the stop signal send (race condition) must not cause errors
- Multiple stop commands on the same process are idempotent (second is ignored)
- stopwaitsecs must be >= 1: 0 is a config error, because it would SIGKILL right after the stop signal (#138). For an immediate kill, set `stopsignal = "SIGKILL"`. `kahi migrate` writes a supervisord `stopwaitsecs = 0` as 1, with a warning.

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

| Error Condition                     | Expected Behavior       | User-Facing Message                              |
| ----------------------------------- | ----------------------- | ------------------------------------------------ |
| Invalid autorestart value in config | Config validation error | "autorestart must be true, false, or unexpected" |

**Edge Cases:**

- Autorestart applies only after process has reached RUNNING state (not during STARTING/BACKOFF)
- Process manually stopped (via stop command) is never autorestarted regardless of setting
- Autorestart during shutdown is suppressed
- `autorestart` accepts the TOML booleans `true` and `false` as well as the strings `"true"`, `"false"` and `"unexpected"`; a value of any other type is a config error naming the key

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

| Error Condition | Expected Behavior                                    | User-Facing Message                           |
| --------------- | ---------------------------------------------------- | --------------------------------------------- |
| startretries=0  | Process goes directly to FATAL on first failed start | "entered FATAL state, too many start retries" |

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

| Error Condition                | Expected Behavior   | User-Facing Message         |
| ------------------------------ | ------------------- | --------------------------- |
| waitpid returns unexpected PID | Log warning, ignore | "reaped unknown pid {pid}"  |
| waitpid returns ECHILD         | Stop reaping loop   | (internal, no user message) |

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
  **When** process_name is set and does not contain %(process_num)
  **Then** config validation fails

- **Given** numprocs > 1
  **When** process_name is unset
  **Then** instances are named `<program>_<n>` (for example web_0, web_1)

**Error Handling:**

| Error Condition                                  | Expected Behavior       | User-Facing Message              |
| ------------------------------------------------ | ----------------------- | -------------------------------- |
| numprocs < 1                                     | Config validation error | "numprocs must be >= 1"          |
| Duplicate process names after template expansion | Config validation error | "duplicate process name: {name}" |

**Edge Cases:**

- numprocs=1 with no %(process_num) in process_name is valid (default behavior)
- Process names must be unique across all programs, included files too; this is checked at startup and on every reload
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

- **Given** programs m1 (priority 900) and m2 (priority 800) in `[groups.g]` with priority 1, and a program solo (priority 5)
  **When** "start all" is issued
  **Then** they start in order m2, m1, solo, and "stop all" stops them in order solo, m1, m2: a program in a `[groups]` table is ordered by the group's priority first, then by its own, then by name (#196)

**Error Handling:**

| Error Condition        | Expected Behavior       | User-Facing Message                                                   |
| ---------------------- | ----------------------- | --------------------------------------------------------------------- |
| Priority outside 0-999 | Config validation error | "priority must be between 0 and 999" (programs and `[groups]` tables) |

**Edge Cases:**

- Priority ordering applies to group-level operations as well
- One ordering key serves every ordered action (daemon start and shutdown, group start and stop, the bulk API): the `[groups]` table's priority if the program is in one, else its own priority; then its own priority; then its name
- An unset `[groups]` priority is 999, like a program's
- Shutdown uses the same reverse-priority ordering

**Dependencies:** FUNC-002

---

### FUNC-009: Homogeneous Process Groups

**Description:** Each program definition implicitly creates a group with the same name containing all its instances.

**Acceptance Criteria:**

- **Given** a [programs.web] section with numprocs=2
  **When** config is loaded
  **Then** a group named "web" exists containing processes web_0 and web_1 (the default process_name is `{program}_{n}`)

- **Given** a group "web"
  **When** "kahi ctl start web:\*" is issued
  **Then** all processes in the web group are started

**Error Handling:**

| Error Condition                | Expected Behavior | User-Facing Message     |
| ------------------------------ | ----------------- | ----------------------- |
| Reference to nonexistent group | Return error      | "no such group: {name}" |

**Edge Cases:**

- A program with numprocs=1 creates a group with a single process
- A `[groups.x]` table whose name equals a program's name is a config error, "groups.{x}: name collides with program {x}" (#214); two programs cannot share a name (TOML keys); an include that redefines a program is refused with `duplicate program name "{name}": defined in both main config and {include path}`
- Through the CLI, `kahi ctl start nope:*` reports "no such group: nope" and exits 1 (FUNC-031)

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

| Error Condition                           | Expected Behavior       | User-Facing Message                                                              |
| ----------------------------------------- | ----------------------- | -------------------------------------------------------------------------------- |
| Group references nonexistent program      | Config validation error | "groups.{name}: references nonexistent program \"{prog}\""                       |
| Program already in another explicit group | Config validation error | "program \"{prog}\" is in multiple groups: {a} and {b}" ({a} before {b}, sorted) |
| Group with no programs                    | Config validation error | "groups.{name}: empty group (no programs)"                                       |

**Edge Cases:**

- When a heterogeneous group exists, the implicit homogeneous groups for its member programs are suppressed: the table owns its members (decided 2026-10-29, #212)
- `{name}:*` resolves a group first and otherwise the program of that name, so `web:*` still targets the processes of a member program `web` and `services:*` targets the whole table (decided 2026-10-29, so existing scripts keep working). Single members are also reachable by process name (`web_0`)
- `kahi ctl add|remove` act on groups only: for a member program they fail with "{prog} is in group {table}; use {table}" and exit 1 (404 NOT_FOUND from the API)
- Status, the API and state events report a member's `group` as the table name ("services"); `%(group_name)s` in config expansion stays the program name (FUNC-012)
- A reload that drops a table whose programs stay in `[programs]` stops and removes the table's group (PROCESS_GROUP_REMOVED) and adds each program as its own group (PROCESS_GROUP_ADDED; autostart applies, so a running member with `autostart = false` stays stopped after the split), as supervisord does. A reload that adds a table over active programs removes their own groups and adds the table the same way. Changing a table's member list does both: a member that leaves gets its own group, one that joins leaves its own group
- `reread`, `update` and `reload` list an added, changed or removed table by its name under added/changed/removed
- Empty groups (no programs) are rejected at config validation

**Dependencies:** FUNC-009

---

### FUNC-011: Group Lifecycle Management

**Description:** Add and remove process groups at runtime via API/CLI, with supervisorctl semantics (decided 2026-10-29, #212).

**Acceptance Criteria:**

- **Given** a new program added to the config file
  **When** "kahi ctl update" is issued
  **Then** the new group is added and its autostart processes begin

- **Given** a group with all processes in STOPPED state
  **When** "kahi ctl remove {group}" is issued
  **Then** the group is removed from active management

- **Given** a group with running processes
  **When** "kahi ctl remove {group}" is issued
  **Then** the command fails with "group {group} has running processes, stop first" and exit code 1; nothing changes

- **Given** a group defined in config that is not active (removed earlier)
  **When** "kahi ctl add {group}" is issued
  **Then** the group is active again with its processes loaded from the current config, its autostart processes start, and PROCESS_GROUP_ADDED is published (FUNC-053)

**Error Handling:**

| Error Condition                              | Expected Behavior  | User-Facing Message                              |
| -------------------------------------------- | ------------------ | ------------------------------------------------ |
| Add already-active group                     | Return error       | "group already active: {name}"                   |
| Remove group with running processes          | Return error       | "group {name} has running processes, stop first" |
| Remove nonexistent group                     | Return error       | "no such group: {name}"                          |
| Add or remove a member of a `[groups]` table | Return error (404) | "{prog} is in group {table}; use {table}"        |
| Add or remove during shutdown                | 409 SHUTTING_DOWN  | "daemon is shutting down"                        |

**Edge Cases:**

- Removing a group does not delete its config; it just deactivates it
- `kahi ctl remove --stop {group}` stops the group, waits up to each process's stopwaitsecs plus margin, then removes it; a process that does not stop fails it with "group {group}: process {name} did not stop" (409). `kahi ctl add --reload {group}` loads the config file first, as `kahi ctl update` does, then adds the group (one the reload added counts as added). Without the flags ctl appends a hint to the two errors: "(or use kahi ctl remove --stop)" and "(not in the loaded config; run kahi ctl update, or kahi ctl add --reload)" (decided 2026-11-11, so scripts written for the old commands have a direct replacement)
- `kahi ctl add` uses the config the daemon last loaded (at startup or by the last reload); it does not read the config file. Edits not yet reloaded take effect with the next reload or `kahi ctl update` (ADR-034)
- "Stopped" means every process of the group is STOPPED, EXITED or FATAL; STARTING, RUNNING, BACKOFF and STOPPING count as running
- A removed group's processes leave status and the API until the group is added again. A reload, `kahi ctl update` or `kahi ctl add` brings it back; a reload reports it under "added"
- A `[groups]` table owns its members (FUNC-010): `kahi ctl add|remove {table}` adds or removes all member processes; a member program name fails with "{prog} is in group {table}; use {table}"
- A reload that drops a `[groups]` table whose programs stay splits it into program groups, and one that adds a table merges them (FUNC-010); one that drops the table and its programs removes the processes. Each table add or remove publishes one group event listing the table's processes (FUNC-053)
- A reload drops an inactive group that the config no longer defines; it is no longer reported anywhere
- API: `POST /api/v1/groups/{name}/add` and `POST /api/v1/groups/{name}/remove`; errors are 409 CONFLICT (already active, has running processes) and 404 NOT_FOUND (no such group). `kahi ctl add` and `kahi ctl remove` use them and print "{group}: added" / "{group}: removed"
- Adding a group that the config does not define is "no such group: {name}"

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

| Error Condition                | Expected Behavior | User-Facing Message                       |
| ------------------------------ | ----------------- | ----------------------------------------- |
| Undefined environment variable | Config error      | "undefined environment variable: ${name}" |
| Unknown template variable      | Config error      | "unknown template variable: %({name})s"   |

**Edge Cases:**

- Recursive expansion is not supported (a variable cannot reference another variable)
- Literal `%` and `$` can be escaped as `%%` and `$$`
- Expansion happens at config load time, not at process start time
- `%(group_name)s` is the program's own group, which is the program name, even when the program is also listed in an explicit `[groups.x]` table. This matches supervisord, which expands it in the program section before any regrouping (#167).

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

| Error Condition                                                      | Expected Behavior      | User-Facing Message                                                                                      |
| -------------------------------------------------------------------- | ---------------------- | -------------------------------------------------------------------------------------------------------- |
| Included file has syntax error                                       | Config load fails      | "include {file}: config parse error in {file}: {parse_error}"                                            |
| Circular include (a file includes one that includes it)              | Config load fails      | "circular include detected: {file}"                                                                      |
| Include glob matches no files                                        | Warning logged         | "include pattern \"{pattern}\" matched no files"                                                         |
| Two files define the same program                                    | Config load fails      | "duplicate program name \"{name}\": defined in both {file} and {file}" ("main config" for the main file) |
| A file is matched again (second pattern, or included from two files) | Merged once, warning   | "include {file} is matched more than once; merged once"                                                  |
| An included file has [supervisor] or [server.*]                      | Table ignored, warning | "{file}: [supervisor] in an included file is ignored; set it in the main config" (#136)                  |

**Edge Cases:**

- Included files can define programs, groups and webhooks but not override [supervisor] or [server.*] settings; those tables are ignored with a warning (#136)
- A [groups] table in a later file replaces one of the same name from an earlier file
- Duplicate program names across included files are caught at load, naming both files
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

| Error Condition                      | Expected Behavior                         | User-Facing Message                        |
| ------------------------------------ | ----------------------------------------- | ------------------------------------------ |
| Reloaded config has syntax error     | Reload rejected, existing config retained | "config reload failed: {error}"            |
| Reloaded config has validation error | Reload rejected                           | "config reload failed: {validation_error}" |

**Edge Cases:**

- Programs not changed in the reload are untouched (processes keep running)
- Reload during shutdown is ignored
- Concurrent reload requests are serialized (second waits for first to complete)
- A numprocs change is spawn-affecting: the program is recreated, so growing 2 -> 4 runs four instances and restarts the original two (#119, #136)
- During config reload of a program, stop/start commands targeting that program are queued and executed after reload completes
- If reload is rejected due to validation error, any in-flight restart completes and previous config is retained

**Dependencies:** FUNC-012, FUNC-013, FUNC-011

---

### FUNC-015: Config File Search Paths

**Description:** Search predefined paths for config file when not explicitly specified.

**Acceptance Criteria:**

- **Given** no `-c` flag and no KAHI_CONFIG
  **When** the daemon starts
  **Then** it searches in order: `/etc/kahi/kahi.toml`, `/etc/kahi.toml` (no `./kahi.toml`, SEC-017)

- **Given** no `-c` flag and no KAHI_CONFIG
  **When** `kahi ctl` starts
  **Then** it searches in order: `./kahi.toml`, `/etc/kahi/kahi.toml`, `/etc/kahi.toml`

- **Given** a `-c /path/to/config.toml` flag
  **When** Kahi starts
  **Then** only the specified path is used

- **Given** no config found in any search path
  **When** Kahi starts
  **Then** it exits with error code 1

**Error Handling:**

| Error Condition          | Expected Behavior | User-Facing Message                                                     |
| ------------------------ | ----------------- | ----------------------------------------------------------------------- |
| No config file found     | Exit code 1       | "no config file found; searched [{paths}]" (the daemon's list, SEC-017) |
| Config file not readable | Exit code 1       | "cannot read config: {path}: {error}"                                   |

**Edge Cases:**

- Symlinks to config files are followed
- Config file specified via KAHI_CONFIG environment variable takes precedence over search paths; `-c` takes precedence over KAHI_CONFIG. A KAHI_CONFIG that names no file is an error, not a fall-back to the search paths: "cannot read config: {path}: {error}", exit 1 (the same message as a `-c` path that does not exist)
- The daemon's search order leaves out `./kahi.toml` (SEC-017); `kahi ctl` keeps it, to find the socket

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

| Error Condition            | Expected Behavior   | User-Facing Message                                      |
| -------------------------- | ------------------- | -------------------------------------------------------- |
| Output file already exists | Refuse to overwrite | "file already exists: {path} (use --force to overwrite)" |
| Output path not writable   | Exit with error     | "cannot write to {path}: {error}"                        |

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
  **Then** daemon logs are written to the specified file instead of stdout (decided 2026-10-28: a container leaves logfile unset and reads stdout; journald would otherwise get every line twice)

**Error Handling:**

| Error Condition            | Expected Behavior       | User-Facing Message                                                                          |
| -------------------------- | ----------------------- | -------------------------------------------------------------------------------------------- |
| Log file path not writable | Exit with error         | "cannot open log file: {path}: {error}"                                                      |
| Invalid log level          | Config validation error | "supervisor.log_level: invalid log level: \"{level}\" (must be debug, info, warn, or error)" |
| Invalid log format         | Config validation error | "supervisor.log_format: invalid log format: \"{format}\" (must be json or text)"             |

**Edge Cases:**

- Log file is opened with append mode
- Log level change on config reload takes effect immediately, logged as "log level changed" with from and to
- A log_format or logfile change on reload is logged as needing a restart ("supervisor.log_format and supervisor.logfile changes take effect after restart")

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

| Error Condition            | Expected Behavior     | User-Facing Message                        |
| -------------------------- | --------------------- | ------------------------------------------ |
| Log file path not writable | Process goes to FATAL | "cannot open log file for {name}: {error}" |

**Edge Cases:**

- Output is line-buffered when forwarding to container stdout
- Large output bursts (>64KB/s) must not block the child process (non-blocking pipe reads)
- Pipe is closed on process exit; remaining buffered data is flushed
- Each stream is forwarded only when it has no log file. stdout goes to Kahi's stdout and stderr to Kahi's stderr. With redirect_stderr, stderr lines are part of the stdout stream ("stream":"stdout"). Lines are split per pipe, so output from the two streams never mixes within a line. A line over 64 KB without a newline is forwarded in 64 KB pieces.
- Forwarding applies in the foreground only: `--daemon` redirects Kahi's stdout and stderr to /dev/null, so a daemonized Kahi needs log files
- A log file that cannot be opened fails the start like a spawn error: BACKOFF, then FATAL after startretries, with the message shown in status. Each start retries the file, so the program starts once the path becomes writable; until then its output stays in the in-memory buffer only

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

| Error Condition       | Expected Behavior                           | User-Facing Message                       |
| --------------------- | ------------------------------------------- | ----------------------------------------- |
| Rotation rename fails | Log error, continue writing to current file | "log rotation failed for {name}: {error}" |

**Edge Cases:**

- Rotation is atomic (rename, not copy+truncate)
- Rotation check happens before each write; if adding this write would exceed threshold, rotate first then write
- If a single write is larger than maxbytes, the write is allowed (do not split writes); rotation happens on the next write
- Backup file numbering: `.1` -> `.2`, `.2` -> `.3`, etc.; `.N` where N > backup_count is deleted
- Byte size parsing supports: B, KB, MB, GB suffixes

**Dependencies:** FUNC-018

---

### FUNC-020: Syslog Forwarding

**Description:** Optionally forward process stdout/stderr to syslog, one message per output line. Planned for v0.3.0 (#152); until then `stdout_syslog` and `stderr_syslog` are accepted and ignored with a warning.

**Configuration:**

```toml
[supervisor]
syslog_address = ""          # "" = the local syslog socket; "unix:///path", "udp://host:port" or "tcp://host:port"

[programs.web]
stdout_syslog = true
stderr_syslog = false
syslog_facility = "LOCAL0"   # KERN..LOCAL7 names as in syslog(3), case-insensitive
syslog_priority = "INFO"     # EMERG, ALERT, CRIT, ERR, WARNING, NOTICE, INFO, DEBUG
```

**Acceptance Criteria:**

- **Given** `stdout_syslog = true` in process config
  **When** the child writes to stdout
  **Then** each line is sent to syslog with facility LOCAL0, priority INFO and the process name (`web`, or `web_0` for a numprocs member) as tag

- **Given** syslog forwarding enabled
  **When** output contains multi-line messages
  **Then** each line is sent as a separate syslog message, without its trailing newline

- **Given** `syslog_address = "unix:///tmp/test.sock"` and a datagram listener bound there
  **When** a process with `stdout_syslog = true` writes "hello\n"
  **Then** the listener receives one message whose body ends in "hello" (this is the e2e route; no host syslogd is needed)

- **Given** `syslog_facility = "LOCAL3"` and `syslog_priority = "NOTICE"`
  **When** a line is forwarded
  **Then** the message carries facility LOCAL3 and priority NOTICE

**Error Handling:**

| Error Condition                    | Expected Behavior                                                                                                          | User-Facing Message                                                                                                       |
| ---------------------------------- | -------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Syslog connection fails at spawn   | The process starts; the connection reconnects with backoff (1 s doubling to 60 s); WARN once per outage of that connection | "syslog connection failed for {process} {stream}: {error}"                                                                |
| Write to syslog fails during a run | Reconnect with the same backoff, dropping lines while disconnected; capture and files continue; INFO on recovery           | "syslog connection failed for {process} {stream}: {error}"; recovery: "syslog connection restored for {process} {stream}" |
| Unknown facility or priority name  | Config validation error                                                                                                    | "programs.{name}.syslog_facility: unknown facility {value}" / "programs.{name}.syslog_priority: unknown priority {value}" |
| Unparsable `syslog_address`        | Config validation error                                                                                                    | "supervisor.syslog_address: invalid address {value}"                                                                      |

**Edge Cases:**

- Syslog forwarding works alongside file logging, the ring buffer and console passthrough; a syslog failure never blocks or fails the process
- Lines are forwarded after ANSI stripping when `strip_ansi = true`, the same data the log file receives; a partial last line is forwarded when the process exits
- With `redirect_stderr = true`, stderr lines follow `stdout_syslog`; setting `stderr_syslog` as well logs a config warning "programs.{name}.stderr_syslog has no effect with redirect_stderr"
- Both streams use the same facility and priority
- Messages use the RFC 3164 format Go's `log/syslog` writes; TCP is newline-framed; lines over 2048 bytes are split into several messages
- The default local socket is `/dev/log` on Linux and `/var/run/syslog` on macOS
- One connection per process and stream; each has its own goroutine and a bounded queue of 1000 lines that drops the oldest while disconnected or slow, so a slow syslog never blocks the capture writer (FUNC-018); dials and reconnects happen only in that goroutine
- Outage state is tracked per connection (process and stream); each connection logs its own WARN and INFO
- `unix://{path}` dials a datagram socket first and falls back to a stream socket
- A `syslog_address` change on reload takes effect after restart ("supervisor.syslog_address change takes effect after restart"); a per-program syslog key change is a program change (FUNC-014)
- SIGUSR2 (FUNC-022) does not touch syslog connections; forwarding continues without interruption
- `kahi migrate` maps supervisord `stdout_syslog` / `stderr_syslog` to the same keys

**Dependencies:** FUNC-018, FUNC-021, FUNC-022

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
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

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

| Error Condition         | Expected Behavior                     | User-Facing Message                       |
| ----------------------- | ------------------------------------- | ----------------------------------------- |
| File cannot be reopened | Log error, continue without that file | "cannot reopen log file: {path}: {error}" |

**Edge Cases:**

- Log reopen is safe to call concurrently with log writes
- SIGUSR2 during shutdown is ignored

**Dependencies:** FUNC-018

---

### FUNC-023: Log Cleanup on Startup (Withdrawn)

**Status:** Withdrawn 2026-11-14 (#113). supervisord removes stale `AUTO` child logs from its `childlogdir`; Kahi has no `childlogdir` and never names log files itself (every log file is an explicit `stdout_logfile` / `stderr_logfile`), so there is nothing to clean. The ID is kept and not reused.

**Edge Cases:**

- `[supervisor] nocleanup` stays accepted and is ignored with the startup and reload warning "supervisor.nocleanup has no effect: kahi does not auto-name child logs"
- `kahi migrate` drops supervisord `nocleanup` and `childlogdir` with the same note

**Dependencies:** None

---

### FUNC-024: Unix Socket Server

**Description:** Serve the control API over a Unix domain socket.

**Acceptance Criteria:**

- **Given** default config
  **When** Kahi starts
  **Then** a Unix socket is created at the configured path (default: see FUNC-044)

- **Given** `[server.unix] chmod = "0600"` in config
  **When** the socket is created
  **Then** the socket file has permissions 0600

- **Given** `[server.unix] chmod = "0770"` (group or other access) or any `chown`
  **When** Kahi starts
  **Then** startup fails with a config error: the socket is an unauthenticated control channel, so it must be owner-only (SEC-022)

- **Given** the socket file already exists from a previous crashed run
  **When** Kahi starts
  **Then** the stale socket file is removed and a new one is created

**Error Handling:**

| Error Condition                                                      | Expected Behavior       | User-Facing Message                                                                                 |
| -------------------------------------------------------------------- | ----------------------- | --------------------------------------------------------------------------------------------------- |
| Socket directory not writable                                        | Exit with error         | "permission denied: cannot create socket in {dir}: {error}"                                         |
| Socket cannot be bound                                               | Exit with error         | "cannot create socket: {path}: {error}"                                                             |
| Socket path longer than sun_path allows (103 bytes macOS, 107 Linux) | Config validation error | "socket path too long: {path} ({n} bytes, at most {max} on {os}); set a shorter [server.unix] file" |

**Edge Cases:**

- Socket is cleaned up on graceful shutdown
- Socket is not cleaned up on SIGKILL (handled by stale detection on next start)
- chown is not supported: a config that sets it is rejected (SEC-022). Share access through a reverse proxy that authenticates, not through socket ownership

**Dependencies:** INFRA-004

---

### FUNC-025: TCP HTTP Server

**Description:** Optionally serve the API over TCP with HTTP.

**Acceptance Criteria:**

- **Given** `[server.http] enabled = true` and `listen = "127.0.0.1:9876"`
  **When** Kahi starts
  **Then** an HTTP server listens on 127.0.0.1:9876

- **Given** TCP server enabled without username/password
  **When** Kahi starts
  **Then** startup fails with "http listener on {addr} requires username/password" (SEC-015)

- **Given** TCP server with auth configured
  **When** a request carries no or invalid credentials
  **Then** the request is rejected with 401 Unauthorized

- **Given** TCP server with auth configured
  **When** valid Basic Auth credentials are provided
  **Then** the request is processed

**Error Handling:**

| Error Condition        | Expected Behavior       | User-Facing Message                 |
| ---------------------- | ----------------------- | ----------------------------------- |
| Port already in use    | Exit with error         | "cannot bind {address}: {error}"    |
| Listen address invalid | Config validation error | "invalid listen address: {address}" |

**Edge Cases:**

- TCP server requires authentication; Unix socket does not by default
- `listen` must be `host:port` (a numeric port or a service name); an empty or malformed address is a config error, not a bind failure
- Binding to all interfaces (`0.0.0.0`, `::` or an empty host) prints a config warning on stderr ("server.http.listen {address} binds all interfaces; ..."), and the daemon log repeats it when the listener starts
- TLS is not included in initial release but the architecture supports adding it

**Dependencies:** FUNC-024

---

### FUNC-026: REST API Endpoints

**Description:** JSON API for process management, status, and log access.

**Acceptance Criteria:**

- **Given** the API server is running
  **When** `GET /api/v1/processes` is requested
  **Then** a JSON array of all process info objects is returned (name, group, state, statecode, pid, uptime, description, exitstatus, and exitsignal when a signal killed the process, for example `"SIGKILL"` with exitstatus -1)

- **Given** a process named "web"
  **When** `POST /api/v1/processes/web/start` is requested
  **Then** the process is started and 200 is returned with `{"status": "started", "name": "web"}`

- **Given** a process named "web"
  **When** `POST /api/v1/processes/web/stop` is requested
  **Then** the process is stopped and 200 is returned with `{"status": "stopped", "name": "web"}`

- **Given** a process named "web"
  **When** `POST /api/v1/processes/web/restart` is requested
  **Then** the process is stopped then started, and 200 is returned with `{"status": "restarted", "name": "web"}`

- **Given** a process named "web"
  **When** `POST /api/v1/processes/web/signal` with body `{"signal": "HUP"}` (or `"SIGHUP"`) is requested
  **Then** SIGHUP is sent to the process, and 200 is returned with `{"status": "signaled", "name": "web", "signal": "SIGHUP"}`

- **Given** the API server
  **When** `GET /api/v1/processes/web/log/stdout?length=1000` is requested
  **Then** the last 1000 bytes of the in-memory stdout buffer are returned as `text/plain` (`length` defaults to 1600; a larger value than the buffer holds returns the whole buffer)

- **Given** the API server
  **When** `POST /api/v1/config/reload` is requested
  **Then** config is re-read and `{"status": "reloaded", "added": [...], "changed": [...], "removed": [...], "warnings": [...]}` is returned; with `?dry_run=true` nothing is applied and the status is `"preview"`

- **Given** the API server
  **When** `POST /api/v1/shutdown` is requested
  **Then** graceful shutdown is initiated and 200 is returned with `{"status": "shutting_down"}`

- **Given** the API server
  **When** `GET /api/v1/config` is requested
  **Then** all process config info is returned

**Error Handling:**

Every error is JSON, `{"error": "...", "code": "..."}`, with `Content-Type: application/json`. Successful responses are JSON too, except the log (`text/plain`) and the SSE streams (`text/event-stream`).

| Error Condition             | Expected Behavior               | User-Facing Message                                                       |
| --------------------------- | ------------------------------- | ------------------------------------------------------------------------- |
| Unknown process name        | 404 with JSON error             | `{"error": "no such process: web", "code": "NOT_FOUND"}`                  |
| Invalid signal name         | 400 with JSON error             | `{"error": "process web: invalid signal \"FOO\"", "code": "BAD_REQUEST"}` |
| Method not allowed          | 405 with JSON error and `Allow` | `{"error": "method not allowed", "code": "METHOD_NOT_ALLOWED"}`           |
| Unknown route under `/api/` | 404 with JSON error             | `{"error": "not found", "code": "NOT_FOUND"}`                             |
| Server in shutdown state    | 409 with JSON error             | `{"error": "daemon is shutting down", "code": "SHUTTING_DOWN"}`           |

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
  **When** `GET /api/v1/events/stream?types=PROCESS_STATE_RUNNING,PROCESS_STATE_EXITED` is requested
  **Then** only events of the named types are streamed, each as `event: {TYPE}` and `data: {JSON}`; types are exact event type names, and an unknown name matches nothing

- **Given** an SSE client disconnects
  **When** the connection drops
  **Then** the server cleans up the subscription (no resource leak)

**Error Handling:**

| Error Condition                                                               | Expected Behavior        | User-Facing Message                                                    |
| ----------------------------------------------------------------------------- | ------------------------ | ---------------------------------------------------------------------- |
| Process not found                                                             | 404 before stream starts | `{"error": "no such process: web", "code": "NOT_FOUND"}`               |
| Stream other than stdout or stderr                                            | 400                      | `{"error": "stream must be stdout or stderr", "code": "BAD_REQUEST"}`  |
| `Last-Event-ID` not of the form `{generation}-{offset}`, or a negative offset | 400 before stream starts | `{"error": "invalid resume position: {value}", "code": "BAD_REQUEST"}` |

**Edge Cases:**

- SSE includes `X-Accel-Buffering: no` header for nginx reverse proxy compatibility
- Each log event is an unnamed SSE event (`id:` line, then `data:` lines, then a blank line) and carries `id: {generation}-{offset}`. {offset} is the number of bytes of that stream captured since the process was created, up to the end of the event's data; {generation} identifies that creation and changes whenever a reload, remove+add or daemon restart creates the process again (decided 2026-10-29, #155)
- The log stream is fed by the capture writer directly, not by PROCESS_LOG bus events, so it works whatever `stdout_events_enabled` says (FUNC-051, ADR-040)
- A request without `Last-Event-ID`, or with an empty one, streams live output only; clients read the backlog with `GET .../log/{stream}` (the Web UI does), so nothing is sent twice
- A request with `Last-Event-ID: {generation}-{offset}` of the current generation first replays the bytes after that offset that the in-memory buffer (FUNC-082) still holds, then streams live output. An offset equal to the current one replays nothing
- If part of the requested range is no longer held, the server first sends `event: gap` with `data: {"bytes": {n}}` ({n} = oldest held offset - requested offset), then replays from the oldest held byte. A named event, not a comment, so a browser EventSource sees it
- A `Last-Event-ID` of another generation, or with an offset beyond the current one, means the process was created again (offsets restart at 0): the server sends `event: reset` with `data: {}` and replays every held byte
- `gap` and `reset` events carry no `id`, so the client's last id stays that of the last log event it received
- Every log event, also a replayed one, carries the `id` of its end (start offset + length of its data), so a client that reconnects again resumes where it stopped
- Multiple clients can stream the same log simultaneously
- The log stream reads the in-memory buffer and live output whether or not a log file is configured
- Event data is the captured output, one `data:` line per line (SEC-013); a chunk that ends mid-line is continued by the next event
- A slow client that cannot take an event (64 queued) has its stream closed by the server. A browser EventSource reconnects with its last `id` and the replay delivers the missed bytes while the buffer still holds them; otherwise it gets a `gap` event. No event is silently dropped from an open stream
- With `redirect_stderr = true` stderr goes to stdout; the stderr stream stays empty

**Dependencies:** FUNC-026

---

### FUNC-028: HTTP Basic Auth

**Description:** Authenticate API requests using HTTP Basic Authentication with bcrypt-hashed passwords.

**Acceptance Criteria:**

- **Given** `[server.http]` with `username` and a bcrypt-hashed `password` field
  **When** a request with matching Basic Auth is received
  **Then** the request is processed

- **Given** auth configured
  **When** a request without credentials or with wrong credentials is received
  **Then** 401 Unauthorized is returned with `WWW-Authenticate: Basic` header

- **Given** auth configured
  **When** `kahi ctl --addr host:port -u <user> -p <password>` connects
  **Then** the credentials from the `-u`/`-p` flags are sent (ctl has no config section; the daemon config holds only the bcrypt hash)

**Error Handling:**

| Error Condition              | Expected Behavior       | User-Facing Message                                                     |
| ---------------------------- | ----------------------- | ----------------------------------------------------------------------- |
| Plaintext password in config | Config validation error | "password must be bcrypt-hashed (use 'kahi hash-password' to generate)" |

**Edge Cases:**

- Auth is required for the TCP server. The Unix socket has no authentication; it is owner-only instead (SEC-022)
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
| --------------- | ----------------- | ------------------- |
| None specific   | N/A               | N/A                 |

**Edge Cases:**

- /healthz does not require authentication (it's a probe endpoint)
- Response includes no process-level information (that's /readyz's job)

**Dependencies:** FUNC-024

---

### FUNC-030: Readiness Check Endpoint

**Description:** Readiness probe endpoint that reports whether managed processes have stabilized.

**Acceptance Criteria:**

- **Given** all autostart processes are in RUNNING state
  **When** `GET /readyz` is requested
  **Then** 200 OK is returned with `{"status": "ready"}`

- **Given** some autostart processes are still in STARTING or BACKOFF state
  **When** `GET /readyz` is requested
  **Then** 503 is returned with `{"status": "not_ready", "pending": ["api", "web"], "failing": []}`

- **Given** `GET /readyz?process=web,api` is requested
  **When** web is RUNNING and api is RUNNING
  **Then** 200 OK is returned

- **Given** `GET /readyz?process=web,api` is requested
  **When** web is RUNNING and api is STOPPED
  **Then** 503 is returned with `{"status": "not_ready", "pending": [], "failing": ["api"]}`

**Error Handling:**

| Error Condition                     | Expected Behavior | User-Facing Message                                        |
| ----------------------------------- | ----------------- | ---------------------------------------------------------- |
| Unknown process in ?process= filter | 400               | `{"error": "no such process: foo", "code": "BAD_REQUEST"}` |

**Edge Cases:**

- /readyz does not require authentication
- A process is ready only in RUNNING state. A 503 lists the others in two arrays, always both present: `pending` for STARTING and BACKOFF (may still come up) and `failing` for every other state
- FATAL is not ready and is listed as `failing`: a program that gave up fails the probe, so the orchestrator sees it
- STOPPED and EXITED processes are not ready (`failing`)
- The unfiltered lists are sorted by name; the filtered ones keep the order of `?process=`
- If `?process=` list is empty or missing, check all autostart processes
- Processes with autostart=false are excluded from default readiness check

**Dependencies:** FUNC-029

---

### FUNC-031: CLI Process Control Commands

**Description:** `kahi ctl` subcommands for starting, stopping, restarting, and signaling processes.

**Acceptance Criteria:**

- **Given** a stopped process "web"
  **When** `kahi ctl start web` is issued
  **Then** the process is started and "web: started" is printed

- **Given** a running process "web"
  **When** `kahi ctl stop web` is issued
  **Then** the process is stopped and "web: stopped" is printed

- **Given** `kahi ctl restart all` is issued
  **When** processes are running
  **Then** all processes are restarted through the ordered bulk API (FUNC-083), one line per process: "{name}: ok", "{name}: skipped" or "{name}: error: {message}"

- **Given** `kahi ctl signal HUP web` or `kahi ctl signal SIGHUP web` is issued
  **When** web is running
  **Then** SIGHUP is sent to the web process and "web: signaled SIGHUP" is printed

- **Given** `kahi ctl start web nope` is issued
  **When** web is stopped and nope does not exist
  **Then** web is started ("web: started"), "nope: no such process: nope" is printed, then "1 of 2 failed" on stderr, and the command exits 1 (#213)

- **Given** `kahi ctl start web:*` is issued
  **When** web is a group
  **Then** all processes in the web group are started

**Error Handling:**

| Error Condition                                                          | Expected Behavior | User-Facing Message                                      |
| ------------------------------------------------------------------------ | ----------------- | -------------------------------------------------------- |
| Daemon not running                                                       | Exit code 1       | "connection failed: {error}"                             |
| Process name not found                                                   | Exit code 1       | "{name}: no such process: {name}"                        |
| Invalid signal                                                           | Exit code 1       | "process {name}: invalid signal \"{signal}\""            |
| Any target failed                                                        | Exit code 1       | "{n} of {m} failed" (stderr, after the per-target lines) |
| `all` with other names                                                   | Exit code 2       | "all cannot be combined with other names"                |
| Any other usage error (wrong argument count, unknown flag or subcommand) | Exit code 2       | cobra's message                                          |

**Edge Cases:**

- `all` keyword applies to all processes, through `/api/v1/all/{start,stop,restart}`; a program named "all" is reachable as `all:*` or through the API (#213). `all` must be the only target: `kahi ctl stop all web` is a usage error, exit 2, nothing is sent (decided 2026-10-29)
- `all` prints one line per process; any "{name}: error: {message}" line makes the command exit 1 after "{n} of {m} failed"
- Signal names follow `<signal.h>`: `kahi ctl signal` accepts `HUP` and `SIGHUP`, and output always uses the `SIG` form (decided 2026-10-29)
- `group:*` syntax targets all processes in a group
- Multiple process names can be specified: `kahi ctl start web api worker`; every target is attempted, and the command exits 1 if any of them failed (#213)
- `kahi ctl signal` takes one process; on a stopped process it reports "process not running: {name}"
- `kahi ctl` without a subcommand prints help and exits 0
- Exit codes for every `kahi` command (decided 2026-10-29): 0 success, 1 runtime failure, 2 usage error. Release-note item
- start, stop and restart (also `all` and `name:*`) have no overall client time limit: the daemon bounds each process by its stopwaitsecs, so a bulk stop can take the sum of them. `--timeout {duration}` limits the wait; at the limit ctl exits 1 with "daemon still working after {duration}" and the daemon finishes the action. Connecting is bounded by 5 s; queries (status, pid, health, ready, version, tail without -f) by 30 s (#222)

**Dependencies:** FUNC-026, INFRA-004, FUNC-083

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

| Error Condition         | Expected Behavior | User-Facing Message        |
| ----------------------- | ----------------- | -------------------------- |
| No processes configured | Print empty table | (empty table with headers) |

**Edge Cases:**

- State is color-coded in terminal output (green=RUNNING, red=FATAL, yellow=STARTING)
- Uptime is formatted as human-readable duration (1d 2h 30m)
- Exit status shown for EXITED processes: "exit 1", or "killed by SIGKILL" for a process a signal killed (#201)

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

| Error Condition        | Expected Behavior | User-Facing Message       |
| ---------------------- | ----------------- | ------------------------- |
| Process does not exist | Exit code 1       | "no such process: {name}" |

**Edge Cases:**

- `tail -f` uses SSE streaming from the API and prints the output exactly as written: a line written in several chunks stays one line, and nothing is added (#190)
- Without a log file, tail reads the in-memory buffer (64 KB per stream by default, FUNC-086)
- Ctrl+C cleanly terminates the tail stream
- Default byte count (1600) is configurable via `--bytes` flag
- When no log file is configured, tail reads from the ring buffer (FUNC-082)

**Dependencies:** FUNC-027, FUNC-031

---

### FUNC-034: CLI Foreground Attach

**Description:** Attach the terminal to a running process: keystrokes go to its stdin unchanged, its stdout and stderr stream to the terminal, and only one session per process is allowed. Raw mode and the session lock are v0.3.0 (#157); until then `attach` forwards stdin line by line and streams stdout only.

**Acceptance Criteria:**

- **Given** `kahi ctl attach web` is issued from a terminal
  **When** web is RUNNING
  **Then** the terminal is put in raw mode, every byte typed (Ctrl+C included, as 0x03) is written to web's stdin unchanged, web's stdout is written to the terminal's stdout and its stderr to the terminal's stderr, and "attached to web (Ctrl+] to detach)" is printed to stderr

- **Given** an attach session
  **When** the user presses Ctrl+] (0x1d)
  **Then** the session ends, the terminal mode is restored, "detached from web" is printed to stderr, the command exits 0 and web keeps running

- **Given** an attach session to web is open
  **When** a second `kahi ctl attach web` is issued
  **Then** it exits 1 with "process web: already attached" and the first session is unaffected

- **Given** an attach session
  **When** web exits
  **Then** the session ends, the terminal mode is restored, "process web exited" is printed to stderr and the command exits 0

- **Given** stdin is not a terminal (a pipe or file)
  **When** `kahi ctl attach web` is issued
  **Then** raw mode is not used, the "attached" and "detached" banners are not printed, and 0x1d is forwarded as an ordinary byte (no detach key); "process {name} exited" and errors are still printed to stderr. Input is forwarded as it arrives. End of input never closes the process's stdin; after it, output keeps streaming until the process exits or 1 second passes without output, then the command exits 0 (this is the e2e route; no pty is needed)

**Error Handling:**

| Error Condition                   | Expected Behavior          | User-Facing Message                |
| --------------------------------- | -------------------------- | ---------------------------------- |
| Process not RUNNING               | Exit 1, terminal untouched | "process not running: {name}"      |
| No such process                   | Exit 1                     | "no such process: {name}"          |
| Another session holds the process | Exit 1 (API status 409)    | "process {name}: already attached" |
| Connection to the daemon lost     | Restore terminal, exit 1   | "attach to {name} lost: {error}"   |

API: a successful upgrade is 101 Switching Protocols with `Upgrade: kahi-attach`; no such process is 404, not RUNNING is 400 "process not running: {name}", already attached is 409, an event listener process is 400 (FUNC-055), and a missing or wrong Upgrade header is 400 "attach requires Upgrade: kahi-attach". All errors are JSON, sent before the upgrade.

**Edge Cases:**

- The command is `attach` (supervisorctl calls it `fg`); decided 2026-10-26 (#157)
- Transport: `POST /api/v1/processes/{name}/attach` is upgraded (HTTP Upgrade, hijacked connection) to one raw two-way byte stream on the Unix socket and on TCP (auth on TCP, SEC-015); output frames are tagged stdout or stderr so the client can split them (decided 2026-11-14)
- While a session is open, `kahi ctl send` and `POST .../stdin` for that process are refused with 409 "process {name}: attached", so input never interleaves (decided 2026-11-14)
- Only input is put in raw mode (ICANON, ECHO and ISIG off); output post-processing stays on, so a child's `\n` still starts a new line. The child is not on a pty, so typed input is not echoed locally; programs that want echo do it themselves
- The session lock is held by the daemon, not the client: it is taken when the session opens and released when the client disconnects, detaches or the process exits, so a killed client never leaves the process locked
- Ctrl+] is the only detach key; Ctrl+C, Ctrl+Z and Ctrl+\ are forwarded to the process as bytes (decided 2026-11-14), so an interactive program receives ^C as it would in a terminal. SIGINT, SIGTERM or SIGHUP sent to the `kahi ctl` client itself still ends the session and restores the terminal
- Input bytes are written without a trailing newline; the line-oriented `kahi ctl send` (FUNC-077) is unchanged
- With `redirect_stderr = true` the process has no separate stderr stream; everything arrives on stdout
- Output already in the ring buffer is not replayed; the session shows output produced after it opens

**Dependencies:** FUNC-031, FUNC-077, FUNC-051, SEC-015, SEC-025

---

### FUNC-035: CLI Config Operations

**Description:** Reread config, update running state, add and remove groups via CLI.

**Acceptance Criteria:**

- **Given** `kahi ctl reread` is issued
  **When** config has changed
  **Then** prints "would apply: added=[...] changed=[...] removed=[...]" (does not apply changes)

- **Given** `kahi ctl update` is issued
  **When** config has changed
  **Then** applies changes: stops removed, restarts changed, adds new, and prints "updated: added=[...] changed=[...] removed=[...]"; a removed program that is running is stopped first

- **Given** `kahi ctl add web` is issued
  **When** web exists in config but is not active
  **Then** web group is added to active management (FUNC-011)

- **Given** `kahi ctl remove web` is issued
  **When** web is stopped
  **Then** web group is removed from active management (FUNC-011); with running processes it fails with "group web has running processes, stop first"

**Error Handling:**

| Error Condition        | Expected Behavior            | User-Facing Message       |
| ---------------------- | ---------------------------- | ------------------------- |
| Config file has errors | Exit code 1, nothing applied | "config error: {details}" |

**Edge Cases:**

- `kahi ctl avail` and `kahi ctl update <group>` are FUNC-085 (#161, v0.3.0)
- `reread`, `update` and `reload` print the new config's warnings (unknown keys, settings that are accepted but ignored) to stderr as `warning: ...`. The reload API returns them in a `warnings` array, for a preview (`?dry_run=true`) as well as an applied reload.

**Dependencies:** FUNC-031, FUNC-014

---

### FUNC-036: CLI Daemon Operations

**Description:** Shutdown, reload, version, and PID queries via CLI.

**Acceptance Criteria:**

- **Given** `kahi ctl shutdown` is issued
  **When** daemon is running
  **Then** graceful shutdown is initiated and the CLI waits until the daemon has exited (`--timeout`, default 60s, 0 does not wait), printing "shutdown initiated" and then "shut down"; if the daemon is still running at the timeout it exits 1 with "daemon still running after {timeout}"

- **Given** `kahi ctl reload` is issued
  **When** daemon is running
  **Then** the config is reloaded through the API (the same reload as SIGHUP) and the CLI prints "config reloaded: added=[...] changed=[...] removed=[...]"

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

| Error Condition    | Expected Behavior | User-Facing Message          |
| ------------------ | ----------------- | ---------------------------- |
| Daemon not running | Exit code 1       | "connection failed: {error}" |

**Edge Cases:**

- `kahi ctl pid all` prints `{name} {pid}` for every running process, one per line, sorted by name; `all` is a keyword, as in supervisorctl

**Dependencies:** FUNC-031

---

### FUNC-037: CLI Tab Completion

**Description:** Shell completion for bash and zsh, with subcommands, flags, process and group names from the running daemon, and signal names. Dynamic names are v0.3.0 (#156); until then completion covers subcommands and flags only.

**Acceptance Criteria:**

- **Given** `kahi completion bash` is run
  **When** output is sourced in bash
  **Then** tab completion works for subcommands, flags and process names

- **Given** `kahi completion zsh` is run
  **When** output is sourced in zsh
  **Then** tab completion works for subcommands, flags and process names

- **Given** a daemon managing `web` (numprocs 1) and `worker` (numprocs 2, group `worker`)
  **When** the user types `kahi ctl start <TAB>`
  **Then** `web`, `worker_0`, `worker_1`, `web:*`, `worker:*` and `all` are offered (the names `kahi ctl status` shows; every program is addressable as `name:*`)

- **Given** the user types `kahi ctl signal <TAB>`
  **When** completion is active
  **Then** the signal names `kahi ctl signal` accepts are offered in the `SIG` form (`SIGTERM`, `SIGHUP`, ...); after the signal, process names, `{group}:*` and `all` are offered

**Error Handling:**

| Error Condition                        | Expected Behavior                        | User-Facing Message          |
| -------------------------------------- | ---------------------------------------- | ---------------------------- |
| Daemon not running or unreachable      | Static completion only; no error printed | (no process names suggested) |
| Daemon does not answer within 1 second | Same as unreachable                      | (no process names suggested) |

**Edge Cases:**

- Per command: `start`, `stop`, `restart`, `status` and the `signal` target offer process names, `{group}:*` for every group in the running state (program groups and `[groups.x]` tables) and `all`; `tail`, `pid`, `send` and `attach` offer process names only; `add` offers the groups from the avail response (FUNC-085) that are not in the running state: `[groups.x]` table names, programs in no table and event listener pool names; `remove` offers groups in the running state; `update` and `reread` offer the union of running groups and groups in the config file
- Completion queries the daemon through the same client as the command, honouring `-s`, `-c` and the TCP flags already typed on the command line; it uses its own 1-second budget instead of the normal dial and query timeouts
- File names are never offered where a process name is expected
- fish and powershell scripts are generated by the same mechanism and get the same dynamic names, but only bash and zsh are tested
- The e2e proof runs cobra's hidden `kahi __complete ctl start ""` through the binary, against a test daemon and with no daemon

**Dependencies:** INFRA-004, FUNC-031, FUNC-085

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

| Error Condition                          | Expected Behavior  | User-Facing Message    |
| ---------------------------------------- | ------------------ | ---------------------- |
| Process doesn't stop within stopwaitsecs | SIGKILL escalation | "force-killing {name}" |

**Edge Cases:**

- SIGQUIT behaves as SIGTERM; a second SIGTERM, SIGINT or SIGQUIT during shutdown kills (FUNC-066)
- Autorestart is suppressed during shutdown
- New process starts are rejected during shutdown: the start, restart and bulk APIs return 409 `{"error": "daemon is shutting down", "code": "SHUTTING_DOWN"}`
- API continues to serve status requests during shutdown (`GET /api/v1/processes` is 200), while `/healthz` returns 503 `{"status": "shutting_down"}` (FUNC-029)

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

| Error Condition    | Expected Behavior       | User-Facing Message             |
| ------------------ | ----------------------- | ------------------------------- |
| Config parse error | Log error, keep running | "SIGHUP reload failed: {error}" |

**Edge Cases:**

- SIGHUP during startup is ignored: `kahi daemon` ignores it from its first statement until the supervisor's handler takes over, before any program is spawned. A SIGHUP before the Go runtime has started cannot be ignored by the program (#136)
- SIGHUP during shutdown is ignored
- SIGHUPs while a reload runs are coalesced into one more reload that runs after it and reads the file then, so the last edit is applied (#136)

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
| --------------- | ----------------- | ------------------- |
| None specific   | N/A               | N/A                 |

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

| Error Condition   | Expected Behavior   | User-Facing Message             |
| ----------------- | ------------------- | ------------------------------- |
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

| Error Condition            | Expected Behavior               | User-Facing Message                     |
| -------------------------- | ------------------------------- | --------------------------------------- |
| Signal channel buffer full | Signal is dropped (Go behavior) | (none -- buffer should be large enough) |

**Edge Cases:**

- Channel buffer size: 16
- Signals are received and dispatched in the main goroutine (no concurrent handlers), in the order they arrive; each is logged as "received signal"
- SIGHUP runs the reload off the main loop (#119); a SIGHUP while a reload runs queues one more reload ("reload in progress; another one follows it"), and further SIGHUPs while it is queued coalesce into it ("reload already queued; SIGHUP coalesced") (#136)
- The kernel merges a signal that arrives while the same signal is pending, so a burst of one signal can be handled fewer times than it was sent

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

| Error Condition | Expected Behavior                                                                                                    | User-Facing Message    |
| --------------- | -------------------------------------------------------------------------------------------------------------------- | ---------------------- |
| setpgid fails   | The spawn fails (setpgid happens in fork/exec); the process goes BACKOFF, then FATAL, with the spawn error in status | "spawn error: {error}" |

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

| Error Condition                   | Expected Behavior | User-Facing Message                                     |
| --------------------------------- | ----------------- | ------------------------------------------------------- |
| Path not writable by current user | Exit with error   | "permission denied: cannot create socket in {dir}: ..." |

**Edge Cases:**

- Default socket path is /var/run/kahi.sock when running as root
- As another user it is $XDG_RUNTIME_DIR/kahi.sock when XDG_RUNTIME_DIR is set to an absolute path, else kahi.sock in a kahi-{uid} directory under the temp dir ($TMPDIR, else /tmp). A fixed name directly in a shared /tmp is avoided: another user could create it first, so the daemon could not start or kahi ctl would talk to that user's socket (#146)
- The daemon creates the kahi-{uid} directory with mode 0700 and refuses to start if it exists but is not a directory, is owned by another user or is reachable by group or others: "socket directory {dir} must be a directory owned by uid {uid} with mode 0700"
- kahi ctl uses the same default when no socket is given and the config sets none

**Dependencies:** FUNC-024

---

### FUNC-045: Optional Privilege Dropping

**Description:** When running as root, drop to a configured user after the socket and ports are bound. The user comes from `kahi daemon -u` or `[supervisor] user`, as a name or a numeric id. Names, the `[supervisor] user` key and supplementary groups from `/etc/group` are v0.3.0 (#159).

**Configuration:**

```toml
[supervisor]
user = "kahi"        # name or uid, optionally ":group" (name or gid); kahi daemon -u takes precedence
```

**Acceptance Criteria:**

- **Given** Kahi runs as root with `user = "kahi"` in `[supervisor]`
  **When** startup completes (socket created, ports bound)
  **Then** the daemon drops to kahi's uid and passwd primary gid, with kahi's supplementary groups from `/etc/group` (SEC-010)

- **Given** Kahi runs as root with `user = "kahi:adm"`
  **When** startup completes
  **Then** the primary gid is adm's gid; the supplementary groups are kahi's own list (initgroups with kahi's passwd gid) plus adm

- **Given** Kahi runs as non-root with `[supervisor] user` set
  **When** startup begins
  **Then** the key is ignored with the warning "supervisor.user ignored: dropping privileges needs root (running as uid {uid})" and startup continues

- **Given** both `kahi daemon -u app` and `[supervisor] user = "kahi"`
  **When** startup completes as root
  **Then** the daemon drops to app, and "kahi daemon -u app overrides supervisor.user" is logged at INFO

**Error Handling:**

| Error Condition                | Expected Behavior     | User-Facing Message                                                            |
| ------------------------------ | --------------------- | ------------------------------------------------------------------------------ |
| User name not in `/etc/passwd` | Exit 1 before binding | "user not found: {name}"                                                       |
| Group name not in `/etc/group` | Exit 1 before binding | "group not found: {name}"                                                      |
| `kahi daemon -u` as non-root   | Exit 1 before binding | "kahi daemon -u {user}: dropping privileges needs root (running as uid {uid})" |
| setgroups/setgid/setuid fails  | Exit 1                | "privilege drop failed: {error}"                                               |

**Edge Cases:**

- Accepted forms: `name`, `uid`, `name:group`, `name:gid`, `uid:gid`, `uid:group`. A numeric value is a uid even when a user with that name exists
- A numeric uid with a passwd entry takes that entry's primary gid and groups; without one the gid defaults to the uid and the supplementary groups are reset to the gid alone (the pre-#159 behaviour)
- Lookups are pure Go (no CGO, Architectural Constraint 5): `/etc/passwd` and `/etc/group` only, no NSS, LDAP or SSSD
- The explicit `-u` flag fails closed as non-root while the config key only warns, because one config file is often shared between root and non-root runs (decided 2026-11-14)
- The user is resolved and validated before anything is bound, so a bad name never leaves a socket behind
- Before the drop, the daemon hands the files it created as root to the target uid and gid by path with lchown (never following symlinks): the control socket path (still mode 0700), the socket directory when the daemon created it, the PID file and its own log file. `kahi ctl` run as the target user then connects, and the daemon removes the socket and PID file at shutdown when the target user can write their parent directory; otherwise it logs one WARN and the next root start replaces the stale files (decided 2026-11-14). A chown failure exits 1 before the drop with "privilege drop failed: chown {path}: {error}". This is internal and unrelated to the rejected `server.unix.chown` key (SEC-022); the socket stays owned by the identity the daemon runs as. Proven in the ADR-037 container: after `kahi daemon -u kahi`, `kahi ctl status` run as kahi succeeds
- Startup order (FUNC-045, 047, 067, 068 together): load config and includes (a relative `-c` path is made absolute first, so reload re-reads the same file after `directory` is applied), resolve and validate the drop user, chdir (`directory`), set the umask (`umask`), open the daemon log file (`[supervisor] logfile`) as the launching user, raise rlimits (`minfds`, `minprocs`; root can raise hard limits only here), write the PID file, bind sockets and ports, lchown the socket, its directory, the PID file and the log, drop privileges, start processes. Messages before the log is open (config warnings, user resolution, chdir and umask errors) go to stderr; when `logfile` is unset the daemon logs to stdout and no log file is handed over. Relative paths (PID file, socket, log files) resolve against the working directory after `[supervisor] directory` is applied; includes are resolved before it, relative to the including file
- With `-d` the foreground parent stops after loading the config, resolving the user and checking the PID file, then re-executes; only the child applies directory, umask, log, rlimits, PID file and bind
- After a drop the daemon is non-root for FUNC-046: a per-process `user` that differs from the drop user gets the non-root warning and is ignored; one that resolves to the drop uid (and gid, when given) is a no-op without a warning
- On macOS, Go's `os/user` resolves names through the system library (Directory Services) even without CGO, so every local user resolves. The root-only criteria are proven on Linux in the ADR-037 container
- Privilege drop happens after socket creation but before processing any requests
- Changing `[supervisor] user` on reload logs "supervisor.user change takes effect after restart"
- `kahi migrate` maps supervisord `[supervisord] user` to `[supervisor] user`

**Until #159 (v0.3.0):** the user comes only from `kahi daemon -u uid[:gid]`, numeric. A name fails with "cannot resolve user \"{name}\": invalid uid: {name}". The supplementary groups are reset to the target gid alone.

**Dependencies:** FUNC-044, FUNC-047, FUNC-067, FUNC-068, FUNC-046 (name resolver, for the `[supervisor] user` part)

---

### FUNC-046: Per-Process User Switching

**Description:** When running as root, start child processes as specified users, given by name or numeric id, with the user's supplementary groups. Names and supplementary groups are v0.3.0 (#159).

**Acceptance Criteria:**

- **Given** Kahi runs as root and a process has `user = "www-data"`
  **When** the process starts
  **Then** the child runs as www-data's uid and passwd primary gid, with www-data's supplementary groups from `/etc/group` (SEC-010)

- **Given** Kahi runs as non-root and a process has `user` config
  **When** the process starts
  **Then** warning logged, user setting ignored, process runs as current user

- **Given** Kahi runs as root and a process has `user = "nosuchuser"`
  **When** the process starts
  **Then** each start attempt fails with "spawn error: user not found: nosuchuser" and the process never runs: it goes to BACKOFF and, after startretries + 1 failed attempts (the first start plus `startretries` retries), to FATAL (as FUNC-018)

**Error Handling:**

| Error Condition                           | Expected Behavior                                           | User-Facing Message                                                         |
| ----------------------------------------- | ----------------------------------------------------------- | --------------------------------------------------------------------------- |
| Specified user does not exist             | Spawn error: BACKOFF, then FATAL after startretries retries | "spawn error: user not found: {name}"                                       |
| Specified group does not exist            | Spawn error: BACKOFF, then FATAL after startretries retries | "spawn error: group not found: {name}"                                      |
| Running as non-root with per-process user | Warning logged, setting ignored                             | "user switching unavailable (not running as root), running as current user" |

**Edge Cases:**

- Accepted forms and the numeric-uid rules are the same as FUNC-045
- The user is looked up at each spawn, so a passwd change applies at the next start; an unknown name also logs the config warning "programs.{name}.user: user not found: {user}" at load and reload
- User switching sets uid, gid, and supplementary groups before exec
- A name resolving to a different uid than the daemon's counts as privilege-differentiated for the clean environment (SEC-016)

**Until #159 (v0.3.0):** `user` takes a numeric uid[:gid]. As root, a value that cannot be applied makes the process FATAL with "spawn error: cannot apply user \"{user}\": ..." and it never runs; the supplementary groups are cleared.

**Dependencies:** FUNC-045

---

### FUNC-047: Process Umask

**Description:** Configure file creation umask per process and for the daemon (#160).

**Acceptance Criteria:**

- **Given** `umask = "0027"` in `[supervisor]`
  **When** Kahi starts
  **Then** the daemon umask is set to 0027 before it opens its log file or binds a socket, and children without their own `umask` inherit 0027

- **Given** `umask = "0077"` in process config
  **When** the child starts
  **Then** the child umask is set to 0077 before exec (in the child, never in the daemon; ADR-024)

- **Given** no `umask` in `[supervisor]`
  **When** Kahi starts
  **Then** the daemon keeps the umask it was started with

- **Given** `umask = "0999"` (or "abc", or "01777") in `[supervisor]` or a process
  **When** the config is loaded
  **Then** validation fails with "invalid umask: {value}"

**Error Handling:**

| Error Condition     | Expected Behavior                             | User-Facing Message      |
| ------------------- | --------------------------------------------- | ------------------------ |
| Invalid umask value | Config validation error at load and on reload | "invalid umask: {value}" |

**Edge Cases:**

- A umask is 1 to 4 octal digits with a value up to 0777 (`"022"` and `"0022"` are equal); `kahi init` and the docs show the 4-digit form
- The socket bind still ORs in 077 around the bind (ADR-026), whatever the daemon umask is
- A daemon umask change on reload logs "supervisor.umask change takes effect after restart" and is not applied (decided 2026-11-14), like `directory`, `user` and `minfds`; this also keeps it out of the SEC-023 bind window
- `kahi migrate` maps supervisord `[supervisord] umask`
- Supersedes the earlier "default daemon umask is 0022": Kahi sets a daemon umask only when configured (decided 2026-11-14)

**Dependencies:** FUNC-002, FUNC-017, SEC-023, FUNC-014

---

### FUNC-048: Root Detection Warning

**Description:** Warn when running as root without explicit user configuration.

**Acceptance Criteria:**

- **Given** Kahi starts as root
  **When** no user to drop to is given (neither `kahi daemon -u` nor, from v0.3.0 (#159), `[supervisor] user`)
  **Then** a warning is logged once at startup: "running as root without user config; consider kahi daemon -u <user> for privilege dropping"; from v0.3.0 the text names both: "running as root without user config; set [supervisor] user or kahi daemon -u <user> for privilege dropping"

- **Given** Kahi starts as non-root
  **When** any config
  **Then** no warning is logged

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- Warning is logged once at startup, not repeated, and not again on a SIGHUP reload
- With `-d` only the daemonized child logs it, not the parent that re-execs (#209)

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

| Error Condition            | Expected Behavior                                                  | User-Facing Message                                                |
| -------------------------- | ------------------------------------------------------------------ | ------------------------------------------------------------------ |
| Subscriber callback panics | Panic recovered, logged as error, other subscribers still notified | "event handler panicked" (with the event type and the panic value) |

**Edge Cases:**

- Publish never blocks: each subscriber has its own goroutine and bounded drop-oldest queue (ADR-041), so callbacks run after Publish returns, in publish order per subscriber
- Subscribers receive events from one publisher in the order they were published
- Subscriber registration/unregistration is safe from any goroutine

**Dependencies:** INFRA-001

---

### FUNC-050: Process State Events

**Description:** Emit events on every process state transition.

**Acceptance Criteria:**

- **Given** the event system is enabled
  **When** a process transitions to any state
  **Then** a PROCESS*STATE*{STATE} event is emitted with data `name`, `group`, `state`, `from_state` and `pid` ("0" when no child exists), all strings

- **Given** a stopped process is started
  **When** PROCESS_STATE_STARTING is emitted
  **Then** it is published before the spawn, with pid "0"

- **Given** a RUNNING process exits by itself
  **When** PROCESS_STATE_EXITED is emitted
  **Then** its data also carries `expected` ("true" when the exit code is in exitcodes), `exitstatus`, and `exitsignal` when a signal killed it, named as in `<signal.h>` (`"SIGKILL"`) (ADR-032, #201)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- Events are emitted after the state transition is committed and carry the state in their data (ADR-032); from v0.3.0 delivery is asynchronous (ADR-041), so a subscriber reads the state from the event, not by querying the process from its callback
- Transitions of one process are published one at a time, so `from_state` always equals the `state` of that process's previous event
- All 7 states (STOPPED, STARTING, RUNNING, BACKOFF, STOPPING, EXITED, FATAL) have corresponding event types
- A transition that passes through BACKOFF to FATAL in one step (no retries left) emits PROCESS_STATE_FATAL with from_state STARTING

**Dependencies:** FUNC-049, FUNC-001

---

### FUNC-051: Process Log Events

**Description:** Publish PROCESS_LOG_STDOUT / PROCESS_LOG_STDERR events for a process's output when enabled for that process. The log stream (FUNC-027) no longer depends on these events. The per-process keys are v0.3.0 (#160).

**Acceptance Criteria:**

- **Given** `stdout_events_enabled = true` in process config
  **When** the process writes to stdout
  **Then** PROCESS_LOG_STDOUT events are published on the event bus with the output data, and reach the events API, event listeners and webhooks subscribed to them

- **Given** `stdout_events_enabled = false` (default)
  **When** the process writes to stdout
  **Then** no PROCESS_LOG_STDOUT event is published, and `kahi ctl tail -f` and the Web UI log viewer still stream the output

- **Given** `stderr_events_enabled = true`
  **When** the process writes to stderr
  **Then** PROCESS_LOG_STDERR events are published

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- The log stream (FUNC-027, ADR-033) is fed directly by the capture writer, not through the event bus; turning events on or off never changes what `tail -f` shows (decided 2026-11-14)
- Log events carry the chunk the capture writer stored (ANSI-stripped when `strip_ansi = true`), the same data the log stream and log file get
- With `redirect_stderr = true`, stderr output is published as PROCESS_LOG_STDOUT when `stdout_events_enabled` is true; setting `stderr_events_enabled` as well logs "programs.{name}.stderr_events_enabled has no effect with redirect_stderr"
- High-throughput processes can generate many events; publishing never blocks the capture writer, because every subscriber enqueues into a bounded drop-oldest queue (FUNC-055 edge case)
- Webhooks may not subscribe to PROCESS_LOG_* (one POST per output chunk); listing one in `[webhooks.x] events` is a config error "webhooks.{name}.events: PROCESS_LOG events cannot be sent to webhooks" (decided 2026-11-14). Listeners and the events API may subscribe
- `kahi migrate` maps supervisord `stdout_events_enabled` / `stderr_events_enabled`

**Dependencies:** FUNC-049, FUNC-018, FUNC-027, FUNC-021

---

### FUNC-052: Supervisor State Events

**Description:** Emit events when the supervisor itself changes state.

**Acceptance Criteria:**

- **Given** event system is enabled
  **When** Kahi finishes startup and enters RUNNING state **Then** SUPERVISOR_STATE_RUNNING event is emitted with data `state` "RUNNING" and `from_state` "STARTING"

- **Given** event system is enabled
  **When** Kahi begins shutdown
  **Then** SUPERVISOR_STATE_STOPPING event is emitted with data `state` "STOPPING" and `from_state` "RUNNING" (#154)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- These events fire once per state change, not periodically; a reload or a second shutdown request publishes none
- SUPERVISOR_STATE_RUNNING is published before any client of the event stream can connect; webhooks and event listeners (#106, v0.3.0) will be able to receive it

**Dependencies:** FUNC-049

---

### FUNC-053: Process Group Events

**Description:** Emit events when process groups are added or removed.

**Acceptance Criteria:**

- **Given** event system is enabled
  **When** a group is added to active management
  **Then** PROCESS_GROUP_ADDED event is emitted with data `group` and `processes` (the process names, sorted, comma-separated, for example "web_0,web_1")

- **Given** event system is enabled
  **When** a group is removed from active management
  **Then** PROCESS_GROUP_REMOVED event is emitted with data `group` and `processes`

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- Group events fire after the operation completes, not before: `processes` of an ADDED event are already in status, those of a REMOVED event already gone
- A group is added when a reload adds a program or a `[groups]` table (one event for the table, listing all its processes), and on `kahi ctl add` (FUNC-011); loading the config at startup is not an add (#212)
- A group is removed when a reload removes a program or a `[groups]` table, and on `kahi ctl remove`; a reload that restarts a changed program publishes neither event

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
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- Tick events include the timestamp of the tick: `{"when": "<Unix seconds>"}`, as supervisord's TICK events
- Ticks are not emitted during shutdown: the ticker stops when shutdown begins
- Clock drift does not cause missed ticks (use monotonic time)
- `GET /api/v1/events/stream` sends TICK_5, TICK_60 and TICK_3600 only when they are named in `?types=`; the default stream carries no ticks (decided 2026-10-28, #147)

**Dependencies:** FUNC-049

---

### FUNC-055: Event Listener Pools

**Description:** Managed processes that subscribe to events via a stdin/stdout protocol. Configured in `[eventlisteners.<name>]`, run by the process manager, wired into the daemon in v0.3.0 (#106); until then no listener runs.

**Configuration:**

```toml
[eventlisteners.notify]
command = "/usr/local/bin/notify-listener"
events = ["PROCESS_STATE_EXITED", "PROCESS_STATE_FATAL"]
buffer_size = 10        # queued events per pool (default 10)
result_timeout = 60     # seconds a listener may take to answer RESULT (default 60)
numprocs = 1            # every [programs.x] key is accepted (autorestart, user, environment, ...)
```

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

- **Given** a pool with `numprocs = 3`, all READY
  **When** three events arrive
  **Then** each listener process receives exactly one of them (round-robin over READY members)

**Error Handling:**

| Error Condition                                                                                     | Expected Behavior                                                                         | User-Facing Message                                           |
| --------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| Listener crashes                                                                                    | Restart per autorestart policy; its in-flight event is re-queued                          | "event listener {name} exited unexpectedly"                   |
| Buffer overflow                                                                                     | Oldest queued (not in-flight) event dropped and counted; one WARN per burst               | "event buffer overflow for {pool}, dropped {n} oldest events" |
| Invalid result format                                                                               | Log error, re-queue event                                                                 | "invalid result from listener {name}: {data}"                 |
| No RESULT within `result_timeout` (default 60 s)                                                    | Member stopped with its stopsignal, restarted per its autorestart policy, event re-queued | "listener {name} timed out on event {serial}"                 |
| Event fails 3 times (FAIL, invalid result, timeout, or the member exiting while it holds the event) | Event dropped                                                                             | "event {serial} for {pool} dropped after 3 failures"          |
| Unknown event type in `events`                                                                      | Config validation error                                                                   | "eventlisteners.{name}.events: unknown event type {type}"     |
| Name shared with a `[programs]` table                                                               | Config validation error                                                                   | "eventlisteners.{name}: name already used by programs.{name}" |

**Edge Cases:**

- Multiple listener instances in a pool; events dispatched round-robin to READY listeners
- Listener pool with numprocs=3 has 3 listener processes
- Events are buffered while no member is READY, up to `buffer_size`
- Event envelope format: `ver:3.0 server:kahi serial:{n} pool:{pool} poolserial:{n} eventname:{type} len:{n}\n{payload}`; `len` is the payload length in bytes, so the length framing of SEC-020 holds
- Listener response format: `RESULT {len}\n{payload}` where len is byte length of payload; payload "OK" = success, "FAIL" = re-queue
- If response parsing fails or payload is unrecognized, log error and re-queue event for retry
- A listener's stdout carries the protocol and is not captured as output; its stderr is captured like a program's. `redirect_stderr = true` on a listener is a config error "eventlisteners.{name}.redirect_stderr: not allowed, stdout carries the protocol"; the stdout output keys (`stdout_logfile*`, `stdout_syslog`, `stdout_events_enabled`, `stdout_capture_maxbytes`) are ignored with the warning "eventlisteners.{name}.{key} has no effect: stdout carries the protocol"
- `events` takes exact event type names, as the events API does (FUNC-027); supervisord's supertypes such as `PROCESS_STATE` are not accepted, and `kahi migrate` expands them to their member types
- Every bus subscriber (listener pools, webhooks, the events API) only enqueues into its own bounded queue that drops the oldest entry when full, so publishing never blocks the publisher: not the supervision loop and not a capture writer (FUNC-018). Drops are counted. The WARN is logged at most once per 10 s per subscriber: when the queue has room again after drops, or 10 s after the previous report while the queue stays full (a consumer that never reads still gets its drops reported); `{n}` counts the drops since the previous WARN: "event buffer overflow for {pool}, dropped {n} oldest events" for pools, "webhook queue full for {webhook}, dropped {n} oldest events" for webhooks, "events stream queue full, dropped {n} events" for the events API (decided 2026-11-14). Metrics are not a bus subscriber (ADR-049)
- A pool's bus subscription queue is its event buffer: one queue of `buffer_size`; the in-flight event is held outside it. A re-queued event goes to the head of the buffer and is never the one dropped on overflow
- Listener processes appear in `kahi ctl status` and the API like programs and can be started, stopped and restarted the same way
- A reload adds, changes and removes pools like programs (FUNC-014); events queued for a removed pool are discarded; a changed pool restarts its members and keeps its buffered events in order, the in-flight event is re-queued without counting as a failure
- A timed-out member is killed and restarted per its autorestart policy like any exit
- `buffer_size` and `result_timeout` must be >= 1: "eventlisteners.{name}.buffer_size must be >= 1", "eventlisteners.{name}.result_timeout must be >= 1"
- Attach and stdin writes (FUNC-034, FUNC-077) to a listener process are refused with 400 "process {name}: stdin carries the event listener protocol"
- `kahi migrate` maps supervisord `[eventlistener:x]` to `[eventlisteners.x]`
- Buffer overflow dropped events are counted via the `kahi_event_buffer_drops_total{pool=listener_name}` counter (FUNC-060; served only when `[server.metrics] enabled = true`)

**Dependencies:** FUNC-049, FUNC-050, FUNC-002, FUNC-051, FUNC-004, FUNC-007, FUNC-014, FUNC-031

---

### FUNC-056: Webhook Notifications

**Description:** Send HTTP POST notifications to configured URLs on specified events. `[webhooks.*]` is parsed today but nothing is delivered; delivery is wired in v0.3.0 (#106).

**Configuration:**

```toml
[webhooks.slack]
url = "${SLACK_WEBHOOK_URL}"
events = ["PROCESS_STATE_FATAL"]
template = "slack"          # generic (default), slack, pagerduty
timeout = 5                 # seconds per POST
max_retries = 3             # retries after the first attempt
headers = { "X-Team" = "ops" }
allow_insecure = false      # accept http:// (FUNC-059)
routing_key = ""            # PagerDuty integration key; required with template = "pagerduty"
```

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

| Error Condition                  | Expected Behavior                                                                                                                                                                                                   | User-Facing Message                                        |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| Webhook POST fails (timeout/5xx) | Retry up to max_retries (default: 3) with exponential backoff: `delay = min(1s * 2^attempt, 60s)` + random jitter of +/-10%                                                                                         | "webhook delivery failed for {webhook}: {error}, retrying" |
| All retries exhausted            | Log error, move on                                                                                                                                                                                                  | "webhook delivery failed permanently for {webhook}"        |
| Webhook URL unreachable          | Circuit breaker opens after 5 consecutive failed deliveries (a delivery fails when it ends without a 2xx: retries exhausted or a non-retryable response)                                                            | "webhook circuit breaker open for {webhook}"               |
| Circuit breaker open             | Events for that webhook are dropped (DEBUG) except the first event after each 5-minute interval, which is sent as the probe (no event, no probe); if it succeeds, the failure counter resets and the breaker closes | "webhook circuit breaker closed for {webhook}"             |

**Edge Cases:**

- Webhook delivery is async and never blocks the main supervision loop
- Webhook timeout default is 5 seconds
- Multiple webhooks can subscribe to the same event
- Webhook failures do not affect process management
- Circuit breaker state transitions logged at INFO level
- `max_retries` counts retries after the first attempt (3 means up to 4 POSTs); the older key `retries` is accepted as an alias with the warning "webhooks.{name}.retries is deprecated; use max_retries"
- 2xx is success; 3xx, 4xx and 5xx responses and transport errors are failures; 4xx other than 408 and 429 is not retried and fails the delivery at once
- `{webhook}` in messages is the config table name; log lines never contain the URL or header values, which often carry credentials (FUNC-058). `{error}` is the unwrapped cause (the `Err` of a Go `*url.Error`) or the HTTP status, never a formatted request; an e2e test with an unreachable URL containing a marker string asserts the marker is absent from the daemon log
- 408 and 429 are retried and honour `Retry-After` (capped at 60 s); redirects are not followed (a 3xx is a failure), so headers and `routing_key` are never re-sent elsewhere
- `attempt` is 0 for the first retry (delays 1 s, 2 s, 4 s ...). 408/429 retries count against `max_retries` and wait max(Retry-After, backoff) with +/-10% jitter, capped at 60 s; Retry-After is accepted in seconds or HTTP-date form. The probe is a single POST with no retries; a failed probe keeps the breaker open for another 5 minutes
- Deliveries per webhook are serialized through a bounded queue of 100 events (the event being delivered is held outside it); when full the oldest is dropped, one WARN per burst: "webhook queue full for {webhook}, dropped {n} oldest events"
- While the breaker is open, the first event after each 5-minute interval is sent as the probe
- `timeout` must be >= 1 and `max_retries` >= 0: "webhooks.{name}.timeout must be >= 1", "webhooks.{name}.max_retries must be >= 0"
- A reload rebuilds the webhook set: added webhooks start, removed ones stop after in-flight deliveries finish, changed ones restart with a closed breaker
- Expose the `kahi_webhook_circuit_breaker{webhook,status=open|closed}` gauge metric (FUNC-060). The `webhook` label is the config table name (`[webhooks.slack]` gives `webhook="slack"`), never the URL: webhook URLs often embed credentials (a Slack incoming-webhook URL is the secret) and `/metrics` must not disclose them

**Dependencies:** FUNC-049, FUNC-051, FUNC-014, FUNC-050

---

### FUNC-057: Webhook Templates

**Description:** Built-in payload templates for common services.

**Acceptance Criteria:**

- **Given** `template = "slack"`
  **When** a PROCESS_STATE_FATAL event fires
  **Then** payload matches Slack incoming webhook format: `{"text": "Process {name} entered FATAL state on {hostname}"}`

- **Given** `template = "pagerduty"`
  **When** a subscribed event fires
  **Then** payload matches PagerDuty Events API v2 format: `{"routing_key": "{routing_key}", "event_action": "trigger", "dedup_key": "kahi-{hostname}-{name}", "payload": {"summary": "{slack text for the event type}", "source": "{hostname}", "severity": "critical"}}`; every subscribed event sends `severity: critical`, and the dedup key keeps repeated events for one process in one incident

- **Given** `template = "pagerduty"` and no `routing_key`
  **When** the config is loaded
  **Then** validation fails with "webhooks.{name}: template pagerduty requires routing_key"

- **Given** `template = "generic"` (default)
  **When** an event fires
  **Then** payload is: `{"event": "{type}", "process": "{name}", "group": "{group}", "timestamp": "{iso8601}", "details": {...}}`, where details is the event's data without `name` and `group`

**Error Handling:**

| Error Condition       | Expected Behavior       | User-Facing Message                |
| --------------------- | ----------------------- | ---------------------------------- |
| Unknown template name | Config validation error | "unknown webhook template: {name}" |

**Edge Cases:**

- Built-in templates fill their fields from the event data (process name, group, event type, from_state, exit details, hostname); Slack text is per event type, for example "Process {name} entered FATAL state on {hostname}", with a generic "{event} for {name} on {hostname}" for other types
- `routing_key` is expanded and redacted like headers (FUNC-058)
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

| Error Condition                | Expected Behavior | User-Facing Message                                                                                                      |
| ------------------------------ | ----------------- | ------------------------------------------------------------------------------------------------------------------------ |
| Undefined environment variable | Config error      | "webhooks.{webhook}.{field}: undefined environment variable: ${name}" (the FUNC-012 message, prefixed with the key path) |

**Edge Cases:**

- Expansion happens at config load time, not at delivery time, through the same `${VAR}` expansion as the rest of the config (FUNC-012): `url`, `headers` values and `routing_key`
- Webhook URLs/headers containing secrets are not logged (redacted in debug output)

**Dependencies:** FUNC-056, FUNC-012, SEC-014

---

### FUNC-059: Webhook TLS Requirement

**Description:** Enforce TLS for webhook destinations by default.

**Acceptance Criteria:**

- **Given** a webhook with `url = "http://example.com/hook"` (plain HTTP)
  **When** config is validated
  **Then** config error: "webhooks.{webhook}: URL must use HTTPS (set allow_insecure = true to override)"

- **Given** `allow_insecure = true` on the webhook
  **When** config is validated
  **Then** plain HTTP URL is accepted with the warning "webhooks.{webhook}: allow_insecure set, sending over plain HTTP"

**Error Handling:**

| Error Condition    | Expected Behavior       | User-Facing Message              |
| ------------------ | ----------------------- | -------------------------------- |
| Invalid URL format | Config validation error | "invalid webhook URL: {webhook}" |

**Edge Cases:**

- `localhost`, `127.0.0.1` and `::1` URLs are exempt from the TLS requirement (for local development)
- A URL whose scheme is neither `https` nor `http`, or which has no host, is "invalid webhook URL: {webhook}" (the URL itself is not printed)
- TLS certificate verification uses the system trust store

**Dependencies:** FUNC-056

---

### FUNC-060: Prometheus /metrics Endpoint

**Description:** Expose a Prometheus-compatible metrics endpoint on the existing API listeners, behind the same authentication as the rest of the API.

**Configuration:**

```toml
[server.metrics]
enabled = false   # serve GET /metrics; requires [server.http] enabled = true
```

**Acceptance Criteria:**

- **Given** `[server.http] enabled = true` with credentials and `[server.metrics] enabled = true`
  **When** `GET /metrics` is requested over TCP with valid Basic Auth credentials
  **Then** 200 with Prometheus text exposition format is returned

- **Given** metrics enabled
  **When** `GET /metrics` is requested over TCP without or with invalid credentials
  **Then** 401 Unauthorized is returned (the route is wrapped in `requireAuth`, like `/ui/`; SEC-015)

- **Given** metrics enabled
  **When** `GET /metrics` is requested over the Unix socket
  **Then** 200 is returned without credentials (the socket is owner-only and unauthenticated for the whole API; SEC-022)

- **Given** `[server.metrics] enabled = true` and `[server.http] enabled = false`
  **When** the config is loaded
  **Then** validation fails with "server.metrics.enabled requires server.http.enabled"

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

| Error Condition                   | Expected Behavior                                                                                                                | User-Facing Message                |
| --------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------- |
| Metrics disabled                  | /metrics returns 404                                                                                                             | `{"error": "metrics not enabled"}` |
| Metrics collector cannot be built | /metrics returns 503, daemon keeps supervising (Principle: graceful degradation); proven by a unit test that injects the failure | `{"error": "metrics unavailable"}` |

**Edge Cases:**

- Go runtime and process collectors (goroutines, memory, GC, fds) included; all series live in a dedicated registry, not the global default
- `/metrics` requires authentication on TCP exactly like every other non-probe route; only `/healthz` and `/readyz` are unauthenticated. Prometheus scrapes with `basic_auth` in its scrape config. This supersedes the earlier "no auth" wording here and in plan.md
- `/metrics` is outside the `/api/v1` prefix and is subject to the same cross-origin protection as the rest of the mux (GET is a safe method and is not blocked)
- Changing `[server.metrics]` on reload logs "server.metrics change takes effect after restart" and does not rebind
- Metric names follow Prometheus naming conventions (snake_case, `_total` for counters)
- Also served: `kahi_event_buffer_drops_total{pool}` (FUNC-055 edge case) and `kahi_webhook_circuit_breaker{webhook,status}` (FUNC-056 edge case; labelled by webhook name, never URL; two series per webhook, the current status 1 and the other 0)

**Dependencies:** FUNC-025, SEC-015, FUNC-055, FUNC-056 (pool and breaker series), FUNC-028, FUNC-024

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

- **Given** a process is removed by a config reload
  **When** /metrics is next scraped
  **Then** none of its `kahi_process_*` series are present

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message |
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- `kahi_process_start_total` counts every spawn: manual starts, autorestarts and backoff retries
- `expected` on `kahi_process_exit_total`: `"true"` when the exit followed a stop request (STOPPING) or the process exited from RUNNING with a code listed in `exitcodes`; `"false"` otherwise, including every exit from STARTING
- `kahi_process_uptime_seconds` is computed at scrape time (0 when not RUNNING)
- Series are keyed by process name (`web`, `web_0` for numprocs members) with the group as a label on `kahi_process_state`
- Metrics survive config reload for processes that remain, including programs changed by the reload and recreated (counters are not reset)
- A spawn that fails before exec counts as a start and as an unexpected exit
- Removed processes, including those dropped by a numprocs decrease, have their series deleted at removal; there is no retention period or config key. Prometheus marks the series stale and keeps already-scraped history

**Dependencies:** FUNC-060, FUNC-014, FUNC-004, FUNC-007

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
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- The `kahi_info` metric is a constant 1 gauge with labels `version`, `go_version` and `fips`, with values from `internal/version`
- `kahi_supervisor_processes` has one series per state, including states with a count of 0, computed at scrape time
- `kahi_supervisor_uptime_seconds` is computed at scrape time
- `kahi_supervisor_config_reload_total` counts every reload attempt, applied or rejected, excluding `dry_run` previews; `update <group>` counts as one attempt; a filtered reload refused before loading (unknown or member name) counts as one attempt and one error. A rejected reload also increments `kahi_supervisor_config_reload_errors_total`

**Dependencies:** FUNC-060, FUNC-014

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
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- Ctrl+C in foreground mode triggers graceful shutdown
- Foreground mode is the default; no flag needed to enable it

**Dependencies:** INFRA-004

---

### FUNC-064: Optional Daemonization

**Description:** Optionally daemonize for bare-metal deployments. Go cannot fork a running program safely, so `-d` re-executes the binary in the background (decided 2026-10-28, #143).

**Acceptance Criteria:**

- **Given** `kahi daemon --daemonize`
  **When** started
  **Then** Kahi starts itself again in a new session with stdio on /dev/null, waits until that daemon runs (PID file written, sockets bound, processes started), prints "kahi daemon started (pid {pid})" and exits 0; if the daemon fails to start, its error is printed and the command exits 1

- **Given** daemonize mode
  **When** PID file path is configured
  **Then** PID file contains the daemon PID

- **Given** daemonize mode on exit
  **When** daemon stops
  **Then** PID file is removed

**Error Handling:**

| Error Condition                                                    | Expected Behavior | User-Facing Message                                     |
| ------------------------------------------------------------------ | ----------------- | ------------------------------------------------------- |
| PID file path not writable                                         | Exit with error   | "cannot write PID file: {path}: {error}"                |
| Another daemon already running (PID file exists with live process) | Exit with error   | "another instance running (pid {pid}); PID file {path}" |

**Edge Cases:**

- Stale PID file (process not running, or not a PID) is detected and overwritten with a warning ("replacing stale PID file")
- The PID file is checked before the socket is bound, so a second daemon names the running one
- With -d and no `[supervisor] logfile` the daemon log goes to /dev/null; Kahi warns about it
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
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- PID 1 detection at startup: `os.Getpid() == 1`, logged as "running as PID 1; orphaned processes are reaped"
- Reaping runs in the main loop on SIGCHLD (and on every tick during shutdown), not in a separate goroutine
- As PID 1 a reaped process Kahi does not manage is an expected orphan, logged at debug ("reaped orphan"); otherwise it is logged as a warning ("reaped unknown child")

**Dependencies:** FUNC-006

---

### FUNC-066: Graceful Shutdown Orchestration

**Description:** Stop all processes level by level in descending priority during shutdown, waiting for each priority level to stop before signalling the next, so that dependencies (for example a database at priority 100 behind applications at priority 300) outlive their dependents. Pulled into v0.2.4 (#151, decided 2026-10-29; the milestone was renamed from v0.2.3 on 2026-11-10, when v0.2.3 became a toolchain patch release from v0.2.2).

**Acceptance Criteria:**

- **Given** programs `app` (priority 300), `cache` (priority 200) and `db` (priority 100), each exiting 1 second after SIGTERM
  **When** shutdown is initiated
  **Then** `cache` receives its stop signal only after `app` has exited, and `db` only after `cache` has exited; the daemon exits with code 0 between 3 and 5 seconds after shutdown began

- **Given** two programs share priority 300
  **When** shutdown reaches level 300
  **Then** both receive their stop signal together, and level 200 starts only after both have exited

- **Given** a process in the current level does not exit within its stopwaitsecs
  **When** its stopwaitsecs elapses
  **Then** SIGKILL is sent to it (FUNC-003), and the next level starts once it has been reaped

- **Given** `shutdown_timeout` expires before all levels have stopped
  **When** the timeout fires
  **Then** every process still running in any level, including levels not yet signalled, receives SIGKILL immediately and the daemon exits with code 0

- **Given** a second SIGTERM, SIGINT or SIGQUIT arrives during shutdown
  **When** the signal is received
  **Then** every remaining process receives SIGKILL immediately and the daemon exits with code 0 (FUNC-038)

- **Given** all processes have stopped
  **When** shutdown completes
  **Then** Kahi exits with code 0

**Error Handling:**

| Error Condition                                                                        | Expected Behavior                                          | User-Facing Message                                                                                                                                                                |
| -------------------------------------------------------------------------------------- | ---------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Process not reaped within 1s of a SIGKILL from the shutdown timeout or a second signal | Log error per process, exit anyway                         | "process {name} stuck, giving up"                                                                                                                                                  |
| Sum of per-level max stopwaitsecs exceeds shutdown_timeout                             | Config warning at load and reload (does not block startup) | "ordered shutdown can take up to {n}s (largest stopwaitsecs per priority level, summed), more than supervisor.shutdown_timeout ({t}s); processes still running then are SIGKILLed" |

**Edge Cases:**

- Ordering uses the FUNC-008 ordering key, the same as start-all and the bulk API (FUNC-083): the `[groups]` table's priority for its members, else the program's own priority, then the program's own priority. A level is the set of processes with the same key; the name only orders signals within a level
- Shutdown timeout is configurable (`supervisor.shutdown_timeout`, default 30 seconds total); its clock starts when shutdown begins and covers all levels
- Each level waits until every process in it has been reaped; a process's own stopwaitsecs escalation (FUNC-003) bounds that wait. A level does not wait longer than the time left on the global clock
- Processes in STARTING are stopped like RUNNING ones (stop signal, then stopwaitsecs); processes in BACKOFF are moved to STOPPED without a signal (FUNC-083 / #92); STOPPED, EXITED and FATAL processes are skipped
- No process is started, restarted or retried once shutdown has begun (#92)
- Children are reaped throughout shutdown (#90)
- After a SIGKILL from the shutdown timeout or a second signal, the daemon waits up to 1 second to reap; processes not reaped by then are logged as stuck and the daemon exits
- Each level is logged at INFO: "stopping priority level {p}" with the process names. {p} is the program's priority, or "{table priority}/{own priority}" for members of a `[groups]` table
- A process stuck after its own stopwaitsecs SIGKILL holds its level until the shutdown timeout; only the timeout or a second signal leads to "stuck, giving up"
- The per-program warning "stopwaitsecs exceeds shutdown_timeout" is replaced by the per-level sum warning above, which covers it
- Ordered shutdown is the default and can make a full shutdown take much longer than signalling every process at once. The sample configs (README, `cmd/kahi/kahi.toml`, and the `kahi init` template in `internal/config/generate.go`) document how to tune it in the comments next to the keys involved:
  - `shutdown_timeout`: total budget for all levels; set it at least to the sum, over priority levels, of the largest `stopwaitsecs` in each level, and below the service manager's own stop timeout (systemd `TimeoutStopSec`, `docker stop -t`)
  - `priority`: programs sharing a priority stop together as one level; give programs without stop-order dependencies the same priority to shorten shutdown
  - `stopwaitsecs`: how long one process may take before SIGKILL; it also bounds how long its level holds up the next one
- A test asserts that the generated `kahi init` output contains the ordered-shutdown tuning comments for all three keys

**Dependencies:** FUNC-003, FUNC-008

---

### FUNC-067: Working Directory

**Description:** Configure the working directory for the daemon and per process (#113).

**Acceptance Criteria:**

- **Given** `directory = "/opt/app"` in `[supervisor]`
  **When** Kahi starts
  **Then** the daemon changes its working directory to /opt/app after the config (and its includes) is loaded and before it opens its log file, binds a socket or starts a process

- **Given** `directory = "/opt/app/web"` in process config
  **When** the process starts
  **Then** child working directory is set to /opt/app/web

- **Given** `[supervisor] directory = "/nonexistent"`
  **When** Kahi starts
  **Then** it exits 1 with "directory does not exist: /nonexistent" before binding anything

- **Given** a process with `directory = "/nonexistent"`
  **When** the process starts
  **Then** each start attempt fails with "spawn error: directory does not exist: /nonexistent": BACKOFF, then FATAL after `startretries` (as FUNC-018)

**Error Handling:**

| Error Condition                  | Expected Behavior                                           | User-Facing Message                                     |
| -------------------------------- | ----------------------------------------------------------- | ------------------------------------------------------- |
| Daemon directory does not exist  | Exit 1 at startup                                           | "directory does not exist: {path}"                      |
| Daemon directory not accessible  | Exit 1 at startup                                           | "cannot access directory: {path}: {error}"              |
| Process directory does not exist | Spawn error: BACKOFF, then FATAL after startretries retries | "spawn error: directory does not exist: {path}"         |
| Process directory not accessible | Spawn error: BACKOFF, then FATAL after startretries retries | "spawn error: cannot access directory: {path}: {error}" |

**Edge Cases:**

- Without `[supervisor] directory` the daemon keeps the working directory it was started in; supersedes the earlier "default is the directory containing the config file" (decided 2026-11-14), which would change how existing relative paths resolve
- A per-process directory is checked at spawn, not at load, as the target uid and gid when `user` is set: it may be created by another process or a mount after startup, and the retries give it the `startretries` window
- Per-process directory supports variable expansion
- A relative `command` containing `/` resolves against the process `directory` when set, otherwise against the daemon working directory; relative config paths resolve as in the FUNC-045 startup order
- Changing `[supervisor] directory` on reload logs "supervisor.directory change takes effect after restart"
- `kahi migrate` maps supervisord `[supervisord] directory`

**Dependencies:** FUNC-002, FUNC-012, FUNC-014

---

### FUNC-068: Resource Limits

**Description:** Configure minimum file descriptor and process limits for the daemon and raise the limits at startup when they are lower (#113).

**Acceptance Criteria:**

- **Given** `minfds = 4096` in daemon config and a NOFILE hard limit of 8192
  **When** Kahi starts
  **Then** the limits are left alone: Go has raised the daemon's own soft limit to the hard limit, and children get their original soft limit back from `os/exec` (decided 2026-11-15)

- **Given** `minfds = 4096` and a NOFILE hard limit of 2048, as root with CAP_SYS_RESOURCE
  **When** Kahi starts
  **Then** Kahi raises both NOFILE limits to 4096 with an explicit Setrlimit, so children inherit 4096

- **Given** `minprocs = 512` in daemon config
  **When** Kahi starts and the NPROC soft limit is below 512
  **Then** Kahi raises the NPROC soft limit to 512

- **Given** `minfds = 4096` set explicitly and a NOFILE hard limit of 2048 as non-root
  **When** Kahi starts
  **Then** it exits 1 with "cannot set NOFILE to 4096: hard limit is 2048 (current soft={s}, hard=2048)", {s} being the soft limit kahi reads, which Go has already raised towards the hard limit

- **Given** `minprocs` not set (default 200) and an NPROC hard limit of 100 as non-root
  **When** Kahi starts
  **Then** the soft limit is raised to 100, the warning "NPROC is 100, below the default minprocs 200; set supervisor.minprocs = 100 or lower to silence this" is logged, and startup continues (decided 2026-11-14)

- **Given** the soft limit already meets the minimum
  **When** Kahi starts
  **Then** the limits are not touched

**Error Handling:**

| Error Condition                            | Expected Behavior | User-Facing Message                                              |
| ------------------------------------------ | ----------------- | ---------------------------------------------------------------- |
| Cannot raise NOFILE to an explicit minfds  | Exit 1 at startup | "cannot set NOFILE to {n}: {error} (current soft={s}, hard={h})" |
| Cannot raise NPROC to an explicit minprocs | Exit 1 at startup | "cannot set NPROC to {n}: {error} (current soft={s}, hard={h})"  |

**Edge Cases:**

- Default minfds=1024, minprocs=200 (matching supervisord defaults). A value is explicit when the key is present in `[supervisor]`, whatever its value. A default that cannot be met raises the soft limit to the hard limit and warns "{RLIMIT} is {n}, below the default {key} {default}; set supervisor.{key} = {n} or lower to silence this" (NOFILE/minfds, NPROC/minprocs)
- The raise happens once at startup, before any process starts. NPROC: the soft limit is raised to minprocs and children inherit it. NOFILE: every Go program raises its own soft limit to the hard limit and `os/exec` gives children the original soft limit back unless the program calls Setrlimit; the original cannot be read, so minfds is a guarantee on the hard limit: kahi calls Setrlimit only when the hard limit is below minfds (then children inherit the raised limit), and otherwise children keep their original soft limit (decided 2026-11-15). Per-process limits (SEC-021) are applied on top in the child
- A daemon with CAP_SYS_RESOURCE (root outside a restricted container) also raises the hard limit; without it only the soft limit is raised, up to the hard limit, and a hard limit below an explicit value is the error above
- On containers, rlimits may be set by the container runtime; raise only if needed
- On macOS an NPROC refusal does not exit; it warns "cannot set NPROC to {n}: {error}; continuing (macOS)". A NOFILE value above `kern.maxfilesperproc` fails with "cannot set NOFILE to {n}: exceeds kern.maxfilesperproc ({max}) (current soft={s}, hard={h})" (explicit: exit 1; default: warn)
- Changing `minfds` or `minprocs` on reload logs "supervisor.{key} change takes effect after restart"
- The daemon-level raise is the exception ADR-024 allows: it changes the daemon's own limits, once, at startup
- `kahi migrate` maps supervisord `minfds` and `minprocs`

**Dependencies:** INFRA-001, FUNC-002, FUNC-014

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

- **Given** a full-line comment (`;` or `#`) directly above a section header or a key
  **When** migration runs
  **Then** the comment is written as a `#` comment directly above the corresponding TOML table header or key (v0.3.0, #158)

- **Given** a key with an inline comment (`startsecs = 10 ; ten seconds`)
  **When** migration runs
  **Then** the comment is kept at the end of the generated line (`startsecs = 10 # ten seconds`)

- **Given** a commented key that Kahi drops or does not support
  **When** migration runs
  **Then** its comment is kept on the line above the `# UNSUPPORTED: ...` line as `# (from supervisord) {comment}`

- **Given** a value continued over several lines, with a comment on a continuation line
  **When** migration runs
  **Then** that comment is dropped with the warning "comment on continuation line {n} dropped" and the joined value is unchanged

**Error Handling:**

| Error Condition          | Expected Behavior     | User-Facing Message                        |
| ------------------------ | --------------------- | ------------------------------------------ |
| Input file not found     | Exit with error       | "file not found: {path}"                   |
| Input file not valid INI | Exit with error       | "parse error: {details}"                   |
| Output file exists       | Refuse unless --force | "output file exists: {path} (use --force)" |

**Edge Cases:**

- An `[include]` section is written as a commented TOML `include` with a warning; the included files are not expanded or inlined (the user converts them separately)
- Maps environment variables from supervisord format to Kahi format
- Writes signal names in the `<signal.h>` form (TERM -> SIGTERM; a value that already has the prefix is kept)
- Comment rules (decision D5 of the 2026-10-26 delivery plan; scope in #158): a comment travels with the section or key it is attached to, so it survives the sorted output order (tables by name, keys alphabetically) and section renames (`[supervisord]` -> `[supervisor]`, `[unix_http_server]` -> `[server.unix]`). A comment followed by a blank line stays above the next item; a comment at the end of a section or file is written after that section's last key
- A renamed key with a user comment gets the user comment first, then the note: `{kahi_key} = {value} # {user comment} (renamed from "{supervisord_key}")`
- A comment on `environment` goes above the generated `[programs.{name}.environment]` table; a comment on a section header line goes at the end of the TOML table header line
- Comment text is written on one line; `;` comment markers become `#`

**Dependencies:** INFRA-005, FUNC-070

---

### FUNC-070: Migration INI Parser

**Description:** Parse supervisord INI config format including all section types.

**Acceptance Criteria:**

- **Given** a supervisord.conf file
  **When** parsed
  **Then** all section types are recognized: [supervisord], [program:name], [group:name], [eventlistener:name], [fcgi-program:name], [include], [unix_http_server], [inet_http_server]

- **Given** INI file with inline comments (a `;` at the start of a line or after whitespace, as in supervisord; `a;b` keeps its semicolon)
  **When** parsed
  **Then** comments are stripped from values and kept, with their position, for output (FUNC-069)

- **Given** INI file with continuation lines (leading whitespace)
  **When** parsed
  **Then** values are properly joined

**Error Handling:**

| Error Condition      | Expected Behavior      | User-Facing Message                  |
| -------------------- | ---------------------- | ------------------------------------ |
| Malformed INI syntax | Error with line number | "parse error at line {n}: {details}" |
| Unknown section type | Warning                | "unknown section type: {section}"    |

**Edge Cases:**

- supervisord.conf supports `%(ENV_X)s` syntax, which must be mapped to Kahi's `${X}` syntax
- `%(here)s`, `%(program_name)s`, `%(group_name)s`, `%(process_num)d` and `%(numprocs)d` are kept: Kahi expands them with the same syntax. A width such as `%(process_num)02d` is written as `%(process_num)d` with a warning (Kahi has no padding); a variable Kahi does not know is kept with a warning
- An unknown option is written as `# UNSUPPORTED: key = value (unknown option)`, keeping its key and value
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
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

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
| --------------- | ----------------- | ------------------- |
| None            | N/A               | N/A                 |

**Edge Cases:**

- Dry run is the default behavior when no --output is specified (stdout output)

**Dependencies:** FUNC-069

---

### FUNC-073: Migration Validation

**Description:** Validate the generated kahi.toml after migration.

**Acceptance Criteria:**

- **Given** migration produces a TOML file
  **When** validation runs
  **Then** the output is parsed and validated as a valid Kahi config, with the same checks the daemon runs when it loads the file, including process names and group references (#245)

- **Given** validation finds errors
  **When** migration completes
  **Then** errors are reported to the user with the generated file still available for manual correction

**Error Handling:**

| Error Condition                   | Expected Behavior                                                                                                                    | User-Facing Message                                                                                        |
| --------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------- |
| Generated config fails validation | The TOML is still printed or written; the errors go to stderr as one message, each on its own indented line, then exit code 1 (#111) | "validation: generated config has validation errors: {details}", then "generated config failed validation" |

**Edge Cases:**

- Validation catches issues like missing required fields that couldn't be inferred from supervisord config

**Dependencies:** FUNC-069, INFRA-005

---

### FUNC-074: FastCGI Program Definition

**Status:** Descoped 2026-10-18. The `internal/fcgi` package was never wired into the daemon (no config surface, no caller) and has been removed. FastCGI program supervision is now Out of Scope in the constitution; `kahi migrate` warns on `[fcgi-program:x]` sections and comments them out. Retained for history; not to be implemented.

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

| Error Condition       | Expected Behavior       | User-Facing Message                            |
| --------------------- | ----------------------- | ---------------------------------------------- |
| Missing socket config | Config validation error | "fcgi_program {name}: socket is required"      |
| Invalid socket format | Config validation error | "fcgi_program {name}: invalid socket: {value}" |

**Edge Cases:**

- FastCGI programs support all standard program options (autorestart, numprocs, etc.)
- Socket is created by Kahi, not by the child process

**Dependencies:** FUNC-002

---

### FUNC-075: FastCGI Socket Management

**Status:** Descoped 2026-10-18. The `internal/fcgi` package was never wired into the daemon (no config surface, no caller) and has been removed. FastCGI program supervision is now Out of Scope in the constitution; `kahi migrate` warns on `[fcgi-program:x]` sections and comments them out. Retained for history; not to be implemented.

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

| Error Condition                    | Expected Behavior     | User-Facing Message                      |
| ---------------------------------- | --------------------- | ---------------------------------------- |
| Socket bind fails (address in use) | Process goes to FATAL | "cannot bind socket: {address}: {error}" |
| Socket permission error            | Process goes to FATAL | "cannot set socket permissions: {error}" |

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

| Error Condition          | Expected Behavior | User-Facing Message                        |
| ------------------------ | ----------------- | ------------------------------------------ |
| Cannot connect to daemon | Exit code 1       | "cannot connect to kahi daemon"            |
| Processes not ready      | Exit code 1       | "NOT READY: web (STARTING), api (BACKOFF)" |

**Edge Cases:**

- Exit code 0 = healthy/ready, exit code 1 = not healthy/not ready
- Designed for Kubernetes exec probes

**Dependencies:** FUNC-029, FUNC-030, FUNC-031

---

### FUNC-077: Process stdin Writing

**Description:** Write data to a process's standard input via API/CLI.

**Acceptance Criteria:**

- **Given** a running process
  **When** `POST /api/v1/processes/web/stdin` with body `{"data": "quit\n"}` is sent
  **Then** "quit\n" is written to the process's stdin pipe

- **Given** `kahi ctl send web "quit"` is issued
  **When** web is running
  **Then** "quit\n" is written to stdin

**Error Handling:**

| Error Condition              | Expected Behavior  | User-Facing Message                                         |
| ---------------------------- | ------------------ | ----------------------------------------------------------- |
| Process not running          | Return error       | "process not running: {name}"                               |
| Stdin pipe broken            | Return error       | "stdin pipe broken for {name}"                              |
| Attach session open          | Return error (409) | "process {name}: attached"                                  |
| Process is an event listener | Return error (400) | "process {name}: stdin carries the event listener protocol" |

API status codes: "process not running" is 400, "stdin pipe broken" is 409, and "process {name}: attached" (an attach session is open, FUNC-034) is 409; all are JSON errors (the listener case is 400, FUNC-055) like every other API error, and `kahi ctl send` prints the message and exits 1.

**Edge Cases:**

- A write fails as "stdin pipe broken" when the process is still running but its stdin is closed (EPIPE, `os.ErrClosed` or `io.ErrClosedPipe`); the cause stays wrapped for the daemon log. A process that exits between the check and the write reports "process not running"
- The e2e proof for the broken pipe uses a program that closes its stdin and keeps running: `sh -c 'exec 0<&-; sleep 30'`
- Every process gets a stdin pipe; there is no per-process option to turn it off (decided 2026-10-26: the spawner always creates one, so "does not accept stdin" cannot occur)
- Data must include explicit newlines if needed

**Dependencies:** FUNC-026, FUNC-002, FUNC-031

---

### FUNC-078: Web UI Status Page

**Description:** HTML page showing all processes with state, PID, uptime, and action buttons. Served at `/ui/` (FUNC-084). **Clarify decisions (resolved 2026-10-18):** actions require JavaScript (no form fallback); "Clear Log" is deferred to a later 0.2.x release because no API can clear a log; "Start/Stop/Restart All" use the bulk endpoint FUNC-083.

**Acceptance Criteria:**

- **Given** Web UI is enabled and the request is authenticated
  **When** `GET /ui/` is requested in a browser
  **Then** an HTML page lists all processes with: name, group, state (color-coded), PID, uptime, description, and action buttons (Start, Stop, Restart, Tail Stdout, Tail Stderr). The Tail links are `/ui/log/{name}/stdout` and `/ui/log/{name}/stderr`, with `{name}` escaped by a `pathEscape` template function (`url.PathEscape`)

- **Given** the status page
  **When** "Start All", "Stop All" or "Restart All" is clicked
  **Then** the page sends an XHR `POST /api/v1/all/{start|stop|restart}` (FUNC-083), disables every action button until the response arrives, shows one success notification "{Action} all: done" or one error notification per result whose `status` is `error`, and refreshes the table; the browser does not navigate away

- **Given** the status page
  **When** a per-process Start, Stop or Restart button is clicked
  **Then** an XHR `POST /api/v1/processes/{name}/{action}` is sent with `{name}` taken from the button's `data-name` and encoded by `encodeURIComponent`, and the result is shown as a notification

- **Given** JavaScript is disabled
  **When** the page loads
  **Then** the server-rendered process table is readable, every action button carries the `disabled` attribute (`app.js` removes it on load), and a `<noscript>` element reads "Actions require JavaScript"

- **Given** the status table refresh (`GET /api/v1/processes`) returns a non-200 status or fails at the network level
  **When** the refresh completes
  **Then** the error notification is shown and the last rendered table stays in place

**Error Handling:**

| Error Condition                       | Expected Behavior                      | User-Facing Message                  |
| ------------------------------------- | -------------------------------------- | ------------------------------------ |
| Per-process API call fails            | Display error notification             | "Failed to {action} {name}: {error}" |
| Status refresh fails                  | Display error notification; keep table | "Failed to refresh status: {error}"  |
| Bulk result entry has `error`         | One error notification per such entry  | "Failed to {action} {name}: {error}" |
| Bulk call returns `409 CONFLICT`      | Display error notification             | "Another bulk action is in progress" |
| Bulk call returns `409 SHUTTING_DOWN` | Display error notification             | "Daemon is shutting down"            |
| Bulk call fails otherwise             | Display error notification             | "Failed to {action} all: {error}"    |

**Edge Cases:**

- Status updates are driven by `EventSource /api/v1/events/stream`, which sends named events (`event: PROCESS_STATE_*`), so the page registers `addEventListener` for each of the seven `PROCESS_STATE_*` types (`onmessage` never fires). Each such event triggers a refresh, and so does every `open` event, including the one after the browser's built-in reconnect, so changes missed while disconnected are picked up. Reconnection uses `EventSource`'s own retry; there is no custom timer. If `EventSource` is undefined, the page polls every 5 seconds
- JavaScript-rendered uptime is produced by a `formatUptime(seconds)` function in `app.js` with the same format as the server's `FormatUptime`: `{d}d {h}h {m}m` when days > 0, `{h}h {m}m` when hours > 0, else `{m}m`
- A bulk call can take as long as a full sequential stop (FUNC-083); the page sets no XHR timeout and keeps the buttons disabled until it answers
- Without JavaScript the page is read-only (no form fallback: a form POST to the JSON API would navigate the browser to raw JSON)
- Responsive layout for mobile viewing (STYLE-001)
- Process names are untrusted for markup purposes: they reach the page only through `html/template` escaping or `textContent`, and reach URLs only through `encodeURIComponent` (JavaScript) or `pathEscape` (templates) (SEC-026)
- Names containing `/` are encoded like any other character and not special-cased; whether the API's `{name}` route resolves them is FUNC-026's behaviour. Name validation is out of scope for 0.2.0 (it would reject existing configs)

**Dependencies:** FUNC-026, FUNC-027, FUNC-083

---

### FUNC-079: Web UI Log Viewer

**Description:** Browser-based log tailing for process stdout/stderr, served at `/ui/log/{name}/{stream}` (`stream` is `stdout` or `stderr`). "Tail Stdout" and "Tail Stderr" are links to separate pages, not an in-page toggle. **Clarify decisions (resolved 2026-10-18):** the initial backlog is read through the existing log-read endpoint; ANSI rendering is a hand-written minimal SGR subset. **Analyze remediation (2026-10-18):** `handleStreamLog` subscribes to the event bus before it flushes the response headers, so the browser's `open` event guarantees the subscription exists.

**Acceptance Criteria:**

- **Given** Web UI is enabled and "Tail Stdout" is clicked for process "web"
  **When** the log viewer page loads
  **Then** the page is `200` HTML with a server-rendered `<main id="log">` element carrying `data-name="web"` and `data-stream="stdout"`; `log.js` opens `EventSource /api/v1/processes/web/log/stdout/stream`, and in its first `open` handler reads `GET /api/v1/processes/web/log/stdout?length=65536`, shows the last 100 complete lines of that response, and then renders streamed output after them

- **Given** `handleStreamLog`
  **When** a stream request arrives
  **Then** it calls `bus.Subscribe` before it writes and flushes the response headers, so any chunk published after the browser's `open` event is delivered on the stream. A chunk published between the subscription and the backlog read appears twice; this is accepted

- **Given** streamed SSE events, which carry captured output chunks (one event can hold several lines, a partial line, or part of an escape sequence)
  **When** they arrive
  **Then** `log.js` appends each chunk to a pending buffer, renders only complete lines (up to and including `\n`), and keeps the remainder until the next chunk

- **Given** output containing SGR sequences for reset (`0`), bold (`1`), and foreground colors `30`-`37` and `90`-`97`
  **When** it is displayed
  **Then** it is rendered as `<span>` elements with CSS classes (`sgr-bold`, `sgr-fg-31`, ...), text set with `textContent`, and no `style=` attribute; SGR state carries across lines until a reset, as in a terminal; every other escape sequence (other SGR codes, CSI, OSC) is removed and never shown raw

- **Given** a line containing `<script>` or HTML markup
  **When** it is displayed
  **Then** it appears as literal text (no element is created from log content)

- **Given** `GET /ui/log/nosuch/stdout` for a name not in `ListWeb()`, or `GET /ui/log/web/stdin`
  **When** it is requested
  **Then** the response is `404` with "No such process: nosuch", or `400` with "invalid stream", respectively

**Error Handling:**

| Error Condition                              | Expected Behavior                             | User-Facing Message                      |
| -------------------------------------------- | --------------------------------------------- | ---------------------------------------- |
| `{stream}` is not `stdout` or `stderr`       | `400` page                                    | "invalid stream"                         |
| Process not in `ListWeb()`                   | `404` page                                    | "No such process: {name}"                |
| Backlog read returns `200` with empty body   | Display message, keep streaming               | "No output captured for this stream yet" |
| Backlog read returns `404` (process removed) | Display message, close the stream             | "No such process: {name}"                |
| Backlog read returns another status or fails | Display error notification, keep streaming    | "Failed to load log: {error}"            |
| SSE connection drops                         | `EventSource` built-in retry; no backlog read | (reconnects silently)                    |

**Edge Cases:**

- The backlog is up to the last 64 KB of the in-memory ring buffer (the read endpoint returns bytes, not lines). If the response is exactly 65536 bytes, the buffer was full and its first line is likely partial, so everything up to and including the first `\n` is dropped (a full response with no `\n` at all is dropped entirely); a trailing line without `\n` goes into the pending buffer. Then the last 100 complete lines are shown, or fewer when fewer exist
- `EventSource` exposes no HTTP status, so "no such process" is detected from the backlog read only
- The first stream request carries no `Last-Event-ID`, so it streams live output only and the backlog read does not duplicate it (FUNC-027)
- After a reconnect, `EventSource` sends `Last-Event-ID` and the server replays the output written while disconnected (FUNC-027, #155); no backlog re-read
- A `gap` event first ends the pending partial line as a line, then shows a line "[{n} bytes not shown]"; a `reset` event does the same with "[log restarted]" and then renders the replay
- The server closes a stream whose client falls 64 events behind; `EventSource` reconnects and resumes, so a fast writer no longer leaves silent gaps (v0.2.4)
- With `strip_ansi = true` the backlog and the stream are both stripped: the stream publishes the chunk the capture writer stored, which its offsets count (FUNC-027)
- The backlog request stays at 65536 bytes when a program's buffer is larger (FUNC-086): a larger buffer that holds at least 64 KB still returns exactly 65536 bytes, so the partial-first-line rule above holds
- The read endpoint ignores its `offset` parameter; the viewer does not use it
- `stderr` with `redirect_stderr = true` has no capture and returns an empty body (the "no output" message)
- Auto-scroll to bottom; scrolling up pauses auto-scroll until the user returns to the bottom
- The viewer keeps at most 5000 rendered lines and drops the oldest beyond that, so a long-open tab on a chatty process does not grow without bound (2026-10-19 security review)
- The process name is placed in API URLs with `encodeURIComponent(dataset.name)`

**Dependencies:** FUNC-027, FUNC-078, FUNC-082

---

### FUNC-080: Web UI Embedded Assets

**Description:** Serve Web UI as Go-embedded static files for zero-dependency deployment, under `/ui/static/`. **Clarify decision (resolved 2026-10-18):** `static_dir` overlays the embedded assets per file; it does not replace them. Only static assets can be overridden; templates are always the embedded ones. The directory reaches the package as `web.Config.StaticDir`, so this feature is testable without the `[server.web]` config table (FUNC-084).

**Acceptance Criteria:**

- **Given** Web UI is enabled
  **When** the binary is deployed with no additional files
  **Then** HTML, CSS, JavaScript and the favicon are served from embedded assets via `go:embed`

- **Given** the embedded assets
  **When** they are listed
  **Then** they are `app.js`, `log.js`, `style.css` and `favicon.svg` (served as `image/svg+xml`); both templates reference the favicon as `<link rel="icon" href="/ui/static/favicon.svg">` and contain no `data:` URI

- **Given** `static_dir = "/opt/kahi/web"` containing only `style.css`
  **When** `/ui/static/style.css` and `/ui/static/app.js` are requested
  **Then** `style.css` is served from the directory and `app.js` from the embedded assets (per-file overlay)

- **Given** two builds whose embedded `app.js` differ in content
  **When** each serves `/ui/static/app.js`
  **Then** the `ETag` values differ: the ETag is the quoted hex sha256 of the file content, not derived from `ModTime` (embedded files have a zero `ModTime`)

- **Given** a request with `If-None-Match` equal to the current ETag
  **When** the asset is unchanged
  **Then** the response is `304 Not Modified`

- **Given** an overlay open of `../web.go`, or of a symlink inside `static_dir` that points outside it
  **When** the overlay FS is asked for it
  **Then** it returns an error, and the HTTP response for such a path is `404`; nothing outside `static_dir` or the embedded FS is served (`os.Root` confinement)

**Error Handling:**

| Error Condition                                 | Expected Behavior                         | User-Facing Message                           |
| ----------------------------------------------- | ----------------------------------------- | --------------------------------------------- |
| Custom static_dir not found                     | Fall back to embedded assets with warning | "static_dir not found, using embedded assets" |
| Custom static_dir is not a directory            | Fall back to embedded assets with warning | "static_dir not found, using embedded assets" |
| File missing from static_dir (`fs.ErrNotExist`) | Serve the embedded file                   | n/a                                           |
| Other overlay open error (escape, permission)   | `404`, logged at debug level, no fallback | (standard 404)                                |
| Asset in neither static_dir nor embed           | `404`                                     | (standard 404)                                |

**Edge Cases:**

- Content-Type is set from a fixed extension map before `http.ServeContent` (`.js` → `text/javascript; charset=utf-8`, `.css` → `text/css; charset=utf-8`, `.svg` → `image/svg+xml`, `.ico` → `image/x-icon`), replacing the current switch in `web.go` (which sends `application/javascript`); the result therefore does not depend on the host's MIME database (`mime.TypeByExtension` reads system files, and older Linux images map `.js` to `application/javascript`)
- Caching headers: `ETag` and `Cache-Control: private, max-age=3600` (`private`: responses to authenticated requests must not be stored by shared caches); an overridden file's ETag is derived from its content as read
- `static_dir` is consulted only for the asset types the UI uses (`.js`, `.css`, `.svg`, `.ico`); other files in it, such as an `.html` file or one without an extension, are never served, because they would be served as HTML and could run same-origin script (2026-10-19 security review)
- Conditional requests are evaluated by `http.ServeContent` with the `ETag` header preset
- `static_dir` cannot override templates (`index.html`, `log.html`), so HTML escaping stays under the project's control

**Dependencies:** FUNC-078, FUNC-079

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

| Error Condition | Expected Behavior | User-Facing Message        |
| --------------- | ----------------- | -------------------------- |
| Empty password  | Error             | "password cannot be empty" |

**Edge Cases:**

- Password prompt suppresses echo for security
- Output is just the hash string, suitable for copy-paste into config

**Dependencies:** INFRA-004

---

### FUNC-082: Process Output Ring Buffer

**Description:** Keep an in-memory ring buffer of each process's recent output for log tailing and streaming without file logging. The size defaults to 64 KB per process per stream and is configurable per program (FUNC-086, #113, v0.3.0).

**Acceptance Criteria:**

- **Given** default config (no file logging)
  **When** `kahi ctl tail web` is issued
  **Then** the last data from the ring buffer is returned

- **Given** a process that has written 70000 bytes to stdout
  **When** `GET /api/v1/processes/web/log/stdout?length=65536` is requested
  **Then** exactly the last 65536 bytes are returned. A `length` larger than what the buffer holds returns the whole buffer (decided 2026-11-14); only a non-numeric or negative value, or one above the absolute maximum of 16777216 (the FUNC-086 cap), is refused with 400 (api-input-bounds)

- **Given** the ring buffer is full
  **When** new output arrives
  **Then** the oldest data is evicted

**Error Handling:**

None specific.

**Edge Cases:**

- Ring buffer is per-process per-stream (stdout and stderr separate)
- When the program is removed, its buffers go with it: the log read returns 404 "no such process: {name}"
- Ring buffer feeds SSE streaming (FUNC-027) whether or not a log file exists

**Dependencies:** FUNC-018

---

### FUNC-083: Bulk Process Actions API

**Description:** Start, stop or restart every configured process with one API call, in priority order. Added 2026-10-18 for the Web UI's "Start/Stop/Restart All" controls (FUNC-078), which until now posted to `/api/v1/processes/all/{action}` and so acted on a process literally named `all`. A server-side endpoint is used instead of client-side iteration because only the Manager can keep priority order (ascending for start, descending for stop); a reserved process name `all` was rejected because names are not validated and an existing program called `all` would break.

**Response shape:** `200` with `{"results": [{"name": "<process>", "status": "ok|skipped|error", "error": "<message>"}]}`. `error` is present only when `status` is `error` (`api.ActionResult`, `json:"error,omitempty"`). `results` is always an array, `[]` when empty, never `null`.

**Acceptance Criteria:**

- **Given** four processes `p10` (priority 10), `a` and `b` (priority 20) and `p30` (priority 30), all STOPPED
  **When** `POST /api/v1/all/start` is sent with valid credentials
  **Then** they are started in the order `p10`, `a`, `b`, `p30`, and `results` lists them in that order, each `"status": "ok"`

- **Given** the same processes, all RUNNING
  **When** `POST /api/v1/all/stop` is sent
  **Then** they are stopped in the order `p30`, `b`, `a`, `p10`, each stop completing before the next begins

- **Given** running processes
  **When** `POST /api/v1/all/restart` is sent
  **Then** the Manager's `RestartAllOrdered` stops all as for `stop`, then starts all as for `start` except those whose stop failed; both passes use one snapshot taken when the call begins, and `results` has exactly one entry per process in start order: the stop error if its stop failed (that process is never started, even if it finishes stopping later, and its entry sits at its start-order position), otherwise the outcome of its start; a process skipped on stop because it was already stopped gets its start outcome

- **Given** processes in each state
  **When** `POST /api/v1/all/start` is sent
  **Then** RUNNING, STARTING and BACKOFF processes are `skipped` (BACKOFF is already retrying); STOPPING is an `error` ("process is stopping"); STOPPED, EXITED and FATAL are started

- **Given** processes in each state
  **When** `POST /api/v1/all/stop` is sent
  **Then** STOPPED, EXITED and FATAL processes are `skipped`; RUNNING and STARTING are stopped; BACKOFF is moved to STOPPED (`ok`); STOPPING is waited for without a second signal and is `ok` once stopped, or the timeout error

- **Given** a process that does not stop within its `stopwaitsecs` plus 5 seconds
  **When** `POST /api/v1/all/stop` is sent
  **Then** its entry is `{"status": "error", "error": "stop timed out after {n}s"}` and the call moves on to the next process

- **Given** one process whose start fails (e.g. command not found)
  **When** `POST /api/v1/all/start` is sent
  **Then** the other processes are still started, the response is `200`, and the failing entry has `"status": "error"` and a non-empty `error`

- **Given** a bulk call in progress
  **When** a second `POST /api/v1/all/{action}` arrives
  **Then** it returns `409` with `{"error": "bulk action in progress", "code": "CONFLICT"}` and acts on nothing

- **Given** daemon shutdown has begun (the supervisor's `IsShuttingDown()` or the Manager's `ShuttingDown()` is true)
  **When** `POST /api/v1/all/{action}` arrives
  **Then** it returns `409` with `{"error": "daemon is shutting down", "code": "SHUTTING_DOWN"}`, the same response the per-process start route already gives; and a bulk start already running checks for shutdown before and after starting each process: a process not yet started is recorded as `{"status": "error", "error": "daemon is shutting down"}` and not started, and one whose start raced the beginning of shutdown is stopped at once and recorded the same way; "daemon is shutting down" wins over any start or stop error for that process

- **Given** no processes are configured
  **When** `POST /api/v1/all/start` is sent
  **Then** the response body is exactly `{"results":[]}`

- **Given** a process named `all` in the config
  **When** `POST /api/v1/processes/all/start` is sent
  **Then** that single process is started (the per-process route is unchanged)

- **Given** no credentials on TCP, or a cross-site `Sec-Fetch-Site`
  **When** `POST /api/v1/all/stop` is sent
  **Then** the response is `401` or `403` respectively, and nothing is stopped (`requireAuth` and SEC-025 apply)

**Error Handling:**

| Error Condition                | Expected Behavior                       | User-Facing Message                                             |
| ------------------------------ | --------------------------------------- | --------------------------------------------------------------- |
| Unknown action (`/all/foo`)    | `404` from the mux; nothing acted on    | (standard 404)                                                  |
| Wrong method (`GET /all/stop`) | `405` from the mux                      | (standard 405)                                                  |
| A single process action fails  | Continue; record it in `results`; `200` | `"error"` field of that result entry                            |
| Stop exceeds stopwaitsecs + 5s | Record error; continue                  | "stop timed out after {n}s"                                     |
| Another bulk call in progress  | `409`; nothing acted on                 | `{"error": "bulk action in progress", "code": "CONFLICT"}`      |
| Shutdown has begun             | `409`; nothing acted on                 | `{"error": "daemon is shutting down", "code": "SHUTTING_DOWN"}` |
| No processes configured        | `200` with `{"results":[]}`             | n/a                                                             |

**Edge Cases:**

- Only `POST` routes are registered, so SEC-025's rule that safe methods never change state still holds
- The call is synchronous: the response is written after the last process has been acted on. `stop` can take up to the sum over processes of `stopwaitsecs` plus 5 seconds. The API's `http.Server`s set no `WriteTimeout` today (verified 2026-10-18), so the response is not cut short; if a write timeout is ever added, this route must be exempt or the timeout must exceed that sum
- The call keeps running to completion if the client disconnects (it does not use the request context)
- "One at a time" is one mutex on `api.Server`, shared by `start`, `stop` and `restart` and by both listeners (they serve the same handler)
- The call takes its order from one snapshot when it begins, then re-resolves each process by name (`Manager.GetProcess`) immediately before acting on it. A process removed by a concurrent config reload since the snapshot is recorded as `{"status": "error", "error": "no such process: {name}"}` and is not acted on, so a detached `*Process` is never started; one added during the call is not acted on. Bulk calls and reloads are not otherwise serialized
- The shutdown check runs before the bulk lock is taken, so when both a shutdown and a running bulk call apply, the response is `409 SHUTTING_DOWN`
- On signal-driven shutdown (SIGTERM, SIGINT, SIGQUIT) the supervisor's `IsShuttingDown()` stays false today; the bulk route is still refused there through the Manager's `ShuttingDown()`, which `StopAll` makes true
- Known gap, pre-existing and tracked separately: `StopAll` stops only RUNNING and STARTING processes and `retryAfterBackoff` does not check for shutdown, so a process in BACKOFF when shutdown begins (for example one whose bulk start just failed) can respawn during or after shutdown. Bulk start does not fix this
- `start` returns once each process has been asked to start, not after `startsecs` (same semantics as the per-process start route)
- `start` includes processes with `autostart = false`, as `supervisorctl start all` does; `autostart` only governs daemon startup
- Skips are decided by checking state before acting, not by interpreting the error a start or stop returns (ADR-030)
- The order is the FUNC-008 ordering key: a program in a `[groups]` table takes the group's priority first (#196), then its own priority, then its name (ascending for start, descending for stop)
- `kahi ctl` support (e.g. `kahi ctl stop all`) is not part of this feature

**Dependencies:** FUNC-003, FUNC-008, FUNC-026, FUNC-028, FUNC-066, SEC-025

---

### FUNC-084: Web UI Configuration and Mounting

**Description:** Make the Web UI an optional subsystem toggled in `kahi.toml` (Constitution Architectural Constraint 4) and mount `internal/web` on the API server. Until this feature, nothing imported `internal/web`. **Clarify decisions (resolved 2026-10-18):** a new `[server.web]` table, off by default; the UI is mounted on the shared API mux under the prefix `/ui/`, so it is served on every listener that serves the API; a template failure disables the UI without affecting supervision (Principle 5).

**Configuration:**

```toml
[server.web]
enabled = false      # serve the Web UI under /ui/ (requires [server.http] enabled = true)
static_dir = ""      # optional per-file overlay for /ui/static/ (FUNC-080)
```

**Acceptance Criteria:**

- **Given** `[server.web]` is absent or `enabled = false`
  **When** `GET /ui/`, `GET /ui/static/app.js` or `GET /` is requested
  **Then** each returns `404`, exactly as before this feature

- **Given** `[server.web] enabled = true` and `[server.http] enabled = false`
  **When** the config is validated (`kahi daemon` or `kahi ctl reload`)
  **Then** validation fails with `server.web.enabled requires server.http.enabled` (browsers cannot reach a Unix socket, so the UI would be unreachable)

- **Given** `[server.web] enabled = true` and the TCP listener with credentials
  **When** any path under `/ui/`, or `GET /`, is requested without credentials
  **Then** the response is `401` with `WWW-Authenticate: Basic`; with valid credentials `/ui/` paths reach the UI handler and `GET /` returns `302` with `Location: /ui/`

- **Given** the UI is enabled
  **When** `GET /ui` (no trailing slash) is requested
  **Then** `ServeMux` answers `307` with `Location: /ui/` before authentication (verified on Go 1.27.2); the redirect reveals nothing

- **Given** the UI is enabled
  **When** a request reaches the Unix socket
  **Then** the UI is served without authentication, as the whole API already is on the socket

- **Given** the UI is enabled and building the web handler returns an error
  **When** the daemon starts
  **Then** an error is logged, the API and process supervision run normally, and every `/ui/` path returns `503` with `Content-Type: text/plain; charset=utf-8` and body "web UI unavailable", still behind `requireAuth`

- **Given** the UI is enabled
  **When** a response is written by `web.Handler` (page, asset, or its own 400/404) or by the `503` fallback
  **Then** it carries SEC-026's `Content-Security-Policy` and `X-Content-Type-Options: nosniff` headers, both set through the exported `web.SetSecurityHeaders(http.Header)`; every response not written by `web.Handler` or the `503` fallback is exempt: the `401` from `requireAuth`, the `302` for `GET /`, and the mux's `307` and `405`

- **Given** the UI is enabled
  **When** the status page is rendered
  **Then** its process data comes from `api.ProcessManager.List()` through an adapter implementing `web.ProcessLister`, which maps every `api.ProcessInfo` field and computes `StateLower` and `UptimeStr`

- **Given** a config reload whose `[server.web]` differs from the one the daemon started with
  **When** it is applied (signal-driven `handleReload` or API `Reload`)
  **Then** the UI keeps the startup setting until a daemon restart, and the reload logs "server.web changes take effect after restart". The comparison is against the `Server.Web` captured in `supervisor.New` from the config the daemon starts with, so the warning repeats on every reload while they differ. `GET /api/v1/config` shows the file's values, not the running UI setting

- **Given** a browser holding an open status or log page (an SSE connection that never ends on its own)
  **When** the daemon shuts down
  **Then** `api.Server.Stop` is called with a 5-second context from `cmd/kahi/daemon.go`, and when `Shutdown` returns a context error, `Stop` calls `Close` on each `http.Server`, so open streams are cut and the daemon exits

- **Given** the release documentation
  **When** this feature ships
  **Then** `README.md` documents the `[server.web]` table in its configuration reference, lists `/ui/` and `POST /api/v1/all/{action}` among the routes, replaces the "Web UI (planned; not yet served by the daemon)" line, states in its Security section that the Unix socket serves the API and UI without authentication, so a reverse proxy in front of the socket is unauthenticated, and has a Web UI section; and `kahi init` (`config.DefaultConfigTOML`) writes a commented `[server.web]` block

**Error Handling:**

| Error Condition                               | Expected Behavior                           | User-Facing Message                               |
| --------------------------------------------- | ------------------------------------------- | ------------------------------------------------- |
| `enabled = true` without `[server.http]`      | Config validation error; daemon won't start | "server.web.enabled requires server.http.enabled" |
| Building the web handler fails                | Log error; `/ui/` returns `503`             | "web UI unavailable"                              |
| `[server.web]` on reload differs from startup | Keep startup setting; log warning           | "server.web changes take effect after restart"    |

**Edge Cases:**

- One `requireAuth` wrap covers the whole `/ui/` subtree, so a UI route added later cannot be unauthenticated (SEC-026); the UI sits inside the API's `CrossOriginProtection`
- Only `GET` routes are added, so SEC-025's safe-method rule holds
- The page's own links and API calls use absolute paths (`/ui/...`, `/api/v1/...`); serving under a different proxy prefix is not supported in 0.2.0
- The handler build, the adapter and the `503` fallback live in testable functions outside the daemon's run function (ADR-029), so the failure path can be exercised by injecting a failing constructor
- When `[server.web]` is disabled, `buildUIHandler` returns an untyped `nil` `http.Handler`. Returning a nil `*web.Handler` inside the interface would make it non-nil, mount `/ui/`, and panic on the first request

**Dependencies:** INFRA-005, FUNC-014, FUNC-016, FUNC-026, FUNC-028, FUNC-078, FUNC-079, FUNC-080, SEC-026

---

### FUNC-085: CLI Available Programs and Group Update

**Description:** `kahi ctl avail` lists every program and group in the config with whether it is active, and `kahi ctl update <group>...` applies config changes to the named groups only. v0.3.0 (#161).

**Acceptance Criteria:**

- **Given** a config with `web` (active, autostart, priority 999) and `batch` (in the config but not added, `autostart = false`, priority 500)
  **When** `kahi ctl avail` is issued
  **Then** one line per program is printed, sorted by name, in the form `{name} {in use|avail} {auto|manual} {priority}`: `batch avail manual 500` and `web in use auto 999` (`batch` was removed from the running state with `kahi ctl remove`)

- **Given** the config changed `web` and `worker`
  **When** `kahi ctl update web` is issued
  **Then** only `web` is applied (stopped and restarted with the new config), `worker` is untouched, and "updated: added=[] changed=[web] removed=[]" is printed

- **Given** a group was removed from the config
  **When** `kahi ctl update` without arguments, or `kahi ctl update all`, is issued
  **Then** every change is applied, as in FUNC-035

- **Given** `kahi ctl update nosuch` is issued
  **When** no group `nosuch` exists in the running state or the new config
  **Then** it exits 1 with "no such group: nosuch" and nothing is applied

**Error Handling:**

| Error Condition        | Expected Behavior       | User-Facing Message       |
| ---------------------- | ----------------------- | ------------------------- |
| Unknown group name     | Exit 1, nothing applied | "no such group: {name}"   |
| Config file has errors | Exit 1, nothing applied | "config error: {details}" |

**Edge Cases:**

- `avail` is served by the daemon (`GET /api/v1/config/avail`) from the file it would load on reload (the same source as `reread`), so a root-owned config works and programs added to the file since the last reload show as `avail`. `in use` means present in the running state, whatever its process state or `autostart`
- `avail` lists event listener pools (FUNC-055) like programs; a `[groups.x]` table is listed under its member programs, not as an extra line
- `update <group>` applies a removal only when the removed group is named: `kahi ctl update old` stops and removes `old` if it is gone from the config
- The API takes the filter as `POST /api/v1/config/reload?groups=web,worker`; `dry_run=true` combines with it, so `reread` can preview one group
- `reload` and `update` without arguments are the same operation (decided 2026-11-14): they apply program, `[groups.x]`, event listener pool and webhook changes, and the `[supervisor]` settings not marked "takes effect after restart". Every `[supervisor]` and `[server.*]` key introduced in v0.3.0 is restart-only and logs its "... takes effect after restart" line. `reload` is the daemon-level name (as SIGHUP), `update` the supervisorctl-compatible one that also takes names
- After `update web`, groups that were not named keep their old config, and a later `reread` still reports them; `[supervisor]` changes are applied only by a full update
- Arguments are group names: a program outside any table, a `[groups.x]` table name, or an event listener pool name; a member program of a table fails with "{prog} is in group {table}; use {table}" (as FUNC-011) and an unknown name with "no such group: {name}"; both are 404 on `POST /api/v1/config/reload?groups=` and exit 1 in the CLI, and nothing is applied. Webhook changes are applied only by a full update. `reread` accepts the same arguments for a preview
- Each `avail` entry also carries its group (the table name, or the program's own name), which completion uses (FUNC-037)
- `avail` prints fields separated by single spaces, without padding
- e2e proof runs `avail`, `update <group>`, `add` and `remove` through the binary

**Dependencies:** FUNC-035, FUNC-014, FUNC-010, FUNC-011, FUNC-055

---

### FUNC-086: Configurable Output Capture Buffer

**Description:** Set the size of each program's in-memory output buffer (FUNC-082) per stream with `stdout_capture_maxbytes` and `stderr_capture_maxbytes`. v0.3.0 (#113); until then the keys are accepted and ignored with a warning and the buffer is 64 KB.

**Acceptance Criteria:**

- **Given** `stdout_capture_maxbytes = "1MB"` on `web`
  **When** web has written 2 MB to stdout
  **Then** `GET /api/v1/processes/web/log/stdout?length=1048576` returns the last 1048576 bytes

- **Given** no capture key
  **When** a process runs
  **Then** each stream's buffer is 64 KB, as before

- **Given** `stderr_capture_maxbytes = "100"` (below the minimum) or `"32MB"` (above the maximum)
  **When** the config is loaded
  **Then** validation fails with "programs.{name}.stderr_capture_maxbytes: must be between 1KB and 16MB"

**Error Handling:**

| Error Condition        | Expected Behavior       | User-Facing Message                                   |
| ---------------------- | ----------------------- | ----------------------------------------------------- |
| Size outside 1KB..16MB | Config validation error | "programs.{name}.{key}: must be between 1KB and 16MB" |
| Unparsable size        | Config validation error | "programs.{name}.{key}: invalid size {value}"         |

**Edge Cases:**

- Sizes use the same syntax as `stdout_logfile_maxbytes` (`"64KB"`, `"1MB"`, or a plain byte count)
- A `length` above the stream's buffer size returns the whole buffer; above 16777216 it is refused (FUNC-082)
- Changing the size on reload counts as a program change (FUNC-014): the program is restarted and its buffer is recreated empty
- With `redirect_stderr = true`, `stderr_capture_maxbytes` logs "programs.{name}.stderr_capture_maxbytes has no effect with redirect_stderr"
- Memory use is buffer size x 2 streams x numprocs; the 16 MB cap bounds a single stream
- supervisord's `capture_maxbytes` means "capture mode" for PROCESS_COMMUNICATION events, not a ring buffer; `kahi migrate` keeps it as `# UNSUPPORTED: stdout_capture_maxbytes = {value} (supervisord capture mode for PROCESS_COMMUNICATION events, not a buffer size)` instead of mapping it

**Dependencies:** FUNC-082, SEC-001, FUNC-079

---

## Style Features

### STYLE-001: Web UI Visual Design

**Description:** Clean, functional Web UI with status colors and responsive layout. Colors use the custom properties already defined in `internal/web/static/style.css` (`--color-running`, `--color-fatal`, `--color-starting`, `--color-stopping`, `--color-stopped`).

**Acceptance Criteria:**

- [ ] Process states use distinct colors: RUNNING=green (`--color-running`), FATAL=red (`--color-fatal`), STARTING/BACKOFF=yellow (`--color-starting`), STOPPED/EXITED=gray (`--color-stopped`), STOPPING=orange (`--color-stopping`); any other state, including `UNKNOWN(n)`, gets the class `state-unknown` and the gray color
- [ ] Table layout is responsive, collapsing to card layout on screens up to and including 768px wide (`@media (max-width: 768px)`)
- [ ] Every table cell, server-rendered and JavaScript-rendered, carries the `data-label` attribute the card layout uses for its column labels
- [ ] A row's Start, Stop, Restart, Tail Stdout and Tail Stderr controls sit in one cell, each with a visible text label; the Start All, Stop All and Restart All controls sit in one toolbar above the table
- [ ] Log viewer uses monospace font with dark background
- [ ] SGR colors in the log viewer are CSS classes (`sgr-bold`, `sgr-fg-30` ... `sgr-fg-37`, `sgr-fg-90` ... `sgr-fg-97`) defined in `style.css`, never inline styles
- [ ] No external CSS/JS dependencies (self-contained)

**Edge Cases:**

- `StateLower` for a state outside the known seven maps to `unknown`, so the class name never contains `(` or digits

**Dependencies:** FUNC-078, FUNC-079

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

### STYLE-003: Project Branding and Logo

**Description:** Project branding assets (SVG logo) stored in `assets/` directory and referenced from README.md. The logo is displayed at the top of the README for visual identity on GitHub and documentation sites.

**Acceptance Criteria:**

- [ ] SVG logo file exists at `assets/kahi_logo.svg`
- [ ] README.md references the logo via `![Kahi](assets/kahi_logo.svg)`
- [ ] Logo renders correctly on GitHub (visible in repo landing page)
- [ ] No logo files exist at the repository root (assets live in `assets/`)
- [ ] `assets/` directory is excluded from the Go build (no `.go` files)

**Error Handling:**

| Scenario                      | Behavior                                                 |
| ----------------------------- | -------------------------------------------------------- |
| Logo file missing             | README displays broken image alt text "Kahi"             |
| SVG contains embedded scripts | Rejected by GitHub CSP; use clean SVG without JavaScript |

**Dependencies:** None

---

### STYLE-004: Portfolio Landing Page (schwichtgit/kahi)

**Description:** After the codebase moves to `github.com/kahiteam/kahi`, convert `github.com/schwichtgit/kahi` into a minimal landing page repository. The README presents Kahi accurately as a process supervisor, describes the author's role, and directs visitors to the canonical repo.

**Acceptance Criteria:**

- [ ] `schwichtgit/kahi` repository contains only a `README.md` (no source code)
- [ ] README title identifies Kahi as a process supervisor (not AI/automation hub)
- [ ] README contains a prominent link to `https://github.com/kahiteam/kahi` as the canonical repo
- [ ] README describes author's role: architecture, core implementation, DX and CI/CD
- [ ] README includes a "What Kahi does" section accurate to the project (process supervision, config management, REST API, log capture, hot reload)
- [ ] README includes a "Where to find the code" section with links to: source repo, releases, Go module import path
- [ ] README includes contact/collaboration section linking to GitHub profile
- [ ] No stale source code, CI workflows, or Go files remain in `schwichtgit/kahi`
- [ ] README does not contain emoji (per constitution communication style)

**Error Handling:**

| Scenario                    | Behavior                                            |
| --------------------------- | --------------------------------------------------- |
| Canonical repo URL is wrong | Visitors land on 404; verify link before publishing |
| Old repo still has code     | Confuses visitors; must be cleaned to README-only   |

**Dependencies:** INFRA-008

---

## Security Features

### SEC-001: API Input Bounds Validation (CWE-770)

**Description:** Add defense-in-depth bounds checking for user-controlled allocation sizes in the log read API path. CodeQL alert [go/uncontrolled-allocation-size](https://github.com/kahiteam/kahi/security/code-scanning/1) flags `make([]byte, n)` at `internal/logging/ringbuf.go:54` where `n` originates from the HTTP `?length=` query parameter without an explicit upper bound.

The ring buffer already clamps `n` to available data (max 64KB), so exploitation requires no fix beyond satisfying the static analysis rule. However, defense-in-depth dictates explicit bounds at both the API handler and the ring buffer.

**Data Flow:**

```
GET /api/v1/processes/{name}/log/{stream}?length=N
  -> handleReadLog()          [internal/api/api.go:386]
  -> Manager.ReadLog()        [internal/process/manager.go:526]
  -> CaptureWriter.ReadTail() [internal/logging/capture.go:93]
  -> RingBuffer.Read(n)       [internal/logging/ringbuf.go:54]
  -> make([]byte, n)          <-- flagged allocation
```

**Acceptance Criteria:**

- **Given** `GET /api/v1/processes/web/log/stdout?length=999999999`
  **When** the request is processed by `handleReadLog()`
  **Then** a `length` above the absolute maximum (16777216, the largest buffer FUNC-086 allows) is refused with 400 before any allocation, and the ring buffer returns at most the bytes it holds, so a `length` between the buffer size and the maximum returns the whole buffer

- **Given** `RingBuffer.Read(n)` is called with `n > rb.size`
  **When** the allocation occurs
  **Then** `n` is clamped to `rb.size` before `make([]byte, n)` executes

- **Given** the fix is deployed
  **When** CodeQL re-scans the repository
  **Then** alert go/uncontrolled-allocation-size is resolved (no longer open)

- **Given** `GET /api/v1/processes/web/log/stdout?length=-5`
  **When** the request is processed
  **Then** the negative value is rejected (existing `v > 0` check) and the default of 1600 is used

**Error Handling:**

| Scenario                              | Behavior                                              |
| ------------------------------------- | ----------------------------------------------------- |
| `length` exceeds 64KB                 | Silently clamped to 64KB; no error returned           |
| `length` is negative or non-numeric   | Ignored; default 1600 used (existing behavior)        |
| Ring buffer is smaller than requested | Returns available data (existing clamp at line 47-49) |

**Edge Cases:**

- `length=0` is treated as "use default" by the existing `v > 0` check
- The 64KB cap matches the ring buffer allocation in `capture.go:39` (`64 * 1024`)
- From v0.3.0 the API cap is the absolute maximum 16777216 and the per-stream limit is the buffer itself (FUNC-086): the early-return guard shape described below for CodeQL is kept, only the constant changes; the message becomes "length exceeds maximum of 16777216"

**Dependencies:** FUNC-082, FUNC-026

---

### SEC-002: Static-Analysis-Visible Allocation Bounds (CWE-770)

**Description:** CodeQL alert [go/uncontrolled-allocation-size #1](https://github.com/kahiteam/kahi/security/code-scanning/1) flags `make([]byte, n)` in `RingBuffer.Read()` where `n` originates from the HTTP `?length=` query parameter. Prior attempts (PR #7: constant guards, PR #8: `min()` in `make()`) did not close the alert because:

1. **Clamp patterns** (`if n > X { n = X }`) merge both branches at a phi node; CodeQL's `BarrierGuard` does not propagate sanitization through phi-node merges.
2. **`min()` builtin** propagates taint (value flow model in `builtin.model.yml`), not a barrier.

**Root Cause:** CodeQL's `BarrierGuard` only recognizes relational comparisons (`<`, `<=`, `>`, `>=`) where the **unsafe branch terminates** (early return or panic). After such a guard, only the safe branch survives and the tainted value is sanitized on that branch.

**Fix (two layers):**

1. **Ring buffer (primary):** Replace the clamp in `Read()` with an early-return guard that terminates the unsafe branch:

```go
if n <= 0 || n > maxReadAlloc {
    return nil
}
```

After this guard, `n` is proven `<= maxReadAlloc` on the only surviving branch. The `make([]byte, n)` call uses the sanitized value.

2. **API handler (defense-in-depth):** Replace `min(v, maxReadLength)` in `handleReadLog()` with an explicit HTTP 400 that terminates the request:

```go
if v > maxReadLength {
    writeError(w, http.StatusBadRequest, "length exceeds maximum of 65536", "BAD_REQUEST")
    return
}
length = v
```

This validates user input at the system boundary and gives CodeQL a second cut point.

**Acceptance Criteria:**

- **Given** `NewRingBuffer(2_000_000)` is called
  **When** the buffer is created
  **Then** `size` is silently clamped to 1,048,576 (1MB)

- **Given** `RingBuffer.Read(200_000)` is called
  **When** n exceeds `maxReadAlloc` (64KB)
  **Then** `Read()` returns nil (early-return guard)

- **Given** `GET /api/v1/processes/web/log/stdout?length=999999`
  **When** length exceeds `maxReadLength` (64KB)
  **Then** HTTP 400 is returned with error message

- **Given** the fix is merged to `main`
  **When** CodeQL re-scans via `.github/workflows/security.yml`
  **Then** alert #1 (`go/uncontrolled-allocation-size`) auto-closes

- **Given** normal operation with default config (API reads <= 64KB)
  **When** any code path calls `Read()` with a valid length
  **Then** behavior is unchanged (values are already within bounds)

**Error Handling:**

| Scenario                                | Behavior                             |
| --------------------------------------- | ------------------------------------ |
| `NewRingBuffer(size)` with `size > 1MB` | Silently clamped to 1MB              |
| `NewRingBuffer(size)` with `size <= 0`  | Clamped to 1 (minimum viable buffer) |
| `Read(n)` with `n <= 0` or `n > 64KB`   | Returns nil (early-return guard)     |
| `handleReadLog` with `length > 64KB`    | HTTP 400 BAD_REQUEST                 |

**Edge Cases:**

- The API handler already ensures `length <= 64KB` for all HTTP paths, so `Read()` returning nil for `n > 64KB` is unreachable from the API; it guards against direct callers only
- The 1MB cap on `NewRingBuffer` is 16x the default ring buffer size (64KB)
- Both `maxRingBufSize` and `maxReadAlloc` are unexported package-level `const` values
- CodeQL's `BarrierGuard` mechanism: relational comparison + branch termination = taint sanitization on the surviving branch
- `min()` is NOT a barrier in CodeQL -- it has value-flow propagation that passes taint through
- Existing tests pass without modification since they use values within bounds

**Dependencies:** SEC-001

---

### SEC-003: Keyless Cosign Signing of GoReleaser Artifacts

**Description:** Sign every GoReleaser-produced archive and the `checksums.txt` file using Sigstore cosign in keyless mode via GitHub Actions OIDC. Signatures are recorded in the Rekor transparency log; no long-lived private key material exists. Verification uses cosign with the release workflow certificate identity pinned to the tag ref.

**Acceptance Criteria:**

- [ ] `.goreleaser.yml` has a `signs:` block with `cmd: cosign`, `artifacts: all`, `--yes`, `--output-certificate`, `--output-signature`
- [ ] Every archive in the release (e.g. `kahi_v0.1.0_linux_amd64.tar.gz`) has a matching `.sig` and `.pem` asset attached to the GitHub Release
- [ ] `checksums.txt` has a matching `.sig` and `.pem` produced by the same keyless cosign invocation
- [ ] `release.yml` permissions include `id-token: write` alongside `contents: write` and `packages: write`
- [ ] `release.yml` installs `sigstore/cosign-installer@v3` before the goreleaser step
- [ ] `COSIGN_YES: 'true'` is set in the workflow env
- [ ] `cosign verify-blob --certificate-identity "https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/<tag>" --certificate-oidc-issuer "https://token.actions.githubusercontent.com" --signature <sig> --certificate <cert> <archive>` succeeds for every archive
- [ ] Same verification succeeds for `checksums.txt`
- [ ] `goreleaser release --snapshot --skip=publish,sign` still succeeds locally for developer smoke tests

**Error Handling:**

| Scenario                             | Behavior                                                        |
| ------------------------------------ | --------------------------------------------------------------- |
| `id-token: write` permission missing | cosign OIDC exchange fails; job fails; release is not published |
| Rekor transparency log unreachable   | cosign retries; timeout fails the job (no silent skip)          |
| cosign binary missing                | cosign-installer step fails; job fails before goreleaser runs   |

**Dependencies:** INFRA-003, INFRA-013

---

### SEC-004: CycloneDX SBOM Generation for GoReleaser Artifacts

**Description:** Generate a CycloneDX 1.5 SBOM for every GoReleaser archive using syft. SBOMs are attached to the GitHub Release as assets and are covered by the cosign `signs: all` block from SEC-003, so every SBOM is itself signed. Downstream consumers can feed the SBOM into trivy, grype, or any CycloneDX-aware scanner.

**Acceptance Criteria:**

- [ ] `.goreleaser.yml` has an `sboms:` block invoking syft with `cyclonedx-json` output
- [ ] Every archive has a matching `*.cdx.json` SBOM asset attached to the GitHub Release
- [ ] The SBOM lists the Go module `github.com/kahiteam/kahi` as the primary component and all direct and indirect dependencies from `go.mod`
- [ ] The SBOM CycloneDX `specVersion` is `1.5` or later
- [ ] syft is installed via `anchore/sbom-action/download-syft@v0` before the goreleaser step runs
- [ ] `cyclonedx-cli validate --input-file kahi_<version>_linux_amd64.cdx.json` exits 0 on the produced SBOM
- [ ] Each `.cdx.json` has a matching `.sig` and `.pem` produced by the SEC-003 signs block

**Error Handling:**

| Scenario                                  | Behavior                                                      |
| ----------------------------------------- | ------------------------------------------------------------- |
| syft binary missing                       | sbom-action step fails; job fails before goreleaser runs      |
| `go.mod` unreadable (invalid checkout)    | syft produces empty SBOM; goreleaser aborts on empty artifact |
| SBOM generation slower than archive build | GoReleaser serializes SBOM generation per archive; no race    |

**Dependencies:** INFRA-003, SEC-003

---

### SEC-005: Keyless Cosign Signing of Container Images

**Description:** Sign the multi-arch container manifest published to `ghcr.io/kahiteam/kahi` using cosign keyless mode with GitHub Actions OIDC. The signature is bound to the image digest (not the tag) so retagging or mutation does not invalidate verification. Cosign records the entry in Rekor and uploads the signature reference to GHCR.

**Acceptance Criteria:**

- [ ] `release.yml` Docker job has a step that runs `cosign sign --yes ghcr.io/kahiteam/kahi@${{ steps.build.outputs.digest }}` after `docker/build-push-action`
- [ ] The signing step runs exactly once per release (digest-based, not per-tag)
- [ ] `release.yml` Docker job permissions include `id-token: write` in addition to `packages: write`
- [ ] `docker/build-push-action@v7` has `provenance: mode=max` and `sbom: true` (was `provenance: false, sbom: false`)
- [ ] `cosign verify --certificate-identity "https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/<tag>" --certificate-oidc-issuer "https://token.actions.githubusercontent.com" ghcr.io/kahiteam/kahi:<tag>` succeeds
- [ ] Same verification succeeds against `ghcr.io/kahiteam/kahi@sha256:<digest>`
- [ ] `cosign tree ghcr.io/kahiteam/kahi:<tag>` lists the signature under the `Signatures` section
- [ ] Releases prior to this feature (v0.1.0-alpha.1..v0.1.0-alpha.6) are unaffected

**Error Handling:**

| Scenario                                        | Behavior                                   |
| ----------------------------------------------- | ------------------------------------------ |
| Registry login missing                          | cosign sign fails; release job fails       |
| Digest output not exported by build-push-action | signing step fails fast with a clear error |
| `id-token: write` permission missing            | OIDC exchange fails; release job fails     |

**Dependencies:** INFRA-012, SEC-003

---

### SEC-006: Container SBOM and Provenance Attestations

**Description:** Attach both a CycloneDX SBOM and a SLSA provenance attestation to the container image. BuildKit-native in-band attestations are produced via `docker/build-push-action@v7` (`sbom: true, provenance: mode=max`, discoverable via OCI 1.1 referrers). Additionally, a cosign attestation of the CycloneDX SBOM is attached so that `cosign download sbom` and `cosign verify-attestation --type cyclonedx` work as expected.

**Acceptance Criteria:**

- [ ] `docker/build-push-action@v7` has `sbom: true` and `provenance: mode=max`
- [ ] After the image is pushed, a step runs `syft ghcr.io/kahiteam/kahi@<digest> -o cyclonedx-json > image-sbom.cdx.json`
- [ ] A step runs `cosign attest --yes --predicate image-sbom.cdx.json --type cyclonedx ghcr.io/kahiteam/kahi@<digest>`
- [ ] `cosign download sbom ghcr.io/kahiteam/kahi:<tag>` returns a valid CycloneDX JSON document
- [ ] `cosign verify-attestation --type cyclonedx --certificate-identity "https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/<tag>" --certificate-oidc-issuer "https://token.actions.githubusercontent.com" ghcr.io/kahiteam/kahi:<tag>` succeeds
- [ ] `cosign verify-attestation --type slsaprovenance ...` succeeds against the BuildKit-produced provenance
- [ ] The SBOM covers all image layers and packages, not only the Go module

**Error Handling:**

| Scenario                                    | Behavior                                                            |
| ------------------------------------------- | ------------------------------------------------------------------- |
| BuildKit too old for `provenance: mode=max` | docker/build-push-action fails with a clear error; upgrade required |
| syft unable to scan the image               | step fails; attestation not uploaded; release fails                 |
| `cosign attest` fails mid-run               | release job fails; verify-signatures and release-notes are not run  |

**Dependencies:** SEC-005

---

### SEC-007: Release Verify-Signatures Gate Job

**Description:** A dedicated `verify-signatures` job runs after `goreleaser` and the Docker publish job, re-verifying every signature and attestation just produced. Any failure aborts the release workflow before GitHub Release creation. Any CI failure in SBOM generation, signing, or attestation causes the whole release to fail; partial publication is never acceptable.

**Acceptance Criteria:**

- [ ] `release.yml` has a `verify-signatures` job with `needs: [goreleaser, docker]`
- [ ] `verify-signatures` downloads every GoReleaser artifact plus its `.sig` and `.pem` and runs `cosign verify-blob` with the tag-pinned certificate identity
- [ ] `verify-signatures` verifies the container image signature with `cosign verify`
- [ ] `verify-signatures` verifies both the CycloneDX attestation (`--type cyclonedx`) and the SLSA provenance attestation (`--type slsaprovenance`) with `cosign verify-attestation`
- [ ] GitHub Release creation (if separated into its own job) runs only with `needs: verify-signatures`
- [ ] Any verification failure causes the workflow run status to be `failure`; release assets and container tags are not promoted (no `latest` tag applied if verify fails)
- [ ] The verify script sources its expected identity from `refs/tags/${GITHUB_REF_NAME}`; no branch-based fallback
- [ ] Any prior job failure (sign, sbom, attest) also blocks the release — the gate is not the only safeguard

**Error Handling:**

| Scenario                                         | Behavior                                                         |
| ------------------------------------------------ | ---------------------------------------------------------------- |
| One artifact signature missing                   | verify-blob fails; job fails; release fails                      |
| Certificate identity does not match tag workflow | verify fails; job fails; release fails                           |
| Rekor transparency log entry missing             | verify fails; job fails; release fails (no bypass flag)          |
| Upstream SBOM/sign job failed                    | verify-signatures is skipped; release job already failed earlier |

**Dependencies:** SEC-003, SEC-004, SEC-005, SEC-006

---

### SEC-008: Release Verification Documentation

**Description:** A `docs/verifying-releases.md` document describes how downstream consumers verify release artifacts and container images. Includes cosign install, the certificate identity and OIDC issuer for the current release process, step-by-step verify commands for archives, checksums, and container images, and SBOM consumption examples with cyclonedx-cli. Auto-generated release notes embed a copy-pasteable verify command block so every GitHub Release is self-documenting.

**Acceptance Criteria:**

- [ ] `docs/verifying-releases.md` exists at repo root
- [ ] Document explains the keyless cosign model (Fulcio, Rekor, OIDC) in 1-2 paragraphs
- [ ] Document includes copy-pasteable `cosign verify-blob` commands for archive and checksums verification
- [ ] Document includes copy-pasteable `cosign verify` and `cosign verify-attestation` commands for container images
- [ ] Document includes a `cyclonedx-cli validate` example for SBOM consumption
- [ ] Document states the expected certificate identity format: `https://github.com/kahiteam/kahi/.github/workflows/release.yml@refs/tags/<tag>`
- [ ] Document states the expected OIDC issuer: `https://token.actions.githubusercontent.com`
- [ ] README.md has a "Verifying releases" section that links to `docs/verifying-releases.md`
- [ ] Auto-generated release notes include a verify command example block (mirrors the ai-resume pattern)

**Dependencies:** SEC-003, SEC-005, SEC-006, SEC-007

---

### SEC-009: Per-Process Credential Switching Is Enforced

**Description:** The per-process `user` setting specified in FUNC-046 must be applied to the actual spawned child process, not merely parsed. A security review on 2026-05-26 found the configured uid/gid was resolved but never set on the child (the credential `SysProcAttr` was never attached during spawn), so children silently ran with the supervisor's inherited privileges. This requirement makes the behavior fail-closed and adds a regression gate.

**Acceptance Criteria:**

- **Given** Kahi runs as root and a process has `user = "www-data"`
  **When** the process is spawned
  **Then** the child's `SysProcAttr.Credential` is set to the resolved uid/gid before exec, verified in the `task test` QA gate by a non-root regression test asserting the resolved credential is attached to the spawn config; a root-gated integration test (skipped when not privileged) additionally asserts the spawned child's effective uid/gid

- **Given** a process specifies `user` but the credential cannot be applied (resolution failure, or running as a uid that cannot setuid the target)
  **When** the process is spawned
  **Then** the spawn fails and the process goes to FATAL; the child MUST NOT start with the supervisor's privileges

- **Given** the regression test suite
  **When** `task test` runs in the QA gate
  **Then** a test asserts that a configured `user` results in a credential being attached to the spawn config (preventing silent regression of the wiring)

**Error Handling:**

| Error Condition                              | Expected Behavior     | User-Facing Message                 |
| -------------------------------------------- | --------------------- | ----------------------------------- |
| Configured user cannot be resolved           | Process goes to FATAL | "user not found: {name}"            |
| Credential cannot be applied at spawn (fail) | Process goes to FATAL | "cannot apply user {name}: {error}" |

**Edge Cases:**

- The credential is attached to the spawn configuration, never dropped on the floor when `user` is non-empty
- Resource limits and umask, already wired, remain applied alongside the credential

**Dependencies:** FUNC-046

---

### SEC-010: Supplementary Groups Reset on Privilege Drop

**Description:** When the daemon drops privileges (FUNC-045), it must reset supplementary group memberships, not just set the primary gid and uid. The 2026-05-26 review found `DropPrivileges` called `setgid`/`setuid` but never `setgroups`, leaving root's supplementary groups (e.g. `docker`, `wheel`) active after the drop and inherited by children. This requirement enforces an explicit `setgroups` step.

**Acceptance Criteria:**

- **Given** Kahi runs as root with supplementary groups and `user = "kahi"` in config
  **When** the privilege drop runs
  **Then** `setgroups` is called to replace the supplementary groups with kahi's own group list before `setuid`, in the order setgroups -> setgid -> setuid; none of the groups the daemon inherited survive

- **Given** a numeric target uid with no `/etc/passwd` entry
  **When** the privilege drop runs
  **Then** the supplementary groups are reset to the target gid alone

- **Given** the `setgroups` call fails
  **When** the privilege drop runs
  **Then** startup aborts with an error; privileges MUST NOT be partially dropped

- **Given** the regression test suite
  **When** `task test` runs in the QA gate
  **Then** a test asserts the privilege-drop path invokes the supplementary-group reset (verified via an injectable syscall seam, since the test process is not root)

**Error Handling:**

| Error Condition | Expected Behavior | User-Facing Message         |
| --------------- | ----------------- | --------------------------- |
| setgroups fails | Exit with error   | "setgroups failed: {error}" |

**Edge Cases:**

- Call order is setgroups, then setgid, then setuid; reversing leaves a window of residual privilege
- The new list is the target user's groups as initgroups(3) computes them (the passwd primary gid plus every `/etc/group` entry naming the user), plus the override gid of a `user:group` value. Groups the target user is itself listed in are kept, gid 0 included; only the daemon's inherited list is discarded (v0.3.0, #159, decided 2026-11-14). Until #159 it is the target gid only. The purpose is unchanged: the daemon's inherited groups are always discarded, never kept
- The same list is used for children switched to a user (FUNC-046), so a child never keeps the daemon's groups either

**Dependencies:** FUNC-045

---

### SEC-011: FastCGI Unix Socket Secure Default Permissions

**Status:** Descoped 2026-10-18. The `internal/fcgi` package was never wired into the daemon (no config surface, no caller) and has been removed. FastCGI program supervision is now Out of Scope in the constitution; `kahi migrate` warns on `[fcgi-program:x]` sections and comments them out. Retained for history; not to be implemented.

**Description:** A FastCGI Unix-domain socket must never be left at the umask-dependent default permission (commonly world-accessible). The 2026-05-26 security review found `Socket.Open` chmod-ed the socket only when `socket_mode` was explicitly set; with `socket_mode` omitted, any local user could connect to the socket and speak FastCGI to the backend it fronts. The constitution already mandates a default socket mode of 0700; this requirement enforces it on the FastCGI path.

**Acceptance Criteria:**

- **Given** a FastCGI Unix socket configured without `socket_mode`
  **When** the socket is opened
  **Then** the socket file is chmod-ed to 0700, verified in the `task test` QA gate by a test asserting the on-disk file mode

- **Given** a FastCGI Unix socket configured with an explicit `socket_mode`
  **When** the socket is opened
  **Then** the configured mode is applied, verified by an automated test

- **Given** the chmod fails
  **When** the socket is opened
  **Then** Open returns an error and the listener is closed; no socket is left in service with wider-than-intended permissions

**Error Handling:**

| Error Condition       | Expected Behavior                   | User-Facing Message                            |
| --------------------- | ----------------------------------- | ---------------------------------------------- |
| chmod on socket fails | Open returns error, listener closed | "cannot chmod FastCGI socket: {path}: {error}" |

**Edge Cases:**

- Default mode 0700 matches the daemon control socket default in the constitution
- TCP FastCGI sockets are unaffected; their exposure is governed by the bind address

**Dependencies:** FUNC-075

---

### SEC-012: Log Files Refuse To Follow Symlinks

**Description:** Process and daemon log files must be opened with `O_NOFOLLOW` so a symlink planted at the configured log path cannot redirect writes to an attacker-chosen file. The 2026-05-26 security review (issue #39) noted the log opens used `O_CREATE|O_WRONLY|O_APPEND` without `O_NOFOLLOW`; if logs are placed in a directory writable by a less-trusted user, that user could pre-create a symlink and have the (possibly root) daemon append log output to a sensitive file. This is defense-in-depth: it requires a misconfigured log directory to exploit, but the guard is cheap and removes the failure mode.

**Acceptance Criteria:**

- **Given** a configured log path whose final component is a symlink
  **When** the capture writer or daemon logger opens it
  **Then** the open fails (ELOOP) and the error is surfaced, verified in the `task test` QA gate by a test that points a logfile at a symlink and asserts the open errors

- **Given** a normal (non-symlink) log path
  **When** the log file is opened
  **Then** it is created/appended as before; existing logging behavior is unchanged

**Error Handling:**

| Error Condition                  | Expected Behavior                         | User-Facing Message                     |
| -------------------------------- | ----------------------------------------- | --------------------------------------- |
| Log path final component is link | Open fails; process to FATAL / daemon err | "cannot open log file: {path}: {error}" |

**Edge Cases:**

- `O_NOFOLLOW` affects only the final path component; a symlinked parent directory is still resolved normally
- Applies to all log opens: process stdout/stderr capture and the daemon log file

**Dependencies:** FUNC-018

---

### SEC-013: SSE Log Stream Framing Is Injection-Safe

**Description:** The log SSE endpoint must encode process output so it cannot inject SSE frames. The 2026-05-26 review (issue #39) found the log stream wrote raw output with `fmt.Fprintf(w, "data: %s\n\n", data)`; output containing newlines or `event:`/`data:` prefixes could desynchronize the SSE stream. Impact is benign (the web client ignores unknown event types and renders via `createTextNode`, so no XSS), but the framing must be correct. Each output line is emitted as its own `data:` field so embedded newlines and field prefixes are carried as literal data.

**Acceptance Criteria:**

- **Given** process output containing newlines and a line such as `event: injected`
  **When** it is streamed over the log SSE endpoint
  **Then** every line is emitted as a `data:` field (the injected text is delivered as literal data, not parsed as a new SSE event), verified by a test on the SSE writer

- **Given** ordinary single-line output
  **When** it is streamed
  **Then** the client receives the same content as before; the consuming web UI behavior is unchanged

**Error Handling:**

| Error Condition         | Expected Behavior          | User-Facing Message |
| ----------------------- | -------------------------- | ------------------- |
| n/a (output formatting) | Output framed per SSE spec | n/a                 |

**Edge Cases:**

- A blank line in output is framed as a bare `data:` line (empty field), never as the blank line that ends an SSE event; e.g. output `line1\n\nline3` is written as `data: line1` / `data:` / `data: line3` followed by one terminating blank line
- SSE also ends a line at a bare CR or at CRLF, so both are normalized to LF before framing; output `x\revent: foo` is written as `data: x` / `data: event: foo` (found by the 2026-10-19 Web UI security review). A carriage-return progress bar is shown as separate lines
- Scope is the log SSE endpoint only; the event stream endpoint (`/api/v1/events/stream`) already JSON-encodes its payload and is unaffected

**Dependencies:** FUNC-027

---

### SEC-014: Redact Secrets from Config API Response

**Description:** The `GET /api/v1/config` endpoint must not expose secrets. The 2026-07-09 whole-repo security review (HIGH, confidence 9) found that `handleGetConfig` serializes the live `*config.Config` directly via `json.Encode`; the config structs carry only `toml:` tags and no `json:` tags, so every field is emitted verbatim. The response leaks `Server.HTTP.Password` (the API credential), every `Programs[*].Environment` map (routinely DB passwords, API keys, tokens), and every `Webhooks[*].Headers` value and credential-bearing `Webhooks[*].URL`. Any principal who can reach the API — unauthenticated when combined with SEC-015 — harvests these in one call. Hardens FUNC-026 and enforces constitution Security Requirement 3.

**Acceptance Criteria:**

- **Given** a config with an HTTP password, a program with `environment` secrets, and a webhook with an `Authorization` header
  **When** `GET /api/v1/config` is requested by an authorized caller
  **Then** the response body contains none of the password value, no environment values, and no webhook header values; secret fields are omitted or masked (e.g. `"***"`)

- **Given** `HTTPServerConfig.Password` and `Username`
  **When** the config struct is JSON-encoded for any API response
  **Then** those fields are excluded (`json:"-"`) and never appear in output

- **Given** a webhook URL embedding credentials (`https://user:pass@host/path`)
  **When** the config is returned
  **Then** the userinfo component is stripped or masked

**Error Handling:**

| Error Condition                  | Expected Behavior                          | User-Facing Message |
| -------------------------------- | ------------------------------------------ | ------------------- |
| Redaction DTO construction fails | Return 500; do not fall back to raw config | "SERVER_ERROR"      |

**Edge Cases:**

- Redaction applies to every endpoint that returns config, not only `GET /api/v1/config` (e.g. any reload or diff response echoing config)
- Non-secret config fields (program names, commands, numprocs) remain visible so the endpoint stays useful
- A regression test asserts the response body contains none of the seeded secret values

**Dependencies:** FUNC-026

---

### SEC-015: Fail-Closed Authentication on the TCP Listener

**Description:** Enabling the TCP HTTP listener must require credentials for any bind address, loopback included. The 2026-07-09 review (MEDIUM, confidence 8) found `requireAuth` grants unauthenticated access whenever `authUser == ""`, while `StartTCP` is invoked on `HTTP.Enabled` alone with only a log warning on an all-interfaces bind. A config that sets `http.enabled = true` without a `username` exposes the entire control API — `start`/`stop`/`signal`, `stdin` write, `config/reload`, `shutdown` — with no authentication. Loopback is explicitly NOT treated as a trust boundary: in a shared network namespace (Kubernetes pods share `localhost` across all containers; `--network host` shares the host loopback) a `127.0.0.1`-bound API with no auth is reachable unauthenticated by any co-located container or local process. The password-free local path is the Unix domain socket (auth-skipped, filesystem-gated to the service UID at default mode 0700 — see FUNC-024 and SEC-022), so requiring credentials on all TCP binds does not impede local CLI use, which defaults to the socket and only uses TCP when `--addr` is passed. Auth must be fail-closed, not opt-in. Hardens FUNC-025 and FUNC-028 and enforces constitution Security Requirements 1 and 3.

**Acceptance Criteria:**

- **Given** `http.enabled = true` with any `listen` address (loopback or routable) and no username/password
  **When** the daemon starts
  **Then** startup fails with a clear configuration error and the TCP listener is not opened

- **Given** `http.enabled = true` bound to a loopback address (`127.0.0.1`/`::1`) with no credentials
  **When** the daemon starts
  **Then** startup fails identically to a routable bind — loopback receives no exemption

- **Given** `http.enabled = true` with a configured username and password
  **When** the daemon starts and a request arrives without valid credentials
  **Then** the listener starts and the request is rejected with 401

- **Given** the operator wants password-free local administration
  **When** they use the CLI without `--addr`
  **Then** it connects over the Unix socket and works with no credentials (unchanged), independent of whether TCP is enabled

**Error Handling:**

| Error Condition                           | Expected Behavior              | User-Facing Message                                  |
| ----------------------------------------- | ------------------------------ | ---------------------------------------------------- |
| TCP enabled (any address), no credentials | Refuse to start; exit non-zero | "http listener on {addr} requires username/password" |

**Edge Cases:**

- The Unix socket path is unaffected (its trust boundary is filesystem permissions, default 0700; see SEC-022)
- Cross-container control without a password is achieved by sharing the socket via a mounted volume, not by loopback TCP; loopback TCP for cross-container use still requires credentials
- The all-interfaces warning is retained in addition to the hard failure: config warnings are printed on stderr before the config error

**Dependencies:** FUNC-025

---

### SEC-016: Clean Environment for Lower-Privilege Child Processes

**Description:** A child that drops to a different user must not inherit the supervisor's environment by default. The 2026-07-09 review (MEDIUM, confidence 7) found `buildEnv` seeds every child with the supervisor's full `os.Environ()`, with `clean_environment` opt-in (default false). When the supervisor runs as root with secrets in its environment and a program is configured with `user = "<uid>:<gid>"`, the semi-trusted child inherits root's secrets (readable via `/proc/self/environ`) — a downward privilege-boundary crossing. Hardens FUNC-046 and enforces constitution Security Requirement 6.

**Acceptance Criteria:**

- **Given** the supervisor running with a distinctive env var (for example a variable named `ROOT_ONLY`) set and a program configured with a per-process `user` that differs from the supervisor
  **When** the child is spawned without explicit inheritance opt-in
  **Then** the child's environment does not contain `ROOT_ONLY`; it contains only a minimal base (PATH, HOME for the target user, `SUPERVISOR_*` vars) plus the program's explicit `environment` entries

- **Given** a program with no per-process `user` (runs as the supervisor)
  **When** it is spawned
  **Then** existing environment behavior is unchanged (backward compatible)

- **Given** a program that explicitly opts into inheritance
  **When** it is spawned
  **Then** it receives the supervisor's environment as before

**Error Handling:**

| Error Condition                         | Expected Behavior                       | User-Facing Message |
| --------------------------------------- | --------------------------------------- | ------------------- |
| Target user's home directory unresolved | Fall back to `/` for HOME; log at debug | n/a                 |

**Edge Cases:**

- Interaction with the existing `clean_environment` flag is defined: a differing `user` implies clean-by-default even when `clean_environment` is unset
- `SUPERVISOR_*` process-metadata vars are always injected regardless of clean mode

**Dependencies:** FUNC-046

---

### SEC-017: Drop Current-Directory Config Precedence for the Daemon

**Description:** The daemon must not load a config from an untrusted current working directory ahead of system paths. The 2026-07-09 review (hardening, confidence ~6) found `DefaultSearchPaths` lists `./kahi.toml` first, before `/etc/kahi/kahi.toml` and `/etc/kahi.toml`. If root launches `kahi daemon` without `-c`/`KAHI_CONFIG` from a directory an unprivileged local user can write to, that user plants `./kahi.toml` defining a program whose `command` runs as root — local privilege escalation that also shadows the correct system config. Hardens FUNC-015.

**Acceptance Criteria:**

- **Given** the daemon started as root with no `-c` and no `KAHI_CONFIG`, from a directory containing a `./kahi.toml`
  **When** config resolution runs
  **Then** the CWD-relative file is not selected; resolution uses the system search paths only (or fails if none exist)

- **Given** an explicit `-c ./kahi.toml` or `KAHI_CONFIG`
  **When** the daemon starts
  **Then** the explicitly requested path is honored (explicit intent is trusted)

**Error Handling:**

| Error Condition                              | Expected Behavior                   | User-Facing Message                        |
| -------------------------------------------- | ----------------------------------- | ------------------------------------------ |
| No config found in system paths, no explicit | Exit non-zero with search-path list | "no config file found; searched [{paths}]" |

**Edge Cases:**

- Non-daemon CLI subcommands may retain CWD-relative convenience lookup; the restriction targets the daemon's default search order
- Alternatively, honor `./kahi.toml` only when the process euid is non-zero and the file is owned by the invoking user — either policy is acceptable if enforced and tested

**Dependencies:** FUNC-015

---

### SEC-018: Remove Plaintext Password Fallback and Use Constant-Time Comparison

**Description:** Credential checking must not accept plaintext-stored passwords and must compare in constant time. The 2026-07-09 review (hardening) found `checkPassword` falls back to `plain == hash` for any non-`$2` hash prefix (a plaintext comparison, labeled "testing only" but reachable in production), and both `user != s.authUser` and the plaintext branch are non-constant-time, enabling username enumeration via timing. **Clarify decision (resolved):** hard-reject at startup — a non-bcrypt `http.password` fails config validation immediately (no deprecation window); this is an accepted breaking change for the plaintext "testing only" path, acceptable under the project's 0.x alpha semver. Enforces constitution Security Requirement 3 (bcrypt only, no plaintext storage).

**Acceptance Criteria:**

- **Given** a configured password that is not a bcrypt hash (`$2` prefix)
  **When** the daemon loads config
  **Then** it rejects the config at startup with an error (no plaintext password accepted)

- **Given** an authentication attempt
  **When** the username and password are compared
  **Then** both comparisons are constant-time (`subtle.ConstantTimeCompare` / `hmac.Equal`), leaking no timing signal that distinguishes a wrong username from a wrong password

**Error Handling:**

| Error Condition               | Expected Behavior | User-Facing Message                   |
| ----------------------------- | ----------------- | ------------------------------------- |
| Non-bcrypt password in config | Refuse to start   | "http.password must be a bcrypt hash" |

**Edge Cases:**

- Empty password with empty configured hash behavior is preserved only where intended (e.g. auth disabled); it must not become a bypass
- Test helpers that previously used plaintext passwords are updated to use bcrypt hashes

**Dependencies:** FUNC-028

---

### SEC-019: Extend Log-File Symlink Protection Beyond the Final Component

**Description:** Log-file opening should resist symlinked parent directories, not only the final path component. The 2026-07-09 review (hardening) noted SEC-012's `O_NOFOLLOW` guards only the last component, so a swapped parent-directory symlink can still redirect a root daemon's writes. Log paths come from trusted config, so this is defense-in-depth. Deliverable: open log files relative to a verified directory (e.g. `openat` with `O_NOFOLLOW` on each component, or resolve and validate the parent is not attacker-writable), or explicitly document and accept the trusted-path assumption in the constitution and code. Hardens SEC-012.

**Acceptance Criteria:**

- **Given** a log path whose parent directory is replaced with a symlink to an unintended location
  **When** the log file is opened
  **Then** the open either fails or resolves within the intended directory; writes are never redirected through the swapped parent

- **Given** a normal log path with no symlinked components
  **When** the log file is opened
  **Then** behavior is unchanged from SEC-012

**Error Handling:**

| Error Condition               | Expected Behavior                         | User-Facing Message                     |
| ----------------------------- | ----------------------------------------- | --------------------------------------- |
| A path component is a symlink | Open fails; process to FATAL / daemon err | "cannot open log file: {path}: {error}" |

**Edge Cases:**

- If the accepted resolution is to document the trusted-path assumption rather than harden, the decision is recorded in the plan as an ADR and the code comment is updated accordingly
- Behavior is consistent across process stdout/stderr capture and the daemon log file

**Dependencies:** SEC-012

---

### SEC-020: Length-Frame Event Listener Payloads

**Description:** Event listener payloads must be framed so process output cannot forge protocol lines. The 2026-07-09 review (hardening) found `formatEventPayload` emits `TYPE ts key:value` as an unframed newline-terminated line; a managed process whose output contains a newline (only untrusted under a process-compromise assumption) could inject a forged protocol line into a listener's stdin. Real supervisord length-prefixes the payload for exactly this reason. Hardens FUNC-055.

**Acceptance Criteria:**

- **Given** an event whose payload data contains an embedded newline or a line resembling a protocol header
  **When** the payload is written to an event listener's stdin
  **Then** it is length-prefixed (payload length announced, then exactly that many bytes) so the embedded content cannot be parsed as a new protocol line

- **Given** an ordinary single-line payload
  **When** it is delivered
  **Then** a conforming listener parses it identically to before (protocol remains compatible with the documented handshake)

**Error Handling:**

| Error Condition            | Expected Behavior              | User-Facing Message |
| -------------------------- | ------------------------------ | ------------------- |
| Listener stdin write fails | Listener marked failed; logged | n/a                 |

**Edge Cases:**

- The READY/RESULT handshake framing is preserved; only the payload body gains a length prefix
- Empty payloads are framed with length 0

**Dependencies:** FUNC-055, FUNC-051

---

### SEC-021: Apply Configured Resource Limits and Umask at Spawn

**Description:** Configured per-process rlimits and umask must actually be applied. The 2026-07-09 review (correctness/hardening) found `ExecSpawner.Spawn` silently ignores `SpawnConfig.RLimits`, and per-process `Umask` is never applied in the spawn path (`ApplyUmask`/`ApplyRLimits` have no non-test callers). Operators setting `NOFILE`/`NPROC` limits or a restrictive umask get no enforcement, weakening intended isolation. Hardens FUNC-068 and FUNC-047.

**Acceptance Criteria:**

- **Given** a program configured with a `NOFILE` (and/or `NPROC`) rlimit
  **When** the child is spawned
  **Then** the child process runs with that limit applied (verifiable via `/proc/<pid>/limits` or a getrlimit self-check in a test binary)

- **Given** a program configured with a `umask`
  **When** the child is spawned and creates a file
  **Then** the file mode reflects the configured umask

- **Given** a program with neither configured
  **When** it is spawned
  **Then** the child inherits the supervisor's limits/umask as before

**Error Handling:**

| Error Condition                        | Expected Behavior             | User-Facing Message                     |
| -------------------------------------- | ----------------------------- | --------------------------------------- |
| Invalid rlimit value in config         | Reject at config validation   | "invalid rlimit {name}: {value}"        |
| setrlimit/umask syscall fails at spawn | Spawn fails; process to FATAL | "cannot apply resource limits: {error}" |

**Edge Cases:**

- Limits are applied in the child (post-fork, pre-exec) via `SysProcAttr`/init, consistent with credential and umask handling
- Platform differences (Linux vs Darwin rlimit sets) are respected via the existing `rlimit_linux.go`/`rlimit_darwin.go` split

**Dependencies:** FUNC-068

---

### SEC-022: Lock the Control Socket to the Service Identity

**Description:** The Unix domain control socket is the password-free local administration path (auth is skipped for socket connections — `isUnixConn`), so its access control must be sound. The 2026-07-09 transport review found that `isUnixConn` trusts the transport rather than the peer (no `SO_PEERCRED`/`LOCAL_PEERCRED` check), so the daemon authorizes any process that can connect; and that `server.unix.chown` is dead config — a struct field present in the generated template and parsed by the migrator, but never applied at bind (only `chmod` is; no `os.Chown` anywhere), so an operator who sets it gets a silent no-op and a false sense of control. **Clarify decision (resolved):** the socket is locked to the service identity — owner-only, no multi-user sharing path. The daemon keeps the default mode 0700 owned by the service UID and **rejects the `server.unix.chown` field with a config error** rather than silently ignoring it (a socket shared by ownership/group is not supported, because without a peer-credential check it would grant unrestricted authority to every connecting process). Peer-credential (`SO_PEERCRED`/`getpeereid`) authorization is therefore not required now; it is recorded as a future extension to revisit only if controlled multi-user local administration is ever added. Enforces constitution Security Requirement 2 and hardens FUNC-024.

**Acceptance Criteria:**

- **Given** the default configuration
  **When** the daemon binds the control socket
  **Then** the socket is mode 0700, owned by the service UID, and a chmod failure closes the listener (no world-accessible socket is ever served) — existing behavior preserved

- **Given** a config that sets `server.unix.chmod` to a mode granting group or other access (e.g. `0770`, `0666`)
  **When** the daemon starts
  **Then** it rejects the configuration with an error explaining that the control socket must remain owner-only (the socket authorizes by transport, so a shared mode would grant unrestricted control)

- **Given** `server.unix.chown` set in config
  **When** the daemon starts
  **Then** the daemon refuses to start with a clear error — the field is never silently ignored

**Error Handling:**

| Error Condition                        | Expected Behavior                | User-Facing Message                                        |
| -------------------------------------- | -------------------------------- | ---------------------------------------------------------- |
| `server.unix.chown` present in config  | Refuse to start                  | "server.unix.chown is not supported; socket is owner-only" |
| `server.unix.chmod` grants group/other | Refuse to start                  | "control socket must be owner-only (0700); got {mode}"     |
| `os.Chmod` fails at bind               | Close the listener; do not serve | "cannot set socket permissions: {path}: {error}"           |

**Edge Cases:**

- The migrator (`internal/migrate`) must not emit `chown` into generated config, and the generated template must drop the commented `chown` example, so migration output stays loadable under the new rule
- Rejecting a group/other-accessible `chmod` is the enforcement mechanism for "lock to the service id"; a future peer-credential check would be the prerequisite before any shared mode could be permitted
- This does not change the SEC-015 rule that TCP always requires credentials; this feature governs the Unix socket transport only

**Dependencies:** FUNC-024

---

### SEC-023: Bind Unix Sockets Owner-Only From Creation

**Description:** Unix sockets must never exist, even briefly, with wider permissions than intended. The 2026-10-18 whole-project review found that the control socket (`internal/api` `StartUnix`) and the FastCGI socket (`internal/fcgi` `Open`) are created by `net.Listen("unix", ...)` and only afterwards narrowed with `os.Chmod`. Kahi never sets its own umask, so the socket is born at `0777 &^ inherited-umask`. Under the common inherited umask `022` the result is `0755`, which is not exploitable because connecting to a Unix socket requires write permission. Under a permissive inherited umask (any umask with the `002` or `020` bit clear) other local users or group members can connect in the window between `bind()` and `chmod()`, and a connection accepted in that window survives the later chmod. On the control socket that would be a full unauthenticated control session, because Unix-socket peers skip authentication (`isUnixConn`). The review rated this below the reporting threshold (it needs a non-default umask and a microsecond window at daemon start); this feature removes the dependence on the inherited umask as defense-in-depth. Deliverable: create every Unix socket under a restrictive umask so it is owner-only at the moment of creation, then apply the configured mode as today. The FastCGI socket was covered too when this was written; FastCGI has since been descoped and `internal/fcgi` removed (FUNC-074/075), so this now governs the control socket. Enforces constitution Security Requirement 2 and hardens SEC-022.

**Acceptance Criteria:**

- **Given** a Unix platform (`//go:build unix`) and a daemon process running with a permissive umask (e.g. `000`)
  **When** a Unix socket is created, before any chmod is applied
  **Then** the socket inode has no group or other bit set at creation

- **Given** a socket is created under the restrictive umask
  **When** creation completes, successfully or with an error
  **Then** the process umask is restored to its previous value, so files created afterwards are unaffected

- **Given** a second socket creation begins while a first is between setting the temporary umask and binding
  **When** both creations complete
  **Then** the second does not enter its critical section until the first has restored the umask, and the final process umask equals the original value

- **Given** an inherited umask stricter than `077` (e.g. `0277`)
  **When** a socket is created and any other file is created concurrently by another goroutine
  **Then** neither is created with a wider mode than the inherited umask alone would produce

**Error Handling:**

| Error Condition                 | Expected Behavior                    | User-Facing Message                              |
| ------------------------------- | ------------------------------------ | ------------------------------------------------ |
| Control socket `bind()` fails   | Restore umask; return the bind error | "cannot create socket: {path}: {error}"          |
| Control socket `os.Chmod` fails | Close the listener (unchanged)       | "cannot set socket permissions: {path}: {error}" |

**Edge Cases:**

- The temporary umask is the inherited umask with `077` added (`inherited | 077`), never a fixed value, so it can only add restriction. A file created by another goroutine during the masked window may get a more restrictive mode than requested (a log file opened `0644` under inherited `022` is created `0600`), but never a wider one than the inherited umask would give. This is accepted and recorded in ADR-026
- Set, bind and restore are serialized by a lock so concurrent socket creations cannot leave the daemon at the temporary umask. The lock serializes callers of the socket helper only; the one other `umask` call site (`process.ApplyUmask`, used in `childinit.go`) runs in the re-exec'd child process, not the daemon, so it cannot interleave
- On `!unix` builds the helper is plain `net.Listen` and this guarantee does not apply; no shipped target is `!unix` (GoReleaser builds linux and darwin only)

**Dependencies:** FUNC-024, SEC-022

---

### SEC-024: Socket Directory Probe Must Not Follow Symlinks

**Description:** The socket directory writability check must not be usable to truncate files through a planted symlink. `ValidateSocketPermissions` (`internal/supervisor/daemon.go`) runs as the launching user before any privilege drop (so as root when the daemon is launched as root and drops to `--user` later), and probes the socket directory with `os.Create(dir + "/.kahi_perm_check")`. That open uses `O_TRUNC`, a fixed predictable name and no `O_EXCL` or `O_NOFOLLOW`, so a symlink planted at that name is followed and its target truncated as root. The review rated this below the reporting threshold: the default socket directory `/var/run` is not attacker-writable, the socket path comes from trusted config, and Linux `fs.protected_symlinks` blocks the attack in sticky world-writable directories such as `/tmp`. It remains reachable if an operator points the socket at a non-sticky attacker-writable directory, and on macOS, which has no equivalent protection. This applies the SEC-019 principle (root must not follow attacker-planted symlinks) to the probe. Deliverable: probe with an exclusive, randomly named file so no existing path is ever opened or truncated. The same function also extracts the parent directory by hand, with two bugs: a socket path such as `/kahi.sock` resolves to `.` and probes the current directory instead of `/`, and a bare relative name such as `kahi.sock` (no `/`) leaves the socket path itself as the "directory", so validation fails with "socket directory does not exist" for a valid relative path. Both are corrected by using the real parent directory. Hardens FUNC-024; extends SEC-019.

**Acceptance Criteria:**

- **Given** a symlink at `{socket-dir}/.kahi_perm_check` pointing to an existing file with content
  **When** the socket directory is validated
  **Then** the target file's content is unchanged and the symlink itself is not removed or followed

- **Given** a writable socket directory
  **When** the socket directory is validated
  **Then** validation succeeds and leaves no probe file behind

- **Given** a socket directory the daemon cannot write to
  **When** the socket directory is validated
  **Then** validation fails with the existing permission-denied error

- **Given** a socket path directly under `/` (e.g. `/kahi.sock`)
  **When** its directory is derived
  **Then** the directory is `/`, not the current working directory

- **Given** a bare relative socket name (e.g. `kahi.sock`)
  **When** its directory is derived
  **Then** the directory is `.` and validation of a writable working directory succeeds

**Error Handling:**

| Error Condition                     | Expected Behavior | User-Facing Message                                         |
| ----------------------------------- | ----------------- | ----------------------------------------------------------- |
| Probe file cannot be created        | Refuse to start   | "permission denied: cannot create socket in {dir}: {error}" |
| Stat of socket directory fails      | Refuse to start   | "socket directory does not exist: {dir}: {error}"           |
| Socket directory is not a directory | Refuse to start   | "socket path parent is not a directory: {dir}"              |

**Edge Cases:**

- A random probe name created with `O_EXCL` cannot collide with or follow a planted path, so the fix does not depend on `fs.protected_symlinks` and holds on macOS
- If removing the probe file fails, validation still succeeds: the directory is already proven writable, and the leftover file has a random name that nothing else opens. This matches the existing behavior, which also ignores the remove error. Removing by name is safe even if the file were swapped for a symlink, because `unlink(2)` removes the link itself and never follows it
- Log rotation's `os.Truncate` in `internal/logging/rotation.go` also follows symlinks, but its `backups == 0` branch is unreachable from config (`ApplyDefaults` replaces `0` with `10`) and the log path is trusted config; it is out of scope here and recorded as a residual. Note that the same defaulting means an operator cannot request zero backups, which supervisord allows; that is a separate compatibility gap, not addressed here

**Dependencies:** FUNC-024, SEC-019

---

### SEC-025: Reject Cross-Origin State-Changing API Requests

**Description:** The REST API must reject state-changing requests that a browser sends on behalf of another site. This was found on 2026-10-18 while analysing v0.1.4's exposure. The API performs no `Origin` or `Sec-Fetch-Site` check. Process and group `start`, `stop` and `restart`, `config/reload` and `shutdown` read no request body, and `signal` and `stdin` decode JSON without checking `Content-Type`, so a cross-site HTML form is enough to trigger any of them (`enctype="text/plain"` produces a body that decodes as JSON for the two JSON routes). When the TCP listener is enabled (it is off by default) and a user has authenticated to the API in a browser, the browser caches the HTTP Basic credentials for that origin and attaches them to cross-site requests. A malicious page the user visits can then start, stop, restart or signal processes and groups, write to process stdin, reload the configuration, or shut the daemon down, stopping every supervised process. Browsers cannot connect to the Unix socket directly; but if an operator puts an HTTP reverse proxy in front of it, the socket path has no authentication at all, and only this protection stops a cross-site request there. Deliverable: wrap the API handler in the Go standard library's `http.CrossOriginProtection`, with a JSON deny response consistent with the rest of the API. Hardens FUNC-026 and FUNC-028.

**Acceptance Criteria:**

- **Given** the TCP listener with valid Basic credentials
  **When** `POST /api/v1/processes/{name}/stop` arrives carrying `Sec-Fetch-Site: cross-site`
  **Then** the response is `403` with JSON body `{"error": "cross-origin request rejected", "code": "FORBIDDEN"}`, and the process is not stopped

- **Given** an unsafe-method request with an `Origin` whose host[:port] differs from the `Host` header (a port mismatch counts), and no `Sec-Fetch-Site` header
  **When** it reaches the API
  **Then** it is rejected with the same `403` JSON response

- **Given** an unsafe-method request carrying `Sec-Fetch-Site: same-origin` or `Sec-Fetch-Site: none`
  **When** it reaches the API
  **Then** it is processed normally

- **Given** an unsafe-method request with neither `Origin` nor `Sec-Fetch-Site` (`kahi ctl`, `curl`, scripts)
  **When** it reaches the API
  **Then** it is processed normally, so non-browser clients are unaffected

- **Given** a cross-site `GET` or `HEAD`, including the SSE log and event streams
  **When** it reaches the API
  **Then** it is processed normally (safe methods are always allowed)

- **Given** a cross-site `POST /api/v1/shutdown` or `POST /api/v1/groups/{name}/stop`
  **When** it reaches the API
  **Then** it is rejected with `403 FORBIDDEN` and the daemon or group is unaffected

- **Given** a cross-site unsafe-method request with _invalid_ Basic credentials
  **When** it reaches the API
  **Then** the response is `403 FORBIDDEN`, not `401`: the check runs before authentication

- **Given** the Unix and TCP servers are both started
  **When** a cross-site `POST /api/v1/processes/{name}/stop` is sent to each over a real connection
  **Then** each returns `403 FORBIDDEN` (a behavioural check: handler identity cannot be compared, because the protected handler is an `http.HandlerFunc`)

**Error Handling:**

| Error Condition                            | Expected Behavior                                 | User-Facing Message                                               |
| ------------------------------------------ | ------------------------------------------------- | ----------------------------------------------------------------- |
| Cross-origin request with an unsafe method | `403`; the request is never dispatched to a route | `{"error": "cross-origin request rejected", "code": "FORBIDDEN"}` |

**Edge Cases:**

- Safe methods (`GET`, `HEAD`, `OPTIONS`) are always allowed, so the protection is only sound while no safe-method handler changes state. All ten `GET` handlers were verified read-only on 2026-10-18. Any future `GET` handler must stay read-only
- The check runs before authentication. A rejected cross-origin request gets `403` whatever its credentials, and reveals nothing about whether they were valid
- Behind a reverse proxy that rewrites `Host`, modern browsers still send `Sec-Fetch-Site: same-origin` for same-origin requests, which is allowed. Only browsers that lack `Sec-Fetch-Site` fall back to comparing `Origin` with `Host`, and could be denied. No trusted-origin setting is added now; `AddTrustedOrigin` is the extension point if the 0.2.x Web UI needs it
- The Unix socket serves the same handler. `kahi ctl` sends neither header, so it is unaffected; and behind a reverse proxy, where the socket path skips authentication, the protection is the only thing that stops a cross-site request
- No route accepts `OPTIONS`; `ServeMux` answers `405`, so allowing `OPTIONS` as a safe method bypasses nothing

**Dependencies:** FUNC-025, FUNC-026, FUNC-028

---

### SEC-026: Web UI Authentication and Output Encoding

**Description:** Requirements for making the Web UI reachable (FUNC-084) without opening new exposure; they are met before the UI is mounted. Found by inventory on 2026-10-18: `web.RegisterRoutes` registers bare handlers with no authentication, and `static/app.js` builds per-process buttons with `innerHTML` and inline `onclick="doAction('…')"` handlers, using an `esc()` helper that does not escape `'`, so a process name containing `'` breaks out of the JavaScript string. `log.html` builds the SSE URL from the process name without `encodeURIComponent`. None of this was reachable while nothing mounted the UI; the 2026-10-18 security review's "XSS not reachable" verdict held only for that reason. Process names come from the config (operator-controlled), so this is defense-in-depth, not an exploitable vulnerability; it is still fixed before the UI ships.

**Acceptance Criteria:**

- **Given** the UI is mounted with TCP credentials
  **When** any path under `/ui/` is requested without credentials
  **Then** it returns `401`. Authentication holds by construction (one `requireAuth` wrap around the whole subtree, FUNC-084), so this criterion is verified by FUNC-084's tests: at the API mux with a stub UI handler and arbitrary paths (`/ui/`, `/ui/a/b`, `/ui/static/zzz`), and with the real UI handler over TCP for `/ui/`, `/ui/log/web/stdout`, `/ui/static/app.js`, `/ui/static/log.js` and `/ui/static/style.css`

- **Given** a process named `a'b"<c>&d`
  **When** the status page is rendered server-side
  **Then** the raw HTML contains its escaped form `a&#39;b&#34;&lt;c&gt;&amp;d` in text and in `data-name`, and never the raw name or an unescaped `<c>` (the Tail `href` holds a separately URL-encoded form)

- **Given** a process named `x y/#?`
  **When** the status page renders its Tail links
  **Then** the `href` values are `/ui/log/x%20y%2F%23%3F/stdout` and `/ui/log/x%20y%2F%23%3F/stderr`

- **Given** the shipped `app.js` and `log.js`
  **When** they are checked
  **Then** every `/api/v1/processes/` and `/ui/log/` URL they build (including the Tail links `app.js` rebuilds on refresh) applies `encodeURIComponent` to the process name (from `dataset.name`, or from the API JSON for rows rebuilt on refresh)

- **Given** the shipped templates and scripts
  **When** they are scanned
  **Then** they contain no inline event-handler attributes (`onclick=`, `onload=`, `onerror=`), no `innerHTML`, `outerHTML` or `insertAdjacentHTML`, no `eval(` or `new Function`, no `style=` attribute, no `<style` element, no `data:` URI, and no `<script>` element without `src`

- **Given** any Go code in `internal/web`
  **When** it is parsed
  **Then** it contains no conversion to `template.HTML`, `template.JS`, `template.URL` or `template.HTMLAttr`

- **Given** any response written by `web.Handler`, including its 400 and 404 pages
  **When** its headers are inspected
  **Then** it carries `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'` and `X-Content-Type-Options: nosniff`; with `script-src 'self'`, any inline script or handler that slips in is blocked by the browser, and `default-src 'self'` allows the favicon at `/ui/static/favicon.svg`

**Error Handling:**

| Error Condition                  | Expected Behavior                    | User-Facing Message |
| -------------------------------- | ------------------------------------ | ------------------- |
| UI route requested without creds | `401` with `WWW-Authenticate: Basic` | (browser prompt)    |

**Edge Cases:**

- Event handlers are attached with `addEventListener`; the process name is carried in a `data-*` attribute rendered by `html/template` or set through `dataset`, never concatenated into markup
- Templates contain no inline `<script>` or `<style>` blocks and no `style=` attributes, so the CSP needs no `'unsafe-inline'` or nonce. Today `log.html` has an inline `<script>` block (line 31), an inline `data:` favicon (line 8), and `index.html` has inline `onclick` handlers (lines 49-51); the log viewer's script moves to `/ui/static/log.js`, and the process name and stream reach it through `data-*` attributes on a server-rendered element
- CSRF needs no separate design: the UI is served from the API's origin and its XHR POSTs are same-origin, which SEC-025's `CrossOriginProtection` allows; cross-site POSTs stay rejected
- `frame-ancestors 'none'` prevents clickjacking of the action buttons
- XSS review (performed and recorded under TEST-006, not a gate for this feature): once the UI is mounted, the implementing session reviews `internal/web` (templates, `app.js`, `log.js`, handlers) for any path where request or process data reaches markup, a URL or a script without the encodings above, and records the verdict and any findings in the PR description. The automated scans above are the gate; the review is a recorded check, not a human approval step

**Dependencies:** FUNC-028, FUNC-078, FUNC-079, FUNC-080, SEC-025

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

**Timeouts and Polling:**

- Readiness polling stages: socket existence (5s, 100ms interval), health endpoint (3s, 50ms interval), process state (5s, 50ms interval)
- Cumulative readiness polling shall not exceed 30s before test failure
- Each E2E test sets a `context.WithTimeout` of 60s covering startup, execution, and cleanup
- `TestMain` sets a suite-wide timeout of 10 minutes as fallback
- `waitForState` polls at 50ms intervals; on timeout calls `t.Fatal()` with: "timeout waiting for process {name} to reach {state}; last state was {observed}"

**Cleanup Escalation:**

- Test cleanup sends shutdown command with 5s timeout
- If daemon process is still alive after 5s, send SIGKILL with 2s timeout
- If SIGKILL timeout expires, log warning and continue (do not block other test cleanup)
- Socket file and temp directory are always cleaned up via `t.Cleanup`

**Acceptance Criteria:**

- [ ] E2E tests start a real Kahi daemon, configure real programs (e.g., `sleep`, `echo`)
- [ ] Tests verify process start, stop, restart, autorestart, and shutdown behavior
- [ ] Tests verify API endpoints return correct status
- [ ] Tests verify CLI commands produce expected output
- [ ] E2E tests run in isolated temp directories
- [ ] `task test-e2e` runs end-to-end tests specifically
- [ ] E2E tests have a timeout to prevent hanging on failures

**Dependencies:** TEST-002

#### E2E Test Suite: 11 files, 68 tests, 9 domains

**Known Skips (2):**

- `TestDaemon_Daemonize` -- Go runtime cannot safely fork after goroutines start; skipped with `t.Skip`
- `TestAuth_TCPWithCreds` -- requires TCP port discovery not yet implemented; skipped with `t.Skip`

| File                           | Coverage area                                                                      | Tests |
| ------------------------------ | ---------------------------------------------------------------------------------- | ----- |
| `e2e/helpers_test.go`          | TestMain (build binary), shared startDaemon/waitForState/getProcessInfo helpers    | --    |
| `e2e/daemon_lifecycle_test.go` | Startup, health, version, PID, readiness, shutdown, daemonize flag                 | 8     |
| `e2e/process_ctl_test.go`      | Start/stop/restart, status, signal, error paths (bad name, already running)        | 15    |
| `e2e/process_state_test.go`    | State transitions: autorestart, backoff->fatal, expected/unexpected exit codes     | 10    |
| `e2e/group_ctl_test.go`        | Group start/stop/restart, group:\* syntax, priority ordering                       | 6     |
| `e2e/config_reload_test.go`    | SIGHUP reload, add/remove/change programs, reread preview                          | 6     |
| `e2e/logging_test.go`          | Tail stdout/stderr, tail -f (SSE), log rotation, ANSI stripping                    | 6     |
| `e2e/stdin_attach_test.go`     | WriteStdin, attach (if feasible in test)                                           | 3     |
| `e2e/env_test.go`              | env passthrough, clean_environment, program-overrides-global, variable expansion   | 5     |
| `e2e/auth_test.go`             | TCP mode with basic auth, rejected without creds                                   | 3     |
| `e2e/regression_test.go`       | Ported supervisord regressions: Unicode tail, literal %, numprocs, redirect_stderr | 8     |

#### Shared Helpers (`e2e/helpers_test.go`)

- `startDaemon(t, config string) (*ctl.Client, func())` -- Writes config to temp dir, starts daemon subprocess, polls until healthy, returns client and cleanup function.
- `waitForState(t, client, name, state string, timeout time.Duration)` -- Polls process state at 50ms intervals until condition met; calls `t.Fatal("timeout waiting for process {name} to reach {state}; last state was {observed}")` on expiry.
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

- `TestGroup_StartAll` -- Start group:\* starts all members
- `TestGroup_StopAll` -- Stop group:\* stops all members
- `TestGroup_RestartAll` -- Restart group:\* restarts all members
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

| supervisord E2E test            | Kahi disposition                                                       |
| ------------------------------- | ---------------------------------------------------------------------- |
| test_stdout_capturemode         | Ported -> TestLog_TailStdout                                           |
| test_stderr_capturemode         | Ported -> TestLog_TailStderr                                           |
| test_tail_follow                | Ported -> TestLog_TailFollow                                           |
| test_unicode_stdout             | Ported -> TestRegression_UnicodeTail                                   |
| test_start_stop                 | Ported -> TestProcess_Start, TestProcess_Stop                          |
| test_restart                    | Ported -> TestProcess_Restart                                          |
| test_signal                     | Ported -> TestProcess_Signal                                           |
| test_autorestart                | Ported -> TestState_AutorestartTrue                                    |
| test_environment                | Ported -> TestEnv_Passthrough                                          |
| test_update (add/remove)        | Ported -> TestReload_AddProgram, TestReload_RemoveProgram              |
| test_shutdown                   | Ported -> TestDaemon_Shutdown                                          |
| test*eventlistener*\* (6 tests) | Deferred until event listener pools are wired (FUNC-055, #106, v0.3.0) |
| test*xmlrpc*\* (5 tests)        | Skipped -- Python XML-RPC specific, not applicable to REST API         |

---

### TEST-004: Test Output Formatting with gotestsum

**Description:** Replace raw `go test -v` output with `gotestsum` for both local development and CI. All test targets produce JUnit XML and use testdox formatting. CI workflows render 3 separate `dorny/test-reporter` checks (Unit, Integration, E2E) so PR reviewers can triage failures by category.

**Acceptance Criteria:**

- [ ] `gotestsum` is added to the setup task in Taskfile.yml
- [ ] `task test` uses `gotestsum` with `--format testdox` and `--junitfile unit-results.xml`
- [ ] `task test-integration` uses `gotestsum` with `--format testdox` and `--junitfile integration-results.xml`
- [ ] `task test-e2e` uses `gotestsum` with `--format testdox` and `--junitfile e2e-results.xml`
- [ ] `task coverage` uses `gotestsum` with `--format testdox` and `--junitfile unit-results.xml`
- [ ] CI `ci.yml` workflow has `dorny/test-reporter` step producing "Unit Test Results" check from `unit-results.xml`
- [ ] CI `integration.yml` workflow has 2 `dorny/test-reporter` steps producing "Integration Test Results" and "E2E Test Results" checks
- [ ] All 3 test reporter checks appear on PR checks tab
- [ ] Local runs show live progress (test name + pass/fail as each completes)
- [ ] Failing tests show full output inline (not suppressed by formatting)
- [ ] JUnit XML files are listed in `.gitignore`

**Error Handling:**

| Scenario                   | Behavior                                                             |
| -------------------------- | -------------------------------------------------------------------- |
| `gotestsum` not installed  | `task setup` installs it; CI installs it in setup step               |
| JUnit XML write fails      | CI step logs warning but does not fail the build                     |
| Test reporter action fails | Non-blocking; test pass/fail still determined by gotestsum exit code |

**Dependencies:** TEST-003

---

### TEST-005: PR Test-Result Comment Tiles with Opt-In Full Per-Test Table

**Description:** Each CI test surface posts a sticky pull-request comment ("tile") summarizing its results, keyed by a per-surface `header` via `marocchino/sticky-pull-request-comment` so repeated pushes update the same comment in place. Three tiles are posted: Unit (from `ci.yml`), Integration and E2E (from `integration.yml`). Each tile shows a pass/skip/fail summary table rendered by the shared `.github/scripts/build-test-comment.go` (stdlib `encoding/xml`; excluded from the module build by living under a dot directory), collapsed inside a `<details>` twisty on a green run and auto-expanded on failure. As merged (PR #51), the twisty carries only the failing and skipped tests — deliberately, to keep the comment compact for the ~600-test unit suite. This feature adds an **opt-in full per-test table**: reviewers who want the complete list of every test with a color-coded status can enable it without bloating the default comment. Because GitHub markdown has no arbitrary text color, status color is conveyed with the same emoji indicators used in the summary tile (✅ passed, ⚠️ skipped, ❌ failed). **Clarify decisions (resolved):** the opt-in is a **`full-test-report` PR label** — the renderer checks for it at tile-render time via the `pull_request` event's label set, and adding the label (a `pull_request: labeled` trigger) re-runs the workflow so the tiles regenerate with the full table; and the feature is **scoped to GitHub Actions only** (Kahi's CI platform), with GitLab/Jenkins remaining documentation mapping guides, not implemented targets.

**Acceptance Criteria:**

- **Given** a CI run on a same-repo pull request
  **When** each test surface completes
  **Then** its sticky tile is posted/updated with the summary table, collapsed on green and expanded on failure, and the existing three-tile behavior (Unit, Integration, E2E) is unchanged

- **Given** full-table mode is NOT opted in
  **When** the tile is rendered
  **Then** the twisty contains only failures and skipped tests plus an "All N passed" line on green (current default behavior preserved)

- **Given** the `full-test-report` label is present on the PR
  **When** the workflow runs (including the re-run triggered by adding the label) and the tile is rendered
  **Then** the twisty contains a table of every test with columns Test (name), Result (✅/⚠️/❌ + word), and Duration, ordered failures → skipped → passed, with all rows color-coded by status emoji

- **Given** the `full-test-report` label is added to an open PR
  **When** the `pull_request: labeled` event fires
  **Then** the workflow re-runs and updates the sticky tiles in place with the full table (no new comments are created)

- **Given** a suite large enough that the full table would exceed GitHub's comment size limit (65536 chars)
  **When** the full table is rendered
  **Then** the renderer truncates deterministically (all failures and skipped always included; passed rows capped) and emits an explicit "N passed rows omitted — see run log" note rather than silently dropping rows

**Error Handling:**

| Error Condition                        | Expected Behavior                                    | User-Facing Message                   |
| -------------------------------------- | ---------------------------------------------------- | ------------------------------------- |
| JUnit XML missing/unparseable          | Post a "no results" tile; do not fail the build      | "no results" tile body                |
| Dependabot / fork PR (read-only token) | Post step is best-effort (`continue-on-error`); skip | n/a (posting silently degrades)       |
| Full table exceeds comment size limit  | Truncate passed rows with an explicit omission note  | "N passed rows omitted — see run log" |

**Edge Cases:**

- The `full-test-report` label controls all three surfaces uniformly (one label drives the Unit, Integration, and E2E tiles)
- Adding the label re-runs the test suites (the `labeled` event triggers a fresh run); rendering the full table from a retained JUnit artifact without re-running is a possible optimization deferred to the plan phase, not required here
- Removing the label does not retroactively rewrite an existing tile; the next push (or re-run) renders the compact form again
- Test names may contain markdown/backticks; the renderer escapes or code-spans them so the table cannot break formatting or inject HTML (GitHub sanitizes comment HTML regardless)
- On green with full-table opted in, the twisty stays collapsed by default (opt-in changes contents, not the collapse-on-green behavior)
- The renderer remains excluded from `task build`/`vet`/`lint`/`coverage` (dot-directory placement); a unit test may exercise it via `go run` with fixture JUnit files

**Dependencies:** TEST-004

---

### TEST-006: Web UI End-to-End Reachability

**Description:** Prove that the Web UI is reachable from the real daemon, not only from `internal/web`'s package tests. The four Web UI features were once marked passing on package tests alone while nothing imported the package; this test is the bar for marking FUNC-078..080, FUNC-083, FUNC-084, SEC-026 and STYLE-001 as passing. It boots the `kahi` binary as a real daemon process (not an in-process handler) and talks to it over real TCP and Unix-socket connections.

**Acceptance Criteria:**

- **Given** a config with `[server.http]` enabled (bcrypt credentials) on a free port from `testutil.FreeTCPPort`, `[server.web] enabled = true`, and one program `sleeper`
  **When** the daemon is started, its TCP port accepts connections, and `GET /ui/` is sent over TCP with valid credentials
  **Then** the response is `200`, `Content-Type: text/html`, and the body contains `sleeper`

- **Given** the same daemon
  **When** `GET /ui/` is sent over TCP without credentials
  **Then** the response is `401`

- **Given** the same daemon
  **When** `GET /ui/` is sent over its Unix socket without credentials
  **Then** the response is `200` HTML containing `sleeper`

- **Given** the same daemon
  **When** `GET /ui/static/app.js` and `GET /ui/static/style.css` are sent with credentials
  **Then** each is `200` with the correct `Content-Type` and a non-empty `ETag`

- **Given** the same daemon
  **When** `GET /ui/log/sleeper/stdout` is sent with credentials
  **Then** the response is `200` HTML

- **Given** the same daemon
  **When** `POST /api/v1/all/stop` and then `POST /api/v1/all/start` are sent with credentials
  **Then** `sleeper` is STOPPED and then RUNNING again, as seen by `GET /api/v1/processes`

- **Given** a config with `[server.web]` absent
  **When** the daemon is started and `GET /ui/` is sent with credentials
  **Then** the response is `404`

- **Given** `[server.web] enabled = true` with `[server.http]` disabled
  **When** the binary is run with a 10-second context timeout and its stderr captured
  **Then** it exits non-zero (`*exec.ExitError`) and stderr contains `server.web.enabled requires server.http.enabled`

**Error Handling:**

| Error Condition                   | Expected Behavior                                                                      | User-Facing Message |
| --------------------------------- | -------------------------------------------------------------------------------------- | ------------------- |
| Daemon fails to start             | Test fails; the daemon's stderr appears in the test output (`startDaemon` forwards it) | n/a                 |
| TCP port not accepting within 10s | Test fails with the address it waited on                                               | n/a                 |

**Edge Cases:**

- Lives in `e2e/` (`//go:build e2e`), run by `integration.yml` via `task test-e2e`, on the same Go toolchain as the release build
- Uses the existing `startDaemon` helper, which keeps its temp dir short (`os.MkdirTemp("", "k-")`) to stay under the 104-byte Unix socket path limit on macOS, and stops the daemon in `t.Cleanup`; `t.TempDir` is not used for the socket. A new helper runs the binary for the invalid-config case with stderr captured into a buffer; it writes the whole config itself, including `[supervisor]` and a `[server.unix] file` inside its own short temp dir, because it does not go through `startDaemon`'s config injection
- The HTTP client has a 10-second timeout, so no request can hang the suite
- Browser-side JavaScript behaviour (encoding, notifications) is covered by SEC-026's static checks and package tests, not by this test; no headless browser is added
- After this test passes, the implementing session performs SEC-026's XSS review of `internal/web` and records the verdict in the PR description

**Dependencies:** TEST-003, FUNC-083, FUNC-084, SEC-026

---

## Non-Functional Requirements

### Performance

- Daemon startup time (cold start, no processes): < 100ms
- Process start latency (time from API request received to child process begins execution, post-fork before exec): < 50ms
- API response time for status query (100 processes): < 10ms
- Memory overhead per managed process: < 1MB
- SSE streaming latency (log line to client): < 100ms
- Config reload time (100 program definitions): < 500ms

**Measurement methodology:** All latency thresholds are 99th percentile, measured on an idle system. Performance tests run 100 iterations, report mean/median/p99. Test fails if p99 exceeds threshold, reporting measured values.

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
- Go: 1.26.2+
- Static binary, no shared library dependencies
