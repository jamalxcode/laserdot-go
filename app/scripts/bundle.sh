#!/usr/bin/env bash
# Assembles app/build/Laserdot.app: the SwiftUI shell with laserdotd beside it
# in Contents/MacOS. Run app/scripts/build-go.sh first.
# Not signed or notarized (that needs the developer's certificates).
set -euo pipefail
cd "$(dirname "$0")/.."
DAEMON=build/go/laserdotd
[ -x "$DAEMON" ] || { echo "missing $DAEMON: run app/scripts/build-go.sh first" >&2; exit 1; }
swift build -c release
BIN="$(swift build -c release --show-bin-path)"
APP=build/Laserdot.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN/Laserdot" "$APP/Contents/MacOS/Laserdot"
cp "$DAEMON" "$APP/Contents/MacOS/laserdotd"
for b in "$BIN"/*.bundle; do [ -e "$b" ] && cp -R "$b" "$APP/Contents/Resources/"; done
cp Info.plist "$APP/Contents/Info.plist"
echo "built $APP"
