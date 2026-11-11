import AppKit
import LaserdotKit

/// Asks for Full Disk Access (from the menu, and when indexing a protected
/// folder fails): it lets laserdotd index Documents, Desktop, Pictures and
/// drives without per-folder prompts. Opening files does not need it. After opening System Settings it watches for
/// the switch and offers to restart Laserdot, which macOS requires.
final class DiskAccessPrompt {
    static let shared = DiskAccessPrompt()
    private static let quietKey = "laserdot.fullDiskAccess.dontAsk"
    private var watching = false

    /// Launch: ask once, unless granted or the user said not to ask.
    func checkAtLaunch() {
        guard !DiskAccess.granted, !UserDefaults.standard.bool(forKey: Self.quietKey) else { return }
        ask(reason: L("fda.launch"), allowDontAsk: true)
    }

    /// From the menu: always ask (or say it is already on).
    func askFromMenu() {
        if DiskAccess.granted {
            info(L("fda.already"))
        } else {
            ask(reason: L("fda.launch"), allowDontAsk: false)
        }
    }

    /// Open a file in its default app (Preview, the assigned player…). The
    /// app is not sandboxed, so this needs no Full Disk Access: the other app
    /// opens the file with its own permissions.
    func open(_ url: URL) {
        NSWorkspace.shared.open(url)
    }

    private func ask(reason: String, allowDontAsk: Bool, tryAnyway: URL? = nil) {
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = L("fda.title")
        a.informativeText = reason + "\n\n" + L("fda.steps")
        a.addButton(withTitle: L("fda.openSettings"))
        a.addButton(withTitle: L("drive.notNow"))
        if tryAnyway != nil { a.addButton(withTitle: L("fda.tryAnyway")) }
        if allowDontAsk {
            a.showsSuppressionButton = true
            a.suppressionButton?.title = L("fda.dontAsk")
        }
        let r = a.runFront()
        if a.suppressionButton?.state == .on { UserDefaults.standard.set(true, forKey: Self.quietKey) }
        switch r {
        case .alertFirstButtonReturn:
            NSWorkspace.shared.open(DiskAccess.settingsURL)
            watchForGrant()
        case .alertThirdButtonReturn:
            if let u = tryAnyway { NSWorkspace.shared.open(u) }
        default:
            break
        }
    }

    /// Once the switch is on, offer the restart macOS needs.
    private func watchForGrant(waited: Int = 0) {
        guard !watching || waited > 0 else { return }
        watching = true
        if DiskAccess.granted {
            watching = false
            NSApp.activate(ignoringOtherApps: true)
            let a = NSAlert()
            a.messageText = L("fda.grantedTitle")
            a.informativeText = L("fda.grantedText")
            a.addButton(withTitle: L("fda.restart"))
            a.addButton(withTitle: L("drive.notNow"))
            if a.runFront() == .alertFirstButtonReturn {
                Updater.relaunch(Bundle.main.bundleURL)
                NSApp.terminate(nil)
            }
            return
        }
        guard waited < 300 else {
            watching = false
            return
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
            self?.watchForGrant(waited: waited + 2)
        }
    }

    private func info(_ text: String) {
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = text
        a.runFront()
    }
}
