# Contributing to Kahi

Thank you for your interest in contributing to Kahi. This guide covers
the development workflow, conventions, and requirements for contributions.

By participating in this project, you agree to abide by our
[Code of Conduct](CODE_OF_CONDUCT.md).

## Prerequisites

- [Go 1.26+](https://go.dev/dl/)
- [Task](https://taskfile.dev/) (task runner)
- [golangci-lint](https://golangci-lint.run/) (optional, for local linting)

## Getting Started

```bash
git clone https://github.com/kahiteam/kahi.git
cd kahi
./init.sh
task test
```

The `init.sh` script installs Git hooks and verifies tool versions.
Running `task test` confirms the build and test suite pass on your machine.

## Branching Model

- All development happens on feature branches off `main`.
- Branch names should be descriptive: `fix/signal-race`, `feat/syslog-forwarding`.
- Every change requires a pull request. Direct pushes to `main` are blocked.
- Keep branches short-lived. Rebase on `main` before opening a PR.

## Commit Conventions

This project uses [Conventional Commits](https://www.conventionalcommits.org/).

Format:

```
<type>(<optional scope>): <description>

[optional body]

[optional footer(s)]
```

Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`,
`ci`, `chore`, `revert`.

Subject line must be 72 characters or fewer.

Examples:

```
feat(config): add glob-based include directive
fix(process): prevent race in SIGCHLD reaping
docs: update API authentication section
test(logging): add rotation edge case coverage
```

Do not use emoji in commit messages.

## Pull Request Process

1. Create a feature branch from `main`.
2. Make your changes with atomic, well-described commits.
3. Run the full check suite locally:
   ```bash
   task all
   ```
4. Push your branch and open a pull request.
5. Fill in the PR template with a description of the change and testing done.
6. Address review feedback by pushing additional commits (do not force-push
   during review).
7. A maintainer will merge the PR once CI passes and review is approved.

### PR Requirements

- All CI checks must pass (tests, lint, vet).
- At least one maintainer approval.
- PR description explains the "why", not just the "what".
- No unrelated changes bundled in the same PR.

## Code Style

- Format all Go code with `gofmt` (enforced by CI).
- Pass `go vet ./...` with zero findings.
- Pass `golangci-lint run ./...` with zero findings.
- Follow standard Go conventions: exported names are documented, error
  values are checked, packages are small and focused.
- No dead code, unused imports, or commented-out blocks.

## Testing

- Write tests for all new functionality.
- Minimum coverage threshold: **85%**. CI will fail if coverage drops below
  this.
- Use table-driven tests where applicable.
- Unit tests go alongside the code they test (`foo_test.go` next to `foo.go`).
- Integration tests use the `integration` build tag.
- End-to-end tests use the `e2e` build tag.
- Run tests locally:
  ```bash
  task test              # unit tests with -race
  task test-integration  # integration tests
  task coverage          # generate coverage report
  ```

## Reporting Issues

- Use [GitHub Issues](https://github.com/kahiteam/kahi/issues) for bugs and
  feature requests.
- For security vulnerabilities, see [SECURITY.md](SECURITY.md). Do not open
  public issues for security reports.

## License

By contributing, you agree that your contributions will be licensed under the
same license as the project.
