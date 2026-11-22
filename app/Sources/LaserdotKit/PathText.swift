import Foundation

/// A folder path as people read it (the owner's review): "Home › Trips ›
/// 2023" rather than "/Users/j/Trips/2023", "iCloud Drive" rather than
/// "~/Library/Mobile Documents/com~apple~CloudDocs", a drive by its name,
/// and no "private" or temporary-folder codes. The full path stays in the
/// tooltip.
public enum PathText {
    /// Where a folder starts.
    /// `iCloudApp`: an app's own iCloud folder (Ulysses, Pages…), by the
    /// app's name as Finder shows it (`container` is its path, for asking
    /// macOS that name; `name` the guess from its id when macOS cannot say).
    public enum Root: Equatable, Sendable {
        case home, iCloud, temporary, drive(String), computer
        case iCloudApp(name: String, container: String)
    }

    /// The folder `dir` as its root and the folders under it.
    public static func components(_ dir: String, home: String) -> (root: Root, folders: [String]) {
        var d = dir
        for p in ["/private/var/", "/private/tmp/", "/private/etc/"] where d.hasPrefix(p) {
            d = String(d.dropFirst("/private".count))
        }
        func rest(_ prefix: String) -> [String] {
            String(d.dropFirst(prefix.count)).split(separator: "/").map(String.init)
        }
        let icloud = home + "/Library/Mobile Documents/com~apple~CloudDocs"
        if d == icloud || d.hasPrefix(icloud + "/") { return (.iCloud, rest(icloud)) }
        // An app's iCloud folder ("X5AZV975AG~com~soulmen~ulysses3"): the
        // owner saw "…/Mobile Documents/X5AZ…~ulysses3/Documents/…" where
        // Finder says "iCloud Drive › Ulysses". Its Documents folder is
        // implied, as in Finder.
        let mobile = home + "/Library/Mobile Documents/"
        if d.hasPrefix(mobile) {
            var parts = rest(mobile)
            if let id = parts.first {
                let container = mobile + id
                parts.removeFirst()
                if parts.first == "Documents" { parts.removeFirst() }
                return (.iCloudApp(name: appName(fromContainer: id), container: container), parts)
            }
        }
        if d == home || d.hasPrefix(home + "/") { return (.home, rest(home)) }
        if d.hasPrefix("/Volumes/") {
            let parts = rest("/Volumes/")
            return (.drive(parts.first ?? ""), Array(parts.dropFirst()))
        }
        // macOS's per-user temporary folders: /var/folders/xx/yyyy/T/…
        if d.hasPrefix("/var/folders/") {
            let parts = rest("/var/folders/")
            if parts.count >= 3 { return (.temporary, Array(parts.dropFirst(3))) }
            return (.temporary, [])
        }
        if d == "/tmp" || d.hasPrefix("/tmp/") || d == "/var/tmp" || d.hasPrefix("/var/tmp/") {
            return (.temporary, rest(d.hasPrefix("/var/tmp") ? "/var/tmp" : "/tmp"))
        }
        return (.computer, d.split(separator: "/").map(String.init))
    }

    /// "X5AZV975AG~com~soulmen~ulysses3" → "Ulysses": the id's last part,
    /// without a version number, capitalised.
    public static func appName(fromContainer id: String) -> String {
        var last = String(id.split(separator: "~").last ?? Substring(id))
        while let c = last.last, c.isNumber, last.count > 1 { last.removeLast() }
        return last.prefix(1).uppercased() + last.dropFirst()
    }

    /// Root first, then the folders; long paths keep the last `keep` folders.
    public static func parts(_ dir: String, home: String, rootName: (Root) -> String, keep: Int = 3) -> [String] {
        let (root, folders) = components(dir, home: home)
        let shown = folders.count > keep ? ["…"] + folders.suffix(keep) : folders
        return [rootName(root)] + shown
    }
}
