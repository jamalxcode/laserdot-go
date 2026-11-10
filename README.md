# laserdot-go

laserdot in Go: private, media-aware file search for macOS — a Spotlight successor that can't phone home.

- **Fast and live:** its own in-memory index; filters are bitmap operations, names are matched by regex behind a
  trigram prefilter.
- **Media-aware:** resolution, codecs, HDR, frame rate, channels, per-track languages, subtitles, EXIF, music tags.
- **Plain input, regex underneath:** type `invoice march`, `report*` or `res:4k -sample`.
- **Arabic-aware:** `أحمد` = `احمد`, `فاتورة` = `فاتوره`, `٢٠٢٤` = `2024`, `café` = `cafe`.
- **No network, no AI, no telemetry**, enforced by the OS sandbox and checked in CI.

The spec is [`docs/spotlight-successor-spec.md`](docs/spotlight-successor-spec.md). The port's plan and progress are
in [`issues/002-go-port.md`](issues/002-go-port.md). The original Rust implementation is `jamalxcode/laserdot`.

## Status

Porting. Phase **G1** (daemon and search) is done: name and file-filter search, the SQLite store, the CLI, and
the daemon with the OS network sandbox. Next: **G2**, media parsing.

| Phase | State |
|---|---|
| G1 daemon + search | done |
| G2 media | next |
| G3 live updates | — |
| G4 drives, duplicates, content, snapshot, Tier 2, app | — |

## Build

```bash
go build ./...
go test ./...
go run ./cmd/laserdot index ~/Documents
go run ./cmd/laserdot search 'invoice march'
go run ./cmd/laserdotd &            # optional: the CLI then goes through the daemon
go run ./cmd/laserdot status
```

Pure Go (no cgo); the Go version is in `go.mod`.

The menu-bar app (macOS only) talks to `laserdotd` over its Unix socket:

```bash
app/scripts/build-go.sh             # laserdotd + laserdot into app/build/go, and the app's registry
(cd app && swift build && LASERDOTD=$PWD/build/go/laserdotd swift test)
app/scripts/bundle.sh               # → app/build/Laserdot.app (unsigned; CI uploads Laserdot-unsigned.zip)
```
