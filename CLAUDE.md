# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Project

**laserdot-go** — laserdot reimplemented in Go: a private, media-aware file, media and disk search tool for macOS
(a Spotlight successor). Menu-bar app (⌥Space overlay) with its own index, header-parsed media columns, regex
matching generated from plain user input, a duplicate-by-quality finder, and portable per-drive indexes.
Go core and daemon + the SwiftUI shell, which talks to the daemon over a Unix socket. The spec is the source of
truth for behavior.

The Rust implementation lives in `jamalxcode/laserdot` (M0–M8 built there). It is the reference: when porting a
feature, read its code and tests there, and port the tests first.

## Key documents

- `docs/spotlight-successor-spec.md` — full feature spec. Section numbers (§) below refer to it. Written for the
  Rust build; where Go changes a mechanism (not a behavior), Issue 002 records the mapping.
- `issues/001-spotlight-successor.md` — the epic: user stories, decisions, per-milestone notes from the Rust build,
  and the **Design Decision Log** (binding unless revisited deliberately).
- `issues/002-go-port.md` — the Go port: decisions, phases, Rust→Go mapping, and progress.

When the spec and an issue disagree, flag it to the user rather than silently picking one.

## Non-negotiable pillars

1. **No network, ever.** Every process applies an OS sandbox that denies network access before doing anything
   else (seccomp on Linux, the macOS sandbox profile on macOS). Our code opens Unix sockets only (the daemon's
   socket); `internal/netcheck` fails the build if a package that speaks a network protocol (`net/http`,
   `crypto/tls`, `net/rpc`, `net/smtp`, third-party HTTP clients) enters the dependency graph, or if our code
   calls `net.Dial`/`net.Listen` with anything but `"unix"`. No telemetry, analytics, crash reporters.
   **The one exception** (granted by the owner, spec §7.4): the bundled `Laserdot Updater` helper
   (`app/Sources/LaserdotUpdater`) may reach GitHub only — on request or once a day — to find and download a
   release. Laserdot itself and `laserdotd` stay network-blocked; CI fails if network code (URLSession,
   Network.framework…) appears anywhere else in the app. Do not widen the exception without the owner.
2. **No AI.** No embeddings, semantic search, model downloads, or inference. Perceptual hashes (DCT) are fine.
3. **Regex-only matching.** Name/path queries run as regex (Go `regexp`, RE2: linear time) over an in-memory name
   table with a trigram prefilter — never SQLite. One regex per word, ANDed. Build regexes as `regexp/syntax`
   trees, not by string concatenation. Content search is FTS5 word matching (the one exception). The input
   language is the EBNF grammar in §2.7 — extend it there first.
4. **Scope is files, media, and disks.** Personal data sources, System Settings, calculator, dictionary,
   clipboard and snippets are out of scope.

## Layout

```
internal/fold     Unicode folding (§2.5) — single source of truth for index AND query folding
internal/query    filter registry (§8.8), §2.7 parser, AST, regex compilation
internal/scan     folder walk + ignore rules, shared Change type, Finder tags, volumes, Trash
internal/watch    FSEvents (purego, no cgo) / inotify / polling → Changes
internal/media    file kinds, Tier 1 header parsers, Tier 2 (ffprobe/ffmpeg), duplicate hashing
internal/index    SQLite durable store (modernc), mutable in-memory store + media index, update engine,
                  execution + ranking, content (FTS5), snapshot, sealed drive-index container
internal/core     public API = test Seam 1 (index, watch, search, drives, dupes, cleanup, content, guidance)
internal/sandbox  OS-enforced no-network
internal/ipc      daemon protocol (JSON lines over a Unix socket) + client — the Rust wire format
internal/daemon   the daemon's request handling (testable in-process)
internal/netcheck the network-dependency check (pillar 1)
cmd/laserdotd     the daemon: single writer of the index
cmd/laserdot      the CLI (routes through the daemon when it runs)
app/              SwiftUI menu-bar app (SwiftPM): LaserdotKit = pure-Swift daemon client + registry-driven guidance
app/Sources/LaserdotUpdater  the update helper — the only networked code (spec §7.4)
app/scripts/      build-go.sh (laserdotd, CLI, Registry.generated.swift), bundle.sh (icon, helper, version,
                  ad-hoc signature), make-dmg.sh, registry/ (the generator)
```

- **Pure Go, no cgo.** SQLite is `modernc.org/sqlite`. Dependencies must not speak a network protocol.
- **Speed rules:** no per-file process spawning on the indexing path; header-only reads; name matching and media
  filters never hit SQLite at query time.
- **Filter registry:** every filter is declared once in `internal/query`; parser, hints, completions, cheat sheet
  and CLI help derive from it.
- **Drive indexes are untrusted input:** open them through `index.OpenDrive` (schema whitelist, name validation,
  mount-relative paths). The system index never crosses into a volume. Fingerprints live only in this Mac's index.
- **Snapshot:** `internal/index/snapshot.go` writes every `Store`/`MediaIndex` field in order; a new field must be
  added there (and `snapshotVersion` bumped). Every `Db` write calls `invalidateSnapshot` in its transaction.
- **Duplicate cleanup never deletes:** moves to the Trash or a folder after a dry run; a group is skipped unless
  every kept copy is unchanged.
- macOS-only code goes behind build tags (`//go:build darwin`) with a portable fallback, so everything builds and
  tests on Linux.

## Commands

```bash
go build ./...
go test ./...
go vet ./...
gofmt -l .                       # must print nothing
go test ./internal/fold -fuzz FuzzFoldIdempotent -fuzztime 60s
go test ./internal/index -run X -bench Search1M -benchtime 20x   # 1M-entry benchmark
go run ./cmd/laserdot index ~/Movies                     # build/refresh the index
go run ./cmd/laserdot search 'invoice march'
go run ./cmd/laserdot help-syntax                       # cheat sheet from the registry
go run ./cmd/laserdotd --db /tmp/x/index.db             # the daemon; `laserdot --db /tmp/x/index.db status`
go run ./cmd/laserdot watch                             # keep the index current without the daemon
go run ./cmd/laserdot drives                            # plan/index/unlock/detach/rebuild/delete/eject
go run ./cmd/laserdot dupes 'kind:video'                # duplicate finder (dry run; --trash / --move-to DIR)
go run ./cmd/laserdot content add ~/Docs                # content search folders (status/add/remove/max-size/update)
LASERDOT_STRESS_SEEDS=200 go test ./internal/core -run TestRandom   # longer lifecycle stress run
# macOS only — the app:
app/scripts/build-go.sh            # app/build/go/{laserdotd,laserdot} + the registry (`universal` for arm64+amd64)
(cd app && swift build && LASERDOTD=$PWD/build/go/laserdotd swift test)
VERSION=0.2.0 app/scripts/bundle.sh   # → app/build/Laserdot.app (laserdotd, Helpers/Laserdot Updater.app)
app/scripts/make-dmg.sh out/Laserdot-0.2.0.dmg
git tag v0.2.0 && git push origin v0.2.0   # the Release workflow builds and publishes the .dmg
```

Run gofmt, vet and the full test suite before committing.

## Testing approach

- Test external behavior: query in, results out. Seam 1 is `internal/core` with real files in temp dirs.
- Port the Rust tests for each feature (same fixtures, same expectations) before or with the code.
- Parsers and the query translator: fuzz tests — any input yields a query or a positioned error, never a panic.
- **Privacy gate (§15.5):** `internal/sandbox/deny_test.go` (child process: TCP/UDP blocked, IPC socket folder
  allowed, inherited by children), the daemon binary's `status.network_denied` (`internal/daemon`), and
  `internal/netcheck`. Every process calls `sandbox.DenyNetwork` first (on macOS it re-executes the process).
- **App:** the app reaches the daemon only through `app/Sources/LaserdotKit/DaemonClient.swift` (a Unix socket,
  JSON lines; no TCP, no URLSession) and applies the same Seatbelt profile to itself at launch. Its Codable types
  mirror the protocol-5 wire shapes; a feature whose op answers `unsupported` is hidden or disabled. Guidance
  (hints, completions, sections, filter list, cheat sheet) comes from the daemon's `hint`, `section_at`,
  `completions`, `filters` and `cheat_sheet` ops on a connection of its own; when the daemon is not running or lacks
  them, `Guidance.swift` answers from `Registry.generated.swift`. Rerun `app/scripts/build-go.sh` after changing the
  registry (CI fails on a stale file) and add the filter's Arabic description in `Guidance.swift` too (a Swift test
  enforces it). Swift compiles only on macOS: the CI `app` job builds, tests and bundles it.

## Conventions

- Keep the spec in sync: if Go forces a behavior change, update the spec and note it in Issue 002.
- User-facing strings in English and Arabic.
- Work on a branch; merge to `main` by fast-forward once CI passes.
