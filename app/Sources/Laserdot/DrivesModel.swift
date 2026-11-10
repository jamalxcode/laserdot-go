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
        let engine = self.engine
        queue.async { [weak self] in
            let outcome = Result { try engine.drives() }
            DispatchQueue.main.async {
                guard let self else { return }
                self.drives = (try? outcome.get()) ?? []
                if case let .failure(e) = outcome { self.available = !e.isUnsupported } else { self.available = true }
                self.offerNewDrives()
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
        let engine = self.engine
        queue.async { [weak self] in
            let outcome = Result { try work(engine) }
            DispatchQueue.main.async {
                self?.busy = nil
                if case .failure(let e) = outcome {
                    let alert = NSAlert()
                    alert.messageText = title
                    alert.informativeText = Self.message(e)
                    alert.runModal()
                }
                self?.refresh()
            }
        }
    }

    private static func message(_ e: Error) -> String {
        if e.isUnsupported { return L("feature.unsupported") }
        return (e as? LaserdotError)?.message ?? e.localizedDescription
    }

    /// Ask before indexing: what it will take and where the index goes.
    func promptIndex(_ d: Drive, rebuild: Bool = false) {
        let mount = d.mount
        queue.async { [weak self] in
            let plan = try? self?.engine.drivePlan(mount: mount)
            DispatchQueue.main.async { self?.showIndexPrompt(d, plan: plan, rebuild: rebuild) }
        }
    }

    private func showIndexPrompt(_ d: Drive, plan: DrivePlan?, rebuild: Bool) {
        NSApp.activate(ignoringOtherApps: true)
        let alert = NSAlert()
        alert.messageText = String(format: L(rebuild ? "drive.rebuild.title" : "drive.index.title"), d.name)
        if let p = plan {
            let size = ByteCountFormatter.string(fromByteCount: Int64(p.bytes), countStyle: .file)
            let index = ByteCountFormatter.string(fromByteCount: Int64(p.indexEstimateBytes), countStyle: .file)
            var text = String(format: L("drive.index.estimate"), Int64(p.files), size, index)
            if !p.onDrive { text += "\n" + L("drive.index.offDrive") }
            alert.informativeText = text
        }
        let encrypt = NSButton(checkboxWithTitle: L("drive.index.encrypt"), target: nil, action: nil)
        let pass = NSSecureTextField(frame: NSRect(x: 0, y: 0, width: 260, height: 24))
        pass.placeholderString = L("drive.passphrase")
        let stack = NSStackView(views: [encrypt, pass])
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.frame = NSRect(x: 0, y: 0, width: 260, height: 54)
        alert.accessoryView = stack
        alert.addButton(withTitle: L(rebuild ? "drive.rebuild" : "drive.index"))
        alert.addButton(withTitle: L("drive.notNow"))
        if !rebuild { alert.addButton(withTitle: L("drive.never")) }
        switch alert.runModal() {
        case .alertFirstButtonReturn:
            let secret = encrypt.state == .on && !pass.stringValue.isEmpty ? pass.stringValue : nil
            let mount = d.mount
            run(String(format: L("drive.indexing"), d.name)) { engine in
                if rebuild {
                    _ = try engine.driveRebuild(mount: mount, passphrase: secret)
                } else {
                    _ = try engine.driveIndex(mount: mount, passphrase: secret)
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
        alert.accessoryView = pass
        alert.addButton(withTitle: L("drive.unlock"))
        alert.addButton(withTitle: L("drive.notNow"))
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let secret = pass.stringValue
        let mount = d.mount
        run(String(format: L("drive.unlock.title"), d.name)) { engine in
            try engine.driveUnlock(mount: mount, passphrase: secret)
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
        alert.alertStyle = .warning
        alert.addButton(withTitle: L("drive.delete"))
        alert.addButton(withTitle: L("drive.notNow"))
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let mount = d.mount
        run(d.name) { engine in _ = try engine.driveDelete(mount: mount) }
    }

    func eject(_ d: Drive) {
        let mount = d.mount
        run(String(format: L("drive.ejecting"), d.name)) { engine in try engine.driveEject(mount: mount) }
    }

    func reveal(_ d: Drive) {
        NSWorkspace.shared.activateFileViewerSelecting([URL(fileURLWithPath: d.mount)])
    }

    /// The first time an unindexed drive mounts this
    /// session, offer to index it (unless the user said never).
    private func offerNewDrives() {
        guard busy == nil else { return }
        let declined = Set(UserDefaults.standard.stringArray(forKey: declinedKey) ?? [])
        for d in drives where d.state == "unindexed" && !offered.contains(d.mount) && !declined.contains(d.name) {
            offered.insert(d.mount)
            promptIndex(d)
            return
        }
        for d in drives where d.state == "locked" && !offered.contains(d.mount) {
            offered.insert(d.mount)
            promptUnlock(d)
            return
        }
    }
}
