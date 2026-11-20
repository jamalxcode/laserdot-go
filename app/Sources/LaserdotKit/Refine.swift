import Foundation

/// One choice among a search's results ("1080p", 12 of them): choosing it
/// adds `token` to the query. `key`, when set, names words the app shows
/// instead of `label` ("With sound").
public struct RefineOption: Codable, Equatable, Hashable, Sendable {
    public let token: String
    public let label: String
    public let key: String?
    /// Results it gives with the other groups' choices applied (0: greyed).
    public let count: UInt64
    /// The search already has it (`token` is then as written there).
    public let selected: Bool?
    /// The search leaves it out (`token` is the "-" word as written there).
    public let excluded: Bool?

    public var isSelected: Bool { selected ?? false }
    public var isExcluded: Bool { excluded ?? false }

    public init(token: String, label: String, key: String?, count: UInt64, selected: Bool? = nil, excluded: Bool? = nil) {
        self.token = token
        self.label = label
        self.key = key
        self.count = count
        self.selected = selected
        self.excluded = excluded
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

    /// A search for files like the one at `path` (same type, similar size,
    /// near date); nil when the daemon cannot say.
    public func moreLike(path: String) -> String? {
        guard let answer = guide(.moreLike(path: path), as: String.self) else { return nil }
        return answer
    }
}

/// The choice row's rules, as plain functions (unit-tested in
/// RefineLogicTests; the app's model only calls them).
public enum RefineLogic {
    /// The query after clicking `o` (in `group`): a chosen value leaves it;
    /// another value of a group with a choice takes that choice's place (a
    /// video has one resolution); otherwise it is added.
    public static func applying(_ o: RefineOption, in group: RefineGroup?, to query: String) -> String {
        var next: String
        if o.isSelected {
            next = removing(o.token, from: query)
        } else if let current = group?.options.first(where: \.isSelected) {
            next = removing(current.token, from: query) + " " + o.token
        } else {
            let q = query.trimmingCharacters(in: .whitespaces)
            next = q.isEmpty ? o.token : q + " " + o.token
        }
        return next.trimmingCharacters(in: .whitespaces)
    }

    /// The query after leaving `o` out (⌥-click, or Leave Out): the choice
    /// itself goes if it was chosen, and "-token" is added. Clicking the
    /// left-out chip (it is selected) takes the "-token" out again.
    public static func excluding(_ o: RefineOption, to query: String) -> String {
        if o.isExcluded { return removing(o.token, from: query) }
        var q = query
        if o.isSelected { q = removing(o.token, from: q) }
        q = q.trimmingCharacters(in: .whitespaces)
        let word = "-" + o.token
        return (q.isEmpty ? word : q + " " + word)
    }

    /// The query without one token (as written in it), spaces tidied.
    public static func removing(_ token: String, from q: String) -> String {
        var words = q.components(separatedBy: " ")
        if let i = words.firstIndex(of: token) {
            words.remove(at: i)
        } else if let r = q.range(of: token) {
            words = q.replacingCharacters(in: r, with: "").components(separatedBy: " ")
        }
        return words.filter { !$0.isEmpty }.joined(separator: " ")
    }

    /// The groups as shown: one with a choice shrinks to it and comes first;
    /// every other group stays, to keep narrowing.
    public static func visible(_ groups: [RefineGroup]) -> [RefineGroup] {
        var chosen: [RefineGroup] = [], open: [RefineGroup] = []
        for g in groups {
            // A left-out value does not close its group: the others stay
            // to choose or leave out too.
            let picked = g.options.filter { $0.isSelected && !$0.isExcluded }
            if picked.isEmpty {
                open.append(g)
            } else {
                chosen.append(RefineGroup(id: g.id, options: picked + g.options.filter(\.isExcluded)))
            }
        }
        return chosen + open
    }

    /// The last word of a query when it is a plain word (not a filter, not
    /// followed by a space): the one whose meaning the hint explains.
    public static func plainWord(_ q: String) -> String? {
        guard !q.hasSuffix(" "), let last = q.split(separator: " ").last.map(String.init)?.lowercased(),
              !last.isEmpty, !last.contains(":") else { return nil }
        return last
    }
}
