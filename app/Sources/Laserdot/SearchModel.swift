import AppKit
import Foundation
import LaserdotKit

/// State behind the overlay: the query, live hint, results and actions.
/// Searches run off the main thread; only the latest one is shown.
final class SearchModel: ObservableObject {
    struct Problem: Equatable {
        var message: String
        var start: Int
        var end: Int
        var suggestion: String?
    }

    @Published var query = "" {
        didSet { if query != oldValue { queryChanged() } }
    }
    @Published var results: [Hit] = []
    /// Result order: relevance (sections), or name, size, date (one list).
    @Published var sort: SortKey = SortKey(rawValue: UserDefaults.standard.string(forKey: "laserdot.sort") ?? "") ?? .relevance {
        didSet {
            UserDefaults.standard.set(sort.rawValue, forKey: "laserdot.sort")
            if sort != oldValue { queryChanged() }
        }
    }
    /// The sort's other direction (smallest, oldest, Z→A).
    @Published var reverse = false {
        didSet { if reverse != oldValue { queryChanged() } }
    }
    /// The results in sections, like Spotlight: Top Hit, then Applications,
    /// Documents, Images… in the order their best result ranks. `results` is
    /// in this display order, so ↑/↓ and ⌘1–9 follow what is on screen.
    @Published private(set) var sections: [ResultSection] = []
    /// Clickable completions for the word being typed (spec §8.2).
    @Published private(set) var completionChips: [Completion] = []
    @Published private(set) var total: UInt64 = 0
    @Published private(set) var elapsedMs: Double = 0
    /// The results on screen are for an earlier query; the current one is
    /// still being answered.
    @Published var searching = false
    /// Changes with every answer, so the result list is rebuilt rather than
    /// patched (a patched lazy list could keep rows of the previous query).
    @Published private(set) var resultsID = 0
    /// Bumped by Edit ▸ Find (⌘F): the search field takes the keyboard.
    @Published var focusRequest = 0
    @Published private(set) var hint: String?
    /// The hint, unless it says nothing about what was typed.
    var visibleHint: String? {
        guard let h = hint, !isGenericHint(h, lang: appLang) else { return nil }
        return h
    }
    /// Every result of the last answer, before sections were capped.
    private var allHits: [Hit] = []
    /// Sections whose "Show all" was clicked.
    private var expanded = Set<String>()
    /// The user is working with the results (moved with ↑/↓ or clicked a
    /// row) rather than typing: Space opens Quick Look, as in Finder.
    /// Typing again (the query changes) ends it.
    @Published var browsing = false
    /// Rows selected besides `selection` (⇧↑/↓, ⌘-click).
    @Published var extraSelection = Set<Int>()
    var anchor = 0
    /// ⌘K: the actions for the selected result(s).
    @Published var showActions = false {
        didSet { actionIndex = 0; updateCompact() }
    }
    @Published var actionIndex = 0
    /// `>` at the start of the query: commands instead of a search.
    @Published var commandIndex = 0
    var commandProvider: (() -> [ActionItem])?
    /// "Indexing “Home”… 45%", while a folder is first indexed.
    @Published private(set) var progressText: String?
    private var progressTimer: Timer?
    /// The overlay shrinks to a slim bar when there is nothing to show.
    @Published private(set) var compact = true
    @Published private(set) var problem: Problem?
    @Published private(set) var notes: [String] = []
    @Published private(set) var unavailable: String?
    @Published var selection = 0
    /// Results as thumbnails (⌘J), remembered.
    @Published var gridView = UserDefaults.standard.bool(forKey: "laserdot.grid") {
        didSet { UserDefaults.standard.set(gridView, forKey: "laserdot.grid") }
    }
    /// Tiles per row in the grid (set by the view).
    var gridColumns = 1
    /// Space presses that reached the model (for the self-test's report).
    var spacePresses = 0
    /// The last file operation, for Undo (⌥⌘Z) and the banner.
    /// File operations that Undo (⌥⌘Z, the banner) can reverse, newest
    /// last; kept across launches for a week (UndoStep.load).
    @Published var undoStack: [UndoStep] = UndoStep.load()
    var undoStep: UndoStep? { undoStack.last }
    @Published var undoBannerVisible = false
    /// Words from inside the selected document (content matches).
    @Published private(set) var snippet: String?
    @Published var showCheatSheet = false {
        didSet {
            if showCheatSheet && !oldValue { loadCounts() }
            updateCompact()
        }
    }
    @Published private(set) var networkBlocked = false
    /// Zero-results suggestions (spec §8.3).
    @Published private(set) var coaching: [CoachingInfo] = []
    /// Live counts for the cheat sheet, by key then value (spec §8.9.5).
    @Published private(set) var counts: [String: [String: UInt64]] = [:]
    /// Cheat-sheet entry selected with the keyboard (spec §8.9.8).
    @Published var cheatIndex = 0
    /// The cheat sheet's own filter box (§8.9.7).
    @Published var cheatFilter = "" {
        didSet { cheatIndex = 0 }
    }
    /// Every filter, in the app's language: the bundled registry until the
    /// daemon answers.
    @Published private var allFilters = filters(lang: appLang)
    /// The cheat-sheet section for the token under the caret (§8.9.6).
    @Published private(set) var currentSection: String?
    /// Guidance calls (hint, section, filters) go to the daemon on a queue of
    /// their own, so they never wait behind a search.
    private let guideQueue = DispatchQueue(label: "laserdot.guide", qos: .userInitiated)

    /// Cheat-sheet entries shown, in order.
    var cheatEntries: [FilterInfo] {
        let f = cheatFilter.lowercased()
        return allFilters.filter { f.isEmpty || $0.key.contains(f) || $0.description.lowercased().contains(f) }
    }

    /// ↑/↓ in the cheat sheet.
    func moveCheat(_ delta: Int) {
        let n = cheatEntries.count
        guard n > 0 else { return }
        cheatIndex = max(0, min(n - 1, cheatIndex + delta))
    }

    /// Tab: the first entry of the next section.
    func nextCheatSection() {
        let e = cheatEntries
        guard e.indices.contains(cheatIndex) else { return }
        let here = e[cheatIndex].sectionId
        cheatIndex = e.indices.first { $0 > cheatIndex && e[$0].sectionId != here } ?? 0
    }

    /// Return: insert the selected filter.
    func insertSelectedCheat() {
        let e = cheatEntries
        guard e.indices.contains(cheatIndex) else { return }
        insertFilter(e[cheatIndex].key)
    }
    /// Pinned queries, shown as chips on an empty box (spec §8.6).
    @Published private(set) var pinned: [String] = UserDefaults.standard.stringArray(forKey: "laserdot.pinned") ?? []
    /// Recent queries, newest first (spec §8.6). Stays on this Mac.
    var history: [String] = UserDefaults.standard.stringArray(forKey: "laserdot.history") ?? []
    var historyIndex = -1

    var onStatus: ((DaemonStatus) -> Void)?
    /// Indexing started (the folder's name), for the menu's status line.
    var onIndexing: ((String) -> Void)?
    /// Indexing failed: the reason, for an alert.
    var onIndexFailed: ((String, String) -> Void)?
    /// Connected, and nothing is indexed yet (first launch).
    var onNothingIndexed: (() -> Void)?
    let engine: Engine
    private var generation = 0
    private let queue = DispatchQueue(label: "laserdot.search", qos: .userInitiated)
    /// Indexing runs here, one folder after another, never ahead of searches.
    private let indexQueue = DispatchQueue(label: "laserdot.index", qos: .utility)

    init(engine: Engine) {
        self.engine = engine
        hint = LaserdotKit.hint(query: "", cursor: 0, lang: appLang)
    }

    /// Offset of the caret in Unicode scalars, as the daemon counts spans.
    private var cursor: Int { query.unicodeScalars.count }

    func connect() {
        let engine = self.engine
        queue.async { [weak self] in
            let status: DaemonStatus
            do {
                status = try engine.connect()
            } catch {
                status = engine.status()
            }
            let list = engine.filters(lang: appLang)
            DispatchQueue.main.async {
                if !list.isEmpty { self?.allFilters = list }
                self?.networkBlocked = status.networkDenied
                self?.unavailable = status.running ? nil : L("daemon.starting")
                self?.onStatus?(status)
                if self?.query.isEmpty == true { self?.loadHome() }
                if status.running && status.folders.isEmpty && status.entries == 0 {
                    self?.onNothingIndexed?()
                }
            }
        }
    }

    func queryChanged() {
        if historyIndex >= 0 && (historyIndex >= history.count || history[historyIndex] != query) {
            historyIndex = -1
        }
        generation += 1
        let gen = generation
        let q = query
        let (engine, caret) = (self.engine, cursor)
        selection = 0
        extraSelection = []
        expanded = []
        showActions = false
        browsing = false
        if isCommandMode {
            // `>`: commands, not a search.
            commandIndex = 0
            searching = false
            hint = L("commands.hint")
            completionChips = []
            (results, sections, allHits) = ([], [], [])
            problem = nil
            coaching = []
            updateCompact()
            return
        }
        guideQueue.async { [weak self] in
            let h = engine.hint(query: q, cursor: caret, lang: appLang)
            let section = engine.sectionAt(query: q, cursor: caret)
            let chips = Array(engine.completions(query: q, cursor: caret, lang: appLang).prefix(6))
            DispatchQueue.main.async {
                guard let self, gen == self.generation else { return }
                self.hint = h
                self.currentSection = section
                self.completionChips = chips
            }
        }
        if q.trimmingCharacters(in: .whitespaces).isEmpty {
            searching = false
            completionChips = []
            total = 0
            problem = nil
            notes = []
            coaching = []
            loadHome()
            return
        }
        searching = true
        let (sort, reverse) = (self.sort, self.reverse)
        queue.async { [weak self] in
            let outcome = Result { try engine.search(query: q, limit: 100, sort: sort, reverse: reverse) }
            DispatchQueue.main.async {
                guard let self, gen == self.generation else { return }
                self.apply(outcome)
            }
        }
    }

    private func apply(_ outcome: Result<Results, Error>) {
        searching = false
        resultsID &+= 1
        coaching = []
        switch outcome {
        case .success(let r):
            allHits = r.hits
            (results, sections) = arranged(r.hits)
            total = r.total
            elapsedMs = r.elapsedMs
            notes = r.notes
            problem = nil
            unavailable = nil
            if r.total == 0 { loadCoaching() }
        case .failure(let error):
            results = []
            sections = []
            allHits = []
            total = 0
            switch error as? LaserdotError {
            case let .query(message, start, end, suggestion)?:
                // "laserdot index …" is advice for the CLI; coaching offers
                // a button instead.
                let fix = suggestion.flatMap { $0.hasPrefix("laserdot ") ? nil : $0 }
                problem = Problem(message: message, start: start, end: end, suggestion: fix)
                loadCoaching()
            case let .unavailable(message)?:
                unavailable = String(format: L("daemon.unavailable"), message)
            case let e?:
                unavailable = e.message
            case nil:
                unavailable = error.localizedDescription
            }
        }
        updateCompact()
    }

    private func updateCompact() {
        compact = query.isEmpty && results.isEmpty && !showCheatSheet && !showActions
    }

    // MARK: the empty box: recently opened, recent downloads, pins

    /// What the empty box shows: results opened lately and new downloads,
    /// in the same list (↑/↓, ⌘1–9, preview all work).
    func loadHome() {
        let engine = self.engine
        let gen = generation
        queue.async { [weak self] in
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
    private func replace(_ start: Int, _ end: Int, with text: String) {
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
    private var loadingMore = false

    func loadMore() {
        guard canLoadMore, !loadingMore else { return }
        loadingMore = true
        let (engine, q, gen, offset, sort, reverse) = (self.engine, self.query, self.generation, allHits.count, self.sort, self.reverse)
        queue.async { [weak self] in
            let more = (try? engine.search(query: q, limit: 100, offset: offset, sort: sort, reverse: reverse).hits) ?? []
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

    /// Sections by kind when sorted by relevance; one list in the chosen
    /// order otherwise.
    private func arranged(_ hits: [Hit]) -> ([Hit], [ResultSection]) {
        guard sort != .relevance else { return Self.grouped(hits, expanded: expanded) }
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

    // MARK: commands (`>`)

    var isCommandMode: Bool { query.hasPrefix(">") }

    var commandEntries: [ActionItem] {
        let f = query.dropFirst().trimmingCharacters(in: .whitespaces).lowercased()
        // Titles are localized; the ids (dupes, index, settings, eject…) also
        // match, so English command names work in every language.
        return (commandProvider?() ?? []).filter { f.isEmpty || $0.title.lowercased().contains(f) || $0.id.contains(f) }
    }

    func runCommand(_ i: Int? = nil) {
        let list = commandEntries
        let index = i ?? commandIndex
        guard list.indices.contains(index) else { return }
        query = ""
        list[index].run()
    }

    /// Tab: take the first completion for the word under the caret (§8.2).
    func acceptCompletion() {
        guard let c = engine.completions(query: query, cursor: cursor, lang: appLang).first else { return }
        replace(Int(c.replaceStart), Int(c.replaceEnd), with: c.insert)
    }

    /// Replace the word containing the error with the suggested fix.
    func applySuggestion() {
        guard let p = problem, let fix = p.suggestion else { return }
        let scalars = Array(query.unicodeScalars)
        var s = min(p.start, scalars.count)
        var e = min(max(p.end, s), scalars.count)
        while s > 0 && !CharacterSet.whitespaces.contains(scalars[s - 1]) { s -= 1 }
        while e < scalars.count && !CharacterSet.whitespaces.contains(scalars[e]) { e += 1 }
        replace(s, e, with: fix)
    }

    func insertFilter(_ key: String) {
        let sep = query.isEmpty || query.hasSuffix(" ") ? "" : " "
        query += sep + key + ":"
    }

    // MARK: coaching, counts (spec §8.3, §8.9.5)

    private func loadCoaching() {
        let (engine, q, gen) = (self.engine, self.query, self.generation)
        queue.async { [weak self] in
            let list = (try? engine.coach(query: q)) ?? []
            DispatchQueue.main.async {
                guard let self, gen == self.generation else { return }
                self.coaching = list
            }
        }
    }

    func follow(_ c: CoachingInfo) {
        switch c.kind {
        case "index_folder":
            if let p = c.path { index(folder: URL(fileURLWithPath: p)) }
        case "show_syntax":
            showCheatSheet = true
        default:
            if let q = c.query { query = q }
        }
    }

    private func loadCounts() {
        let engine = self.engine
        queue.async { [weak self] in
            let list = (try? engine.valueCounts()) ?? []
            var map: [String: [String: UInt64]] = [:]
            for v in list { map[v.key, default: [:]][v.value] = v.count }
            DispatchQueue.main.async { self?.counts = map }
        }
    }


    // MARK: history and pins (spec §8.6)

    /// Remember a query that was used (a result opened or revealed).
    /// Opened results rank higher next time (spec §6.2); kept on this Mac.
    func noteOpened(_ h: Hit) {
        guard h.volume == nil else { return } // the history covers this Mac's index
        let (engine, path) = (self.engine, h.path)
        queue.async { engine.recordOpen(path: path) }
    }

    func remember() {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return }
        history.removeAll { $0 == q }
        history.insert(q, at: 0)
        history = Array(history.prefix(50))
        UserDefaults.standard.set(history, forKey: "laserdot.history")
        historyIndex = -1
    }

    /// ↑/↓ with no results to move through: walk the history.
    func recall(_ delta: Int) {
        // Only from an empty box, or while already walking the history.
        guard !history.isEmpty, query.isEmpty || historyIndex >= 0 else { return }
        historyIndex = max(-1, min(history.count - 1, historyIndex + delta))
        query = historyIndex < 0 ? "" : history[historyIndex]
    }

    var isPinned: Bool { pinned.contains(query.trimmingCharacters(in: .whitespaces)) }

    func togglePin(_ q: String? = nil) {
        let q = (q ?? query).trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return }
        if let i = pinned.firstIndex(of: q) { pinned.remove(at: i) } else { pinned.append(q) }
        UserDefaults.standard.set(pinned, forKey: "laserdot.pinned")
    }

    func index(folder: URL) {
        let engine = self.engine
        let path = folder.path
        let name = folder.lastPathComponent
        onIndexing?(name)
        startProgress(name)
        indexQueue.async { [weak self] in
            var failure: String?
            do {
                try engine.index(path: path)
            } catch {
                failure = Self.describe(error)
            }
            let status = engine.status()
            DispatchQueue.main.async {
                self?.stopProgress()
                self?.onStatus?(status)
                if let failure { self?.onIndexFailed?(name, failure) }
                self?.queryChanged() // run the query again: the folder is in now
            }
        }
    }

    /// Poll the daemon once a second for how far indexing has got.
    private func startProgress(_ name: String) {
        progressText = String(format: L("index.running"), name)
        progressTimer?.invalidate()
        progressTimer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            guard let self else { return }
            let engine = self.engine
            self.guideQueue.async {
                let p = engine.status().indexing
                DispatchQueue.main.async {
                    guard self.progressTimer != nil, let p else { return }
                    let folder = p.root == FileManager.default.homeDirectoryForCurrentUser.path
                        ? L("index.home") : URL(fileURLWithPath: p.root).lastPathComponent
                    if p.phase == "saving", p.total > 0 {
                        let pct = Int(Double(p.done) / Double(p.total) * 100)
                        self.progressText = String(format: L("index.progress"), folder, pct)
                    } else {
                        self.progressText = String(format: L("index.scanning"), folder)
                    }
                }
            }
        }
    }

    private func stopProgress() {
        progressTimer?.invalidate()
        progressTimer = nil
        progressText = nil
    }

    /// A readable reason for a failed request.
    static func describe(_ error: Error) -> String {
        switch error {
        case LaserdotError.query(let message, _, _, _): return message
        case LaserdotError.unavailable(let message): return message
        default: return (error as? LocalizedError)?.errorDescription ?? String(describing: error)
        }
    }
}
