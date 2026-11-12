# Laserdot — Feature Specification

*(working title during design: "Spotlight Successor")*

**Version:** 1.27 (draft)
**Date:** 2026-11-11 — v1.18–v1.22 record the owner's decisions while using the app; the conversation behind them is
in `docs/design-conversation.md`
**Target platform:** macOS 14+, Apple Silicon
**Distribution:** Direct download, signed + notarized, non-sandboxed

---

## 1. Overview

A private, media-aware **file, media, and disk** search tool for macOS. It matches Spotlight for file search and exceeds it for media and removable volumes; it deliberately does not search personal data sources (contacts, calendar, mail, messages, notes, photos, Safari) — that is out of scope. Core differentiators:

1. **Own index** — SQLite (durable store) + in-memory name table (hot path) + FTS5 (file contents), independent of Apple's Spotlight; deterministic results
2. **Media intelligence** — resolution, channels, codec, HDR, language tracks, EXIF as first-class filterable columns, read by fast in-process header parsers (ffmpeg only as fallback)
3. **Regex-native matching** — all name/path searches execute as regex, but users type plain words and wildcards; the engine generates the regex. File-content search uses FTS5 word matching (see 2.4.1)
4. **Structural privacy** — the app cannot make a network connection; every app process denies itself network access with a macOS sandbox profile at startup (see 7.1)
5. **Duplicate-by-quality cleanup** — find and resolve duplicate media, keeping the best copy

---

## 2. Search Engine

### 2.1 Index

- **2.1.1** SQLite database (WAL mode, memory-mapped reads) is the durable store and source of truth; FTS5 is used for file contents only (2.4)
- **2.1.1a** Name/path matching hot path: an in-memory table of folded filenames, loaded from SQLite at startup and kept in sync by the indexer. Queries scan it in parallel across all cores with the Rust `regex` crate — never via SQLite `REGEXP`, which forces a full row-by-row scan
- **2.1.1b** Trigram prefilter: every 3-character chunk of each folded filename is indexed (`invoice` → `inv`, `nvo`, `voi`, `oic`, `ice`). For each query regex, the required literals are extracted (`regex-syntax` literal extraction), the trigram index narrows candidates, and only those candidates are verified with the full regex. Regexes with no extractable literals (e.g. `re:^.{3}$`) fall back to the full parallel scan
- **2.1.1c** Media hot store: media attributes are held in memory as fixed-width columns (struct-of-arrays, ~24 bytes per media file): `width/height u16`, `duration_ms u32`, `fps×1000 u16`, `bitrate u32`, `vcodec/acodec u8 enum`, `channels u8`, flag bits (`hdr`, `dv`, `atmos`, `lossless`, `vfr`, `local`, `probe_failed`…), `audio_langs u64` and `sub_langs u64` bitsets (63 common languages + overflow table)
  - *As built (M3):* numeric media columns exist only for entries that have a media record (an entry → row map costs 4 bytes per non-media file). Languages, codecs, profiles, channel layouts, HDR formats, containers, cameras, lenses and music tags are **facets** — folded value → roaring bitmap of entry ids — rather than fixed bitsets, so any value works without a fixed table. Per-track lists are kept for track groups
- **2.1.1d** Bitmap indexes (`roaring` crate), one per enumerated value — `kind`, `codec`, `res` bucket, HDR type, each language, each boolean flag. Enumerated filters resolve as bitmap ANDs/ORs in microseconds; numeric ranges (`fsize:>4gb`, `duration:>2h`) are single-column scans (~1 ms per 1M rows)
- **2.1.1e** Query planner order: bitmap filters → numeric column ranges → trigram candidates → regex verification → top-k. Relevance uses a bounded top-k heap, not a full sort
- **2.1.1f** Pre-sorted row orders maintained for size, date modified, and date created: sorting by these walks the order and stops at the page size (10.4), so it is constant-time regardless of library size
- **2.1.1g** Cold start: the daemon writes a snapshot of all in-memory tables (zero-copy `rkyv` format) that the shell and CLI memory-map at launch; first query is served without loading rows from SQLite. SQLite remains the durable store and the source for snapshot rebuilds
  - *As built (after M8):* the snapshot is `index.db.snapshot`, written by the index's owner: the daemon at launch when it had to rebuild, every 60 s when the index changed, and on shutdown; the CLI after it writes without a daemon. It is written to a temporary file and renamed, so a mapping of the previous one stays valid. The daemon and the CLI map it at launch; the app does not read the index itself (it talks to the daemon), so "the shell" maps it through the daemon.
    - **Format: a custom sectioned layout, not `rkyv`.** Plain per-entry columns (names, parents, sizes, dates, kinds, flags, dimensions, sort orders, the children index, the media columns) are native-endian arrays aligned to 8 bytes and served straight from the mapping as slices. The hot paths keep reading plain slices, which `rkyv`'s archived types (`u32_le`, archived maps) would have replaced everywhere. Bitmaps (roaring's portable format), maps, track lists and strings are decoded into memory. The writer destructures the store exhaustively, so a new field cannot be left out.
    - **Live updates on a mapped store:** new entries go to an owned tail, so a new file copies nothing. An in-place write (rename, move, changed date, hidden flag) copies only that column into memory. A rebuild (new folder, compaction) writes a new snapshot and maps it again.
    - **Validity:** the snapshot carries a random token that the database also stores. Every write to entries or roots clears it, in the same transaction, so a snapshot is used only for the exact database it describes. A missing, stale, truncated or damaged snapshot falls back to the rebuild from SQLite. Before use, every id, offset and length is checked, and names are checked for valid UTF-8.
    - **Measured** (1M synthetic entries, 63% media, 4-vCPU container): 210 MiB on disk, written in about 0.4 s, mapped in about 0.2 s against 3.4 s to build from rows (plus reading them from SQLite). Query times are unchanged. A fresh process that maps it and queries holds 125 MiB dirty plus 183 MiB file-backed.
    - Drive indexes still load from their own SQLite file.
- **2.1.1h** Directory walking uses macOS `getattrlistbulk` (entries + size, dates, inode, flags incl. `SF_DATALESS` in batched syscalls) rather than `readdir` + `stat` per file. Names are stored in a shared arena with a parent-directory id per entry, not as full path strings
- **2.1.2** Incremental updates via FSEvents (coalesced, no full rescans); re-probe files only on mtime/size change
  - *As built (M2):* macOS uses a directory-level FSEvents stream over all indexed folders (300 ms coalescing). The last applied event id is stored per folder, so on start the stream replays everything since (`sinceWhen`); the `MustScanSubDirs`, `UserDropped`, `KernelDropped`, `RootChanged` and `EventIdsWrapped` flags become recursive rescans. Linux uses inotify (one watch per folder; queue overflow → recursive rescan) and, having no replay, reconciles with a full diff on start. Every backend only says "something under this folder changed"; the update engine rescans and diffs, so no backend is trusted for details
  - The in-memory store is updated in place: new and changed entries are appended, removed ones tombstoned, renames and moves applied to the parent pointer; it is rebuilt from SQLite once the overlay exceeds max(50k, 10 %) of entries
- **2.1.3** Batch indexing queue with priority (user folders first), resumable across restarts via checkpoint
- **2.1.4** Index excluded from Time Machine (rebuildable artifact)
- **2.1.5** Performance targets: time-to-first-result under 50 ms over millions of indexed files (trigram-prefilterable queries are expected to be well under this; full-scan regexes must still meet it). Pure media-filter queries (e.g. `res:4k hdr:true lang:ar`) under 5 ms over 1M media files

### 2.2 User input model (no regex knowledge required)

- **2.2.1** Plain-language input: type words as you'd say them — `invoice march`. The engine builds **one regex per word** and a file matches only if **all** word regexes match (AND): `invoice march` → `invoice` AND `march`. Word order never matters, and no permuted regex (`invoice.*march|march.*invoice`) is ever generated — that form grows factorially (5 words = 120 branches) and breaks the regex size limit once typo variants are added
- **2.2.1a** Plain words match against the filename by default. A word is matched against the full path only when it contains `/`; folder scoping uses `in:`
- **2.2.2** User-facing wildcards, translated to regex under the hood:
  - `*` → any characters, `?` → exactly one character
  - A word **without** wildcards matches anywhere in the name (substring): `report` → `report`
  - A word **with** wildcards matches the **whole** name (glob semantics, anchored both ends): `report*` → `^report.*$`, `file?.txt` → `^file.\.txt$`, `*2024*` → `^.*2024.*$`
- **2.2.3** Smart matching strategy — engine picks the best interpretation per query:
  - Exact match exists → ranked first
  - No exact hit → auto-fallback to partial/token matching (word order ignored)
  - Transposition and single-character typo tolerance, implemented as bounded regex alternatives generated **per word** at query time — no fuzzy engine, still pure regex
  - Typo variants are generated only for words of 4+ characters, only when exact matching returns fewer than a threshold of results, and typo matches always rank below exact matches
  - *Revised (v1.23):* three-letter words get only swapped neighbours (`pfd` → `pdf`); any other edit of three letters matches almost everything. When every result is only a typo match the app says "No exact matches — showing similar names"
  - *Result order (v1.18, the owner's request; like Spotlight):* by relevance, results are ordered by how well the name matches (exact, then prefix, then word, then substring; typo matches last); among equally good matches, **files come before folders and hidden items (dot files and anything inside a dot folder such as `.obsidian` or `.git`) come last**, then shorter names, then shallower paths. A query with no name words (`jpg`, `kind:video`, `res:4k`) is newest first with the same files → folders → hidden order. While a new query is being answered the app fades the previous results and shows "Searching…", and Return does not open a result of the previous query
  - Words containing digits never get typo variants (`2024` must not match `2023`)
  - Each typo variant contributes its longest literal piece to an any-of trigram prefilter group, so typo fallback stays index-assisted rather than scanning every name
- **2.2.4** Advanced mode: `re:` passes a raw regex through untouched (`re:^IMG_\d{4}\.heic$`). `re:` may follow filters (`kind:image re:^IMG_\d{4}`); everything after `re:` to the end of the query is the regex. Syntax is the Rust `regex` crate's: linear-time matching (no catastrophic backtracking), no lookaround, no backreferences
- **2.2.5** Regex-special characters in user input (`(`, `[`, `+`, `.`) are always literal outside `re:` mode, so `report(2).pdf` just works. The translator builds regexes as `regex-syntax` syntax trees, never by string concatenation, so escaping is correct by construction; no escaping in `re:` mode
- **2.2.6** The exact input language is defined by the grammar in 2.7; behavior not covered there is undefined and must be added to 2.7 before it is implemented

### 2.3 Filters

- **2.3.1** Structured filters combinable with free text in one query: `kind:video res:4k fsize:>4gb since:2025-01-01 in:~/Movies`
- **2.3.2** Filterable facets: file type, extension, date created/modified, size, folder scope, Finder tags (`tag:`; read from `com.apple.metadata:_kMDItemUserTags`, or freedesktop `user.xdg.tags` on Linux)
- **2.3.3** `local:true` — only files physically present on disk (excludes iCloud offloaded placeholders)
- **2.3.4** `volume:<name>` — only results on that drive (pattern on the drive's name); `volume:local` = this Mac's own index (section 9)

### 2.4 Content search

- **2.4.1** Full-text search inside text files, source code, and PDFs, backed by FTS5. **Content search is word (token) matching with BM25 ranking, not regex** — regex over gigabytes of extracted text cannot meet the latency budget. Text is folded (2.5) in Rust before it is inserted and before the query is tokenized, so the same folding applies to names and contents. This is the one deliberate exception to regex-native matching
- **2.4.2** Content matches ranked after name/path matches
- **2.4.3** Scope-limited: user chooses which folders get content indexing (default: on, but limited to Documents/Desktop/Downloads; configurable)
- **2.4.4** Skip rules: files over a configurable size limit (default 20 MB), binary detection, and dev-cache directories
- **2.4.5** Incremental re-index of content only when mtime changes
  - *As built (M7):* the content index is a separate SQLite file next to the index (`index.content.db`), a **contentless** FTS5 table (`contentless_delete=1`, tokenizer `unicode61 remove_diacritics 0` over text already folded by `laserdot-fold`) plus a `docs` table of path, size and mtime: it stores no copy of the text and is rebuilt from the files if damaged. Text files are read as UTF-8 (BOM-aware, lossy) or UTF-16 with a BOM; a NUL byte in the first 8 KiB or mostly undecodable bytes mean binary. ~100 text, markup, data and source-code extensions are read; PDFs are extracted in pure Rust (`pdf-extract`, panics caught). At most 4 MiB of text per file is indexed. Files over the limit (default 20 MB), binary or unreadable files are recorded as skipped and not read again until they change. A file is (re)read when its size or mtime differs from the recorded one; files that leave the scope or disappear are removed. Content folders and the size limit are stored in the index's settings (default `~/Documents`, `~/Desktop`, `~/Downloads`; only files that are also inside an indexed folder). The daemon updates contents in the background after every change, reading files without holding the index lock; the CLI does it after `index`. Plain-word content matches are capped at the 1,000 best (BM25) per query; `content:` filters are not capped. Drive indexes have no content index yet. Managed with `laserdot content [status|add|remove|max-size|update]` and the app's Content Search menu; results found by contents are marked

### 2.5 Unicode and internationalization

- **2.5.1** NFC normalization applied at index time and query time
- **2.5.2** Diacritics-insensitive matching for Latin and Arabic (normalize before indexing). Folding steps, applied identically to names and query words: NFD → remove combining marks (Latin accents, harakat, shadda, sukun, hamza above/below, superscript alef) → remove tatweel `ـ` → letter variants below → digits → full case folding → NFC

  | Folded | From | Note |
  |---|---|---|
  | `ا` | `أ إ آ ٱ` | hamza forms via mark removal; wasla mapped |
  | `و` / `ي` | `ؤ` / `ئ` | via mark removal |
  | `ي` | `ى ی` | alef maqsura, Farsi ya |
  | `ه` | `ة` | ta marbuta |
  | `ك` | `ک` | keheh |
- **2.5.3** Arabic-Indic (`٠–٩`) and Extended Arabic-Indic (`۰–۹`) digit folding (٤ ↔ ۴ ↔ 4)
- **2.5.4** Case-insensitive by default (ASCII + full Unicode case folding)
- **2.5.5** RTL text rendered correctly in the overlay (Arabic filenames display right-to-left with proper bidi)

### 2.6 File lifecycle handling

- **2.6.1** Rename/move tracked as a single update, not delete+insert (inode + path tracking)
  - *As built:* new entries (including the contents of new folders) are paired with removed entries (including the contents of removed folders) by inode; a pair is updated in place and its media is not re-read. A file renamed to a different extension is re-added, keeping its stored probe when the content is unchanged and the new type is one Tier 1 reads
  - Inode numbers are reused by file systems, so a folder pairing is verified: the folder is rescanned where it now is if its listing differs from the moved entry's children, if any change in the batch lies inside its old or new location or inside a recursive rescan, or if any change in the batch pointed at a path the index could not resolve (activity under a name never seen, e.g. a folder renamed twice within one coalescing window). A randomized test applies tens of thousands of random file operations and checks after every batch that the live index, the reopened database and a fresh index return identical results
- **2.6.2** Deletion tombstones with periodic purge (default: purge entries unverified for 30 days)
  - *As built:* an entry is removed as soon as a rescan of its folder proves it is gone. Tombstoning applies to whole indexed folders that disappear (an unplugged drive, an unmounted share): their entries stay searchable, the folder is marked offline, and it is purged once it has gone 30 days without being seen
- **2.6.3** Files being actively written: skip probe until size is stable for 10 seconds
  - *As built:* a media file modified less than 10 s ago is indexed by name immediately and its probe is marked pending; the live watcher re-checks its folder 11 s later, and any refresh probes it once stable. Half-downloaded files (`.crdownload`, `.part`, `.partial`, `.download`, `.opdownload`, `.!qB`, `.!ut`) are not indexed until renamed to their final name (story 12)
- **2.6.4** Index corruption detection (SQLite integrity check on launch); on failure: quarantine, rebuild from checkpoint, never lose the queue state
  - *As built:* `PRAGMA quick_check` on open; a failing database is moved to `index.db.corrupt-<time>` and recreated, and every folder listed in the side file `index.db.roots` (rewritten whenever the folder list changes) is re-indexed automatically
- **2.6.5** Symlinks: indexed by default with cycle detection; hard links deduplicated by inode
  - *As built:* symlinks are recorded, never followed (so no cycles arise). Full scans list a hard-linked file once, under its lexicographically smallest path; incremental additions do not de-duplicate across folders until the next full index
- **2.6.6** Network volumes (SMB/NFS): FSEvents unavailable — polling fallback at configurable interval, or opt-out entirely
  - *As built:* network volumes are detected by file-system type (`statfs`: smbfs, nfs, afpfs, webdav, cifs) and polled every 60 s; `laserdot watch --poll N` forces polling
- **2.6.7** Encrypted/sparse images (DMG, sparsebundle): contents not indexed while unmounted; mounted volumes indexed as normal volumes

### 2.7 Query grammar

The query language is defined here in EBNF (ISO 14977). It is the contract for the Rust core's query API (Seam 1): the parser implements exactly this, and tests are written against it.

```ebnf
(* Digits are folded (2.5.3) before parsing, so Arabic-Indic digits are
   valid wherever a digit is. Digit folding maps one char to one char, so
   error positions match the text as typed. Words are fully folded
   (including NFC) when compiled. *)

query       = ws , [ clause , { ws1 , clause } ] , [ ws1 , raw ] , ws
            | ws , raw , ws ;
raw         = "re:" , regex_text ;          (* rest of input, unparsed; Rust regex syntax *)
clause      = term , { ws1 , "OR" , ws1 , term } ;
term        = [ "-" ] , ( filter | phrase | type_word | pattern ) ;
type_word   = known_type_word ;             (* a whole unquoted word without wildcards; see Semantics *)

filter      = [ scope , "." ] , key , ":" , value
            | scope , "(" , ws , filter , { ws1 , filter } , ws , ")" ;   (* track group *)
scope       = "v" | "a" | "s" ;               (* video / audio / subtitle track *)
key         = known_key ;                   (* see filter table; unknown keys → pattern *)
value       = comparison | range | list | phrase | pattern ;
comparison  = ( ">=" | "<=" | ">" | "<" | "=" ) , scalar ;
range       = scalar , ".." , scalar ;
list        = pattern , "," , pattern , { "," , pattern } ;
scalar      = date | number , [ unit ] ;
number      = digit , { digit } , [ "." , digit , { digit } ] ;
unit        = "b" | "kb" | "mb" | "gb" | "tb"   (* size, decimal: 1 gb = 10^9 bytes, as Finder *)
            | "s" | "min" | "h" ;               (* duration *)
date        = digit , digit , digit , digit , "-" , digit , digit , [ "-" , digit , digit ]
            | "today" | "yesterday" | number , ( "d" | "w" | "mo" | "y" ) ;   (* relative: 7d = last 7 days *)

phrase      = '"' , { phrase_char } , '"' ;
phrase_char = ( any_char - ( '"' | "\" ) ) | '\"' | "\\" ;
pattern     = pchar , { pchar } ;           (* may contain the wildcards * and ? *)
pchar       = any_char - ( white | '"' ) ;

ws          = { white } ;
ws1         = white , { white } ;
white       = " " | "\t" | "\n" ;
```

**Semantics**

- **AND / OR / NOT:** clauses separated by whitespace are ANDed. `OR` (uppercase, standalone) binds tighter than AND: `mkv OR mp4 res:4k` = (`mkv` OR `mp4`) AND `res:4k`. A leading `-` negates a term: `-sample`. No grouping parentheses in v1 (track groups below are the only parentheses).
- **Type words (v1.18, the owner's request; like Spotlight):** a plain word that names a file type means that type, not text in the name. A known extension becomes `ext:` with the type's other spellings (`jpg` = `ext:jpg,jpeg,jpe`, `tif` = `tif,tiff`, `mpg` = `mpg,mpeg`, `ppt` = `ppt,pptx,pps,ppsx`, `doc` = `doc,docx`, `xls` = `xls,xlsx`); `powerpoint`/`presentation(s)` = PowerPoint and Keynote, `excel`/`spreadsheet(s)`/`اكسل` = `xls,xlsx,csv,numbers`; `video(s)`, `movie(s)`, `photo(s)`, `picture(s)`, `image(s)`, `graphics`, `music`, `song(s)`, `audio`, `document(s)`, `archive(s)` and their Arabic forms (`فيديو`, `صور`, `موسيقى`, `مستندات`…) become `kind:`. `-jpg` excludes the type and `jpg OR avi` works as expected. To search names for the word, quote it (`"avi"`) or use a wildcard (`avi*`). Extensions that are also common words (`go`, `key`, `pages`, `raw`, `app`, `ts`) are not type words. The hint line says what a type word stands for. The list lives in `internal/query/typewords.go`.
- **Plain-language words (v1.21, the owner's UI critique):** words people say stand for filters, like type words: `today`, `yesterday` (modified), `recent` (since:7d), `big`/`large` (fsize:>100mb), `huge` (>1gb), `small` (<100kb), `tiny` (<10kb), `4k`/`8k`/`1080p`/`720p` (res), `hdr`, `screenshot(s)`, and the phrases `last|past day|week|month|year` (since), `this week|month|year`; Arabic too (`اليوم`, `امس`, `كبير`, `الاسبوع الماضي`…). The hint line says what each stands for; quotes search names. They live in `internal/query/typewords.go`.
- **Track scope:** `a.lang:ar` = the file has *any* audio track in Arabic. Unscoped media keys apply to their natural track type (`lang:` = audio, `channels:` = audio, `codec:` = video). `a(lang:ar channels:5.1)` = **one and the same** audio track matches every filter inside the group. Without the group, `a.lang:ar a.channels:5.1` may be satisfied by two different tracks.
- **Track groups** accept track-level filters only: `lang` `channels` `codec` `acodec` `profile` `pix_fmt` `bit_depth` `hdr` `fps` `atmos` `lossless` `sample_rate` `forced` `format`. Whole-file filters (`fsize`, `duration`, `count`…) inside a group are a positioned error
- **`false` means "has the thing, without the property":** `atmos:false` = files with audio but no Atmos track; `hdr:false` = videos without HDR; `gps:false` = images without a location; `raw:false` = non-RAW images — never documents or folders
- **Bit rates** take `bps`, `kbps`, `mbps` (`bitrate:>20mbps`); `container:` matches the container format (`mp4`, `mov`, `mkv`, `webm`, `m4a`, `mp3`, `flac`, `ogg`, `jpeg`, `heif`…)
- **Patterns:** follow 2.2.1–2.2.3 — substring match without wildcards, whole-name glob with wildcards, matched on the folded filename (full path only if the pattern contains `/`). Each pattern compiles to its own regex.
- **Phrases:** `"march invoice"` is one literal substring, spaces included; wildcards inside quotes are literal.
- **Filter keys:** a token is a filter only if its key is in the filter table (2.3, 3.6); otherwise the whole token is a pattern, so a filename like `10:30.mp4` still works. Keys and units are case-insensitive.
- **Filter values:** text-valued keys (`artist:`, `ext:`, `in:`…) take patterns, so `artist:fair*` works; numeric/date keys take `comparison`/`range`/`scalar`; boolean keys take `true`/`false`; `list` means any of (`ext:mkv,mp4`).
- **Bare values and comparisons:** a bare value matches at the precision typed — `modified:2024` = all of 2024, `fsize:4gb` = 4.00–4.99 GB, `megapixels:12` = 12.0–12.99. Comparisons on numbers, sizes and durations are exact (`fsize:>4gb` = more than 4,000,000,000 bytes, `megapixels:>12` includes 12.2). Comparisons on dates use the whole period (`modified:>2024` = from 1 Jan 2025). Ranges include both ends at their precision (`modified:2025-01..2025-06` = January through June). `since:` and `before:` take a single date. Dates are UTC until local-time support lands
- **File filters** also include `modified:`, `created:`, `before:` and `hidden:` (dot files and anything inside a dot folder)
- **Type errors:** a value of the wrong type for its key (`fsize:hello`, `res:999p`) is a positioned error with a suggestion (8.1, 8.3), not a silent zero-result query.
- **Content (2.4):** a clause that is a single positive plain word or phrase (no wildcards, no `/`) matches the file's **name or contents**: name matches come first, then content matches in BM25 order, each still satisfying every other clause. While any such word is a single character, contents are not searched. Contents are matched as whole folded words (FTS5), not substrings: the last token of each plain word is a prefix when it has at least three characters (`quart` finds `quarterly`), and a word that splits into several tokens (`2024-03`) is a phrase. Wildcard words, negated words and `re:` match names only
- **`content:`** requires a content match: `content:merger`, `content:"merger plans"` (tokens in that order), `content:merg*` (prefix — the only wildcard allowed, as a trailing `*`), `content:merger,invoice` (either). `-content:x` excludes files whose contents match. `content:` cannot be part of an `OR` clause (positioned error). Only files whose text is indexed (2.4.3, 2.4.4) can match
- **`re:`:** may appear once, after any filters; everything after it is passed to the regex crate. Regex syntax errors are reported with the regex crate's span, offset to the position in the full query.
- **Empty query:** returns no results (the UI shows example chips, 8.5).

**Examples**

| Query | Meaning |
|---|---|
| `invoice march` | name contains `invoice` AND `march`, any order |
| `report*` | name starts with `report` |
| `"tax return" -draft` | name contains `tax return`, not `draft` |
| `kind:video res:4k fsize:>4gb` | 4K videos over 4 GB |
| `ext:mkv,mp4 OR kind:audio lang:ar` | (mkv/mp4 files OR audio) with an Arabic track |
| `since:2025-01-01 in:~/Documents` | changed since 1 Jan 2025, under Documents |
| `kind:video fsize:1gb..4gb` | videos between 1 and 4 GB |
| `kind:image re:^IMG_\d{4}\.heic$` | images whose name matches the raw regex |
| `a(lang:ar channels:5.1) res:4k` | 4K files with an Arabic 5.1 audio track |
| `v.bit_depth:10 hdr:dv s.lang:en` | 10-bit Dolby Vision with English subtitles |
| `kind:image camera:*R5* taken:2024 megapixels:>40` | photos from a camera matching `*R5*`, taken in 2024, over 40 MP |
| `budget ext:pdf` | PDFs named `*budget*`, then PDFs mentioning `budget` |
| `content:"merger plans" -draft` | files containing the phrase `merger plans`, not named `*draft*` |

---

## 3. Media Intelligence

### 3.1 Index-time probing

- **3.1.1** Probing is tiered. Spawning an ffprobe process per file is not used on the main path: process launch plus `find_stream_info` (which decodes frames) costs 50–150 ms per file — 28–55 hours for 1M images

| Tier | When | Reads | Implementation | Target (SSD) |
|---|---|---|---|---|
| **0 — listing** | during the walk | name, size, dates, inode, iCloud flag | `getattrlistbulk` (2.1.1h) | ≥100k files/s |
| **1 — headers** | right after the walk | dimensions, duration, codecs, channels, languages, HDR, rotation, EXIF, music tags | in-process Rust header parsers reading only the first ~64 KB (plus the MP4 `moov` box wherever it is) | images ≥2,000/s, video ≥300/s |
| **2 — deep** | idle time or on demand | true VFR detection, Dolby Vision RPU details, loudness, perceptual hashes (4.x) | libavformat in a crash-isolated, restartable worker process; frames decoded with VideoToolbox | as budget allows |

- **3.1.2** Tier 1 parsers: images via `imagesize` (dimensions) and `nom-exif` (EXIF for JPEG/HEIC/RAW/MOV); MP4/MOV via direct `moov` box parsing (tracks, codec config, `colr`, `dvcC`/`dvvC`, language, channel layout); MKV/WebM via EBML header + Tracks + Info elements only; everything else via libavformat opened **without** `find_stream_info`. Only re-probe on mtime/size change; a rename or move never re-probes
  - *As built (M3):* ISO-BMFF (mp4, m4v, mov, m4a, 3gp) and Matroska/WebM use in-house header parsers (`moov` only; EBML header + Info + Tracks + Tags, reached through the SeekHead when written after the clusters). Other audio (mp3, flac, ogg, opus, wav, aiff, ape, wavpack, aac) uses lofty with cover art skipped. Images use `imagesize` plus kamadak-exif over at most the first 1 MiB. AVI, MPEG-TS, WMV and other containers are not probed until Tier 2
  - Profiles and pixel formats use ffprobe's spelling in lowercase (`main 10`, `constrained baseline`, `yuv420p10le`); a cross-check test compares every stream field against ffprobe on the golden corpus
  - Languages are stored as ISO 639-1 where one exists (`ara` → `ar`); `lang:ar`, `lang:ara` and (for French) `lang:fre`/`lang:fra` all match. A Matroska track without a Language element is English, per the Matroska spec
  - Tier 1 limits: HDR10+ metadata lives in per-frame SEI and is not visible in headers, so `hdr:hdr10+` matches nothing until Tier 2; variable frame rate is detected from MP4 sample timing only; duration is the container duration (edit lists are not applied)
- **3.1.3** Per-device I/O scheduling: solid-state vs rotational detected via IOKit. SSD: 8+ concurrent readers; HDD: 1–2 readers, files probed in inode order (approximates on-disk layout) to minimise seeks. Header-only reads are the main lever on USB hard drives (~10 ms per seek caps them at ~50–100 probes/s)
- **3.1.4** Probe results stored as a compact binary record (postcard-encoded `MediaRecord`: all parsed fields, including tracks, tags and EXIF) — measured under 400 B for a 6-track MKV, so no compression is applied. Not raw ffprobe JSON (5–30 KB per file). A record that no longer decodes after a format change is simply probed again
- **3.1.5** Probing runs at `QOS_CLASS_BACKGROUND` (efficiency cores, throttled I/O); see 12.3

### 3.2 Video fields

- Resolution (width × height), with stored display resolution after rotation metadata
- Codec + profile/level (h264, hevc, av1, vp9)
- Frame rate (normalised to 23.976/24/25/29.97/30/50/59.94/60/120; others kept exact); variable frame rate flagged (Tier 1 heuristic from container timing, confirmed in Tier 2)
- Bitrate (total and per-stream); **`bpp`** (bits per pixel per frame = bitrate ÷ (width × height × fps)) as a codec-aware quality measure
- HDR: `color_transfer` (pq/hlg), `color_primaries` → `hdr:hdr10|hdr10+|hlg|dv`; Dolby Vision profile (`dv_profile`)
- Duration, container, `pix_fmt`, `bit_depth`, `profile` (e.g. main10), `field_order`, subtitle streams present, audio track count
- Resolution buckets keyed on the **long edge** so letterboxed and portrait content bucket correctly (1920×800 = 1080p, 1080×1920 = 1080p, 3840×1600 = 4k): sd < 1280 ≤ 720p < 1920 ≤ 1080p < 2560 ≤ 1440p < 3840 ≤ 4k < 7680 ≤ 8k
- Derived: aspect ratio (`ar:21:9`), `orientation:portrait|landscape`

### 3.3 Audio fields

- Codec (aac, flac, opus, dts, truehd), sample rate, channel layout (`channels:stereo|5.1|7.1`)
- Atmos is a separate flag (`atmos:true`), not a channel layout — detected from the TrueHD / E-AC-3 JOC stream profile
- Bitrate; lossless flag (flac/alac/dts-hd ma)
- Per-track language tags (`lang:ar`, `a.lang:ar`), track count (`a.count:>=2`)

### 3.3a Subtitle fields

- Per-track language (`s.lang:en`), forced flag (`s.forced:true`), format (`s.format:srt|ass|pgs`), sidecar present (`sidecar:true`)

### 3.4 Image fields

- Dimensions (full image size, not HEIC tile size), color space, `megapixels`
- EXIF via in-process Rust parsing (no exiftool): `camera`, `lens`, `iso`, `aperture`, `focal`, `gps:true`, and **`taken:`** (EXIF capture date, distinct from file dates)
- Flags: `raw:true` (CR3/NEF/ARW/DNG…, by extension), `live:true` (a HEIC/JPEG and a MOV with the same name stem in the same folder — both files are flagged), `screenshot:true` (macOS screenshot names in English or Arabic — `Screenshot…`, `Screen Shot…`, `لقطة شاشة…` — or an EXIF UserComment containing `Screenshot`)

### 3.5 Music tags

- ID3 / Vorbis comments / MP4 atoms: artist, album, genre, title, track, year — all searchable and filterable (`artist:"fairouz"`, `genre:electronic`)

### 3.6 Media queries

- Field names follow ffprobe's naming where one exists (`pix_fmt`, `color_transfer`, `profile`, `sample_rate`), so ffmpeg users already know them
- Resolution presets as first-class filters: `res:1080p`, `res:4k`
- Track-scoped queries (2.7): `a(lang:ar channels:5.1)` requires one track to match all conditions
- Combined queries: `channels:5.1 codec:hevc`, `s.lang:ar`, `lossless:true`, `hdr:true fsize:>4gb`, `v.bit_depth:10 bpp:<0.05`
- Escape hatch: `probe.<field>:value` queries the stored probe record (3.1.4) for fields not promoted to columns; it is unindexed (slow path) and the hint line says so
- Loudness analysis on demand (not at index time): `ffmpeg -af loudnorm=print_format=json` per user request

### 3.7 Media edge cases

- DRM'd files (FairPlay M4V): probe fails → graceful fallback to filename-only indexing, flagged `probe:failed`
- Corrupt/truncated containers: probe error captured, file still indexed by name, flagged
- Sidecar files (.srt, .ass, .ssa, .vtt, .sub, .sup) linked to their video by name stem — `movie.srt`, `movie.en.srt` and `movie.en.forced.srt` all attach to `movie.mkv` — and the video gets `sidecar:true`
- External audio tracks (e.g. separate .ac3) linked when matching name stem

---

## 4. Duplicate Finder

- **4.1** Three deterministic duplicate detectors (no machine learning):
  - **Exact:** same size, then BLAKE3 of the first, middle, and last 64 KB; full hash only to confirm. A 50 GB file is never read in full to be ruled out
  - **Near-duplicate images:** 64-bit perceptual hash (dHash/pHash — a DCT, not ML) computed from the **embedded thumbnail** (EXIF/HEIC) where present; indexed in a BK-tree / multi-index hash so a 1M-image scan completes in seconds
  - **Re-encoded video:** duration within ±2 s plus perceptual hashes of keyframes at 10/50/90% (VideoToolbox decode), catching the same content across codecs, containers, and names. Resolution is **not** a grouping key, so a 4k and a 1080p copy land in the same group
  - *As built (M6):* **exact** — files are grouped by size; only same-size files get the quick hash (BLAKE3 of the size and the first, middle and last 64 KiB; files of at most 192 KiB are hashed whole), and only quick-hash collisions get the full hash. Hard links (same inode in one index) count once. **Images** — pHash: 32×32 grayscale, 2-D DCT-II, the 8×8 lowest frequencies without the DC row and column, one bit per coefficient above their median. Read from the EXIF thumbnail when there is one (cropped to the image's aspect ratio, as thumbnails are often padded), else decoded (JPEG at 1/2–1/8 scale; PNG, GIF, WebP, BMP, TIFF with the `image` crate), then turned upright by the EXIF orientation. HEIC, AVIF and RAW use ImageIO on macOS and are not compared elsewhere. Pairs within 7 bits (configurable up to 11; copies in the test corpus sit at 0–6) are found with a multi-index hash: four 16-bit chunks, every chunk value within ⌊d/4⌋ bits probed, which by the pigeonhole principle finds every pair within d bits. Measured on 1M uniformly random hashes (the worst case: real collections cluster) in the CPU-limited Linux development container: about 11–14 s at 7 bits and 27–73 s at 10 bits; 8–11 bits cost about 7× more than ≤ 7. Not yet measured on Apple Silicon. Nearly uniform images are skipped. **Video** — candidates are pairs whose durations are within 2 s; each frame at 10/50/90 % is decoded (AVFoundation `AVAssetImageGenerator` on macOS, `ffmpeg` elsewhere, found via `$LASERDOT_FFMPEG`, next to the binary or in a standard location; without one, re-encodes are not compared and the report says so) and pHashed. Frames must match within 12 bits each, nearly uniform (black, white) frames only match each other, and at least one frame must have detail
  - *As built (M6):* groups are the connected components of all matches, so an exact copy of a picture joins that picture's similar group. Fingerprints are cached in this Mac's index (table `fingerprints`, keyed by path and checked against size and modification time) — never in a drive's index, which is untrusted (a forged hash could make a different file look like a copy). A rescan reads only files that changed. The scan can be limited by any query (`kind:video in:~/Movies`) and a minimum size
- **4.2** Keep-best ranking within a group: resolution → HDR → `bpp` → audio channels → bitrate. Raw bitrate alone is not used: it is not comparable across codecs
  - *As built (M6):* images have no stream bitrate, so their `bpp` is file bits per pixel (less compression ranks higher). Ties go to the copy on this Mac over a drive, then the older file (usually the original), then the shorter path
- **4.3** Bulk cleanup flow: review → select → move to trash or folder
  - *As built (M6):* the app's Duplicates window (menu bar → Find Duplicates…) lists groups best-first with the recommendation preselected; every file can be marked or unmarked except a group's last kept copy. The Trash is the Finder's (`NSFileManager trashItemAtURL:`, with Put Back); on Linux the freedesktop.org Trash. A folder move renames, or copies, verifies the size and removes the original across devices. CLI: `laserdot dupes [SCOPE] [--trash | --move-to DIR] [--yes]`
- **4.4** Dry-run preview before any deletion; nothing deleted without explicit confirmation
  - *As built (M6):* nothing is ever deleted — files are moved. The dry run re-checks every file against what the scan saw (still a regular file, same size and modification time). A group is skipped entirely unless at least one kept copy is still there unchanged, and a hard link to a kept copy is never moved. Applying re-runs the same checks, then updates the index for every folder touched. The app shows the dry run (count, bytes, anything skipped and why) and asks first; the CLI is a dry run unless `--trash` or `--move-to` is given, and then asks unless `--yes`

---

## 5. Actions

- **5.1** Per-result actions: open, reveal in Finder, copy path, rename, move, drag out to other apps
- **5.2** Bulk multi-select operations on search results
- **5.3** ffmpeg actions on media results: extract audio (`-vn -acodec copy`), remux, transcode presets, thumbnail/contact sheet generation
- **5.3a** Encoding actions use VideoToolbox encoders (`h264_videotoolbox`, `hevc_videotoolbox`); ffmpeg is built LGPL without x264/x265
- **5.4** Quick Look preview inline with media metadata panel (dimensions, channels, codec, bitrate)

---

## 6. Launcher Layer (file-focused)

- **6.1** App launcher — apps are files on disk, indexed and launched like any other result
- **6.2** Adaptive ranking from open history (bias results toward what you actually open); history stays local
  - *As built (v1.20, the owner's critique):* the app reports each opened or revealed result (`opened` op); core keeps up to 1,000 paths with a count and last time in this Mac's index settings (never a drive's) and gives them a boost (count × 4 within a week, × 2 within a month, × 1 after; at most 255). The boost ranks an opened result first among equally good name matches (after match quality, before files → folders → hidden) and, for filter-only queries, ahead of newer files. `ClearOpens` forgets it
- **6.3** Fallback chain: no local hit → offer Finder query or web search (web search opens the browser; the app itself still makes no network connection) without retyping
- **6.4** Global hotkey ⌥Space overlay; conflict detection at first run (Raycast, Alfred, Spotlight) with alternative suggestions; re-approval prompt if macOS requires it
  - *As built (M4, M8):* the shortcut is registered with Carbon's hot-key API (no Accessibility permission). If another app already owns it, the app says so and offers ⌃Space, ⌥⌘Space and ⌃⌥Space; the choice is remembered, and the menu bar's Hot Key submenu changes it at any time. macOS has no API to name the app that owns a shortcut, so the prompt cannot say which one

---

## 7. Privacy Architecture (non-negotiable)

- **7.1** (exceptions: the update helper, 7.4; the app shell is not OS-sandboxed, below) Zero network connections, enforced by the OS:
  - Omitting `com.apple.security.network.client` alone does **not** block anything: that entitlement only takes effect inside the App Sandbox, and this app is non-sandboxed (13.8)
  - Instead, every app process — the indexer daemon (13.2a) and the SwiftUI shell — applies a macOS sandbox profile to itself at startup (`sandbox_init` with an SBPL profile equivalent to `(allow default) (deny network*)`). File access stays unrestricted; all network access is denied by the kernel
  - The profile is inherited by child processes, so bundled ffmpeg/ffprobe are confined too
  - Opening the browser for web-search fallback (6.3) goes through LaunchServices, not a socket, and keeps working
  - Defense in depth: `cargo-deny` bans networking crates (`reqwest`, `hyper`, `ureq`, `rustls`, `native-tls`, tokio `net`…) so they fail the build; bundled ffmpeg is built with `--disable-network --disable-protocols --enable-protocol=file,pipe`
  - *As built (M4):* crate `laserdot-sandbox`. The macOS profile is `(version 1) (allow default) (deny network*)` plus `(allow network* (local unix-socket (subpath "<socket folder>")))` and the same for `remote` — so DNS (mDNSResponder's socket) is denied too. The app applies it first thing at launch; `laserdotd` applies it before opening anything (when started by the already-sandboxed app it inherits the profile instead). On Linux (development and CI) a seccomp filter makes `AF_INET`/`AF_INET6`/`AF_PACKET` socket creation fail and refuses `io_uring` and the x32 ABI, on every thread. A test re-runs itself as a child process and proves TCP and UDP are blocked, the IPC socket still works, other Unix sockets are blocked (macOS), and processes started afterwards inherit the restriction; it runs on both CI platforms, and the daemon reports `network_denied` in its status
- **7.2** No AI, no embeddings, no model downloads, no inference
- **7.3** No telemetry, analytics, crash reporters, or third-party SDKs that call home
  - *Decided (v1.17, the owner's decision):* the Laserdot app shell (the window, overlay and menus) no longer applies the macOS sandbox profile to itself. macOS only lets a sandboxed app hand a file it can read itself to Preview or a player, so opening results in Documents, Pictures and the like required Full Disk Access. The shell keeps the no-network rule by construction: it has no network code, CI fails if network APIs appear anywhere in the app outside the update helper, and it talks to `laserdotd` over the Unix socket only. `laserdotd` — the process that reads every file — still denies itself the network through the OS before anything else, reports it (`network_denied`), and the privacy gate (15.5) still covers it. Full Disk Access is now optional: it spares per-folder prompts when indexing protected folders and is offered from the menu
- **7.4** Updates via signed, notarized builds, downloaded manually. Sparkle (or any in-app updater) is incompatible with 7.1 because it needs the network; a separate, user-launched updater is an open question
  - *Decided (v1.16, the owner's exception to 7.1):* a bundled helper, `Laserdot.app/Contents/Helpers/Laserdot Updater.app`, is the **only** part of Laserdot allowed on the network, and only to GitHub: `api.github.com` (the release API) and the storage host GitHub redirects release downloads to (redirects to any other host are refused). It runs only when Laserdot asks — **Check for Updates…**, or once a day while Laserdot runs — sends nothing but its requests, and exits. *Revised (v1.18, the owner's decision):* the repository and its releases are public, so the helper needs no token. It learns the newest version from where `github.com/<repo>/releases/latest` redirects (`…/releases/tag/v0.2.7`) and downloads `…/releases/download/v<version>/Laserdot-<version>.dmg`; the API (`releases/latest`) is only a fallback, because its anonymous limit (60 requests an hour per network address) is often used up on shared networks and answers HTTP 403 and the user never types anything; only the owner can push code or publish a release (outside changes arrive as pull requests the owner must merge; CI runs them with a read-only token and no secrets). The token flow of 0.2.0–0.2.2 is gone. It downloads the newest release's `Laserdot-<version>.dmg` to `~/Library/Caches/app.laserdot.Laserdot/Updates` and writes `update.json`; Laserdot, still network-blocked, checks the download (bundle id, newer version, `laserdotd` present; with a Developer ID build also the same team's signature and Gatekeeper) and on the user's go stops `laserdotd`, swaps the app atomically (into /Applications when the running copy is on a disk image or translocated) and relaunches. The helper is a separate app launched by macOS, not a child of Laserdot, so the app's and the daemon's OS network block are unchanged and the privacy gate (15.5) still holds for them; CI fails if network code appears anywhere in the app but the helper. Releases are published by the `Release` workflow for `v*` tags
- **7.5** Full Disk Access requested only if indexing outside home folder; scopes stated explicitly in-app
- **7.6** Published audit page: every path written, every process spawned
- **7.7** Thumbnails come from embedded images first (EXIF thumbnail, HEIC thumbnail item, MP4 `covr`, MKV attachments), falling back to a VideoToolbox-decoded frame; cached as small HEIC files in a size-capped LRU cache. The cache is stored in `~/Library/Caches/<bundle-id>`, excluded from Time Machine and from the app's own index (no recursive indexing of own artifacts)
- **7.8** Diagnostics export: user-triggered local log bundle (last N sessions, index stats, no file contents) for bug reports — the telemetry-free replacement for crash reporting
- **7.9** Removable-drive indexes (see 9.x) may contain sensitive filenames; encrypted index option (a sealed AES-256-GCM container, §9.8) offered per drive, and the exposure documented in the audit page

---

## 8. Search Input Hints

- **8.1** Live syntax hints while typing — one-line contextual tip under the query box:
  - Bare word → "add filters like `res:4k` or `kind:audio` to narrow"
  - `res:` → valid values: `720p 1080p 1440p 4k 8k`
  - `channels:` → `stereo 5.1 7.1 atmos`
  - Invalid regex (in `re:` mode) → error message with position highlighted plus fix suggestion
- **8.2** Autocomplete for filter keys and values (arrow keys, Tab to accept)
- **8.3** Zero-results coaching: three most likely causes — filter too narrow (one-click "relax this filter"), typo in filter value (suggested correction), folder not indexed (one-click "index this folder")
  - *As built (M8):* computed in the core from the index (`coach`), at most three, in this order:
    - **index this folder:** an `in:` path that exists on disk but is not inside an indexed folder;
    - **corrected value:** a text value that matches no indexed value (extensions, tags, media facets such as `artist`, `camera`, `codec`) gets the closest indexed value within edit distance max(2, ⌊len/4⌋), offered only if the corrected query finds something;
    - **relax:** removing each clause in turn, offering the two removals that find the most (filters before words on ties), each with its result count;
    - **show every filter:** fills any remaining slot.

    A query the parser rejects already carries its positioned fix (`res:4l` → `res:4k`), so coaching then only points to the cheat sheet. The CLI prints the same suggestions on zero results
- **8.4** Cheat sheet button — see 8.9. (Typing `?` does not open it: `?` is the single-character wildcard, 2.2.2)
- **8.5** Example queries as clickable chips on first launch and when the index is empty
- **8.6** Query history with recall (up/down arrows) and pinning frequent queries
  - *As built (M8):* the app keeps the last 50 queries that led to opening or revealing a result, in its own preferences on this Mac. ↑/↓ walk them from an empty box (or while already walking them); with results, the arrows move the selection. ⌘D (or the pin button) pins the current query; pinned queries are chips above the examples on an empty box, unpinned from their context menu
- **8.7** Hints teach the simple syntax — words, wildcards, filters — never regex itself
- **8.8** All guidance surfaces — hints (8.1), autocomplete (8.2), coaching (8.3), cheat sheet (8.9), and the CLI's `laserdot help syntax` — are generated from **one filter registry** in the Rust core (key, scope, value type, valid values, units, description, examples), the same registry the parser (2.7) uses. A filter cannot exist without its guidance, and guidance cannot go stale

### 8.9 Cheat sheet button

- **8.9.1** A persistent `?` button at the trailing end of the query field (mirrors to the left in RTL). Shortcut **⌘/**. Also opened by "Show syntax" in zero-results coaching (8.3)
- **8.9.2** Opens as a panel attached to the overlay; the query field keeps focus so the user can keep typing while it is open. Esc closes it and returns to the query
- **8.9.3** Sections, in order:
  1. **Basics** — words (any order), `"exact phrase"`, `-exclude`, `OR`
  2. **Wildcards** — `*` any characters, `?` one character, with the whole-name rule (2.2.2)
  3. **File** — `kind` `ext` `fsize` `modified` `created` `since` `before` `in` `local` `hidden` `tag`
  4. **Video** — `res` `codec` `hdr` `fps` `bit_depth` `duration` `bpp` `ar` `orientation`
  5. **Audio** — `channels` `atmos` `lossless` `lang` `acodec` `sample_rate`
  6. **Subtitles** — `s.lang` `s.forced` `s.format` `sidecar`
  7. **Images** — `camera` `lens` `taken` `megapixels` `iso` `gps` `raw` `live` `screenshot`
  8. **Music** — `artist` `album` `genre` `year`
  9. **Track groups** — `a(lang:ar channels:5.1)` explained in one line
  10. **Values** — size units, date forms (`2025-01-01`, `7d`, `today`), ranges (`1gb..4gb`), lists (`ext:mkv,mp4`), comparisons
  11. **Advanced** (collapsed by default) — `re:` mode and `probe.<field>:`
- **8.9.4** Every entry shows the key, a one-line description, valid values, and one example. Entries are **clickable**: clicking inserts the filter at the cursor (e.g. `res:`) and opens value autocomplete; clicking an example replaces the query with it
- **8.9.5** Live counts next to enumerated values (`res:4k · 1,204`), computed from the bitmap indexes (2.1.1d) at near-zero cost; values with zero matches are dimmed, not hidden
  - *As built (M8):* the core counts every value of every enumerated and true/false filter by running `key:value` over every index (bitmap work only, `value_counts`, daemon request `value_counts`). The app loads the counts when the cheat sheet opens and shows `4k · 1,204`, with zero-count values dimmed
- **8.9.6** Context-aware: the section for the token under the cursor is highlighted and scrolled into view (cursor in `chan…` → Audio)
  - *As built (M8):* `section_at(query, cursor)` in `laserdot-query`: the key of the token under the caret, a scope prefix (`a.` → Audio), or the first filter the token is a prefix of
- **8.9.7** Filterable: typing in the panel's own search box narrows entries by key or description
- **8.9.8** Fully keyboard-driven (arrows move, Return inserts, Tab switches sections), VoiceOver-labelled, localized in English and Arabic with RTL layout; filter keys themselves stay ASCII in both languages
  - *As built (M8):* while the cheat sheet is open the query field keeps focus and ↑/↓, Return, Tab and Esc drive the sheet. Filter descriptions, section titles, hint sentences and the CLI cheat sheet (`laserdot help-syntax --lang ar`) come in English and Arabic from `laserdot-query`'s `i18n` table; a test requires every registry filter to have an Arabic description. Keys, values and examples are laid out left-to-right inside the Arabic UI. Parser error messages are still English

---

## 9. Removable Drives

- **9.1** Per-drive index written to the drive itself (`/Volumes/<name>/.laserdot/index.db`) — drive carries its own index wherever it goes
  - *As built (M5):* next to it, `volume.json` holds the drive's identity (format, random id, name, created, encrypted). Paths inside a drive index are stored **relative to the mount point** (`.` is the root), so the same file works whatever the drive is mounted as and on any Mac. FSEvents ids are not stored in drive indexes (they belong to one Mac); on mount the drive is reconciled with a diff instead. A read-only drive cannot hold its index: it is kept on the Mac in `<data>/drives/<hash of name, file system, size>/` and the prompt says so. *Decided (v1.22, the owner, for confidentiality):* such an index is deleted as soon as its drive is no longer mounted — laserdotd checks at start, every minute, whenever a drive leaves, and when it quits (the CLI checks when it opens the index); a mounted or attached drive's index is kept, and nothing is deleted when the volume listing comes back empty while drives are attached. Plugging the drive in again means indexing it again
  - *As built (M5):* the system index never crosses into another volume: a scan does not descend into folders on a different device, the live updater skips mount points, and `laserdot index /Volumes/X/...` is refused with a pointer to the drive's own index (17.6.1)
- **9.2** On mount: detect existing index, verify schema version, use instantly; only probe files changed since last index
  - *As built (M5):* the daemon checks the volumes folder every 3 s (`--volume-interval`). A drive with an index is opened, verified and attached, then refreshed by diff, so only changed files are probed. A drive index is **untrusted input** (any drive can be plugged in): it is opened with SQLite's defensive mode and `trusted_schema=OFF`, its schema must match an exact whitelist (a trigger or view is refused), and every stored name is validated (no `/`, `.`, `..` or NUL) before it reaches the in-memory store
  - *As built (M5):* on FAT and exFAT, inode numbers are not stable across mounts, so they are not stored; moves are paired by (name, size, modified time) instead
- **9.3** First mount of unindexed drive: prompt, show estimated size/file count, confirm the index write before starting
  - *As built (M5):* the estimate is a Tier 0 walk (files, folders, bytes) and an index size of about 220 bytes per entry. The prompt offers Index, Not Now and Never for This Drive (remembered by drive name), and an Encrypt checkbox with a passphrase
- **9.4** On unmount: drive's index detaches cleanly; system index untouched
- **9.5** Index self-contained and portable — plug into another Mac running the app, search is instant
- **9.6** Hidden by default (dot-folder); in-app drive manager with "rebuild index" and "eject" per drive
  - *As built (M5):* the menu bar's Drives submenu lists mounted drives with their state (not indexed, N items, not searched, locked, unreadable) and per state: Index, Unlock, Search This Drive, Stop Searching (detach), Rebuild, Delete Index, Show in Finder and Eject (`diskutil eject`). The same actions are `laserdot drives [list|plan|index|unlock|detach|rebuild|delete|eject]` and daemon requests (protocol v2). Results from a drive carry its name, shown as a badge
- **9.7** Stale detection: unplugged mid-index → resume from queue checkpoint on next mount
  - *As built (M5):* the first index is written in chunks of 20,000 entries, each committed with its probes; the root stays marked incomplete until the last chunk. On the next mount an incomplete index is finished by diff, reusing every probe already written, and ends equal to a fresh index
- **9.8** Encrypted index option per drive (SQLCipher); plaintext exposure documented in audit page
  - *Go port (v1.15, replaces SQLCipher):* the core is pure Go (no cgo), so SQLCipher is not available. An encrypted drive index is a sealed container in place of `index.db`: AES-256-GCM in 1 MiB chunks, key from the passphrase with scrypt (N=2^15, r=8, p=1, random salt), each chunk's nonce and authenticated data binding its position and whether it is the last, so reordering, dropping or truncating chunks fails the open. On attach the container is decrypted into an owner-only working copy in this Mac's data folder (`drives/work-<id>.db`), which SQLite uses; it is sealed back to the drive after every write and on detach, and deleted on detach. A wrong passphrase fails authentication of the first chunk and never touches the file. Exposure is the same as before on the drive (only `volume.json` is plaintext); on this Mac the working copy exists while the drive is attached (inside the user's own data folder, mode 0600).
  - *As built (M5, Rust):* SQLCipher is compiled in (rusqlite `bundled-sqlcipher`; CommonCrypto on macOS, the system libcrypto on Linux — no networking or OpenSSL crate). The passphrase is never written to disk; the daemon holds it in memory for the session, so a drive unplugged and plugged back in reattaches without asking. After a restart the drive shows as locked until unlocked. A wrong passphrase leaves the index untouched. Unencrypted: the drive's `index.db` and `volume.json` (the drive's name and a random id) are plaintext; encrypted: only `volume.json` is

---

## 10. Result Presentation

- **10.1** Sort modes: relevance (default), date modified, date created, size, and for media: resolution, duration, bitrate, `taken` — toggleable, remembered per session; backed by pre-sorted orders (2.1.1f)
- **10.2** Grouping option: by kind (Videos / Audio / Images / Documents / Other)
- **10.3** Result row content per type: name, path, size, date; media rows add resolution, codec, channels, duration badges
- **10.4** Max results per query: 100 (fast path), with "show more" pagination
- **10.5** Highlighting of matched substrings in name and path
- **10.6** Saved searches / smart folders: pin queries like `res:4k lang:ar` as one-click entries in the overlay

---

## 11. iCloud and Synced Locations

- **11.1** Dataless/offloaded files detected (ubiquitous items not fully downloaded) and skipped from probing — never trigger silent downloads
- **11.2** Offloaded files still indexed by name/metadata; filterable with `local:true` / `local:false`
- **11.3** User warning before any action that would force-download an offloaded file

---

## 12. Performance and Battery Budgets

- **12.1** Cold launch to overlay interactive: under 300 ms (SwiftUI shell target ~63 MiB RSS)
- **12.2** Time-to-first-result: under 50 ms warm, under 200 ms cold query
- **12.3** Background indexing runs at `QOS_CLASS_BACKGROUND` (efficiency cores, throttled I/O) instead of a hard CPU percentage; a user-configurable duty-cycle limit remains available; paused on battery below 20% (configurable)
- **12.4** Low Power Mode awareness: indexing paused while active
- **12.5** Thermal state awareness: reduce indexing priority under thermal pressure
- **12.6** Indexing throughput targets (internal SSD): Tier 0 listing ≥100,000 files/s; Tier 1 headers ≥2,000 images/s and ≥300 videos/s. USB hard drive: I/O-bound, target ≥50 Tier 1 probes/s. Reference: full Tier 1 index of 1M images in under 10 minutes on SSD
- **12.7** Memory ceiling for background indexer: 150 MiB of dirty (non-file-backed) memory; the memory-mapped snapshot (2.1.1g) is file-backed page cache and is reported separately
  - *As built:* at 1M entries, a process serving the index from the snapshot holds 125 MiB dirty (Linux `RssAnon`, measured by `laserdot bench`). `laserdot stats` reports heap and mapped memory separately. Before the snapshot, the same index used about 240 MiB of heap

---

## 13. Technical Architecture

- **13.1** SwiftUI menu-bar shell (native footprint, ~63 MiB)
  - *As built (M4):* SwiftPM package in `app/`: a `MenuBarExtra` app (`LSUIElement`), a borderless non-activating overlay panel opened with ⌥Space (Carbon hot key, no Accessibility permission), query field with the live hint line and inline error + one-click fix, results with icon, path and media badges, keyboard actions (↑/↓, Return open, ⌘Return reveal, ⌘⇧C copy path, Tab completes, Esc closes), drag out, example chips on an empty box, the cheat-sheet button (⌘/) listing every filter from the registry, and a footer showing whether the network is blocked. UI strings in English and Arabic. CI builds it on macOS, runs XCTest through the real daemon, and uploads an unsigned `Laserdot.app`
- **13.2** Rust core (workspace layout in 13.9) exposed to Swift via UniFFI: indexer, FSEvents, SQLite FTS5, regex engine, in-memory hot store, Tier 1 media parsers, Tier 2 worker pool
  - *As built (M4):* crate `laserdot-ffi` with UniFFI 0.32 (proc-macro API). Swift sees an `Engine` (connect / start the daemon, search, index, status) and registry-driven functions: `hint`, `completions`, `filters`, `cheatSheet`, plus `denyNetwork`. The app links the static library; bindings are generated by `app/scripts/build-rust.sh`
- **13.2a** The indexer runs as a separate Rust background daemon (LaunchAgent) that applies the no-network sandbox profile (7.1) at startup and is the single writer to the index; the shell and CLI read the index
  - *As built (M4):* `laserdotd` (crate `laserdot-daemon`) serves newline-delimited JSON (protocol v1, crate `laserdot-ipc`: ping, status, search, index, forget, refresh, roots, stats, shutdown; v2 in M5 adds the drive requests of 9.6, v3 in M6 the duplicate scan and cleanup of 4, v4 in M7 content status, settings and update of 2.4, v5 in M8 coaching and value counts of 8) on a Unix socket next to the index (`laserdotd.sock`, owner-only permissions; if that path exceeds the 104-byte Unix limit, `/tmp/laserdot-<uid>/<hash>.sock` in an owner-only folder). The index sits behind a read–write lock: searches run concurrently, updates are exclusive, and the watcher waits for changes without holding it. One daemon per socket. The CLI routes through the daemon when it answers (`--local` opens the index read-only), and the app starts the bundled daemon when none is running; registering it as a LaunchAgent (`SMAppService`) is still to do
- **13.3** Bundled: LGPL ffmpeg libraries/binaries (Tier 2 and actions only), built without network protocols. No exiftool — EXIF is parsed in Rust
- **13.4** CLI sharing the same index and query syntax for scripting
- **13.5** URL scheme `laserdot://search?q=...` and Shortcuts actions (search, open result, start index)
- **13.6c** *The third critique (v1.24):* results can be sorted (name, size, date; reversed) — sorted results are one list, not sections; Library ▸ Storage lists a folder's entries by total size from the index alone (`storage` op; no disk reads) and the largest files; the bundled `ffmpeg`/`ffprobe` (Tier 2) are built in CI from FFmpeg's source, LGPL only, with `--disable-network` and only the `file` and `pipe` protocols (CI checks the protocol list), and run under laserdotd's sandbox; background work (contents, Tier 2) pauses on battery (a setting, on by default), when the Mac is hot or in Low Power Mode (`background` op, sent by the app); Content Search shows its size and can skip source code (`skip_code`); `laserdot://search?q=…`, `laserdot://storage`, `laserdot://duplicates`, `laserdot://settings` open Laserdot (for Shortcuts' Open URL); the overlay resizes from its edges and remembers its size; media previews load a player only on Play (selecting a video must never take the keyboard from the query)
- **13.6d** *The fourth critique (v1.25):* file operations from ⌘K (Move to Trash, Rename, Move to…, Compress) can be undone (a banner with Undo, ⌥⌘Z, Edit ▸ Undo): moved files return to where they were, a new zip goes to the Trash; nothing is ever deleted outright. Settings ▸ Privacy lists what is stored in the data folder (index, document words, read-only drive indexes, history) with sizes and the number of remembered passphrases, and **Erase All Laserdot Data** stops laserdotd for good (the engine is retired so nothing restarts it), deletes the data folder, the Keychain items and the settings, and relaunches with nothing indexed (no tour, no automatic indexing; drives keep their own indexes). Before an update replaces the app, the replaced version is copied to `~/Library/Application Support/Laserdot Previous Version/`; Settings ▸ General offers **Go Back to <version>**, after which that newer version is not installed again automatically (a newer one is; an interactive check clears this). The first-launch tour has a page on the macOS permission prompts, and the default folders are indexed only once it is closed. Content hits show a passage in the preview (`snippet` op: the line of the document holding the most query words, folded comparison, at most ~220 characters, read from the file at request time — nothing extra is stored). ⌘K ▸ Search in This Folder and dropping a folder on the window set the query to `in:"<folder>" `. A drive's passphrase can be remembered in the login Keychain (item "Laserdot drive passphrase", account = the drive's volume id, this-device-only); a locked drive with a remembered passphrase unlocks without asking, and a passphrase that fails is forgotten and asked for. Indexing writes entries 64 rows per INSERT with a 64 MiB SQLite cache (write time −45%).
- **13.6e** *The fifth critique (v1.26):* only the owner can change the repository (no collaborators, invitations or writable deploy keys; workflow tokens read-only by default; outside pull requests run workflows only after the owner approves; the default branch cannot be deleted or force-pushed) — enforced by the `access-lock` task of the owner's Repo admin workflow. Undo is a list: up to 20 steps, newest first, kept in the app's settings for 7 days (so it survives quitting); each ⌥⌘Z reverses the newest; a move is reversed only if the file is still where it went and nothing took its old place. ⌘K ▸ Tags… adds and removes Finder tags (the standard colours in the user's language, the selection's own tags, a new one) and is undoable. ↑ at the top of the empty box recalls earlier searches (the 50 kept), ↑ again goes further back, ↓ goes into the results. A full index records what it could not read (`unreadable` op: per indexed folder, the count and the first 200 paths with "permission" or "error"; kept in the index's settings, dropped with the folder); Settings ▸ Folders lists them with the Full Disk Access button. A full index builds the in-memory index from the entries it just wrote (other folders are read from the database), probing the next chunk while writing the current one, and lists folders with 4 readers per core. The `snippet` op waits at most 400 ms for a document's text, keeps reading in the background, and caches the text of the last 8 documents (16 MB) by path, size and date. The app creates the ⌥Space window and starts drives, content status and the duplicate probe just after launch; Save Diagnostics reports the launch time (to the menu bar, and until the index answered).
- **13.6f** *Permissions at once (v1.27):* before laserdotd starts, the app opens each macOS-protected place itself — Desktop, Documents, Downloads, iCloud Drive, and one removable and one network volume if mounted — one after another, so macOS's prompts come back to back (each call waits for its answer; places already answered return at once). laserdotd, started by the app, is covered by the same answers. On first launch this happens when the welcome tour's Start is pressed, then the default folders are indexed (§13.6a); on later launches at once, which also gathers the prompts macOS shows again after an update it sees as a new app. Refused places are named once, with Full Disk Access offered. Builds are signed with the repository's own key when the Actions secrets `LASERDOT_SIGN_P12`/`LASERDOT_SIGN_PASSWORD` exist (a self-signed certificate, "Laserdot Self-Signed"), so macOS keeps the answers across updates; otherwise ad hoc.
- **13.6b** *Folders and exclusions (v1.23, the owner's critique):* Settings ▸ Folders lists every indexed folder (add, remove) and the folders never indexed. An excluded folder is skipped by every walk and rescan, and excluding a folder removes what was indexed there (an indexed folder inside it is forgotten); the list lives in this Mac's index settings (`exclude`, `include`, `exclusions` ops). Words inside files are searched in the whole home folder by default (Documents, Desktop and Downloads before). Settings ▸ General ▸ Clear History forgets recent searches and the open history (`clear_opens`); pins stay. Laserdot opens at login by default (asked once, for the copy in Applications) so the index stays current after a restart
- **13.6a** *Default scope (v1.20, the owner's critique):* nothing to set up. The app indexes the apps and the home folder once, without asking (no first-run prompt). In `~/Library` only `Mobile Documents` (iCloud Drive) is indexed (`OnlyChildren`); the rest is app data and caches. Indexing runs on its own connection to the daemon, so searches are never held up
- **13.6** User-configurable index scopes and ignore patterns (node_modules, .venv, caches), with churn detection auto-excluding hot directories; auto-excluded directories can be permanently re-included
  - *Decided (v1.19, the owner's report that `claude.app` found nothing):* macOS packages are opaque like `.git`: a folder named `*.app`, `.appex`, `.framework`, `.bundle`, `.plugin`, `.kext`, `.xpc`, `.prefPane`, `.qlgenerator`, `.mdimporter`, `.saver`, `.photoslibrary` or `.musiclibrary` is one result (apps are `kind:app`; the type word `apps` finds them) and its contents are not indexed; contents indexed before this leave the index on the next refresh. The app indexes `/Applications`, `/System/Applications` and `~/Applications` once, without asking, since apps are what people look for first
  - Defaults never indexed: `node_modules`, `.venv`, `__pycache__`, `.cache`, `.Trash`, `.Trashes`, `.Spotlight-V100`, `.fseventsd`, `.DocumentRevisions-V100`, `.TemporaryItems`, `.laserdot`, and the index database itself
  - Version-control folders (`.git`, `.hg`, `.svn`) are listed as results but their contents are not indexed
- **13.7** Index schema versioned; app updates migrate in place; incompatible versions trigger rebuild with checkpoint resume
- **13.9** Rust workspace layout (Cargo workspace, `crates/`):

| Crate | Responsibility |
|---|---|
| `laserdot-fold` | Unicode folding (2.5): NFC, case folding, Latin + Arabic diacritics, Arabic letter variants, Arabic-Indic and Extended Arabic-Indic digits |
| `laserdot-query` | filter registry (8.8), parser for the 2.7 grammar, query AST, compilation of patterns to regexes via `regex-syntax` |
| `laserdot-media` | Tier 1 header parsers (3.1.2) producing the media record |
| `laserdot-scan` | Tier 0 directory walk and ignore rules (13.6) |
| `laserdot-index` | durable store (SQLite), in-memory hot store (2.1.1a–g), query planner and execution |
| `laserdot-core` | public Rust API — Seam 1 — used by the CLI and, through UniFFI, the SwiftUI shell |
| `laserdot-cli` | the `laserdot` command-line tool (13.4) |

  Platform-specific code (FSEvents, `getattrlistbulk`, `sandbox_init`, IOKit, `setiopolicy_np`) lives behind `cfg(target_os = "macos")` with portable fallbacks, so the core builds and its tests run on Linux CI
- **13.10** Build milestones (each is a vertical slice testable at Seam 1):
  - **M0** — fold + query crates: registry, grammar parser, regex compilation, fuzz and property tests
  - **M1** — scan + index + CLI: name search end to end, SQLite persistence, trigram prefilter, benchmarks
  - **M2** — FSEvents and file lifecycle (2.6), event-ID replay
  - **M3** — Tier 1 media parsers (images, MP4/MOV, MKV), media columns, bitmaps, media filters
  - *Status (2026-11-10):* M0–M8 are built and on `main`, plus the memory-mapped snapshot (2.1.1g) added afterwards, with CI passing on Linux and macOS (the README has the status table and the full list of what is missing). Not built yet:
    - planned gaps inside milestones: Tier 2 and the `probe.<field>:` escape hatch (3), loudness (story 20), per-volume inclusion toggles and browse views (17.6.3, 17.3.2), content search on drives (2.4), parser errors in Arabic (8.9.8);
    - sections outside M0–M8: result actions (5), ranking from open history and the fallback chain (6.2, 6.3), the audit page and diagnostics export (7.6, 7.8), media sort modes, grouping, highlighting and saved searches (10.1, 10.2, 10.5, 10.6), battery and thermal awareness (12.3–12.5), the URL scheme and Shortcuts (13.5);
    - distribution: signing, notarization and LaunchAgent registration (13.2a).
  - **M4** — daemon, no-network sandbox profile (7.1), UniFFI bindings, minimal SwiftUI overlay
  - **M5** — removable drives (9)
  - **M6** — duplicate finder (4)
  - **M7** — content search (2.4)
  - **M8** — hints, coaching, cheat sheet UI, localization polish (8, 14)
- **13.8** Non-sandboxed (App Sandbox), direct distribution — required for whole-disk indexing and the Full Disk Access UX; not App Store. Network isolation comes from the self-applied sandbox profile in 7.1, not from the App Sandbox

---

## 14. Accessibility and Localization

- **14.1** VoiceOver support for overlay, results, and actions
- **14.2** Full keyboard navigation throughout
- **14.3** Dark/light appearance following system; high-contrast support
- **14.4** RTL layout for Arabic UI strings; localization architecture from day one (strings externalized), English + Arabic at launch
  - *As built (M8):* every app string is in `Localizable.strings` (en, ar). Guidance from the Rust core follows the app's localization (`hint`, `completions`, `filters`, `cheat_sheet` take a language). VoiceOver labels are set on the query field, the pin and cheat-sheet buttons, result rows (name, kind, size, badges, "in contents", path), cheat-sheet entries and coaching. Untested: VoiceOver end to end and high-contrast appearance, which need a person at a Mac

---

## 15. Testing and Quality

- **15.1** Golden media corpus as fixture suite: rotated videos, HDR/Dolby Vision, DRM'd, VFR, multi-language audio, corrupt files, offloaded iCloud placeholders, iPhone HEIC grids, RAW formats, Live Photo pairs, `moov`-at-end MP4s. Most fixtures generated at test time with `ffmpeg -f lavfi`; only files that cannot be synthesised (DRM, Dolby Vision, camera RAW) are committed. Tier 1 parser output is checked field-by-field against libavformat on the whole corpus
- **15.2** Regex translator fuzz tests: every user input shape (wildcards, special chars, typos, Arabic, mixed scripts) must parse per the 2.7 grammar and produce valid regex, or a clean error with its position — never a crash
- **15.3** Benchmark suite on a pinned corpus (1M images + 50k videos), on both internal SSD and USB hard drive: Tier 0/1 throughput, query latency p50/p99 for name, media-filter, and combined queries, memory ceilings — run on every release
- **15.4** Lifecycle tests: rename/move/delete/rename-while-indexing, unplug mid-index, schema migration from prior versions
- **15.5** Privacy test (CI gate): verify each app process applies the 7.1 sandbox profile, run the binaries under a harness that fails on any outbound connection attempt (including from spawned ffmpeg/ffprobe), and run `cargo-deny`. Checking for a missing entitlement alone proves nothing for a non-sandboxed app

---

## 16. First-Run Experience

- **16.1** Permission walkthrough: Full Disk Access only if needed, hotkey setup with conflict check
- **16.2** Initial index: estimated time and file count shown, runs in background with progress
- **16.3** Example query chips surfaced immediately; the cheat sheet button (8.9) pulses once on first launch
- **16.4** Clean uninstall: remove app + offer to delete all indexes and caches

---

## 17. Spotlight Parity — Files, Media, and Disks Only

This app is a file, media, and disk search tool. It matches Spotlight for everything file-related and exceeds it for media and disks. Personal data sources (contacts, calendar, mail, messages, notes, photos, Safari history), System Settings search, and quick answers (math, conversions, dictionary) are deliberately out of scope.

### 17.1 File search parity (covered by sections 2–5)
- **17.1.1** Search by name, path, content, kind, extension, and Finder tags — everything Spotlight's file search does, deterministically (no silently omitted results)
- **17.1.2** Quick Look preview on Space for documents, images, PDFs, and videos (5.4)
- **17.1.3** Recently opened files surfaced as results (from the app's own open history)
- **17.1.4** Drill-down: Tab on a folder result → search within that scope; Esc returns (Spotlight Tahoe behavior)
- **17.1.5** Category toggles limited to file domains: Applications, Documents, Media, Archives, Other

### 17.2 File quick actions (all local, bundled binaries)
- **17.2.1** Rotate image, combine PDFs, compress, move to trash, batch rename
- **17.2.2** Media actions from 5.3: extract audio, remux, transcode, thumbnails/contact sheets

### 17.3 Disk management (integrates with section 9)
- **17.3.1** Eject disk action for every mounted volume, available inline from the drive manager and results
- **17.3.2** Per-volume views: browse a volume's contents through the same search interface, using that volume's own index
- **17.3.3** Disk overview in the drive manager: capacity, indexed state, index size, last scan time
- **17.3.4** Rebuild / detach / delete index per volume

### 17.4 What Spotlight does that stays excluded (by design)
- **17.4.1** Personal data source search (contacts, calendar, reminders, notes, messages, mail, photos, Safari history) — out of scope; this is a file/media/disk tool
- **17.4.2** Siri Suggestions, web suggestions, Apple intelligence results — require network
- **17.4.3** System Settings search, dictionary, unit conversions — out of scope
- **17.4.4** Web search happens only in the user's browser via the fallback (6.3); the app itself never connects

### 17.5 Advantages over Spotlight
- **17.5.1** Deterministic results — nothing silently omitted from the index
- **17.5.2** Media columns (resolution, channels, language, HDR, bitrate) Spotlight cannot filter on
- **17.5.3** Same search, filters, and actions on removable volumes via each volume's own portable index (section 9)
- **17.5.4** Duplicate-by-quality finder (section 4) — Spotlight has no equivalent

### 17.6 Removable volumes and privacy (extends section 9)
- **17.6.1** Removable-drive indexes include only files physically on that volume
- **17.6.2** No system-index data is ever written to removable-drive indexes
- **17.6.3** The system index can optionally exclude removable volumes entirely; per-volume inclusion toggles in the drive manager
- **17.6.4** Full parity on a mounted volume: same query syntax, filters, and actions; media probing runs identically against the volume's own index

---

## Appendix A — Out of Scope (v1)

- AI/semantic search of any kind (by design, permanent)
- Network features of any kind (by design, permanent)
- Personal data source search — contacts, calendar, reminders, notes, messages, Apple Mail, photos, Safari history — out of scope; this is a file/media/disk tool (Mail is additionally blocked by Apple's Core Spotlight access rules)
- Siri Suggestions, web suggestions, and any Apple online service results (require network)
- System Settings search, dictionary lookups, unit conversions, calculator
- Clipboard history, snippets, and general launcher utilities
- Windows/Linux support
- Cloud sync of settings or index
- Video content search (frame-level OCR)
- App Store distribution
