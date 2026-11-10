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
