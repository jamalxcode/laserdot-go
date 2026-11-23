import AppKit
import Foundation
import LaserdotKit

// The empty box (recently opened, recent downloads, pins), editing the query,
// sections and Show More, and the passage shown for a content match: split
// from SearchModel.swift to keep each file about one thing.
extension SearchModel {
    // MARK: the empty box: recently opened, recent downloads, pins

    /// What the empty box shows: results opened lately and new downloads,
    /// in the same list (↑/↓, ⌘1–9, preview all work).
    /// A number as the user's language writes it ("4,210").
    static func number(_ n: UInt64) -> String {
        let f = NumberFormatter()
        f.numberStyle = .decimal
        return f.string(from: NSNumber(value: n)) ?? "\(n)"
    }

    func loadHome() {
        let engine = self.engine
        let gen = generation
        let lastTip = tip?.id ?? UserDefaults.standard.string(forKey: "laserdot.lastTip")
        let pins = pinned
        queue.async { [weak self] in
            // A tip from this Mac's own files, a different one each time.
            let tip = Tips.pick(counts: (try? engine.valueCounts()) ?? [], after: lastTip)
            DispatchQueue.main.async {
                self?.tip = tip
                if let id = tip?.id { UserDefaults.standard.set(id, forKey: "laserdot.lastTip") }
            }
            // The smart lists' and pins' counts, live (a list with nothing in
            // it is not shown).
            var counts: [String: UInt64] = [:]
            for q in SmartList.builtIn.map(\.query) + pins {
                counts[q] = (try? engine.search(query: q, limit: 1).total) ?? 0
            }
            DispatchQueue.main.async { self?.smartCounts = counts }
            let recent = engine.recentOpens(limit: 6)
            let downloads = (try? engine.search(query: "in:~/Downloads since:30d -kind:folder", limit: 6).hits) ?? []
            DispatchQueue.main.async {
                guard let self, gen == self.generation, self.query.isEmpty else { return }
                var hits: [Hit] = []
                var secs: [ResultSection] = []
                let seen = Set(recent.map(\.path))
                let fresh = downloads.filter { !seen.contains($0.path) }
                for (id, list) in [("recent", recent), ("downloads", fresh)] where !list.isEmpty {
                    secs.append(ResultSection(id: id, title: L("home.\(id)"), range: hits.count..<(hits.count + list.count),
                                              filter: nil, total: list.count, hidden: 0))
                    hits += list
                }
                self.resultsID &+= 1
                (self.results, self.sections, self.allHits) = (hits, secs, [])
                self.updateCompact()
            }
        }
    }

    var isHome: Bool { query.trimmingCharacters(in: .whitespaces).isEmpty }

    // MARK: editing

    /// Replace Unicode-scalar range `start..<end` of the query.
    func replace(_ start: Int, _ end: Int, with text: String) {
        var scalars = Array(query.unicodeScalars)
        let s = max(0, min(start, scalars.count))
        let e = max(s, min(end, scalars.count))
        scalars.replaceSubrange(s..<e, with: text.unicodeScalars)
        var out = String.UnicodeScalarView()
        out.append(contentsOf: scalars)
        query = String(out)
    }

    /// A completion chip was clicked.
    func apply(_ c: Completion) {
        replace(c.replaceStart, c.replaceEnd, with: c.insert)
        focusSearch()
    }

    /// Narrow to one section's kind (its chip was clicked): adds kind:….
    func narrow(to section: ResultSection) {
        guard let filter = section.filter else { return }
        let sep = query.hasSuffix(" ") ? "" : " "
        query += sep + filter
        focusSearch()
    }

    /// Chips that narrow the results to one kind, when they mix kinds and
    /// the query does not already name one.
    var kindChips: [ResultSection] {
        guard !query.contains("kind:") else { return [] }
        let kinds = sections.filter { $0.filter != nil }
        return kinds.count >= 2 ? kinds : []
    }

    /// Every result is only similar to what was typed (no exact match).
    var similarOnly: Bool { !isHome && !results.isEmpty && results.allSatisfy(\.typo) }

    /// More results than were fetched: scrolling to the end loads them.
    var canLoadMore: Bool { !isHome && !isCommandMode && !allHits.isEmpty && UInt64(allHits.count) < total }

    func loadMore() {
        guard canLoadMore, !loadingMore else { return }
        loadingMore = true
        let (engine, q, gen, offset, sort, reverse) = (self.engine, self.effectiveQuery, self.generation, allHits.count, self.sort, self.reverse)
        queue.async { [weak self] in
            // Show More: a thousand at a time (the owner: "max 1000 instead of 100").
            let more = (try? engine.search(query: q, limit: Self.morePage, offset: offset, sort: sort, reverse: reverse).hits) ?? []
            DispatchQueue.main.async {
                guard let self else { return }
                self.loadingMore = false
                guard gen == self.generation, !more.isEmpty else { return }
                let current = self.selected?.path
                let seen = Set(self.allHits.map(\.path))
                self.allHits += more.filter { !seen.contains($0.path) }
                (self.results, self.sections) = self.arranged(self.allHits)
                self.selection = self.results.firstIndex { $0.path == current } ?? self.selection
            }
        }
    }

    /// Settings ▸ Clear History: recent searches and the opened-results
    /// history (pins stay).
    func clearHistory() {
        history = []
        historyIndex = -1
        UserDefaults.standard.removeObject(forKey: "laserdot.history")
        let engine = self.engine
        queue.async { [weak self] in
            try? engine.clearOpens()
            DispatchQueue.main.async { if self?.isHome == true { self?.loadHome() } }
        }
    }

    /// Sections by kind in the list sorted by relevance; one list in the
    /// chosen order otherwise. Columns and the grid are always one list, as
    /// in Finder (the grid keeps the relevance order, best first, untitled).
    func arranged(_ hits: [Hit]) -> ([Hit], [ResultSection]) {
        if viewMode == .grid && sort == .relevance {
            return (hits, hits.isEmpty ? [] : [ResultSection(id: "grid", title: "", range: 0..<hits.count, filter: nil, total: hits.count, hidden: 0)])
        }
        guard sort != .relevance || viewMode == .columns else { return Self.grouped(hits, expanded: expanded) }
        let title = L("sort.\(sort.rawValue)")
        return (hits, hits.isEmpty ? [] : [ResultSection(id: "sorted", title: title, range: 0..<hits.count, filter: nil, total: hits.count, hidden: 0)])
    }

    /// ⌘→: "Show all" for the selected result's section.
    func expandSelectedSection() {
        guard let sec = sections.first(where: { $0.range.contains(selection) }), sec.hidden > 0 else { return }
        expand(sec.id)
    }

    /// "Show all 37": the section's other results join the list.
    func expand(_ id: String) {
        expanded.insert(id)
        let current = selected?.path
        (results, sections) = arranged(allHits)
        selection = results.firstIndex { $0.path == current } ?? 0
        resultsID &+= 1
    }

    /// For a document found by its words, the passage that holds them.
    func loadSnippet(for hit: Hit) {
        snippet = nil
        guard hit.content, hit.volume == nil else { return }
        let words = highlightWords.joined(separator: " ")
        guard !words.isEmpty else { return }
        let engine = self.engine
        let path = hit.path
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let text = (try? engine.snippet(path: path, query: words)) ?? ""
            DispatchQueue.main.async {
                if self?.selected?.path == path { self?.snippet = text.isEmpty ? nil : text }
            }
        }
    }

    /// The words of the query that match names, for bold highlighting.
    var highlightWords: [String] {
        query.split(separator: " ").compactMap { t -> String? in
            var w = String(t)
            if w == "OR" || w.hasPrefix("-") || w.contains(":") { return nil }
            w = w.trimmingCharacters(in: CharacterSet(charactersIn: "\"*?"))
            return w.isEmpty ? nil : w
        }
    }
}
