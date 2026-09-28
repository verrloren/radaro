#!/bin/sh
# Radaro installer: downloads the latest (or $RADARO_VERSION) release for this
# OS/arch, verifies its SHA-256 against checksums.txt, and installs the binary.
#
#   curl -fsSL https://raw.githubusercontent.com/verrloren/radaro/main/install.sh | sh
#
# Environment:
#   RADARO_VERSION      e.g. v0.1.0 (default: latest release)
#   RADARO_INSTALL_DIR  target directory (default: /usr/local/bin if writable, else ~/.local/bin)
set -eu

REPO="verrloren/radaro"

say() { printf '%s\n' "$*"; }
fail() { printf 'radaro install: %s\n' "$*" >&2; exit 1; }
need() { command -v "$1" >/dev/null 2>&1 || fail "$1 is required"; }

fetch() { # fetch <url> <output>
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
	else
		fail "curl or wget is required"
	fi
}

need tar
need uname

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
	linux | darwin) ;;
	*) fail "unsupported OS: $os (on Windows, download the .zip from https://github.com/$REPO/releases)" ;;
esac
arch=$(uname -m)
case "$arch" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) fail "unsupported architecture: $arch" ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

version=${RADARO_VERSION:-}
if [ -z "$version" ]; then
	fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmp/latest.json"
	version=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n 1)
	[ -n "$version" ] || fail "could not determine the latest release"
fi
case "$version" in v*) ;; *) version="v$version" ;; esac

archive="radaro_${version#v}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"
say "Downloading radaro $version for $os/$arch…"
fetch "$base/$archive" "$tmp/$archive" || fail "download failed: $base/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || fail "could not download checksums.txt"

expected=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d ' ' -f 1)
[ -n "$expected" ] || fail "$archive is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$tmp/$archive" | cut -d ' ' -f 1)
elif command -v shasum >/dev/null 2>&1; then
	actual=$(shasum -a 256 "$tmp/$archive" | cut -d ' ' -f 1)
else
	fail "sha256sum or shasum is required to verify the download"
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive"

tar -xzf "$tmp/$archive" -C "$tmp" radaro

dir=${RADARO_INSTALL_DIR:-}
if [ -z "$dir" ]; then
	if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"
install -m 0755 "$tmp/radaro" "$dir/radaro" 2>/dev/null || { cp "$tmp/radaro" "$dir/radaro" && chmod 0755 "$dir/radaro"; }

say "✓ installed $("$dir/radaro" --version) to $dir/radaro"
case ":$PATH:" in
	*":$dir:"*) ;;
	*) say "  $dir is not on your PATH. Add it:  export PATH=\"$dir:\$PATH\"" ;;
esac
say ""
say "Next:"
say "  radaro demo                  # synthetic data + dashboard at http://127.0.0.1:8042"
say "  radaro skill install         # teach Claude Code / Codex to use radaro"
say "  radaro connect bluesky --handle you.bsky.social"
