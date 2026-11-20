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
  ⌥-click (or right-click ▸ Leave Out) leaves a value out: a `-` word (`-ext:pdf`), shown struck through.
- **Pills:** the query is one string; the field shows its typed words and its filter words as pills before them
  (`Pills.split`/`join` in LaserdotKit). A filter becomes a pill once a space follows it; choices are added in front,
  so typing goes on at the end. A chosen group leaves the choice row (its pill stands for it).
- **Place menu:** the magnifying glass sets Settings ▸ Search in (Everywhere, This Mac, recent folders, a folder).
- **Default place:** Settings ▸ General ▸ Search in (Everywhere, This Mac, a folder) adds `volume:local` or `in:"…"`
  to every search that names no place; a pill shows it, and Search Everywhere drops it until the field is cleared.
- **System junk** (`.DS_Store`, `._*`, `.localized`, `Icon\r`, `Thumbs.db`, `desktop.ini`…) is never indexed
  (`scan.IsSystemJunk`, in the same rule as partial downloads); an older index is cleared of it once on opening.
- **Content search** reads no hidden folder (`.obsidian`, `.git`, an app's plugins); among content matches,
  documents come first, then other files, then code.
- **Inside zips:** the daemon lists the names inside this Mac's `.zip` files (the central directory only) into
  `archives.db`; plain words match those names with the same regexes, and the zip is a result after name and content
  matches, with "Contains …". Filters apply to the zip itself.
- **Date words:** "march 2024", seasons ("last summer", "summer 2023"), "between jan and mar [2024]" and their Arabic
  forms become one `modified:` range; a month alone stays a word.
- **More Like This** (⌘K): `ext:` + `fsize:` half to twice + `modified:` ±30 days of the file, put in the field.
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
