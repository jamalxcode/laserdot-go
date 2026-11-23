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
    @Published var sections: [ResultSection] = []
    /// Clickable completions for the word being typed (spec §8.2).
    @Published private(set) var completionChips: [Completion] = []
    /// What the results have to choose from (resolutions, sound, type…).
    @Published private(set) var refinements: [RefineGroup] = []
    /// The default search place is off for this search (its ✕ or its chip
    /// was clicked); back on once the search field is cleared.
    @Published var placeOff = false
    /// A result was opened (or shown in Finder): the ⌥Space window goes
    /// away, as Spotlight's does (the owner: "when a file is selected
    /// laserdot needs to go away"). Set by the overlay.
    var onOpened: (() -> Void)?
    /// The empty box's list (recently opened, new downloads, tips) is shown:
    /// ↓ or the Recently Opened button; the bar alone otherwise.
    @Published var showHomeList = false {
        didSet { updateCompact() }
    }

    /// Results fetched at first, and added by each Show More.
    static let morePage = 1000

    /// Settings ▸ General ▸ Search in ("" everywhere, "local", or a folder).
    /// A folder that is no longer there (a drive unplugged, a folder deleted)
    /// counts as everywhere, so it never hides every result.
    var searchPlace: String {
        let p = UserDefaults.standard.string(forKey: SearchPlace.key) ?? ""
        if p.isEmpty || p == "local" { return p }
        return Self.placeExists(p) ? p : ""
    }

    /// Whether the place's folder is there, asked of the disk at most every
    /// few seconds: the window reads the place on every redraw, and a check
    /// on a slow network drive must not stall it.
    private static var placeChecks: [String: (exists: Bool, at: Date)] = [:]
    static func placeExists(_ path: String) -> Bool {
        if let c = placeChecks[path], Date().timeIntervalSince(c.at) < 3 { return c.exists }
        let exists = FileManager.default.fileExists(atPath: path)
        if placeChecks.count > 32 { placeChecks.removeAll() }
        placeChecks[path] = (exists, Date())
        return exists
    }

    /// Reset: every search term, pill and choice, the search place (back to
    /// Everywhere) and the order — a clean start (the owner: "a button that
    /// resets all search terms, especially if an old drive that is removed is
    /// still shown").
    func resetSearch() {
        UserDefaults.standard.removeObject(forKey: SearchPlace.key)
        placeOff = false
        placeVersion += 1
        sort = .relevance
        reverse = false
        showHomeList = false
        if query.isEmpty { queryChanged() } else { query = "" }
        focusSearch()
    }

    /// Something narrows or orders the search beyond the empty box.
    var canReset: Bool {
        !query.isEmpty || !searchPlace.isEmpty || sort != .relevance || reverse
    }
    /// The search actually sent: the query with the default place.
    var effectiveQuery: String { SearchPlace.effective(query, place: searchPlace, off: placeOff) }
    /// The default place is narrowing this search (its pill shows).
    var placeInEffect: Bool { effectiveQuery != query }

    /// Search everywhere for this search (the place chip's click).
    func dropPlace() {
        placeOff = true
        queryChanged()
    }

    /// Bumped when the place changes (the place menu redraws).
    @Published private(set) var placeVersion = 0

    /// The place menu: where searches look from now on (the same setting as
    /// Settings ▸ General ▸ Search in). A folder joins the recent places.
    func setPlace(_ place: String) {
        UserDefaults.standard.set(place, forKey: SearchPlace.key)
        if place != "" && place != "local" {
            var recent = recentPlaces.filter { $0 != place }
            recent.insert(place, at: 0)
            UserDefaults.standard.set(Array(recent.prefix(5)), forKey: "laserdot.recentPlaces")
        }
        placeOff = false
        placeVersion += 1
        queryChanged()
    }

    /// Folders chosen as places before (newest first).
    var recentPlaces: [String] {
        // Only folders still there (an unplugged drive's drop out).
        (UserDefaults.standard.stringArray(forKey: "laserdot.recentPlaces") ?? []).filter { Self.placeExists($0) }
    }

    // MARK: pills

    /// The query as the field last set it (its last word may be a filter
    /// still being typed: it stays text until a space follows).
    private var fieldEditQuery: String?

    /// Filters shown as pills in the field, and the words typed.
    var fieldParts: (pills: [String], text: String) { Pills.split(query, typing: query == fieldEditQuery) }
    var pills: [String] { fieldParts.pills }

    /// What the search field shows and edits: the query without its pills.
    var fieldText: String {
        get { fieldParts.text }
        set {
            let q = Pills.join(fieldParts.pills, newValue)
            fieldEditQuery = q
            query = q
        }
    }

    /// A pill's ✕ (or Delete in an empty field for the last one).
    func removePill(_ pill: String) {
        query = Pills.removing(pill, from: query)
        focusSearch()
    }

    func removeLastPill() -> Bool {
        guard let last = pills.last else { return false }
        removePill(last)
        return true
    }

    /// A pill's words: the choice row's label when the pill came from it,
    /// else read from the filter ("PDF", "not PDF", "in Work").
    func pillLabel(_ pill: String) -> String {
        let bare = pill.hasPrefix("-") ? String(pill.drop(while: { $0 == "-" })) : pill
        for g in refinements {
            if let o = g.options.first(where: { $0.token == pill || $0.token == bare }) {
                let words = o.key.map { L("refine." + $0) } ?? o.label
                return pill.hasPrefix("-") ? String(format: L("pill.not"), words) : words
            }
        }
        return Pills.label(pill) { k in
            switch k {
            case "not": return L("pill.not")
            case "in": return L("pill.in")
            case "on": return L("pill.on")
            case "thisMac": return L("refine.drive.local")
            default:
                if k.hasPrefix("kind.") { return L("refine." + k) }
                return ""
            }
        }
    }
    @Published var total: UInt64 = 0
    @Published private(set) var elapsedMs: Double = 0
    /// The results on screen are for an earlier query; the current one is
    /// still being answered.
    @Published var searching = false
    /// Changes with every answer, so the result list is rebuilt rather than
    /// patched (a patched lazy list could keep rows of the previous query).
    @Published var resultsID = 0
    /// Bumped by Edit ▸ Find (⌘F): the search field takes the keyboard.
    @Published var focusRequest = 0
    /// The overlay reopened with a search in the field: select it all.
    @Published var selectAllRequest = 0
    /// The suggestion shown when the box is empty.
    @Published var tip: Tip?
    @Published private(set) var hint: String?
    /// The hint, unless it says nothing about what was typed.
    var visibleHint: String? {
        if let note = refreshNote { return note }
        guard let h = hint, !isGenericHint(h, lang: appLang) else { return nil }
        // Nothing typed beside the pills while the choices show: the hint
        // would only repeat them ("kind: video audio image…", the owner's
        // screenshot). It explains what is being typed.
        if showsRefinements && fieldText.trimmingCharacters(in: .whitespaces).isEmpty { return nil }
        // A plain word's meaning ("jpg means ext:jpg…") is explained the
        // first few times only; then the choice row teaches the rest. Help
        // for a filter being typed (res: …) always shows.
        if let word = Self.plainWord(query), Self.hintsShown[word, default: 0] > Self.hintRepeats { return nil }
        return h
    }

    /// How many times a plain word's meaning is explained.
    static let hintRepeats = 3
    static let hintsKey = "laserdot.hintsShown"
    static var hintsShown: [String: Int] {
        get { UserDefaults.standard.dictionary(forKey: hintsKey) as? [String: Int] ?? [:] }
        set { UserDefaults.standard.set(newValue, forKey: hintsKey) }
    }

    static func plainWord(_ q: String) -> String? { RefineLogic.plainWord(q) }

    /// Counts one explanation of the query's plain word (once per search).
    func noteHintShown() {
        guard let word = Self.plainWord(query), hint.map({ !isGenericHint($0, lang: appLang) }) == true else { return }
        var seen = Self.hintsShown
        seen[word, default: 0] += 1
        Self.hintsShown = seen
    }
    /// Every result of the last answer, before sections were capped.
    var allHits: [Hit] = []
    /// Sections whose "Show all" was clicked.
    var expanded = Set<String>()
    /// The user is working with the results (moved with ↑/↓ or clicked a
    /// row) rather than typing: Space opens Quick Look, as in Finder.
    /// Typing again (the query changes) ends it.
    @Published var browsing = false
    /// Rows selected besides `selection` (⇧↑/↓, ⌘-click).
    @Published var extraSelection = Set<Int>()
    var anchor = 0
    /// ⌘K: the actions for the selected result(s).
    @Published var showActions = false {
        didSet { actionIndex = 0; actionsExpanded = []; updateCompact() }
    }
    /// ⌘K groups opened (Open With…, More Actions…).
    @Published var actionsExpanded: Set<String> = []
    @Published var actionIndex = 0
    /// `>` at the start of the query: commands instead of a search.
    @Published var commandIndex = 0
    var commandProvider: (() -> [ActionItem])?
    /// "Indexing “Home”… 45%", while a folder is first indexed.
    @Published var progressText: String?
    var progressTimer: Timer?
    /// The overlay shrinks to a slim bar when there is nothing to show.
    @Published private(set) var compact = false
    @Published private(set) var problem: Problem?
    @Published private(set) var notes: [String] = []
    @Published private(set) var unavailable: String?
    @Published var selection = 0
    /// ⌘R is looking at the indexed folders again; then what it found.
    @Published var refreshing = false
    @Published var refreshNote: String?
    /// Results as thumbnails (⌘J), remembered.
    /// How results are shown (⌘J goes to the next), remembered: rich rows
    /// in sections, Finder's columns (sortable headers), or thumbnails.
    @Published var viewMode: ResultsView = ResultsView.saved {
        didSet {
            UserDefaults.standard.set(viewMode.rawValue, forKey: ResultsView.key)
            if viewMode != oldValue {
                // Columns and the grid are one flat list; the list groups by kind.
                let current = selected?.path
                (results, sections) = arranged(allHits)
                selection = results.firstIndex { $0.path == current } ?? 0
                resultsID &+= 1
            }
        }
    }
    var gridView: Bool {
        get { viewMode == .grid }
        set { viewMode = newValue ? .grid : .list }
    }

    /// A column header was clicked: sort by it; clicked again, the other way.
    func sortBy(_ key: SortKey) {
        if sort == key {
            reverse.toggle()
        } else {
            reverse = false
            sort = key
        }
    }
    /// Thumbnail size in the grid (points), remembered.
    static let tileSizes: ClosedRange<CGFloat> = 64...256
    @Published var tileSize: CGFloat = {
        let v = UserDefaults.standard.double(forKey: "laserdot.tileSize")
        return v >= 64 ? CGFloat(min(v, 256)) : 104
    }() {
        didSet { UserDefaults.standard.set(Double(tileSize), forKey: "laserdot.tileSize") }
    }

    func resizeTiles(by delta: CGFloat) {
        tileSize = min(Self.tileSizes.upperBound, max(Self.tileSizes.lowerBound, tileSize + delta))
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
    /// The results by month, for the timeline strip (every month from the
    /// first to the last, empty ones included).
    @Published var timeline: [MonthCount] = []
    /// How many results each smart list and pinned search has now (by query).
    @Published var smartCounts: [String: UInt64] = [:]
    /// The choice the keyboard is on (Tab into the choices; nil: the field).
    @Published var choiceFocus: Int?
    /// → into folders: each step's query before and after, for ←.
    var folderTrail: [(before: String, after: String)] = []
    /// The More button's panel of tools is open (⌘T).
    @Published var showTools = false
    @Published var snippet: String?
    /// Show More is fetching the next page (SearchModel+Home).
    var loadingMore = false
    @Published var showCheatSheet = false {
        didSet {
            if showCheatSheet && !oldValue { loadCounts() }
            updateCompact()
        }
    }
    @Published private(set) var networkBlocked = false
    /// Zero-results suggestions (spec §8.3).
    @Published var coaching: [CoachingInfo] = []
    /// Live counts for the cheat sheet, by key then value (spec §8.9.5).
    @Published var counts: [String: [String: UInt64]] = [:]
    /// Cheat-sheet entry selected with the keyboard (spec §8.9.8).
    @Published var cheatIndex = 0
    /// The cheat sheet's own filter box (§8.9.7).
    @Published var cheatFilter = "" {
        didSet { cheatIndex = 0 }
    }
    /// Every filter, in the app's language: the bundled registry until the
    /// daemon answers.
    @Published var allFilters = filters(lang: appLang)
    /// The cheat-sheet section for the token under the caret (§8.9.6).
    @Published private(set) var currentSection: String?
    /// Guidance calls (hint, section, filters) go to the daemon on a queue of
    /// their own, so they never wait behind a search.
    let guideQueue = DispatchQueue(label: "laserdot.guide", qos: .userInitiated)

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
    @Published var pinned: [String] = UserDefaults.standard.stringArray(forKey: "laserdot.pinned") ?? []
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
    var generation = 0
    let queue = DispatchQueue(label: "laserdot.search", qos: .userInitiated)
    /// Indexing runs here, one folder after another, never ahead of searches.
    let indexQueue = DispatchQueue(label: "laserdot.index", qos: .utility)

    init(engine: Engine) {
        self.engine = engine
        hint = LaserdotKit.hint(query: "", cursor: 0, lang: appLang)
    }

    /// Offset of the caret in Unicode scalars, as the daemon counts spans.
    var cursor: Int { query.unicodeScalars.count }

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
        choiceFocus = nil // typing (or a choice taken) leaves the choices
        if historyIndex >= 0 && (historyIndex >= history.count || history[historyIndex] != query) {
            historyIndex = -1
        }
        generation += 1
        let gen = generation
        generationLock.lock()
        generationShared = gen
        generationLock.unlock()
        let q = query
        if q.trimmingCharacters(in: .whitespaces).isEmpty {
            placeOff = false
            showHomeList = false // back to the start
        }
        let sent = effectiveQuery // with the default search place
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
            refinements = []
            (results, sections, allHits) = ([], [], [])
            problem = nil
            coaching = []
            updateCompact()
            return
        }
        // A choice just clicked is not a word being typed: no completions for
        // it (they offered its own value back, "1440p", and hid the choices).
        let fromChoice = choiceJustApplied
        guideQueue.async { [weak self] in
            guard let self, gen == self.currentGeneration() else { return }
            let h = engine.hint(query: q, cursor: caret, lang: appLang)
            let section = engine.sectionAt(query: q, cursor: caret)
            let chips = fromChoice ? [] : Array(engine.completions(query: q, cursor: caret, lang: appLang).prefix(6))
            DispatchQueue.main.async {
                guard gen == self.generation else { return }
                self.hint = h
                self.currentSection = section
                self.completionChips = chips
                self.noteHintShown() // counted when the explanation itself arrives
            }
        }
        if q.trimmingCharacters(in: .whitespaces).isEmpty {
            searching = false
            completionChips = []
            refinements = []
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
            // A newer keystroke came while this one waited: its search
            // replaces this one, which would only be thrown away.
            guard let self, gen == self.currentGeneration() else { return }
            // The first page is a thousand too (the owner: "still showing 100 max").
            let outcome = Result { try engine.search(query: sent, limit: Self.morePage, sort: sort, reverse: reverse) }
            DispatchQueue.main.async {
                guard gen == self.generation else { return }
                self.apply(outcome)
            }
        }
    }

    /// Asks what the results have to choose from, after they arrive (on the
    /// guidance line, skipped when a newer keystroke came meanwhile).
    /// Asked even for one result or none: after choices narrow the results
    /// that far, the choices must stay to be changed or taken out (the
    /// engine answers nothing when the search itself has nothing to choose
    /// between).
    func loadRefinements(total: UInt64) {
        let (gen, q, engine) = (generation, effectiveQuery, self.engine)
        guideQueue.async { [weak self] in
            guard let self, gen == self.currentGeneration() else { return }
            let groups = engine.refinements(query: q)
            let months = TimelineLogic.months(engine.timeline(query: q))
            DispatchQueue.main.async {
                guard gen == self.generation else { return }
                self.refinements = groups
                self.timeline = months
            }
        }
    }

    /// The generation, read from any thread (a stale request is skipped).
    private let generationLock = NSLock()
    private var generationShared = 0
    func currentGeneration() -> Int {
        generationLock.lock()
        defer { generationLock.unlock() }
        return generationShared
    }

    /// Set while a choice changes the query (queryChanged then offers no
    /// completions for the token it added).
    private var choiceJustApplied = false

    /// The choice row shows whenever there are choices (completions for a
    /// word being typed show above it; they never hide it).
    var showsRefinements: Bool { !openRefinements.isEmpty && !isCommandMode }

    /// The choices as shown: a group with a choice shrinks to that choice
    /// (its other values go; click it to take it out and they come back),
    /// and chosen ones come first; every other group stays, to keep
    /// narrowing (the owner: "the resolution attributes should go, but other
    /// choices remaining to allow further choices like location, modified,
    /// camera").
    var visibleRefinements: [RefineGroup] {
        RefineLogic.compact(openRefinements, expanded: allChoices).shown
    }

    /// Every open choice, before the calm view leaves some out.
    var openRefinements: [RefineGroup] {
        RefineLogic.visible(refinements, hiding: Set([SearchPlace.token(searchPlace)].compactMap { $0 }))
    }

    /// How many choices the calm view leaves out ("More choices (N)").
    var hiddenChoices: Int { RefineLogic.compact(openRefinements, expanded: allChoices).hidden }

    /// Show every choice (More choices); remembered, so someone who likes
    /// them all keeps seeing them all.
    @Published var allChoices = UserDefaults.standard.bool(forKey: "laserdot.allChoices") {
        didSet { UserDefaults.standard.set(allChoices, forKey: "laserdot.allChoices") }
    }

    /// A refinement chip. Chosen already: it leaves the search. Another
    /// value of a group with a choice: it takes that choice's place (a video
    /// is one resolution). Otherwise it joins the search. Every other group
    /// stays available to add to.
    func applyRefinement(_ o: RefineOption, in group: RefineGroup? = nil) {
        // The default place's chip (chosen, but not in the field): clicking
        // it searches everywhere, like the pill's ✕.
        if o.isSelected, o.token == SearchPlace.token(searchPlace), !query.contains(o.token) {
            dropPlace()
            return
        }
        let next = RefineLogic.applying(o, in: group, to: query)
        choiceJustApplied = true
        query = next // one change: queryChanged sees the flag
        choiceJustApplied = false
    }

    /// Leave a choice out (⌥-click or Leave Out): "-token" joins the search.
    func excludeRefinement(_ o: RefineOption) {
        let next = RefineLogic.excluding(o, to: query)
        choiceJustApplied = true
        query = next
        choiceJustApplied = false
    }

    static func removing(_ token: String, from q: String) -> String { RefineLogic.removing(token, from: q) }

    func apply(_ outcome: Result<Results, Error>) {
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
            loadRefinements(total: r.total)
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

    /// The start is the bar and its buttons until something is typed, ↓ is
    /// pressed or Recently Opened is clicked.
    func updateCompact() {
        // One full screen, always (the owner: "the full option, full choices
        // screen all the time"): no slim start any more.
        compact = false
    }
}
