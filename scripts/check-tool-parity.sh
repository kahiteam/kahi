#!/usr/bin/env bash
# Fail if the tools actually on PATH disagree with the versions pinned in this
# repository. Pinning makes drift unlikely; this makes it a named failure
# instead of "CI disagrees with my green local run".
#
# Pins live in two places, by tool type:
#   .tool-versions  golangci-lint, shellcheck, installed as verified binary downloads
#   tools/go.mod    gotestsum, govulncheck, goreleaser, deadcode, as tool directives
#   tools/actionlint/go.mod  actionlint, as a tool directive (own module: its
#                   yaml/v4 version conflicts with goreleaser's)
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || { printf 'check-tool-parity: cannot cd to %s\n' "$ROOT" >&2; exit 1; }

errors=0
fail() { printf 'FAIL %-14s %s\n' "$1" "$2"; errors=$((errors + 1)); }
ok()   { printf 'ok   %-14s %s\n' "$1" "$2"; }

# golangci-lint: pinned in .tool-versions, resolved from bin/ or PATH.
want="$(awk '$1 == "golangci-lint" { print $2; exit }' .tool-versions)"
if [[ -z "$want" ]]; then
  fail golangci-lint ".tool-versions declares no version"
else
  bin="./bin/golangci-lint"
  [[ -x "$bin" ]] || bin="$(command -v golangci-lint || true)"
  if [[ -z "$bin" ]]; then
    fail golangci-lint "not installed; run scripts/install-golangci-lint.sh"
  else
    got="$("$bin" --version 2>/dev/null | sed -n 's/.*has version \([^ ]*\) .*/\1/p')"
    if [[ "$got" != "$want" ]]; then
      fail golangci-lint "pinned $want, found ${got:-unknown} ($bin)"
    else
      ok golangci-lint "$want"
    fi
  fi
fi

# The shellcheck pin lives in .tool-versions; only bin/ counts. actionlint is
# always given ./bin/shellcheck, so a shellcheck elsewhere on PATH is irrelevant.
want="$(awk '$1 == "shellcheck" { print $2; exit }' .tool-versions)"
if [[ -z "$want" ]]; then
  fail shellcheck ".tool-versions declares no version"
elif [[ ! -x ./bin/shellcheck ]]; then
  fail shellcheck "not installed; run scripts/install-shellcheck.sh"
else
  got="$(./bin/shellcheck --version 2>/dev/null | sed -n 's/^version: //p')"
  if [[ "$got" != "$want" ]]; then
    fail shellcheck "pinned $want, found ${got:-unknown} (./bin/shellcheck)"
  else
    ok shellcheck "$want"
  fi
fi

# tools/go.mod tools: compare the pinned module version against what runs.
# These run through `go tool -modfile=tools/go.mod`, which always builds and
# runs exactly the version tools/go.mod pins, so their --version output cannot
# drift from the pin. Comparing it anyway meant compiling goreleaser from
# source on every run (~110s in CI). Check instead that each one is pinned and
# declared as a tool, so `go tool` resolves it.
# The pin comes from `go list -m`, not from grepping go.mod: the `tool (` block
# carries no versions, those sit in `require` as indirect entries.
check_tool() {
  local name="$1" module="$2" tool="$3" modfile="${4:-tools/go.mod}"
  local want
  want="$(go list -modfile="$modfile" -m "$module" 2>/dev/null | awk '{print $2}')"
  if [[ -z "$want" ]]; then
    fail "$name" "$modfile does not pin $module"
  elif ! grep -qE "^([[:space:]]*|tool )${tool//./\\.}\$" "$modfile"; then
    fail "$name" "$modfile does not declare $tool as a tool"
  else
    ok "$name" "$want"
  fi
}

check_tool gotestsum   gotest.tools/gotestsum              gotest.tools/gotestsum
check_tool govulncheck golang.org/x/vuln                   golang.org/x/vuln/cmd/govulncheck
check_tool goreleaser  github.com/goreleaser/goreleaser/v2 github.com/goreleaser/goreleaser/v2
check_tool deadcode    golang.org/x/tools                  golang.org/x/tools/cmd/deadcode
check_tool actionlint  github.com/rhysd/actionlint         github.com/rhysd/actionlint/cmd/actionlint tools/actionlint/go.mod

# The release job's goreleaser must equal the one pinned in tools/go.mod,
# otherwise the binaries built in CI differ from a local `task` build.
want_gr="$(go list -modfile=tools/go.mod -m github.com/goreleaser/goreleaser/v2 2>/dev/null | awk '{print $2}')"
got_gr="$(sed -n "s/^[[:space:]]*version: '\(v[0-9][^']*\)'.*/\1/p" .github/workflows/release.yml | head -1)"
if [[ "$got_gr" != "$want_gr" ]]; then
  fail goreleaser-ci "release.yml pins ${got_gr:-none}, tools/go.mod pins $want_gr"
else
  ok goreleaser-ci "$want_gr"
fi

# No version input may be a floating range.
if grep -rnE "version: '([~^]|.*\.x')" .github/workflows/ 2>/dev/null; then
  fail ranges "floating version range above; pin an exact version"
fi

# Nothing may reintroduce an unpinned install.
if grep -rn '@latest' Taskfile.yml .github/workflows/ 2>/dev/null; then
  fail pins "@latest found above; pin the version instead"
fi

if [[ "$errors" -gt 0 ]]; then
  printf '\n%d tool(s) drifted from their pins.\n' "$errors"
  exit 1
fi
printf '\nAll tools match their pins.\n'
