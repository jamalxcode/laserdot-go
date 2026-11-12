#!/usr/bin/env bash
# Assembles app/build/Laserdot.app: the SwiftUI app, laserdotd beside it in
# Contents/MacOS, the Laserdot Updater helper in Contents/Helpers, the icon,
# and the version. Run app/scripts/build-go.sh first.
#
#   VERSION=0.2.0 BUILD=42 app/scripts/bundle.sh
#
# Signed ad hoc (Apple Silicon requires a signature); a Developer ID signature
# and notarization need the developer's certificates.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-0.1.0}"
BUILD="${BUILD:-1}"
DAEMON=build/go/laserdotd
[ -x "$DAEMON" ] || { echo "missing $DAEMON: run app/scripts/build-go.sh first" >&2; exit 1; }
swift build -c release
BIN="$(swift build -c release --show-bin-path)"
APP=build/Laserdot.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$APP/Contents/Helpers"
cp "$BIN/Laserdot" "$APP/Contents/MacOS/Laserdot"
cp "$DAEMON" "$APP/Contents/MacOS/laserdotd"
# The bundled ffmpeg/ffprobe (LGPL, no network), when built: Tier 2 media
# details. laserdotd finds them next to itself.
if [ -x build/ffmpeg/ffprobe ]; then
  cp build/ffmpeg/ffmpeg build/ffmpeg/ffprobe "$APP/Contents/MacOS/"
  mkdir -p "$APP/Contents/Resources/FFmpeg"
  cp build/ffmpeg/LICENSE.txt build/ffmpeg/README.txt "$APP/Contents/Resources/FFmpeg/"
fi
for b in "$BIN"/*.bundle; do [ -e "$b" ] && cp -R "$b" "$APP/Contents/Resources/"; done
cp Info.plist "$APP/Contents/Info.plist"
# The app's own language folders: macOS picks the app's language, and with
# Arabic the right-to-left layout, from the main bundle's .lproj folders (the
# strings themselves are in the SwiftPM resource bundle).
for lang in en ar; do
  mkdir -p "$APP/Contents/Resources/$lang.lproj"
  printf '"CFBundleDisplayName" = "Laserdot";\n"CFBundleName" = "Laserdot";\n' > "$APP/Contents/Resources/$lang.lproj/InfoPlist.strings"
done
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $VERSION" -c "Set :CFBundleVersion $BUILD" "$APP/Contents/Info.plist"

# What's new in this version, from CHANGELOG.md (shown when Laserdot updates).
awk -v v="## $VERSION" '$0 == v {on=1; next} /^## / {on=0} on' ../CHANGELOG.md | sed '/^$/d' > "$APP/Contents/Resources/WhatsNew.txt" || true

# The icon, from the 1024-px PNG.
ICONSET=build/AppIcon.iconset
rm -rf "$ICONSET" && mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
  sips -z $s $s Resources/AppIcon.png --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s * 2)) $((s * 2)) Resources/AppIcon.png --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/AppIcon.icns"

# The updater helper: its own small app, so macOS (not Laserdot) launches it.
HELPER="$APP/Contents/Helpers/Laserdot Updater.app"
mkdir -p "$HELPER/Contents/MacOS" "$HELPER/Contents/Resources"
cp "$BIN/LaserdotUpdater" "$HELPER/Contents/MacOS/LaserdotUpdater"
cat > "$HELPER/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key><string>Laserdot Updater</string>
    <key>CFBundleIdentifier</key><string>app.laserdot.Laserdot.Updater</string>
    <key>CFBundleExecutable</key><string>LaserdotUpdater</string>
    <key>CFBundlePackageType</key><string>APPL</string>
    <key>CFBundleShortVersionString</key><string>$VERSION</string>
    <key>CFBundleVersion</key><string>$BUILD</string>
    <key>LSMinimumSystemVersion</key><string>14.0</string>
    <key>LSUIElement</key><true/>
    <key>CFBundleIconFile</key><string>AppIcon</string>
</dict>
</plist>
PLIST
cp "$APP/Contents/Resources/AppIcon.icns" "$HELPER/Contents/Resources/AppIcon.icns"

# Signing. With the repository's own key (Actions secrets LASERDOT_SIGN_P12
# and LASERDOT_SIGN_PASSWORD) every version carries the same signature, so
# macOS keeps the permissions the user gave (Desktop, Documents, drives…)
# across updates. Without it (forks, local builds) the signature is ad hoc,
# and macOS treats each build as a new app.
if [ -n "${LASERDOT_SIGN_P12:-}" ] && [ -n "${LASERDOT_SIGN_PASSWORD:-}" ]; then
  sign_dir=$(mktemp -d)
  trap 'security delete-keychain "$sign_dir/sign.keychain-db" >/dev/null 2>&1 || true; rm -rf "$sign_dir"' EXIT
  kc="$sign_dir/sign.keychain-db"
  kc_pw=$(openssl rand -hex 16)
  security create-keychain -p "$kc_pw" "$kc"
  security set-keychain-settings -lut 3600 "$kc"
  security unlock-keychain -p "$kc_pw" "$kc"
  printf '%s' "$LASERDOT_SIGN_P12" | base64 -D > "$sign_dir/sign.p12"
  security import "$sign_dir/sign.p12" -k "$kc" -P "$LASERDOT_SIGN_PASSWORD" -T /usr/bin/codesign >/dev/null
  rm -f "$sign_dir/sign.p12"
  security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$kc_pw" "$kc" >/dev/null
  # The certificate is self-signed (not trusted by macOS), so pick the
  # identity by its hash rather than asking for a "valid" one.
  identity=$(security find-identity -p codesigning "$kc" | awk '/Laserdot Self-Signed/ {print $2; exit}')
  [ -n "$identity" ] || { echo "error: the signing key did not import" >&2; exit 1; }
  codesign --force --deep --keychain "$kc" --sign "$identity" "$APP"
  echo "signed with the Laserdot key ($identity)"
else
  codesign --force --deep --sign - "$APP"
  echo "signed ad hoc (no LASERDOT_SIGN_P12)"
fi
codesign --verify --deep --strict "$APP"
echo "built $APP ($VERSION, build $BUILD)"
