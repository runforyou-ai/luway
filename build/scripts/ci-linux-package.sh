#!/usr/bin/env bash
set -euo pipefail
: "${VERSION:?}" "${TARGET_ARCH:?}" "${BUILD_NUMBER:?}"
export GOARCH="$TARGET_ARCH"
slug="$(jq -r .slug internal/common/brand/brand.json)"
go run build/scripts/set_release_version.go "$VERSION" "$BUILD_NUMBER"
wails3 task linux:package ARCH="$TARGET_ARCH" APP_VERSION="$VERSION"
mkdir -p release-assets release-stage
cp "bin/$slug" "release-stage/$slug"
tar -C release-stage -czf "release-assets/${slug}_${VERSION}_linux_${TARGET_ARCH}.tar.gz" "$slug"
case "$TARGET_ARCH" in
  amd64) appimage_arch=x86_64 ;;
  arm64) appimage_arch=aarch64 ;;
  *) echo "Unsupported Linux architecture: $TARGET_ARCH" >&2; exit 1 ;;
esac
cp "bin/${slug}-${appimage_arch}.AppImage" "release-assets/${slug}_${VERSION}_linux_${TARGET_ARCH}.AppImage"
for extension in deb rpm pkg.tar.zst; do
  cp "bin/$slug.$extension" "release-assets/${slug}_${VERSION}_linux_${TARGET_ARCH}.$extension"
done
file "bin/$slug"
