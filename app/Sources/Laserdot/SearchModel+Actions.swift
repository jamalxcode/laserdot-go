import AppKit
import LaserdotKit

/// ⌘K: what can be done with the selected result(s), and the file
/// operations behind it (each one undoable from the banner or ⌥⌘Z).
extension SearchModel {
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
        list += [
            ActionItem(id: "terminal", title: L("action.terminal"), symbol: "terminal") {
                // The folder itself, or the folder a file is in.
                let dir = first.isDir && first.kind != "app" ? urls[0] : urls[0].deletingLastPathComponent()
                if let terminal = NSWorkspace.shared.urlForApplication(withBundleIdentifier: "com.apple.Terminal") {
                    NSWorkspace.shared.open([dir], withApplicationAt: terminal, configuration: NSWorkspace.OpenConfiguration())
                }
            },
            ActionItem(id: "searchIn", title: L("action.searchIn"), symbol: "magnifyingglass") { [weak self] in
                let dir = first.isDir && first.kind != "app" ? first.path : (first.path as NSString).deletingLastPathComponent
                self?.searchInFolder(dir)
            },
            ActionItem(id: "copyLink", title: L("action.copyLink"), symbol: "link.badge.plus") {
                let links = zip(hits, urls).map { h, u in "[\(h.name)](\(u.absoluteString))" }
                NSPasteboard.general.clearContents()
                NSPasteboard.general.setString(links.joined(separator: "\n"), forType: .string)
            },
            ActionItem(id: "compress", title: L("action.compress"), symbol: "doc.zipper") { [weak self] in self?.compress(hits) },
            ActionItem(id: "moveTo", title: L("action.moveTo"), symbol: "folder.badge.gearshape") { [weak self] in self?.move(hits) },
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
        if let error = renameFile(h, to: field.stringValue) { NSAlert(error: error).runFront() }
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
        refreshSoon()
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
                self?.refreshSoon()
                done(made)
            }
        }
        do { try p.run() } catch { done(nil) }
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
        refreshSoon()
        return failure
    }

    /// Move to the Trash (Finder's ⌘⌫; recoverable from the Trash, or Undo).
    private func trash(_ hits: [Hit]) {
        trashFiles(hits) { error in
            if let error { NSAlert(error: error).runFront() }
        }
    }

    func trashFiles(_ hits: [Hit], done: @escaping (Error?) -> Void = { _ in }) {
        let urls = hits.map { URL(fileURLWithPath: $0.path) }
        NSWorkspace.shared.recycle(urls) { [weak self] newURLs, error in
            DispatchQueue.main.async {
                let moves = urls.compactMap { u in newURLs[u].map { (u, $0) } }
                if !moves.isEmpty {
                    self?.offerUndo(UndoStep(title: String(format: L("undo.trashed"), moves.count), moves: moves))
                }
                self?.refreshSoon()
                done(error)
            }
        }
    }

    /// ⌘K "Search in This Folder", or a folder dropped on the window.
    func searchInFolder(_ path: String) {
        let escaped = path.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
        query = "in:\"\(escaped)\" "
        showActions = false
    }

    // MARK: Undo

    func offerUndo(_ step: UndoStep) {
        undoStep = step
        let id = step.id
        // The banner goes away after a while; ⌥⌘Z still works until the next action.
        DispatchQueue.main.asyncAfter(deadline: .now() + 12) { [weak self] in
            if self?.undoStep?.id == id { self?.undoBannerVisible = false }
        }
        undoBannerVisible = true
    }

    /// Puts things back: moved/renamed/trashed files return, a new zip goes to the Trash.
    @discardableResult
    func undo() -> Bool {
        guard let step = undoStep else { return false }
        undoStep = nil
        undoBannerVisible = false
        var ok = true
        for (from, to) in step.moves.reversed() {
            do {
                guard !FileManager.default.fileExists(atPath: from.path) else { ok = false; continue }
                try FileManager.default.moveItem(at: to, to: from)
            } catch {
                ok = false
            }
        }
        if !step.created.isEmpty {
            NSWorkspace.shared.recycle(step.created) { [weak self] _, _ in
                DispatchQueue.main.async { self?.refreshSoon() }
            }
        }
        if !ok {
            let a = NSAlert()
            a.messageText = L("undo.failed")
            a.runFront()
        }
        refreshSoon()
        return ok
    }

    /// The daemon's watcher picks the change up; search again a moment later.
    func refreshSoon() {
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) { [weak self] in self?.queryChanged() }
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
struct UndoStep {
    let id = UUID()
    let title: String
    var moves: [(URL, URL)] = []
    var created: [URL] = []
}
