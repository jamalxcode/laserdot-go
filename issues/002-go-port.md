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

