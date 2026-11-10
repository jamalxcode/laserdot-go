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
