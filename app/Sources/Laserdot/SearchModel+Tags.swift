import AppKit
import LaserdotKit

/// ⌘K ▸ Tags…: Finder tags for the selected files (undoable).
extension SearchModel {
    /// The standard Finder tags, in the user's language (Red, Orange…).
    static var standardTags: [String] {
        // fileLabels: index 0 is "None", then the colors.
        Array(NSWorkspace.shared.fileLabels.dropFirst()).reversed()
    }

    static func tags(of path: String) -> [String] {
        (try? URL(fileURLWithPath: path).resourceValues(forKeys: [.tagNamesKey]).tagNames) ?? []
    }

    /// Writes a file's Finder tags (NSURL's setter works before macOS 26).
    static func setFinderTags(_ tags: [String], on path: String) throws {
        try (URL(fileURLWithPath: path) as NSURL).setResourceValue(tags, forKey: .tagNamesKey)
    }

    func editTags(_ hits: [Hit]) {
        guard !hits.isEmpty else { return }
        let current = hits.map { Set(Self.tags(of: $0.path)) }
        let onAll = current.dropFirst().reduce(current[0]) { $0.intersection($1) }
        let known = Self.standardTags + current.flatMap { $0 }.filter { !Self.standardTags.contains($0) }.sorted()
        var seen = Set<String>()
        let names = known.filter { seen.insert($0).inserted }

        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = hits.count == 1 ? String(format: L("tags.title"), hits[0].name)
                                        : String(format: L("tags.titleMany"), hits.count)
        let boxes = names.map { name -> NSButton in
            let b = NSButton(checkboxWithTitle: name, target: nil, action: nil)
            b.state = onAll.contains(name) ? .on : .off
            return b
        }
        let field = NSTextField(string: "")
        field.placeholderString = L("tags.new")
        field.frame = NSRect(x: 0, y: 0, width: 240, height: 24)
        let stack = NSStackView(views: boxes + [field])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 4
        stack.frame = NSRect(x: 0, y: 0, width: 240, height: CGFloat(boxes.count) * 22 + 30)
        a.accessoryView = stack
        a.addButton(withTitle: L("tags.save"))
        a.addButton(withTitle: L("action.cancel")).keyEquivalent = "\u{1b}"
        guard a.runFront() == .alertFirstButtonReturn else { return }
        var add = Set(zip(names, boxes).filter { $0.1.state == .on }.map(\.0))
        let typed = field.stringValue.trimmingCharacters(in: .whitespaces)
        if !typed.isEmpty { add.insert(typed) }
        let remove = onAll.subtracting(add)
        if let error = setTags(hits, adding: add, removing: remove) { NSAlert(error: error).runFront() }
    }

    /// Adds and removes tags on each file (the self-test calls this directly).
    @discardableResult
    func setTags(_ hits: [Hit], adding add: Set<String>, removing remove: Set<String>) -> Error? {
        var changes: [UndoStep.TagChange] = []
        var failure: Error?
        for h in hits {
            let old = Self.tags(of: h.path)
            var new = old.filter { !remove.contains($0) }
            for t in add.sorted() where !new.contains(t) { new.append(t) }
            guard new != old else { continue }
            do {
                try Self.setFinderTags(new, on: h.path)
                changes.append(UndoStep.TagChange(path: h.path, old: old))
            } catch {
                failure = error
            }
        }
        if !changes.isEmpty {
            offerUndo(UndoStep(title: String(format: L("undo.tagged"), changes.count), tags: changes))
        }
        refreshSoon()
        return failure
    }
}
