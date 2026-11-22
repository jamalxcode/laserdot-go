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
  installed without GitHub's build proof, and each Mac signs the update with its own key. A version installed by
  hand from the disk image (signed ad hoc) re-signs itself with that key at its first launch and relaunches, before
  macOS is asked anything, so Full Disk Access is granted once and kept. Save Diagnostics shows the signature and the
  last signing problem.

## The index

- **This Mac's folders** (home, Applications): one SQLite database plus an in-memory index; a snapshot file lets the
  engine start in milliseconds. A first index walks the folder, writes it, and makes files searchable before reading
  media headers and Finder tags in the background.
- **Drives**: each drive keeps its own index on the drive (or on this Mac for read-only drives, deleted when the
  drive leaves). A drive's first index is streamed folder by folder: searchable while it runs, bounded memory,
  checkpoints inside the drive's own index; it can be stopped and continued, and resumes after an unplug. The index
  file on a drive is open only during a job, so a drive can always be ejected; when macOS is about to eject one, the
  engine lets go first. A new drive is offered for indexing once — but not a disk image opened to install an app
  (read-only, or with an app, an installer package or an Applications shortcut at its top).
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
  The groups come top down, large to granular (`core.refineOrder`): drive, folder · kind, type · size, modified ·
  resolution, shape, HDR, codec · length, sound · camera, place, picture · artist, album, genre; each family is a
  shaded band of its own (Okabe–Ito colours and an icon each). The calm view (`RefineLogic.compact`) hides choices
  that would give nothing, keeps a group's four biggest values and the first four bands; More choices shows all.
- **One screen:** both windows show the same thing, always: the search box as a rounded bar, a row of round
  buttons (Videos, Images, Music, Documents, Apps, Recent Downloads, Big Files — each adds or removes its pills,
  keeping the typed words — then Duplicates, Storage, Drives while one is attached, Index a Folder, Settings), then
  hints, choices and results; recent files when the box is empty.
- **Reset:** the ✕ in the box clears words, pills, the search place and the order; a place whose folder is gone
  counts as Everywhere. A search shows up to 1,000 results, and Show More adds 1,000 at a time.
- **Show in Finder** (`Finder.reveal`, `RevealPlan`): a file inside a package (a .ulysses sheet, an app) is shown in a
  window on its own folder, as Show Package Contents would; Finder alone stops outside the package.
- **Menu-bar dot:** flashes red, white, blue, green while Laserdot works (indexing, details, a drive, duplicates, a
  refresh); the engine is asked every 5 s with one small request at background priority, never two at once, so the
  nicety never holds up real work. The round buttons wrap in a narrow window.
- **Fresh results:** reopening the window runs the search again; after a file action the app sends `touched` with
  the folders it changed, and the engine lists them again at once (drives included).
- **Opening** a result (Return, double-click, Open With, Show in Finder, Open in Terminal) hides the ⌥Space window.
  Right-click on a result: Open, Quick Look, Show in Finder, Copy Path, Rename…, More Like This, Move to Trash, More.
- **Freezes** of the app's window (2 s or more) are written to `~/Library/Logs/Laserdot/app-hangs.log` with what it
  showed (never a search or path); Save Diagnostics includes them. PDF previews are read off the main thread.
- **Pills:** the query is one string; the field shows its typed words and its filter words as pills before them
  (`Pills.split`/`join` in LaserdotKit). A filter becomes a pill once a space follows it; choices are added in front,
  so typing goes on at the end. A chosen group leaves the choice row (its pill stands for it).
- **Place menu:** the magnifying glass sets Settings ▸ Search in (Everywhere, This Mac, recent folders, a folder).
- **Default place:** Settings ▸ General ▸ Search in (Everywhere, This Mac, a folder) adds `volume:local` or `in:"…"`
  to every search that names no place; a pill shows it, and Search Everywhere drops it until the field is cleared.
- **System junk** (`.DS_Store`, `._*`, `.localized`, `Icon\r`, `Thumbs.db`, `desktop.ini`…) is never indexed
  (`scan.IsSystemJunk`, in the same rule as partial downloads); an older index is cleared of it once, in the
  background after opening, and a drive's index each time the drive is attached.
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
- A disk that stops answering (a sleeping network share) never holds the index lock: the drive list and the drive
  checks look at disks without it, the app asks for that list one request at a time, and its "this folder changed"
  notes go on their own queue, so searches keep working.
- Recovery: a bug in a request or a background job is caught, written to `laserdotd-crash.log` and the daemon carries
  on (it restarts itself if the bug left the index locked). The app's watchdog restarts a daemon that is gone or stops
  answering, but gives one that is still loading its index up to 3 minutes; the daemon opens its socket before it
  writes the snapshot. A damaged index or snapshot is set aside and rebuilt; two failed launches in a row offer a
  fresh index.

## Testing and releasing

- Go tests (Linux and macOS, race detector), a crash test that kills a real process, a random lifecycle test, unit
  tests for the app's logic, and a self-test that drives the real app in English and Arabic; CI also ejects a drive
  while the app indexes it.
- A commit is tested once on its branch; `main` is fast-forwarded to it and releases itself. User-facing releases are
  batched (see business rules §5).
