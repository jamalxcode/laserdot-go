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
    /// Related groups, shown together and shaded alike (where, what, file,
    /// picture, time, photo, music); nil from an older daemon.
    public let family: String?
    public let options: [RefineOption]

    public init(id: String, family: String? = nil, options: [RefineOption]) {
        self.id = id
        self.family = family
        self.options = options
    }
}

/// Groups of one family, side by side in one shaded band.
public struct RefineBand: Equatable, Sendable {
    public let family: String
    public let groups: [RefineGroup]
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
    /// video has one resolution); otherwise it is added. Choices go in front
    /// of the typed words (they show as pills; the words stay where typing
    /// goes on).
    public static func applying(_ o: RefineOption, in group: RefineGroup?, to query: String) -> String {
        var q = query
        if o.isSelected {
            return removing(o.token, from: q).trimmingCharacters(in: .whitespaces)
        }
        if let current = group?.options.first(where: { $0.isSelected && !$0.isExcluded }) {
            q = removing(current.token, from: q)
        }
        return prepending(o.token, to: q)
    }

    /// `token` in front of the query (the choices come first).
    public static func prepending(_ token: String, to query: String) -> String {
        let q = query.trimmingCharacters(in: .whitespaces)
        return q.isEmpty ? token : token + " " + q
    }

    /// The query after leaving `o` out (⌥-click, or Leave Out): the choice
    /// itself goes if it was chosen, and "-token" is added. Clicking the
    /// left-out chip (it is selected) takes the "-token" out again.
    public static func excluding(_ o: RefineOption, to query: String) -> String {
        if o.isExcluded { return removing(o.token, from: query) }
        var q = query
        if o.isSelected { q = removing(o.token, from: q) }
        return prepending("-" + o.token, to: q)
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

    /// The groups as shown in the choice row. What is chosen shows as a pill
    /// in the search field, so a group with a choice leaves the row (remove
    /// the pill and it comes back, to choose again); a value left out leaves
    /// its group (its pill says "not …") while the other values stay. Tokens
    /// in `hiding` (the default search place, shown by the place menu) are
    /// dropped without closing their group.
    public static func visible(_ groups: [RefineGroup], hiding: Set<String> = []) -> [RefineGroup] {
        var out: [RefineGroup] = []
        for g in groups {
            let options = g.options.filter { !hiding.contains($0.token) }
            if options.contains(where: { $0.isSelected && !$0.isExcluded }) { continue }
            let open = options.filter { !$0.isSelected }
            if !open.isEmpty { out.append(RefineGroup(id: g.id, family: g.family, options: open)) }
        }
        return out
    }

    /// The calm view of the choices (the owner: "less cluttered … hide
    /// choices that are not applicable, and have a way of getting them
    /// back"): values that would give nothing are left out, each group shows
    /// its `perGroup` values with the most results (in their usual order),
    /// and only the first `bandLimit` families show. `hidden` counts what was
    /// left out, for the "More choices (N)" button; expanded shows everything.
    public static func compact(_ groups: [RefineGroup], expanded: Bool,
                               perGroup: Int = 4, bandLimit: Int = 4) -> (shown: [RefineGroup], hidden: Int) {
        if expanded { return (groups, 0) }
        var shown: [RefineGroup] = []
        var hidden = 0
        var families: [String] = []
        for g in groups {
            let family = g.family ?? g.id
            if !families.contains(family) { families.append(family) }
            guard families.count <= bandLimit else {
                hidden += g.options.count
                continue
            }
            let applicable = g.options.filter { $0.count > 0 }
            hidden += g.options.count - applicable.count
            var keep = applicable
            if keep.count > perGroup {
                let top = Set(applicable.sorted { $0.count > $1.count }.prefix(perGroup).map(\.token))
                keep = applicable.filter { top.contains($0.token) }
                hidden += applicable.count - keep.count
            }
            if !keep.isEmpty { shown.append(RefineGroup(id: g.id, family: g.family, options: keep)) }
        }
        return (shown, hidden)
    }

    /// The choice row top down (the owner: "starting large then going
    /// granular, with related properties"): consecutive groups of one family
    /// form a band, shaded by its family. The daemon sends them in order.
    public static func bands(_ groups: [RefineGroup]) -> [RefineBand] {
        var out: [RefineBand] = []
        for g in groups {
            let f = g.family ?? g.id
            if let last = out.last, last.family == f {
                out[out.count - 1] = RefineBand(family: f, groups: last.groups + [g])
            } else {
                out.append(RefineBand(family: f, groups: [g]))
            }
        }
        return out
    }

    /// The last word of a query when it is a plain word (not a filter, not
    /// followed by a space): the one whose meaning the hint explains.
    public static func plainWord(_ q: String) -> String? {
        guard !q.hasSuffix(" "), let last = q.split(separator: " ").last.map(String.init)?.lowercased(),
              !last.isEmpty, !last.contains(":") else { return nil }
        return last
    }
}
