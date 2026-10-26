#!/usr/bin/env bash
# qb: Neversoft QScript and PRE archive toolchain
# Author: epicmaxim
# www.thug.one
#
# Builds the release archives for every platform into dist/:
#
#   ./release.sh v1.0.0
#
# qb is pure Go (no cgo), so one machine cross-compiles all of them.
set -euo pipefail

version="${1:?usage: ./release.sh <version, e.g. v1.0.0>}"
targets=(
	windows/amd64 windows/arm64
	darwin/amd64 darwin/arm64
	linux/amd64 linux/arm64
)

cd "$(dirname "$0")"
go vet ./...
go test ./...

rm -rf dist
mkdir -p dist
for target in "${targets[@]}"; do
	os="${target%/*}" arch="${target#*/}"
	name="qb-${version}-${os}-${arch}"
	exe="qb"
	[[ "$os" == windows ]] && exe="qb.exe"

	mkdir -p "dist/$name"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
		-ldflags "-s -w -X main.version=${version}" -o "dist/$name/$exe" .
	cp README.md "dist/$name/"
	[[ -f LICENSE ]] && cp LICENSE "dist/$name/"

	# zip for Windows, tar.gz elsewhere (keeps the executable bit)
	(
		cd dist
		if [[ "$os" == windows ]]; then
			zip -qr "$name.zip" "$name"
		else
			tar -czf "$name.tar.gz" "$name"
		fi
	)
	rm -rf "dist/$name"
	echo "built dist/$name"
done

(cd dist && shasum -a 256 qb-* > SHA256SUMS)
echo
cat dist/SHA256SUMS
