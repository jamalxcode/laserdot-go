import Darwin
import Foundation

/// Full Disk Access (macOS privacy, TCC). Laserdot needs it to index and to
/// open files in protected places (Documents, Desktop, Downloads, Pictures,
/// other apps' data): macOS checks the app before handing a file to Preview
/// or the assigned player. Apps cannot grant it to themselves; Laserdot can
/// only tell whether it has it and open the right System Settings page.
public enum DiskAccess {
    /// The System Settings page where the user turns it on.
    public static let settingsURL = URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles")!

    /// Probes files only Full Disk Access may read. Nil when the probes are
    /// missing on this Mac (assume granted then).
    public static var granted: Bool {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let probes = [
            "/Library/Application Support/com.apple.TCC/TCC.db",
            home + "/Library/Application Support/com.apple.TCC/TCC.db",
            home + "/Library/Safari/Bookmarks.plist",
        ]
        for p in probes {
            let fd = Darwin.open(p, O_RDONLY)
            if fd >= 0 {
                Darwin.close(fd)
                return true
            }
            if errno == EPERM || errno == EACCES { return false }
        }
        return true
    }

    /// Paths macOS protects without Full Disk Access.
    public static func isProtected(_ path: String) -> Bool {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        let guarded = ["Documents", "Desktop", "Downloads", "Pictures", "Movies", "Music", "Library"]
        return guarded.contains { path == home + "/" + $0 || path.hasPrefix(home + "/" + $0 + "/") }
            || path.hasPrefix("/Volumes/")
    }
}
