#!/usr/bin/env bash
# Regenerates the golden media corpus (spec §15.1). Outputs are committed so
# tests do not need ffmpeg; rerun this after changing a fixture.
# Requires ffmpeg with libx264, libx265, libsvtav1, libvpx-vp9, libopus, libmp3lame.
set -euo pipefail
cd "$(dirname "$0")"
F="ffmpeg -hide_banner -loglevel error -y"
COL="color=c=0x336699:s"
TONE="sine=frequency=440:sample_rate=48000"

# 1. H.264 4K, AAC stereo English, moov at front, title tag
$F -f lavfi -i "$COL=3840x2160:r=24:d=1" -f lavfi -i "$TONE:d=1" \
   -c:v libx264 -preset ultrafast -pix_fmt yuv420p -profile:v high \
   -c:a aac -ac 2 -b:a 64k -metadata:s:a:0 language=eng \
   -metadata title="Big Trip" -movflags +faststart h264_4k.mp4

# 2. HEVC Main10 HDR10 (PQ, BT.2020), E-AC-3 5.1 Arabic, hvc1 tag
$F -f lavfi -i "$COL=1920x1080:r=30000/1001:d=1" -f lavfi -i "$TONE:d=1" \
   -c:v libx265 -preset ultrafast -pix_fmt yuv420p10le -tag:v hvc1 \
   -color_primaries bt2020 -color_trc smpte2084 -colorspace bt2020nc \
   -x265-params log-level=error \
   -c:a eac3 -ac 6 -b:a 192k -metadata:s:a:0 language=ara -movflags +faststart hevc10_pq.mp4

# 3. Rotated phone clip: 1920x1080 coded, displayed portrait; moov at the END
$F -f lavfi -i "$COL=1920x1080:r=30:d=1" -f lavfi -i "$TONE:d=1" \
   -c:v libx264 -preset ultrafast -pix_fmt yuv420p -c:a aac -ac 1 -b:a 32k /tmp/laserdot-rot.mov
# stream copy so the display matrix is written without re-encoding
$F -display_rotation 90 -i /tmp/laserdot-rot.mov -c copy rotated.mov
rm -f /tmp/laserdot-rot.mov

# 4. AV1 in MP4
$F -f lavfi -i "$COL=640x360:r=25:d=1" -c:v libsvtav1 -preset 12 -pix_fmt yuv420p av1.mp4

# 5. VP9 + Opus in WebM
$F -f lavfi -i "$COL=1280x720:r=60:d=1" -f lavfi -i "$TONE:d=1" \
   -c:v libvpx-vp9 -deadline realtime -cpu-used 8 -b:v 100k -c:a libopus -b:a 32k vp9.webm

# 6. MKV: HEVC 10-bit HLG 3840x1600, three audio tracks, two subtitle tracks, tags
printf '1\n00:00:00,000 --> 00:00:00,900\nHello\n' > /tmp/laserdot-sub.srt
$F -f lavfi -i "$COL=3840x1600:r=24000/1001:d=1" \
   -f lavfi -i "$TONE:d=1" -f lavfi -i "$TONE:d=1" -f lavfi -i "$TONE:d=1" \
   -i /tmp/laserdot-sub.srt -i /tmp/laserdot-sub.srt \
   -map 0:v -map 1:a -map 2:a -map 3:a -map 4:s -map 5:s \
   -c:v libx265 -preset ultrafast -pix_fmt yuv420p10le \
   -color_primaries bt2020 -color_trc arib-std-b67 -colorspace bt2020nc -x265-params log-level=error \
   -c:a:0 ac3 -ac:a:0 6 -b:a:0 192k -metadata:s:a:0 language=ara \
   -c:a:1 aac -ac:a:1 2 -b:a:1 64k -metadata:s:a:1 language=eng \
   -c:a:2 flac -ac:a:2 8 -metadata:s:a:2 language=fre \
   -c:s srt -metadata:s:s:0 language=eng -disposition:s:0 forced -metadata:s:s:1 language=ara \
   -metadata title="Desert Concert" -metadata ARTIST="Fairouz" multi.mkv

# 7. Variable frame rate MP4 (irregular timestamps)
$F -f lavfi -i "$COL=320x240:r=30:d=1" -vf "setpts='if(eq(mod(N,3),0),PTS+2,PTS)'" -fps_mode vfr \
   -c:v libx264 -preset ultrafast -pix_fmt yuv420p vfr.mp4

# 8. PAL interlaced-era SD: 720x576, 25 fps, MPEG-2 in MKV
$F -f lavfi -i "$COL=720x576:r=25:d=1" -c:v mpeg2video -b:v 500k pal.mkv

# 9. Music: ALAC m4a, FLAC, MP3 (ID3v2), Opus
TAGS=(-metadata artist="Fairouz" -metadata album="Live at Baalbek" -metadata genre="Classical" -metadata title="Ya Rayah" -metadata date="1970")
$F -f lavfi -i "$TONE:d=1" -ac 2 -c:a alac "${TAGS[@]}" song_alac.m4a
$F -f lavfi -i "$TONE:d=1" -ac 2 -c:a flac "${TAGS[@]}" song.flac
$F -f lavfi -i "$TONE:d=1" -ac 2 -c:a libmp3lame -b:a 128k -id3v2_version 3 "${TAGS[@]}" song.mp3
$F -f lavfi -i "$TONE:d=1" -ac 2 -c:a libopus -b:a 48k -metadata artist="Daft Punk" -metadata genre="Electronic" voice.opus

# 10. Base JPEG (tests add EXIF to it at runtime)
$F -f lavfi -i "$COL=64x48" -frames:v 1 base.jpg

# 11. Truncated MP4: header present, moov (at the end) cut off
$F -f lavfi -i "$COL=320x240:r=30:d=1" -c:v libx264 -preset ultrafast -pix_fmt yuv420p /tmp/laserdot-full.mp4
head -c 2000 /tmp/laserdot-full.mp4 > truncated.mp4
rm -f /tmp/laserdot-sub.srt /tmp/laserdot-full.mp4
ls -la
