#!/bin/bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <external-build-dir> <new-app-bundle-path>" >&2
  exit 64
fi

fixture_root="$(cd "$(dirname "$0")" && pwd)"
build_dir="$1"
app_path="$2"

case "$build_dir$app_path" in
  /Volumes/BuildOffload/*) ;;
  *) echo "build directory and app bundle must be under /Volumes/BuildOffload" >&2; exit 64 ;;
esac
if [[ -e "$app_path" ]]; then
  echo "refusing to overwrite existing app bundle: $app_path" >&2
  exit 73
fi
mkdir -p "$build_dir" "$(dirname "$app_path")"
swift build --package-path "$fixture_root" --scratch-path "$build_dir/swift" --configuration release --jobs 2

contents="$app_path/Contents"
mkdir -p "$contents/MacOS"
install -m 755 "$build_dir/swift/release/ComuseFixture" "$contents/MacOS/ComuseFixture"
cat > "$contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>en</string>
  <key>CFBundleExecutable</key><string>ComuseFixture</string>
  <key>CFBundleIdentifier</key><string>com.sirerun.comuse.fixture</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>Comuse Fixture</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSPrincipalClass</key><string>NSApplication</string>
</dict>
</plist>
PLIST
codesign --force --deep --sign - "$app_path"
echo "Built unsigned/ad-hoc experimental fixture bundle: $app_path"
echo "Launch with: open -n '$app_path' --args --fixture-nonce <nonce>"
