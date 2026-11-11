#!/usr/bin/env bash
# Speed on a real Mac with real files (spec §12, §15.3): creates 100,000
# files in nested folders, indexes them with the CLI, and times searches.
# Fails only far below the targets (CI disks vary): indexing under 10,000
# files a second, or a search over 50 ms.
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
"$d/laserdot" --db "$d/data/index.db" index "$d/files" > /dev/null
end=$(python3 -c 'import time; print(time.time())')
rate=$(python3 -c "print(int(100000 / ($end - $start)))")
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
python3 -c "import sys; sys.exit(0 if $rate >= 10000 else 1)" || { echo "::error::indexing too slow: $rate files/s"; exit 1; }
python3 -c "import sys; sys.exit(0 if $worst < 50 else 1)" || { echo "::error::a search took $worst ms"; exit 1; }
rm -rf "$d"
