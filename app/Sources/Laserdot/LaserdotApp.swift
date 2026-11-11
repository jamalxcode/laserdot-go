import AppKit
import Carbon.HIToolbox
import LaserdotKit
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
        }
        .defaultSize(width: 860, height: 600)
        .commands { LaserdotCommands(delegate: delegate, updates: delegate.updates) }

        MenuBarExtra("Laserdot", systemImage: "scope") {
            StatusMenu(delegate: delegate, updates: delegate.updates)
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
        Button(L("menu.fullDiskAccess")) { DiskAccessPrompt.shared.askFromMenu() }
        Divider()
        Text(delegate.statusLine)
        Text(delegate.networkLine)
        Divider()
        Button(L("menu.quit")) { NSApp.terminate(nil) }
            .keyboardShortcut("q")
    }
}

/// The main window: search, and a status bar (index size, network, update).
struct MainWindow: View {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController

    var body: some View {
        VStack(spacing: 0) {
            if let model = delegate.model {
                SearchView(model: model, close: { model.query = "" }, fixedSize: false)
            } else {
                ProgressView(L("daemon.starting")).frame(maxWidth: .infinity, maxHeight: .infinity)
            }
            Divider()
            HStack(spacing: 12) {
                Text(delegate.statusLine)
                Text(delegate.networkLine).foregroundStyle(.secondary)
                Spacer()
                if updates.checking {
                    ProgressView().controlSize(.small)
                    Text(L("update.checking")).foregroundStyle(.secondary)
                }
                if let r = updates.ready {
                    Button(String(format: L("update.button"), r.latest ?? "")) { updates.offerInstall() }
                        .buttonStyle(.borderedProminent)
                }
            }
            .font(.callout)
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
        }
        .frame(minWidth: 640, minHeight: 420)
    }
}

/// The menu bar: File (index, duplicates), Library (drives, content search,
/// hot key) and Check for Updates in the app menu.
struct LaserdotCommands: Commands {
    @ObservedObject var delegate: AppDelegate
    @ObservedObject var updates: UpdateController

    var body: some Commands {
        CommandGroup(after: .appInfo) {
            Button(L("menu.checkUpdates")) { updates.check(interactive: true) }
                .disabled(updates.checking)
            if let r = updates.ready {
                Button(String(format: L("update.button"), r.latest ?? "")) { updates.offerInstall() }
            }
            Button(L("menu.fullDiskAccess")) { DiskAccessPrompt.shared.askFromMenu() }
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
            if let drives = delegate.drives {
                DrivesMenu(model: drives)
            }
            if let content = delegate.content {
                ContentMenu(model: content)
            }
            Menu(String(format: L("menu.hotkey"), delegate.hotKeyChoice.label)) {
                ForEach(HotKeyChoice.all, id: \.label) { c in
                    Button(c.label) { delegate.useHotKey(c) }
                }
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
        registerHotKey(prompt: true)
        model.onStatus = { [weak self] status in self?.show(status) }
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
        model.onNothingIndexed = { [weak self] in self?.offerFirstIndex() }
        model.connect()
        let content = ContentModel(engine: engine)
        self.content = content
        content.refresh()
        let drives = DrivesModel(engine: engine)
        self.drives = drives
        drives.start()
        probeDuplicates()
        updates.stopDaemon = { [weak engine] in engine?.shutdown() }
        updates.offerMoveToApplications()
        updates.startDailyChecks()
    }

    /// Laserdot started laserdotd; it stops it when it quits.
    func applicationWillTerminate(_ notification: Notification) {
        engine?.shutdown()
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
            w.contentView = NSHostingView(rootView: DuplicatesView(model: DuplicatesModel(engine: engine)))
            w.center()
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
    private func offerFirstIndex() {
        guard !UserDefaults.standard.bool(forKey: "laserdot.firstIndexOffered") else { return }
        UserDefaults.standard.set(true, forKey: "laserdot.firstIndexOffered")
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = L("firstrun.title")
        alert.informativeText = L("firstrun.body")
        alert.addButton(withTitle: L("firstrun.home"))
        alert.addButton(withTitle: L("firstrun.choose"))
        alert.addButton(withTitle: L("drive.notNow"))
        switch alert.runFront() {
        case .alertFirstButtonReturn:
            model?.index(folder: FileManager.default.homeDirectoryForCurrentUser)
        case .alertSecondButtonReturn:
            chooseFolder()
        default:
            break
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
