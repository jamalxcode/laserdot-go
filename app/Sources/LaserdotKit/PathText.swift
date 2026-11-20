import Foundation

/// A folder path as people read it (the owner's review): "Home › Trips ›
/// 2023" rather than "/Users/j/Trips/2023", "iCloud Drive" rather than
/// "~/Library/Mobile Documents/com~apple~CloudDocs", a drive by its name,
/// and no "private" or temporary-folder codes. The full path stays in the
/// tooltip.
public enum PathText {
    /// Where a folder starts.
    public enum Root: Equatable, Sendable { case home, iCloud, temporary, drive(String), computer }

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

    /// Root first, then the folders; long paths keep the last `keep` folders.
    public static func parts(_ dir: String, home: String, rootName: (Root) -> String, keep: Int = 3) -> [String] {
        let (root, folders) = components(dir, home: home)
        let shown = folders.count > keep ? ["…"] + folders.suffix(keep) : folders
        return [rootName(root)] + shown
    }
}
