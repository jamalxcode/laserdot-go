import Foundation

/// A quiet record of how Laserdot runs on this Mac, one line per launch — how
/// long it took to start and to answer, how many items are indexed, watchdog
/// restarts, how the app is signed — kept for later (the
/// owner: "do not do a health page, keep a health log, for later, user doesn't
/// care"). Nothing is shown and nothing is sent; Save Diagnostics includes
/// its end. The last 500 launches are kept.
public enum HealthLog {
    public static var url: URL {
        UpdateStatus.defaultURL.deletingLastPathComponent().appendingPathComponent("health-log.txt")
    }

    private static let lock = NSLock()

    /// One launch: "0.9.46 launch=780ms ready=1620ms entries=209443 …".
    public static func line(version: String, launchMS: Int, readyMS: Int, entries: UInt64,
                            restarts: Int, signed: String) -> String {
        "\(version) launch=\(launchMS)ms ready=\(readyMS)ms entries=\(entries) restarts=\(restarts) signed=\(signed)"
    }

    public static func note(_ line: String, at url: URL = url, keep: Int = 500) {
        lock.lock()
        defer { lock.unlock() }
        let stamp = ISO8601DateFormatter().string(from: Date())
        var lines = ((try? String(contentsOf: url, encoding: .utf8)) ?? "").split(separator: "\n").map(String.init)
        lines.append("\(stamp) \(line)")
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? (lines.suffix(keep).joined(separator: "\n") + "\n").write(to: url, atomically: true, encoding: .utf8)
    }

    public static func tail(_ n: Int = 20, at url: URL = url) -> [String] {
        let text = (try? String(contentsOf: url, encoding: .utf8)) ?? ""
        return Array(text.split(separator: "\n").suffix(n)).map(String.init)
    }
}
