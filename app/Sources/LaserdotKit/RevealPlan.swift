import Foundation

/// How to show a file in Finder. Finder does not go inside packages (an app,
/// a .ulysses or .photoslibrary bundle) when asked to select a file in one:
/// it stops at an outer folder (the owner: "show in finder doesn't really
/// take us to the finder location", for a picture inside a Ulysses sheet).
/// A file inside a package is shown in a window opened on its own folder
/// instead, the way Show Package Contents does.
public enum RevealPlan: Equatable, Sendable {
    /// Select it in its folder (the usual way).
    case select
    /// Open a window on `folder` (inside a package) with the file selected.
    case insidePackage(folder: String, package: String)

    public static func plan(for path: String) -> RevealPlan {
        guard let package = outermostPackage(containing: path) else { return .select }
        return .insidePackage(folder: (path as NSString).deletingLastPathComponent, package: package)
    }

    /// The outermost package folder `path` is inside (not `path` itself).
    public static func outermostPackage(containing path: String) -> String? {
        var found: String?
        var url = URL(fileURLWithPath: path).standardizedFileURL.deletingLastPathComponent()
        while url.path != "/" && !url.path.isEmpty {
            if (try? url.resourceValues(forKeys: [.isPackageKey]))?.isPackage == true {
                found = url.path
            }
            url = url.deletingLastPathComponent()
        }
        return found
    }
}
