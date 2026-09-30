#!/bin/sh
# Installs turnback from its GitHub releases.
#
#   curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/Waiivyy/turnback/main/install.sh | sh
#
# It downloads the archive for this machine, verifies its SHA-256 checksum
# and installs the binary. No root rights needed. Settings:
#
#   TURNBACK_INSTALL_DIR  where to put the binary (default: ~/.local/bin)
#   TURNBACK_VERSION      a release tag such as v0.1.0 (default: the latest)
#   TURNBACK_BASE_URL     download from a mirror of the release files
set -eu

repo=Waiivyy/turnback

fail() {
	echo "turnback install: $*" >&2
	exit 1
}

fetch() {
	if [ -n "$secure" ]; then
		curl --proto '=https' --tlsv1.2 -fsSL "$@"
	else
		curl -fsSL "$@"
	fi
}

# Everything runs from main, called on the last line, so a download cut
# short can never run half of the script.
main() {
	dir=${TURNBACK_INSTALL_DIR:-$HOME/.local/bin}
	version=${TURNBACK_VERSION:-latest}

	os=$(uname -s | tr '[:upper:]' '[:lower:]')
	arch=$(uname -m)
	case $arch in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	esac
	case $os in
	mingw* | msys* | cygwin*)
		fail "on Windows, download turnback_windows_$arch.zip from https://github.com/$repo/releases/latest, or install with Go: go install github.com/$repo@latest"
		;;
	esac
	case $os/$arch in
	darwin/amd64 | darwin/arm64 | linux/amd64 | linux/arm64 | freebsd/amd64) ;;
	*) fail "there is no prebuilt binary for $os/$arch; install with Go instead: go install github.com/$repo@latest" ;;
	esac

	if command -v sha256sum >/dev/null 2>&1; then
		sha256() { sha256sum "$1"; }
	elif command -v shasum >/dev/null 2>&1; then
		sha256() { shasum -a 256 "$1"; }
	else
		fail "neither sha256sum nor shasum is installed, so the download cannot be verified; nothing was installed"
	fi

	# Downloads from GitHub may only use HTTPS, even after a redirect. A
	# mirror given in TURNBACK_BASE_URL may use any protocol curl knows.
	secure=yes
	if [ -n "${TURNBACK_BASE_URL:-}" ]; then
		secure=""
		base=$TURNBACK_BASE_URL
	else
		if [ "$version" = latest ]; then
			# Resolve the tag once, so the archive and its checksum always
			# come from the same release.
			url=$(fetch -I -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest") ||
				fail "could not find the latest release"
			version=${url##*/}
			case $version in
			v[0-9]*) ;;
			*) fail "could not find the latest release (got $url)" ;;
			esac
		fi
		base=https://github.com/$repo/releases/download/$version
	fi

	name=turnback_${os}_${arch}
	tmp=$(mktemp -d)
	staged=""
	trap 'rm -rf "$tmp"; [ -z "$staged" ] || rm -f "$staged"' EXIT
	trap 'exit 130' INT
	trap 'exit 143' TERM

	echo "Downloading $name.tar.gz ($version)..."
	fetch -o "$tmp/$name.tar.gz" "$base/$name.tar.gz" || fail "could not download $base/$name.tar.gz"
	fetch -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "could not download $base/checksums.txt"

	expected=$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/checksums.txt")
	[ -n "$expected" ] || fail "$name.tar.gz is not listed in checksums.txt; nothing was installed"
	actual=$(sha256 "$tmp/$name.tar.gz" | awk '{ print $1 }')
	[ -n "$actual" ] || fail "could not compute the checksum of $name.tar.gz; nothing was installed"
	[ "$expected" = "$actual" ] || fail "checksum mismatch for $name.tar.gz; nothing was installed"

	tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
	[ -f "$tmp/$name/turnback" ] || fail "$name.tar.gz holds no turnback binary; nothing was installed"

	# The new binary is staged next to the old one, so the final move is a
	# rename within one file system and replaces it in a single step.
	mkdir -p "$dir"
	staged=$dir/.turnback.new.$$
	cp "$tmp/$name/turnback" "$staged"
	chmod 755 "$staged"
	mv -f "$staged" "$dir/turnback"
	staged=""

	echo "Installed $("$dir/turnback" --version) to $dir/turnback"
	case :$PATH: in
	*:"$dir":* | *:"$dir/":*) ;;
	*)
		case ${SHELL:-} in
		*/zsh) profile="~/.zshrc" ;;
		*/bash) profile="~/.bashrc" ;;
		*/fish) profile="" ;;
		*) profile="your shell's profile" ;;
		esac
		if [ -z "$profile" ]; then
			echo "Note: $dir is not on your PATH. Add it with: fish_add_path $dir"
		else
			echo "Note: $dir is not on your PATH. Add this line to $profile:"
			echo "  export PATH=\"$dir:\$PATH\""
		fi
		;;
	esac
}

main "$@"
