#!/bin/sh
# Builds release archives for every supported platform into dist/, with
# checksums, and writes the release notes for the version from CHANGELOG.md
# into release-notes.md.
#
#   scripts/build-release.sh v0.1.0
set -eu

version=${1:?usage: scripts/build-release.sh <version, e.g. v0.1.0>}
cd "$(dirname "$0")/.."

rm -rf dist
mkdir dist
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64 freebsd/amd64; do
	os=${target%/*}
	arch=${target#*/}
	name=turnback_${os}_${arch}
	bin=turnback
	if [ "$os" = windows ]; then bin=turnback.exe; fi
	mkdir "dist/$name"
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
		-ldflags "-s -w -X github.com/Waiivyy/turnback/internal/cli.version=$version" \
		-o "dist/$name/$bin" .
	cp LICENSE README.md "dist/$name/"
	if [ "$os" = windows ]; then
		(cd dist && zip -qr "$name.zip" "$name")
	else
		tar -C dist -czf "dist/$name.tar.gz" "$name"
	fi
	rm -rf "dist/$name"
done

cd dist
if command -v sha256sum >/dev/null 2>&1; then
	sha256sum -- * >checksums.txt
else
	shasum -a 256 -- * >checksums.txt
fi
cd ..

# The notes are the version's section of the changelog, without the blank
# lines around it or the link definitions that end the file.
awk -v heading="## [${version#v}]" '
	index($0, "## [") == 1 { if (found) exit; if (index($0, heading) == 1) { found = 1; next } }
	found && /^\[[^]]*\]: / { exit }
	found { lines[++n] = $0 }
	END {
		first = 1
		while (first <= n && lines[first] == "") first++
		last = n
		while (last >= first && lines[last] == "") last--
		for (i = first; i <= last; i++) print lines[i]
	}
' CHANGELOG.md >release-notes.md
if [ ! -s release-notes.md ]; then
	echo "build-release.sh: CHANGELOG.md has no section for ${version#v}" >&2
	exit 1
fi
echo "Built $(ls dist | wc -l | tr -d ' ') files in dist/ for $version"
