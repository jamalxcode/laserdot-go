import AppKit
import LaserdotKit

/// ⌘K: what can be done with the selected result(s), and the file
/// operations behind it (each one undoable from the banner or ⌥⌘Z).
extension SearchModel {
    /// ⌘K as shown: the common actions first, then "Open With…" and "More
    /// Actions…", which open in place (the owner's review: a shorter list,
    /// nothing taken away). Every action keeps its shortcut.
    var actions: [ActionItem] {
        let all = allActions
        let primary: Set<String> = ["open", "quicklook", "reveal", "copyPath", "moreLike", "searchIn", "trash"]
        let withApps = all.filter { $0.id.hasPrefix("with:") }
        var out: [ActionItem] = []
        for a in all where a.id == "open" { out.append(a) }
        if !withApps.isEmpty {
            if actionsExpanded.contains("openWith") {
                out += withApps
            } else {
                out.append(ActionItem(id: "group:openWith", title: L("action.openWithMenu"), symbol: "square.grid.2x2", keepsOpen: true) { [weak self] in
                    self?.expandActions("openWith")
                })
            }
        }
        out += all.filter { primary.contains($0.id) && $0.id != "open" }
        let rest = all.filter { !primary.contains($0.id) && !$0.id.hasPrefix("with:") }
        if !rest.isEmpty {
            if actionsExpanded.contains("more") {
                out += rest
            } else {
                out.append(ActionItem(id: "group:more", title: L("action.moreMenu"), symbol: "ellipsis.circle", keepsOpen: true) { [weak self] in
                    self?.expandActions("more")
                })
            }
        }
        return out
    }

    /// Opens a ⌘K group in place, its first action highlighted.
    func expandActions(_ group: String) {
        let before = actions.firstIndex { $0.id == "group:" + group } ?? actionIndex
        actionsExpanded.insert(group)
        actionIndex = before
    }

    /// Every action for the selection (⌘K shows them grouped).
    var allActions: [ActionItem] {
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
                    self?.onOpened?()
                })
            }
        }
        list += [
            ActionItem(id: "quicklook", title: L("action.quickLook"), symbol: "eye", shortcut: "⌘Y") { [weak self] in self?.quickLook(first) },
            ActionItem(id: "reveal", title: L("action.reveal"), symbol: "folder", shortcut: "⌘↩") { [weak self] in
                Finder.reveal(urls)
                self?.onOpened?()
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
        list += [
            ActionItem(id: "terminal", title: L("action.terminal"), symbol: "terminal") { [weak self] in
                // The folder itself, or the folder a file is in.
                let dir = first.isDir && first.kind != "app" ? urls[0] : urls[0].deletingLastPathComponent()
                if let terminal = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.apple.Terminal") {
                    NSWorkspace.shared.open([dir], withApplicationAt: terminal, configuration: NSWorkspace.OpenConfiguration())
                }
                self?.onOpened?()
            },
            ActionItem(id: "searchIn", title: L("action.searchIn"), symbol: "magnifyingglass") { [weak self] in
                let dir = first.isDir && first.kind != "app" ? first.path : (first.path as NSString).deletingLastPathComponent
                self?.searchInFolder(dir)
            },
            ActionItem(id: "moreLike", title: L("action.moreLike"), symbol: "square.stack.3d.down.right") { [weak self] in
                self?.moreLike(path: first.path)
            },
            ActionItem(id: "copyLink", title: L("action.copyLink"), symbol: "link.badge.plus") {
                let links = zip(hits, urls).map { h, u in "[\(h.name)](\(u.absoluteString))" }
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(links.joined(separator: "\n"), forType: .string)
            },
            ActionItem(id: "tags", title: L("action.tags"), symbol: "tag") { [weak self] in self?.editTags(hits) },
            ActionItem(id: "compress", title: L("action.compress"), symbol: "doc.zipper") { [weak self] in self?.compress(hits) },
            ActionItem(id: "moveTo", title: L("action.moveTo"), symbol: "folder.badge.gearshape") { [weak self] in self?.move(hits) },
        ]
        if !many {
            list.append(ActionItem(id: "rename", title: L("action.rename"), symbol: "pencil") { [weak self] in self?.rename(first) })
        } else {
            list.append(ActionItem(id: "renameMany", title: String(format: L("action.renameMany"), hits.count), symbol: "pencil.line") { [weak self] in
                self?.renameMany(hits)
            })
        }
        list.append(ActionItem(id: "trash", title: L("action.trash"), symbol: "trash") { [weak self] in self?.trash(hits) })
        return list
    }

    func rename(_ h: Hit) {
        let a = NSAlert()
        a.messageText = String(format: L("rename.title"), h.name)
        let field = NSTextField(string: h.name)
        field.frame = NSRect(x: 0, y: 0, width: 300, height: 24)
        a.accessoryView = field
        a.addButton(withTitle: L("action.rename"))
        a.addButton(withTitle: L("action.cancel"))
        a.window.initialFirstResponder = field
        guard a.runFront() == .alertFirstButtonReturn else { return }
        if let error = renameFile(h, to: field.stringValue) { NSAlert(error: error).runFront() }
    }

    /// Rename several files from a pattern, with a preview; one undo step.
    func renameMany(_ hits: [Hit]) {
        let a = NSAlert()
        a.messageText = String(format: L("renameMany.title"), hits.count)
        a.informativeText = L("renameMany.help")
        let preview = RenamePreview(files: hits.map(\.name))
        a.accessoryView = preview.view
        a.addButton(withTitle: L("action.rename"))
        a.addButton(withTitle: L("action.cancel"))
        a.window.initialFirstResponder = preview.pattern
        guard a.runFront() == .alertFirstButtonReturn else { return }
        if let error = renameFiles(hits, pattern: preview.pattern.stringValue, start: preview.startNumber) {
            NSAlert(error: error).runFront()
        }
    }

    /// Renames several files without asking (the self-test uses it): all or
    /// nothing — a name in use or a bad pattern renames none.
    @discardableResult
    func renameFiles(_ hits: [Hit], pattern: String, start: Int = 1) -> Error? {
        let names = BatchRename.names(for: hits.map(\.name), pattern: pattern, start: start)
        if let p = BatchRename.problem(names) {
            return NSError(domain: "Laserdot", code: 1, userInfo: [NSLocalizedDescriptionKey: L("renameMany.problem." + p)])
        }
        let plan = zip(hits, names).map { h, n -> (URL, URL) in
            let from = URL(fileURLWithPath: h.path)
            return (from, from.deletingLastPathComponent().appendingPathComponent(n))
        }.filter { $0.0 != $0.1 }
        let sources = Set(plan.map(\.0.path))
        if let taken = plan.first(where: { FileManager.default.fileExists(atPath: $0.1.path) && !sources.contains($0.1.path) }) {
            return NSError(domain: "Laserdot", code: 2,
                           userInfo: [NSLocalizedDescriptionKey: String(format: L("renameMany.taken"), taken.1.lastPathComponent)])
        }
        // Through temporary names first, so "a→b, b→a" swaps work.
        var done: [(URL, URL)] = []
        do {
            var temps: [(URL, URL, URL)] = []
            for (from, to) in plan {
                let tmp = from.deletingLastPathComponent().appendingPathComponent(".laserdot-rename-\(UUID().uuidString)")
                try FileManager.default.moveItem(at: from, to: tmp)
                temps.append((from, tmp, to))
            }
            for (from, tmp, to) in temps {
                try FileManager.default.moveItem(at: tmp, to: to)
                done.append((from, to))
            }
        } catch {
            return error
        }
        offerUndo(UndoStep(title: String(format: L("undo.renamedMany"), done.count), moves: done))
        refreshSoon(touching: done.map(\.0.path))
        return nil
    }

    /// Renames without asking (the ⌘K action and the self-test use it).
    @discardableResult
    func renameFile(_ h: Hit, to newName: String) -> Error? {
        let name = newName.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty, name != h.name, !name.contains("/") else { return nil }
        let from = URL(fileURLWithPath: h.path)
        let to = from.deletingLastPathComponent().appendingPathComponent(name)
        do {
            try FileManager.default.moveItem(at: from, to: to)
            offerUndo(UndoStep(title: String(format: L("undo.renamed"), name), moves: [(from, to)]))
        } catch {
            return error
        }
        refreshSoon(touching: [from.path])
        return nil
    }

    /// A zip next to the first file (one file: name.zip; several: Archive.zip),
    /// as Finder's Compress does.
    private func compress(_ hits: [Hit]) {
        compressFiles(hits) { dest in
            if let dest { NSWorkspace.shared.activateFileViewerSelecting([dest]) }
        }
    }

    func compressFiles(_ hits: [Hit], done: @escaping (URL?) -> Void) {
        guard let first = hits.first else { return done(nil) }
        let dir = URL(fileURLWithPath: first.path).deletingLastPathComponent()
        let base = hits.count == 1 ? first.name : L("action.archiveName")
        var dest = dir.appendingPathComponent(base + ".zip")
        var n = 2
        while FileManager.default.fileExists(atPath: dest.path) {
            dest = dir.appendingPathComponent("\(base) \(n).zip")
            n += 1
        }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/ditto")
        p.arguments = hits.count == 1
            ? ["-c", "-k", "--sequesterRsrc", "--keepParent", first.path, dest.path]
            : ["-c", "-k", "--sequesterRsrc"] + hits.map(\.path) + [dest.path]
        p.terminationHandler = { [weak self] proc in
            DispatchQueue.main.async {
                let made = proc.terminationStatus == 0 ? dest : nil
                if let made {
                    self?.offerUndo(UndoStep(title: String(format: L("undo.compressed"), made.lastPathComponent),
                                             created: [made]))
                }
                self?.refreshSoon(touching: [dest.path])
                done(made)
            }
        }
        do { try p.run() } catch { done(nil) }
    }

    /// The place menu's Choose Folder…: searches look there from now on.
    func choosePlaceFolder() {
        NSApp.activate(ignoringOtherApps: true)
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.prompt = L("settings.searchIn.choose")
        guard panel.runFront() == .OK, let folder = panel.url else { return }
        setPlace(folder.path)
        focusSearch()
    }

    /// Move to a folder chosen in a panel.
    private func move(_ hits: [Hit]) {
        NSApp.activate(ignoringOtherApps: true)
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.prompt = L("action.moveHere")
        guard panel.runFront() == .OK, let folder = panel.url else { return }
        if let error = moveFiles(hits, to: folder) { NSAlert(error: error).runFront() }
    }

    @discardableResult
    func moveFiles(_ hits: [Hit], to folder: URL) -> Error? {
        var moves: [(URL, URL)] = []
        var failure: Error?
        for h in hits {
            let from = URL(fileURLWithPath: h.path)
            let to = folder.appendingPathComponent(from.lastPathComponent)
            do {
                try FileManager.default.moveItem(at: from, to: to)
                moves.append((from, to))
            } catch {
                failure = error
                break
            }
        }
        if !moves.isEmpty {
            offerUndo(UndoStep(title: String(format: L("undo.moved"), moves.count, folder.lastPathComponent), moves: moves))
        }
        refreshSoon(touching: moves.flatMap { [$0.0.path, $0.1.path] })
        return failure
    }

    /// Move to the Trash (Finder's ⌘⌫; recoverable from the Trash, or Undo).
    func trash(_ hits: [Hit]) {
        trashFiles(hits) { error in
            if let error { NSAlert(error: error).runFront() }
        }
    }

    func trashFiles(_ hits: [Hit], done: @escaping (Error?) -> Void = { _ in }) {
        trashPaths(hits.map(\.path), done: done)
    }

    /// Move paths to the Trash, with the same Undo as results (the Storage
    /// window's Move to Trash).
    func trashPaths(_ paths: [String], done: @escaping (Error?) -> Void = { _ in }) {
        let urls = paths.map { URL(fileURLWithPath: $0) }
        NSWorkspace.shared.recycle(urls) { [weak self] newURLs, error in
            DispatchQueue.main.async {
                let moves = urls.compactMap { u in newURLs[u].map { (u, $0) } }
                if !moves.isEmpty {
                    self?.offerUndo(UndoStep(title: String(format: L("undo.trashed"), moves.count), moves: moves))
                }
                self?.refreshSoon(touching: urls.map(\.path))
                done(error)
            }
        }
    }

    /// ⌘K "More Like This": the search becomes files like this one (same
    /// type, similar size, near date), as words that can be changed.
    func moreLike(path: String) {
        let engine = self.engine
        showActions = false
        guideQueue.async { [weak self] in
            guard let q = engine.moreLike(path: path), !q.isEmpty else { return }
            DispatchQueue.main.async { self?.query = q + " " }
        }
    }

    /// ⌘K "Search in This Folder", or a folder dropped on the window.
    func searchInFolder(_ path: String) {
        let escaped = path.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
        query = "in:\"\(escaped)\" "
        showActions = false
    }

    /// A folder (or a file: its folder) dropped on the window: search inside it.
    func handleDrop(_ providers: [NSItemProvider]) -> Bool {
        guard let p = providers.first, p.canLoadObject(ofClass: URL.self) else { return false }
        _ = p.loadObject(ofClass: URL.self) { [weak self] url, _ in
            guard let url else { return }
            var isDir: ObjCBool = false
            FileManager.default.fileExists(atPath: url.path, isDirectory: &isDir)
            let dir = isDir.boolValue ? url.path : url.deletingLastPathComponent().path
            DispatchQueue.main.async {
                self?.searchInFolder(dir)
                self?.focusSearch()
            }
        }
        return true
    }

    // MARK: Undo

    func offerUndo(_ step: UndoStep) {
        undoStack.append(step)
        undoStack = Array(undoStack.suffix(UndoStep.keep))
        UndoStep.save(undoStack)
        let id = step.id
        // The banner goes away after a while; ⌥⌘Z still works.
        DispatchQueue.main.asyncAfter(deadline: .now() + 12) { [weak self] in
            if self?.undoStep?.id == id { self?.undoBannerVisible = false }
        }
        undoBannerVisible = true
    }

    /// Reverses the newest step: moved, renamed and trashed files go back,
    /// a new zip goes to the Trash, tags are put back. Pressing again
    /// reverses the one before.
    @discardableResult
    func undo() -> Bool {
        guard let step = undoStack.popLast() else { return false }
        UndoStep.save(undoStack)
        undoBannerVisible = false
        let fm = FileManager.default
        var ok = true
        for m in step.moves.reversed() {
            // Only if the file is still where it went, and nothing took its old place.
            guard fm.fileExists(atPath: m.to), !fm.fileExists(atPath: m.from) else { ok = false; continue }
            do {
                try fm.moveItem(atPath: m.to, toPath: m.from)
            } catch {
                ok = false
            }
        }
        for t in step.tags {
            do { try Self.setFinderTags(t.old, on: t.path) } catch { ok = false }
        }
        let created = step.created.map { URL(fileURLWithPath: $0) }.filter { fm.fileExists(atPath: $0.path) }
        if !created.isEmpty {
            NSWorkspace.shared.recycle(created) { [weak self] _, _ in
                DispatchQueue.main.async { self?.refreshSoon(touching: created.map(\.path)) }
            }
        }
        if !ok {
            let a = NSAlert()
            a.messageText = L("undo.failed")
            a.runFront()
        }
        refreshSoon(touching: step.moves.flatMap { [$0.from, $0.to] })
        return ok
    }

    /// ⌘R: look at every indexed folder again (deleted files leave the
    /// index), then search again.
    func refreshIndex() {
        guard !refreshing else { return }
        refreshing = true
        let engine = self.engine
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let outcome = try? engine.refresh()
            DispatchQueue.main.async {
                self?.refreshing = false
                if let o = outcome {
                    self?.refreshNote = String(format: L("refresh.done"), Int64(o.removed), Int64(o.added))
                    DispatchQueue.main.asyncAfter(deadline: .now() + 6) { self?.refreshNote = nil }
                }
                self?.queryChanged()
            }
        }
    }

    /// The daemon's watcher picks the change up; search again a moment later.
    /// After the app changed files: the folders they were (and are) in are
    /// listed again by the engine at once — on a drive too, which has no
    /// change events — then the search runs again, so nothing gone stays.
    static let touchQueue = DispatchQueue(label: "laserdot.touched", qos: .utility)

    func refreshSoon(touching paths: [String] = []) {
        let dirs = Set(paths.map { ($0 as NSString).deletingLastPathComponent })
        guard !dirs.isEmpty else {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) { [weak self] in self?.queryChanged() }
            return
        }
        let engine = self.engine
        // Its own queue: telling the daemon waits for the index writer, which a
        // big indexing run can hold, and searches must not queue behind that.
        Self.touchQueue.async { [weak self] in
            for d in dirs { engine.touched(folder: d) }
            DispatchQueue.main.async { self?.queryChanged() }
        }
    }

    var selected: Hit? { results.indices.contains(selection) ? results[selection] : nil }

    /// ⌘1–⌘9: open the nth result on screen.
    func openNumbered(_ n: Int) {
        guard results.indices.contains(n - 1) else { return }
        selection = n - 1
        open(results[n - 1])
    }
}

/// One file operation that Undo can reverse.
struct UndoStep: Codable {
    struct Move: Codable { var from: String; var to: String }
    struct TagChange: Codable { var path: String; var old: [String] }

    var id = UUID()
    var date = Date()
    let title: String
    var moves: [Move] = []
    var created: [String] = []
    var tags: [TagChange] = []

    init(title: String, moves: [(URL, URL)] = [], created: [URL] = [], tags: [TagChange] = []) {
        self.title = title
        self.moves = moves.map { Move(from: $0.0.path, to: $0.1.path) }
        self.created = created.map(\.path)
        self.tags = tags
    }

    static let keep = 20
    static let key = "laserdot.undo"
    static let maxAge: TimeInterval = 7 * 86400

    static func load() -> [UndoStep] {
        guard let data = UserDefaults.standard.data(forKey: key),
              let steps = try? JSONDecoder().decode([UndoStep].self, from: data) else { return [] }
        return steps.filter { Date().timeIntervalSince($0.date) < maxAge }
    }

    static func save(_ steps: [UndoStep]) {
        if let data = try? JSONEncoder().encode(steps) { UserDefaults.standard.set(data, forKey: key) }
    }
}
