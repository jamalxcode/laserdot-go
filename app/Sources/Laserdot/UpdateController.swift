import AppKit
import LaserdotKit

/// Updates (spec §7.4). The app never uses the network itself: it asks the
/// bundled Laserdot Updater helper (the one exception) to look for a newer
/// release and download it, then checks and installs the download offline.
final class UpdateController: ObservableObject {
    /// A downloaded update, ready to install.
    @Published private(set) var ready: UpdateStatus?
    @Published private(set) var checking = false
    /// Where a check or install has got (shown in the update window, and
    /// in the menu while a background check downloads).
    @Published private(set) var step: UpdateStep?
    @Published private(set) var stepVersion: String?
    /// Settings ▸ General ▸ Check for updates automatically (on by default).
    @Published var autoCheck = UserDefaults.standard.object(forKey: UpdateController.autoKey) as? Bool ?? true {
        didSet { UserDefaults.standard.set(autoCheck, forKey: Self.autoKey) }
    }
    static let autoKey = "laserdot.update.auto"

    /// Stops the daemon before the app is replaced.
    var stopDaemon: () -> Void = {}

    private let updater = Updater()
    private var timer: Timer?
    private var panel: NSPanel?
    /// The running helper (Cancel stops it), and which check is current: a
    /// cancelled check's result is ignored.
    private var helperApp: NSRunningApplication?
    /// The downloaded update, already signed and copied beside the app, so
    /// installing it (on quit, or from the prompt) is a rename.
    private var staged: StagedUpdate?
    private var staging = false
    private var generation = 0
    /// Slow work (unpacking, signing, copying) runs here, never on the main
    /// thread, so Laserdot keeps answering while it updates.
    private let work = DispatchQueue(label: "laserdot.update", qos: .userInitiated)
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

    // MARK: the update window

    /// What the menu shows while a check runs.
    var statusText: String {
        if case let .downloading(r, t) = step, t > 0 {
            return String(format: L("update.menu.downloading"), Int(r * 100 / t))
        }
        return step?.title ?? L("update.checking")
    }

    private func setStep(_ s: UpdateStep?, version: String? = nil, show: Bool = false) {
        step = s
        if let version { stepVersion = version }
        if s == nil {
            stepVersion = nil
            hideProgress()
        } else if show {
            showProgress()
        }
    }

    /// Bring the update window up (also from the menu while a check runs).
    func showProgress() {
        guard step != nil else { return }
        if panel == nil { panel = UpdateProgressPanel.make(updates: self) }
        panel?.orderFrontRegardless()
    }

    func hideProgress() {
        panel?.orderOut(nil)
    }

    /// Cancel: stop the helper while it checks or downloads. Nothing is
    /// left behind (a download is moved into place only once complete); the
    /// next check starts over.
    func cancelCheck() {
        guard checking else { return }
        generation += 1
        helperApp?.forceTerminate()
        helperApp = nil
        checking = false
        try? FileManager.default.removeItem(at: UpdateProgress.defaultURL)
        setStep(nil)
    }

    /// The self-test: the window at a given step, without a real update.
    func showStepForTests(_ s: UpdateStep?, version: String? = nil) {
        checking = s?.canHide == true
        setStep(s, version: version, show: true)
    }
    var progressPanelForTests: NSWindow? { panel }

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
            updater.discardStaged()
            staged = nil
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
        if ready == nil {
            // Staged copies of versions now installed (or skipped) go.
            work.async { [updater] in _ = updater.stagedUpdate() }
        }
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
        try? FileManager.default.removeItem(at: UpdateProgress.defaultURL)
        setStep(.checking, show: interactive)
        // Asking for updates means the version gone back from is welcome again.
        if interactive { UserDefaults.standard.removeObject(forKey: Self.skipKey) }
        UserDefaults.standard.set(Date(), forKey: Self.lastCheckKey)
        let started = Date()
        let cfg = NSWorkspace.OpenConfiguration()
        cfg.arguments = ["--app", Bundle.main.bundlePath] + (interactive ? ["--interactive"] : [])
        cfg.activates = interactive
        cfg.createsNewApplicationInstance = true
        cfg.addsToRecentItems = false
        generation += 1
        let gen = generation
        NSWorkspace.shared.openApplication(at: helper, configuration: cfg) { [weak self] app, error in
            DispatchQueue.main.async {
                guard let self, gen == self.generation else {
                    app?.forceTerminate() // cancelled before it started
                    return
                }
                self.helperApp = app
                if let error {
                    self.checking = false
                    self.setStep(nil)
                    if interactive { self.alert(L("update.failed"), info: error.localizedDescription) }
                    return
                }
                self.waitForResult(since: started, interactive: interactive, generation: gen)
            }
        }
    }

    /// The helper writes update-progress.json as it goes and update.json
    /// when it is done; both are read a few times a second.
    private func waitForResult(since: Date, interactive: Bool, generation gen: Int, waited: TimeInterval = 0) {
        guard gen == generation else { return } // cancelled
        if let s = UpdateStatus.load(), s.checkedAt >= since.addingTimeInterval(-1) {
            checking = false
            helperApp = nil
            report(s, interactive: interactive)
            return
        }
        if let p = UpdateProgress.load(), p.updatedAt >= since.addingTimeInterval(-1) {
            let s = UpdateStep(p)
            if s != step || (p.version != nil && p.version != stepVersion) { setStep(s, version: p.version) }
        }
        guard waited < 900 else {
            checking = false
            setStep(nil)
            if interactive { alert(L("update.failed"), info: L("update.timeout")) }
            return
        }
        let tick = 0.25
        DispatchQueue.main.asyncAfter(deadline: .now() + tick) { [weak self] in
            self?.waitForResult(since: since, interactive: interactive, generation: gen, waited: waited + tick)
        }
    }

    private func report(_ s: UpdateStatus, interactive: Bool) {
        try? FileManager.default.removeItem(at: UpdateProgress.defaultURL)
        // Installing shows its own steps; everything else closes the window.
        if !(interactive && s.state == .downloaded) { setStep(nil) }
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
        stageIfNeeded()
    }

    /// Get the downloaded update ready to swap in, in the background, so
    /// quitting installs it at once (with automatic updates on).
    private func stageIfNeeded() {
        guard autoCheck, !staging, let s = ready, let file = s.file, staged?.version.short != s.latest else { return }
        staging = true
        work.async { [updater] in
            // One left by an earlier run, else a fresh one.
            var result = updater.stagedUpdate()
            if result?.version.short != s.latest {
                result = (try? updater.prepare(file: URL(fileURLWithPath: file))).flatMap { try? updater.stage($0) }
            }
            DispatchQueue.main.async {
                self.staging = false
                if self.ready?.latest == s.latest { self.staged = result }
            }
        }
    }

    /// The staged update for the ready version, if there is one.
    private var stagedForReady: StagedUpdate? {
        guard let st = staged, st.version.short == ready?.latest else { return nil }
        return st
    }

    /// Quitting with a downloaded update installs it, so the next launch is
    /// the new version (with automatic updates on; nothing is asked). The
    /// window shows the steps while it works off the main thread; `done` is
    /// called on the main thread when it is over. False: nothing to install.
    func installOnQuit(done: @escaping () -> Void) -> Bool {
        guard autoCheck, let s = ready, let file = s.file else { return false }
        if let st = stagedForReady {
            // Ready ahead of time: a rename, done before Laserdot closes.
            ready = nil
            stopDaemon()
            _ = try? updater.finishStaged(st)
            return false
        }
        ready = nil
        setStep(.verifying, version: s.latest, show: true)
        let stop = stopDaemon
        work.async { [updater] in
            stop()
            if let st = updater.stagedUpdate(), st.version.short == s.latest {
                // Staging finished meanwhile.
                _ = try? updater.finishStaged(st)
            } else if let c = try? updater.prepare(file: URL(fileURLWithPath: file)) {
                _ = try? updater.install(c) { step in
                    DispatchQueue.main.async { self.setStep(step == .signing ? .signing : .installing) }
                }
            }
            DispatchQueue.main.async {
                self.setStep(nil)
                done()
            }
        }
        return true
    }

    /// Ask, then install the downloaded update and relaunch.
    func offerInstall() {
        // Not while an install is already under way.
        guard step?.canHide != false, let s = ready, let file = s.file else { return }
        NSApp.activate(ignoringOtherApps: true)
        if let st = stagedForReady {
            confirmStaged(s, st)
            return
        }
        // Unpack and check it first, so the prompt can say what is new.
        setStep(.verifying, version: s.latest, show: true)
        work.async { [updater] in
            let result = Result { try updater.prepare(file: URL(fileURLWithPath: file)) }
            DispatchQueue.main.async { self.confirmInstall(s, result) }
        }
    }

    /// The prompt for an update made ready ahead of time; installing it is
    /// a rename, then Laserdot restarts.
    private func confirmStaged(_ s: UpdateStatus, _ st: StagedUpdate) {
        guard askToInstall(s, app: st.app, fallbackNotes: Updater.whatsNew(in: st.app)) else { return }
        setStep(.installing, version: s.latest, show: true)
        let stop = stopDaemon
        work.async { [updater] in
            stop()
            let result = Result { try updater.finishStaged(st) }
            DispatchQueue.main.async { self.relaunchAfter(result) }
        }
    }

    /// The install prompt, with what is new since this version.
    private func askToInstall(_ s: UpdateStatus, app: URL, fallbackNotes: String?) -> Bool {
        // Everything new since this version, not only the last release.
        let notes = Updater.notes(in: app, since: updater.currentVersion?.short) ?? fallbackNotes
        let a = Self.installAlert(version: s.latest ?? "", current: currentVersion, notes: notes)
        return a.runFront() == .alertFirstButtonReturn
    }

    /// The install prompt. What is new goes in a scrolling box of fixed
    /// height, so the prompt fits any screen and its buttons always show
    /// (all the notes since an old version can be very long).
    static func installAlert(version: String, current: String, notes: String?) -> NSAlert {
        let a = NSAlert()
        a.messageText = String(format: L("update.available"), version)
        a.informativeText = String(format: L("update.installText"), current)
        if let notes, !notes.isEmpty {
            let scroll = NSScrollView(frame: NSRect(x: 0, y: 0, width: 440, height: 260))
            scroll.hasVerticalScroller = true
            scroll.borderType = .bezelBorder
            scroll.drawsBackground = false
            let text = NSTextView(frame: scroll.contentView.bounds)
            text.isEditable = false
            text.isSelectable = true
            text.drawsBackground = false
            text.font = .systemFont(ofSize: NSFont.smallSystemFontSize)
            text.textContainerInset = NSSize(width: 6, height: 6)
            text.isVerticallyResizable = true
            text.autoresizingMask = [.width]
            text.textContainer?.widthTracksTextView = true
            text.string = notes
            scroll.documentView = text
            a.accessoryView = scroll
        }
        a.addButton(withTitle: L("update.install"))
        a.addButton(withTitle: L("drive.notNow"))
        return a
    }

    private func relaunchAfter(_ result: Result<URL, Error>) {
        switch result {
        case let .success(target):
            ready = nil // installed: quitting must not install it again
            staged = nil
            setStep(.restarting)
            // A moment so "Restarting" is seen before the window goes.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) {
                Updater.relaunch(target)
                NSApp.terminate(nil)
            }
        case let .failure(error):
            setStep(nil)
            alert(L("update.failed"), info: (error as? LocalizedError)?.errorDescription ?? "\(error)")
        }
    }

    private func confirmInstall(_ s: UpdateStatus, _ result: Result<UpdateCandidate, Error>) {
        hideProgress()
        let candidate: UpdateCandidate
        switch result {
        case let .success(c): candidate = c
        case let .failure(error):
            setStep(nil)
            alert(L("update.failed"), info: (error as? LocalizedError)?.errorDescription ?? "\(error)")
            return
        }
        guard askToInstall(s, app: candidate.app, fallbackNotes: candidate.whatsNew) else {
            updater.discard(candidate)
            setStep(nil)
            return
        }
        installAndRelaunch(candidate)
    }

    /// Check and install an update file, then relaunch from where it went.
    func install(_ file: URL) {
        setStep(.verifying, show: true)
        work.async { [updater] in
            let result = Result { try updater.prepare(file: file) }
            DispatchQueue.main.async {
                switch result {
                case let .success(c): self.installAndRelaunch(c)
                case let .failure(error):
                    self.setStep(nil)
                    self.alert(L("update.failed"), info: (error as? LocalizedError)?.errorDescription ?? "\(error)")
                }
            }
        }
    }

    /// Sign and place the checked update in the background, with each step
    /// in the window, then relaunch the new version.
    private func installAndRelaunch(_ c: UpdateCandidate) {
        setStep(.installing, show: true)
        let stop = stopDaemon
        work.async { [updater] in
            stop()
            let result = Result {
                try updater.install(c) { step in
                    DispatchQueue.main.async { self.setStep(step == .signing ? .signing : .installing) }
                }
            }
            DispatchQueue.main.async { self.relaunchAfter(result) }
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
