#!/usr/bin/env bash
# Install golangci-lint at the version pinned in .tool-versions, verifying the
# download against the committed checksums in golangci-lint.sha256.
#
# Upstream recommends binary installation and states that go install, the tools
# pattern and tool directives "aren't guaranteed to work", so this tool is not
# in tools/go.mod. Pinning it here keeps local and CI on the same build: an
# older golangci-lint cannot read a newer Go's export data, which is how a
# green local run and a green CI run disagree.
#
#   install-golangci-lint.sh            install the pinned version into bin/
#   install-golangci-lint.sh --update   rewrite the pins to the latest release
#
# --update reads the checksums published with the release and writes both
# .tool-versions and golangci-lint.sha256, so a bump is one reviewable diff.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PINS="$ROOT/.tool-versions"
SUMS="$ROOT/golangci-lint.sha256"
DEST="$ROOT/bin"

die() { printf 'install-golangci-lint: %s\n' "$*" >&2; exit 1; }

read_pin() {
  [[ -f "$PINS" ]] || die "$PINS not found"
  awk '$1 == "golangci-lint" { print $2; exit }' "$PINS"
}

if [[ "${1:-}" == "--update" ]]; then
  command -v gh >/dev/null || die "--update needs the gh CLI"
  latest="$(gh release view --repo golangci/golangci-lint --json tagName --jq .tagName)"
  ver="${latest#v}"
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  curl -sSfL "https://github.com/golangci/golangci-lint/releases/download/v${ver}/golangci-lint-${ver}-checksums.txt" \
    -o "$tmp/upstream.txt" || die "could not fetch checksums for v${ver}"
  grep -E "(linux|darwin)-(amd64|arm64)\.tar\.gz$" "$tmp/upstream.txt" > "$tmp/pinned.txt"
  [[ "$(wc -l < "$tmp/pinned.txt")" -eq 4 ]] || die "expected 4 platform checksums for v${ver}"
  mv "$tmp/pinned.txt" "$SUMS"
  tmppins="$(mktemp)"
  awk -v v="$ver" '$1 == "golangci-lint" { print $1, v; next } { print }' "$PINS" > "$tmppins"
  mv "$tmppins" "$PINS"
  printf 'pinned golangci-lint %s; review .tool-versions and golangci-lint.sha256\n' "$ver"
  exit 0
fi

VERSION="$(read_pin)"
[[ -n "$VERSION" ]] || die "$PINS declares no golangci-lint version"
[[ -f "$SUMS" ]] || die "$SUMS not found"

case "$(uname -s)" in
  Linux)  os=linux ;;
  Darwin) os=darwin ;;
  *)      die "unsupported OS: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *)             die "unsupported architecture: $(uname -m)" ;;
esac

# Already at the pinned version? Nothing to do.
if [[ -x "$DEST/golangci-lint" ]] \
  && "$DEST/golangci-lint" --version 2>/dev/null | grep -qF "has version $VERSION "; then
  printf 'golangci-lint %s already installed\n' "$VERSION"
  exit 0
fi

archive="golangci-lint-${VERSION}-${os}-${arch}.tar.gz"
expected="$(awk -v a="$archive" '$2 == a { print $1; exit }' "$SUMS")"
[[ -n "$expected" ]] || die "$SUMS has no entry for $archive (pin and checksums out of step?)"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -sSfL "https://github.com/golangci/golangci-lint/releases/download/v${VERSION}/${archive}" \
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
install -m 0755 "$tmp/golangci-lint-${VERSION}-${os}-${arch}/golangci-lint" "$DEST/golangci-lint"
printf 'installed golangci-lint %s (%s-%s), checksum verified\n' "$VERSION" "$os" "$arch"
