import AppKit
import Foundation

/// Notices when Laserdot's window stops answering (the beachball) and writes
/// it down, so Save Diagnostics can say whether a freeze was Laserdot's and
/// what it was showing (the owner saw the beachball and could not tell). A
/// background timer asks the main thread to answer every half second; a gap of
/// 2 seconds or more is a freeze. Nothing leaves the Mac.
final class HangMonitor {
    static let shared = HangMonitor()
    /// What the window showed, in a few words (never a search or a path).
    var context: () -> String = { "" }

    private let queue = DispatchQueue(label: "laserdot.hangs", qos: .utility)
    private var timer: DispatchSourceTimer?
    /// Seconds since boot, not counting sleep (a sleeping Mac is no freeze).
    private var lastAnswer = ProcessInfo.processInfo.systemUptime
    private var lastAnswerDate = Date()
    private var frozenSince: TimeInterval?
    private var lastContext = ""
    private let lock = NSLock()

    static var logURL: URL {
        FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Logs/Laserdot/app-hangs.log")
    }

    func start() {
        guard timer == nil else { return }
        let t = DispatchSource.makeTimerSource(queue: queue)
        t.schedule(deadline: .now() + 1, repeating: 0.5)
        t.setEventHandler { [weak self] in self?.tick() }
        t.resume()
        timer = t
    }

    private func tick() {
        lock.lock()
        let since = ProcessInfo.processInfo.systemUptime - lastAnswer
        // Over two minutes is macOS pausing a background app, not a freeze.
        if since >= 2, since < 120, frozenSince == nil {
            frozenSince = lastAnswer
            write("froze at \(Self.stamp(lastAnswerDate)) (\(lastContext))")
        }
        lock.unlock()
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            let ctx = self.context()
            self.lock.lock()
            let now = ProcessInfo.processInfo.systemUptime
            if let f = self.frozenSince {
                self.write(String(format: "answered again after %.1f s", now - f))
                self.frozenSince = nil
            }
            self.lastAnswer = now
            self.lastAnswerDate = Date()
            self.lastContext = ctx
            self.lock.unlock()
        }
    }

    private func write(_ line: String) {
        let url = Self.logURL
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        let text = line + "\n"
        // Kept short: the last 200 lines once it passes 64 KB.
        if let size = (try? FileManager.default.attributesOfItem(atPath: url.path))?[.size] as? Int, size > 64_000,
           let old = try? String(contentsOf: url, encoding: .utf8) {
            let kept = old.split(separator: "\n").suffix(200).joined(separator: "\n") + "\n"
            try? kept.write(to: url, atomically: true, encoding: .utf8)
        }
        if let h = try? FileHandle(forWritingTo: url) {
            h.seekToEndOfFile()
            h.write(Data(text.utf8))
            try? h.close()
        } else {
            try? text.write(to: url, atomically: true, encoding: .utf8)
        }
    }

    /// The last freezes (for Save Diagnostics).
    static func tail(lines: Int = 30) -> [String] {
        guard let text = try? String(contentsOf: logURL, encoding: .utf8) else { return [] }
        return Array(text.split(separator: "\n").suffix(lines)).map(String.init)
    }

    private static func stamp(_ d: Date) -> String {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd HH:mm:ss"
        return f.string(from: d)
    }
}
