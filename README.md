# Laserdot

Private, media-aware search for your Mac — a Spotlight successor that cannot phone home.

- **Finds things the way you say them:** `jpg`, `photos last week`, `big videos`, `pdf yesterday`, `invoice march`,
  `4k hdr`. Arabic too: `صور الاسبوع الماضي`, `فاتورة ٢٠٢٤`.
- **Knows your media:** resolution, codecs, HDR, frame rate, audio channels, track languages, subtitles, camera
  EXIF, music tags — searchable without opening a file (`res:4k codec:hevc`, `lang:ar channels:5.1`).
- **Fast and live:** its own index; results appear as you type (a few milliseconds even over a million files) and
  stay current as files change — a deleted file never shows.
- **Drives that remember:** an external drive keeps its own index, so it is searchable on any Mac with Laserdot.
- **Duplicates by quality:** finds identical files, similar pictures and re-encoded videos and keeps the best copy.
  It never deletes: files are moved to the Trash or a folder you choose, after a preview.
- **Private by construction:** no AI, no telemetry, no accounts. The search engine is blocked from the network by
  macOS itself; the one exception is the updater, which only talks to GitHub to fetch new versions.

## Install

1. Download the latest **Laserdot-x.y.z.dmg** from [Releases](https://github.com/jamalxcode/laserdot-go/releases/latest).
2. Open it and drag **Laserdot** to **Applications**.
3. Open Laserdot. macOS asks about Desktop, Documents, Downloads, iCloud Drive and any connected drive, all at once
   — click **Allow** on each. Laserdot then indexes your apps and your home folder by itself (skipping `~/Library`,
   except iCloud Drive). If it finds an older copy of Laserdot on your Mac, it offers to move it to the Trash.

Updates install themselves: Laserdot checks once a day, downloads a new version in the background and installs it
when you quit (or now, from **Check for Updates…** in the menu-bar icon's menu). No account or token is needed. Each
update is signed on your Mac with a key Laserdot makes there, so macOS keeps your "Allow" answers. Not happy with
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
| Tab / ⌥1–⌥9 | complete a filter / press a suggestion chip |
| Esc | close actions, the cheat sheet, then the window |

Results can also be shown as **columns** like Finder's list (click Name, Date Modified, Size, Kind or Date Created
to sort; again to reverse) or as **thumbnails** — the view switch at the top, or ⌘J. A file deleted in Finder disappears from the results right away;
**⌘R** (the circular arrow) looks at every indexed folder again.

The preview pane on the right shows a large thumbnail, plays videos and songs, pages through PDFs, and for a
document found by its words, the passage where they are. Drop a folder on the window to search inside it.

**Library ▸ Storage** (⇧⌘S) shows what fills your disk, folder by folder, and the largest files. The sort menu
(arrows next to the pin) orders results by name, size or date. `laserdot://search?q=…` links open a search (use them
in Shortcuts with **Open URL**).

**Settings** (⌘,): the search shortcut, open at login, pausing background work on battery, automatic updates (and
**Go Back** to the version before the last update), **Folders** (what is indexed, folders that are never
indexed, and what Laserdot couldn't read, with how to fix it), **Content Search**, **Drives**, and **Privacy** (what is kept and its size, Clear History, **Erase All
Laserdot Data**). **Help** has the search syntax, the
welcome tour, What's New, and **Save Diagnostics** (a file for a bug report, with every path removed; it also shows
how long Laserdot took to start).

## Where things are kept

Everything stays on your Mac, in `~/Library/Application Support/laserdot/` (**Settings → General → Index → Show in
Finder**):

- `index.db` — names, sizes, dates, media details; `index.db.snapshot` — a copy for a fast start.
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
VERSION=0.9.4 app/scripts/bundle.sh      # → app/build/Laserdot.app
```

CI builds and tests on Linux and macOS, bundles the app, launches it, runs the updater against GitHub, and runs
the **self-test**: the real app types into its own ⌥Space window, checks what it shows (results, sections, hints,
commands, actions, Quick Look, Rename, Move to…, Compress, Tags, Undo, search history, dropping a folder, content
passages, the unreadable list, the Keychain, Settings and, last, Erase All Data) in English and Arabic, measures its
launch time, and saves screenshots
(`app/Sources/Laserdot/SelfTest.swift`, judged by `app/scripts/check-selftest.py`).

A speed test on a Mac (`app/scripts/perf-mac.sh`) indexes 100,000 files (and again without bulk folder reading, to
compare), times searches and measures the daemon's memory. On CI's Mac, 100,000 files are searchable after about
1.7 s; searches take 2–4 ms.

Releases: run the **Release** workflow with a version (or push a `v*` tag); it builds, launch-tests and publishes
`Laserdot-<version>.dmg`.

## Documents

- [`docs/spotlight-successor-spec.md`](docs/spotlight-successor-spec.md) — the spec, the source of truth for behavior.
- [`docs/design-conversation.md`](docs/design-conversation.md) — what the owner asked for in the app's first weeks,
  and what was decided and why.
- [`issues/001-spotlight-successor.md`](issues/001-spotlight-successor.md) — the epic and the Design Decision Log.
- [`issues/002-go-port.md`](issues/002-go-port.md) — the Go port: mapping from Rust, progress, as-built notes.

The original Rust implementation is `jamalxcode/laserdot`.
