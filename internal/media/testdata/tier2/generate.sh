#!/usr/bin/env bash
# Tier 2 corpus (spec §3.1.1): containers the Tier 1 parsers do not read,
# a variable-frame-rate Matroska file (Tier 1 detects VFR in MP4 only) and
# tones for loudness. Outputs are committed. Requires ffmpeg with libx264.
# (An HDR10+ sample needs x265 built with HDR10+ support; most distribution
# builds are not, so none is generated here.)
set -euo pipefail
cd "$(dirname "$0")"
F="ffmpeg -hide_banner -loglevel error -y"
COL="testsrc2=s"
TONE="sine=frequency=440:sample_rate=48000"

# AVI: MPEG-4 Part 2 + MP3
$F -f lavfi -i "$COL=320x240:r=25:d=1" -f lavfi -i "$TONE:d=1" -c:v mpeg4 -q:v 12 \
   -c:a libmp3lame -b:a 32k -ac 2 legacy.avi
# MPEG-TS: H.264 + Arabic AAC stereo + English AC-3 5.1
$F -f lavfi -i "$COL=640x360:r=25:d=1" -f lavfi -i "$TONE:d=1" -f lavfi -i "$TONE:d=1" \
   -map 0:v -map 1:a -map 2:a -c:v libx264 -preset ultrafast -crf 40 -pix_fmt yuv420p \
   -c:a:0 aac -ac:a:0 2 -b:a:0 32k -metadata:s:a:0 language=ara \
   -c:a:1 ac3 -ac:a:1 6 -b:a:1 96k -metadata:s:a:1 language=eng broadcast.ts
# WMV (ASF): WMV2 + WMA2
$F -f lavfi -i "$COL=640x360:r=30:d=1" -f lavfi -i "$TONE:d=1" -c:v wmv2 -b:v 150k \
   -c:a wmav2 -b:a 32k clip.wmv
# FLV: Sorenson Spark + MP3 mono
$F -f lavfi -i "$COL=320x240:r=15:d=1" -f lavfi -i "$TONE:d=1" -c:v flv -b:v 100k \
   -c:a libmp3lame -ar 22050 -ac 1 -b:a 24k old.flv
# MPEG-PS (DVD-style): MPEG-2 PAL + MP2
$F -f lavfi -i "$COL=720x576:r=25:d=1" -f lavfi -i "$TONE:d=1" -c:v mpeg2video -b:v 400k \
   -c:a mp2 -b:a 64k dvd.mpg
# Variable frame rate in Matroska (remux of ../vfr.mp4)
$F -i ../vfr.mp4 -c copy vfr.mkv
# Loudness: a 1 kHz tone, and the same 6 dB quieter
$F -f lavfi -i "sine=frequency=1000:sample_rate=16000:d=3" -af "volume=0.1" -ac 2 -c:a flac tone.flac
$F -f lavfi -i "sine=frequency=1000:sample_rate=16000:d=3" -af "volume=0.05" -ac 2 -c:a flac tone_quiet.flac
