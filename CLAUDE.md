# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Description:** Kahi -- lightweight process supervisor for modern infrastructure
**Tech Stack:** Go 1.26.0, cobra, BurntSushi/toml, stdlib slog
**Repository:** github.com/kahiteam/kahi

## Development Workflow

### Subagent Policy

The main conversation orchestrates, summarizes, and interacts with the user. All heavy lifting MUST be delegated to subagents via the Task tool. Never perform these tasks inline:

**Mandatory delegation:**

- Schema validation, JSON validation, data integrity checks
- Dependency graph analysis (cycle detection, depth, width)
- Multi-file exploration and codebase search (3+ queries)
- Spec scoring, quality analysis, coverage analysis
- Running test suites and collecting results
- Complex multi-file changes
- Any operation that reads more than 3 files to produce a result

**Parallelization:**

- When multiple independent analyses are needed, launch them as parallel subagents
- Skill sub-commands that involve scoring dimensions should split each dimension into its own subagent where practical

**Main conversation responsibilities:**

- Orchestrate subagent launches
- Aggregate and format subagent results for the user
- Ask clarifying questions
- Make single-file edits based on clear user instructions
- Present summaries and scorecards

### Commands

```bash
# Start development
./init.sh

# Run tests
task test

# Run single test
go test -v -run TestName ./internal/package/...

# Lint
task lint

# Type check
task vet

# Build
task build
```

## Architecture

Single binary (`kahi`) with subcommand routing via cobra:
- `cmd/kahi/` -- CLI entry point and subcommands
- `internal/config/` -- TOML parsing, validation, defaults, search paths
- `internal/process/` -- State machine, start/stop, reaping
- `internal/supervisor/` -- Main run loop, signal handling, shutdown
- `internal/api/` -- REST handlers, SSE streaming, auth middleware
- `internal/events/` -- Pub/sub bus, event types
- `internal/logging/` -- slog-based structured logging
- `internal/ctl/` -- CLI control client logic
- `internal/migrate/` -- supervisord.conf parser and converter
- `internal/fcgi/` -- FastCGI socket management
- `internal/metrics/` -- Prometheus collectors
- `internal/web/` -- Web UI templates, embedded assets
- `internal/version/` -- Build metadata
- `internal/testutil/` -- Shared test helpers

## Quality Standards

Quality is enforced by the Claude Project Foundation hooks:

- **Pre-edit:** Blocks modification of sensitive files (.env, keys, credentials, lock files)
- **Pre-bash:** Blocks destructive commands (rm -rf /, force push, hard reset)
- **Post-edit:** Auto-formats files by language (Prettier, Ruff, rustfmt, etc.)
- **On stop:** Runs lint, type check, and test suite before allowing session end
- **Pre-commit:** Checks for secrets, forbidden files, runs linters
- **Commit-msg:** Validates conventional commits, blocks AI-isms and emoji

Coverage threshold: 85% (configured in constitution)

## Git Commit Guidelines

- Format: Conventional Commits (`feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert`)
- Subject line: <= 72 characters
- No emoji in commit messages or PR titles
- No AI-isms or self-referential language
- No Co-Authored-By trailers

## Communication Style

- Technical and direct
- No emoji
- No AI-isms ("I have", "Certainly", "seamless", "robust", "elegant")
- No marketing adjectives
- Terse: prefer short, factual statements
