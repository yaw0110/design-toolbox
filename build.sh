#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
RELEASE_DIR="$ROOT_DIR/releases/beta"
TEMP_DIR="$(mktemp -d)"

cleanup() {
  find "$TEMP_DIR" -depth -delete
}
trap cleanup EXIT

LDFLAGS="-s -w"
BUILD_FLAGS=(-mod=vendor -trimpath -ldflags="$LDFLAGS")

build() {
  local goos="$1" goarch="$2" output="$3"
  echo "Building ${goos}/${goarch}..."
  (
    cd "$ROOT_DIR"
    GOPROXY=off CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
      go build "${BUILD_FLAGS[@]}" -o "$output" ./cmd/toolbox
  )
}

rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR/input/pdf" "$RELEASE_DIR/input/svg" "$RELEASE_DIR/input/pdfmerge" \
  "$RELEASE_DIR/output/pdf" "$RELEASE_DIR/output/svg/gif" "$RELEASE_DIR/output/svg/apng" \
  "$RELEASE_DIR/output/pdfmerge"

build darwin arm64 "$TEMP_DIR/toolbox-darwin-arm64"
build darwin amd64 "$TEMP_DIR/toolbox-darwin-amd64"

echo "Creating macOS Universal binary..."
/usr/bin/lipo -create \
  "$TEMP_DIR/toolbox-darwin-arm64" \
  "$TEMP_DIR/toolbox-darwin-amd64" \
  -output "$RELEASE_DIR/toolbox"
chmod 755 "$RELEASE_DIR/toolbox"

build windows amd64 "$RELEASE_DIR/toolbox.exe"

build linux amd64 "$TEMP_DIR/toolbox-linux-amd64"
cp "$TEMP_DIR/toolbox-linux-amd64" "$RELEASE_DIR/toolbox-linux-amd64"
chmod 755 "$RELEASE_DIR/toolbox-linux-amd64"

cp "$ROOT_DIR/docs/用户使用说明.md" "$RELEASE_DIR/readme.md"

echo
echo "Release files:"
find "$RELEASE_DIR" -type f -exec ls -la {} +
