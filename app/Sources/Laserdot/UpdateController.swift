import AppKit
import LaserdotKit

/// Updates (spec §7.4). The app never uses the network itself: it asks the
/// bundled Laserdot Updater helper (the one exception) to look for a newer
/// release and download it, then checks and installs the download offline.
final class UpdateController: ObservableObject {
    /// A downloaded update, ready to install.
    @Published private(set) var ready: UpdateStatus?
    @Published private(set) var checking = false

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

    /// Check now if the last check is a day old, and every hour after that
    /// look again whether a day has passed.
    func startDailyChecks() {
        if let s = UpdateStatus.load(), s.state == .downloaded { adopt(s) }
        checkIfDue()
        timer = Timer.scheduledTimer(withTimeInterval: 3600, repeats: true) { [weak self] _ in self?.checkIfDue() }
    }

    private func checkIfDue() {
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
            if interactive || ready != nil { offerInstall() }
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
        ready = s
    }

    /// Ask, then install the downloaded update and relaunch.
    func offerInstall() {
        guard let s = ready, let file = s.file else { return }
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = String(format: L("update.available"), s.latest ?? "")
        a.informativeText = String(format: L("update.installText"), currentVersion)
        a.addButton(withTitle: L("update.install"))
        a.addButton(withTitle: L("drive.notNow"))
        guard a.runModal() == .alertFirstButtonReturn else { return }
        install(URL(fileURLWithPath: file))
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
        guard a.runModal() == .alertFirstButtonReturn else { return }
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
        a.runModal()
    }
}
