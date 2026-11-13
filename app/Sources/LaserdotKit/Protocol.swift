import Foundation

// The daemon protocol (spec §13.2a): one JSON object per line over the Unix
// socket next to the index. These types mirror the Rust `Request` enum and
// the result structs of laserdot-core field for field (snake_case on the
// wire); the Go laserdotd speaks the same format. Decoding is lenient where
// the daemons differ harmlessly: a missing or `null` list decodes as empty.

/// One call to laserdotd. Encoded as `{"op": "<snake_case>", ...fields}`.
public enum DaemonRequest: Encodable, Sendable {
    case ping
    case status
    case search(query: String, sort: SortKey = .relevance, reverse: Bool = false, offset: Int = 0, limit: Int = 100, typo: Bool = true)
    case index(path: String)
    case forget(path: String)
    case refresh
    case roots
    case stats
    case shutdown
    /// Mounted drives and their index state (spec §9.6).
    case drives
    /// Estimate a drive's index without writing (spec §9.3).
    case drivePlan(mount: String)
    case driveIndex(mount: String, passphrase: String?)
    case driveAttach(mount: String, passphrase: String?)
    case driveDetach(mount: String)
    case driveRebuild(mount: String, passphrase: String?)
    case driveDelete(mount: String)
    case driveEject(mount: String)
    /// Find duplicates (spec §4).
    case dupes(options: Wire.DupeOptions)
    /// Dry run of a cleanup (spec §4.4).
    case cleanupPlan(request: Wire.CleanupRequest)
    case cleanup(request: Wire.CleanupRequest)
    /// Content search state (spec §2.4).
    case contentStatus
    case setContentSettings(settings: Wire.ContentSettings)
    case updateContent
    /// Suggestions for a query that found nothing (spec §8.3).
    case coach(query: String)
    /// Live counts for the cheat sheet (spec §8.9.5).
    case valueCounts
    /// The hint line for the caret at `cursor` (Unicode scalars) — string or null (spec §8.1).
    case hint(query: String, cursor: Int, lang: String)
    /// The cheat-sheet section id under the caret — string or null (spec §8.9.6).
    case sectionAt(query: String, cursor: Int)
    /// Autocomplete for the token under the caret (spec §8.2).
    case completions(query: String, cursor: Int, lang: String)
    /// Every filter, for the cheat sheet (spec §8.9).
    case filters(lang: String)
    /// The plain-text cheat sheet.
    case cheatSheet(lang: String)
    /// The user opened a result: it ranks higher next time (spec §6.2).
    case opened(path: String)
    /// The results opened most recently, newest first.
    case recentOpens(limit: Int)
    /// Forget the open history.
    case clearOpens
    /// A folder's entries by size ("" = the indexed folders).
    case storage(path: String, limit: Int)
    /// Pause or resume background work (battery, heat, Low Power Mode).
    case background(paused: Bool)
    /// The passage of a document where the query's words were found.
    case snippet(path: String, query: String)
    /// What the last full index of each folder could not read.
    case unreadable
    /// Folders never indexed: list, add, remove.
    case exclusions
    case exclude(path: String)
    case include(path: String)

    /// The `op` tag.
    public var op: String {
        switch self {
        case .ping: return "ping"
        case .status: return "status"
        case .search: return "search"
        case .index: return "index"
        case .forget: return "forget"
        case .opened: return "opened"
        case .recentOpens: return "recent_opens"
        case .clearOpens: return "clear_opens"
        case .storage: return "storage"
        case .background: return "background"
        case .snippet: return "snippet"
        case .unreadable: return "unreadable"
        case .exclusions: return "exclusions"
        case .exclude: return "exclude"
        case .include: return "include"
        case .refresh: return "refresh"
        case .roots: return "roots"
        case .stats: return "stats"
        case .shutdown: return "shutdown"
        case .drives: return "drives"
        case .drivePlan: return "drive_plan"
        case .driveIndex: return "drive_index"
        case .driveAttach: return "drive_attach"
        case .driveDetach: return "drive_detach"
        case .driveRebuild: return "drive_rebuild"
        case .driveDelete: return "drive_delete"
        case .driveEject: return "drive_eject"
        case .dupes: return "dupes"
        case .cleanupPlan: return "cleanup_plan"
        case .cleanup: return "cleanup"
        case .contentStatus: return "content_status"
        case .setContentSettings: return "set_content_settings"
        case .updateContent: return "update_content"
        case .coach: return "coach"
        case .valueCounts: return "value_counts"
        case .hint: return "hint"
        case .sectionAt: return "section_at"
        case .completions: return "completions"
        case .filters: return "filters"
        case .cheatSheet: return "cheat_sheet"
        }
    }

    private enum Key: String, CodingKey {
        case op, query, sort, reverse, offset, limit, typo, path, mount, passphrase, options, request, settings, cursor, lang, paused
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: Key.self)
        try c.encode(op, forKey: .op)
        switch self {
        case let .search(query, sort, reverse, offset, limit, typo):
            try c.encode(query, forKey: .query)
            try c.encode(sort, forKey: .sort)
            try c.encode(reverse, forKey: .reverse)
            try c.encode(offset, forKey: .offset)
            try c.encode(limit, forKey: .limit)
            try c.encode(typo, forKey: .typo)
        case let .index(path), let .forget(path), let .opened(path), let .exclude(path), let .include(path):
            try c.encode(path, forKey: .path)
        case let .drivePlan(mount), let .driveDetach(mount), let .driveDelete(mount), let .driveEject(mount):
            try c.encode(mount, forKey: .mount)
        case let .driveIndex(mount, passphrase), let .driveAttach(mount, passphrase), let .driveRebuild(mount, passphrase):
            try c.encode(mount, forKey: .mount)
            try c.encodeIfPresent(passphrase, forKey: .passphrase)
        case let .dupes(options):
            try c.encode(options, forKey: .options)
        case let .cleanupPlan(request), let .cleanup(request):
            try c.encode(request, forKey: .request)
        case let .setContentSettings(settings):
            try c.encode(settings, forKey: .settings)
        case let .coach(query):
            try c.encode(query, forKey: .query)
        case let .hint(query, cursor, lang), let .completions(query, cursor, lang):
            try c.encode(query, forKey: .query)
            try c.encode(cursor, forKey: .cursor)
            try c.encode(lang, forKey: .lang)
        case let .sectionAt(query, cursor):
            try c.encode(query, forKey: .query)
            try c.encode(cursor, forKey: .cursor)
        case let .filters(lang), let .cheatSheet(lang):
            try c.encode(lang, forKey: .lang)
        case let .recentOpens(limit):
            try c.encode(limit, forKey: .limit)
        case let .storage(path, limit):
            try c.encode(path, forKey: .path)
            try c.encode(limit, forKey: .limit)
        case let .background(paused):
            try c.encode(paused, forKey: .paused)
        case let .snippet(path, query):
            try c.encode(path, forKey: .path)
            try c.encode(query, forKey: .query)
        case .ping, .status, .refresh, .roots, .stats, .shutdown, .drives, .contentStatus, .updateContent, .valueCounts,
             .clearOpens, .exclusions, .unreadable:
            break
        }
    }
}

/// Result order for `search`.
public enum SortKey: String, Codable, Sendable, CaseIterable {
    case relevance, name, size, modified, created, kind, opened
}

/// Character range (Unicode scalars) in the query as typed.
public struct Span: Codable, Equatable, Sendable {
    public var start: Int
    public var end: Int
}

/// A failed request (`{"ok":false,"error":{...}}`). Query errors keep their
/// position and suggestion so the UI can underline the exact spot (§8.1).
public struct RemoteError: Codable, Equatable, Sendable, Error {
    public var message: String
    public var span: Span?
    public var suggestion: String?
    /// Machine-readable kind: `unsupported`, `drive_locked`, `bad_request`…
    public var code: String?
}

extension KeyedDecodingContainer {
    /// The value, or `fallback` when the key is missing or `null`.
    func value<T: Decodable>(_ key: Key, or fallback: T) throws -> T {
        try decodeIfPresent(T.self, forKey: key) ?? fallback
    }
}

/// The result types, as laserdot-core serializes them.
public enum Wire {
    /// `status`: what the daemon says about itself (also the privacy audit).
    public struct Status: Decodable, Sendable {
        public var protocolVersion: Int
        public var version: String
        public var pid: Int
        /// The OS refuses the daemon network access (spec §7.1).
        public var networkDenied: Bool
        /// FsEvents, Inotify, Poll or None.
        public var watcher: String
        public var db: String
        public var entries: UInt64
        /// A folder being indexed now (newer daemons).
        public var indexing: IndexProgress?
        /// The watcher's hourly comparison with the disk (newer daemons).
        public var catchUp: CatchUp?

        enum CodingKeys: String, CodingKey {
            case protocolVersion = "protocol", version, pid, networkDenied = "network_denied", watcher, db, entries, indexing
            case catchUp = "catch_up"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            protocolVersion = try c.value(.protocolVersion, or: 0)
            version = try c.value(.version, or: "")
            pid = try c.value(.pid, or: 0)
            networkDenied = try c.value(.networkDenied, or: false)
            watcher = try c.value(.watcher, or: "")
            db = try c.value(.db, or: "")
            entries = try c.value(.entries, or: 0)
            indexing = try c.decodeIfPresent(IndexProgress.self, forKey: .indexing)
            catchUp = try c.decodeIfPresent(CatchUp.self, forKey: .catchUp)
        }
    }

    /// `status.catch_up`: when the indexed folders were last compared with
    /// the disk, and what the events had missed.
    public struct CatchUp: Decodable, Sendable, Equatable {
        public var lastCatchUp: Int64
        public var missed: UInt64
        public var restarts: UInt64

        enum CodingKeys: String, CodingKey {
            case lastCatchUp = "last_catch_up", missed, restarts
        }
    }

    /// `status.indexing`: phase "scanning" (total unknown) or "saving".
    public struct IndexProgress: Decodable, Sendable, Equatable {
        public var root: String
        public var phase: String
        public var done: UInt64
        public var total: UInt64
    }

    /// Compact media facts for a result row (spec §10.3).
    public struct MediaSummary: Decodable, Sendable {
        public var durationMs: UInt64?
        public var bitrate: UInt64?
        public var fps: Double?
        public var videoCodec: String?
        public var hdr: [String]
        /// One entry per audio track: `ac3 5.1 ar`.
        public var audio: [String]
        public var subtitles: [String]

        enum CodingKeys: String, CodingKey {
            case durationMs = "duration_ms", bitrate, fps, videoCodec = "video_codec", hdr, audio, subtitles
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            durationMs = try c.decodeIfPresent(UInt64.self, forKey: .durationMs)
            bitrate = try c.decodeIfPresent(UInt64.self, forKey: .bitrate)
            fps = try c.decodeIfPresent(Double.self, forKey: .fps)
            videoCodec = try c.decodeIfPresent(String.self, forKey: .videoCodec)
            hdr = try c.value(.hdr, or: [])
            audio = try c.value(.audio, or: [])
            subtitles = try c.value(.subtitles, or: [])
        }
    }

    public struct Hit: Decodable, Sendable {
        public var path: String
        public var name: String
        public var kind: String
        public var isDir: Bool
        public var size: UInt64
        /// Unix seconds.
        public var modified: Int64
        public var created: Int64
        public var width: UInt32?
        public var height: UInt32?
        /// Matched only through a typo alternative.
        public var typo: Bool
        public var media: MediaSummary?
        /// The removable drive it is on (`nil` = this Mac's own index).
        public var volume: String?
        /// Matched by its contents, not its name (spec §2.4.2).
        public var content: Bool
        /// Last opened from Laserdot (Unix seconds; 0 never).
        public var opened: Int64

        enum CodingKeys: String, CodingKey {
            case path, name, kind, isDir = "is_dir", size, modified, created, width, height, typo, media, volume, content
            case opened
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            path = try c.decode(String.self, forKey: .path)
            name = try c.value(.name, or: (path as NSString).lastPathComponent)
            kind = try c.value(.kind, or: "other")
            isDir = try c.value(.isDir, or: false)
            size = try c.value(.size, or: 0)
            modified = try c.value(.modified, or: 0)
            created = try c.value(.created, or: 0)
            width = try c.decodeIfPresent(UInt32.self, forKey: .width)
            height = try c.decodeIfPresent(UInt32.self, forKey: .height)
            typo = try c.value(.typo, or: false)
            media = try c.decodeIfPresent(MediaSummary.self, forKey: .media)
            volume = try c.decodeIfPresent(String.self, forKey: .volume)
            content = try c.value(.content, or: false)
            opened = try c.value(.opened, or: 0)
        }
    }

    /// A non-fatal note, e.g. an unknown filter key treated as a word.
    public struct Warning: Decodable, Sendable {
        public var message: String
        public var span: Span?
        public var suggestion: String?
    }

    public struct SearchResults: Decodable, Sendable {
        /// Total matches (all pages).
        public var total: UInt64
        public var hits: [Hit]
        public var typoFallback: Bool
        public var warnings: [Warning]
        public var elapsedUs: UInt64

        enum CodingKeys: String, CodingKey {
            case total, hits, typoFallback = "typo_fallback", warnings, elapsedUs = "elapsed_us"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            total = try c.value(.total, or: 0)
            hits = try c.value(.hits, or: [])
            typoFallback = try c.value(.typoFallback, or: false)
            warnings = try c.value(.warnings, or: [])
            elapsedUs = try c.value(.elapsedUs, or: 0)
        }
    }

    /// `index`, `drive_index`, `drive_rebuild`.
    public struct IndexStats: Decodable, Sendable {
        public var root: String
        public var files: UInt64
        public var folders: UInt64
        public var probed: UInt64
        public var scanErrors: UInt64
        /// A refresh of an indexed folder rather than a first index.
        public var incremental: Bool
        public var added: UInt64
        public var removed: UInt64
        public var renamed: UInt64
        public var modified: UInt64

        enum CodingKeys: String, CodingKey {
            case root, files, folders, probed, scanErrors = "scan_errors", incremental, added, removed, renamed, modified
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            root = try c.value(.root, or: "")
            files = try c.value(.files, or: 0)
            folders = try c.value(.folders, or: 0)
            probed = try c.value(.probed, or: 0)
            scanErrors = try c.value(.scanErrors, or: 0)
            incremental = try c.value(.incremental, or: false)
            added = try c.value(.added, or: 0)
            removed = try c.value(.removed, or: 0)
            renamed = try c.value(.renamed, or: 0)
            modified = try c.value(.modified, or: 0)
        }
    }

    /// `refresh`.
    public struct UpdateStats: Decodable, Sendable {
        public var added: UInt64
        public var removed: UInt64
        public var renamed: UInt64
        public var modified: UInt64
        public var ms: UInt64

        enum CodingKeys: String, CodingKey { case added, removed, renamed, modified, ms }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            added = try c.value(.added, or: 0)
            removed = try c.value(.removed, or: 0)
            renamed = try c.value(.renamed, or: 0)
            modified = try c.value(.modified, or: 0)
            ms = try c.value(.ms, or: 0)
        }
    }

    /// `roots`: one indexed folder.
    public struct RootInfo: Decodable, Sendable {
        public var path: String
        public var offline: Bool
        /// The first full index finished.
        public var complete: Bool

        enum CodingKeys: String, CodingKey { case path, offline, complete }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            path = try c.decode(String.self, forKey: .path)
            offline = try c.value(.offline, or: false)
            complete = try c.value(.complete, or: true)
        }
    }

    /// `stats`.
    public struct Stats: Decodable, Sendable {
        public var entries: UInt64
        public var roots: [String]
        public var memoryBytes: UInt64
        public var loadMs: UInt64
        public var drives: [String]

        enum CodingKeys: String, CodingKey {
            case entries, roots, memoryBytes = "memory_bytes", loadMs = "load_ms", drives
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            entries = try c.value(.entries, or: 0)
            roots = try c.value(.roots, or: [])
            memoryBytes = try c.value(.memoryBytes, or: 0)
            loadMs = try c.value(.loadMs, or: 0)
            drives = try c.value(.drives, or: [])
        }
    }
}
