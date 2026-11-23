# How Laserdot works now

One page, kept current: what is true today. The history and the reasons are in
[`../history/design-conversation.md`](../history/design-conversation.md), the binding rules in [`business-rules.md`](business-rules.md) and
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
- **Packages** (`scan.DefaultOpaqueExts`): an app is one result, its contents never indexed; every other package
  (Ulysses sheets, RTFD, Scrivener, Xcode projects, Final Cut and iMovie libraries, frameworks, Photos and Music
  libraries…) is not a result at all, nor anything inside it (`scan.IsHiddenPackage`, 0.9.45); an older index is
  cleared of them once (`junkRules` "3").
- **Choices' time limit**: `core.RefineBudget` (150 ms); past it the groups counted so far are offered (a chosen
  value always is). Over a million entries they take about 50 ms (`BenchmarkRefinements1M`).
- **What's New**: after an update with news, a blue dot on the menu-bar icon (`menuBarIconWithNews`) and a What's New
  item in its menu; no window opens by itself.
- **Health log**: `health-log.txt` beside the index, one line per launch (start-up and ready times on this Mac,
  entries, watchdog restarts, signature); never shown, never sent; the end is in Save Diagnostics; erased with
  everything else.
- **Ranking** with words: match quality, then opened more, files before folders, then changed more recently (today,
  this week, month, quarter, year, older), then shorter names and shallower paths; without words, newest first. An app's iCloud folder shows as the app
  ("iCloud Drive › Ulysses", `PathText.Root.iCloudApp`).
- **While searching** the round buttons show the kinds of files and the tools fold into **More** (`barButtons`,
  `barTools`; ⌘T, ↑↓, Return, Esc); the hint hides when only pills are in the box and the choices show.
- **Checks**: CI's app checks run on two Macs side by side (the English self-test; every other check), about 8 minutes.
- **Moving around** (0.9.47): → on a folder result searches inside it (`FolderNav`, an `in:` pill; ← goes back,
  `folderTrail`); in the grid ←/→ move tiles. Tab with nothing to complete focuses the choices (`choiceFocus`,
  `ChoiceNav`): ←/→ move, Tab/↑↓ change group, Return picks, Esc returns to the field.
- **Smart lists** (`SmartList.builtIn`): ready-made searches on the empty box with live counts (empty ones hidden);
  pinned searches are counted too.
- **Timeline strip**: the `timeline` op counts a search's results by month (`core.Timeline`); the app fills the gaps
  (`TimelineLogic.months`, last 120) and a click or drag adds `modified:YYYY-MM[..YYYY-MM]`, replacing a date filter.
- **Rename several files** (`BatchRename`, `renameFiles`): ## becomes the number, extensions kept, all or nothing
  (through temporary names), one undo step.
- **Video frames** (`VideoFrames`, 0.9.48): AVFoundation for what macOS reads, else the bundled ffmpeg (two at a time,
  20 s each, ffprobe for the length); used for video thumbnails macOS has none for (MKV, WebM, AVI…) and for scrubbing.
- **Grid**: videos scrub under the pointer (`ScrubThumb`, ten frames, all fetched once the pointer comes in, the last
  shown until the next arrives; the resting picture is a frame a tenth in); a picture the
  pointer rests on (0.6 s) shows 420 pt wide in a popover beside its tile (`PhotoPeek`, `SearchModel.peekPath`);
  tiles up to 384 pt.
- **The search window** (`OverlayController`) stays open until closed (Esc, ⌥Space) — opening a result no longer
  hides it; when another app comes forward it drops from floating to a normal window, and floats again when it takes
  the keyboard. It belongs to the desktop it was opened on (`.moveToActiveSpace`): ⌥Space or the menu brings it to
  the current one, or hides it when it is already there and in front.
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
