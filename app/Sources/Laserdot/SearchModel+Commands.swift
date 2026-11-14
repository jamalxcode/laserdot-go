import AppKit
import Foundation
import LaserdotKit

/// Commands (`>`), coaching and counts, history and pins.
extension SearchModel {
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
    /// Asked off the main thread, so a busy daemon never freezes typing; the
    /// answer applies only if the text has not changed meanwhile.
    func acceptCompletion() {
        let (q, caret, engine) = (query, cursor, self.engine)
        guideQueue.async { [weak self] in
            let c = engine.completions(query: q, cursor: caret, lang: appLang).first
            DispatchQueue.main.async {
                guard let self, let c, self.query == q else { return }
                self.replace(Int(c.replaceStart), Int(c.replaceEnd), with: c.insert)
            }
        }
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

    func loadCoaching() {
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

    func loadCounts() {
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
    func startProgress(_ name: String) {
        progressText = String(format: L("index.running"), name)
        progressTimer?.invalidate()
        progressTimer = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            guard let self else { return }
            let engine = self.engine
            self.guideQueue.async {
                let p = engine.status().indexing
                DispatchQueue.main.async {
                    guard self.progressTimer != nil, let p else { return }
                    self.progressText = AppDelegate.progressText(p)
                }
            }
        }
    }

    func stopProgress() {
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
