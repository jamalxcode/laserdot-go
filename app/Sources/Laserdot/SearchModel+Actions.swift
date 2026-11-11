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

    /// A zip next to the first file (one file: name.zip; several: Archive.zip),
    /// as Finder's Compress does.
    private func compress(_ hits: [Hit]) {
        guard let first = hits.first else { return }
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
                if proc.terminationStatus == 0 {
                    NSWorkspace.shared.activateFileViewerSelecting([dest])
                }
                self?.refreshSoon()
            }
        }
        try? p.run()
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
        for h in hits {
            let from = URL(fileURLWithPath: h.path)
            do {
                try FileManager.default.moveItem(at: from, to: folder.appendingPathComponent(from.lastPathComponent))
            } catch {
                NSAlert(error: error).runFront()
                break
            }
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
