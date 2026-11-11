#!/usr/bin/env bash
# Builds the Go side for the Swift app: laserdotd (bundled beside the app),
# the laserdot CLI, and Sources/LaserdotKit/Registry.generated.swift (the
# filter registry the app's guidance derives from).
# Usage: [VERSION=0.2.5] app/scripts/build-go.sh [host|universal]   (default: host)
# VERSION must match the one given to bundle.sh: the app replaces a running
# laserdotd that reports another version.
# Output: app/build/go/laserdotd and app/build/go/laserdot.
set -euo pipefail
cd "$(dirname "$0")/../.."
MODE="${1:-host}"
OUT=app/build/go
mkdir -p "$OUT"
build() { # build <goarch> <suffix>
  for cmd in laserdotd laserdot; do
    CGO_ENABLED=0 GOOS="$(go env GOOS)" GOARCH="$1" go build -trimpath -ldflags "-X github.com/jamalxcode/laserdot-go/internal/daemon.Version=${VERSION:-0.1.0}" -o "$OUT/$cmd$2" "./cmd/$cmd"
  done
}
case "$MODE" in
  host)
    build "$(go env GOARCH)" ""
    ;;
  universal) # macOS only: one binary for Apple silicon and Intel
    build arm64 -arm64
    build amd64 -amd64
    for cmd in laserdotd laserdot; do
      lipo -create -output "$OUT/$cmd" "$OUT/$cmd-arm64" "$OUT/$cmd-amd64"
      rm "$OUT/$cmd-arm64" "$OUT/$cmd-amd64"
    done
    ;;
  *)
    echo "usage: $0 [host|universal]" >&2
    exit 2
    ;;
esac
go run ./app/scripts/registry > app/Sources/LaserdotKit/Registry.generated.swift.tmp
mv app/Sources/LaserdotKit/Registry.generated.swift.tmp app/Sources/LaserdotKit/Registry.generated.swift
echo "go: built $OUT/laserdotd, $OUT/laserdot ($MODE) and the registry for the app"
