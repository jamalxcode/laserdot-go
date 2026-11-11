import Foundation

/// What the Laserdot Updater helper last found (spec §7.4). The helper is
/// the only part of Laserdot allowed on the network (an exception the owner
/// granted for updates: api.github.com only); it writes this file, and the
/// app — which stays network-blocked — reads it and installs the download
/// offline.
public struct UpdateStatus: Codable, Equatable, Sendable {
    public enum State: String, Codable, Sendable {
        /// The newest release is not newer than the running app.
        case upToDate = "up_to_date"
        /// A newer release was downloaded to `file`.
        case downloaded
        /// No GitHub token yet (the helper asks for one when run on request).
        case needsToken = "needs_token" // written by updaters before 0.2.3
        case failed
    }

    public var state: State
    public var checkedAt: Date
    /// The newest release's version ("0.3.0").
    public var latest: String?
    /// The downloaded .dmg, when `state` is `downloaded`.
    public var file: String?
    public var message: String?

    public init(state: State, checkedAt: Date = Date(), latest: String? = nil, file: String? = nil, message: String? = nil) {
        self.state = state
        self.checkedAt = checkedAt
        self.latest = latest
        self.file = file
        self.message = message
    }

    /// ~/Library/Application Support/laserdot/update.json
    public static var defaultURL: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/laserdot/update.json")
    }

    /// Where downloads go: ~/Library/Caches/app.laserdot.Laserdot/Updates
    public static var downloadsFolder: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Caches/app.laserdot.Laserdot/Updates")
    }

    private static func coder() -> (JSONEncoder, JSONDecoder) {
        let e = JSONEncoder()
        e.dateEncodingStrategy = .iso8601
        e.outputFormatting = [.prettyPrinted, .sortedKeys]
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return (e, d)
    }

    public static func load(from url: URL = defaultURL) -> UpdateStatus? {
        guard let data = try? Data(contentsOf: url) else { return nil }
        return try? coder().1.decode(UpdateStatus.self, from: data)
    }

    public func save(to url: URL = defaultURL) throws {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Self.coder().0.encode(self).write(to: url, options: .atomic)
    }
}
