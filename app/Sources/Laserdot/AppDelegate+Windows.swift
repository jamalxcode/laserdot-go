import AppKit
import LaserdotKit
import Quartz
import ServiceManagement
import SwiftUI

/// The app's windows, links, menus and Erase All Data (AppDelegate, continued).
extension AppDelegate {
    /// Library ▸ Storage: what fills the disk.
    func showStorage() {
        guard let engine else { return }
        if storageWindow == nil {
            let model = StorageModel(engine: engine)
            storageModel = model
            let w = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 680, height: 560),
                styleMask: [.titled, .closable, .resizable, .miniaturizable], backing: .buffered, defer: false)
            w.title = L("menu.storageTitle")
            w.isReleasedWhenClosed = false
            w.contentMinSize = NSSize(width: 520, height: 380)
            w.contentView = NSHostingView(rootView: StorageView(model: model).environment(\.layoutDirection, appLayoutDirection))
            w.center()
            w.setFrameAutosaveName("Storage")
            storageWindow = w
        }
        NSApp.activate(ignoringOtherApps: true)
        storageWindow?.makeKeyAndOrderFront(nil)
        storageModel?.load()
    }


    @discardableResult
    func handle(_ url: URL) -> Bool {
        guard url.scheme == "laserdot" else { return false }
        switch url.host ?? "" {
        case "search":
            let q = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "q" }?.value ?? ""
            overlay?.show()
            model?.query = q
        case "storage": showStorage()
        case "duplicates": showDuplicates()
        case "settings": showSettings()
        default: return false
        }
        return true
    }

    /// Laserdot opens at login unless the user turns it off: an index that
    /// is only updated while the app runs would go stale after a restart.
    /// Asked once, and only for the copy in Applications.
    func openAtLoginByDefault() {
        let key = "laserdot.loginItemDefaulted"
        guard !UserDefaults.standard.bool(forKey: key), Bundle.main.bundlePath.hasPrefix("/Applications/") else { return }
        UserDefaults.standard.set(true, forKey: key)
        if SMAppService.mainApp.status != .enabled { try? SMAppService.mainApp.register() }
    }

    /// First launch: the welcome tour. A launch after an update: "What's new".
    func noteVersion() {
        let current = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
        let last = UserDefaults.standard.string(forKey: "laserdot.lastVersion")
        if !UserDefaults.standard.bool(forKey: "laserdot.welcomed") {
            UserDefaults.standard.set(true, forKey: "laserdot.welcomed")
            showTour = true
        } else if let last, last != current, Updater.notes(in: Bundle.main.bundleURL, since: last) != nil {
            // Updated, with something people will notice (fixes-only
            // versions are marked quiet and say nothing).
            UserDefaults.standard.set(last, forKey: "laserdot.whatsNewSince")
            whatsNewPending = true
        }
        UserDefaults.standard.set(current, forKey: "laserdot.lastVersion")
    }

    /// Help ▸ What's New: after an update, every version since the one the
    /// user had (not only the last); otherwise this version's notes.
    func showWhatsNew() {
        let since = whatsNewPending ? UserDefaults.standard.string(forKey: "laserdot.whatsNewSince") : nil
        whatsNewPending = false
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
        let a = NSAlert()
        a.messageText = String(format: L("whatsnew.title"), version)
        a.informativeText = Updater.notes(in: Bundle.main.bundleURL, since: since) ?? L("whatsnew.none")
        a.runFront()
    }

    /// Help ▸ Save Diagnostics: a text file to attach to a bug report —
    /// versions, index counts and the search engine's recent log, with every
    /// path replaced by <path> (no file or folder names). Nothing is sent.
    func saveDiagnostics() {
        guard let engine else { return }
        NSApp.activate(ignoringOtherApps: true)
        let panel = NSSavePanel()
        panel.nameFieldStringValue = "Laserdot diagnostics.txt"
        panel.message = L("diagnostics.message")
        guard panel.runFront() == .OK, let url = panel.url else { return }
        let (launch, ready, steps) = (launchMS, readyMS, LaunchTimer.summary)
        DispatchQueue.global().async {
            let s = engine.status()
            let info = ProcessInfo.processInfo
            let app = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "?"
            let scrub: (String) -> String = { line in
                line.replacingOccurrences(of: #"/[^\s"',)]+"#, with: "<path>", options: .regularExpression)
            }
            var lines = [
                "Laserdot \(app)",
                "macOS \(info.operatingSystemVersionString)",
                "laserdotd \(s.version) (protocol \(s.protocolVersion)), running: \(s.running)",
                "network denied by macOS: \(s.networkDenied)",
                "watcher: \(s.watcher)",
                "entries: \(s.entries), indexed folders: \(s.folders.count), excluded: \(engine.exclusions().count)",
                "indexing now: \(s.indexing.map { "\($0.phase) \($0.done)/\($0.total)" } ?? "no")",
                "drives: \(((try? engine.drives()) ?? []).map(\.state).joined(separator: ", "))",
                "launch: \(launch) ms to the menu bar, \(ready) ms until the index answered",
                "launch steps (ms since start): \(steps)",
                "couldn't read: \((try? engine.unreadable())?.total ?? 0) items",
                "",
                "Recent laserdotd log (paths removed):",
            ]
            lines += engine.logTail().map(scrub)
            try? lines.joined(separator: "\n").write(to: url, atomically: true, encoding: .utf8)
            DispatchQueue.main.async { NSWorkspace.shared.activateFileViewerSelecting([url]) }
        }
    }


    /// Settings, in front and active — also from the menu-bar icon, when
    /// Laserdot is not the active app (it would open behind other windows).
    func showSettings() {
        NSApp.activate(ignoringOtherApps: true)
        if let open = openSettingsAction {
            open()
        } else {
            NSApp.sendAction(Selector(("showSettingsWindow:")), to: nil, from: nil)
        }
        // Bring it forward once it exists (it may be created a moment later).
        for delay in [0.05, 0.3] {
            DispatchQueue.main.asyncAfter(deadline: .now() + delay) {
                guard let w = NSApp.windows.first(where: { $0.identifier?.rawValue.contains("Settings") == true && $0.isVisible })
                else { return }
                NSApp.activate(ignoringOtherApps: true)
                w.makeKeyAndOrderFront(nil)
                w.orderFrontRegardless()
            }
        }
    }

    /// What `>` in the search box offers: the menus' actions, typed.
    func commands() -> [ActionItem] {
        var list: [ActionItem] = [
            ActionItem(id: "dupes", title: L("menu.duplicates"), symbol: "square.on.square") { [weak self] in self?.showDuplicates() },
            ActionItem(id: "storage", title: L("menu.storage"), symbol: "chart.bar.xaxis", shortcut: "⇧⌘S") { [weak self] in self?.showStorage() },
            ActionItem(id: "refresh", title: L("refresh.help"), symbol: "arrow.clockwise", shortcut: "⌘R") { [weak self] in
                self?.model?.refreshIndex()
            },
            ActionItem(id: "index", title: L("menu.index"), symbol: "folder.badge.plus", shortcut: "⌘O") { [weak self] in self?.chooseFolder() },
            ActionItem(id: "syntax", title: L("menu.syntax"), symbol: "questionmark.circle", shortcut: "⌘/") { [weak self] in
                self?.model?.showCheatSheet = true
            },
            ActionItem(id: "settings", title: L("menu.settings"), symbol: "gearshape", shortcut: "⌘,") {
                self.showSettings()
            },
            ActionItem(id: "updates", title: L("menu.checkUpdates"), symbol: "arrow.down.circle") { [weak self] in
                self?.updates.check(interactive: true)
            },
            ActionItem(id: "content", title: L("commands.contentAdd"), symbol: "doc.text.magnifyingglass") { [weak self] in
                self?.content?.addFolder()
            },
        ]
        for d in drives?.drives ?? [] {
            list.append(ActionItem(id: "eject:" + d.mount, title: String(format: L("commands.eject"), d.name), symbol: "eject") { [weak self] in
                self?.drives?.eject(d)
            })
        }
        list.append(ActionItem(id: "quit", title: L("menu.quit"), symbol: "power", shortcut: "⌘Q") { NSApp.terminate(nil) })
        return list
    }
    // MARK: Erase All Laserdot Data

    func confirmEraseAll() {
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.alertStyle = .critical
        a.messageText = L("privacy.erase.title")
        a.informativeText = L("privacy.erase.text")
        a.addButton(withTitle: L("privacy.erase.confirm")).hasDestructiveAction = true
        a.addButton(withTitle: L("action.cancel")).keyEquivalent = "\u{1b}"
        guard a.runFront() == .alertFirstButtonReturn else { return }
        eraseAll(relaunch: true)
    }

    /// Stops the daemon, deletes the data folder (indexes, content words,
    /// history, read-only drive indexes), the remembered passphrases and the
    /// settings, then starts again — without indexing the home folder again
    /// (Folders adds what you want back). Indexes on drives stay on them
    /// (Settings ▸ Drives deletes those).
    func eraseAll(relaunch: Bool) {
        overlay?.hide()
        engine?.retire()
        let dir = (Paths.defaultDbPath as NSString).deletingLastPathComponent
        try? FileManager.default.removeItem(atPath: dir)
        Keychain.forgetAll()
        LocalSigner().forget() // the next update makes a new key (and macOS asks once more)
        if let domain = Bundle.main.bundleIdentifier {
            UserDefaults.standard.removePersistentDomain(forName: domain)
        }
        // Erased, not new: no tour, no automatic indexing.
        UserDefaults.standard.set(true, forKey: "laserdot.welcomed")
        UserDefaults.standard.set(true, forKey: "laserdot.defaultsIndexed")
        UserDefaults.standard.set(Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String,
                                  forKey: "laserdot.lastVersion")
        UserDefaults.standard.synchronize()
        guard relaunch else { return }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", "sleep 1; /usr/bin/open -n \"$0\"", Bundle.main.bundlePath]
        try? p.run()
        NSApp.terminate(nil)
    }
}
