import AppKit
import LaserdotKit

/// Two copies of Laserdot (an old one left in Downloads, say) share one
/// identity, and macOS may start the old one at login: its menu has no
/// update item and it can hang on "Starting the index…". At launch the
/// newest copy looks for older ones and offers to move them to the Trash
/// (one click; the Trash keeps them recoverable).
enum OlderCopies {
    /// Older Laserdot copies on this Mac (not this one, not a disk image,
    /// not the version kept for "Go Back").
    static func find() -> [URL] {
        guard let id = Bundle.main.bundleIdentifier, let mine = AppVersion(bundle: Bundle.main.bundleURL) else { return [] }
        let me = Bundle.main.bundleURL.resolvingSymlinksInPath().standardizedFileURL
        let keep = Updater.defaultPreviousDir.resolvingSymlinksInPath().standardizedFileURL.path
        return NSWorkspace.shared.urlsForApplications(withBundleIdentifier: id).filter { u in
            let u = u.resolvingSymlinksInPath().standardizedFileURL
            guard u != me, !u.path.hasPrefix(keep), !u.path.contains("/AppTranslocation/"),
                  (try? u.resourceValues(forKeys: [.volumeIsReadOnlyKey]).volumeIsReadOnly) != true,
                  FileManager.default.isDeletableFile(atPath: u.path),
                  let v = AppVersion(bundle: u) else { return false }
            return v < mine
        }
    }

    static func offerCleanup() {
        let old = find()
        guard !old.isEmpty else { return }
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = L("older.title")
        let lines = old.map { u in "• \(AppVersion(bundle: u)?.short ?? "?") — \((u.path as NSString).abbreviatingWithTildeInPath)" }
        a.informativeText = L("older.text") + "\n\n" + lines.joined(separator: "\n")
        a.addButton(withTitle: L("older.trash"))
        a.addButton(withTitle: L("drive.notNow"))
        guard a.runFront() == .alertFirstButtonReturn else { return }
        // Quit an old copy that is running, then move them all to the Trash.
        for app in NSRunningApplication.runningApplications(withBundleIdentifier: Bundle.main.bundleIdentifier ?? "")
        where app.processIdentifier != ProcessInfo.processInfo.processIdentifier
            && old.contains(where: { $0.standardizedFileURL == app.bundleURL?.standardizedFileURL }) {
            app.terminate()
        }
        NSWorkspace.shared.recycle(old) { _, error in
            if let error { DispatchQueue.main.async { NSAlert(error: error).runFront() } }
        }
    }
}
