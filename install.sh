#!/bin/sh
# Installs turnback from its GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/Waiivyy/turnback/main/install.sh | sh
#
# It downloads the archive for this machine, verifies its SHA-256 checksum
# and installs the binary. No root rights needed. Settings:
#
#   TURNBACK_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
#   TURNBACK_VERSION      a release tag such as v0.1.0 (default: the latest)
#   TURNBACK_BASE_URL     download from a mirror of the release files
set -eu

repo=Waiivyy/turnback
dir=${TURNBACK_INSTALL_DIR:-$HOME/.local/bin}
version=${TURNBACK_VERSION:-latest}

fail() {
	echo "turnback install: $*" >&2
	exit 1
}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case $arch in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
esac
case $os/$arch in
darwin/amd64 | darwin/arm64 | linux/amd64 | linux/arm64 | freebsd/amd64) ;;
*) fail "there is no prebuilt binary for $os/$arch; install with Go instead: go install github.com/$repo@latest" ;;
esac

if [ -n "${TURNBACK_BASE_URL:-}" ]; then
	base=$TURNBACK_BASE_URL
elif [ "$version" = latest ]; then
	base=https://github.com/$repo/releases/latest/download
else
	base=https://github.com/$repo/releases/download/$version
fi

name=turnback_${os}_${arch}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "Downloading $name.tar.gz ($version)..."
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || fail "could not download $base/$name.tar.gz"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "could not download $base/checksums.txt"

expected=$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$expected" ] || fail "$name.tar.gz is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	actual=$(sha256sum "$tmp/$name.tar.gz" | awk '{ print $1 }')
else
	actual=$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{ print $1 }')
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $name.tar.gz; nothing was installed"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$dir"
cp "$tmp/$name/turnback" "$tmp/turnback.new"
chmod 755 "$tmp/turnback.new"
mv -f "$tmp/turnback.new" "$dir/turnback"

echo "Installed $("$dir/turnback" --version) to $dir/turnback"
case :$PATH: in
*:"$dir":*) ;;
*) echo "Note: $dir is not on your PATH. Add it with: export PATH=\"$dir:\$PATH\"" ;;
esac
