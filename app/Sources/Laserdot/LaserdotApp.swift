import AppKit
import Carbon.HIToolbox
import LaserdotKit
import Quartz
import ServiceManagement
import SwiftUI

/// Laserdot is both a Dock app and a menu-bar app (spec §6.4, §13.1): a main
/// window for searching, the same search as a floating overlay on ⌥Space,
/// the app's menus, and a menu-bar icon with the everyday actions. The index
/// lives in laserdotd, which the app starts and stops.
@main
struct LaserdotApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) var delegate

    var body: some Scene {
        WindowGroup("Laserdot", id: "main") {
            MainWindow(delegate: delegate, updates: delegate.updates)
                .environment(\.layoutDirection, appLayoutDirection)
        }
        // laserdot:// links are handled by the app delegate, not new windows.
        .handlesExternalEvents(matching: [])
        .defaultSize(width: 860, height: 600)
        .commands { LaserdotCommands(delegate: delegate, updates: delegate.updates) }

        Settings {
            SettingsView(delegate: delegate, updates: delegate.updates)
                .environment(\.layoutDirection, appLayoutDirection)
        }

        MenuBarExtra {
            StatusMenu(delegate: delegate, updates: delegate.updates)
        } label: {
            Image(nsImage: menuBarIcon)
        }
    }
}

/// The menu-bar icon's menu.
struct StatusMenu: View {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        Button(L("menu.search")) { delegate.toggleOverlay() }
            .keyboardShortcut(.space, modifiers: .option)
        Button(L("menu.showWindow")) {
            NSApp.activate(ignoringOtherApps: true)
            openWindow(id: "main")
        }
        Button(L("menu.index")) { delegate.chooseFolder() }
        Button(L("menu.duplicates")) { delegate.showDuplicates() }
            .disabled(!delegate.dupesAvailable)
        Button(L("menu.storage")) { delegate.showStorage() }
        if let drives = delegate.drives {
            DrivesMenu(model: drives)
        }
        if let content = delegate.content {
            ContentMenu(model: content)
        }
        Divider()
        if let r = updates.ready {
            Button(String(format: L("update.button"), r.latest ?? "")) { updates.offerInstall() }
        }
        Button(L("menu.checkUpdates")) { updates.check(interactive: true) }
            .disabled(updates.checking)
        Button(L("menu.settings")) { delegate.showSettings() }
            .keyboardShortcut(",")
        Divider()
        Text(delegate.statusLine)
        Text(delegate.networkLine)
        Divider()
        Button(L("menu.quit")) { NSApp.terminate(nil) }
            .keyboardShortcut("q")
    }
}

/// The main window: search, and one status bar (results, index size,
/// network, update) — standard window chrome, not the overlay's card.
struct MainWindow: View {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController
    @Environment(\.openSettings) private var openSettings

    var body: some View {
        VStack(spacing: 0) {
            if let model = delegate.model {
                SearchView(model: model, close: { model.query = "" }, fixedSize: false)
            } else {
                ProgressView(L("daemon.starting")).frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            Divider()
            HStack(spacing: 12) {
                if let model = delegate.model {
                    StatusSummary(model: model)
                    Spacer()
                    ShortcutBar(model: model).font(.caption)
                }
                Spacer()
                if updates.checking {
                    ProgressView().controlSize(.small)
                    Text(L("update.checking"))
                }
                if let r = updates.ready {
                    Button(String(format: L("update.button"), r.latest ?? "")) { updates.offerInstall() }
                        .buttonStyle(.borderedProminent)
                        .controlSize(.small)
                }
                if delegate.whatsNewPending {
                    Button(L("menu.whatsNew")) { delegate.showWhatsNew() }
                        .controlSize(.small)
                }
                Text(delegate.statusLine)
                if let model = delegate.model, !delegate.networkLine.isEmpty {
                    NetworkLock(blocked: model.networkBlocked)
                }
            }
            .font(.callout)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
        }
        .frame(minWidth: 640, minHeight: 420)
        // SwiftUI's way to open Settings, kept for the menu-bar menu, `>`
        // commands and the app menu.
        .onAppear { delegate.openSettingsAction = { openSettings() } }
        .sheet(isPresented: $delegate.showTour) { WelcomeTour { delegate.tourClosed() } }
    }
}

/// The menu bar: Check for Updates in the app menu (Settings… comes from the
/// Settings scene), File (index, duplicates), Edit ▸ Find (⌘F), Library
/// (drives, content search) and Help (the search syntax).
struct LaserdotCommands: Commands {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController
    @Environment(\.openWindow) private var openWindow

    var body: some Commands {
        CommandGroup(after: .appInfo) {
            Button(L("menu.checkUpdates")) { updates.check(interactive: true) }
                .disabled(updates.checking)
            if let r = updates.ready {
                Button(String(format: L("update.button"), r.latest ?? "")) { updates.offerInstall() }
            }
        }
        CommandGroup(after: .undoRedo) {
            // ⌥⌘Z works in the search window; this is the same from the menu bar.
            Button(L("menu.undoFile")) { delegate.model?.undo() }
        }
        CommandGroup(after: .pasteboard) {
            Divider()
            Button(L("menu.find")) {
                openWindow(id: "main")
                delegate.model?.focusSearch()
            }
            .keyboardShortcut("f")
        }
        CommandGroup(replacing: .help) {
            Button(L("menu.syntax")) {
                openWindow(id: "main")
                delegate.model?.showCheatSheet = true
            }
            Button(L("menu.tour")) {
                openWindow(id: "main")
                delegate.showTour = true
            }
            Button(L("menu.whatsNew")) { delegate.showWhatsNew() }
            Divider()
            Button(L("menu.diagnostics")) { delegate.saveDiagnostics() }
        }
        CommandGroup(replacing: .newItem) {
            Button(L("menu.search")) { delegate.toggleOverlay() }
            Button(L("menu.index")) { delegate.chooseFolder() }
                .keyboardShortcut("o")
            Button(L("menu.duplicates")) { delegate.showDuplicates() }
                .disabled(!delegate.dupesAvailable)
                .keyboardShortcut("u", modifiers: [.command, .shift])
        }
        CommandMenu(L("menu.library")) {
            Button(L("menu.storage")) { delegate.showStorage() }
                .keyboardShortcut("s", modifiers: [.command, .shift])
            Divider()
            if let drives = delegate.drives {
                DrivesMenu(model: drives)
            }
            if let content = delegate.content {
                ContentMenu(model: content)
            }
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate, ObservableObject {
    @Published var statusLine = L("daemon.starting")
    @Published var networkLine = ""
    @Published var drives: DrivesModel?
    @Published var content: ContentModel?
    /// The daemon implements the duplicate finder (older ones answer
    /// `unsupported`; the menu item is disabled then).
    @Published var dupesAvailable = true
    @Published var folders: FoldersModel?
    /// The welcome tour (first launch, or Help ▸ Welcome Tour).
    @Published var showTour = false
    /// This version's notes, until seen (Help ▸ What's New).
    @Published var whatsNewPending = false
    var sharedEngine: Engine? { engine }
    private var engine: Engine?
    private var duplicates: NSWindow?
    @Published private(set) var model: SearchModel?
    let updates = UpdateController()
    private var overlay: OverlayController?
    private var hotKey: HotKey?
    @Published private(set) var hotKeyChoice = HotKeyChoice.saved

    func applicationDidFinishLaunching(_ notification: Notification) {
        let db = Paths.defaultDbPath
        let sockets = Paths.socketDir(forDb: db)
        try? FileManager.default.createDirectory(atPath: sockets, withIntermediateDirectories: true)
        // The app itself is not OS-sandboxed (owner's decision, spec §7.1):
        // a sandboxed app may only hand files it can read itself to Preview or
        // a player, so opening results needed Full Disk Access. It keeps the
        // no-network rule by its code alone (CI rejects network code outside
        // the update helper). laserdotd, which reads every file, still asks
        // macOS to deny it the network before anything else.
        let daemon = Bundle.main.url(forAuxiliaryExecutable: "laserdotd")?.path
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
        let engine = Engine(dbPath: db, daemonPath: daemon, expectedVersion: version)
        self.engine = engine
        let model = SearchModel(engine: engine)
        self.model = model
        overlay = OverlayController(model: model)
        registerHotKey(prompt: !SelfTest.active)
        model.onStatus = { [weak self] status in
            self?.show(status)
            if !SelfTest.active { self?.indexDefaultsOnce(status) }
        }
        model.commandProvider = { [weak self] in self?.commands() ?? [] }
        model.onIndexing = { [weak self] name in
            self?.statusLine = String(format: L("index.running"), name)
        }
        model.onIndexFailed = { name, reason in
            NSApp.activate(ignoringOtherApps: true)
            let alert = NSAlert()
            alert.alertStyle = .warning
            alert.messageText = String(format: L("index.failed"), name)
            alert.informativeText = reason + "\n\n" + L("index.failedHelp")
            alert.runFront()
        }
        // Nothing to ask on first launch: the default folders are indexed
        // automatically (indexDefaultsOnce).
        model.connect()
        folders = FoldersModel(engine: engine)
        let content = ContentModel(engine: engine)
        self.content = content
        content.refresh()
        let drives = DrivesModel(engine: engine)
        self.drives = drives
        drives.start()
        probeDuplicates()
        updates.stopDaemon = { [weak engine] in engine?.shutdown() }
        if let out = SelfTest.outDir {
            // CI: drive the real app and report what it shows (SelfTest.swift).
            selfTest = SelfTestRunner(app: self, out: out)
            selfTest?.start()
            return
        }
        updates.offerMoveToApplications()
        updates.startDailyChecks()
        openAtLoginByDefault()
        noteVersion()
        power = PowerWatcher { [weak engine] pause in
            DispatchQueue.global().async { engine?.setBackground(paused: pause) }
        }
    }

    private var power: PowerWatcher?
    private(set) var duplicatesModel: DuplicatesModel?
    private var storageWindow: NSWindow?
    private(set) var storageModel: StorageModel?

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

    /// laserdot:// links (Shortcuts' "Open URL", Terminal's `open`):
    ///   laserdot://search?q=photos%20last%20week   the overlay with that search
    ///   laserdot://storage   laserdot://duplicates   laserdot://settings
    func application(_ application: NSApplication, open urls: [URL]) {
        urls.forEach { _ = handle($0) }
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
    private func openAtLoginByDefault() {
        let key = "laserdot.loginItemDefaulted"
        guard !UserDefaults.standard.bool(forKey: key), Bundle.main.bundlePath.hasPrefix("/Applications/") else { return }
        UserDefaults.standard.set(true, forKey: key)
        if SMAppService.mainApp.status != .enabled { try? SMAppService.mainApp.register() }
    }

    /// First launch: the welcome tour. A launch after an update: "What's new".
    private func noteVersion() {
        let current = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
        let last = UserDefaults.standard.string(forKey: "laserdot.lastVersion")
        if !UserDefaults.standard.bool(forKey: "laserdot.welcomed") {
            UserDefaults.standard.set(true, forKey: "laserdot.welcomed")
            showTour = true
        } else if let last, last != current, Updater.whatsNew(in: Bundle.main.bundleURL) != nil {
            whatsNewPending = true
        }
        UserDefaults.standard.set(current, forKey: "laserdot.lastVersion")
    }

    /// Help ▸ What's New: this version's notes.
    func showWhatsNew() {
        whatsNewPending = false
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? ""
        let a = NSAlert()
        a.messageText = String(format: L("whatsnew.title"), version)
        a.informativeText = Updater.whatsNew(in: Bundle.main.bundleURL) ?? L("whatsnew.none")
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
                "",
                "Recent laserdotd log (paths removed):",
            ]
            lines += engine.logTail().map(scrub)
            try? lines.joined(separator: "\n").write(to: url, atomically: true, encoding: .utf8)
            DispatchQueue.main.async { NSWorkspace.shared.activateFileViewerSelecting([url]) }
        }
    }

    private var selfTest: SelfTestRunner?

    /// Set by the main window (SwiftUI's openSettings).
    var openSettingsAction: (() -> Void)?

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

    // Quick Look's controller (the app delegate is at the end of every
    // responder chain): the panel gets its items from QuickLook.shared.
    override func acceptsPreviewPanelControl(_ panel: QLPreviewPanel!) -> Bool { true }

    override func beginPreviewPanelControl(_ panel: QLPreviewPanel!) {
        panel.dataSource = QuickLook.shared
        panel.delegate = QuickLook.shared
    }

    override func endPreviewPanelControl(_ panel: QLPreviewPanel!) {}

    /// What `>` in the search box offers: the menus' actions, typed.
    func commands() -> [ActionItem] {
        var list: [ActionItem] = [
            ActionItem(id: "dupes", title: L("menu.duplicates"), symbol: "square.on.square") { [weak self] in self?.showDuplicates() },
            ActionItem(id: "storage", title: L("menu.storage"), symbol: "chart.bar.xaxis", shortcut: "⇧⌘S") { [weak self] in self?.showStorage() },
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
    var engineForTests: Engine? { engine }
    var overlayForTests: OverlayController? { overlay }

    /// Laserdot started laserdotd; it stops it when it quits.
    func applicationWillTerminate(_ notification: Notification) {
        engine?.shutdown()
        updates.installOnQuit()
    }

    /// Closing the window keeps Laserdot (and ⌥Space) running; the Dock icon
    /// opens a new window.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    /// An empty dry run moves nothing; it only tells whether this daemon
    /// has the duplicate finder.
    private func probeDuplicates() {
        guard let engine else { return }
        DispatchQueue.global(qos: .utility).async { [weak self] in
            var available = true
            do {
                _ = try engine.cleanupPreview(sets: [], folder: nil)
            } catch LaserdotError.unsupported {
                available = false
            } catch {}
            DispatchQueue.main.async { self?.dupesAvailable = available }
        }
    }

    /// The duplicate finder window (spec §4), kept for the session.
    func showDuplicates() {
        guard let engine else { return }
        if duplicates == nil {
            let w = NSWindow(
                contentRect: NSRect(x: 0, y: 0, width: 820, height: 560),
                styleMask: [.titled, .closable, .resizable, .miniaturizable],
                backing: .buffered, defer: false)
            w.title = L("dupes.title")
            w.isReleasedWhenClosed = false
            w.contentMinSize = NSSize(width: 560, height: 380)
            let model = DuplicatesModel(engine: engine)
            duplicatesModel = model
            w.contentView = NSHostingView(rootView: DuplicatesView(model: model)
                .environment(\.layoutDirection, appLayoutDirection))
            w.center()
            w.setFrameAutosaveName("Duplicates") // reopen where the user left it
            duplicates = w
        }
        NSApp.activate(ignoringOtherApps: true)
        duplicates?.makeKeyAndOrderFront(nil)
    }

    /// Register the chosen hot key; if another app owns it (Raycast, Alfred,
    /// Spotlight…), offer the alternatives (spec §6.4).
    private func registerHotKey(prompt: Bool) {
        hotKey = nil
        let c = hotKeyChoice
        hotKey = HotKey(keyCode: c.keyCode, modifiers: c.modifiers) { [weak self] in self?.toggleOverlay() }
        guard hotKey == nil, prompt else { return }
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = String(format: L("hotkey.taken.title"), c.label)
        alert.informativeText = L("hotkey.taken.text")
        let others = HotKeyChoice.all.filter { $0.label != c.label }
        for o in others { alert.addButton(withTitle: o.label) }
        alert.addButton(withTitle: L("drive.notNow"))
        let i = alert.runFront().rawValue - NSApplication.ModalResponse.alertFirstButtonReturn.rawValue
        if others.indices.contains(i) { useHotKey(others[i]) }
    }

    func useHotKey(_ c: HotKeyChoice) {
        hotKeyChoice = c
        c.save()
        registerHotKey(prompt: true)
    }

    /// First launch: nothing is indexed, so searches would find nothing.
    /// Offer the home folder, or a folder of the user's choice.
    /// Like Spotlight, Laserdot searches your files without being set up:
    /// the apps and the home folder (minus ~/Library, except iCloud Drive) are
    /// indexed once, without asking. Each app is one result. Folders removed
    /// later stay removed.
    private func indexDefaultsOnce(_ status: DaemonStatus) {
        let key = "laserdot.defaultsIndexed"
        // Not while the first-launch tour explains the macOS prompts that
        // indexing brings up; closing it starts indexing (tourClosed).
        guard !showTour, status.running, !UserDefaults.standard.bool(forKey: key), let model else { return }
        UserDefaults.standard.set(true, forKey: key)
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        // Apps first: they are quick and what people look for first.
        for path in ["/Applications", "/System/Applications", home] {
            var dir: ObjCBool = false
            guard FileManager.default.fileExists(atPath: path, isDirectory: &dir), dir.boolValue,
                  !status.folders.contains(where: { path == $0 || path.hasPrefix($0 + "/") }) else { continue }
            model.index(folder: URL(fileURLWithPath: path))
        }
    }

    func tourClosed() {
        showTour = false
        guard !SelfTest.active, let engine else { return }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let status = engine.status()
            DispatchQueue.main.async { self?.indexDefaultsOnce(status) }
        }
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

    func toggleOverlay() {
        overlay?.toggle()
    }

    func chooseFolder() {
        NSApp.activate(ignoringOtherApps: true)
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.allowsMultipleSelection = false
        guard panel.runFront() == .OK, let url = panel.url else { return }
        model?.index(folder: url)
    }

    private func show(_ status: DaemonStatus) {
        if status.running {
            statusLine = String(format: L("menu.status"), Int64(status.entries))
        } else {
            statusLine = L("daemon.starting")
        }
        networkLine = status.networkDenied ? L("network.blocked") : L("network.open")
    }
}

/// The app's language for guidance (hints, filter descriptions): the
/// localization the app's own strings use.
let appLang: String = appResources.preferredLocalizations.first ?? "en"

/// The layout direction of the app's language: right to left in Arabic.
/// Set on every window's root view, so the layout follows the strings
/// shown rather than what AppKit derives from the bundle.
let appLayoutDirection: LayoutDirection =
    Locale.Language(identifier: appLang).characterDirection == .rightToLeft ? .rightToLeft : .leftToRight

/// Global shortcuts the overlay can use (spec §6.4).
struct HotKeyChoice {
    let label: String
    let keyCode: UInt32
    let modifiers: UInt32

    static let all = [
        HotKeyChoice(label: "⌥Space", keyCode: UInt32(kVK_Space), modifiers: UInt32(optionKey)),
        HotKeyChoice(label: "⌃Space", keyCode: UInt32(kVK_Space), modifiers: UInt32(controlKey)),
        HotKeyChoice(label: "⌥⌘Space", keyCode: UInt32(kVK_Space), modifiers: UInt32(optionKey | cmdKey)),
        HotKeyChoice(label: "⌃⌥Space", keyCode: UInt32(kVK_Space), modifiers: UInt32(controlKey | optionKey)),
    ]

    static var saved: HotKeyChoice {
        let label = UserDefaults.standard.string(forKey: "laserdot.hotkey")
        return all.first { $0.label == label } ?? all[0]
    }

    func save() {
        UserDefaults.standard.set(label, forKey: "laserdot.hotkey")
    }
}

/// The app's resources (strings in English and Arabic). In Laserdot.app they
/// live in Contents/Resources, the only place code signing allows; SwiftPM's
/// generated `Bundle.module` looks beside the .app instead and stops the app
/// when it is not there, so it is only the fallback for `swift run`.
let appResources: Bundle = {
    let name = "Laserdot_Laserdot.bundle"
    let places = [Bundle.main.resourceURL, Bundle.main.bundleURL, Bundle.main.executableURL?.deletingLastPathComponent()]
    for case let dir? in places {
        if let b = Bundle(url: dir.appendingPathComponent(name)) { return b }
    }
    return Bundle.module
}()

/// Localized string from the app's resources (English + Arabic, spec §14.4).
func L(_ key: String) -> String {
    NSLocalizedString(key, bundle: appResources, comment: "")
}

/// Drives submenu (spec §9.6): one entry per mounted drive, with the
/// actions its state allows. Hidden while the daemon has no drive support.
struct DrivesMenu: View {
    @ObservedObject var model: DrivesModel

    var body: some View {
        if model.available {
            menu
        }
    }

    private var menu: some View {
        Menu(L("menu.drives")) {
            if let busy = model.busy {
                Text(busy)
                Divider()
            }
            if model.drives.isEmpty {
                Text(L("drive.none"))
            }
            ForEach(model.drives, id: \.mount) { d in
                Menu(model.label(d)) {
                    switch d.state {
                    case "attached":
                        Button(L("drive.reveal")) { model.reveal(d) }
                        Button(L("drive.rebuild")) { model.promptIndex(d, rebuild: true) }
                        Button(L("drive.detach")) { model.detach(d) }
                        Button(L("drive.delete")) { model.delete(d) }
                    case "detached":
                        Button(L("drive.attach")) { model.attach(d) }
                        Button(L("drive.delete")) { model.delete(d) }
                    case "locked":
                        Button(L("drive.unlock")) { model.promptUnlock(d) }
                        Button(L("drive.delete")) { model.delete(d) }
                    case "unreadable":
                        Button(L("drive.rebuild")) { model.promptIndex(d, rebuild: true) }
                    default:
                        Button(L("drive.index")) { model.promptIndex(d) }
                    }
                    Divider()
                    Button(L("drive.eject")) { model.eject(d) }
                }
                .disabled(model.busy != nil)
            }
        }
    }
}

/// Content search folders (spec §2.4.3): which folders' files have their
/// text indexed.
final class ContentModel: ObservableObject {
    @Published private(set) var info: ContentInfo?
    /// The daemon implements content search (older ones answer `unsupported`).
    @Published private(set) var available = true
    private let engine: Engine
    private let queue = DispatchQueue(label: "laserdot.content", qos: .utility)

    init(engine: Engine) {
        self.engine = engine
    }

    func refresh() {
        let engine = self.engine
        queue.async { [weak self] in
            let outcome = Result { try engine.contentInfo() }
            DispatchQueue.main.async {
                guard let self else { return }
                self.info = try? outcome.get()
                if case let .failure(e) = outcome { self.available = !e.isUnsupported } else { self.available = true }
            }
        }
    }

    func set(_ folder: String, add: Bool) {
        let engine = self.engine
        queue.async { [weak self] in
            let info = try? engine.setContentFolder(folder: folder, add: add)
            DispatchQueue.main.async { self?.info = info ?? self?.info }
        }
    }

    func setSkipCode(_ skip: Bool) {
        let engine = self.engine
        queue.async { [weak self] in
            let info = try? engine.setContentSkipCode(skip)
            DispatchQueue.main.async { self?.info = info ?? self?.info }
        }
    }

    func addFolder() {
        NSApp.activate(ignoringOtherApps: true)
        let panel = NSOpenPanel()
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        guard panel.runFront() == .OK, let url = panel.url else { return }
        set(url.path, add: true)
    }
}

struct ContentMenu: View {
    @ObservedObject var model: ContentModel

    var body: some View {
        if model.available {
            menu
        }
    }

    private var menu: some View {
        Menu(L("menu.content")) {
            if let info = model.info {
                Text(String(format: L("content.status"), Int64(info.documents), Int64(info.pending)))
                Divider()
                ForEach(info.folders, id: \.self) { f in
                    Menu(URL(fileURLWithPath: f).lastPathComponent) {
                        Text(f)
                        Button(L("content.remove")) { model.set(f, add: false) }
                    }
                }
                Divider()
            }
            Button(L("content.add")) { model.addFolder() }
            Button(L("content.refresh")) { model.refresh() }
        }
    }
}

/// The menu-bar icon: a laser dot in a ring with four ticks, drawn as a
/// template so it follows the menu bar's light and dark look.
let menuBarIcon: NSImage = {
    let img = NSImage(size: NSSize(width: 18, height: 18), flipped: false) { r in
        NSColor.black.set()
        let ring = NSBezierPath(ovalIn: r.insetBy(dx: 3.5, dy: 3.5))
        ring.lineWidth = 1.5
        ring.stroke()
        NSBezierPath(ovalIn: NSRect(x: 6.75, y: 6.75, width: 4.5, height: 4.5)).fill()
        let ticks = NSBezierPath()
        ticks.lineWidth = 1.5
        for (from, to) in [((9.0, 0.5), (9.0, 3.5)), ((9.0, 14.5), (9.0, 17.5)), ((0.5, 9.0), (3.5, 9.0)), ((14.5, 9.0), (17.5, 9.0))] {
            ticks.move(to: NSPoint(x: from.0, y: from.1))
            ticks.line(to: NSPoint(x: to.0, y: to.1))
        }
        ticks.stroke()
        return true
    }
    img.isTemplate = true
    img.accessibilityDescription = "Laserdot"
    return img
}()
