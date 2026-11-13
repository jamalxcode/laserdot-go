#!/usr/bin/env bash
# Speed on a real Mac with real files (spec §12, §15.3): creates FILES
# files (default 100,000) in nested folders, indexes them with the CLI, and
# times searches; compares reading folders in bulk with name by name.
# Then indexes them again through the daemon and samples its memory.
# Fails only far below the targets (CI disks vary): indexing under 10,000
# files a second, a search over 50 ms, or the daemon above 400 MB (the
# target is 150 MB; the number is printed).
set -euo pipefail
cd "$(dirname "$0")/../.."
FILES=${FILES:-100000}
MAX_MB=${MAX_MB:-400}
d=$(mktemp -d)
python3 - "$d/files" "$FILES" <<'PY'
import os, sys
root, total = sys.argv[1], int(sys.argv[2])
names = ["invoice", "report", "holiday", "beach", "notes", "plan", "photo", "clip", "song", "draft"]
exts = ["pdf", "txt", "jpg", "mp4", "mp3", "docx", "png", "md"]
n = 0
for a in range(max(1, total // 2000)):
    for b in range(40):
        p = os.path.join(root, f"dir{a:03d}", f"sub{b:02d}")
        os.makedirs(p)
        for c in range(50):
            open(os.path.join(p, f"{names[(a+b+c) % 10]}-{a}-{b}-{c}.{exts[c % 8]}"), "w").close()
            n += 1
print(f"{n} files")
PY
go build -o "$d/laserdot" ./cmd/laserdot
start=$(python3 -c 'import time; print(time.time())')
"$d/laserdot" --db "$d/data/index.db" index "$d/files" | tail -1 | tee "$d/index.out"
end=$(python3 -c 'import time; print(time.time())')
# Files are searchable once the index call returns ("searchable after");
# Finder tags are read after that, in the background (the CLI waits for
# them before exiting, so the wall time includes them).
ms=$(grep -oE 'searchable after [0-9]+' "$d/index.out" | grep -oE '[0-9]+$' || echo 0)
rate=$(python3 -c "print(int($FILES / max(int('${ms:-0}'), 1) * 1000))")
echo "with the Finder tags read afterwards: $(python3 -c "print(round($end - $start, 2))") s in all"
# Bulk folder reading against name by name, fairly: the files are in the
# disk cache now (they were just read), and each way runs twice,
# alternating; the faster scan of each counts.
scan_ms() { grep -oE 'scan [0-9]+ ms' | grep -oE '[0-9]+'; }
best_bulk=999999; best_names=999999
for round in 1 2; do
  rm -rf "$d/cmp"
  b=$("$d/laserdot" --db "$d/cmp/bulk.db" index "$d/files" | tail -1 | scan_ms)
  n=$(LASERDOT_NO_BULK=1 "$d/laserdot" --db "$d/cmp/names.db" index "$d/files" | tail -1 | scan_ms)
  echo "round $round: scan ${b} ms in bulk, ${n} ms name by name"
  [ "$b" -lt "$best_bulk" ] && best_bulk=$b
  [ "$n" -lt "$best_names" ] && best_names=$n
done
echo "reading folders: ${best_bulk} ms in bulk, ${best_names} ms name by name ($(python3 -c "print(round($best_names / max($best_bulk, 1), 1))")× faster in bulk)"
echo "indexed $FILES files, searchable after ${ms} ms: $rate files/s"
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
# Wait for the daemon itself (status also works without it, from the file).
for i in $(seq 1 100); do "$d/laserdot" --db "$d/mem/index.db" status 2>/dev/null | grep -q '(pid ' && break; sleep 0.2; done
"$d/laserdot" --db "$d/mem/index.db" index "$d/files" > /dev/null &
ipid=$!
peak=0
fpeak=0
# On macOS laserdotd re-executes itself inside its sandbox, so the
# process to measure is the largest one started with this --db.
daemon_ps() { ps -axo rss=,pid=,command= | grep "laserdotd --db $d/mem/index.db" | grep -v grep | sort -n | tail -1; }
daemon_rss() { daemon_ps | awk '{print $1}'; }
# Resident memory counts file pages macOS can drop for free (the mapped
# index); the physical footprint is what Activity Monitor's Memory column
# shows: memory only this process holds. In KB, 0 when it cannot be read.
footprint_kb() {
  pid=$(daemon_ps | awk '{print $2}')
  [ -n "$pid" ] || { echo 0; return; }
  vmmap --summary "$pid" 2>/dev/null | awk '/^Physical footprint:/ {
    v=$3; u=substr(v, length(v)); n=substr(v, 1, length(v)-1)+0
    if (u=="K") n=n; else if (u=="M") n=n*1024; else if (u=="G") n=n*1024*1024; else n=v/1024
    printf "%d\n", n; exit }' | grep . || echo 0
}
n=0
while kill -0 "$ipid" 2>/dev/null; do
  rss=$(daemon_rss || echo 0)
  [ "${rss:-0}" -gt "$peak" ] && peak=$rss
  # The footprint is slower to read: once a second.
  if [ $((n % 5)) -eq 0 ]; then
    fp=$(footprint_kb)
    [ "${fp:-0}" -gt "$fpeak" ] && fpeak=$fp
  fi
  n=$((n + 1))
  sleep 0.2
done
wait "$ipid" || true
for q in "invoice pdf" "beach jpg" "kind:video"; do "$d/laserdot" --db "$d/mem/index.db" search "$q" > /dev/null 2>&1 || true; done
sleep 5 # the daemon hands freed memory back a moment after indexing
idle=$(daemon_rss || echo 0)
fidle=$(footprint_kb)
entries=$("$d/laserdot" --db "$d/mem/index.db" status | grep -oE '\(([0-9]+) entries\)' | grep -oE '[0-9]+' || echo 0)
echo "the daemon indexed $entries entries"
[ "${entries:-0}" -ge "$FILES" ] || { echo "::error::the daemon did not index the files ($entries entries): the memory number is meaningless"; exit 1; }
kill "$dpid" 2>/dev/null || true
peak_mb=$((peak / 1024)); idle_mb=$((${idle:-0} / 1024))
fpeak_mb=$((fpeak / 1024)); fidle_mb=$((${fidle:-0} / 1024))
echo "daemon memory (Activity Monitor's Memory column, physical footprint): peak $fpeak_mb MB while indexing $FILES files, $fidle_mb MB after"
echo "daemon resident memory (includes index pages macOS can drop): peak $peak_mb MB, $idle_mb MB after"
# The limit applies to the footprint when it can be read, else to resident memory.
limit_mb=$fpeak_mb; [ "$fpeak_mb" -gt 0 ] || limit_mb=$peak_mb
python3 -c "import sys; sys.exit(0 if $rate >= 10000 else 1)" || { echo "::error::indexing too slow: $rate files/s"; exit 1; }
python3 -c "import sys; sys.exit(0 if $worst < 50 else 1)" || { echo "::error::a search took $worst ms"; exit 1; }
[ "$limit_mb" -lt "$MAX_MB" ] || { echo "::error::the daemon used $limit_mb MB"; exit 1; }
rm -rf "$d"
