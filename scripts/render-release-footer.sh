#!/usr/bin/env bash
# Render the release notes footer for a tag (#225).
#
#   scripts/render-release-footer.sh TAG [FILE]
#
# Replaces {{ .Tag }} with TAG and {{ .Version }} with TAG without its
# leading "v", in FILE (default .goreleaser/release-footer.md; "-" reads
# stdin), and prints the result. GoReleaser renders the footer template only
# once, so text passed in through an environment variable keeps its own
# templates unexpanded; the release workflow renders it here instead.
#
# Fails if TAG is not a vX.Y.Z[-pre] tag, or if any "{{" is left.
set -euo pipefail

tag="${1:-}"
file="${2:-.goreleaser/release-footer.md}"
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
  printf 'render-release-footer: %q is not a vX.Y.Z[-pre] tag\n' "$tag" >&2
  exit 2
fi
version="${tag#v}"

if [[ "$file" == "-" ]]; then
  input="$(cat)"
else
  input="$(cat "$file")"
fi

# The tag was validated above, so it contains no sed metacharacters.
out="$(printf '%s\n' "$input" | sed -e "s/{{ \.Tag }}/${tag}/g" -e "s/{{ \.Version }}/${version}/g")"

if grep -q '{{' <<<"$out"; then
  echo "render-release-footer: unrendered template left:" >&2
  grep -n '{{' <<<"$out" >&2
  exit 1
fi
printf '%s\n' "$out"
