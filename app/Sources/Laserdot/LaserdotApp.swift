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

    init() { LaunchTimer.mark("app") }

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

final class AppDelegate: NSObject, NSApplicationDelegate, ObservableObject {
    @Published var statusLine = L("daemon.starting")
    @Published var networkLine = ""
    @Published var watcherLine = ""
    var statusTimer: Timer?
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
    var engine: Engine?
    var duplicates: NSWindow?
    @Published private(set) var model: SearchModel?
    let updates = UpdateController()
    /// The ⌥Space window, made just after launch (not during it, so the
    /// app is up sooner) or on first use.
    var overlayStorage: OverlayController?
    var overlay: OverlayController? {
        if overlayStorage == nil, let model { overlayStorage = OverlayController(model: model) }
        return overlayStorage
    }
    /// Milliseconds from process start to the end of launching, and to the
    /// first answer from laserdotd (Save Diagnostics shows both).
    var launchMS = -1
    /// startEngine ran (after the permission prompts).
    var engineStarted = false
    var readyMS = -1
    var hotKey: HotKey?
    @Published private(set) var hotKeyChoice = HotKeyChoice.saved

    func applicationDidFinishLaunching(_ notification: Notification) {
        LaunchTimer.mark("launched")
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
        LaunchTimer.mark("model")
        registerHotKey(prompt: !SelfTest.active)
        LaunchTimer.mark("hotkey")
        model.onStatus = { [weak self] status in
            if let self, self.readyMS < 0, status.running {
                self.readyMS = msSinceProcessStart()
                LaunchTimer.mark("daemon")
            }
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
        folders = FoldersModel(engine: engine)
        content = ContentModel(engine: engine)
        drives = DrivesModel(engine: engine)
        updates.stopDaemon = { [weak engine] in engine?.shutdown() }
        if SelfTest.active {
            startEngine()
        } else {
            // Every macOS question at once, before anything is read (nothing
            // is shown for places already answered; after an update macOS
            // sees as a new app, all of them come together), then the index.
            // Not tied to the welcome tour: a menu-bar launch may never show it.
            noteVersion()
            askPermissionsThenStart()
        }
        launchMS = msSinceProcessStart()
        LaunchTimer.mark("ready")
        if let out = SelfTest.outDir {
            // CI: drive the real app and report what it shows (SelfTest.swift).
            selfTest = SelfTestRunner(app: self, out: out)
            selfTest?.start()
            return
        }
        updates.offerMoveToApplications()
        OlderCopies.offerCleanup()
        updates.startDailyChecks()
        openAtLoginByDefault()
        power = PowerWatcher { [weak engine] pause in
            DispatchQueue.global().async { engine?.setBackground(paused: pause) }
        }
    }

    var power: PowerWatcher?
    var duplicatesModel: DuplicatesModel?
    var storageWindow: NSWindow?
    var storageModel: StorageModel?

    /// laserdot:// links (Shortcuts' "Open URL", Terminal's `open`):
    ///   laserdot://search?q=photos%20last%20week   the overlay with that search
    ///   laserdot://storage   laserdot://duplicates   laserdot://settings
    func application(_ application: NSApplication, open urls: [URL]) {
        urls.forEach { _ = handle($0) }
    }
    // Quick Look's controller (the app delegate is at the end of every
    // responder chain): the panel gets its items from QuickLook.shared.
    override func acceptsPreviewPanelControl(_ panel: QLPreviewPanel!) -> Bool { true }

    override func beginPreviewPanelControl(_ panel: QLPreviewPanel!) {
        panel.dataSource = QuickLook.shared
        panel.delegate = QuickLook.shared
    }

    override func endPreviewPanelControl(_ panel: QLPreviewPanel!) {}

    var selfTest: SelfTestRunner?
    /// Set by the main window (SwiftUI's openSettings).
    var openSettingsAction: (() -> Void)?

    var engineForTests: Engine? { engine }
    var overlayForTests: OverlayController? { overlay }

    /// Laserdot started laserdotd; it stops it when it quits.
    func applicationWillTerminate(_ notification: Notification) {
        engine?.shutdown()
    }

    /// Quitting with a downloaded update installs it first, with the update
    /// window showing each step (the work runs off the main thread).
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        updates.installOnQuit { NSApp.reply(toApplicationShouldTerminate: true) } ? .terminateLater : .terminateNow
    }

    /// Closing the window keeps Laserdot (and ⌥Space) running; the Dock icon
    /// opens a new window.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    /// An empty dry run moves nothing; it only tells whether this daemon
    /// has the duplicate finder.
    func probeDuplicates() {
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
    func registerHotKey(prompt: Bool) {
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
    func indexDefaultsOnce(_ status: DaemonStatus) {
        let key = "laserdot.defaultsIndexed"
        // The macOS questions were asked before laserdotd started.
        guard status.running, !UserDefaults.standard.bool(forKey: key), let model else { return }
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
        // Indexing may have waited for the tour (indexDefaultsOnce).
        guard !SelfTest.active, engineStarted, let engine else { return }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let status = engine.status()
            DispatchQueue.main.async { self?.indexDefaultsOnce(status) }
        }
    }

    /// Every macOS prompt at once, then laserdotd (which then indexes the
    /// default folders by itself, indexDefaultsOnce).
    func askPermissionsThenStart() {
        Permissions.askAll(waiting: { [weak self] place in
            guard self?.engineStarted == false else { return }
            self?.statusLine = String(format: L("permissions.waiting"), place.name)
        }, done: { [weak self] refused in
            self?.statusLine = L("daemon.starting")
            self?.startEngine()
            Permissions.explain(refused)
        })
    }

    /// Connects to laserdotd (starting it) and everything that talks to it.
    func startEngine() {
        guard !engineStarted, let model else { return }
        engineStarted = true
        model.connect()
        // The menu-bar menu's status lines (entries, watching) stay current.
        statusTimer = Timer.scheduledTimer(withTimeInterval: 60, repeats: true) { [weak self] _ in
            self?.refreshStatus()
        }
        // After the first frame: nothing here is needed to show a window.
        DispatchQueue.main.async { [weak self] in
            _ = self?.overlay
            self?.content?.refresh()
            self?.drives?.start()
            self?.probeDuplicates()
        }
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

    func show(_ status: DaemonStatus) {
        if status.running {
            statusLine = String(format: L("menu.status"), Int64(status.entries))
        } else {
            statusLine = L("daemon.starting")
        }
        networkLine = status.networkDenied ? L("network.blocked") : L("network.open")
        watcherLine = Self.watcherText(status)
    }

    /// "Watching for changes", or that it is not (and what the hourly
    /// catch-up found).
    static func watcherText(_ status: DaemonStatus) -> String {
        guard status.running, !status.folders.isEmpty else { return "" }
        let watching = !status.watcher.isEmpty && status.watcher != "None"
        var text = L(watching ? "watcher.on" : "watcher.off")
        if let c = status.catchUp, c.missed > 0 {
            text += " — " + String(format: L("watcher.caughtUp"), Int64(c.missed))
        }
        return text
    }

    /// Show the daemon's current state in the menu-bar menu.
    func refreshStatus() {
        guard engineStarted, let engine else { return }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let status = engine.status()
            DispatchQueue.main.async { self?.show(status) }
        }
    }
}
