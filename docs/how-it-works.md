# How Laserdot works now

One page, kept current: what is true today. The history and the reasons are in
[`design-conversation.md`](design-conversation.md), the binding rules in [`business-rules.md`](business-rules.md) and
[`interface-rules.md`](interface-rules.md), the details in the spec.

## The parts

- **Laserdot.app** (SwiftUI, menu bar, ⌥Space window). No network code, not sandboxed (so files open in their apps).
  Talks to the engine over a Unix socket only.
- **laserdotd** (Go). Owns the index, answers searches, watches for changes. Denied the network by macOS before it
  does anything else; runs at low priority with throttled disk access, so it never slows the Mac.
- **Laserdot Updater** (a helper app). The only networked code: finds and downloads releases from GitHub; nothing is
  installed without GitHub's build proof, and each Mac signs the update with its own key.

## The index

- **This Mac's folders** (home, Applications): one SQLite database plus an in-memory index; a snapshot file lets the
  engine start in milliseconds. A first index walks the folder, writes it, and makes files searchable before reading
  media headers and Finder tags in the background.
- **Drives**: each drive keeps its own index on the drive (or on this Mac for read-only drives, deleted when the
  drive leaves). A drive's first index is streamed folder by folder: searchable while it runs, bounded memory,
  checkpoints inside the drive's own index; it can be stopped and continued, and resumes after an unplug. The index
  file on a drive is open only during a job, so a drive can always be ejected; when macOS is about to eject one, the
  engine lets go first.
- **Keeping current**: FSEvents for changes, a folder-time check every 30 minutes while FSEvents misses nothing
  (every 5 otherwise), an hourly catch-up; returning
  APFS/HFS+ drives are checked by folder times, other drives and once a week in full.

## Searching

- Plain words and filters (`kind:video res:4k since:7d`) compile to regular expressions over the in-memory names,
  with a trigram prefilter; media filters use columns and bitmaps; contents use SQLite FTS5.
- After a search, the **choice row** offers what the results have (resolution, sound, shape, camera, type, date,
  size…), with counts; a choice is added to the search as a word, its group shrinks to it, the others stay. It also
  offers where: **Drive** (`volume:local` or a drive, when one is attached) and **Folder** (`in:` the folders holding
  the results: under each indexed folder it goes down while one folder holds them all, then offers its subfolders).
- Speed: about 1–2 ms on 100,000 files, under 10 ms on a million; plain words and simple wildcards are matched
  without the regex engine (same answers, proved by test).

## Long jobs and safety

- Duplicates (identical, similar pictures — iPhone HEIC and RAW through macOS's ImageIO — re-encoded videos) and cleanups: dry run first, files moved to the Trash
  or a folder, never deleted; one copy always stays; undo for a week.
- Every long job shows its step and elapsed time, can be stopped and continued, and is saved as it goes. Quitting,
  ejecting or an update stops jobs at once (indexing pauses while an update installs).
- Storage window: sizes from the index; apps and packages measured once and remembered; items can be moved to the
  Trash (confirmed, undoable).

## Testing and releasing

- Go tests (Linux and macOS, race detector), a crash test that kills a real process, a random lifecycle test, unit
  tests for the app's logic, and a self-test that drives the real app in English and Arabic; CI also ejects a drive
  while the app indexes it.
- A commit is tested once on its branch; `main` is fast-forwarded to it and releases itself. User-facing releases are
  batched (see business rules §5).
