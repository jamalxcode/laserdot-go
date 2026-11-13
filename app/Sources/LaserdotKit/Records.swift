import Foundation

// MARK: records for the UI

public struct Hit: Hashable, Sendable {
    public var path: String
    public var name: String
    public var kind: String
    public var isDir: Bool
    public var size: UInt64
    /// Unix seconds.
    public var modified: Int64
    /// Birth time (Unix seconds; the modification time where unknown).
    public var created: Int64
    /// Last opened from Laserdot (Unix seconds; 0 never).
    public var opened: Int64
    /// Ready-to-show badges (spec §10.3): dimensions, video codec + HDR,
    /// first audio track, duration.
    public var badges: [String]
    public var typo: Bool
    /// The drive this result is on; `nil` for this Mac's index.
    public var volume: String?
    /// Found by its contents, not its name (spec §2.4.2).
    public var content: Bool

    init(_ h: Wire.Hit) {
        var badges: [String] = []
        if let w = h.width, let hh = h.height { badges.append("\(w)×\(hh)") }
        if let m = h.media {
            if let v = m.videoCodec { badges.append(([v] + m.hdr).joined(separator: " ")) }
            if let a = m.audio.first { badges.append(m.audio.count > 1 ? "\(a) +\(m.audio.count - 1)" : a) }
            if let d = m.durationMs, d >= 1000 {
                let s = d / 1000
                badges.append(
                    s >= 3600
                        ? String(format: "%llu:%02llu:%02llu", s / 3600, s / 60 % 60, s % 60)
                        : String(format: "%llu:%02llu", s / 60, s % 60))
            }
        }
        path = h.path
        name = h.name
        kind = h.kind
        isDir = h.isDir
        size = h.size
        modified = h.modified
        created = h.created
        opened = h.opened
        self.badges = badges
        typo = h.typo
        volume = h.volume
        content = h.content
    }
}

public struct Results: Sendable {
    public var total: UInt64
    public var hits: [Hit]
    public var elapsedMs: Double
    public var typoFallback: Bool
    /// Non-fatal notes, e.g. "`rez:` is not a filter".
    public var notes: [String]
}

public struct DaemonStatus: Sendable {
    public var running: Bool
    public var version: String
    public var protocolVersion: Int
    /// The daemon runs with network access denied by the OS (spec §7.1).
    public var networkDenied: Bool
    public var watcher: String
    public var entries: UInt64
    public var folders: [String]
    /// A folder being indexed now, if the daemon reports it.
    public var indexing: Wire.IndexProgress? = nil
    /// The watcher's hourly catch-up (newer daemons).
    public var catchUp: Wire.CatchUp? = nil

    static let down = DaemonStatus(
        running: false, version: "", protocolVersion: 0, networkDenied: false, watcher: "", entries: 0, folders: [])
}

/// A zero-results suggestion (spec §8.3).
public struct CoachingInfo: Hashable, Sendable {
    /// `index_folder`, `fix_value`, `relax` or `show_syntax`.
    public var kind: String
    /// The query to run instead (fix_value, relax).
    public var query: String?
    /// What was removed (relax) or replaced (fix_value).
    public var from: String?
    /// The replacement value (fix_value).
    public var to: String?
    /// The folder to index (index_folder).
    public var path: String?
    /// Results the new query finds.
    public var count: UInt64
}

/// A live count for the cheat sheet (spec §8.9.5).
public struct ValueCountInfo: Hashable, Sendable {
    public var key: String
    public var value: String
    public var count: UInt64
}

/// Content search state for the menu (spec §2.4).
public struct ContentInfo: Sendable {
    public var folders: [String]
    public var documents: UInt64
    public var skipped: UInt64
    public var pending: UInt64
    public var maxBytes: UInt64
    public var skipCode: Bool = false
    /// content.db's size on disk.
    public var bytes: UInt64 = 0
}

/// One file in a duplicate group (spec §4).
public struct DuplicateFile: Hashable, Sendable {
    public var path: String
    public var size: UInt64
    /// Unix seconds.
    public var modified: Int64
    /// The drive it is on; `nil` for this Mac.
    public var volume: String?
    /// `3840×2160 · HDR · 0.12 bpp · 5.1`
    public var summary: String
    /// The recommended copy to keep.
    public var keep: Bool
}

public struct DuplicateGroup: Sendable {
    /// `exact`, `similar_image` or `reencoded_video`.
    public var kind: String
    /// Best first.
    public var files: [DuplicateFile]
    public var reclaimable: UInt64
}

public struct DuplicateReport: Sendable {
    public var groups: [DuplicateGroup]
    public var reclaimable: UInt64
    public var filesChecked: UInt64
    public var filesRead: UInt64
    public var unreadable: UInt64
    /// `nil`: re-encoded videos could not be compared here.
    public var videoDecoder: String?
    public var elapsedMs: UInt64
}

/// A group as the user decided it: what stays, what goes.
public struct CleanupSet: Sendable {
    public var keep: [DuplicateFile]
    public var remove: [DuplicateFile]

    public init(keep: [DuplicateFile], remove: [DuplicateFile]) {
        self.keep = keep
        self.remove = remove
    }
}

/// The dry run shown before anything moves (spec §4.4).
public struct CleanupPreview: Sendable {
    public var files: Int
    public var bytes: UInt64
    public var skipped: [String]
}

public struct CleanupOutcome: Sendable {
    public var moved: Int
    public var bytes: UInt64
    public var skipped: [String]
    public var failed: [String]
    /// The files that were moved (the window drops them without a rescan).
    public var movedPaths: [String] = []
}

/// A mounted drive for the Drives menu (spec §9.6).
public struct Drive: Hashable, Sendable {
    /// The drive's identity (its index's volume id), once it has an index.
    public var id: String?
    public var name: String
    public var mount: String
    /// `unindexed`, `attached`, `detached`, `locked` or `unreadable`.
    public var state: String
    /// Why the index is unreadable, when it is.
    public var detail: String?
    public var readOnly: Bool
    public var encrypted: Bool
    /// The index is on the drive (false: kept on this Mac).
    public var onDrive: Bool
    public var complete: Bool
    public var entries: UInt64
    public var totalBytes: UInt64
    public var freeBytes: UInt64
    public var indexBytes: UInt64
}

/// What indexing a drive will take, for the prompt (spec §9.3).
public struct DrivePlan: Sendable {
    public var name: String
    public var files: UInt64
    public var folders: UInt64
    public var bytes: UInt64
    public var indexEstimateBytes: UInt64
    public var location: String
    public var onDrive: Bool
}
