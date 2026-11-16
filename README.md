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
- ⚡ **Fast and live.** Its own index; a million files are searchable about 10 seconds after a first look, searches
  take milliseconds, and a file you delete in Finder leaves the results right away.
- ⌨️ **Types like Spotlight.** The top result's name completes as you type, Esc clears then closes, your last search
  comes back selected — everything your fingers already know, plus Quick Look on Space and ⌘K actions.
- 💽 **Drives that remember.** An external drive keeps its own index, so it's searchable on any Mac, and can be
  encrypted with a passphrase that's never written down.
- 🧹 **Duplicates by quality.** Identical files, similar pictures and re-encoded videos; it recommends the best copy
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

Press **⌥Space** anywhere (change it in **Settings**), or use the Laserdot window.

| You type | You get |
|---|---|
| `invoice march` | names with both words, in any order (and documents containing them) |
| `jpg`, `avi`, `ppt` | that file type (`jpg` = .jpg, .jpeg, .jpe; `ppt` = all PowerPoint files) |
| `photos`, `videos`, `music`, `apps` | that kind of file |
| `today`, `yesterday`, `last week`, `this month` | files changed then |
| `big`, `huge`, `small` | files over 100 MB, over 1 GB, under 100 KB |
| `4k`, `1080p`, `hdr`, `screenshots` | videos by resolution or HDR, screenshots |
| `"avi"` | the word itself in names (quotes turn the clever words off) |
| `res:4k codec:hevc -sample` | precise filters (**⌘/** lists them all) |
| `> dup` | commands: Find Duplicates, Settings, Eject a drive… |

The hint under the box says what a clever word stands for, e.g. *“last week means since:7d”*.

Typing works like Spotlight: the top result's name completes as you type (the rest shown selected; → or Tab takes
it, Return opens it), and when you reopen the window your last search is there, all selected. With the box empty,
Laserdot suggests a search from your own files (e.g. *“812 of your videos are 4K: res:4k”*) — click it to run it.

**Results** come like Spotlight's: a **Top Hit**, then Applications, Documents, Images, Videos, Folders… (five each,
**Show all** for more). Files you open often rise to the top. The empty box shows what you opened recently and new
downloads.

| Key | Does |
|---|---|
| ↑ ↓ | move through results (⇧↑ ⇧↓ or ⌘-click select several) |
| Return / ⌘1–⌘9 | open the selected / the numbered result |
| Space (after ↑↓ or a click), or Space twice | Quick Look: pictures large, videos and songs playing |
| ⌘K | actions: Open with…, Show in Finder, Copy file, path or link, Share, Search in This Folder, Tags…, Compress, Move to…, Open in Terminal, Rename, Move to Trash |
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

Results can also be shown as **columns** like Finder's list (Name, Date Modified, Size, Kind, Last Opened, Date
Created — click one to sort, again to reverse, drag the line before it to resize) or as **thumbnails** (the slider or
⌘+ ⌘− change their size) — the view switch at the top, or ⌘J. A file deleted in Finder disappears from the results right away;
**⌘R** (the circular arrow) looks at every indexed folder again.

The preview pane on the right shows a large thumbnail, plays videos and songs, pages through PDFs, and for a
document found by its words, the passage where they are. Drop a folder on the window to search inside it.

Long result lists stop at **Show More** rather than loading forever. Drag the window anywhere: it opens there next
time.

**Find Duplicates** (File menu, or `> dup`) finds identical copies, similar pictures and re-encoded videos and
recommends the best copy to keep. It shows each step with a bar, how many files are done and the time so far; it can
be stopped at any point (**Stop and Show What's Found**) and picks up where it left off (**Continue Scan**), saving
what it has read every 30 seconds. Cleaning up is one button — **Move N Duplicates to Trash (size)…** — then a
confirmation; nothing is ever deleted, and Laserdot keeps a running total of the space it has helped clear.

**Library ▸ Storage** (⇧⌘S) shows what fills your disk, folder by folder, and the largest files. The sort menu
(arrows next to the pin) orders results by name, size or date. `laserdot://search?q=…` links open a search (use them
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
- `content.db` — the words inside documents (your whole home folder unless you change **Settings → Content Search**).
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
Keychain**: then macOS's Keychain keeps it and the drive unlocks by itself). When the drive comes back, only
new or changed files are read again. A **read-only** drive cannot hold its index, so it is kept on this Mac in
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
- **Every update proves where it came from**, using GitHub's keyless build attestation; there's no signing key
  anywhere to steal.
- **Every decision is written down.** What the owner asked for, what was decided and why, and what went wrong along
  the way — see the documents below. Most features started as a screenshot from the owner's Mac mini.

Some numbers from GitHub's Mac (which is slower than yours):

| | |
|---|---|
| 100,000 files searchable after | ~1 s |
| a million files searchable after | ~10.6 s (94,000 files/s) |
| slowest search, a million files | 38 ms |
| memory after indexing a million files | 65 MB (Activity Monitor's measure) |
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
VERSION=0.9.17 app/scripts/bundle.sh     # → app/build/Laserdot.app
```

CI builds and tests on Linux and macOS, bundles the app, launches it, runs the updater against GitHub, and runs
the **self-test**: the real app types into its own ⌥Space window, checks what it shows (results, sections, hints,
commands, actions, Quick Look, Rename, Move to…, Compress, Tags, Undo, search history, dropping a folder, content
passages, the unreadable list, the Keychain, Settings and, last, Erase All Data) in English and Arabic, measures its
launch time, and saves screenshots
(`app/Sources/Laserdot/SelfTest.swift`, judged by `app/scripts/check-selftest.py`).

A speed test on a Mac (`app/scripts/perf-mac.sh`) indexes 100,000 files (and again without bulk folder reading, to
compare), times searches and measures the daemon's memory. On CI's Mac, 100,000 files are searchable after about
1 s and a million after about 10.6 s (the weekly **perf-big** run); searches take a few milliseconds.

Releases publish themselves: when CI passes on `main` and the newest version in `CHANGELOG.md` has no release yet,
the **Release** workflow builds that exact commit, launch-tests it and publishes `Laserdot-<version>.dmg` with that
version's notes. (A `v*` tag or a run by hand still work.) A version marked `<!-- quiet -->` in CHANGELOG.md is
released without a What's New message. The **perf-big** workflow measures a million files weekly.

## Documents

Start with whichever sounds most interesting:

- [`docs/design-conversation.md`](docs/design-conversation.md) — the story: what the owner asked for, release by
  release, and what was decided and why.
- [`docs/lessons-learned.md`](docs/lessons-learned.md) — twenty-one things building it taught us, mostly the hard way.
- [`docs/business-rules.md`](docs/business-rules.md) — what Laserdot promises and will never do (privacy, files,
  duplicates, updates, reminders).
- [`docs/interface-rules.md`](docs/interface-rules.md) — how it looks and behaves, and how it copies Spotlight.
- [`docs/design-ideas.md`](docs/design-ideas.md) — the ideas behind it, and the ones still waiting their turn.
- [`docs/spotlight-successor-spec.md`](docs/spotlight-successor-spec.md) — the spec, the source of truth for behavior.
- [`issues/001-spotlight-successor.md`](issues/001-spotlight-successor.md) — the epic and the Design Decision Log.
- [`issues/002-go-port.md`](issues/002-go-port.md) — the Go port: mapping from Rust, progress, as-built notes.
- [`CHANGELOG.md`](CHANGELOG.md) — what's new in each version, in plain words.

The original Rust implementation is `jamalxcode/laserdot`.
