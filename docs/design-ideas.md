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

From the speed review of 2026-11-16 (measured on CI's Mac with a million files), in the order proposed:

1. **Duplicate pictures through macOS's ImageIO.** HEIC (every iPhone photo), RAW and AVIF are not compared for
   similarity at all today, and other pictures are decoded at full size in pure Go before being shrunk to 32×32.
   ImageIO reads all of them and can decode straight to a small size; likely several times faster.
2. **Walk and write at the same time** — done for drives in 0.9.19 (streamed, searchable while indexed, bounded
   memory, resumable). Still to do for this Mac's own folders, which keep the tuned bulk path.
3. **Lighter typing.** Each keystroke sends four requests; stale searches still run while typing fast. Skip them and
   fold the requests together.
4. **The slowest search** rose from 23 to 38 ms on a million files; find that query and bring it down.
5. **A cheaper missed-change check:** ask FSEvents which folders changed instead of looking at every folder every
   5 minutes.
6. **Launch:** most of 200–320 ms is macOS setting up windows before Laserdot's code runs; create less up front.
7. **Video frames through AVFoundation** (hardware decoding) instead of starting ffmpeg for each video.

Smaller ideas noted along the way:

- A Stop button for indexing a drive, as Find Duplicates has (the engine can stop and resume since 0.9.19; the
  button is missing).
- Stream this Mac's own first index too, and let FSEvents say which folders changed instead of comparing times.
- Use the self-test's per-step timings (printed by `check-selftest.py`) to trim its slowest steps; the self-test is
  about 12 of the app job's 17 minutes.

## Left out on purpose

- An Apple Developer ID signature and notarization (needs a paid account; updates carry GitHub's build proof
  instead, and each Mac signs them with its own key).
- More languages than English and Arabic, for now.
- Anything outside files, media and disks (see [`business-rules.md`](business-rules.md) §7).
