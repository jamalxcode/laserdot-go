# Laserdot 🔴

**Spotlight's speed, Finder's manners, and a search engine that physically cannot phone home.**

You press ⌥Space, type `big videos last month`, and before you've lifted your finger the answer is there: a 4K clip
from March, sorted to the top because you open it a lot. You type `صور الاسبوع الماضي` and it understands that too.
You ask for `res:4k atmos:true` and it answers from a million files in a few milliseconds, without opening a single
one. Meanwhile the engine that read all of that is locked out of the network by macOS itself — not by a promise, by
the operating system.

That's Laserdot: a private, media-aware search for your Mac, built by one owner with a very clear idea of what a
file search should be, and a robot that types into it on every change to prove it still is.

## What it does

- 🔎 **Finds things the way you say them.** `jpg`, `photos last week`, `big videos`, `pdf yesterday`,
  `invoice march`, `4k hdr` — and Arabic: `صور الاسبوع الماضي`, `فاتورة ٢٠٢٤`. A hint under the box shows how it read
  you (*“last week means since:7d”*), so you pick up the precise language for free.
- 🎬 **Knows your media without opening it.** Resolution, codecs, HDR, frame rate, audio channels, track languages,
  subtitles, camera EXIF, music tags: `res:4k codec:hevc`, `lang:ar channels:5.1`.
- 🎚️ **Narrow it down with a click.** Search `mov` and a row of choices shows what your results actually have —
  Videos · 4K 11 · 1080p 15 · With sound 82 · Portrait 45 · Under 1 min 47 · in Trips · on This Mac… Pick one and it
  becomes a pill in the search box ("4K ✕"); hover and press **−** to leave one out ("not PDF"). The other kinds of
  choices stay so you can keep going, like the filters in a good shop.
- 📍 **Search where you mean.** The magnifying glass is a menu: Everywhere, This Mac, a recent folder, or any folder
  you choose — remembered until you change it.
- 🗜️ **Finds files inside zips** (`receipt` finds *taxes 2023.zip*, "Contains 2023/receipt.pdf") by reading only
  the zip's table of contents, and **More Like This** (⌘K) finds files of the same type, size and age.
- ⚡ **Fast and live.** Its own index; a million files are searchable about 7 seconds after a first look, searches
  take milliseconds, and a file you delete in Finder leaves the results right away. System clutter (`.DS_Store`,
  `._` copies, `Thumbs.db`…) never shows up, and hidden app folders are never read for words.
- ⌨️ **Types like Spotlight.** The top result's name completes as you type, Esc clears then closes, your last search
  comes back selected — everything your fingers already know, plus Quick Look on Space and ⌘K actions.
- 💽 **Drives that remember — and let go.** An external drive keeps its own index, so it's searchable on any Mac
  (encrypted if you like). A huge drive is searchable while it's being indexed, can be paused and continued, and
  always ejects: Laserdot holds nothing open on it and steps aside when you eject.
- 🐢 **Never in your way.** The engine works as a background task, like Spotlight's: your apps get the disk first,
  and indexing pauses while an update installs.
- 🧹 **Duplicates by quality.** Identical files, similar pictures (iPhone HEIC and camera RAW included) and re-encoded videos; it recommends the best copy
  and **never deletes** — files go to the Trash after a clear confirmation. It tells you what you freed, too.
- 🛟 **Fixes itself.** Bugs are caught and logged, a stuck engine is restarted, a dead drive is skipped, an
  interrupted index heals. You shouldn't notice any of it.
- 🔒 **Private by construction.** No AI, no telemetry, no accounts. The only part allowed online is the updater, and
  only to fetch new versions from GitHub — each one carrying proof that Laserdot's own build made it.

## Install

1. Download the latest **Laserdot-x.y.z.dmg** from [Releases](https://github.com/jamalxcode/laserdot-go/releases/latest).
2. Open it and drag **Laserdot** to **Applications**.
3. Open Laserdot. macOS asks about Desktop, Documents, Downloads, iCloud Drive and any connected drive, all at once
   — click **Allow** on each. Laserdot then indexes your apps and your home folder by itself (skipping `~/Library`,
   except iCloud Drive). If it finds an older copy of Laserdot on your Mac, it offers to move it to the Trash.

Updates install themselves: Laserdot checks once a day, downloads a new version in the background and installs it
when you quit (or now, from **Check for Updates…** in the menu-bar icon's menu). No account or token is needed. Each
update comes with GitHub's proof that Laserdot's own release build made it, and is installed only when that proof
checks out. Each update is then signed on your Mac with a key Laserdot makes there, so macOS keeps your "Allow" answers. Not happy with
an update? **Settings ▸ General ▸ Go Back**.

The app is signed ad hoc, not with an Apple Developer ID, so the first launch may need **right-click → Open**.

## Using it

Press **⌥Space** anywhere (change it in **Settings**), or use the Laserdot window. Like macOS's Spotlight, it starts
as a rounded search bar with round buttons beside it: **Recently Opened, Videos, Images, Music, Documents, Apps**,
then **Find Duplicates, Storage, Drives** (while a drive is connected), **Index a Folder** and **Settings**. The rest
appears when it's needed — start typing, press ↓, or click a button.

| You type | You get |
|---|---|
| `invoice march` | names with both words, in any order (and documents containing them) |
| `jpg`, `avi`, `ppt` | that file type (`jpg` = .jpg, .jpeg, .jpe; `ppt` = all PowerPoint files) |
| `photos`, `videos`, `music`, `apps` | that kind of file |
| `today`, `yesterday`, `last week`, `this month` | files changed then |
| `march 2024`, `last summer`, `between jan and mar`, `الصيف الماضي` | files changed in that month, season or span |
| `big`, `huge`, `small` | files over 100 MB, over 1 GB, under 100 KB |
| `4k`, `1080p`, `hdr`, `screenshots` | videos by resolution or HDR, screenshots |
| `"avi"` | the word itself in names (quotes turn the clever words off) |
| `res:4k codec:hevc -sample` | precise filters (**⌘/** lists them all) |
| `> dup` | commands: Find Duplicates, Settings, Eject a drive… |

The hint under the box says what a clever word stands for, e.g. *“last week means since:7d”*.

Typing works like Spotlight: the top result's name completes as you type (the rest shown selected; → or Tab takes
it, Return opens it), and when you reopen the window your last search is there, all selected. Filters show as
**pills** before your words — `ext:pdf` reads "PDF ✕", `-in:~/Downloads` reads "not in Downloads"; ✕ removes one,
and Delete with nothing typed removes the last. Typing a filter and a space turns it into a pill. With the box
empty, one-click starts (**Recent Downloads, Photos This Month, Videos, Documents This Week, Big Files**) and a
suggestion from your own files (e.g. *“See your 4K videos (812)”*) are a click away.

**Where to search.** Click the magnifying glass: **Everywhere**, **This Mac**, a recent folder or **Choose
Folder…** (the same as **Settings ▸ General ▸ Search in**). The place's name shows beside the glass while it narrows
your searches; a search that names its own place (a Folder choice, `in:`) looks there instead.

**Results** come like Spotlight's: a **Top Hit**, then Applications, Documents, Images, Videos, Folders… (five each,
**Show all** for more). Files you open often rise to the top. The empty box shows what you opened recently and new
downloads.

| Key | Does |
|---|---|
| ↑ ↓ | move through results (⇧↑ ⇧↓ or ⌘-click select several) |
| Return / ⌘1–⌘9 | open the selected / the numbered result |
| Space (after ↑↓ or a click), or Space twice | Quick Look: pictures large, videos and songs playing |
| ⌘K | actions: Open, Quick Look, Show in Finder, Copy Path, More Like This, Search in This Folder, Move to Trash — then **Open With…** and **More Actions…** (Copy file or link, Share, Tags…, Compress, Move to…, Open in Terminal, Rename) |
| ⌥⌘Z | undo the last Move to Trash, Rename, Move to…, Compress or tag change (or click **Undo** on the bar); press again for the one before — Laserdot remembers 20 steps for a week, even after quitting |
| ↑ in the empty box | your earlier searches |
| ⌘↩ / ⌘⇧C | Show in Finder / copy the path |
| ⌘D | pin the search (pins show on the empty box) |
| ⌘J | the next view: rows, columns (click a header to sort, like Finder), thumbnails |
| ⌘R | refresh: look at every indexed folder again (deleted files leave the index) |
| ⌘↓ / ⌘↑ | the next / previous group of results |
| ⌘C | copy the selected file (paste it in Finder or Mail) |
| → or Tab | take the completed name; Tab also completes a filter / ⌥1–⌥9 press a suggestion chip |
| Esc | close actions or the cheat sheet, then clear the search, then close the window |

**Choices under the search.** After a search, the row under the hint shows what your results have to choose from,
with counts: first what they are (Videos, Documents, Folders…); for videos the resolutions, with sound or without,
portrait or landscape, length and codec; for pictures shape, camera, location, screenshots and Live Photos; for
music artist, album and genre; for documents their type; the **drive** (This Mac or each attached drive) and the
**folders** the results are in; and date and size for everything. Click one and it becomes a pill in the search box;
its group leaves the row (remove the pill to choose again) and every other kind of choice stays, each count saying
what it gives with your other picks. Hover a choice and press **−** (or ⌥-click, or right-click ▸ Leave Out) to
leave it out: the pill says "not …".

Results can also be shown as **columns** like Finder's list (Name, Date Modified, Size, Kind, Last Opened, Date
Created — click one to sort, again to reverse, drag the line before it to resize) or as **thumbnails** (the slider or
⌘+ ⌘− change their size) — the view switch at the top, or ⌘J. A file deleted in Finder disappears from the results right away;
**⌘R** looks at every indexed folder again. Refresh, the result order, pinning and the filter list are in the
**…** menu next to the view switch.

The preview pane on the right shows a large thumbnail, plays videos and songs, pages through PDFs, and for a
document found by its words, the passage where they are. Drop a folder on the window to search inside it.

Long result lists stop at **Show More** rather than loading forever. Drag the window anywhere: it opens there next
time.

**Find Duplicates** (File menu, or `> dup`) finds identical copies, similar pictures (iPhone HEIC and camera RAW
too) and re-encoded videos and recommends the best copy to keep, describing each copy in plain words ("high
quality"). Pick what to look at (Everything, Videos, Images…) and where (Everywhere or a folder); a search for the
scope is under **Advanced**. It shows each step with a bar, how many files are done and the time so far; it can
be stopped at any point (**Stop and Show What's Found**) and picks up where it left off (**Continue Scan**), saving
what it has read every 30 seconds. Cleaning up is one button — **Move N Duplicates to Trash (size)…** — then a
confirmation; nothing is ever deleted, and Laserdot keeps a running total of the space it has helped clear.

**Library ▸ Storage** (⇧⌘S) shows what fills your disk, folder by folder, and the largest files — apps and other
packages at their real size (measured once and remembered), counted as apps rather than files. Right-click an item
to show it in Finder or move it to the Trash (confirmed first; Undo puts it back). The **…** menu orders
results by name, size or date. `laserdot://search?q=…` links open a search (use them
in Shortcuts with **Open URL**).

**Settings** (⌘,): the search shortcut, open at login, pausing background work on battery, optional **Reminders**
(check for duplicates every month or 3 months, a warning when the disk is nearly full — off unless you turn them on;
they are notifications made on your Mac), automatic updates (and
**Go Back** to the version before the last update), **Folders** (what is indexed, folders that are never
indexed, and what Laserdot couldn't read, with how to fix it), **Content Search**, **Drives**, and **Privacy** (what is kept and its size, Clear History, **Erase All
Laserdot Data**). **Help** has the search syntax, the
welcome tour, What's New, and **Save Diagnostics** (a file for a bug report, with every path removed; it also shows
how long Laserdot took to start, any bugs the engine caught and how often it was restarted).

**If something goes wrong** Laserdot recovers by itself: a bug in the search engine is caught and logged and the
engine keeps running; if the engine stops or hangs it is restarted within a minute; a drive that stops answering is
skipped after 30 seconds; indexing waits for free space instead of failing halfway; and if Laserdot fails to start
twice in a row it offers to start with a fresh index (your files are never touched).

## Where things are kept

Everything stays on your Mac, in `~/Library/Application Support/laserdot/` (**Settings → General → Index → Show in
Finder**):

- `index.db` — names, sizes, dates, media details; `index.db.snapshot` — a copy for a fast start.
- `laserdotd-crash.log` — bugs the engine caught and kept running through (never sent; Save Diagnostics includes it).
- `content.db` — the words inside documents (your whole home folder unless you change **Settings → Content Search**;
  hidden folders such as `.obsidian` or `.git` are never read).
- `archives.db` — the names of the files inside your zip archives (only each zip's table of contents is read).
- Opened-results history and settings live in the same index; nothing is ever sent anywhere.
- **Settings → Privacy** shows each of these with its size; **Erase All Laserdot Data** deletes them all (and the
  remembered passphrases) and starts over with nothing indexed.
- The version before the last update is kept in `~/Library/Application Support/Laserdot Previous Version/`.
- `~/Library/Application Support/Laserdot Signing/` holds a key Laserdot makes on your Mac to sign each update before
  installing it, so macOS keeps your "Allow" answers across updates. It never leaves your Mac; Erase All Laserdot
  Data deletes it.

**External drives** keep their own index on the drive, in a hidden `/Volumes/<drive>/.laserdot/` folder
(`index.db`, `volume.json`), with paths relative to the drive — so it works on any Mac. Laserdot asks before indexing
a drive and can encrypt the index with a passphrase that is never written down (unless you tick **Remember in my
Keychain**: then macOS's Keychain keeps it and the drive unlocks by itself). A big drive is searchable while it is
being indexed (top folders, recently changed ones and the folders you search in come first), uses little memory
whatever its size, and if you unplug or eject it halfway, it picks up where it stopped next time. **Drives ▸ your
drive ▸ Stop Indexing** pauses it (the drive stays searchable with what was read); **Continue Indexing** goes on.
Laserdot never keeps a drive from ejecting: its index file on the drive is open only while it works on it, and when
you eject, Laserdot stops what it is doing there and lets go. When the drive
comes back, only new or changed files are read again (on Mac-formatted drives, just the folders that changed). A **read-only** drive cannot hold its index, so it is kept on this Mac in
`…/laserdot/drives/` — and deleted as soon as that drive is no longer connected (checked at start, every minute,
when a drive leaves, and on quit).

Media details come from Laserdot's own readers, and for the rest from the **bundled ffmpeg/ffprobe** — built from
FFmpeg's source under the LGPL, without any network support (license in `Laserdot.app/Contents/Resources/FFmpeg`).

## Privacy

- `laserdotd`, the search engine that reads your files, asks macOS to deny it all network access before anything
  else; the app shows it (the lock icon) and CI tests it.
- The app itself contains no network code (CI fails the build if any appears outside the updater).
- The **Laserdot Updater** helper is the only networked part, by the owner's decision: it contacts github.com only,
  once a day or when asked, sends no data about you or your files, and only downloads a release.
- No AI, no embeddings, no telemetry, no crash reporter.

## Behind the scenes

A few things that make this project unusual:

- **A robot uses the app on every commit.** In CI, the real Laserdot.app types into its own ⌥Space window with key
  presses, opens Quick Look, renames and trashes files and undoes it, opens Settings, freezes its own search engine to
  check the watchdog brings it back, and erases all its data — in English and in Arabic, right to left — then prints
  screenshots into the job log.
- **It gets killed on purpose.** A crash test kills a real Laserdot process at random moments while it indexes and
  scans, then checks the index comes back exactly right. On its very first runs it found a real bug, now fixed.
- **Its dialogs can't hide.** Every Laserdot dialog stays in front, on every desktop and over full-screen apps,
  and the self-test checks the update prompt does.
- **It ejects a drive mid-index on purpose.** CI starts the real app, has it index a 30,000-file drive, and ejects
  the drive the way Finder does — the eject must succeed and the app must keep running.
- **Every update proves where it came from**, using GitHub's keyless build attestation; there's no signing key
  anywhere to steal.
- **Every decision is written down.** What the owner asked for, what was decided and why, and what went wrong along
  the way — see the documents below. Most features started as a screenshot from the owner's Mac mini.

Some numbers from GitHub's Mac (which is slower than yours):

| | |
|---|---|
| 100,000 files searchable after | ~0.4 s |
| a million files searchable after | ~6.6 s (152,000 files/s; it was 15 s before 0.9.26 lifted the engine's memory ceiling) |
| slowest search, a million files | ~7 ms (0.9.27; it was 35 ms) |
| memory after indexing a million files | ~140 MB (Activity Monitor's measure) |
| opening the app | ~0.2–0.3 s |

## For developers

Go core and daemon (pure Go, no cgo) + a SwiftUI shell that talks to the daemon over a Unix socket.

```bash
go build ./... && go test ./... && go vet ./... && gofmt -l .
go run ./cmd/laserdot index ~/Documents
go run ./cmd/laserdot search 'photos last week'
go run ./cmd/laserdotd &                 # the CLI then goes through the daemon
go run ./cmd/laserdot help-syntax        # the cheat sheet, from the filter registry
```

The app (macOS):

```bash
app/scripts/build-go.sh                  # laserdotd + CLI, and the app's registry (rerun after registry changes)
(cd app && swift build && LASERDOTD=$PWD/build/go/laserdotd swift test)
VERSION=0.9.30 app/scripts/bundle.sh     # → app/build/Laserdot.app
```

CI builds and tests on Linux and macOS, bundles the app, launches it, runs the updater against GitHub, and runs
the **self-test**: the real app types into its own ⌥Space window, checks what it shows (results, sections, hints,
commands, actions, Quick Look, Rename, Move to…, Compress, Tags, Undo, search history, dropping a folder, content
passages, choices and pills, leaving a choice out, the place menu, zip contents, More Like This, the home starts,
the unreadable list, the Keychain, Settings and, last, Erase All Data) in English and Arabic, measures its
launch time, and saves screenshots
(`app/Sources/Laserdot/SelfTest.swift`, judged by `app/scripts/check-selftest.py`).

A speed test on a Mac (`app/scripts/perf-mac.sh`) indexes 100,000 files (and again without bulk folder reading, to
compare), times searches and measures the daemon's memory. On CI's Mac, 100,000 files are searchable after about
1 s and a million after about 6.6 s (the weekly **perf-big** run); searches take a few milliseconds.

Releases publish themselves: when CI passes on `main` and the newest version in `CHANGELOG.md` has no release yet,
the **Release** workflow builds that exact commit, launch-tests it and publishes `Laserdot-<version>.dmg` with that
version's notes. (A `v*` tag or a run by hand still work.) A version marked `<!-- quiet -->` in CHANGELOG.md is
released without a What's New message. User-facing changes are released in batches, about weekly, unless a fix is
urgent. Documents-only commits skip the Mac jobs, and a job GitHub leaves without a machine retries by itself. The **perf-big** workflow measures a million files weekly.

## Documents

Start with whichever sounds most interesting:

- [`docs/design-conversation.md`](docs/design-conversation.md) — the story: what the owner asked for, release by
  release, and what was decided and why.
- [`docs/lessons-learned.md`](docs/lessons-learned.md) — the things building it taught us, mostly the hard way.
- [`docs/how-it-works.md`](docs/how-it-works.md) — one page: how Laserdot works now.
- [`docs/business-rules.md`](docs/business-rules.md) — what Laserdot promises and will never do (privacy, files,
  duplicates, updates, reminders).
- [`docs/interface-rules.md`](docs/interface-rules.md) — how it looks and behaves, and how it copies Spotlight.
- [`docs/design-ideas.md`](docs/design-ideas.md) — the ideas behind it, and the ones still waiting their turn.
- [`docs/spotlight-successor-spec.md`](docs/spotlight-successor-spec.md) — the spec, the source of truth for behavior.
- [`issues/001-spotlight-successor.md`](issues/001-spotlight-successor.md) — the epic and the Design Decision Log.
- [`issues/002-go-port.md`](issues/002-go-port.md) — the Go port: mapping from Rust, progress, as-built notes.
- [`CHANGELOG.md`](CHANGELOG.md) — what's new in each version, in plain words.

The original Rust implementation is `jamalxcode/laserdot`.
