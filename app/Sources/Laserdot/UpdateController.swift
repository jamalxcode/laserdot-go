import AppKit
import LaserdotKit

/// Updates (spec §7.4). The app never uses the network itself: it asks the
/// bundled Laserdot Updater helper (the one exception) to look for a newer
/// release and download it, then checks and installs the download offline.
final class UpdateController: ObservableObject {
    /// A downloaded update, ready to install.
    @Published private(set) var ready: UpdateStatus?
    @Published private(set) var checking = false
    /// Settings ▸ General ▸ Check for updates automatically (on by default).
    @Published var autoCheck = UserDefaults.standard.object(forKey: UpdateController.autoKey) as? Bool ?? true {
        didSet { UserDefaults.standard.set(autoCheck, forKey: Self.autoKey) }
    }
    static let autoKey = "laserdot.update.auto"

    /// Stops the daemon before the app is replaced.
    var stopDaemon: () -> Void = {}

    private let updater = Updater()
    private var timer: Timer?
    private static let lastCheckKey = "laserdot.update.lastCheck"

    private var helperURL: URL? {
        let u = Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/Laserdot Updater.app")
        return FileManager.default.fileExists(atPath: u.path) ? u : nil
    }

    var currentVersion: String { updater.currentVersion?.description ?? "?" }

    /// The version kept from before the last update ("Go back to 0.7.0").
    var previousVersion: String? { updater.previousVersion?.short }

    /// After going back, the version left is not installed again
    /// automatically; a newer one is.
    static let skipKey = "laserdot.update.skip"

    func offerGoBack() {
        guard let v = previousVersion else { return }
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = String(format: L("update.goBack.title"), v)
        a.informativeText = String(format: L("update.goBack.text"), currentVersion)
        a.addButton(withTitle: String(format: L("update.goBack"), v))
        a.addButton(withTitle: L("action.cancel")).keyEquivalent = "\u{1b}"
        guard a.runFront() == .alertFirstButtonReturn else { return }
        do {
            stopDaemon()
            let leaving = updater.currentVersion?.short
            let target = try updater.goBack()
            UserDefaults.standard.set(leaving, forKey: Self.skipKey)
            ready = nil
            Updater.relaunch(target)
            NSApp.terminate(nil)
        } catch {
            alert(L("update.failed"), info: (error as? LocalizedError)?.errorDescription ?? "\(error)")
        }
    }

    /// Check now if the last check is a day old, and every hour after that
    /// look again whether a day has passed.
    func startDailyChecks() {
        if let s = UpdateStatus.load(), s.state == .downloaded { adopt(s) }
        checkIfDue()
        timer = Timer.scheduledTimer(withTimeInterval: 3600, repeats: true) { [weak self] _ in self?.checkIfDue() }
    }

    private func checkIfDue() {
        guard autoCheck else { return }
        let last = UserDefaults.standard.object(forKey: Self.lastCheckKey) as? Date ?? .distantPast
        if Date().timeIntervalSince(last) > 24 * 3600 { check(interactive: false) }
    }

    /// Run the helper. Interactive: the result is reported in an alert.
    func check(interactive: Bool) {
        guard !checking else { return }
        guard let helper = helperURL else {
            if interactive { alert(L("update.noHelper"), info: "") }
            return
        }
        checking = true
        // Asking for updates means the version gone back from is welcome again.
        if interactive { UserDefaults.standard.removeObject(forKey: Self.skipKey) }
        UserDefaults.standard.set(Date(), forKey: Self.lastCheckKey)
        let started = Date()
        let cfg = NSWorkspace.OpenConfiguration()
        cfg.arguments = ["--app", Bundle.main.bundlePath] + (interactive ? ["--interactive"] : [])
        cfg.activates = interactive
        cfg.createsNewApplicationInstance = true
        cfg.addsToRecentItems = false
        NSWorkspace.shared.openApplication(at: helper, configuration: cfg) { [weak self] _, error in
            DispatchQueue.main.async {
                if let error {
                    self?.checking = false
                    if interactive { self?.alert(L("update.failed"), info: error.localizedDescription) }
                    return
                }
                self?.waitForResult(since: started, interactive: interactive)
            }
        }
    }

    /// The helper writes update.json when it is done.
    private func waitForResult(since: Date, interactive: Bool, waited: TimeInterval = 0) {
        if let s = UpdateStatus.load(), s.checkedAt >= since.addingTimeInterval(-1) {
            checking = false
            report(s, interactive: interactive)
            return
        }
        guard waited < 900 else {
            checking = false
            if interactive { alert(L("update.failed"), info: L("update.timeout")) }
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { [weak self] in
            self?.waitForResult(since: since, interactive: interactive, waited: waited + 1)
        }
    }

    private func report(_ s: UpdateStatus, interactive: Bool) {
        switch s.state {
        case .downloaded:
            adopt(s)
            // A background download asks nothing: the menu and the window
            // show an Update button, and quitting installs it.
            if interactive { offerInstall() }
        case .upToDate:
            ready = nil
            if interactive { alert(L("update.upToDate"), info: String(format: L("update.youHave"), currentVersion)) }
        case .needsToken, .failed:
            break // the helper already showed interactive failures
        }
    }

    /// Keep a downloaded update only if it is really newer than this app.
    private func adopt(_ s: UpdateStatus) {
        guard let f = s.file, FileManager.default.fileExists(atPath: f), let latest = s.latest,
              let cur = updater.currentVersion,
              AppVersion(short: cur.short, build: "0") < AppVersion(short: latest, build: "0") else {
            ready = nil
            return
        }
        // The version the user went back from: only a newer one installs.
        if let skip = UserDefaults.standard.string(forKey: Self.skipKey),
           !(AppVersion(short: skip, build: "0") < AppVersion(short: latest, build: "0")) {
            ready = nil
            return
        }
        ready = s
    }

    /// Quitting with a downloaded update installs it, so the next launch is
    /// the new version (with automatic updates on; nothing is asked).
    func installOnQuit() {
        guard autoCheck, let s = ready, let file = s.file,
              let c = try? updater.prepare(file: URL(fileURLWithPath: file)) else { return }
        _ = try? updater.install(c)
    }

    /// Ask, then install the downloaded update and relaunch.
    func offerInstall() {
        guard let s = ready, let file = s.file else { return }
        NSApp.activate(ignoringOtherApps: true)
        // Unpack and check it first, so the prompt can say what is new.
        let candidate: UpdateCandidate
        do {
            candidate = try updater.prepare(file: URL(fileURLWithPath: file))
        } catch {
            alert(L("update.failed"), info: (error as? LocalizedError)?.errorDescription ?? "\(error)")
            return
        }
        let a = NSAlert()
        a.messageText = String(format: L("update.available"), s.latest ?? "")
        var info = String(format: L("update.installText"), currentVersion)
        // Everything new since this version, not only the last release.
        if let notes = Updater.notes(in: candidate.app, since: updater.currentVersion?.short) ?? candidate.whatsNew {
            info = notes + "\n\n" + info
        }
        a.informativeText = info
        a.addButton(withTitle: L("update.install"))
        a.addButton(withTitle: L("drive.notNow"))
        guard a.runFront() == .alertFirstButtonReturn else { return }
        do {
            stopDaemon()
            let target = try updater.install(candidate)
            Updater.relaunch(target)
            NSApp.terminate(nil)
        } catch {
            alert(L("update.failed"), info: (error as? LocalizedError)?.errorDescription ?? "\(error)")
        }
    }

    /// Check and install an update file, then relaunch from where it went.
    func install(_ file: URL) {
        do {
            let c = try updater.prepare(file: file)
            stopDaemon()
            let target = try updater.install(c)
            Updater.relaunch(target)
            NSApp.terminate(nil)
        } catch {
            alert(L("update.failed"), info: (error as? LocalizedError)?.errorDescription ?? "\(error)")
        }
    }

    /// First launch from a disk image or Downloads: offer to copy Laserdot
    /// into Applications (so it can update itself in place).
    func offerMoveToApplications() {
        let path = Bundle.main.bundlePath
        guard path.hasSuffix(".app"), updater.isOutsideApplications,
              !UserDefaults.standard.bool(forKey: "laserdot.moveOffered") else { return }
        UserDefaults.standard.set(true, forKey: "laserdot.moveOffered")
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = L("move.title")
        a.informativeText = L("move.text")
        a.addButton(withTitle: L("move.move"))
        a.addButton(withTitle: L("drive.notNow"))
        guard a.runFront() == .alertFirstButtonReturn else { return }
        do {
            let target = try updater.moveToApplications()
            stopDaemon()
            Updater.relaunch(target)
            NSApp.terminate(nil)
        } catch {
            alert(L("update.failed"), info: (error as? LocalizedError)?.errorDescription ?? "\(error)")
        }
    }

    private func alert(_ title: String, info: String) {
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = title
        a.informativeText = info
        a.runFront()
    }
}
