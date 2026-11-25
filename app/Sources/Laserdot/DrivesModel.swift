import AppKit
import Foundation
import LaserdotKit

/// The Drives menu (spec §9): mounted drives, their portable indexes, and
/// the prompt shown the first time an unindexed drive mounts (§9.3).
final class DrivesModel: ObservableObject {
    @Published private(set) var drives: [Drive] = []
    /// A long operation in progress ("Indexing Archive…").
    @Published private(set) var busy: String?
    /// The daemon implements drives (older ones answer `unsupported`; the
    /// Drives menu is hidden then).
    @Published private(set) var available = true

    private let engine: Engine
    private let queue = DispatchQueue(label: "laserdot.drives", qos: .utility)
    private var timer: Timer?
    /// Unindexed drives already offered this session.
    private var offered = Set<String>()
    /// Drives being ejected: never offered (the owner ejected a USB drive and
    /// was asked to index it — while it unmounts it can look unindexed).
    private var ejectingMounts = Set<String>()
    /// Mounts seen at the last look (nil before the first): only a drive that
    /// has just appeared is offered, not one changing state as it goes.
    private var lastMounts: Set<String>?
    private var freshMounts = Set<String>()
    /// A listing is on its way: the 3s timer skips its turn instead of piling
    /// up threads behind a drive that answers slowly (a sleeping network share).
    private var listing = false
    private var listAgain = false // asked meanwhile (after an action): list once more

    /// macOS is about to unmount this drive.
    func ejecting(_ mount: String) {
        ejectingMounts.insert(mount)
        freshMounts.remove(mount)
    }
    private let declinedKey = "laserdot.drives.declined"

    init(engine: Engine) {
        self.engine = engine
    }

    /// Poll the daemon: it attaches and detaches drives itself; the app
    /// only shows the state and prompts for new drives.
    func start() {
        refresh()
        timer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in self?.refresh() }
    }

    func refresh() {
        guard !listing else { listAgain = true; return }
        listing = true
        let engine = self.engine
        // Not on `queue`: a drive being indexed holds it for as long as that
        // takes, and the list must stay current meanwhile (Stop Indexing).
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let outcome = Result { try engine.drives() }
            DispatchQueue.main.async {
                guard let self else { return }
                self.listing = false
                self.drives = (try? outcome.get()) ?? []
                if case let .failure(e) = outcome { self.available = !e.isUnsupported } else { self.available = true }
                if case .success = outcome {
                    let now = Set(self.drives.map(\.mount))
                    self.freshMounts.formUnion(now.subtracting(self.lastMounts ?? []))
                    self.freshMounts.formIntersection(now)
                    self.ejectingMounts.formIntersection(now) // gone: it may come back as new
                    self.lastMounts = now
                }
                self.offerNewDrives()
                if self.listAgain { self.listAgain = false; self.refresh() }
            }
        }
    }

    // MARK: labels

    func label(_ d: Drive) -> String {
        let state: String
        switch d.state {
        case "attached": state = String(format: L("drive.state.attached"), Int64(d.entries))
        case "detached": state = L("drive.state.detached")
        case "locked": state = L("drive.state.locked")
        case "unreadable": state = L("drive.state.unreadable")
        default: state = L("drive.state.unindexed")
        }
        return "\(d.name) — \(state)"
    }

    // MARK: actions

    private func run(_ title: String, _ work: @escaping (Engine) throws -> Void) {
        busy = title
        // While the job runs the drives' state is followed (an index being
        // read shows Stop Indexing).
        let ticker = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in self?.refresh() }
        let engine = self.engine
        queue.async { [weak self] in
            let outcome = Result { try work(engine) }
            DispatchQueue.main.async {
                ticker.invalidate()
                self?.busy = nil
                if case .failure(let e) = outcome {
                    let alert = NSAlert()
                    alert.messageText = title
                    alert.informativeText = Self.message(e)
                    alert.runFront()
                }
                self?.refresh()
            }
        }
    }

    private static func message(_ e: Error) -> String {
        if e.isUnsupported { return L("feature.unsupported") }
        return (e as? LaserdotError)?.message ?? e.localizedDescription
    }

    /// A drive chosen in the search window that Laserdot cannot search yet:
    /// index it (asked even if it was declined before: this time it was
    /// asked for), unlock it, or attach its index.
    func offer(mount: String) {
        let engine = self.engine
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            let d = (try? engine.drives())?.first { $0.mount == mount }
            DispatchQueue.main.async {
                guard let self, let d, self.busy == nil else { return }
                switch d.state {
                case "locked": self.promptUnlock(d)
                case "detached": self.attach(d)
                case "attached": break
                default: self.promptIndex(d)
                }
            }
        }
    }

    /// How long the index question waits for the drive to be counted.
    static let planWait: TimeInterval = 6

    /// Ask before indexing: what it will take and where the index goes.
    func promptIndex(_ d: Drive, rebuild: Bool = false) {
        let mount = d.mount
        busy = String(format: L("drive.looking"), d.name)
        let engine = self.engine
        // Not on the drives queue: refreshing the list goes on meanwhile.
        // The question shows within a few seconds whatever the drive does:
        // without the count when counting is not done by then (a sleeping
        // or huge drive), never minutes later.
        final class Box: @unchecked Sendable { var shown = false }
        let box = Box()
        let lock = NSLock()
        func show(_ plan: DrivePlan?) {
            lock.lock()
            let first = !box.shown
            box.shown = true
            lock.unlock()
            guard first else { return }
            DispatchQueue.main.async {
                self.busy = nil
                self.showIndexPrompt(d, plan: plan, rebuild: rebuild)
            }
        }
        DispatchQueue.global(qos: .userInitiated).async {
            show(try? engine.drivePlan(mount: mount))
        }
        DispatchQueue.global(qos: .userInitiated).asyncAfter(deadline: .now() + Self.planWait) {
            show(nil)
        }
    }

    private func showIndexPrompt(_ d: Drive, plan: DrivePlan?, rebuild: Bool) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = String(format: L(rebuild ? "drive.rebuild.title" : "drive.index.title"), d.name)
        if let p = plan {
            let size = ByteCountFormatter.string(fromByteCount: Int64(p.bytes), countStyle: .file)
            let index = ByteCountFormatter.string(fromByteCount: Int64(p.indexEstimateBytes), countStyle: .file)
            var text = String(format: L(p.partial ? "drive.index.estimatePartial" : "drive.index.estimate"),
                              Int64(p.files), size, index)
            if !p.onDrive { text += "\n" + L("drive.index.offDrive") }
            alert.informativeText = text
        }
        let encrypt = NSButton(checkboxWithTitle: L("drive.index.encrypt"), target: nil, action: nil)
        let pass = NSSecureTextField(frame: NSRect(x: 0, y: 0, width: 260, height: 24))
        pass.placeholderString = L("drive.passphrase")
        let remember = NSButton(checkboxWithTitle: L("drive.remember"), target: nil, action: nil)
        let stack = NSStackView(views: [encrypt, pass, remember])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.frame = NSRect(x: 0, y: 0, width: 260, height: 80)
        alert.accessoryView = stack
        alert.addButton(withTitle: L(rebuild ? "drive.rebuild" : "drive.index"))
        alert.addButton(withTitle: L("drive.notNow"))
        if !rebuild { alert.addButton(withTitle: L("drive.never")) }
        switch alert.runFront() {
        case .alertFirstButtonReturn:
            let secret = encrypt.state == .on && !pass.stringValue.isEmpty ? pass.stringValue : nil
            let keep = remember.state == .on
            let mount = d.mount
            run(String(format: L("drive.indexing"), d.name)) { engine in
                if rebuild {
                    _ = try engine.driveRebuild(mount: mount, passphrase: secret)
                } else {
                    _ = try engine.driveIndex(mount: mount, passphrase: secret)
                }
                // The drive has an id now; remember the passphrase under it.
                if let secret, keep, let id = try engine.drives().first(where: { $0.mount == mount })?.id {
                    Keychain.save(secret, for: id)
                }
            }
        case .alertThirdButtonReturn:
            var declined = UserDefaults.standard.stringArray(forKey: declinedKey) ?? []
            declined.append(d.name)
            UserDefaults.standard.set(declined, forKey: declinedKey)
        default:
            break
        }
    }

    func promptUnlock(_ d: Drive) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = String(format: L("drive.unlock.title"), d.name)
        let pass = NSSecureTextField(frame: NSRect(x: 0, y: 0, width: 260, height: 24))
        pass.placeholderString = L("drive.passphrase")
        let remember = NSButton(checkboxWithTitle: L("drive.remember"), target: nil, action: nil)
        let stack = NSStackView(views: [pass, remember])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.frame = NSRect(x: 0, y: 0, width: 260, height: 52)
        alert.accessoryView = stack
        alert.addButton(withTitle: L("drive.unlock"))
        alert.addButton(withTitle: L("drive.notNow"))
        guard alert.runFront() == .alertFirstButtonReturn else { return }
        let secret = pass.stringValue
        let keep = remember.state == .on
        let mount = d.mount
        let id = d.id
        run(String(format: L("drive.unlock.title"), d.name)) { engine in
            try engine.driveUnlock(mount: mount, passphrase: secret)
            if keep, let id { Keychain.save(secret, for: id) }
        }
    }

    func attach(_ d: Drive) {
        let mount = d.mount
        run(d.name) { engine in try engine.driveAttach(mount: mount) }
    }

    func detach(_ d: Drive) {
        let mount = d.mount
        run(d.name) { engine in _ = try engine.driveDetach(mount: mount) }
    }

    func delete(_ d: Drive) {
        let alert = NSAlert()
        alert.messageText = String(format: L("drive.delete.title"), d.name)
        alert.informativeText = L("drive.delete.text")
        alert.alertStyle = .warning
        // Alert buttons are verbs without an ellipsis; Cancel stays the
        // default-safe choice (Escape).
        alert.addButton(withTitle: L("drive.delete.confirm")).hasDestructiveAction = true
        alert.addButton(withTitle: L("action.cancel")).keyEquivalent = "\u{1b}"
        guard alert.runFront() == .alertFirstButtonReturn else { return }
        let mount = d.mount
        run(d.name) { engine in _ = try engine.driveDelete(mount: mount) }
    }

    /// Stop a drive's first index: it stays attached and searchable with what
    /// was read; Continue goes on from there.
    func stopIndexing(_ d: Drive) {
        let (mount, engine) = (d.mount, self.engine)
        // Its own line: the index job itself holds the drive queue.
        DispatchQueue.global(qos: .userInitiated).async { [weak self] in
            _ = try? engine.driveStop(mount: mount)
            DispatchQueue.main.async { self?.refresh() }
        }
    }

    func continueIndexing(_ d: Drive) {
        let mount = d.mount
        run(d.name) { engine in _ = try engine.driveContinue(mount: mount) }
    }

    func eject(_ d: Drive) {
        let mount = d.mount
        run(String(format: L("drive.ejecting"), d.name)) { engine in try engine.driveEject(mount: mount) }
    }

    func reveal(_ d: Drive) {
        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: d.mount)])
    }

    private func autoUnlock(_ d: Drive, id: String, secret: String) {
        let engine = self.engine
        let mount = d.mount
        busy = String(format: L("drive.unlock.title"), d.name)
        queue.async { [weak self] in
            let ok = (try? engine.driveUnlock(mount: mount, passphrase: secret)) != nil
            DispatchQueue.main.async {
                self?.busy = nil
                if !ok {
                    // Changed or wrong: forget it and ask.
                    Keychain.forget(id)
                    self?.promptUnlock(d)
                }
                self?.refresh()
            }
        }
    }

    /// The first time an unindexed drive mounts this
    /// session, offer to index it (unless the user said never).
    private func offerNewDrives() {
        // The self-test mounts a stand-in drive: no question may block it.
        guard busy == nil, !SelfTest.active else { return }
        let declined = Set(UserDefaults.standard.stringArray(forKey: declinedKey) ?? [])
        for d in drives where d.state == "unindexed" && !offered.contains(d.mount) && !declined.contains(d.name)
            && freshMounts.contains(d.mount) && !ejectingMounts.contains(d.mount) {
            offered.insert(d.mount)
            // Asked only if, two seconds on, it is still there and still
            // unindexed (not a drive on its way out), and not a disk image
            // opened to install an app.
            let mount = d.mount
            DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
                guard let self, !self.ejectingMounts.contains(mount), FileManager.default.fileExists(atPath: mount) else { return }
                let engine = self.engine
                DispatchQueue.global(qos: .userInitiated).async {
                    let now = (try? engine.drives())?.first { $0.mount == mount }
                    if let now, InstallerVolume.isInstaller(mount: mount, readOnly: now.readOnly) { return }
                    DispatchQueue.main.async {
                        guard let now, now.state == "unindexed", !self.ejectingMounts.contains(mount), self.busy == nil else { return }
                        self.promptIndex(now)
                    }
                }
            }
            return
        }
        for d in drives where d.state == "locked" && !offered.contains(d.mount) {
            offered.insert(d.mount)
            // A remembered passphrase unlocks it without asking.
            if let id = d.id, let secret = Keychain.load(for: id) {
                autoUnlock(d, id: id, secret: secret)
            } else {
                promptUnlock(d)
            }
            return
        }
    }
}
