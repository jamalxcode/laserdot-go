import AppKit
import LaserdotKit

/// A group of results on screen: `range` indexes `SearchModel.results`.
struct ResultSection: Identifiable, Equatable {
    let id: String
    let title: String
    let range: Range<Int>
    /// The filter that narrows to this section (`kind:image`), if any.
    let filter: String?
    /// Results of this kind in the answer, and how many "Show all" adds.
    let total: Int
    let hidden: Int
}

/// An action (⌘K) or a command (`>`).
struct ActionItem: Identifiable {
    let id: String
    let title: String
    let symbol: String
    var shortcut: String? = nil
    /// An app's own icon (Open with…), shown instead of the symbol.
    var image: NSImage? = nil
    let run: () -> Void
}

extension SearchModel {
    /// Results shown per section until "Show all".
    static let sectionCap = 5

    /// The category a result is shown under.
    static func category(_ h: Hit) -> String {
        if h.kind == "app" { return "app" }
        if h.isDir { return "folder" }
        switch h.kind {
        case "document", "image", "video", "audio", "archive", "code": return h.kind
        default: return "other"
        }
    }

    /// Top Hit (the best result), then one section per category in the order
    /// of each category's best result, up to `sectionCap` each unless
    /// expanded; within a section the ranking order is kept.
    static func grouped(_ hits: [Hit], expanded: Set<String> = []) -> ([Hit], [ResultSection]) {
        guard let top = hits.first else { return ([], []) }
        let topSection = ResultSection(id: "top", title: L("section.top"), range: 0..<1, filter: nil, total: 1, hidden: 0)
        var order: [String] = []
        var byCategory: [String: [Hit]] = [:]
        for h in hits.dropFirst() {
            let c = category(h)
            if byCategory[c] == nil { order.append(c) }
            byCategory[c, default: []].append(h)
        }
        var out = [top]
        var sections = [topSection]
        // One kind only (all pictures for `jpg`): nothing to group, no cap.
        let single = order.count == 1
        for c in order {
            let all = byCategory[c] ?? []
            let shown = single || expanded.contains(c) ? all : Array(all.prefix(sectionCap))
            sections.append(ResultSection(
                id: c, title: L("section.\(c)"), range: out.count..<(out.count + shown.count), filter: "kind:\(c)",
                total: all.count + (category(top) == c ? 1 : 0), hidden: all.count - shown.count))
            out += shown
        }
        return (out, sections)
    }
}
