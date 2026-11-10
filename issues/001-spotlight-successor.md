# Issue 001: Spotlight Successor — Private, Media-Aware File Search for macOS

**Labels:** `ready-for-agent`
**Status:** open
**Created:** 2026-11-10
**Tracker:** local-markdown

---

## Problem Statement

macOS users cannot reliably find their own files. Spotlight's index silently omits files, skips hidden directories and `.git`, lags on new files, and offers almost no media awareness — you cannot ask it for "all 5.1 HEVC files with Arabic audio" or find a file by its resolution, channel layout, or HDR status. Existing replacements (Raycast, Alfred) either gate their best features behind subscriptions, depend on cloud AI, or make network connections the user can't verify. For users with large media libraries, multilingual (Arabic/English) filenames, and strict privacy requirements, there is no search tool that is simultaneously fast, media-aware, and structurally incapable of phoning home.

## Solution

A native macOS menu-bar app (⌥Space overlay) with its own index — independent of Spotlight — that reads the headers of every audio, video, and image file at index time with fast in-process Rust parsers (ffmpeg only as fallback). Resolution, codec, channels, language tracks, HDR, and music tags become first-class filterable columns. All searches execute as regex against the index, but users type plain words and wildcards; the engine generates the regex. A duplicate finder detects exact, near-duplicate, and re-encoded media deterministically and recommends keep-best cleanup. The app omits the network entitlement entirely, so the OS structurally blocks all outbound connections — no AI, no telemetry, no cloud, ever. Removable drives carry their own portable index, and the app is a focused file/media/disk tool: personal data sources (contacts, calendar, mail, messages, notes, photos, Safari) are deliberately out of scope. The shell is SwiftUI; the engine is a Rust core exposed to Swift.

## User Stories

### Search
1. As a Mac user, I want to type plain words like `invoice march` and find the right file, so that I don't have to remember exact filenames.
2. As a Mac user, I want `*` and `?` wildcards to work in my searches, so that I can find files whose names I only partly remember.
3. As a power user, I want an optional `re:` prefix for raw regex, so that I can express exact patterns when I need them.
4. As a Mac user, I want single-character typos and transposed letters to still match, so that my search succeeds despite mistakes.
5. As a Mac user, I want characters like `(` and `.` in my filenames to be treated literally, so that `report(2).pdf` finds the file without escaping.
6. As a Mac user, I want to filter by type, size, date, extension, and folder in the same query, so that I can narrow without a separate UI.
7. As a Mac user, I want to search inside text, code, and PDF files, so that I can find documents by their contents, not just their names.
8. As a Mac user, I want search results in under 50 ms, so that searching feels instant.
9. As a Mac user with Arabic filenames, I want diacritics-insensitive and Arabic-Indic-digit-aware matching, so that `٤` and `4` and `أ` and `ا` find the same files.
10. As a Mac user, I want my files indexed within seconds of creation or change, so that new downloads are findable immediately.
11. As a Mac user, I want files that were renamed or moved to stay in the index without re-probing, so that reorganizing folders doesn't cost indexing time.
12. As a Mac user, I want half-downloaded files excluded until they're complete, so that I never get results for broken files.

### Media intelligence
13. As a media collector, I want to filter by resolution preset (`res:4k`), so that I can find files by quality tier.
14. As a media collector, I want to filter by audio channel layout (`channels:5.1`, `atmos`), so that I can find my surround-sound library.
15. As a media collector, I want per-track language filters (`lang:ar`, `s.lang:en`) and track groups (`a(lang:ar channels:5.1)`), so that I can find files with an Arabic 5.1 track, not just Arabic somewhere and 5.1 somewhere.
16. As a media collector, I want HDR and Dolby Vision flags, so that I can separate HDR content from SDR.
17. As a media collector, I want lossless audio flagged (`lossless:true`), so that I can audit my music library quality.
18. As a music listener, I want to search by artist, album, and genre tags, so that my music is findable like a library, not a folder.
19. As a media collector, I want frame rate and bitrate visible and filterable, so that I can compare versions of the same content.
20. As a media collector, I want on-demand loudness analysis, so that I can normalize playback without analyzing everything upfront.
21. As a media collector, I want sidecar subtitle files shown attached to their videos, so that related files stay together.
22. As a Mac user, I want DRM'd or corrupt files still findable by name, so that nothing silently disappears from results.

### Duplicate finder
23. As a media collector with duplicates, I want exact copies, visually similar images, and re-encodes of the same video grouped together, so that duplicate sets are obvious even when names and resolutions differ.
24. As a media collector, I want each group ranked by quality (resolution, HDR, bits per pixel, audio channels) with a keep-best recommendation, so that I know which copy to keep.
25. As a Mac user, I want a dry-run preview before any deletion, so that cleanup never destroys anything unexpectedly.

### Actions and launcher
26. As a Mac user, I want to act on results directly (copy path, move, rename, reveal, drag out), so that finding and doing are one step.
27. As a Mac user, I want bulk multi-select operations, so that I can clean up dozens of files at once.
28. As a media collector, I want ffmpeg actions (extract audio, remux, transcode, thumbnails) on results, so that common conversions are one click.
29. As a Mac user, I want an inline Quick Look preview with the media metadata panel, so that I can verify a file without opening an app.
30. As a Mac user, I want app launching in the same overlay, so that one hotkey covers finding files and opening apps.
31. As a Mac user, I want results biased toward what I actually open, so that the app learns my habits.
32. As a Mac user, I want a web-search fallback offered when nothing matches locally, so that a miss doesn't dead-end — while the app itself still never connects to the network.

### Privacy
33. As a privacy-conscious user, I want the app to be structurally unable to make network connections, so that privacy is guaranteed by the OS, not by policy.
34. As a privacy-conscious user, I want zero AI, embeddings, or model downloads, so that nothing about my files leaves inference pathways.
35. As a privacy-conscious user, I want no telemetry or crash reporters, so that I can verify the app's behavior.
36. As a privacy-conscious user, I want a published audit page listing every path written and process spawned, so that I can verify claims independently.
37. As a privacy-conscious user, I want a local diagnostics export I control, so that bug reports contain only what I choose to share.
38. As a privacy-conscious user, I want preview caches in the app container, so that thumbnails of private files don't scatter across disk.

### Guidance
39. As a new user, I want live hints under the query box, so that I learn the syntax by using it.
40. As a new user, I want autocomplete for filter names and values, so that I never have to memorize them.
41. As a user with zero results, I want the app to explain why and offer one-click fixes, so that a miss teaches me something.
42. As a new user, I want clickable example queries, so that I see what's possible in ten seconds.
42a. As any user, I want a cheat sheet button next to the query box that lists every filter with values, live counts, and examples I can click to insert, so that I can build a query without memorizing syntax.
43. As a returning user, I want query history with pinning, so that my frequent searches are one keystroke away.

### Removable drives
44. As a user with external drives, I want the index stored on the drive itself, so that it travels with the drive and is instant on any Mac.
45. As a user with external drives, I want a prompt with size estimates before indexing, so that I control what gets written to my drive.
46. As a user with external drives, I want interrupted indexing to resume from a checkpoint, so that unplugging mid-scan doesn't lose work.
47. As a user with external drives, I want an optional encrypted drive index, so that a lost drive doesn't leak my filenames.

### Presentation and integration
48. As a Mac user, I want sortable, groupable results with per-type badges, so that I can scan large result sets quickly.
49. As a Mac user, I want saved searches like `res:4k lang:ar`, so that recurring needs are pinned one-click entries.
50. As a Mac user, I want `local:true` filtering and no silent iCloud downloads, so that offloaded files never cost me bandwidth by accident.
51. As a Mac user, I want VoiceOver and full keyboard access, so that the app is usable regardless of input method.
52. As an Arabic-speaking user, I want a fully localized RTL Arabic interface, so that the app respects my language.
53. As a Mac user on battery, I want indexing to pause when power is low, so that search never drains my laptop.
54. As a scripter, I want a CLI and URL scheme sharing the same index, so that I can automate searches from terminal, Shortcuts, and scripts.
55. As a Mac user, I want a first-run walkthrough with a clean uninstall, so that trying the app has no hidden residue.

## Implementation Decisions

- **Architecture:** SwiftUI menu-bar shell (~63 MiB target) + Rust core exposed to Swift via UniFFI (workspace layout and milestones: spec §13.9–13.10). The Rust core owns indexing, FSEvents, SQLite FTS5, the regex engine, and the ffprobe queue. A CLI front-end reuses the same core.
- **Distribution:** Direct download, signed and notarized, non-sandboxed. Not App Store — whole-disk indexing and the Full Disk Access UX require it. Network isolation comes from a self-applied sandbox profile (see Network isolation), not the App Sandbox.
- **Index store:** SQLite (WAL, memory-mapped reads) as durable store; in-memory folded-filename table with a trigram prefilter as the matching hot path; FTS5 for file contents only. Indexer is a separate Rust daemon and the single writer. Schema versioned with in-place migration; incompatible versions trigger rebuild with checkpoint resume. Index excluded from Time Machine.
- **Matching model:** every name/path query executes as regex (Rust `regex` crate) over the in-memory table, never via SQLite `REGEXP`. Each word compiles to its own regex and results are ANDed — no permuted regexes. Wildcard words match the whole name; plain words match a substring. Typo tolerance is bounded regex alternatives per word (4+ chars, fallback only, ranked lower) — no fuzzy engine, no embeddings. `re:` passes raw regex through. Regexes are built as `regex-syntax` trees, so escaping is correct by construction. Content search is FTS5 word matching (BM25), the one deliberate non-regex path. The input language is defined by the EBNF grammar in spec §2.7.
- **Media probing:** tiered (spec §3.1). Tier 0 listing via `getattrlistbulk`; Tier 1 in-process header parsers (`imagesize`, `nom-exif`, direct MP4 `moov` and MKV EBML parsing) reading ~64 KB per file; Tier 2 libavformat in a crash-isolated worker for deep analysis. Per-device I/O scheduling (SSD vs HDD). Compact zstd probe records instead of raw JSON. No exiftool; LGPL ffmpeg for Tier 2 and actions only.
- **Media query engine:** in-memory fixed-width media columns + roaring bitmap indexes; planner runs bitmaps → ranges → trigrams → regex → top-k; pre-sorted orders for size/date sorts; daemon writes a memory-mapped `rkyv` snapshot for instant cold start.
- **Guidance:** one filter registry in the Rust core drives the parser, hints, autocomplete, coaching, the cheat sheet button (⌘/), and `laserdot help syntax`.
- **File lifecycle:** rename/move tracked as single updates via inode tracking; deletions tombstoned and purged after 30 days; in-progress writes skipped until size is stable for 10 seconds; corruption detected via SQLite integrity check with checkpoint-resumed rebuild.
- **iCloud:** dataless placeholders indexed by name only, never probed, never force-downloaded; `local:true/false` filter.
- **Unicode:** NFC normalization at index and query time; diacritic folding (Latin + Arabic); Arabic-Indic digit folding; full Unicode case folding; RTL rendering in overlay.
- **Network isolation:** the omitted `com.apple.security.network.client` entitlement only works inside the App Sandbox, so instead every app process (daemon and shell) applies a `sandbox_init` profile denying `network*` at startup; bundled tools inherit it. `cargo-deny` bans networking crates; ffmpeg built without network protocols. No telemetry, AI, or third-party SDKs that call home. Updates are manual downloads (Sparkle is incompatible).
- **Thumbnails:** cache in app container, excluded from Time Machine and from the app's own index.
- **Removable drives:** per-drive SQLite index at `.laserdot/index.db` on the volume, schema-versioned, portable between Macs, resumable from checkpoints, optional SQLCipher encryption.
- **Performance budgets:** cold launch under 300 ms; first result under 50 ms warm / 200 ms cold; media-filter queries under 5 ms over 1M files; Tier 0 ≥100k files/s, Tier 1 ≥2,000 images/s and ≥300 videos/s on SSD; background QoS, paused below 20% battery and in Low Power Mode; indexer dirty memory ceiling 150 MiB.
- **Hotkey:** ⌥Space with first-run conflict detection (Raycast, Alfred, Spotlight) and alternatives offered.

## Testing Decisions

- **Philosophy:** test external behavior only — query in, results and actions out — never internal index mechanics. The seams chosen are the two public boundaries: the Rust core's query API (used identically by the SwiftUI shell and the CLI) and the ffprobe ingestion pipeline's output schema.
- **Seam 1 — Rust core query API:** every user story with a query is testable here without any UI: plain-word translation, wildcards, typo tolerance, escaping, filters, media columns, Unicode folding, zero-result coaching data. This is the highest seam and covers the majority of the spec.
- **Seam 2 — ffprobe ingestion:** golden corpus of tricky media files (rotated, HDR, Dolby Vision, DRM'd, VFR, multi-language audio, corrupt, offloaded placeholder) parsed through the real pipeline; assertions on the parsed schema, not on ffprobe's JSON shape.
- **Regex translator fuzzing:** arbitrary user input (wildcards, special chars, Arabic, mixed scripts, deep nesting) must parse per the §2.7 grammar and always produce valid regex or a clean, positioned error — never a crash.
- **Privacy CI gate:** verify each process applies the no-network sandbox profile, fail on any outbound connection attempt at runtime (including spawned ffmpeg/ffprobe), and run `cargo-deny`.
- **Release benchmarks:** indexing throughput, query latency, and memory ceilings run on every release against a fixed corpus.
- **Prior art:** none in-repo (greenfield); the golden-corpus and fuzz patterns follow the testing approach of the local open-source macOS search projects researched during design (fsearch, Seek, MacHunt).

## Out of Scope

- AI or semantic search of any kind — permanent, by design.
- Any network feature inside the app — permanent, by design.
- Personal data source search — contacts, calendar, reminders, notes, messages, Apple Mail, photos, Safari history — the product is a file/media/disk tool (Mail is additionally blocked by Apple's Core Spotlight access rules).
- Siri Suggestions, web suggestions, and Apple online service results (require network).
- System Settings search, dictionary, unit conversions, calculator, clipboard history, snippets, general launcher utilities.
- Windows/Linux support.
- Cloud sync of settings or index.
- Video content search (frame-level OCR).
- App Store distribution.

## Further Notes

- The full user-facing feature list (v1.1 draft) lives in the workspace as `spotlight-successor-spec.md` and is the source document for this issue.
- Key market research grounding: Raycast rebuilt its indexer in Rust for speed/consistency, validating the Rust-core choice; Raycast's own file search achieves the responsiveness bar this spec targets; no mainstream launcher offers resolution/channel/language filtering or duplicate-by-quality cleanup, which are this app's differentiators.
- Design constraint inherited from the user's requirements: no AI anywhere, regex-only matching (machine-generated from simple user input), and OS-level network isolation are non-negotiable pillars, not preferences.

## Implementation Notes

Changes forced by the build, kept in sync with the spec (v1.6):

- **M0 + M1 (2026-11-10):** comparisons on numbers and sizes are exact, while dates compare by whole period. Words containing digits never get typo variants. Resolution classes use the long edge.
- **M3 (2026-11-10):**
  - **Parsing:** media headers are parsed in-process: MP4/MOV and Matroska with in-house parsers, other audio with lofty, EXIF with kamadak-exif. No ffprobe or exiftool runs on the indexing path.
  - **Storage:** records are stored as compact postcard blobs (under 400 B, uncompressed).
  - **Filters:**
    - **Story 15:** track groups (`a(lang:ar channels:5.1)`) work, and language codes match in both 2- and 3-letter forms.
    - **Story 16:** `hdr:hdr10+` cannot be detected from headers and returns nothing until Tier 2.
    - **Story 21:** sidecar subtitles, Live Photo pairs and screenshots (English and Arabic names) are detected by file name.
    - **`false` values** mean "has the thing, without the property" (`atmos:false` = audio without Atmos).
  - **Not built yet:** the `probe.<field>:` escape hatch, loudness analysis (story 20) and Tier 2 (AVI, MPEG-TS, WMV).
- **M2 (2026-11-10):**
  - **Live updates:** FSEvents with persisted event ids (changes made while the app was closed are replayed on start), inotify on Linux, and polling for network volumes. All three feed one update engine that rescans and diffs only the changed folders.
  - **Renames and moves (story 11):** handled in place by inode, with no media re-read. Because file systems reuse inode numbers, moved folders are verified, and rescanned when they don't match.
  - **Stories 10 and 12:** files are findable within about 300 ms of creation. Media still being written is probed once stable. Half-downloaded files appear only once complete.
  - **Lifecycle:** missing folders are kept as offline and purged after 30 days. A corrupt database is quarantined and its folders re-indexed. Finder tags (`tag:`) are indexed.
  - **Known limits:** hard links are de-duplicated on full scans only. The FSEvents code path is verified by tests on the macOS CI runner, not in the Linux development container.
- **M4 (2026-11-10):**
  - **Story 33 (no network):** enforced by the OS in every process, through `sandbox_init` on macOS and seccomp on Linux, and proven by a child-process test on both platforms. The daemon reports `network_denied`, and the Swift test asserts it on macOS.
  - **Daemon:** `laserdotd` is the single writer. It serves the app and the CLI over a Unix socket with a JSON-lines protocol, the same index and query syntax for both (story 54).
  - **App:** UniFFI bindings and a SwiftUI menu-bar overlay on ⌥Space. It has the hint line, completions and the cheat sheet (stories 39, 40, 42), keyboard actions and drag out (story 26), and English and Arabic UI strings (story 52, UI only).
  - **Not yet:** signing and notarization (they need the developer's certificates), LaunchAgent registration, the hot-key conflict prompt, cheat-sheet live counts, and localized hint and filter texts.
- **M5 (2026-11-10):**
  - **Stories 44, 45 (portable drive index):** each drive's index lives at `/Volumes/<name>/.laserdot/index.db` with paths relative to the mount point, so it works on any Mac and under any mount name. The daemon attaches indexed drives on mount and detaches them on unmount. Search is federated across this Mac's index and every attached drive, and results carry the drive's name. A new filter, `volume:`, selects one drive or `local` (spec §2.3.4).
  - **Story 46 (prompt, resume):** the prompt shows an estimate (files, size, index size, location) before anything is written. The first index commits in chunks; after an interruption it resumes by diff and ends equal to a fresh index.
  - **Story 47 (encryption):** SQLCipher with a passphrase held only in memory for the session. A wrong passphrase reports "locked" and changes nothing.
  - **Hardening:** a drive index is untrusted input. It is opened in defensive mode, its schema is whitelisted (triggers are refused), and every name is validated.
  - **Read-only drives:** the index is kept on the Mac. On FAT/exFAT, where inodes aren't stable, moves are paired by name, size and modified time. The system index never crosses into another volume (§17.6.1).
  - **Drive manager:** a Drives submenu in the app and `laserdot drives …` in the CLI (list, plan, index, unlock, detach, rebuild, delete, eject).
  - **Spec changes (v1.9):** the `volume:` filter was added. As-built notes in §9 cover mount-relative paths, FSEvents ids not being stored on drives, the read-only fallback, the hardening, and the encryption exposure.
  - **Not yet:** per-volume inclusion toggles (§17.6.3), per-volume browse views (§17.3.2), and the Swift UI has only been compiled and tested on the CI macOS runner.
- **M6 (2026-11-10):**
  - **Story 23:** three deterministic detectors, all tested with real files.
    - Exact copies: size, then sampled BLAKE3, then the full hash.
    - Similar images: pHash read from the EXIF thumbnail when present, with EXIF orientation applied. Pairs within 7 bits are found with a multi-index hash. In the CPU-limited dev container, 1M random hashes take about 11–14 s; Apple Silicon not yet measured.
    - Re-encoded videos: durations within 2 s, then pHashes of the frames at 10/50/90 %. Decoded with AVFoundation on macOS or ffmpeg on Linux.
    - The fixtures in `laserdot-media/tests/fixtures/dupes` cover a different codec (HEVC), container (.mov, .m4v), size and bitrate.
    - Copies on a drive group with the ones on this Mac.
  - **Story 24:** keep-best order per §4.2. Images use file bits per pixel. Ties go to this Mac's copy, then the older file.
  - **Story 25:** a dry run before anything moves; nothing is deleted. Files are re-checked against the scan, and a group is skipped unless a kept copy is unchanged. Files move to the Trash (with Put Back on macOS) or a folder, and the index is updated.
  - **Interfaces:** the app's Duplicates window, `laserdot dupes`, and daemon requests (protocol v3). The scan does not hold the index lock while it reads files.
  - **Security:** the fingerprint cache lives only in this Mac's index. A drive's untrusted index can't plant hashes.
  - **Spec changes (v1.10):** as-built notes for §4.1–4.4. No behaviour changes.
  - **Not yet:** progress reporting during a long first scan, and comparing HEIC/AVIF/RAW off macOS. Letterboxed or cropped re-encodes are not matched, since frames are compared whole.
- **M7 (2026-11-10):**
  - **Story 7 (content search):** text files (UTF-8 and UTF-16, about 100 text, code and data extensions) and PDFs (pure-Rust extraction) are indexed in a contentless FTS5 table in `index.content.db`. Words are folded with `laserdot-fold`, so Arabic and diacritic folding matches names (§2.5).
  - **Matching:** a plain word matches names, then contents in BM25 order (§2.4.2). The other clauses and negations still apply to content hits, and they are marked in results. `content:` requires a match (phrase, trailing-`*` prefix, list, negation).
  - **Scope and skips (§2.4.3–2.4.5):** content folders default to Documents/Desktop/Downloads and are configurable. Files are skipped when over the 20 MB limit (configurable), binary, or unreadable. A file is re-read only when its size or mtime changes; files that leave the scope are removed.
  - **Daemon:** content updates run in the background after changes, without holding the index lock. Protocol v4.
  - **Interfaces:** CLI `laserdot content …`, the app's Content Search menu, and the "in contents" marker on results.
  - **Spec changes (v1.11):** §2.7 now defines plain-word content matching and the `content:` filter. §2.4.5 has as-built notes.
  - **Not yet:** content indexing on drives, Office formats (docx, pages), snippets of the matched text, and measured content-search latency at scale.
- **M8 (2026-11-10):**
  - **Stories 39–42 (guidance):** zero-results coaching from the index (§8.3): index this folder, corrected value, relax a filter with its count, then the cheat sheet. Also live value counts (§8.9.5), the section under the caret (§8.9.6), and keyboard control of the cheat sheet (§8.9.8).
  - **History and pins (§8.6):** last 50 used queries, recalled with ↑/↓ from an empty box. ⌘D pins a query, and pins show as chips. Kept in the app's preferences on this Mac.
  - **Story 52 (Arabic):** filter descriptions, section titles, hints and the CLI cheat sheet in Arabic. A test keeps the Arabic table complete. Keys stay ASCII and are laid out left-to-right.
  - **Accessibility (§14.1):** VoiceOver labels on the query field, buttons, result rows, cheat-sheet entries and coaching.
  - **Hot key (§6.4):** conflict prompt with alternatives, and a Hot Key menu.
  - **Interfaces:** daemon requests `coach` and `value_counts` (protocol v5). The CLI prints coaching on zero results.
  - **Spec changes (v1.12):** as-built notes for §6.4, §8.3, §8.6, §8.9.5–8.9.8 and §14.4.
  - **Not yet:** parser error messages in Arabic. VoiceOver and high contrast have not been checked by a person at a Mac.
- **Status after M8 (2026-11-10):**
  - **Milestones:** M0–M8 (spec §13.10) are built and on `main` (`23bf5d3`). CI passes on Linux and macOS: Rust tests, clippy, `cargo-deny`, and the Swift app build with XCTest through the real daemon. The issue stays open because of the gaps below. The README's Status section has the same list.
  - **Planned gaps inside milestones:**
    - Tier 2, `probe.<field>:`, loudness (story 20);
    - per-volume inclusion toggles and browse views (§17.6.3, §17.3.2);
    - content search on drives;
    - Arabic parser errors.
  - **Spec sections no milestone covered:**
    - result actions (§5, stories 26–29 beyond open, reveal, copy path and drag out);
    - ranking from open history and the fallback chain (§6.2, §6.3);
    - audit page and diagnostics export (§7.6, §7.8);
    - media sort modes, grouping, highlighting and saved searches (§10.1, §10.2, §10.5, §10.6);
    - battery and thermal awareness (§12.3–12.5);
    - URL scheme and Shortcuts (§13.5).
  - **Distribution:** signing and notarization, LaunchAgent registration, Time Machine exclusion of the index.
  - **Verified only by CI:** nobody has used the app interactively on a Mac. That includes VoiceOver, high contrast, ejecting a real drive and the first-mount prompt.

- **Memory-mapped snapshot (§2.1.1g, after M8):**
  - **What it does:** the daemon and the CLI map `index.db.snapshot` at launch instead of rebuilding the store from SQLite. At 1M entries that takes 0.2 s against 3.4 s plus the SQLite read, and queries are just as fast.
  - **Memory:** a process serving from the snapshot holds 125 MiB dirty plus 183 MiB file-backed, so it is now within the 150 MiB dirty budget (§12.7).
  - **Safety:** a token shared with the database means a snapshot is used only for the exact state it describes. Every write to entries or roots clears the token, and a stale or damaged snapshot falls back to SQLite.
  - **Tests:** results from a mapped index equal a rebuilt one across names, media filters, track groups and all sorts. Live updates on a mapped store, stale and damaged snapshots (300 single-byte corruptions, none of them panic) and a daemon relaunch are covered. The randomized lifecycle test switches the live index to its snapshot every other round.
  - **Spec change (v1.14):** a custom sectioned format instead of `rkyv` (see the decision log).
  - **Not yet:** snapshots for drive indexes.

## Design Decision Log

Decisions made while building M0–M8, with the reason for each. They are binding unless revisited deliberately: change one only with a reason at least as strong, and record the change here and in the spec.

### Privacy and safety

- **No network is enforced by the OS, not by omission.** Every process (app, daemon, CLI, ffmpeg children) applies `sandbox_init` (macOS) or seccomp (Linux) before doing anything else, and `cargo-deny` bans networking crates. A missing entitlement does nothing for a non-sandboxed app, so it is not relied on. A child-process test proves TCP/UDP are blocked and the IPC socket still works.
- **The only IPC channel is a Unix socket in the socket folder.** The macOS sandbox profile allows nothing else. If the preferred socket path exceeds the 104-byte Unix limit, it falls back to `/tmp/laserdot-<uid>/<hash>.sock` in an owner-only folder.
- **A drive's index is untrusted input.** Anyone can plug in a drive. It is opened with SQLite defensive mode and `trusted_schema=OFF`, its schema must match a whitelist (a trigger or view is refused), and every stored name is validated (no `/`, `.`, `..` or NUL) before it reaches memory.
- **Duplicate fingerprints are cached only in this Mac's index, never in a drive's.** A forged hash in a drive index could make a different file look like a copy and get it recommended for removal.
- **Duplicate cleanup never deletes.** Files are moved to the Trash (Finder's, with Put Back) or a folder after a dry run. Every file is re-checked against what the scan saw, and a group is skipped unless a kept copy is still there unchanged. A hard link to a kept copy is never moved.
- **Encrypted drive indexes use SQLCipher through `rusqlite`'s `bundled-sqlcipher`** (CommonCrypto on macOS, the system libcrypto on Linux), so no OpenSSL or networking crate enters the build. The passphrase lives only in the daemon's memory for the session. After a restart the drive shows as locked.
- **The content index stores no copy of the text.** It is a contentless FTS5 table, so it holds only words and positions.

### Matching and the query language

- **Names are matched by regex, never SQLite `REGEXP`.** One regex per word, ANDed, built as `regex-syntax` trees, behind a trigram prefilter over the in-memory name table. Typo tolerance is bounded per-word alternatives, used only as a fallback.
- **Content search is the one exception: FTS5 word matching with BM25.** Regex over gigabytes of text cannot meet the latency budget.
- **The input language is the EBNF grammar in spec §2.7, extended there before any code.** M7 added two rules:
  - a plain positive word matches names first, then contents, and every other clause still applies;
  - `content:` requires a content match and cannot be part of an `OR`.
- **Contents are folded with the same `laserdot-fold` as names before insertion** (FTS5 `unicode61 remove_diacritics 0` over folded text), so Arabic and diacritic folding behaves identically for names and contents.
- **A prefix match on content applies only to tokens of three or more characters, and one-character words skip content search.** Short prefixes would match most of the index on every keystroke.
- **`?` stays the single-character wildcard.** The cheat sheet opens with a button and ⌘/, not by typing `?`.
- **`volume:local` means this Mac's own index.**

### Index and storage

- **SQLite is the durable store only.** Name matching and media filters run on the in-memory store, and never touch SQLite at query time.
- **The hot store is mutable: an overlay of appended entries, tombstones and in-place moves**, compacted by a rebuild when it grows. A randomized lifecycle test checks after every batch of file operations that the live index, the reopened database and a fresh index agree. Any change to the overlay or the update engine must keep it green.
- **Moves are paired by inode, and folder pairings are verified**, because file systems reuse inode numbers.
- **Media values are facets (value → bitmap), not fixed bitsets**, so any codec, language or camera works without a fixed table.
- **`Laserdot`'s database handle sits behind a `Mutex`** (`rusqlite::Connection` is not `Sync`); queries never need it.
- **The memory-mapped snapshot uses a custom sectioned format, not the `rkyv` the spec named.**
  - The hot paths index plain slices (`store.size[id]`). `rkyv`'s archived types (`u32_le`, archived maps) would have changed every access.
  - The custom layout maps the big per-entry columns as native slices and decodes only bitmaps, maps and strings.
- **Mapped columns grow by an owned tail and copy on write.** Live updates append to a tail, so a new file copies nothing; only an in-place write (rename, move, changed date) copies that one column.
- **A snapshot is tied to the database by a random token stored in both.** Every write to entries or roots clears it in the same transaction, so a crash can't leave a stale snapshot looking valid. Only the index's owner writes snapshots: the daemon, or a CLI that wrote without one. A CLI reading next to a running daemon never writes, so the two can't race.
- **A snapshot is validated before use, not checksummed.** Every id, offset and length is range-checked and names are checked for UTF-8, so damage leads to a refusal (fallback to SQLite) rather than a panic. A full-file checksum was skipped because of its cost at launch. Damage to plain values (a size, a date) would go unnoticed, but the snapshot is written by rename, never in place.
- **The content index is a separate SQLite file next to the index (`index.content.db`).** It has its own connection, so content lookups don't contend with the main index, and it is a rebuildable cache: a damaged one is deleted and rebuilt. Its settings (content folders, size limit) live in the main index so they survive a rebuild.
- **Content folders are canonicalized** because the index stores canonical paths (`/var` → `/private/var` on macOS).

### Removable drives

- **A drive carries its own index at `/Volumes/<name>/.laserdot/index.db`, with paths relative to the mount point.** It then works on any Mac and under any mount name. `volume.json` next to it holds the drive's identity.
- **No FSEvents ids are stored in drive indexes**, because they belong to one Mac. A drive is reconciled by a diff when it mounts.
- **A read-only drive's index is kept on the Mac**, in `<data>/drives/<hash of name, file system, size>/`, and the prompt says so.
- **On FAT and exFAT, inodes are not stored; moves are paired by name, size and modification time.** Those file systems don't keep inode numbers stable across mounts.
- **The system index never crosses into another volume.** Scans stop at device boundaries, the updater skips mount points, and indexing a path on a drive points to the drive's own index.
- **A first drive index is committed in chunks of 20,000 entries with a "complete" flag**, so an unplugged drive resumes by diff on the next mount and ends equal to a fresh index.
- **The daemon checks the volumes folder every 3 s instead of using DiskArbitration.** It is portable, testable on Linux with plain folders, and needs no extra framework.
- **Search is federated: each index is searched and the results merged by a sort key**, so drive results interleave correctly with this Mac's.
- **Drives have no content index yet.** Content search covers this Mac's index only.

### Duplicate finder

- **Exact copies: same size, then BLAKE3 of the first, middle and last 64 KiB, then the full hash only to confirm.** A 50 GB file is never read in full to be ruled out.
- **Images: a 64-bit pHash (32×32 DCT), read from the EXIF thumbnail when there is one.**
  - The thumbnail is cropped to the image's aspect ratio, because thumbnails are often padded with bars.
  - The EXIF orientation is applied after downscaling; squashing to a square commutes with 90° turns.
  - Nearly uniform images are skipped.
- **HEIC, AVIF and RAW are compared only on macOS (ImageIO).** There is no pure-Rust decoder for them.
- **Video frames are decoded with AVFoundation on macOS and `ffmpeg` elsewhere.** Spawning ffmpeg is acceptable because the duplicate scan is not the indexing path. Only videos with a duration neighbour (±2 s) are decoded. A flat (black or white) frame matches only another flat frame, and at least one compared frame must have detail.
- **Near pairs are found with a multi-index hash: four 16-bit chunks probed within ⌊d/4⌋ bits.** The first design (six 11-bit chunks) took 178 s for 1M hashes when measured, so it was replaced. The default distance was lowered from 10 to 7 bits; copies in the corpus sit at 0–6. Docs quote measured numbers only.
- **Groups are connected components of all matches**, so an exact copy of a picture joins that picture's similar group.
- **Keep-best order: resolution → HDR → bits per pixel → audio channels → bitrate.**
  - For images, bits per pixel is file bits per pixel.
  - Ties go to the copy on this Mac over a drive, then the older file (usually the original), then the shorter path.
- **The scan runs in three steps.** Collecting candidates and saving fingerprints touch the index (fast); reading files does not. The daemon therefore never holds the index lock for minutes, and the app runs the scan on a connection of its own so searching isn't blocked.

### Daemon, interfaces and app

- **`laserdotd` is the single writer of the index.** The app and the CLI are clients over JSON lines on the Unix socket. The protocol version rises with each milestone (v1 in M4 to v5 in M8). Searches share a read–write lock, and the watcher waits for changes without holding it.
- **Slow reads run off the index lock:** content extraction and duplicate hashing. Writes, including attaching and refreshing a drive, take the write lock.
- **The CLI routes through the daemon when it answers**, and opens the index directly otherwise. `--local` opens it read-only next to a running daemon.
- **The Swift app talks to the daemon through UniFFI (0.32) and links the Rust core as a static library.** SwiftPM does not inherit Rust's link directives, so `Package.swift` names every framework the core uses: Security, AVFoundation, CoreMedia, ImageIO, CoreGraphics and others.
- **Guidance comes from the Rust core in the requested language** (hint, completions, filters, cheat sheet); the app passes its own localization.
- **Query history and pinned queries live in the app's preferences**, on this Mac and outside the index. ↑/↓ walk the history only from an empty box, so a typed query is never overwritten.
- **Hot key: when ⌥Space is taken, the app offers ⌃Space, ⌥⌘Space or ⌃⌥Space and remembers the choice.** macOS has no API to name the app that owns a shortcut. The Carbon event handler is installed once, so changing shortcuts can't double-toggle the overlay.

### Guidance and localization

- **Every filter is declared once in the registry**, and the parser, hints, completions, cheat sheet and CLI help derive from it.
- **Arabic guidance lives in a separate table (`laserdot-query/src/i18n.rs`), with a test that fails if a filter has no Arabic description.** Adding a field to every registry entry was the alternative. Filter keys, values and examples stay ASCII and are laid out left-to-right inside the Arabic UI. Parser error messages are still English.
- **Zero-results coaching is computed in the core from the index**, at most three suggestions, in this order:
  - index an `in:` folder that exists but isn't indexed;
  - correct a text value to the closest indexed one;
  - drop the clause that empties the results, with how many results that gives;
  - open the cheat sheet.
- **Live cheat-sheet counts run `key:value` for every enumerated value.** It is plain bitmap work over every index, so it adds no new code path to maintain.

### Testing and process

- **Tests check external behaviour at Seam 1 (the core API) with real files in temp dirs**, plus the golden media corpus at Seam 2. Duplicate fixtures (images, and a clip re-encoded to another codec, container, size and bitrate) are generated by `fixtures/dupes/generate.sh` and committed.
- **macOS-only Rust code is type-checked locally** with `cargo clippy --target aarch64-apple-darwin`, using a stub C compiler for crates with C build scripts (blake3). It runs for real on the macOS CI runner.
- **CI installs ffmpeg on Linux** so the re-encoded-video path is tested on both platforms (AVFoundation on macOS).
- **Swift compiles only in CI.** Each milestone is built on its own branch, merged to `main` by fast-forward once CI passes. M7 and M8 were merged at the owner's request before their macOS test job ran (runners were unavailable); that job passed afterwards on the same commit.
- **The spec is kept in sync as the build goes:** "As built" notes per section, version bumped per milestone (v1.8 → v1.13), and changes noted in this issue.
