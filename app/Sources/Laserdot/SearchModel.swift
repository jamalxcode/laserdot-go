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
    @Published private(set) var results: [Hit] = []
    @Published private(set) var total: UInt64 = 0
    @Published private(set) var elapsedMs: Double = 0
    /// The results on screen are for an earlier query; the current one is
    /// still being answered.
    @Published private(set) var searching = false
    /// Changes with every answer, so the result list is rebuilt rather than
    /// patched (a patched lazy list could keep rows of the previous query).
    @Published private(set) var resultsID = 0
    @Published private(set) var hint: String?
    @Published private(set) var problem: Problem?
    @Published private(set) var notes: [String] = []
    @Published private(set) var unavailable: String?
    @Published var selection = 0
    @Published var showCheatSheet = false {
        didSet { if showCheatSheet && !oldValue { loadCounts() } }
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
    private var history: [String] = UserDefaults.standard.stringArray(forKey: "laserdot.history") ?? []
    private var historyIndex = -1

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

    /// Example queries shown on an empty box (spec §8.5).
    let examples = [
        "invoice march",
        "res:4k hdr:true",
        "a(lang:ar channels:5.1)",
        "kind:image taken:2024 gps:true",
        "kind:video fsize:>4gb",
        "lossless:true artist:fairouz",
    ]

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
                if status.running && status.folders.isEmpty && status.entries == 0 {
                    self?.onNothingIndexed?()
                }
            }
        }
    }

    private func queryChanged() {
        if historyIndex >= 0 && (historyIndex >= history.count || history[historyIndex] != query) {
            historyIndex = -1
        }
        generation += 1
        let gen = generation
        let q = query
        let (engine, caret) = (self.engine, cursor)
        guideQueue.async { [weak self] in
            let h = engine.hint(query: q, cursor: caret, lang: appLang)
            let section = engine.sectionAt(query: q, cursor: caret)
            DispatchQueue.main.async {
                guard let self, gen == self.generation else { return }
                self.hint = h
                self.currentSection = section
            }
        }
        selection = 0
        if q.trimmingCharacters(in: .whitespaces).isEmpty {
            searching = false
            results = []
            total = 0
            problem = nil
            notes = []
            coaching = []
            return
        }
        searching = true
        queue.async { [weak self] in
            let outcome = Result { try engine.search(query: q, limit: 100) }
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
            results = r.hits
            total = r.total
            elapsedMs = r.elapsedMs
            notes = r.notes
            problem = nil
            unavailable = nil
            if r.total == 0 { loadCoaching() }
        case .failure(let error):
            results = []
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
    }

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

    // MARK: results

    func move(_ delta: Int) {
        guard !results.isEmpty else {
            recall(delta < 0 ? 1 : -1)
            return
        }
        selection = max(0, min(results.count - 1, selection + delta))
    }

    var selected: Hit? { results.indices.contains(selection) ? results[selection] : nil }

    func open(_ hit: Hit? = nil) {
        // Return while a search is pending would open a result of the
        // previous query.
        guard hit != nil || !searching, let h = hit ?? selected else { return }
        remember()
        DiskAccessPrompt.shared.open(URL(fileURLWithPath: h.path))
    }

    func reveal(_ hit: Hit? = nil) {
        guard let h = hit ?? selected else { return }
        remember()
        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: h.path)])
    }

    func copyPath(_ hit: Hit? = nil) {
        guard let h = hit ?? selected else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(h.path, forType: .string)
    }

    func index(folder: URL) {
        let engine = self.engine
        let path = folder.path
        let name = folder.lastPathComponent
        onIndexing?(name)
        queue.async { [weak self] in
            var failure: String?
            do {
                try engine.index(path: path)
            } catch {
                failure = Self.describe(error)
            }
            let status = engine.status()
            DispatchQueue.main.async {
                self?.onStatus?(status)
                if let failure { self?.onIndexFailed?(name, failure) }
                self?.queryChanged() // run the query again: the folder is in now
            }
        }
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
