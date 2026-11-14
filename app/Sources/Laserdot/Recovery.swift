import AppKit
import LaserdotKit

/// Checks every 30 seconds that laserdotd answers (on a connection of its
/// own, so a long job does not count). A daemon that is gone is started
/// again at once; one that stops answering is restarted after two checks in
/// a row. Searches and the menu then carry on by themselves.
final class Watchdog {
    static var interval: TimeInterval = 30

    private let engine: Engine
    private var timer: Timer?
    private var misses = 0
    private var checking = false
    private let queue = DispatchQueue(label: "laserdot.watchdog", qos: .utility)
    private(set) var restarts = 0
    /// Called on the main thread after a restart.
    var onRestart: (() -> Void)?

    init(engine: Engine) {
        self.engine = engine
    }

    func start() {
        timer?.invalidate()
        timer = Timer.scheduledTimer(withTimeInterval: Self.interval, repeats: true) { [weak self] _ in self?.check() }
    }

    /// While Laserdot replaces itself or quits, a stopped daemon is meant.
    func stop() {
        timer?.invalidate()
        timer = nil
    }

    func check() {
        guard !checking else { return }
        checking = true
        let engine = self.engine
        queue.async { [weak self] in
            let alive = engine.isAlive()
            let gone = engine.childRunning == false
            DispatchQueue.main.async {
                guard let self else { return }
                self.checking = false
                if alive {
                    self.misses = 0
                    return
                }
                self.misses += 1
                guard gone || self.misses >= 2 else { return }
                self.misses = 0
                self.restarts += 1
                self.queue.async {
                    engine.restartDaemon()
                    DispatchQueue.main.async { self.onRestart?() }
                }
            }
        }
    }
}

/// Safe start: when Laserdot failed to get going twice in a row, the third
/// launch offers to start with a fresh index (the old one is kept aside, the
/// files themselves are never touched).
enum SafeStart {
    static let attemptsKey = "laserdot.launchAttempts"
    /// The folders that were indexed, kept outside the index so a fresh
    /// index can bring them back.
    static let foldersKey = "laserdot.knownFolders"
    static let reindexKey = "laserdot.reindexKnownFolders"

    /// Counts this launch; returns how many launches in a row have not yet
    /// run for a while.
    static func begin() -> Int {
        let d = UserDefaults.standard
        let n = d.integer(forKey: attemptsKey) + 1
        d.set(n, forKey: attemptsKey)
        d.synchronize() // a crash right after must still count
        return n
    }

    /// Laserdot ran fine (for a while, or quit normally).
    static func succeeded() {
        UserDefaults.standard.set(0, forKey: attemptsKey)
    }

    static func remember(folders: [String]) {
        guard !folders.isEmpty else { return }
        UserDefaults.standard.set(folders, forKey: foldersKey)
    }

    /// Ask, before anything starts; true: start with a fresh index.
    static func offer() -> Bool {
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.alertStyle = .warning
        a.messageText = L("safe.title")
        a.informativeText = L("safe.text")
        a.addButton(withTitle: L("safe.fresh"))
        a.addButton(withTitle: L("safe.usual"))
        return a.runFront() == .alertFirstButtonReturn
    }

    /// Stop a daemon still running on this index, then move the index files
    /// into a dated folder beside them (kept, not deleted).
    static func moveIndexAside(db: String) {
        if let c = DaemonClient.connectLive(socketPath: Paths.socket(forDb: db)) {
            _ = try? c.call(.shutdown, as: String.self)
            Thread.sleep(forTimeInterval: 1)
        }
        let fm = FileManager.default
        let dir = URL(fileURLWithPath: db).deletingLastPathComponent()
        let name = URL(fileURLWithPath: db).lastPathComponent
        let stamp = ISO8601DateFormatter().string(from: Date()).replacingOccurrences(of: ":", with: "-")
        let aside = dir.appendingPathComponent("Index before \(stamp)")
        try? fm.createDirectory(at: aside, withIntermediateDirectories: true)
        for f in (try? fm.contentsOfDirectory(atPath: dir.path)) ?? [] where f.hasPrefix(name) {
            try? fm.moveItem(at: dir.appendingPathComponent(f), to: aside.appendingPathComponent(f))
        }
        UserDefaults.standard.set(true, forKey: reindexKey)
    }

    /// After a fresh start: the folders that were indexed before.
    static func foldersToReindex(nowIndexed: [String]) -> [String] {
        let d = UserDefaults.standard
        guard d.bool(forKey: reindexKey), nowIndexed.isEmpty else { return [] }
        d.set(false, forKey: reindexKey)
        return d.stringArray(forKey: foldersKey) ?? []
    }
}

/// The end of laserdotd's crash log (bugs it caught), for Save Diagnostics.
enum CrashLog {
    static func tail(db: String, lines: Int = 40) -> [String] {
        let path = URL(fileURLWithPath: db).deletingLastPathComponent().appendingPathComponent("laserdotd-crash.log")
        guard let text = try? String(contentsOf: path, encoding: .utf8), !text.isEmpty else { return [] }
        return Array(text.split(separator: "\n", omittingEmptySubsequences: false).suffix(lines)).map(String.init)
    }
}
