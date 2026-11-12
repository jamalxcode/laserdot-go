#!/usr/bin/env bash
# Speed on a real Mac with real files (spec §12, §15.3): creates 100,000
# files in nested folders, indexes them with the CLI, and times searches.
# Then indexes them again through the daemon and samples its memory.
# Fails only far below the targets (CI disks vary): indexing under 10,000
# files a second, a search over 50 ms, or the daemon above 400 MB (the
# target is 150 MB; the number is printed).
set -euo pipefail
cd "$(dirname "$0")/../.."
d=$(mktemp -d)
python3 - "$d/files" <<'PY'
import os, sys
root = sys.argv[1]
names = ["invoice", "report", "holiday", "beach", "notes", "plan", "photo", "clip", "song", "draft"]
exts = ["pdf", "txt", "jpg", "mp4", "mp3", "docx", "png", "md"]
n = 0
for a in range(50):
    for b in range(40):
        p = os.path.join(root, f"dir{a:02d}", f"sub{b:02d}")
        os.makedirs(p)
        for c in range(50):
            open(os.path.join(p, f"{names[(a+b+c) % 10]}-{a}-{b}-{c}.{exts[c % 8]}"), "w").close()
            n += 1
print(f"{n} files")
PY
go build -o "$d/laserdot" ./cmd/laserdot
start=$(python3 -c 'import time; print(time.time())')
"$d/laserdot" --db "$d/data/index.db" index "$d/files" | tail -1
end=$(python3 -c 'import time; print(time.time())')
rate=$(python3 -c "print(int(100000 / ($end - $start)))")
# The same files read name by name (without the bulk call), to compare.
LASERDOT_NO_BULK=1 "$d/laserdot" --db "$d/nobulk/index.db" index "$d/files" | tail -1 | sed 's/^/without bulk reading: /'
echo "indexed 100000 files in $(python3 -c "print(round($end - $start, 2))") s: $rate files/s"
worst=0
for q in "invoice pdf" "beach jpg" "kind:video" "report*" "notes 12"; do
  out=$("$d/laserdot" --db "$d/data/index.db" --local search "$q" 2>&1 || true)
  line=$(printf '%s\n' "$out" | grep -E 'results?( \(showing [0-9]+\))? in [0-9.]+ ms' | head -1 || true)
  ms=$(printf '%s' "$line" | sed -nE 's/.* in ([0-9.]+) ms.*/\1/p')
  echo "search '$q': $line"; [ -z "$line" ] && printf "%s\n" "$out" | head -3
  worst=$(python3 -c "print(max($worst, float('${ms:-0}')))")
done
echo "slowest search: $worst ms"
# Memory: the daemon indexing the same files, sampled every 0.2 s.
go build -o "$d/laserdotd" ./cmd/laserdotd
"$d/laserdotd" --db "$d/mem/index.db" > "$d/daemon.log" 2>&1 &
dpid=$!
for i in $(seq 1 50); do "$d/laserdot" --db "$d/mem/index.db" status > /dev/null 2>&1 && break; sleep 0.2; done
"$d/laserdot" --db "$d/mem/index.db" index "$d/files" > /dev/null &
ipid=$!
peak=0
# On macOS laserdotd re-executes itself inside its sandbox, so the
# process to measure is the largest one started with this --db.
daemon_rss() { ps -axo rss=,command= | grep "laserdotd --db $d/mem/index.db" | grep -v grep | awk '{print $1}' | sort -n | tail -1; }
while kill -0 "$ipid" 2>/dev/null; do
  rss=$(daemon_rss || echo 0)
  [ "${rss:-0}" -gt "$peak" ] && peak=$rss
  sleep 0.2
done
wait "$ipid" || true
for q in "invoice pdf" "beach jpg" "kind:video"; do "$d/laserdot" --db "$d/mem/index.db" search "$q" > /dev/null 2>&1 || true; done
idle=$(daemon_rss || echo 0)
"$d/laserdot" --db "$d/mem/index.db" status | grep -E "laserdotd|index:" || true
kill "$dpid" 2>/dev/null || true
peak_mb=$((peak / 1024)); idle_mb=$((${idle:-0} / 1024))
echo "daemon memory: peak $peak_mb MB while indexing 100000 files, $idle_mb MB after (target 150 MB)"
python3 -c "import sys; sys.exit(0 if $rate >= 10000 else 1)" || { echo "::error::indexing too slow: $rate files/s"; exit 1; }
python3 -c "import sys; sys.exit(0 if $worst < 50 else 1)" || { echo "::error::a search took $worst ms"; exit 1; }
[ "$peak_mb" -lt 400 ] || { echo "::error::the daemon used $peak_mb MB"; exit 1; }
rm -rf "$d"
