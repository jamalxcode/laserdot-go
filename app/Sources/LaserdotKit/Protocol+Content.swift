import Foundation

/// Wire types for content search, storage, unreadable folders and guidance.
extension Wire {
    /// Which files get content indexing (spec §2.4.3).
    public struct ContentSettings: Codable, Sendable, Equatable {
        public var roots: [String]
        /// Larger files are not read.
        public var maxBytes: UInt64
        /// Source code is left out of content search.
        public var skipCode: Bool

        public init(roots: [String], maxBytes: UInt64, skipCode: Bool = false) {
            self.roots = roots
            self.maxBytes = maxBytes
            self.skipCode = skipCode
        }

        /// The home folder; 20 MB (as the Go daemon's default, v1.23).
        public static var defaultForHome: ContentSettings {
            ContentSettings(roots: [NSHomeDirectory()], maxBytes: 20_000_000)
        }

        enum CodingKeys: String, CodingKey { case roots, maxBytes = "max_bytes", skipCode = "skip_code" }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            roots = try c.value(.roots, or: [])
            maxBytes = try c.value(.maxBytes, or: 20_000_000)
            skipCode = try c.value(.skipCode, or: false)
        }

        /// skip_code only when set, so older daemons see what they know.
        public func encode(to encoder: Encoder) throws {
            var c = encoder.container(keyedBy: CodingKeys.self)
            try c.encode(roots, forKey: .roots)
            try c.encode(maxBytes, forKey: .maxBytes)
            if skipCode { try c.encode(true, forKey: .skipCode) }
        }
    }

    /// `content_status`, `set_content_settings`.
    public struct ContentStatus: Decodable, Sendable {
        /// `nil`: never configured (the defaults apply).
        public var settings: ContentSettings?
        public var documents: UInt64
        public var skipped: UInt64
        /// Files waiting to be (re)indexed.
        public var pending: UInt64
        /// content.db's size on disk.
        public var bytes: UInt64

        enum CodingKeys: String, CodingKey { case settings, documents, skipped, pending, bytes }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            settings = try c.decodeIfPresent(ContentSettings.self, forKey: .settings)
            documents = try c.value(.documents, or: 0)
            skipped = try c.value(.skipped, or: 0)
            pending = try c.value(.pending, or: 0)
            bytes = try c.value(.bytes, or: 0)
        }
    }

    /// `unreadable`: entries the last full index could not read.
    public struct Unreadable: Decodable, Sendable {
        public var total: Int
        public var items: [UnreadableItem]
    }

    public struct UnreadableItem: Decodable, Sendable, Hashable {
        public var path: String
        /// "permission" or "error".
        public var reason: String
        public var detail: String
    }

    /// `storage`: a folder's entries by total size.
    public struct StorageItem: Decodable, Sendable, Hashable {
        public var path: String
        public var name: String
        public var isDir: Bool
        public var bytes: UInt64
        public var files: UInt64

        enum CodingKeys: String, CodingKey { case path, name, isDir = "is_dir", bytes, files }
    }

    public struct StorageReport: Decodable, Sendable {
        public var path: String
        public var bytes: UInt64
        public var files: UInt64
        public var items: [StorageItem]

        enum CodingKeys: String, CodingKey { case path, bytes, files, items }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            path = try c.value(.path, or: "")
            bytes = try c.value(.bytes, or: 0)
            files = try c.value(.files, or: 0)
            items = try c.value(.items, or: [])
        }
    }

    /// `update_content`.
    public struct ContentStats: Decodable, Sendable {
        public var indexed: UInt64
        public var skipped: UInt64
        public var removed: UInt64
        public var bytesRead: UInt64
        public var ms: UInt64

        enum CodingKeys: String, CodingKey { case indexed, skipped, removed, bytesRead = "bytes_read", ms }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            indexed = try c.value(.indexed, or: 0)
            skipped = try c.value(.skipped, or: 0)
            removed = try c.value(.removed, or: 0)
            bytesRead = try c.value(.bytesRead, or: 0)
            ms = try c.value(.ms, or: 0)
        }
    }

    /// Internally tagged by `kind`: `index_folder`, `fix_value`, `relax`,
    /// `show_syntax`.
    public struct Coaching: Decodable, Sendable {
        public var kind: String
        /// index_folder
        public var path: String?
        /// fix_value
        public var from: String?
        public var to: String?
        /// relax
        public var removed: String?
        /// fix_value, relax
        public var query: String?
        public var count: UInt64

        enum CodingKeys: String, CodingKey { case kind, path, from, to, removed, query, count }

        public init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            kind = try c.decode(String.self, forKey: .kind)
            path = try c.decodeIfPresent(String.self, forKey: .path)
            from = try c.decodeIfPresent(String.self, forKey: .from)
            to = try c.decodeIfPresent(String.self, forKey: .to)
            removed = try c.decodeIfPresent(String.self, forKey: .removed)
            query = try c.decodeIfPresent(String.self, forKey: .query)
            count = try c.value(.count, or: 0)
        }
    }

    /// `value_counts`: live count for one enumerated filter value.
    public struct ValueCount: Decodable, Sendable {
        public var key: String
        public var value: String
        public var count: UInt64
    }
}
