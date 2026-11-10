#!/usr/bin/env bash
# Duplicate-finder corpus (spec §4.1). Outputs are committed.
# Requires ffmpeg with libx264 and libx265.
set -euo pipefail
cd "$(dirname "$0")"
F="ffmpeg -hide_banner -loglevel error -y"

# Images: one picture as PNG, a smaller low-quality JPEG of it, the same
# picture rotated 90° clockwise (tests add EXIF orientation 6 to undo it),
# and a different picture.
$F -f lavfi -i "mandelbrot=s=640x480:maxiter=200:end_pts=1" -frames:v 1 mandel.png
$F -i mandel.png -vf scale=320:240 -q:v 20 mandel_small.jpg
$F -i mandel.png -vf transpose=1 -q:v 4 mandel_turned.jpg
$F -i mandel.png -vf scale=160:120 -q:v 6 mandel_thumb.jpg
$F -f lavfi -i "testsrc2=s=640x480" -frames:v 1 -q:v 4 other.jpg

# Video: a 4 s clip, the same clip re-encoded (HEVC, smaller, .mov) and as
# a low-bitrate H.264 .m4v, and a different clip of the same length.
$F -f lavfi -i "testsrc2=s=320x240:r=24:d=4" -c:v libx264 -preset veryfast -crf 26 -pix_fmt yuv420p clip.mp4
$F -i clip.mp4 -vf scale=160:120 -c:v libx265 -preset ultrafast -crf 30 -tag:v hvc1 \
   -x265-params log-level=error -pix_fmt yuv420p clip_hevc.mov
$F -i clip.mp4 -c:v libx264 -preset veryfast -b:v 60k -pix_fmt yuv420p clip_low.m4v
$F -f lavfi -i "mandelbrot=s=320x240:r=24" -t 4 -c:v libx264 -preset veryfast -crf 26 -pix_fmt yuv420p other_clip.mp4
