import AppKit
import Darwin
import LaserdotKit

/// macOS asks before an app may read Desktop, Documents, Downloads, iCloud
/// Drive and external drives — each one the first time the app touches it.
/// Left to indexing, those prompts come one at a time, minutes apart. So
/// Laserdot touches each place itself, in one go, before laserdotd starts:
/// the prompts come back to back, and indexing begins once they are answered.
/// (laserdotd is started by the app, so macOS applies the answers to it too.)
/// Places already answered return at once, without a prompt.
enum Permissions {
    struct Place {
        /// For the message: "Desktop", "Documents"…
        let name: String
        let path: String
    }

    /// The protected places on this Mac (one per kind of drive mounted now).
    static func places() -> [Place] {
        let fm = FileManager.default
        let home = fm.homeDirectoryForCurrentUser
        var list: [Place] = []
        for sub in ["Desktop", "Documents", "Downloads"] {
            let u = home.appendingPathComponent(sub)
            if fm.fileExists(atPath: u.path) { list.append(Place(name: fm.displayName(atPath: u.path), path: u.path)) }
        }
        let icloud = home.appendingPathComponent("Library/Mobile Documents/com~apple~CloudDocs")
        if fm.fileExists(atPath: icloud.path) { list.append(Place(name: L("permissions.icloud"), path: icloud.path)) }
        // Removable and network drives are one permission each, whichever drive.
        let keys: [URLResourceKey] = [.volumeIsRemovableKey, .volumeIsEjectableKey, .volumeIsLocalKey, .volumeIsInternalKey]
        var removable = false, network = false
        for v in fm.mountedVolumeURLs(includingResourceValuesForKeys: keys, options: [.skipHiddenVolumes]) ?? [] {
            guard v.path.hasPrefix("/Volumes/"), let r = try? v.resourceValues(forKeys: Set(keys)) else { continue }
            if r.volumeIsLocal == false, !network {
                network = true
                list.append(Place(name: L("permissions.network"), path: v.path))
            } else if r.volumeIsLocal != false, r.volumeIsInternal != true || r.volumeIsRemovable == true || r.volumeIsEjectable == true,
                      !removable {
                removable = true
                list.append(Place(name: L("permissions.removable"), path: v.path))
            }
        }
        return list
    }

    /// Touches every place in turn (each prompt waits for its answer), then
    /// calls `done` on the main thread with the places macOS refused.
    static func askAll(done: @escaping ([Place]) -> Void) {
        let list = places()
        DispatchQueue.global(qos: .userInitiated).async {
            var refused: [Place] = []
            for p in list {
                guard let dir = opendir(p.path) else {
                    if errno == EPERM || errno == EACCES { refused.append(p) }
                    continue
                }
                _ = readdir(dir)
                closedir(dir)
            }
            DispatchQueue.main.async { done(refused) }
        }
    }

    /// After the prompts: if some places were refused, say once what that
    /// means and offer Full Disk Access (one switch for everything).
    static func explain(_ refused: [Place]) {
        let key = "laserdot.permissions.refusedShown"
        let names = refused.map(\.name).sorted()
        guard !names.isEmpty, !DiskAccess.granted,
              UserDefaults.standard.stringArray(forKey: key) != names else { return }
        UserDefaults.standard.set(names, forKey: key)
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = L("permissions.refusedTitle")
        a.informativeText = String(format: L("permissions.refusedText"), names.joined(separator: ", ")) + "\n\n" + L("fda.steps")
        a.addButton(withTitle: L("fda.openSettings"))
        a.addButton(withTitle: L("drive.notNow"))
        if a.runFront() == .alertFirstButtonReturn { DiskAccessPrompt.shared.askFromMenu() }
    }
}
