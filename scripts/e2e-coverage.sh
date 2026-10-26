#!/usr/bin/env bash
# Report per-package coverage of the e2e run against the shipped binary.
#
# Unit-test coverage counts code the daemon never calls, so a package can
# raise the 85% figure without being wired in (#115). This report measures
# what the e2e suite executes in the kahi binary itself (#135). Run the suite
# with KAHI_E2E_COVERDIR set; e2e/helpers_test.go then builds kahi with
# -cover and every kahi process writes its counters there:
#
#   KAHI_E2E_COVERDIR=/tmp/e2e-cover task test-e2e
#   ./scripts/e2e-coverage.sh /tmp/e2e-cover
#
# It writes the merged profile to e2e-coverage.out and prints coverage per
# package. Internal packages the e2e run never executes, and internal
# packages missing from the binary, are warnings: the script exits 0 when it
# finds them, and 1 only when the report cannot be built. In GitHub Actions
# it emits ::warning annotations and a job summary.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || { printf 'e2e-coverage: cannot cd to %s\n' "$ROOT" >&2; exit 1; }

MODULE="github.com/kahiteam/kahi"
# Packages that are not part of the binary by design.
EXPECTED_ABSENT=("$MODULE/internal/testutil")
PROFILE="e2e-coverage.out"

dir="${1:-${KAHI_E2E_COVERDIR:-}}"
if [[ -z "$dir" ]]; then
  printf 'usage: %s <coverdir>  (or set KAHI_E2E_COVERDIR)\n' "$0" >&2
  exit 1
fi
if ! compgen -G "$dir/covmeta.*" >/dev/null; then
  printf 'e2e-coverage: no coverage data in %s; was the e2e suite run with KAHI_E2E_COVERDIR set?\n' "$dir" >&2
  exit 1
fi

in_ci=false
[[ "${GITHUB_ACTIONS:-}" == "true" ]] && in_ci=true

if ! err="$(go tool covdata textfmt -i="$dir" -o="$PROFILE" 2>&1)"; then
  printf 'e2e-coverage: covdata textfmt failed:\n%s\n' "$err" >&2
  exit 1
fi
if ! deps="$(go list -deps ./cmd/kahi 2>&1)"; then
  printf 'e2e-coverage: go list -deps failed:\n%s\n' "$deps" >&2
  exit 1
fi
if ! pkgs="$(go list ./cmd/... ./internal/... 2>&1)"; then
  printf 'e2e-coverage: go list failed:\n%s\n' "$pkgs" >&2
  exit 1
fi

# Profile lines are "file:start.col,end.col statements count". Several kahi
# processes can report the same block, so a block counts as covered when any
# report has a non-zero count. Output: "package covered total".
if ! stats="$(awk '
  NR == 1 { next }
  {
    block = $1; stmts[block] = $2
    if ($3 > 0) hit[block] = 1
  }
  END {
    for (b in stmts) {
      p = b; sub(/:.*/, "", p); sub(/\/[^\/]*$/, "", p)
      total[p] += stmts[b]
      if (b in hit) covered[p] += stmts[b]
    }
    for (p in total) printf "%s %d %d\n", p, covered[p], total[p]
  }' "$PROFILE" | sort)"; then
  printf 'e2e-coverage: cannot parse %s\n' "$PROFILE" >&2
  exit 1
fi
if [[ -z "$stats" ]]; then
  printf 'e2e-coverage: %s has no coverage blocks\n' "$PROFILE" >&2
  exit 1
fi

rows=()
unexecuted=()
absent=()
all_covered=0
all_total=0
while IFS= read -r pkg; do
  [[ -z "$pkg" ]] && continue
  line="$(grep -m1 "^$pkg " <<<"$stats")"
  if [[ -z "$line" ]]; then
    # In the binary but without statements (constants and variables only).
    grep -qxF "$pkg" <<<"$deps" && continue
    skip=false
    for e in "${EXPECTED_ABSENT[@]}"; do [[ "$pkg" == "$e" ]] && skip=true; done
    $skip || absent+=("${pkg#"$MODULE"/}")
    continue
  fi
  read -r _ covered total <<<"$line"
  all_covered=$((all_covered + covered))
  all_total=$((all_total + total))
  pct="$(awk -v c="$covered" -v t="$total" 'BEGIN { printf "%.1f", 100 * c / t }')"
  rows+=("${pkg#"$MODULE"/} $pct $covered $total")
  [[ "$covered" -eq 0 && "$pkg" == "$MODULE/internal/"* ]] && unexecuted+=("${pkg#"$MODULE"/}")
done <<<"$pkgs"

total_pct="$(awk -v c="$all_covered" -v t="$all_total" 'BEGIN { printf "%.1f", 100 * c / t }')"

printf 'E2E coverage of the kahi binary: %s%% (%d of %d statements)\n' "$total_pct" "$all_covered" "$all_total"
for r in "${rows[@]}"; do
  read -r p pct c t <<<"$r"
  printf '  %-28s %6s%%  (%d/%d)\n' "$p" "$pct" "$c" "$t"
done
printf 'Internal packages the e2e run never executes: %d\n' "${#unexecuted[@]}"
for p in "${unexecuted[@]}"; do printf '  %s\n' "$p"; done
printf 'Internal packages not in the binary: %d\n' "${#absent[@]}"
for p in "${absent[@]}"; do printf '  %s\n' "$p"; done

if $in_ci; then
  for p in "${unexecuted[@]}"; do
    printf '::warning title=Package never executed by e2e::%s is in the kahi binary but no e2e test executes any of its statements\n' "$p"
  done
  for p in "${absent[@]}"; do
    printf '::warning title=Package not in the binary::%s is not part of the kahi binary, so e2e cannot cover it\n' "$p"
  done

  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    # Backticks below are Markdown, not command substitution.
    # shellcheck disable=SC2016
    {
      printf '## E2E coverage of the kahi binary (report only)\n\n'
      printf 'Statements of the shipped binary that the e2e suite executes (#135). Unit-test coverage is reported separately.\n\n'
      printf '**Total: %s%%** (%d of %d statements)\n\n' "$total_pct" "$all_covered" "$all_total"
      printf '| Package | E2E coverage | Statements |\n| --- | ---: | ---: |\n'
      for r in "${rows[@]}"; do
        read -r p pct c t <<<"$r"
        printf '| `%s` | %s%% | %d / %d |\n' "$p" "$pct" "$c" "$t"
      done
      printf '\n'
      if [[ ${#unexecuted[@]} -gt 0 || ${#absent[@]} -gt 0 ]]; then
        printf '### Warnings\n\n'
        for p in "${unexecuted[@]}"; do printf -- '- `%s`: never executed by e2e\n' "$p"; done
        for p in "${absent[@]}"; do printf -- '- `%s`: not in the binary\n' "$p"; done
        printf '\n'
      fi
    } >>"$GITHUB_STEP_SUMMARY"
  fi
fi

exit 0
