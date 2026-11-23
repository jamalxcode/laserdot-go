import Foundation

/// Rename several files at once from a pattern (the owner chose "rename many
/// files at once"): "Trip 2024 – ##" gives "Trip 2024 – 01.jpg", "… 02.jpg";
/// the #s set the digits; without #, a number is added at the end. Each file
/// keeps its own extension. Nothing is renamed if any new name is a problem.
public enum BatchRename {
    public static func names(for files: [String], pattern: String, start: Int = 1) -> [String] {
        let base = pattern.trimmingCharacters(in: .whitespaces)
        let hashes = base.filter { $0 == "#" }.count
        let digits = max(hashes, String(start + files.count - 1).count)
        return files.enumerated().map { i, name in
            let n = String(format: "%0\(digits)d", start + i)
            let stem: String
            if let r = base.range(of: String(repeating: "#", count: hashes)), hashes > 0 {
                stem = base.replacingCharacters(in: r, with: n)
            } else {
                stem = base.isEmpty ? n : "\(base) \(n)"
            }
            let ext = (name as NSString).pathExtension
            return ext.isEmpty ? stem : "\(stem).\(ext)"
        }
    }

    /// Why the new names cannot be used (nil: they can): empty, a "/", the
    /// same name twice.
    public static func problem(_ names: [String]) -> String? {
        if names.contains(where: { $0.trimmingCharacters(in: .whitespaces).isEmpty || $0.hasPrefix(".") }) { return "empty" }
        if names.contains(where: { $0.contains("/") || $0.contains(":") }) { return "slash" }
        if Set(names.map { $0.lowercased() }).count != names.count { return "twice" }
        return nil
    }
}
