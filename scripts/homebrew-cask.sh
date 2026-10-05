#!/bin/sh
# Writes the Homebrew cask for a release to standard output, from the
# release's checksums.txt as scripts/build-release.sh writes it:
#
#   scripts/homebrew-cask.sh v0.2.0 dist/checksums.txt >turnback.rb
#
# The cask installs the release's own archives for macOS and Linux, so
# Homebrew installs exactly the files the release lists and attests. It is a
# cask rather than a formula because Homebrew installs a formula without
# bottles as a build from source, which on macOS needs developer tools as
# new as the system.
set -eu

usage="usage: scripts/homebrew-cask.sh <tag, e.g. v0.2.0> <checksums.txt>"
tag=${1:?$usage}
sums=${2:?$usage}

fail() {
	echo "homebrew-cask.sh: $*" >&2
	exit 1
}

case $tag in
v[0-9]*.[0-9]*.[0-9]*) ;;
*) fail "$tag is not a version tag such as v0.2.0" ;;
esac
[ -r "$sums" ] || fail "cannot read $sums"

# sum prints the SHA-256 of one archive, as listed in the checksums file.
sum() {
	s=$(awk -v f="$1" '$2 == f { print $1 }' "$sums")
	case $s in
	*[!0-9a-f]* | "") fail "$sums lists no SHA-256 for $1" ;;
	esac
	[ ${#s} -eq 64 ] || fail "$sums lists no SHA-256 for $1"
	echo "$s"
}

# Every checksum is found before anything is written, so a missing archive
# never produces half a cask.
darwin_arm64=$(sum turnback_darwin_arm64.tar.gz)
darwin_amd64=$(sum turnback_darwin_amd64.tar.gz)
linux_arm64=$(sum turnback_linux_arm64.tar.gz)
linux_amd64=$(sum turnback_linux_amd64.tar.gz)

cat <<EOF
# Written by scripts/homebrew-cask.sh in https://github.com/Waiivyy/turnback
# for each release. Changes made here are replaced by the next release.
cask "turnback" do
  arch arm: "arm64", intel: "amd64"
  os macos: "darwin", linux: "linux"

  version "${tag#v}"
  sha256 arm:          "$darwin_arm64",
         intel:        "$darwin_amd64",
         arm64_linux:  "$linux_arm64",
         x86_64_linux: "$linux_amd64"

  url "https://github.com/Waiivyy/turnback/releases/download/v#{version}/turnback_#{os}_#{arch}.tar.gz"
  name "turnback"
  desc "Per-turn history and selective undo for AI coding agents"
  homepage "https://github.com/Waiivyy/turnback"

  binary "turnback_#{os}_#{arch}/turnback"

  # The binaries are not signed by Apple, so Gatekeeper would refuse to run
  # a copy marked as downloaded from the internet.
  postflight_steps do
    on_macos do
      run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{staged_path}}"]
    end
  end
end
EOF
