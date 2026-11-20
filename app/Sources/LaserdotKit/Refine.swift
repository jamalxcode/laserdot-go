import Foundation

/// One choice among a search's results ("1080p", 12 of them): choosing it
/// adds `token` to the query. `key`, when set, names words the app shows
/// instead of `label` ("With sound").
public struct RefineOption: Codable, Equatable, Hashable, Sendable {
    public let token: String
    public let label: String
    public let key: String?
    public let count: UInt64

    public init(token: String, label: String, key: String?, count: UInt64) {
        self.token = token
        self.label = label
        self.key = key
        self.count = count
    }
}

/// Choices about one attribute (`id`: resolution, sound, shape, length,
/// codec, hdr, camera, place, picture, artist, album, genre, type, modified,
/// size).
public struct RefineGroup: Codable, Equatable, Hashable, Sendable {
    public let id: String
    public let options: [RefineOption]

    public init(id: String, options: [RefineOption]) {
        self.id = id
        self.options = options
    }
}

extension Engine {
    /// The attributes `query`'s results have to choose from, with counts
    /// (empty when the daemon has no such request, or nothing narrows).
    public func refinements(query: String) -> [RefineGroup] {
        guard let answer = guide(.refine(query: query), as: [RefineGroup].self) else { return [] }
        return answer ?? []
    }
}
