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
   calls `net.Dial`/`net.Listen` with anything but `"unix"`. No telemetry, analytics, crash reporters, updaters.
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
internal/scan     folder walk + ignore rules
internal/media    file kinds (G2: Tier 1 header parsers)
internal/index    SQLite durable store (modernc, pure Go), in-memory store, planner + execution
internal/core     public API = test Seam 1
internal/sandbox  OS-enforced no-network
internal/ipc      daemon protocol (JSON lines over a Unix socket) + client — the Rust wire format
internal/daemon   the daemon's request handling (testable in-process)
internal/netcheck the network-dependency check (pillar 1)
cmd/laserdotd     the daemon: single writer of the index
cmd/laserdot      the CLI (routes through the daemon when it runs)
```

- **Pure Go, no cgo.** SQLite is `modernc.org/sqlite`. Dependencies must not speak a network protocol.
- **Speed rules:** no per-file process spawning on the indexing path; header-only reads; name matching and media
  filters never hit SQLite at query time.
- **Filter registry:** every filter is declared once in `internal/query`; parser, hints, completions, cheat sheet
  and CLI help derive from it.
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
```

Run gofmt, vet and the full test suite before committing.

## Testing approach

- Test external behavior: query in, results out. Seam 1 is `internal/core` with real files in temp dirs.
- Port the Rust tests for each feature (same fixtures, same expectations) before or with the code.
- Parsers and the query translator: fuzz tests — any input yields a query or a positioned error, never a panic.
- **Privacy gate (§15.5):** `internal/sandbox/deny_test.go` (child process: TCP/UDP blocked, IPC socket folder
  allowed, inherited by children), the daemon binary's `status.network_denied` (`internal/daemon`), and
  `internal/netcheck`. Every process calls `sandbox.DenyNetwork` first (on macOS it re-executes the process).

## Conventions

- Keep the spec in sync: if Go forces a behavior change, update the spec and note it in Issue 002.
- User-facing strings in English and Arabic.
- Work on a branch; merge to `main` by fast-forward once CI passes.
