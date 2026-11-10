#!/usr/bin/env bash
# Canned ffprobe/ffmpeg outputs for the fixtures, so Tier 2 parsing is tested
# where the tools are not installed (CI). Outputs are committed. Requires
# ffprobe, ffmpeg and jq. Run from anywhere.
set -euo pipefail
cd "$(dirname "$0")/.."
P="ffprobe -v error -protocol_whitelist file"
mkdir -p tier2/ffprobe tier2/frames tier2/pts tier2/loudnorm
for f in tier2/legacy.avi tier2/broadcast.ts tier2/clip.wmv tier2/old.flv tier2/dvd.mpg tier2/vfr.mkv \
         hevc10_pq.mp4 multi.mkv h264_4k.mp4 rotated.mov song.flac; do
  $P -of json -show_format -show_streams "file:$f" | jq 'del(.format.filename)' > "tier2/ffprobe/$(basename "$f").json"
done
$P -select_streams v:0 -read_intervals %+#5 -show_frames -show_entries frame=side_data_list -of json \
   file:hevc10_pq.mp4 > tier2/frames/hevc10_pq.mp4.json
for f in tier2/vfr.mkv pal.mkv h264_4k.mp4; do
  $P -select_streams v:0 -read_intervals %+#300 -show_entries packet=pts_time -of csv=p=0 "file:$f" \
     > "tier2/pts/$(basename "$f").csv"
done
for f in tone tone_quiet; do
  ffmpeg -nostdin -hide_banner -nostats -protocol_whitelist file -i "file:tier2/$f.flac" -vn -sn -map 0:a:0 \
     -af loudnorm=print_format=json -f null - 2> "tier2/loudnorm/$f.txt"
done
