import Foundation

/// → on a folder goes into it, ← comes back out (the owner chose "→ on a folder
/// goes into it"): the folder becomes a pill, the other pills stay, the typed
/// words go (inside the folder everything shows), so each level stays
/// searchable.
public enum FolderNav {
    public static func entering(_ path: String, from query: String) -> String {
        let pills = Pills.tokens(query).filter { Pills.isPill($0) && !$0.lowercased().hasPrefix("in:") }
        let escaped = path.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
        return (pills + ["in:\"\(escaped)\""]).joined(separator: " ") + " "
    }
}

/// The choices by keyboard (the owner chose "Tab jumps into the choice bands"):
/// the open choices in reading order, and where each group starts.
public enum ChoiceNav {
    /// (group, option) for every choice shown, in order.
    public static func flat(_ groups: [RefineGroup]) -> [(group: RefineGroup, option: RefineOption)] {
        groups.flatMap { g in g.options.map { (group: g, option: $0) } }
    }

    /// The first choice of the group `delta` groups away from the one at `index`.
    public static func groupStart(_ groups: [RefineGroup], from index: Int, delta: Int) -> Int {
        var starts: [Int] = []
        var n = 0
        for g in groups where !g.options.isEmpty {
            starts.append(n)
            n += g.options.count
        }
        guard !starts.isEmpty else { return 0 }
        let current = starts.lastIndex { $0 <= index } ?? 0
        return starts[max(0, min(starts.count - 1, current + delta))]
    }
}
