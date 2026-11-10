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

Porting. Phase **G1** (daemon and search) has started: Unicode folding is done.

## Build

```bash
go build ./...
go test ./...
```

Pure Go (no cgo), Go 1.24+.
