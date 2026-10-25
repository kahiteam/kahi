#!/usr/bin/env bash
# Report code that the shipped kahi binary can never reach.
#
# Several features once passed on package unit tests alone while nothing in
# the daemon or CLI called them (#115). Two checks find that class of gap:
#
#   1. deadcode: functions in packages cmd/kahi imports that no call path
#      from main reaches (tests excluded).
#   2. Packages under internal/ that cmd/kahi does not import at all;
#      deadcode cannot see those.
#
# Findings are warnings: this script exits 0 when it finds unreachable code,
# and 1 only when a check itself cannot run, so a broken gate is never silent.
# In GitHub Actions it emits ::warning annotations and a job summary.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || { printf 'check-wiring: cannot cd to %s\n' "$ROOT" >&2; exit 1; }

MODULE="github.com/kahiteam/kahi"
# Packages that are not part of the binary by design.
EXPECTED_UNIMPORTED=("$MODULE/internal/testutil")

in_ci=false
[[ "${GITHUB_ACTIONS:-}" == "true" ]] && in_ci=true

if ! dead="$(go tool -modfile=tools/go.mod deadcode ./cmd/kahi 2>&1)"; then
  printf 'check-wiring: deadcode failed:\n%s\n' "$dead" >&2
  exit 1
fi

if ! imported="$(go list -deps ./cmd/kahi 2>&1)"; then
  printf 'check-wiring: go list -deps failed:\n%s\n' "$imported" >&2
  exit 1
fi
if ! all="$(go list ./internal/... 2>&1)"; then
  printf 'check-wiring: go list ./internal/... failed:\n%s\n' "$all" >&2
  exit 1
fi

unimported=()
while IFS= read -r pkg; do
  [[ -z "$pkg" ]] && continue
  grep -qxF "$pkg" <<<"$imported" && continue
  skip=false
  for e in "${EXPECTED_UNIMPORTED[@]}"; do [[ "$pkg" == "$e" ]] && skip=true; done
  $skip || unimported+=("$pkg")
done <<<"$all"

dead_count=0
[[ -n "$dead" ]] && dead_count="$(grep -c . <<<"$dead")"

printf 'Unimported internal packages: %d\n' "${#unimported[@]}"
for pkg in "${unimported[@]}"; do printf '  %s\n' "${pkg#"$MODULE"/}"; done
printf 'Unreachable functions: %d\n' "$dead_count"
while IFS= read -r line; do
  [[ -n "$line" ]] && printf '  %s\n' "$line"
done <<<"$dead"

if $in_ci; then
  for pkg in "${unimported[@]}"; do
    printf '::warning title=Unwired package::%s is not imported by cmd/kahi; nothing in the shipped binary uses it\n' "${pkg#"$MODULE"/}"
  done
  # deadcode prints "file:line:col: unreachable func: Name".
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    file="${line%%:*}"; rest="${line#*:}"
    lineno="${rest%%:*}"; msg="${rest#*: }"
    printf '::warning file=%s,line=%s,title=Unreachable from cmd/kahi::%s\n' "$file" "$lineno" "$msg"
  done <<<"$dead"

  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    # Backticks below are Markdown, not command substitution.
    # shellcheck disable=SC2016
    {
      printf '## Wiring check (warning only)\n\n'
      printf 'Code that the shipped `kahi` binary cannot reach. A feature is not done until something in `cmd/kahi` calls it (#115).\n\n'
      printf '| Check | Findings |\n| --- | --- |\n'
      printf '| internal packages not imported by `cmd/kahi` | %d |\n' "${#unimported[@]}"
      printf '| unreachable functions (`deadcode`) | %d |\n\n' "$dead_count"
      if [[ ${#unimported[@]} -gt 0 ]]; then
        printf '### Unimported packages\n\n'
        for pkg in "${unimported[@]}"; do printf -- '- `%s`\n' "${pkg#"$MODULE"/}"; done
        printf '\n'
      fi
      if [[ -n "$dead" ]]; then
        printf '<details><summary>Unreachable functions (%d)</summary>\n\n```\n%s\n```\n\n</details>\n' "$dead_count" "$dead"
      fi
    } >>"$GITHUB_STEP_SUMMARY"
  fi
fi

exit 0
