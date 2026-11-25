import AppKit
import LaserdotKit

/// Moving through results, selecting, opening, Quick Look (SearchModel, continued).
extension SearchModel {
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
        // ↑ from the empty box (not yet in its list), and again while
        // walking the history: earlier searches; ↓ goes into the results.
        let walking = historyIndex >= 0 && !browsing && delta < 0
        let atHomeTop = query.isEmpty && !browsing && delta < 0
        guard !results.isEmpty, !walking, !atHomeTop else {
            recall(delta < 0 ? 1 : -1)
            return
        }
        extraSelection = []
        // In the grid ↑↓ go a row up or down.
        let step = gridView ? delta * max(1, gridColumns) : delta
        selection = max(0, min(results.count - 1, selection + step))
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

    /// What is selected now, as row numbers.
    var selectionSet: Set<Int> { extraSelection.isEmpty ? [selection] : extraSelection }

    /// What a right-click acts on, as in Finder: the whole selection when the
    /// row clicked is part of it (the owner: trashing several selected files
    /// moved only the one under the pointer), else that row alone.
    func menuTargets(_ i: Int, _ hit: Hit) -> [Hit] {
        isSelected(i) && extraSelection.count > 1 ? selectedHits : [hit]
    }

    /// Drag-to-select (a box drawn over the results, as in Finder): the rows
    /// the box touches, added to `base` (what was selected when ⌘ or ⇧ was
    /// held as the drag began).
    func marqueeSelect(_ rows: Set<Int>, base: Set<Int>) {
        let all = rows.union(base).filter { results.indices.contains($0) }
        browsing = true
        guard let first = all.min() else { return }
        selection = rows.min() ?? first
        anchor = selection
        extraSelection = all.count > 1 ? all : []
    }

    /// The selected results, in list order (one, or several).
    var selectedHits: [Hit] {
        if extraSelection.isEmpty { return selected.map { [$0] } ?? [] }
        return extraSelection.sorted().compactMap { results.indices.contains($0) ? results[$0] : nil }
    }

    /// Return: open, or run the highlighted action or command.
    func submit() {
        if showActions {
            let a = actions
            let item = a.indices.contains(actionIndex) ? a[actionIndex] : nil
            item?.run()
            if item?.keepsOpen != true { showActions = false }
        } else if isCommandMode {
            runCommand()
        } else if extraSelection.count > 1 {
            selectedHits.forEach { open($0) }
        } else {
            open()
        }
    }

    /// Esc, as in Spotlight: close what is open (actions, cheat sheet), else
    /// clear the search, else (the field already empty) close the overlay.
    func escape(close: () -> Void) {
        if showActions {
            showActions = false
        } else if showCheatSheet {
            showCheatSheet = false
        } else if !query.isEmpty {
            query = ""
            browsing = false
        } else {
            close()
        }
    }

    /// As in Spotlight: the rest of the top hit's name after what was typed,
    /// when the name begins with it (case and accents aside). Not for queries
    /// with filters, commands, or while browsing the results.
    var inlineSuffix: String? {
        let typed = fieldText // the words typed (pills aside)
        guard !typed.isEmpty, !isCommandMode, !browsing, !showCheatSheet, !showActions,
              !typed.contains(":"), !typed.hasSuffix(" "), let top = results.first,
              let r = top.name.range(of: typed, options: [.anchored, .caseInsensitive, .diacriticInsensitive]),
              r.upperBound < top.name.endIndex else { return nil }
        return String(top.name[r.upperBound...])
    }

    /// ⌘↓/⌘↑, as in Spotlight: the first result of the next or previous
    /// section.
    func jumpSection(_ delta: Int) {
        guard !results.isEmpty, !sections.isEmpty else { return }
        let current = sections.firstIndex { $0.range.contains(selection) } ?? 0
        let target = max(0, min(sections.count - 1, current + delta))
        selection = sections[target].range.lowerBound
        anchor = selection
        extraSelection = []
        browsing = true
    }

    /// ⌘C with no text selected, as in Spotlight: the selected files go to
    /// the clipboard (paste them in Finder, Mail…). True when done.
    func copyFiles() -> Bool {
        let hits = selectedHits
        guard !hits.isEmpty, !isCommandMode else { return false }
        NSPasteboard.general.clearContents()
        return NSPasteboard.general.writeObjects(hits.map { URL(fileURLWithPath: $0.path) as NSURL })
    }

    /// ⌘K: the actions for the selection.
    func toggleActions() {
        guard !selectedHits.isEmpty else { return }
        showActions.toggle()
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
        spacePresses += 1
        // A second space in a row (the query already ends in one) also
        // previews: no need to press ↓ for the highlighted result.
        guard browsing || query.hasSuffix(" "), !results.isEmpty, selected != nil,
              !showActions, !isCommandMode, !showCheatSheet else { return false }
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
        onOpened?()
    }

    func reveal(_ hit: Hit? = nil) {
        guard let h = hit ?? selected else { return }
        reveal([h])
    }

    /// Show in Finder, several at once (one Finder window selecting them).
    func reveal(_ hits: [Hit]) {
        guard !hits.isEmpty else { return }
        remember()
        hits.forEach { noteOpened($0) }
        Finder.reveal(hits.map { URL(fileURLWithPath: $0.path) })
        onOpened?()
    }

    func copyPath(_ hit: Hit? = nil) {
        guard let h = hit ?? selected else { return }
        copyPaths([h])
    }

    /// Copy Path for several: one path per line.
    func copyPaths(_ hits: [Hit]) {
        guard !hits.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(hits.map(\.path).joined(separator: "\n"), forType: .string)
    }
}

// MARK: → into folders, ← back; the choices by keyboard

extension SearchModel {
    /// → while in the results: on a folder, search inside it (in the grid:
    /// the next tile); with the choices focused, the next choice. False: the
    /// field moves its caret as usual.
    func arrowRight() -> Bool {
        if choiceFocus != nil {
            moveChoice(1)
            return true
        }
        guard browsing, !showActions, !isCommandMode, !results.isEmpty else { return false }
        if gridView {
            selection = min(results.count - 1, selection + 1)
            anchor = selection
            return true
        }
        guard let h = selected, h.isDir else { return false }
        let before = query
        let after = FolderNav.entering(h.path, from: query)
        folderTrail.append((before, after))
        query = after
        selection = 0
        browsing = true // still in the results: → again goes deeper, ← back
        return true
    }

    /// ← while in the results: back out of the folder → went into (in the
    /// grid: the previous tile); with the choices focused, the previous one.
    func arrowLeft() -> Bool {
        if choiceFocus != nil {
            moveChoice(-1)
            return true
        }
        guard !showActions, !isCommandMode else { return false }
        if browsing, gridView, !results.isEmpty {
            selection = max(0, selection - 1)
            anchor = selection
            return true
        }
        // Back out while in the results, or with nothing typed (only pills).
        guard browsing || fieldText.trimmingCharacters(in: .whitespaces).isEmpty else { return false }
        guard let last = folderTrail.last, last.after == query else {
            folderTrail.removeAll() // the search changed since: nothing to go back to
            return false
        }
        folderTrail.removeLast()
        query = last.before
        selection = 0
        browsing = true
        return true
    }

    var flatChoices: [(group: RefineGroup, option: RefineOption)] { ChoiceNav.flat(visibleRefinements) }

    /// Tab with nothing to complete: into the choices.
    func focusChoices() {
        guard showsRefinements, !flatChoices.isEmpty else { return }
        choiceFocus = 0
    }

    func moveChoice(_ delta: Int) {
        let n = flatChoices.count
        guard n > 0, let f = choiceFocus else { choiceFocus = nil; return }
        choiceFocus = (min(f, n - 1) + delta + n) % n
    }

    /// Tab or ↑↓ with the choices focused: the next or previous group.
    func moveChoiceGroup(_ delta: Int) {
        guard let f = choiceFocus else { return }
        choiceFocus = ChoiceNav.groupStart(visibleRefinements, from: f, delta: delta)
    }

    /// Return on a focused choice: choose it (it becomes a pill).
    func chooseFocusedChoice() {
        let all = flatChoices
        guard let f = choiceFocus, all.indices.contains(f) else { choiceFocus = nil; return }
        choiceFocus = nil
        applyRefinement(all[f].option, in: all[f].group)
    }
}

// MARK: the timeline strip

extension SearchModel {
    /// The strip shows when the results span more than one month.
    var showsTimeline: Bool { timeline.count > 1 && !isHome && !isCommandMode && timeline.contains { $0.count > 0 } }

    /// A click (one month) or a drag (a span) on the strip: the results from
    /// then, in place of any date filter.
    func chooseMonths(from a: String, to b: String) {
        query = TimelineLogic.applying(TimelineLogic.token(from: a, to: b), to: query)
    }
}
