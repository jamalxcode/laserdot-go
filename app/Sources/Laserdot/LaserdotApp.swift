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
            // The dot flashes red, white, blue and green while Laserdot works.
            Image(nsImage: delegate.menuIcon)
        }
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate, ObservableObject {
    @Published var statusLine = L("daemon.starting")
    /// The menu-bar icon (its dot flashes while Laserdot works).
    @Published var menuIcon: NSImage = menuBarIcon
    private var flashTimer: Timer?
    private var flashStep = 0
    private var activityTimer: Timer?
    /// The engine reported a folder being indexed at its last look.
    private var engineIndexing = false
    /// A question to the engine about indexing is on its way (never two).
    private var activityAsking = false
    var watchdog: Watchdog?
    /// Lets go of a drive just before macOS ejects it.
    var ejectWatcher: EjectWatcher?
    private var progressPolling = false
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
    /// An update brought something new: the menu-bar icon carries a blue dot
    /// until the notes are opened from the menu.
    @Published var whatsNewPending = false {
        didSet { if flashTimer == nil { menuIcon = restingIcon } }
    }

    /// The icon when Laserdot is not working.
    var restingIcon: NSImage { whatsNewPending ? menuBarIconWithNews : menuBarIcon }
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
        // Freezes are written down (Save Diagnostics shows them).
        HangMonitor.shared.context = { [weak self] in self?.hangContext() ?? "" }
        HangMonitor.shared.start()
        startActivityWatch()
        let db = Paths.defaultDbPath
        // Safe start: two launches in a row that did not get going offer a
        // fresh index on the third (nothing else is touched).
        let attempts = SafeStart.begin()
        if attempts >= 3, !SelfTest.active, SafeStart.offer() {
            SafeStart.moveIndexAside(db: db)
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 20) { SafeStart.succeeded() }
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
                self.noteHealth(entries: status.entries)
            }
            self?.show(status)
            if !SelfTest.active { self?.indexDefaultsOnce(status) }
        }
        model.commandProvider = { [weak self] in self?.commands() ?? [] }
        model.onDriveChoice = { [weak self, weak model] o in
            self?.drives?.offer(mount: o.mount ?? "") {
                // Chosen like any drive: the search narrows to it.
                model?.applyRefinement(RefineOption(token: o.token, label: o.label, key: nil, count: 1))
            }
        }
        model.onIndexing = { [weak self] name in
            self?.statusLine = String(format: L("index.running"), name)
            // The daemon's progress follows in the menu (show(_:) polls on).
            DispatchQueue.main.asyncAfter(deadline: .now() + 1) { self?.refreshStatus() }
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
        // Replacing Laserdot stops its daemon on purpose: the watchdog stops too.
        updates.pauseEngine = { [weak engine] paused in
            DispatchQueue.global(qos: .userInitiated).async { _ = engine?.setBackground(paused: paused) }
        }
        updates.stopDaemon = { [weak engine, weak self] in
            DispatchQueue.main.async { self?.watchdog?.stop() }
            engine?.shutdown()
        }
        if SelfTest.active {
            startEngine()
        } else {
            // Every macOS question at once, before anything is read (nothing
            // is shown for places already answered; after an update macOS
            // sees as a new app, all of them come together), then the index.
            // Not tied to the welcome tour: a menu-bar launch may never show it.
            noteVersion()
            keepSignatureThenAsk()
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
        watchdog?.stop()
        SafeStart.succeeded()
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
            model.showStorage = { [weak self] in self?.showStorage() }
            if let dm = drives {
                model.follow(dm.$drives)
                model.drives = dm.drives
                model.offerDrive = { [weak dm] mount in dm?.offer(mount: mount) {} }
            }
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
    /// A version installed by hand from the disk image is signed ad hoc, and
    /// macOS forgets Full Disk Access for it at every change of signature. It
    /// is re-signed with this Mac's key (the one updates are signed with) and
    /// relaunched once, before macOS is asked anything, so the answer is kept
    /// from then on. Tried once per version; a failure is noted for Save
    /// Diagnostics and Laserdot carries on.
    func keepSignatureThenAsk() {
        let app = Bundle.main.bundleURL
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "?"
        let triedKey = "laserdot.resignTried"
        // Not for test runs on another index (CI launches the app with LASERDOT_DB).
        guard UserDefaults.standard.string(forKey: triedKey) != version,
              ProcessInfo.processInfo.environment["LASERDOT_DB"] == nil else { return askPermissionsThenStart() }
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            var replaced = false
            do {
                replaced = try LocalSigner().resignInPlace(app)
            } catch {
                LocalSigner.remember(error: "at launch: " + ((error as? LocalizedError)?.errorDescription ?? "\(error)"))
            }
            DispatchQueue.main.async {
                UserDefaults.standard.set(version, forKey: triedKey)
                guard replaced else {
                    self?.askPermissionsThenStart()
                    return
                }
                SafeStart.succeeded() // a planned relaunch, not a failed start
                Updater.relaunch(app)
                NSApp.terminate(nil)
            }
        }
    }

    /// One line in the health log for this launch (kept for later; nothing
    /// shown): start-up times on this Mac, the index's size, how it is signed.
    func noteHealth(entries: UInt64) {
        let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "?"
        let (launch, ready, restarts) = (launchMS, readyMS, watchdog?.restarts ?? 0)
        let app = Bundle.main.bundleURL
        DispatchQueue.global(qos: .utility).async {
            HealthLog.note(HealthLog.line(version: version, launchMS: launch, readyMS: ready, entries: entries,
                                          restarts: restarts, signed: LocalSigner.signatureKind(of: app).rawValue))
        }
    }

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
        // Optional reminders (off unless turned on); a click opens the window.
        Reminders.shared.openDuplicates = { [weak self] in self?.showDuplicates() }
        Reminders.shared.openStorage = { [weak self] in self?.showStorage() }
        if !SelfTest.active { Reminders.shared.start() }
        // laserdotd that stops or hangs is started again by itself.
        if let engine {
            let w = Watchdog(engine: engine)
            w.onRestart = { [weak self] in
                self?.refreshStatus()
                self?.model?.connect()
            }
            w.start()
            watchdog = w
            // A disk about to be ejected: the engine lets go of it first
            // (waiting at most 5 seconds; the eject goes ahead regardless).
            ejectWatcher = EjectWatcher { [weak self] mount in
                // Never offer to index a drive while it is being ejected (it
                // can look unindexed for a moment as it goes).
                DispatchQueue.main.async { self?.drives?.ejecting(mount) }
                let done = DispatchSemaphore(value: 0)
                DispatchQueue.global(qos: .userInitiated).async {
                    _ = try? engine.driveRelease(mount: mount)
                    done.signal()
                }
                _ = done.wait(timeout: .now() + 5)
            }
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
        // The indexed folders are remembered outside the index too, so a fresh
        // index (safe start) brings them back.
        if status.running {
            SafeStart.remember(folders: status.folders)
            for folder in SafeStart.foldersToReindex(nowIndexed: status.folders) {
                model?.index(folder: URL(fileURLWithPath: folder))
            }
        }
        if status.running, let p = status.indexing {
            // Indexing: what it is doing and how far it has got, every 2 s.
            statusLine = Self.progressText(p)
            if !progressPolling {
                progressPolling = true
                DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
                    self?.progressPolling = false
                    self?.refreshStatus()
                }
            }
        } else if status.running {
            statusLine = String(format: L("menu.status"), Int64(status.entries))
        } else {
            statusLine = L("daemon.starting")
        }
        networkLine = status.networkDenied ? L("network.blocked") : L("network.open")
        watcherLine = Self.watcherText(status)
    }

    /// "Finding files in your home folder… 412,000 so far", "Indexing … 45%",
    /// "Reading photo and video details… 12,000 of 80,000".
    static func progressText(_ p: Wire.IndexProgress) -> String {
        let folder = p.root == FileManager.default.homeDirectoryForCurrentUser.path
            ? L("index.home") : URL(fileURLWithPath: p.root).lastPathComponent
        let n = NumberFormatter()
        n.numberStyle = .decimal
        func num(_ v: UInt64) -> String { n.string(from: NSNumber(value: v)) ?? "\(v)" }
        switch p.phase {
        case "scanning" where p.done > 0:
            return String(format: L("index.found"), folder, num(p.done))
        case "saving" where p.total > 0:
            return String(format: L("index.progress"), folder, Int64(Double(p.done) / Double(p.total) * 100))
        case "details" where p.total > 0:
            return String(format: L("index.details"), num(p.done), num(p.total))
        default:
            return String(format: L("index.scanning"), folder)
        }
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

// MARK: the menu-bar dot

extension AppDelegate {
    /// Laserdot is working: indexing (folders or a drive, details included),
    /// scanning for duplicates, or refreshing. Searching is not counted (it
    /// would flash at every keystroke).
    var isWorking: Bool {
        engineIndexing
            || drives?.busy != nil
            || drives?.drives.contains(where: \.indexing) == true
            || duplicatesModel?.scanning == true
            || model?.refreshing == true
    }

    /// Every 5 s: ask the engine (one small request, at background priority,
    /// never two at once) whether it is indexing, then flash the dot or stop.
    /// The dot is a nicety: it must never hold up searching or indexing (the
    /// owner: "don't let it block or take over important processing").
    func startActivityWatch() {
        guard activityTimer == nil else { return }
        let t = Timer(timeInterval: 5, repeats: true) { [weak self] _ in
            guard let self else { return }
            self.updateFlash() // the app's own work, at once
            guard self.engineStarted, let engine = self.engine, !self.activityAsking else { return }
            self.activityAsking = true
            DispatchQueue.global(qos: .background).async { [weak self] in
                let indexing = engine.indexingNow()
                DispatchQueue.main.async {
                    guard let self else { return }
                    self.activityAsking = false
                    self.engineIndexing = indexing ?? false
                    self.updateFlash()
                }
            }
        }
        t.tolerance = 1
        RunLoop.main.add(t, forMode: .common)
        activityTimer = t
    }

    func updateFlash() {
        let working = isWorking
        if working, flashTimer == nil {
            flashStep = 0
            let t = Timer(timeInterval: 0.4, repeats: true) { [weak self] _ in
                guard let self else { return }
                self.menuIcon = Self.flashFrames[self.flashStep % Self.flashFrames.count]
                self.flashStep += 1
            }
            t.tolerance = 0.1 // let macOS bunch the redraws with other work
            RunLoop.main.add(t, forMode: .common)
            flashTimer = t
            t.fire()
        } else if !working, let t = flashTimer {
            t.invalidate()
            flashTimer = nil
            menuIcon = restingIcon
        }
    }

    /// The four icons, drawn once (not on every flash).
    static let flashFrames: [NSImage] = menuBarFlashColors.map { flashingMenuBarIcon(dot: $0) }

    /// Whether the dot is flashing now (the self-test's report).
    var isFlashing: Bool { flashTimer != nil }
}
