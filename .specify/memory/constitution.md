# Project Constitution

This document defines immutable principles for the project. These principles
govern all development activity, including autonomous Claude Code sessions.
Once established, these principles do not change without explicit human approval.

## Project Identity

**Project Name:** Kahi -- Lightweight process supervisor for modern infrastructure
**One-Line Description:** A modern, lightweight process supervisor for POSIX systems, written in Go.
**Canonical Repository:** github.com/kahiteam/kahi
**Primary Language(s):** Go (1.26.0+)
**Target Platform(s):** Linux (amd64, arm64), macOS (amd64, arm64)

## Non-Negotiable Principles

1. Zero runtime dependencies -- single static binary, no external libraries required at runtime
2. POSIX-only -- no Windows support, no abstraction layers for cross-platform compatibility
3. Backwards-compatible config migration -- provide a tool to convert supervisord.conf to kahi.toml
4. Process isolation -- child processes must never receive signals intended for the supervisor
5. Graceful degradation -- if a subsystem fails (metrics, web UI), core supervision continues
6. No unsafe operations -- no use of Go's unsafe package outside of stdlib

## Quality Standards

### Testing

- Minimum code coverage: 85%
- Test framework: Go testing package + testify for assertions
- Test categories required: unit, integration, end-to-end

### Code Style

- Linter: golangci-lint
- Formatter: gofmt
- Type checking: go vet (strict mode: yes)

### Commit Standards

- Format: Conventional Commits (feat, fix, docs, etc.)
- No emoji in commit messages or PR titles
- No AI-isms or self-referential language
- No Co-Authored-By trailers
- Subject line maximum: 72 characters

### Communication Style

- Tone: Technical and terse
- Forbidden patterns: emoji, marketing adjectives, filler words, self-referential language ("I have", "certainly", "seamless", "robust", "elegant")

## Architectural Constraints

1. Single binary -- `kahi` serves as daemon, CLI, and migration tool via subcommands (`kahi daemon`, `kahi ctl`, `kahi migrate`)
2. Configuration via TOML only -- no YAML, no JSON, no INI. One format, no ambiguity
3. REST/JSON API with SSE for streaming -- no gRPC in initial release, no XML-RPC compatibility
4. Feature toggles via config -- all optional subsystems (HTTP API, metrics, web UI, events) enabled/disabled in kahi.toml, not at compile time
5. No CGO -- pure Go for maximum cross-compilation simplicity
6. Go 1.26.0 minimum -- use latest stdlib features (structured logging, enhanced HTTP routing)
7. FIPS 140 enforcing build via GOFIPS140=v1.0.0 -- all cryptographic operations use Go's FIPS-validated module

## Security Requirements

1. Unix socket communication by default -- TCP listener disabled unless explicitly enabled in config
2. Socket file permissions enforced -- default 0700, configurable chmod and chown
3. HTTP Basic Auth with bcrypt-hashed passwords -- no plaintext password storage in config
4. User/group switching -- daemon can drop privileges; child processes can run as specified user
5. No shell execution -- child processes are exec'd directly, never via sh -c, to prevent injection
6. Environment variable sanitization -- clean_environment option controls inheritance; when enabled, only explicitly configured env vars are passed to child processes
7. FIPS 140 compliant cryptography -- all crypto operations use Go's FIPS-validated module

## Out of Scope

1. Windows support -- POSIX only
2. Container orchestration -- manages processes, not containers
3. Remote multi-host management -- single host only
4. XML-RPC compatibility -- no backwards-compatible API for supervisord clients
5. Go plugin loading -- no shared library or go-plugin based extension (event listeners and webhooks cover extension needs)
6. Service mesh / service discovery -- not a replacement for Consul, etcd, etc.
7. Python interop -- no Python runtime dependency or compatibility layer
