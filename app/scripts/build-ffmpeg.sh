#!/usr/bin/env bash
# Builds the ffmpeg and ffprobe that Laserdot bundles for Tier 2 (spec §3.1.1,
# §13.3) into app/build/ffmpeg: LGPL only (no --enable-gpl, nothing
# non-free), and without any network code — --disable-network and only the
# file and pipe protocols, so the bundled tools cannot reach the network even
# before laserdotd's sandbox (which they inherit) applies.
# Skips the build when app/build/ffmpeg already holds this version (CI caches it).
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${FFMPEG_VERSION:-7.1}"
OUT=build/ffmpeg
if [ -x "$OUT/ffprobe" ] && [ "$(cat "$OUT/VERSION" 2>/dev/null)" = "$VERSION" ]; then
  echo "ffmpeg $VERSION already built"
  exit 0
fi
SRC=build/ffmpeg-src
rm -rf "$SRC" "$OUT"
git clone --quiet --depth 1 --branch "n$VERSION" https://github.com/FFmpeg/FFmpeg.git "$SRC"
cd "$SRC"
./configure \
  --prefix="$PWD/install" \
  --disable-network --disable-protocols --enable-protocol=file,pipe \
  --disable-autodetect --enable-zlib --enable-videotoolbox --enable-audiotoolbox \
  --disable-doc --disable-debug --disable-ffplay \
  --disable-devices --disable-indevs --disable-outdevs \
  --disable-encoders --enable-encoder=rawvideo,pcm_s16le,png,mjpeg \
  --enable-static --disable-shared \
  --extra-cflags="-mmacosx-version-min=14.0" --extra-ldflags="-mmacosx-version-min=14.0" >/dev/null
make -j"$(sysctl -n hw.ncpu)" >/dev/null
make install >/dev/null
cd - >/dev/null
mkdir -p "$OUT"
cp "$SRC/install/bin/ffmpeg" "$SRC/install/bin/ffprobe" "$OUT/"
cp "$SRC/COPYING.LGPLv2.1" "$OUT/LICENSE.txt"
printf 'FFmpeg %s (LGPL 2.1), built for Laserdot without network support.\nSource: https://github.com/FFmpeg/FFmpeg/tree/n%s\nConfigure flags: see app/scripts/build-ffmpeg.sh in https://github.com/jamalxcode/laserdot-go\n' "$VERSION" "$VERSION" > "$OUT/README.txt"
echo "$VERSION" > "$OUT/VERSION"
rm -rf "$SRC"
"$OUT/ffprobe" -hide_banner -protocols
