#!/usr/bin/env bash
# Report feature_list.json entries whose passes flag no e2e test backs.
#
# A passing entry once meant "the package's unit tests pass", and many such
# entries turned out unwired or broken (#115). Every passing functional entry
# now names, in verified_by, the top-level e2e tests that prove it against
# the shipped binary (#134). scripts/verifiedby does the check:
#
#   missing  a passing functional entry has no verified_by
#   unknown  an entry names a test that does not exist in e2e/
#
# Findings are warnings: this script exits 0 when it finds them, and 1 only
# when the check itself cannot run, so a broken gate is never silent. In
# GitHub Actions it emits ::warning annotations and a job summary.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || { printf 'check-verified-by: cannot cd to %s\n' "$ROOT" >&2; exit 1; }

in_ci=false
[[ "${GITHUB_ACTIONS:-}" == "true" ]] && in_ci=true

if ! out="$(go run ./scripts/verifiedby -root .)"; then
  printf 'check-verified-by: the check could not run\n' >&2
  exit 1
fi

missing=()
unknown=()
while IFS=$'\t' read -r kind id test; do
  case "$kind" in
    missing) missing+=("$id") ;;
    unknown) unknown+=("$id: $test") ;;
    "") ;;
    *) printf 'check-verified-by: unexpected output: %s\n' "$kind" >&2; exit 1 ;;
  esac
done <<<"$out"

printf 'Passing functional entries without verified_by: %d\n' "${#missing[@]}"
for id in "${missing[@]}"; do printf '  %s\n' "$id"; done
printf 'verified_by names with no e2e test: %d\n' "${#unknown[@]}"
for u in "${unknown[@]}"; do printf '  %s\n' "$u"; done

if $in_ci; then
  for id in "${missing[@]}"; do
    printf '::warning file=feature_list.json,title=Passing entry without e2e proof::%s passes but names no e2e test in verified_by\n' "$id"
  done
  for u in "${unknown[@]}"; do
    printf '::warning file=feature_list.json,title=Unknown verified_by test::%s does not exist in e2e/\n' "$u"
  done

  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    # Backticks below are Markdown, not command substitution.
    # shellcheck disable=SC2016
    {
      printf '## verified_by check (warning only)\n\n'
      printf 'A passing functional entry must name the e2e tests that prove it against the shipped binary (#134).\n\n'
      printf '| Check | Findings |\n| --- | --- |\n'
      printf '| passing functional entries without `verified_by` | %d |\n' "${#missing[@]}"
      printf '| `verified_by` names with no e2e test | %d |\n\n' "${#unknown[@]}"
      if [[ ${#missing[@]} -gt 0 ]]; then
        printf '<details><summary>Entries without e2e proof (%d)</summary>\n\n' "${#missing[@]}"
        for id in "${missing[@]}"; do printf -- '- `%s`\n' "$id"; done
        printf '\n</details>\n\n'
      fi
      if [[ ${#unknown[@]} -gt 0 ]]; then
        printf '### Unknown test names\n\n'
        for u in "${unknown[@]}"; do printf -- '- `%s`\n' "$u"; done
        printf '\n'
      fi
    } >>"$GITHUB_STEP_SUMMARY"
  fi
fi

exit 0
