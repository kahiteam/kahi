#!/usr/bin/env bash
# Fail if the tools actually on PATH disagree with the versions pinned in this
# repository. Pinning makes drift unlikely; this makes it a named failure
# instead of "CI disagrees with my green local run".
#
# Pins live in two places, by tool type:
#   .tool-versions  golangci-lint, installed as a verified binary download
#   tools/go.mod    gotestsum, govulncheck, goreleaser, deadcode, as tool directives
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

# tools/go.mod tools: compare the pinned module version against what runs.
# The pin comes from `go list -m`, not from grepping go.mod: the `tool (` block
# carries no versions, those sit in `require` as indirect entries.
check_tool() {
  local name="$1" module="$2" pattern="$3"
  local want got
  want="$(go list -modfile=tools/go.mod -m "$module" 2>/dev/null | awk '{print $2}')"
  if [[ -z "$want" ]]; then
    fail "$name" "tools/go.mod does not pin $module"
    return
  fi
  got="$(go tool -modfile=tools/go.mod "$name" --version 2>/dev/null \
    | sed -n "$pattern" | head -1)"
  got="v${got#v}"
  if [[ "$got" != "$want" ]]; then
    fail "$name" "pinned $want, found ${got:-unknown}"
  else
    ok "$name" "$want"
  fi
}

# Each tool prints its version differently; parse the specific line, never the
# first version-shaped string (govulncheck leads with the Go version).
check_tool gotestsum   gotest.tools/gotestsum              's/^gotestsum version \(v[0-9][^ ]*\).*/\1/p'
check_tool govulncheck golang.org/x/vuln                   's/^Scanner: govulncheck@\(v[0-9][^ ]*\).*/\1/p'
check_tool goreleaser  github.com/goreleaser/goreleaser/v2 's/^GitVersion:[[:space:]]*\(v[0-9][^ ]*\).*/\1/p'

# deadcode has no --version flag. `go tool` runs exactly the version that
# tools/go.mod pins, so checking the pin and the tool directive is enough.
want_dc="$(go list -modfile=tools/go.mod -m golang.org/x/tools 2>/dev/null | awk '{print $2}')"
if [[ -z "$want_dc" ]] || ! grep -qE '^[[:space:]]*golang.org/x/tools/cmd/deadcode$' tools/go.mod; then
  fail deadcode "tools/go.mod does not pin golang.org/x/tools/cmd/deadcode as a tool"
else
  ok deadcode "$want_dc"
fi

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
