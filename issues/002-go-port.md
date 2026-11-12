# Issue 002: Port laserdot to Go

**Labels:** `ready-for-agent`
**Status:** open
**Created:** 2026-11-10
**Tracker:** local-markdown

---

## Decision

The owner decided to reimplement laserdot in Go, based on the spec (`docs/spotlight-successor-spec.md`). The Rust
implementation in `jamalxcode/laserdot` (M0–M8 plus the memory-mapped snapshot) is the reference and stays as is.

Choices made by the owner (2026-11-10):

- **New repository** (`jamalxcode/laserdot-go`), not a folder in the Rust repo.
- **The SwiftUI app stays**, talking to the Go daemon over the same JSON-lines Unix-socket protocol.
- **Pure-Go SQLite** (`modernc.org/sqlite`): no cgo. FTS5 works (contentless tables with deletes included).
  **Consequence:** no SQLCipher, so encrypted drive indexes (§9.8) need another design, to be settled when drives
  are ported (and recorded as a spec change).
- **Order: daemon and search first**, then the rest.

## How the Rust mechanisms map to Go

| Spec / Rust | Go |
|---|---|
| Rust core + crates (§13.2, §13.9) | Go module with `internal/*` packages and `cmd/laserdot`, `cmd/laserdotd` |
| `regex` crate, `regex-syntax` HIR (§2.2) | `regexp` (RE2: linear time, no backtracking), trees built with `regexp/syntax` |
| `cargo-deny` bans networking crates (§7.1) | `internal/netcheck`: fails if `net/http`, `crypto/tls`, `net/rpc`, `net/smtp` or a third-party HTTP client is in the dependency graph, or our code dials anything but a Unix socket. `net` itself is needed for the socket (and is imported, inertly, by `github.com/google/uuid` via modernc's libc to list interfaces) |
| `sandbox_init` / seccomp (§7.1) | Linux: seccomp filter with TSYNC via `golang.org/x/sys`. macOS without cgo: re-exec under the system sandbox profile |
| rusqlite + `bundled-sqlcipher` | `modernc.org/sqlite`; no encryption (see above) |
| UniFFI bindings (§13.2) | none: the app talks to the daemon over the socket only |
| `rkyv` snapshot (§2.1.1g) | to be designed (the Rust build used a custom mapped format) |
| roaring | `github.com/RoaringBitmap/roaring/v2` |
| `unicode-normalization` + `caseless` | `golang.org/x/text/unicode/norm` + `golang.org/x/text/cases` |

## Phases

- **G1 — daemon + search:** folding, registry and grammar, walk, SQLite store, in-memory store (trigrams, bitmaps,
  sort orders), name and file-filter search, CLI, daemon with socket protocol and OS sandbox, CI with the network
  check.
- **G2 — media:** Tier 1 parsers (MP4/MOV, Matroska/WebM, audio tags, EXIF) against the Rust golden corpus; media
  columns, facets, track groups, every media filter.
- **G3 — live updates:** watcher, update engine, rename/move tracking, file lifecycle, the randomized lifecycle test.
- **G4 — drives, duplicates, content search, guidance, snapshot, Tier 2**, then the Swift app pointed at the Go
  daemon.

## Design decision log (the app, 2026-11-10 → 11)

Decisions the owner made while using the app (the conversation and the reasons: `docs/design-conversation.md`).
Binding unless revisited deliberately; each is also in the spec.

- **Network:** only the Laserdot Updater helper may use the network, and only to reach github.com for releases
  (§7.4). The app shell is not OS-sandboxed (it could not hand protected files to Preview/VLC) and keeps the rule
  by having no network code, enforced in CI; `laserdotd` keeps its OS network block (§7.1).
- **Distribution:** the repository and its releases are public; only the owner can push or publish. Updates need no
  token or account: the version comes from the `releases/latest` web redirect (the API is a fallback, its anonymous
  limit is often used up), and a background download installs on quit.
- **Search like Spotlight:** type words and plain-language words stand for filters, explained on the hint line,
  turned off by quotes (§2.7); equally good matches rank files → folders → hidden, opened results first (§2.2,
  §6.2); a Top Hit then sections of five; apps and other packages are one result (§13.6).
- **Nothing to set up:** apps and the home folder are indexed without asking (`~/Library` skipped except iCloud
  Drive); no first-run prompt (§13.6a).
- **Confidentiality of drives:** a writable drive keeps its own index; a read-only drive's index on this Mac is
  deleted as soon as the drive is gone — at start, every minute, when a drive leaves, on quit (§9, v1.22). FSEvents ids
  are still never stored for drives; a returning drive is reconciled by a diff.
- **The app's screen is tested:** the self-test drives the real bundled app in CI (typing, Space, ⌘K, `>`,
  Settings) in English and Arabic and saves screenshots; a stale `laserdotd` is replaced by version; Arabic is laid
  out right-to-left explicitly on every window.
- **Interaction:** Space opens Quick Look once ↑/↓ or a click has moved into the results (while typing it types);
  ⌘K actions, ⌘1–⌘9, `>` commands; Apple's HIG (Settings window, Find, Help menu, accent selection).
- **The user stays in control (0.6.0):** every indexed folder is visible and removable, folders can be excluded
  for good, history can be cleared, drives are listed with where their index is; Save Diagnostics writes a file the
  user chooses to share, with every path removed. Releases carry plain-language notes (`CHANGELOG.md` →
  `WhatsNew.txt`), shown in the update prompt and after an update.
- **Mistakes can be undone (0.8.0):** file actions have Undo; an update can be undone (Go Back); all of Laserdot's
  data can be erased from Settings ▸ Privacy, which also shows what is kept. Drive passphrases are remembered only
  when the user ticks the box, and only in the login Keychain.
- **Left out by the owner:** an Apple Developer ID signature and notarization (needs a paid account).

## Progress

- **G1 started (2026-11-10):** repository, docs, CI. `internal/fold` ported with the Rust tests plus a fuzz test,
  which found two idempotence bugs the Rust tests never covered: x/text folds Cherokee small letters to capitals
  and back (now folded to a fixed point), and invalid UTF-8 in a file name stopped decomposition of the next
  letter (now replaced with U+FFFD first, as Rust's lossy conversion did).
- **Query language ported (2026-11-10):** registry (generated from the Rust table), §2.7 parser and regex
  compilation, with the Rust tests and fuzz tests for both. Regexes are built as `regexp/syntax` trees and
  serialized by the library (Go compiles regexes from text only). `re:` errors are positioned by locating the
  offending sub-expression that Go reports. **Behavior note:** typo alternatives now apply to words of 4–40
  characters (§2.2.3 sets only the minimum). Fuzzing showed a 4,000-character word expands into a regex too
  large to build quickly; the Rust build hit its regex size limit there and returned an error instead.

- **G1 complete (2026-11-10):** walk (`internal/scan`: ignore rules, opaque bundles, no device crossing, hard
  links listed once, birth times via `statx` on Linux), kind classification, the in-memory store (pre-order ids so
  `in:` is a range, roaring bitmaps, byte-trigram index, pre-sorted size/date orders), cost-ordered execution with
  the trigram prefilter, relevance ranking (top-k selection for a page), the modernc SQLite store (integrity check,
  quarantine, side file of roots), `internal/core` with the Rust Seam 1 tests that need no media parsing, the IPC
  protocol (the Rust wire format: protocol 5, `op`-tagged JSON lines, snake_case results), `laserdotd` and the
  `laserdot` CLI (routes through the daemon when it answers). 1M synthetic entries: build 2.3 s, ~164 MiB; queries
  0.03–12 ms on 4 cores (`go test ./internal/index -bench Search1M -run X`).
  - **Sandbox:** Linux seccomp as in Rust. macOS re-executes the process under `/usr/bin/sandbox-exec -p <profile>`
    (same Seatbelt profile; `sandbox_init` needs cgo). The process checks that the restriction holds with a
    probe that connects an IPv4 UDP socket to the loopback discard port (connecting UDP sends nothing). The privacy
    gate (`internal/sandbox/deny_test.go`: TCP and UDP blocked, IPC folder allowed, children inherit) and the
    daemon's `status.network_denied` test run on both CI OSes. `internal/netcheck` now also allows raw
    `unix.Socket`/`unix.Connect` only inside `internal/sandbox`, and exempts the privacy gate's control listener by
    name.
  - **Not yet:** media filters and track groups answer "not executable yet" (G2); re-indexing re-walks a whole
    root and `refresh` re-walks every root (G3 diffs in place and tracks renames); Finder tags; the daemon serves
    ping, status, search, index, forget, refresh, roots, stats and shutdown, and answers other ops with error code
    `unsupported`; the cheat sheet is English only until the Arabic descriptions (`i18n.rs`) are ported.
- **G3 platform layer (2026-11-10):** `internal/scan` gains the shared `Change{Path, Recursive}` type; Finder tags
  (hand-written, bounds-checked binary-plist reader for `com.apple.metadata:_kMDItemUserTags`, fuzz-tested;
  `user.xdg.tags` comma list as the Linux fallback) wired into `Walk`, so `Entry.Tags` is filled; `IsNetworkVolume`
  (statfs: smbfs/nfs/afpfs/webdav/cifs on macOS; NFS/SMB/CIFS/SMB2/AFS/Coda/9p magics and network FUSE subtypes
  such as `fuse.sshfs` from `/proc/self/mounts` on Linux); `Volume`, `VolumeAt`, `ListVolumes`, `Estimate`; `Trash`.
  New `internal/watch` ports laserdot-watch (`Options`, `WatchRoot`, `Backend`, `Batch`, `Watcher` with
  `NextBatch`/`Replays`/`Close`, `CurrentEventID`) with the same coalescing (same-folder merge, recursive changes
  swallow those beneath them, highest event id wins): inotify through `golang.org/x/sys/unix`, FSEvents without
  cgo through `github.com/ebitengine/purego` (no network packages; netcheck green) on a locked OS thread running a
  CFRunLoop, polling for network volumes. Rust watch/scan tests ported, plus lifecycle (create/modify/rename/
  delete/new and renamed folders) tests for both native backends.
  - **Deviations:** `WatchRoot.Since`/`Batch.EventID` use 0 for "none" instead of `Option<u64>`. Polling diffs
    folder-listing snapshots and reports only folders that changed (Rust re-sent a recursive root change every
    interval); the first poll still reports each root recursively. FSEvents uses `FileEvents` (file-level) and
    maps item events to their folder, so `Change` keeps meaning "this folder changed". inotify also skips other
    volumes, ignored and opaque-child folders when adding watches, re-watches every root after a queue overflow,
    and drops the watches of folders moved out of the tree. The event queue is capped at 1M events, beyond which
    it collapses to a recursive change per root. macOS `Trash` moves into `~/.Trash` (or `<volume>/.Trashes/<uid>`)
    with Finder-style "name 2.ext" names and `RENAME_EXCL` (Rust used `NSFileManager trashItemAtURL:`, which needs
    Objective-C): **no "Put Back"** metadata. Freedesktop `DeletionDate` is local time (the freedesktop spec) where
    Rust wrote UTC.
  - **Gaps:** nothing consumes `internal/watch` yet (update engine, `core` and daemon wiring are G3 work for
    `internal/core`/`internal/index`); FSEvents and Finder-tag code is exercised only on the macOS CI runner.
- **G2 media parsers (2026-11-10):** `internal/media` now has the Tier 1 parsers, ported from `laserdot-media`:
  `Probe(path, kind, foldedExt, fileSize) (ProbeStatus, *Record)` with the Rust `parser()` dispatch table
  (`WantsProbe`) and `Record.Finish`; ISO BMFF (`moov` anywhere, rotation matrix, HDR10/HLG/Dolby Vision, fps
  and VFR from `stts`, avcC/hvcC/av1C/vpcC profiles and `pix_fmt`, E-AC-3 Atmos, `udta`/`ilst` tags), Matroska
  (EBML, Info, Tracks, Tags, SeekHead to elements after the clusters), images (dimensions by content for JPEG,
  PNG, GIF, WebP, HEIF/AVIF, TIFF, BMP, PSD, DDS, QOI, EXR, Radiance HDR, JPEG XL, TGA; EXIF from the first MiB),
  and `NormalizeLang`/`LangAliases`. The Rust golden corpus is in `internal/media/testdata` (with `dupes/`);
  `seam2_test.go` ports every Seam 2 test with the same expectations, including `corrupt_inputs_never_panic`
  (run through the parsers directly) and the ffprobe cross-check (skipped when ffprobe is absent; it passes
  against ffprobe 6.1). Fuzz tests: `FuzzProbeISO`, `FuzzProbeMatroska`, `FuzzProbeAudio`, `FuzzProbeImage`.
  The duplicate-finder fingerprints (`dupe.rs`) are ported with their tests: quick/full exact hash, Hamming,
  `IsFlat`, DCT pHash, `Downscale`, `Orient`, `ImageGray` (EXIF orientation and the embedded thumbnail, stdlib
  JPEG/PNG/GIF and `golang.org/x/image` WebP/TIFF/BMP), `VideoFrames` via ffmpeg and `VideoBackend`.
  - **Hand-written instead of crates:** audio (Rust: `lofty`) reads MP3 (ID3v2.2–2.4 incl. unsynchronisation,
    ID3v1, APEv2, Xing/Info/VBRI or CBR estimate), FLAC (STREAMINFO, Vorbis comments), Ogg Opus/Vorbis/Speex
    (granule-based duration with Opus pre-skip), WAV (fmt, LIST/INFO, id3 chunk), AIFF/AIFC (COMM, ID3, NAME/AUTH),
    APE, WavPack, ADTS AAC and Musepack SV7/SV8 headers, following lofty's rules for the primary tag, TCON genre
    resolution and duration/bitrate formulas. EXIF (Rust: `kamadak-exif`) and image dimensions (Rust:
    `imagesize`) are hand-written too. No new dependency besides `golang.org/x/image` (pure Go, no network).
  - **Deviations:** the audio type is sniffed from content (lofty trusts the extension, so an Opus stream named
    `.ogg` failed there); an MP4 audio file under an audio extension goes through the ISO parser. Exact hashes are
    SHA-256 instead of BLAKE3 (they are only cached in this Mac's index, never in a drive's, so nothing needs to
    agree with the Rust build). `Probe` also recovers from a panic as defence in depth (reported as Failed); the
    tests and fuzzers call the parsers without it. JPEG decoding for pHash uses `image/jpeg` at full size, then
    area-averages (Rust decoded at a reduced DCT scale); distances on the corpus stay within the Rust thresholds.
  - **Gaps:** HEIC/AVIF/camera-RAW perceptual hashes (Rust used ImageIO on macOS) and AVFoundation video frames are
    not available without cgo: `HashableImage` covers only the portable types, and video frames always need an
    ffmpeg (`$LASERDOT_FFMPEG`, beside the executable, `/opt/homebrew/bin`, `/usr/local/bin`, `/usr/bin`).
    ID3v2 frames that are compressed or encrypted are skipped. Media columns, facets and the media filters
    themselves are still to do (rest of G2).
- **App (2026-11-10):** the SwiftUI app moved to `app/` and talks to the Go `laserdotd` directly; the UniFFI shim
  and bindings are gone. `LaserdotKit` is pure Swift: `DaemonClient` (POSIX `AF_UNIX` socket, one JSON line per
  request and response, Codable `DaemonRequest` covering every op of the Rust `Request` enum, `RemoteError` with
  span, suggestion and code), `Engine` (the old FFI surface: shared connection, reconnect once, starts the bundled
  `laserdotd` when nothing answers, remembers ops answered `unsupported`), `Paths` (default db path and the
  `ipc.SocketFor` rule, FNV-1a fallback included) and `Sandbox` (the app applies the same Seatbelt profile to
  itself through `sandbox_init`, looked up with `dlsym`). The Drives and Content menus are hidden and the
  Duplicates item disabled when the daemon answers `unsupported`. **Mechanism change:** hints, completions,
  sections, the filter list and the cheat sheet came from the linked Rust core over FFI; now they are daemon ops
  (`hint`, `section_at`, `completions`, `filters`, `cheat_sheet`), called per keystroke on a guidance connection
  of its own (and off the main thread) so typing never waits behind an index or a scan. While the daemon is not
  running yet (or lacks those ops) `Guidance.swift` answers instead: the Rust `assist` logic ported to Swift over
  `Registry.generated.swift`, which `app/scripts/registry` exports from the Go registry (CI fails if it is stale),
  with the Arabic descriptions (a Swift test requires one per filter). `app/scripts/build-go.sh` builds `laserdotd`
  and `laserdot` with `CGO_ENABLED=0` (host or `universal` via lipo); `bundle.sh` puts `laserdotd` in
  `Contents/MacOS`. The CI `app` job (macos-latest) builds, runs the Swift tests against the Go daemon
  (status `network_denied`, search, positioned errors, drives, dupes, cleanup dry run, content, coach, counts;
  ops answering `unsupported` are skipped) and uploads `Laserdot-unsigned.zip`.
- **G2–G4 integration (2026-11-10):**
  - **Index:** the store is mutable as in Rust (append / kill / in-place rename-move, `dead` and `moved` overlay,
    `in:` walks up for unstable ids, compaction past the threshold); the media index (columns per media row,
    facets, flags, every media filter, track groups, summaries, duplicate quality); sibling pairing (Live Photos,
    sidecars, screenshots). Pre-sorted orders merge appended ids. Probing during bulk index (chunked, resumable,
    probe reuse by size + mtime, files modified in the last 10 s deferred). The update engine is a direct port
    (inode pairing, ext-change probe reuse, followup rescans for reused inodes). The randomized lifecycle test
    (12 seeds × 25 rounds, every other round on a mapped snapshot) and all lifecycle stories pass.
  - **Core:** `ApplyChanges` routes changes to drive indexes; `Refresh` covers drives; `core.Watch` replays
    FSEvents history (or refreshes), records event ids, restarts on root changes and probes files once stable.
    Content search (FTS5 contentless table in `content.db`; PDF text via the pure-Go `github.com/ledongthuc/pdf`,
    recovered on panic), coaching and live counts, drives (portable, resumable, read-only drives indexed on this
    Mac, merged ranking across indexes), the duplicate finder and cleanup, the snapshot.
  - **Behavior change: encrypted drive indexes** (spec §9.8 updated, v1.15): a sealed AES-256-GCM container keyed
    with scrypt replaces SQLCipher; the working copy is decrypted into this Mac's data folder while attached.
  - **Snapshot:** one checksummed (CRC32C) file of 8-byte-aligned sections mapped `MAP_PRIVATE` read-write, so
    live updates write copy-on-write pages; bitmaps are roaring `FromBuffer` views. 1M entries: written in
    0.29 s, loaded in 36 ms.
  - **Daemon/CLI:** every protocol-5 op with the Rust wire shapes, plus `hint`, `section_at`, `completions`,
    `filters` and `cheat_sheet` (the Rust app computed these in-process through FFI); content and mount loops; a
    checkpoint every minute. CLI: `watch`, `drives`, `dupes`, `content`, coaching for empty results,
    `help-syntax ar`.
  - **i18n:** every filter has an Arabic description (enforced by a test); hints, completions and the cheat
    sheet in both languages.
- **Tier 2 (media side) (2026-11-10):** `internal/media/tier2.go` ports `tier2.rs` (§3.1.1): `Tool`,
  `Tier2Available`, `Tier2Probe`/`FromFFprobe` (AVI, MPEG-TS, ASF, FLV, MPEG-PS and files Tier 1 failed on),
  `HDR10Plus`/`HasHDR10Plus`, `DetectVFR`/`IsVFR`/`ParsePTS`, `MeasureLoudness`/`ParseLoudnorm` with the
  `Loudness` struct and `GainTo`. Each step is an ffprobe/ffmpeg subprocess (os/exec, stdin null, both pipes
  drained, killed after 30 s / 600 s for loudness, `-protocol_whitelist file` and absolute `file:` inputs, the
  last stderr line as the error). New `HDRHDR10Plus = 8` flag (the HDR byte is already a varint, so the record
  encoding is unchanged). The duplicate finder's ffmpeg lookup now goes through `Tool` (adds "next to
  `$LASERDOT_FFPROBE`", as in Rust). Corpus in `testdata/tier2` (Rust `generate.sh`); `tests/tier2.rs` ported
  with the same expectations (skipped without ffprobe/ffmpeg), and the same checks run on committed canned
  outputs (`testdata/tier2/canned.sh`: ffprobe JSON, packet timestamps, frames, loudnorm stderr) so CI covers the
  parsing without the tools; `FuzzFromFFprobe`.
  - **Deviations:** the Rust `loudness()` function is `MeasureLoudness` (Go cannot have a function and a type both
    named `Loudness`). `vfr()`'s `None` is `ErrVFRUnknown` from `DetectVFR` (`IsVFR` returns `(vfr, ok)`).
    Strings from ffprobe are clipped to the record's 4 KiB string limit so every record encodes. Case-insensitive
    tag keys are tried in sorted order (serde_json's map order). Nothing schedules Tier 2 yet (idle-time queue,
    on-demand loudness and storing the results are core/daemon work).
- **Tier 2 wired in (2026-11-10):** `core.DeepProbe` runs at idle in the daemon (20 files every 2 minutes) and on
  demand (`laserdot deep`, op `deep_probe`): a full ffprobe read for video/audio files Tier 1 could not read, HDR10+
  detection for HDR10 video, VFR from packet times for Matroska/AVI/TS/MPEG-PS/FLV. Each file version is deep-probed
  once (a `tier2` table in this Mac's index; drives are not deep-probed). Results replace the entry's media record in
  SQLite and in memory; `hdr:hdr10+` now matches. Loudness is on request only (`laserdot loudness FILE`, op
  `loudness`). Without ffprobe, Tier 2 reports itself unavailable and nothing runs.
- **Dock app, menu bar and updates (2026-11-11):** the app is now a Dock app (main window, app menus) and keeps
  the menu-bar icon and the ⌥Space overlay; it starts `laserdotd` and stops it on quit, offers to move itself to
  Applications, and offers a first index. **Behavior change (spec §7.4, v1.16):** the owner granted an exception
  to "no network" for updates only — the bundled `Laserdot Updater` helper checks GitHub on request and once a day
  (private releases, token in the Keychain), downloads the newest `Laserdot-<version>.dmg`, and the network-blocked
  app checks and installs it. `bundle.sh` adds the icon, the helper, the version and an ad-hoc signature;
  `make-dmg.sh` and the `Release` workflow publish a disk image for every `v*` tag. Not yet: Developer ID signing
  and notarization (needs the owner's certificates), so the first launch needs right-click → Open.
- **App shell unsandboxed (2026-11-11, owner's decision, spec v1.17):** opening a result failed with "Laserdot does
  not have permission to open …" for files in protected folders, because macOS checks a sandboxed app before handing
  a file to Preview or VLC. The app no longer applies the Seatbelt profile to itself; it keeps the no-network rule
  by its code (CI rejects network APIs outside the update helper). `laserdotd` still denies itself the network
  (on macOS it now re-executes under the profile itself, the path the daemon tests cover). Full Disk Access is
  optional (menu item; offered when indexing a protected folder fails).
- **Updates without a token (0.2.3).** The owner rejected the token prompt and allowed the code to be public, as long as nobody else can change it. The repository is now public: GitHub lets only the owner push or publish releases, and outside pull requests need the owner's merge (CI runs them read-only, without secrets). The updater reads `releases/latest` and downloads the `.dmg` without credentials, following redirects only to GitHub hosts. 0.2.2 and earlier still ask for a token, so those copies need one manual install of 0.2.3.
- **Type words and dialogs (0.2.4).** The owner asked for Spotlight's behavior: `jpg`, `avi`, `photos`, `videos` mean the file type (spec §2.7 type words). Search-as-you-type was already on. App dialogs now come to the front above other windows (`FrontModal.swift`): a dialog hidden behind another app made every Laserdot menu item look disabled.
- **Spotlight-like order and a stale daemon (0.2.5).** The owner's `png` showed folders named P…: the app had kept the results of the first keystroke while talking to a laserdotd left over from an older version (a force quit skips the app's shutdown, and the app adopted any running daemon). Now: (1) laserdotd reports the app's version (stamped by `build-go.sh` with `-X`), and the app replaces a running daemon of another version, waiting for it to exit; (2) equally good matches order files → folders → hidden (spec §2.2), and filter-only queries do the same within newest-first (they now rank all hits rather than walking the pre-sorted modified order: ext:pdf over 1M files 1.2 ms, kind:video fsize:>500mb 3.1 ms); (3) the app fades old results and says "Searching…" until the current query answers. `TestRelevanceOrder` (ported from Rust) now expects `.cache/invoice.tmp` after the visible prefix match.
- **Stale result rows (0.2.6).** The owner's `jpeg` and `png` showed the right count but the rows of the first keystroke (`j`, `p`). The result list gave each row two identities (`ForEach` by path and `.id(index)`), which lets SwiftUI's lazy stack keep old rows. Rows now have one identity (the path), and the list is rebuilt on every answer (`resultsID`). Type words gained format families (`ppt` = ppt,pptx,pps,ppsx; `powerpoint`, `excel`…).
- **Human Interface Guidelines pass (0.2.7).** The owner asked for Apple's standards. Added: a Settings window (⌘,; General: search shortcut, Open at Login via `SMAppService`, automatic update checks, version, network and Full Disk Access status; Content Search: folders with the standard +/− buttons), Edit ▸ Find (⌘F) to focus the search field, a Help menu item for the search syntax instead of the empty help-book item, and Settings… in the menu-bar menu. The hot-key submenu and Full Disk Access menu items moved into Settings. The main window uses standard window chrome (the rounded material card is the overlay's only) and a single status bar instead of repeating the network line; the selected row is an accent fill with white text, grey when the window is not key; hidden shortcut buttons are hidden from VoiceOver; the Duplicates window remembers its frame and has a minimum size; the delete-index alert uses a destructive verb button and Cancel (no ellipsis on alert buttons).
- **Updates without the API (0.2.8).** The owner's update check failed with HTTP 403: GitHub's anonymous API limit (60 an hour per address) was used up on their network. The helper now reads the version from the `releases/latest` web redirect and downloads the `.dmg` by its fixed name, with the API only as a fallback; a 403/429 is reported as "GitHub is limiting requests". CI runs the bundled helper against GitHub with a fake 0.0.1 app and checks it downloads a valid disk image. The helper got its icon (its Resources folder was never created).
- **The overlay's query field (0.2.9).** In the ⌥Space overlay (a non-activating `NSPanel`), SwiftUI's `TextField` passed on only the first keystroke: `jpg` showed the hint and results of `j` (the main window was fine). The query field is now an AppKit `NSTextField` (`SearchField.swift`) whose delegate reports every change and maps the field editor's commands (moveUp/moveDown, insertTab, insertNewline, cancelOperation) to the model; the overlay focuses it each time it opens.
- **Apps (0.3.0).** `claude.app` found nothing: Applications was never indexed, and an app would have been thousands of entries. Packages (`.app`, `.framework`, `.bundle`, Photos/Music libraries…) are now opaque in `scan` (`OpaqueExts`), the update engine drops contents indexed before, the type word `apps` means `kind:app`, and the app indexes the three Applications folders once.
- **The owner's critique, items 1–4 and 6–10 (0.4.0).** (1) Home folder and apps indexed by default, ~/Library skipped except iCloud Drive, no first-run prompt; indexing on its own connection and queue. (2) Open history ranks opened results first (spec §6.2). (3) A preview pane (Quick Look thumbnail, kind, size, date, media, path, Open/Show in Finder), thumbnails in rows for pictures and videos, ⌘Y Quick Look. (4) Results in sections: Top Hit, then Applications, Documents, Images… in the order of each kind's best result. (6, 9) `SelfTest.swift`: with `LASERDOT_SELFTEST`, the bundled app types into its own overlay with key events, reads the screen through the accessibility tree, saves screenshots and writes a report that `app/scripts/check-selftest.py` judges, in English and in Arabic (right-to-left), in CI. (7) Background-downloaded updates install on quit without a prompt. (8) ⌘1–⌘9 open a result (shown on the rows), ⌘Y Quick Look. (10) Chips under the hint: completions for the word being typed, or the kinds a mixed result list contains (one click adds `kind:`). (5, a Developer ID signature) was left out by the owner.
- **The owner's UI critique, all 15 items (0.5.0).** Short paths (`~ › Documents › AI Code`) with the full path on hover; the hint only when it says something about what was typed (`isGenericHint`); a lock icon instead of the network sentence; the empty box shows Recently Opened (`recent_opens` op) and Recent Downloads, and pinned searches as chips (no code-like examples); the overlay is a slim bar until there is something to show (top edge fixed); plain-language words (spec §2.7); matching letters in bold; five results per section with "Show all N"; ⌘K actions (open, open with, Quick Look, Show in Finder, copy file/path, share, rename, Trash) for one or several results; a footer listing the keys that work now; friendly dates ("Today at 9:04", "3 days ago"); the preview plays video and audio (AVKit) and shows PDF pages (PDFKit); `>` lists commands (duplicates, index, settings, updates, eject, quit); ⇧↑/↓ and ⌘/⇧-click select several; first-time indexing reports progress (`status.indexing`) in the footer and status bar. The self-test covers the slim start, plain-language dates, commands, actions and the empty box in both languages.
- **Space for Quick Look (0.5.1).** As in Finder: once ↑/↓ or a click has moved into the results, Space opens Quick Look for the selected result (pictures large, videos and songs playing) with every result in the panel; Space again or Esc closes it. While typing, Space types. The field catches Space with a local key monitor (it arrives as text, not a command); the overlay no longer hides when Quick Look takes the keyboard and takes it back afterwards; the app delegate is the panel's controller. The self-test presses ↓ and posts a real Space event.
- **Off-drive indexes leave with the drive (0.5.2).** The owner asked that the index of a read-only drive, kept on this Mac, be deleted once the drive is gone, checked at start, on quit and periodically. `core.PurgeOffDriveIndexes` deletes `<data>/drives/<key>` folders whose volume is not mounted and not attached; laserdotd runs it at start, every minute, when a drive is detached and on exit; the CLI when it opens the index directly. Drives that hold their own index are unaffected.
- **The owner's second critique, items 1–14 (0.6.0).** Settings ▸ Folders (indexed folders, "Never Index" exclusions — `core.Exclude/Include`, `exclude`/`include`/`exclusions` ops), Clear History (`clear_opens`), open at login by default, "What's new" from `CHANGELOG.md` (bundled as `WhatsNew.txt`; in the update prompt, Help ▸ What's New, and a button after updating), Space twice previews, results load as you scroll and a single-kind answer is not capped, swapped-letter typos for three-letter words with a "similar names" note, contents searched in the whole home folder by default, side-by-side duplicates with thumbnails, Settings ▸ Drives, a welcome tour, Help ▸ Save Diagnostics (paths scrubbed), an `hdiutil attach` retry (the flaky CI test), and an accessibility-client check in CI: while the self-test holds the overlay, `app/scripts/ax-dump.swift` reads the app through the AX API — on GitHub's runner it is allowed, and it reads the result rows. Fixed on the way: Quick Look did not always close (close() then orderOut) and kept a stale controller (updateController before showing).
- **The third critique, items 1–12 (0.6.1, 0.7.0).** 0.6.1 fixed a crash: SwiftUI's `VideoPlayer` aborted (`getSuperclassMetadata` in `_AVKit_SwiftUI`) in the SwiftPM-built app; the pane uses AppKit's `AVPlayerView`, created only on Play — the self-test now previews a video and a song, which also exposed that the player took the keyboard from the query. 0.7.0: sort menu; Library ▸ Storage (`core.Storage`, `storage` op); bundled LGPL ffmpeg/ffprobe without network (`app/scripts/build-ffmpeg.sh`, cached in CI, protocols checked); resizable overlay; `PowerWatcher` → `background` op; content size and skip-code; `laserdot://` links; ⌘K Open in Terminal, Copy as Link, Compress, Move to…; ⌥1–⌥9 chips, ⌘→ show all (while browsing); a drawn menu-bar icon; `app/scripts/perf-mac.sh` (100,000 real files on GitHub's Mac: indexing ≥ 10,000 files/s (CI Macs vary: 20,000–28,000 measured), searches < 50 ms); the self-test covers sorting, multi-select, Move to Trash, links, Storage, Duplicates and the Settings tabs.

- **0.8.0 (2026-11-11), the fourth critique:** Undo for file actions, Settings ▸ Privacy with Erase All Laserdot
  Data, Go Back to the previous version, a tour page on macOS prompts (default indexing waits for it), content
  passages in the preview (`snippet` op), Search in This Folder and folder drop, Keychain-remembered drive
  passphrases. Indexing writes 64 rows per statement (100k files: write 751 → 405 ms). The big Swift files were
  split (SearchView → ResultRows, PreviewPane, TextHelpers; SearchModel → +Actions, +Grouping). The self-test now
  renames, moves, compresses and undoes each, checks the passage, Search in This Folder, battery pausing, the
  remembered window size, the Privacy page and the launch time; `perf-mac.sh` reports the daemon's peak memory.

- **0.9.0 (2026-11-11), the fifth critique:** the repository is locked to the owner (Repo admin `access-lock`:
  no collaborators, read-only workflow tokens, approval for outside pull requests, `main` protected from deletion
  and force pushes). Undo list kept across launches, ⌘K ▸ Tags…, ↑ for earlier searches, the "couldn't read" list
  (`unreadable` op), snippet time limit and cache, faster full indexing (no read-back: 1.31 → 0.98 s for 100k files
  on Linux; `TestIndexKeepsTheSameIndexAsReopening` proves the result is identical), a quicker launch and its time in
  Save Diagnostics; LaserdotApp.swift and SearchModel.swift split. The self-test waits for results instead of fixed
  pauses and adds tags, history, drop, the unreadable list, the Keychain and, last, Erase All Data.

- **0.9.1 (2026-11-12):** the owner saw macOS ask for different folders at different times. The app now asks for
  every protected place at once before laserdotd starts (first launch: on the tour's Start), then indexes by itself.
  The prompts also came back after updates because the app is signed ad hoc (macOS ties permissions to the exact
  build); `bundle.sh` signs with the repository's own key when its secrets exist (Repo admin task `signing-cert`
  creates it on GitHub; it needs the owner's go-ahead to store the secret).
