# Design ideas

The thinking behind Laserdot's shape, and the ideas still waiting their turn. Rules that are already binding live in
[`business-rules.md`](business-rules.md) and [`interface-rules.md`](interface-rules.md).

## The ideas behind it

- **A Spotlight that can't phone home.** The search engine is blocked from the network by macOS itself, so privacy
  is a property you can check, not a promise.
- **Say it the way you think it.** `photos last week`, `big videos`, `pdf yesterday`: plain words stand for filters,
  and the hint line says how they were read, so people learn the precise language (`kind:image since:7d`) without a
  manual.
- **Know the media without opening it.** Resolution, HDR, codecs, audio channels, languages, subtitles and EXIF come
  from reading file headers only, so `res:4k atmos:true` answers in milliseconds.
- **Drives that remember.** A drive carries its own index, so it is searchable on any Mac, and its confidentiality
  goes with it.
- **Duplicates by quality.** Not just identical files: similar pictures and re-encoded videos, with a recommendation
  for the best copy, and nothing ever deleted.
- **Familiar first, clever second.** Spotlight's habits for everything people already know; Laserdot's extras only
  where Spotlight has nothing (filters, Quick Look on Space, actions, commands).
- **Useful return, not time spent.** Laserdot should be opened often and closed quickly. Reminders only for tidying,
  outcomes instead of badges, suggestions drawn from the user's own files.
- **It fixes itself.** A bug is caught and logged, a stuck engine is restarted, an interrupted index heals, a stuck
  drive is skipped. The user should not need to know any of it happened.

## Waiting their turn

From the search review of 2026-11-20 (the owner chose 2, 4, 5, 8 and 10, all done in 0.9.28), still open:

- **Saved searches** in the menu ("videos in Trips over 1 GB"), beyond today's pins.
- **Search drives that are not plugged in**, from a small copy of each drive's names kept on this Mac ("on Archive
  (not connected)").
- **Where a file came from** (`from:github.com`), read from what macOS already records on downloads.
- **Short path typing**: "dl inv" finds Downloads › invoice.pdf.
- **Group versions**: "report v1, v2, final, final2" folded into one row.
- **Similar-looking pictures in More Like This** (needs pictures fingerprinted at search time; Find Duplicates
  already finds look-alikes).
- Hide a choice that every result already has (e.g. "Documents 1" when the one result is a document).
- Drives indexed before 0.9.30 keep `.DS_Store` entries until rebuilt; clean them on attach too.

From the speed review of 2026-11-16 (measured on CI's Mac with a million files), in the order proposed:

1. **Duplicate pictures through macOS's ImageIO** — done in 0.9.27 for HEIC, RAW and AVIF; JPEG and PNG keep the
   pure-Go decoder so saved fingerprints still compare.
2. **Walk and write at the same time** — done for drives in 0.9.19 (streamed, searchable while indexed, bounded
   memory, resumable). Measured for this Mac's own folders and not adopted (the bulk path stayed faster).
3. **Lighter typing.** Each keystroke sends four requests; stale searches still run while typing fast. Skip them and
   fold the requests together.
4. **The slowest search** — done: 11.4 ms after 0.9.26, about 7 ms after 0.9.27 (plain words and simple wildcards
   matched without the regex engine).
5. **A cheaper missed-change check** — done in 0.9.27: every 30 minutes instead of 5 while FSEvents misses nothing.
6. **Launch:** most of 200–320 ms is macOS setting up windows before Laserdot's code runs; create less up front.
7. **Video frames through AVFoundation** (hardware decoding) instead of starting ffmpeg for each video.

Smaller ideas noted along the way:

- A Stop button for indexing a drive — done in 0.9.26 (Stop Indexing / Continue Indexing).
- Stream this Mac's own first index too, and let FSEvents say which folders changed instead of comparing times.
- The self-test's per-step timings (printed by `check-selftest.py`) show the steps at about 2.7 s each and the
  windows tour at 22 s; the waits that cost minutes were removed on 2026-11-20.

## Left out on purpose

- An Apple Developer ID signature and notarization (needs a paid account; updates carry GitHub's build proof
  instead, and each Mac signs them with its own key).
- More languages than English and Arabic, for now.
- Anything outside files, media and disks (see [`business-rules.md`](business-rules.md) §7).
