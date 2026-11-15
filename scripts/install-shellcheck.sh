#!/usr/bin/env bash
# Install shellcheck at the version pinned in .tool-versions, verifying the
# download against the committed checksums in shellcheck.sha256.
#
# actionlint runs shellcheck on every workflow `run:` script. Left to PATH,
# that is whatever version the machine or runner image has, and findings
# differ between a local run and CI. shellcheck is a Haskell binary, so it
# cannot live in tools/go.mod; it is a verified binary download instead.
#
#   install-shellcheck.sh            install the pinned version into bin/
#   install-shellcheck.sh --update   rewrite the pins to the latest release
#
# Upstream publishes no checksum file. --update takes the SHA-256 digests
# GitHub records for each release asset (the API's `digest` field) and writes
# both .tool-versions and shellcheck.sha256, so a bump is one reviewable diff.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PINS="$ROOT/.tool-versions"
SUMS="$ROOT/shellcheck.sha256"
DEST="$ROOT/bin"
REPO="koalaman/shellcheck"

die() { printf 'install-shellcheck: %s\n' "$*" >&2; exit 1; }

read_pin() {
  [[ -f "$PINS" ]] || die "$PINS not found"
  awk '$1 == "shellcheck" { print $2; exit }' "$PINS"
}

if [[ "${1:-}" == "--update" ]]; then
  command -v gh >/dev/null || die "--update needs the gh CLI"
  latest="$(gh release view --repo "$REPO" --json tagName --jq .tagName)"
  ver="${latest#v}"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  gh api "repos/$REPO/releases/tags/v${ver}" \
    --jq '.assets[] | select(.name | test("\\.(linux|darwin)\\.(x86_64|aarch64)\\.tar\\.gz$")) | "\(.digest)  \(.name)"' \
    > "$tmp/raw.txt" || die "could not fetch asset digests for v${ver}"
  grep -E '^sha256:[0-9a-f]{64}  ' "$tmp/raw.txt" | sed 's/^sha256://' | sort -k2 > "$tmp/pinned.txt"
  [[ "$(wc -l < "$tmp/pinned.txt")" -eq 4 ]] || die "expected 4 platform digests for v${ver}"
  mv "$tmp/pinned.txt" "$SUMS"
  tmppins="$(mktemp)"
  awk -v v="$ver" '$1 == "shellcheck" { print $1, v; next } { print }' "$PINS" > "$tmppins"
  mv "$tmppins" "$PINS"
  printf 'pinned shellcheck %s; review .tool-versions and shellcheck.sha256\n' "$ver"
  exit 0
fi

VERSION="$(read_pin)"
[[ -n "$VERSION" ]] || die "$PINS declares no shellcheck version"
[[ -f "$SUMS" ]] || die "$SUMS not found"

case "$(uname -s)" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *)      die "unsupported OS: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  arch=x86_64 ;;
  arm64|aarch64) arch=aarch64 ;;
  *)             die "unsupported architecture: $(uname -m)" ;;
esac

# Already at the pinned version? Nothing to do.
if [[ -x "$DEST/shellcheck" ]] \
  && "$DEST/shellcheck" --version 2>/dev/null | grep -qxF "version: $VERSION"; then
  printf 'shellcheck %s already installed\n' "$VERSION"
  exit 0
fi

archive="shellcheck-v${VERSION}.${os}.${arch}.tar.gz"
expected="$(awk -v a="$archive" '$2 == a { print $1; exit }' "$SUMS")"
[[ -n "$expected" ]] || die "$SUMS has no entry for $archive (pin and checksums out of step?)"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -sSfL "https://github.com/$REPO/releases/download/v${VERSION}/${archive}" \
  -o "$tmp/$archive" || die "download failed for $archive"

if command -v sha256sum >/dev/null; then
  actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
fi
[[ "$actual" == "$expected" ]] \
  || die "checksum mismatch for $archive: expected $expected, got $actual"

tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$DEST"
install -m 0755 "$tmp/shellcheck-v${VERSION}/shellcheck" "$DEST/shellcheck"
printf 'installed shellcheck %s (%s.%s), checksum verified\n' "$VERSION" "$os" "$arch"
