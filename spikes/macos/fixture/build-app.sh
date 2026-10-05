#!/bin/bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
  echo "usage: $0 <external-build-dir> <new-app-bundle-path>" >&2
  exit 64
fi

fixture_root="$(cd "$(dirname "$0")" && pwd)"
artifact_root="${COMUSE_ARTIFACT_ROOT:-}"
if [[ -z "$artifact_root" || "$artifact_root" != /* ]]; then
  echo "COMUSE_ARTIFACT_ROOT must be an absolute path on the operator-verified external artifact volume" >&2
  exit 64
fi
mkdir -p "$artifact_root"
artifact_root="$(cd "$artifact_root" && pwd -P)"

resolve_output_parent() {
  local requested="$1"
  local parent
  local leaf
  [[ "$requested" == /* ]] || return 1
  parent="$(dirname "$requested")"
  leaf="$(basename "$requested")"
  [[ -n "$leaf" && "$leaf" != "." && "$leaf" != ".." ]] || return 1
  parent="$(python3 -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "$parent")"
  case "$parent" in
    "$artifact_root"|"$artifact_root"/*)
      mkdir -p "$parent"
      printf '%s/%s\n' "$parent" "$leaf"
      ;;
    *) return 1 ;;
  esac
}

build_dir="$(resolve_output_parent "$1")" || { echo "build directory must resolve under COMUSE_ARTIFACT_ROOT" >&2; exit 64; }
app_path="$(resolve_output_parent "$2")" || { echo "app bundle must resolve under COMUSE_ARTIFACT_ROOT" >&2; exit 64; }

[[ "$app_path" == *.app ]] || { echo "app bundle path must end in .app" >&2; exit 64; }
if [[ -e "$app_path" || -L "$app_path" ]]; then
  echo "refusing to overwrite existing app bundle: $app_path" >&2
  exit 73
fi
if [[ -L "$build_dir" || ( -e "$build_dir" && ! -d "$build_dir" ) ]]; then
  echo "build directory must be a real directory, not a file or symlink: $build_dir" >&2
  exit 73
fi
mkdir -p "$build_dir" "$(dirname "$app_path")"
build_dir="$(cd "$build_dir" && pwd -P)"
case "$build_dir" in
  "$artifact_root"|"$artifact_root"/*) ;;
  *) echo "resolved build directory escaped COMUSE_ARTIFACT_ROOT" >&2; exit 64 ;;
esac
swift build --package-path "$fixture_root" --scratch-path "$build_dir/swift" --configuration release --jobs 2
product_dir="$(swift build --package-path "$fixture_root" --scratch-path "$build_dir/swift" --configuration release --show-bin-path)"

contents="$app_path/Contents"
mkdir -p "$contents/MacOS"
install -m 755 "$product_dir/ComuseFixture" "$contents/MacOS/ComuseFixture"
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
echo "Built ad-hoc signed experimental fixture bundle (signing identity: -): $app_path"
echo "Launch with: open -n '$app_path' --args --fixture-nonce <nonce>"
