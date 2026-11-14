import Foundation

/// Wire types for drives, duplicates and cleanup.
extension Wire {
    /// Adjacently tagged: `{"state":"unreadable","detail":"why"}`.
    public struct DriveState: Decodable, Sendable {
        /// `unindexed`, `attached`, `detached`, `locked` or `unreadable`.
        public var state: String
        public var detail: String?
    }

    /// `drives`, `drive_attach`.
    public struct DriveInfo: Decodable, Sendable {
        public var id: String?
        public var name: String
        public var mount: String
        public var fsType: String
        public var readOnly: Bool
        public var totalBytes: UInt64
        public var freeBytes: UInt64
        public var state: DriveState
        /// The index lives on the drive itself.
        public var onDrive: Bool
        public var indexPath: String?
        public var indexBytes: UInt64
        public var encrypted: Bool
        public var entries: UInt64
        public var complete: Bool
        public var lastScan: Int64?

        enum CodingKeys: String, CodingKey {
            case id, name, mount, fsType = "fs_type", readOnly = "read_only", totalBytes = "total_bytes",
                 freeBytes = "free_bytes", state, onDrive = "on_drive", indexPath = "index_path",
                 indexBytes = "index_bytes", encrypted, entries, complete, lastScan = "last_scan"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            id = try c.decodeIfPresent(String.self, forKey: .id)
            mount = try c.decode(String.self, forKey: .mount)
            name = try c.value(.name, or: (mount as NSString).lastPathComponent)
            fsType = try c.value(.fsType, or: "")
            readOnly = try c.value(.readOnly, or: false)
            totalBytes = try c.value(.totalBytes, or: 0)
            freeBytes = try c.value(.freeBytes, or: 0)
            state = try c.value(.state, or: DriveState(state: "unindexed", detail: nil))
            onDrive = try c.value(.onDrive, or: false)
            indexPath = try c.decodeIfPresent(String.self, forKey: .indexPath)
            indexBytes = try c.value(.indexBytes, or: 0)
            encrypted = try c.value(.encrypted, or: false)
            entries = try c.value(.entries, or: 0)
            complete = try c.value(.complete, or: false)
            lastScan = try c.decodeIfPresent(Int64.self, forKey: .lastScan)
        }
    }

    /// `drive_plan`: what indexing a drive would do.
    public struct DrivePlan: Decodable, Sendable {
        public var name: String
        public var mount: String
        public var files: UInt64
        public var folders: UInt64
        public var bytes: UInt64
        public var indexEstimateBytes: UInt64
        public var location: String
        public var onDrive: Bool

        enum CodingKeys: String, CodingKey {
            case name, mount, files, folders, bytes, indexEstimateBytes = "index_estimate_bytes", location,
                 onDrive = "on_drive"
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            name = try c.value(.name, or: "")
            mount = try c.value(.mount, or: "")
            files = try c.value(.files, or: 0)
            folders = try c.value(.folders, or: 0)
            bytes = try c.value(.bytes, or: 0)
            indexEstimateBytes = try c.value(.indexEstimateBytes, or: 0)
            location = try c.value(.location, or: "")
            onDrive = try c.value(.onDrive, or: false)
        }
    }

    /// `dupes` options; defaults as in laserdot-core.
    public struct DupeOptions: Codable, Sendable {
        /// Only files matching this query (empty: every indexed file).
        public var scope = ""
        public var minSize: UInt64 = 1
        public var exact = true
        public var images = true
        public var videos = true
        public var imageDistance: UInt32 = 7
        public var videoDistance: UInt32 = 12
        public var durationToleranceMs: UInt64 = 2000

        public init(scope: String = "", images: Bool = true, videos: Bool = true) {
            self.scope = scope
            self.images = images
            self.videos = videos
        }

        enum CodingKeys: String, CodingKey {
            case scope, minSize = "min_size", exact, images, videos, imageDistance = "image_distance",
                 videoDistance = "video_distance", durationToleranceMs = "duration_tolerance_ms"
        }
    }

    public struct DupeMember: Decodable, Sendable {
        public var path: String
        public var size: UInt64
        /// Unix seconds.
        public var modified: Int64
        public var volume: String?
        public var width: UInt32?
        public var height: UInt32?
        public var hdr: Bool
        public var bpp: Double?
        public var channels: UInt32?
        public var bitrate: UInt64?
        public var durationMs: UInt64?
        /// The recommended copy to keep (one per group).
        public var keep: Bool
        /// `3840×2160 · HDR · 0.12 bpp · 5.1`
        public var summary: String

        enum CodingKeys: String, CodingKey {
            case path, size, modified, volume, width, height, hdr, bpp, channels, bitrate,
                 durationMs = "duration_ms", keep, summary
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            path = try c.decode(String.self, forKey: .path)
            size = try c.value(.size, or: 0)
            modified = try c.value(.modified, or: 0)
            volume = try c.decodeIfPresent(String.self, forKey: .volume)
            width = try c.decodeIfPresent(UInt32.self, forKey: .width)
            height = try c.decodeIfPresent(UInt32.self, forKey: .height)
            hdr = try c.value(.hdr, or: false)
            bpp = try c.decodeIfPresent(Double.self, forKey: .bpp)
            channels = try c.decodeIfPresent(UInt32.self, forKey: .channels)
            bitrate = try c.decodeIfPresent(UInt64.self, forKey: .bitrate)
            durationMs = try c.decodeIfPresent(UInt64.self, forKey: .durationMs)
            keep = try c.value(.keep, or: false)
            summary = try c.value(.summary, or: "")
        }
    }

    public struct DupeGroup: Decodable, Sendable {
        /// `exact`, `similar_image` or `reencoded_video`.
        public var kind: String
        /// Best first.
        public var members: [DupeMember]
        public var reclaimable: UInt64

        enum CodingKeys: String, CodingKey { case kind, members, reclaimable }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            kind = try c.value(.kind, or: "exact")
            members = try c.value(.members, or: [])
            reclaimable = try c.value(.reclaimable, or: 0)
        }
    }

    public struct DupeReport: Decodable, Sendable {
        public var groups: [DupeGroup]
        public var reclaimable: UInt64
        public var files: UInt64
        public var read: UInt64
        public var bytesRead: UInt64
        public var unreadable: UInt64
        /// `nil`: re-encoded videos are not detected on this machine.
        public var videoDecoder: String?
        public var ms: UInt64
        /// Stopped early: the groups are what it had found (newer daemons).
        public var stopped: Bool

        enum CodingKeys: String, CodingKey {
            case groups, reclaimable, files, read, bytesRead = "bytes_read", unreadable,
                 videoDecoder = "video_decoder", ms, stopped
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            groups = try c.value(.groups, or: [])
            reclaimable = try c.value(.reclaimable, or: 0)
            files = try c.value(.files, or: 0)
            read = try c.value(.read, or: 0)
            bytesRead = try c.value(.bytesRead, or: 0)
            unreadable = try c.value(.unreadable, or: 0)
            videoDecoder = try c.decodeIfPresent(String.self, forKey: .videoDecoder)
            ms = try c.value(.ms, or: 0)
            stopped = try c.value(.stopped, or: false)
        }
    }

    /// A file as the duplicate scan saw it.
    public struct CleanupItem: Codable, Sendable {
        public var path: String
        public var size: UInt64
        /// Unix seconds.
        public var modified: Int64

        public init(path: String, size: UInt64, modified: Int64) {
            self.path = path
            self.size = size
            self.modified = modified
        }
    }

    public struct CleanupGroup: Codable, Sendable {
        /// At least one; never touched.
        public var keep: [CleanupItem]
        public var remove: [CleanupItem]

        public init(keep: [CleanupItem], remove: [CleanupItem]) {
            self.keep = keep
            self.remove = remove
        }
    }

    /// Adjacently tagged: `{"to":"trash"}` or `{"to":"folder","folder":"/x"}`.
    public enum CleanupDest: Codable, Sendable {
        case trash
        case folder(String)

        enum CodingKeys: String, CodingKey { case to, folder }

        public func encode(to encoder: Encoder) throws {
            var c = encoder.container(keyedBy: CodingKeys.self)
            switch self {
            case .trash:
                try c.encode("trash", forKey: .to)
            case let .folder(path):
                try c.encode("folder", forKey: .to)
                try c.encode(path, forKey: .folder)
            }
        }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            if try c.decode(String.self, forKey: .to) == "folder" {
                self = .folder(try c.decode(String.self, forKey: .folder))
            } else {
                self = .trash
            }
        }
    }

    public struct CleanupRequest: Codable, Sendable {
        public var groups: [CleanupGroup]
        public var dest: CleanupDest

        public init(groups: [CleanupGroup], dest: CleanupDest) {
            self.groups = groups
            self.dest = dest
        }
    }

    public struct PlannedMove: Decodable, Sendable {
        public var from: String
        /// The folder destination; `nil` for the Trash.
        public var to: String?
        public var size: UInt64
    }

    /// `cleanup_plan`: the dry run.
    public struct CleanupPlan: Decodable, Sendable {
        public var moves: [PlannedMove]
        public var bytes: UInt64
        /// Files or groups left alone, and why.
        public var skipped: [String]

        enum CodingKeys: String, CodingKey { case moves, bytes, skipped }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            moves = try c.value(.moves, or: [])
            bytes = try c.value(.bytes, or: 0)
            skipped = try c.value(.skipped, or: [])
        }
    }

    /// `cleanup`.
    public struct CleanupResult: Decodable, Sendable {
        public var moved: [PlannedMove]
        public var bytes: UInt64
        public var skipped: [String]
        public var failed: [String]

        enum CodingKeys: String, CodingKey { case moved, bytes, skipped, failed }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            moved = try c.value(.moved, or: [])
            bytes = try c.value(.bytes, or: 0)
            skipped = try c.value(.skipped, or: [])
            failed = try c.value(.failed, or: [])
        }
    }
}
