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
    @Published private(set) var searching = false
    /// Changes with every answer, so the result list is rebuilt rather than
    /// patched (a patched lazy list could keep rows of the previous query).
    @Published private(set) var resultsID = 0
    /// Bumped by Edit ▸ Find (⌘F): the search field takes the keyboard.
    @Published private(set) var focusRequest = 0
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
    @Published private(set) var browsing = false
    /// Rows selected besides `selection` (⇧↑/↓, ⌘-click).
    @Published private(set) var extraSelection = Set<Int>()
    private var anchor = 0
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

    private func queryChanged() {
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
            allHits = r.hits
            (results, sections) = Self.grouped(r.hits, expanded: expanded)
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

    /// "Show all 37": the section's other results join the list.
    func expand(_ id: String) {
        expanded.insert(id)
        let current = selected?.path
        (results, sections) = Self.grouped(allHits, expanded: expanded)
        selection = results.firstIndex { $0.path == current } ?? 0
        resultsID &+= 1
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

    // MARK: results

    func move(_ delta: Int) {
        if showActions {
            actionIndex = max(0, min(actions.count - 1, actionIndex + delta))
            return
        }
        if isCommandMode {
            commandIndex = max(0, min(commandEntries.count - 1, commandIndex + delta))
            return
        }
        guard !results.isEmpty else {
            recall(delta < 0 ? 1 : -1)
            return
        }
        extraSelection = []
        selection = max(0, min(results.count - 1, selection + delta))
        anchor = selection
        browsing = true
    }

    /// ⇧↑/↓: grow the selection from where it started.
    func extend(_ delta: Int) {
        guard !results.isEmpty, !showActions, !isCommandMode else { return }
        if extraSelection.isEmpty { anchor = selection }
        selection = max(0, min(results.count - 1, selection + delta))
        extraSelection = Set(min(anchor, selection)...max(anchor, selection))
        browsing = true
    }

    /// A click: plain selects, ⌘ adds or removes, ⇧ selects a range.
    func click(_ i: Int) {
        browsing = true
        let flags = NSEvent.modifierFlags
        if flags.contains(.command) {
            if extraSelection.isEmpty { extraSelection = [selection] }
            if extraSelection.contains(i) && extraSelection.count > 1 {
                extraSelection.remove(i)
            } else {
                extraSelection.insert(i)
            }
            selection = i
        } else if flags.contains(.shift) {
            if extraSelection.isEmpty { anchor = selection }
            selection = i
            extraSelection = Set(min(anchor, i)...max(anchor, i))
        } else {
            extraSelection = []
            selection = i
            anchor = i
        }
    }

    func isSelected(_ i: Int) -> Bool { i == selection || extraSelection.contains(i) }

    /// The selected results, in list order (one, or several).
    var selectedHits: [Hit] {
        if extraSelection.isEmpty { return selected.map { [$0] } ?? [] }
        return extraSelection.sorted().compactMap { results.indices.contains($0) ? results[$0] : nil }
    }

    /// Return: open, or run the highlighted action or command.
    func submit() {
        if showActions {
            let a = actions
            if a.indices.contains(actionIndex) { a[actionIndex].run() }
            showActions = false
        } else if isCommandMode {
            runCommand()
        } else if extraSelection.count > 1 {
            selectedHits.forEach { open($0) }
        } else {
            open()
        }
    }

    /// Esc: close what is open (actions, cheat sheet), else the overlay.
    func escape(close: () -> Void) {
        if showActions {
            showActions = false
        } else if showCheatSheet {
            showCheatSheet = false
        } else if isCommandMode {
            query = ""
        } else {
            close()
        }
    }

    /// ⌘K: the actions for the selection.
    func toggleActions() {
        guard !selectedHits.isEmpty else { return }
        showActions.toggle()
    }

    var actions: [ActionItem] {
        let hits = selectedHits
        guard let first = hits.first else { return [] }
        let urls = hits.map { URL(fileURLWithPath: $0.path) }
        let many = hits.count > 1
        var list: [ActionItem] = [
            ActionItem(id: "open", title: L("action.open"), symbol: "arrow.up.forward.app", shortcut: "↩") { [weak self] in
                hits.forEach { self?.open($0) }
            },
        ]
        if !many {
            let apps = NSWorkspace.shared.urlsForApplications(toOpen: urls[0]).prefix(4)
            for app in apps {
                let name = FileManager.default.displayName(atPath: app.path).replacingOccurrences(of: ".app", with: "")
                list.append(ActionItem(
                    id: "with:" + app.path, title: String(format: L("action.openWith"), name), symbol: "app",
                    image: NSWorkspace.shared.icon(forFile: app.path)) { [weak self] in
                    self?.noteOpened(first)
                    NSWorkspace.shared.open(urls, withApplicationAt: app, configuration: NSWorkspace.OpenConfiguration())
                })
            }
        }
        list += [
            ActionItem(id: "quicklook", title: L("action.quickLook"), symbol: "eye", shortcut: "⌘Y") { [weak self] in self?.quickLook(first) },
            ActionItem(id: "reveal", title: L("action.reveal"), symbol: "folder", shortcut: "⌘↩") {
                NSWorkspace.shared.activateFileViewerSelecting(urls)
            },
            ActionItem(id: "copyFile", title: L(many ? "action.copyFiles" : "action.copyFile"), symbol: "doc.on.doc") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.writeObjects(urls as [NSURL])
            },
            ActionItem(id: "copyPath", title: L("action.copy"), symbol: "link", shortcut: "⌘⇧C") {
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(hits.map(\.path).joined(separator: "\n"), forType: .string)
            },
            ActionItem(id: "share", title: L("action.share"), symbol: "square.and.arrow.up") {
                if let view = NSApp.keyWindow?.contentView {
                    let r = NSRect(x: view.bounds.midX, y: view.bounds.maxY - 60, width: 1, height: 1)
                    NSSharingServicePicker(items: urls).show(relativeTo: r, of: view, preferredEdge: .minY)
                }
            },
        ]
        if !many && first.volume == nil {
            list.append(ActionItem(id: "rename", title: L("action.rename"), symbol: "pencil") { [weak self] in self?.rename(first) })
        }
        list.append(ActionItem(id: "trash", title: L("action.trash"), symbol: "trash") { [weak self] in self?.trash(hits) })
        return list
    }

    private func rename(_ h: Hit) {
        let a = NSAlert()
        a.messageText = String(format: L("rename.title"), h.name)
        let field = NSTextField(string: h.name)
        field.frame = NSRect(x: 0, y: 0, width: 300, height: 24)
        a.accessoryView = field
        a.addButton(withTitle: L("action.rename"))
        a.addButton(withTitle: L("action.cancel"))
        a.window.initialFirstResponder = field
        guard a.runFront() == .alertFirstButtonReturn else { return }
        let name = field.stringValue.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty, name != h.name, !name.contains("/") else { return }
        let from = URL(fileURLWithPath: h.path)
        do {
            try FileManager.default.moveItem(at: from, to: from.deletingLastPathComponent().appendingPathComponent(name))
        } catch {
            let e = NSAlert(error: error)
            e.runFront()
        }
        refreshSoon()
    }

    /// Move to the Trash (Finder's ⌘⌫; recoverable from the Trash).
    private func trash(_ hits: [Hit]) {
        NSWorkspace.shared.recycle(hits.map { URL(fileURLWithPath: $0.path) }) { [weak self] _, error in
            DispatchQueue.main.async {
                if let error { NSAlert(error: error).runFront() }
                self?.refreshSoon()
            }
        }
    }

    /// The daemon's watcher picks the change up; search again a moment later.
    private func refreshSoon() {
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) { [weak self] in self?.queryChanged() }
    }

    var selected: Hit? { results.indices.contains(selection) ? results[selection] : nil }

    /// ⌘1–⌘9: open the nth result on screen.
    func openNumbered(_ n: Int) {
        guard results.indices.contains(n - 1) else { return }
        selection = n - 1
        open(results[n - 1])
    }

    /// ⌘Y: Quick Look, as in Finder.
    func quickLook(_ hit: Hit? = nil) {
        guard let h = hit ?? selected else { return }
        let urls = results.map { URL(fileURLWithPath: $0.path) }
        let i = results.firstIndex { $0.path == h.path } ?? 0
        QuickLook.shared.show(urls.isEmpty ? [URL(fileURLWithPath: h.path)] : urls, at: i)
    }

    /// Space: Quick Look for the selected result while browsing the results
    /// (true: handled); otherwise the field types a space.
    func spaceKey() -> Bool {
        guard browsing, !results.isEmpty, selected != nil, !showActions, !isCommandMode, !showCheatSheet else { return false }
        if QuickLook.shared.isVisible {
            QuickLook.shared.close()
        } else {
            quickLook()
        }
        return true
    }

    func focusSearch() {
        focusRequest &+= 1
    }

    func open(_ hit: Hit? = nil) {
        // Return while a search is pending would open a result of the
        // previous query.
        guard hit != nil || !searching, let h = hit ?? selected else { return }
        remember()
        noteOpened(h)
        DiskAccessPrompt.shared.open(URL(fileURLWithPath: h.path))
    }

    func reveal(_ hit: Hit? = nil) {
        guard let h = hit ?? selected else { return }
        remember()
        noteOpened(h)
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
        for c in order {
            let all = byCategory[c] ?? []
            let shown = expanded.contains(c) ? all : Array(all.prefix(sectionCap))
            sections.append(ResultSection(
                id: c, title: L("section.\(c)"), range: out.count..<(out.count + shown.count), filter: "kind:\(c)",
                total: all.count + (category(top) == c ? 1 : 0), hidden: all.count - shown.count))
            out += shown
        }
        return (out, sections)
    }
}
