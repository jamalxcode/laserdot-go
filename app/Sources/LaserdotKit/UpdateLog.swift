import Foundation

/// A short log of what each update did and how long each step took
/// (update-log.txt beside update.json, the last 300 lines). Save
/// Diagnostics includes it, so a slow or stuck update can be explained.
/// It holds tool names and durations only, never paths or file names.
public enum UpdateLog {
    public static var url: URL {
        UpdateStatus.defaultURL.deletingLastPathComponent().appendingPathComponent("update-log.txt")
    }

    private static let lock = NSLock()

    public static func note(_ line: String, at url: URL = url) {
        lock.lock()
        defer { lock.unlock() }
        let stamp = ISO8601DateFormatter().string(from: Date())
        var lines = ((try? String(contentsOf: url, encoding: .utf8)) ?? "")
            .split(separator: "\n").map(String.init)
        lines.append("\(stamp) \(line)")
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? (lines.suffix(300).joined(separator: "\n") + "\n").write(to: url, atomically: true, encoding: .utf8)
    }

    public static func tail(_ n: Int = 40, at url: URL = url) -> [String] {
        let text = (try? String(contentsOf: url, encoding: .utf8)) ?? ""
        return Array(text.split(separator: "\n").suffix(n)).map(String.init)
    }
}

/// A tool's output, filled by the thread reading it.
final class ToolOutput: @unchecked Sendable {
    private let lock = NSLock()
    private var data = Data()
    func set(_ d: Data) { lock.lock(); data = d; lock.unlock() }
    func get() -> Data { lock.lock(); defer { lock.unlock() }; return data }
}
