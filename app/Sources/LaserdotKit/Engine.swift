import Foundation

/// What went wrong, in the terms the UI acts on.
public enum LaserdotError: Error, LocalizedError, Equatable {
    /// The query is invalid; `start..<end` are Unicode-scalar offsets to underline.
    case query(message: String, start: Int, end: Int, suggestion: String?)
    /// The daemon could not be reached or started.
    case unavailable(String)
    /// The drive's index is encrypted: ask for its passphrase.
    case locked(String)
    /// This laserdotd does not implement the request yet (code `unsupported`):
    /// hide or disable the feature.
    case unsupported(String)
    case failed(message: String, code: String?)

    public var message: String {
        switch self {
        case let .query(m, _, _, _), let .unavailable(m), let .locked(m), let .unsupported(m), let .failed(m, _):
            return m
        }
    }

    public var errorDescription: String? { message }

    init(_ e: RemoteError) {
        if let s = e.span {
            self = .query(message: e.message, start: s.start, end: s.end, suggestion: e.suggestion)
        } else if e.code == "drive_locked" {
            self = .locked(e.message)
        } else if e.code == "unsupported" {
            self = .unsupported(e.message)
        } else {
            self = .failed(message: e.message, code: e.code)
        }
    }
}

extension Error {
    /// The daemon answered `unsupported`: it does not have this feature yet.
    public var isUnsupported: Bool {
        if case .unsupported? = self as? LaserdotError { return true }
        return false
    }
}

// MARK: the engine

/// The app's handle on laserdotd (spec §13.2): the app never opens the index
/// itself. Calls are serialized on one shared connection, which is reopened
/// once if it broke; if nothing answers, the bundled daemon is started.
/// Blocking — call it off the main thread.
public final class Engine: @unchecked Sendable {
    public let dbPath: String
    public let socketPath: String
    let daemonPath: String?
    let lock = NSLock()
    var client: DaemonClient?
    var child: Process?
    var log: [String] = []
    let logLock = NSLock()
    /// Ops this daemon answered `unsupported`; forgotten on reconnect.
    var unsupported = Set<String>()
    /// Set by `retire()`: no more requests, and no daemon is started again.
    var retired = false
    /// Guidance runs on a connection of its own, so typing never waits for
    /// an index or a scan on the shared one.
    let guideLock = NSLock()
    var guideClient: DaemonClient?
    var guideUnsupported = Set<String>()

    /// The version the bundled daemon reports; a running daemon of another
    /// version (left over from before an update, or after a force quit) is
    /// stopped and the bundled one started instead.
    let expectedVersion: String?
    let versionLock = NSLock()
    var versionChecked = false

    /// `dbPath`: index location (default: the per-user one).
    /// `daemonPath`: the bundled `laserdotd`, started if none is running.
    /// `expectedVersion`: the bundled daemon's version (the app's version).
    public init(dbPath: String? = nil, daemonPath: String? = nil, expectedVersion: String? = nil) {
        let db = dbPath ?? Paths.defaultDbPath
        self.dbPath = db
        socketPath = Paths.socket(forDb: db)
        self.daemonPath = daemonPath
        self.expectedVersion = expectedVersion
    }

    /// Once per engine, before the first connection: replace a running
    /// daemon whose version is not the bundled one's.
    func replaceStaleDaemon() {
        versionLock.lock()
        defer { versionLock.unlock() }
        guard !versionChecked else { return }
        versionChecked = true
        guard let want = expectedVersion, daemonPath != nil,
              let c = DaemonClient.connectLive(socketPath: socketPath),
              let s = try? c.call(.status, as: Wire.Status.self), s.version != want else { return }
        _ = try? c.call(.shutdown, as: String.self)
        // Wait until it has really exited (closed the index), not just
        // closed its socket.
        let pid = pid_t(s.pid)
        let deadline = Date().addingTimeInterval(15)
        while Date() < deadline, DaemonClient.connectLive(socketPath: socketPath) != nil || (pid > 0 && kill(pid, 0) == 0) {
            usleep(100_000)
        }
    }

    /// Whether the daemon has not (yet) refused `op` as unsupported.
    public func supports(_ op: String) -> Bool {
        lock.lock()
        let shared = !unsupported.contains(op)
        lock.unlock()
        guideLock.lock()
        defer { guideLock.unlock() }
        return shared && !guideUnsupported.contains(op)
    }

    /// Connect to the daemon, starting it if needed.
    @discardableResult
    public func connect() throws -> DaemonStatus {
        _ = try call(.status, as: Wire.Status.self)
        return status()
    }

    public func status() -> DaemonStatus {
        guard let s = try? call(.status, as: Wire.Status.self) else { return .down }
        let roots = (try? call(.roots, as: [Wire.RootInfo].self)) ?? []
        return DaemonStatus(
            running: true, version: s.version, protocolVersion: s.protocolVersion, networkDenied: s.networkDenied,
            watcher: s.watcher, entries: s.entries, folders: roots.map(\.path), indexing: s.indexing,
            catchUp: s.catchUp)
    }

    public func search(query: String, limit: Int = 100, offset: Int = 0, sort: SortKey = .relevance, reverse: Bool = false) throws -> Results {
        let r = try call(.search(query: query, sort: sort, reverse: reverse, offset: offset, limit: limit, typo: true),
                         as: Wire.SearchResults.self)
        return Results(
            total: r.total, hits: r.hits.map(Hit.init), elapsedMs: Double(r.elapsedUs) / 1000,
            typoFallback: r.typoFallback, notes: r.warnings.map(\.message))
    }

    /// Forget which results were opened (Settings ▸ Clear History).
    public func clearOpens() throws {
        _ = try call(.clearOpens, as: String.self)
    }

    /// Folders never indexed.
    public func exclusions() -> [String] {
        (try? call(.exclusions, as: [String].self)) ?? []
    }

    @discardableResult
    public func exclude(path: String) throws -> [String] {
        try call(.exclude(path: path), as: [String].self)
    }

    @discardableResult
    public func include(path: String) throws -> [String] {
        try call(.include(path: path), as: [String].self)
    }

    /// The last lines laserdotd wrote (for Save Diagnostics).
    public func logTail() -> [String] {
        logLock.lock()
        defer { logLock.unlock() }
        return log
    }

    /// The results opened most recently (empty with older daemons).
    public func recentOpens(limit: Int = 8) -> [Hit] {
        ((try? call(.recentOpens(limit: limit), as: [Wire.Hit].self)) ?? []).map(Hit.init)
    }

    /// Note that the user opened `path` (spec §6.2). Older daemons answer
    /// `unsupported`; that is fine.
    public func recordOpen(path: String) {
        _ = try? call(.opened(path: path), as: String.self)
    }

    /// Index a folder (or refresh it if already indexed). Returns a summary.
    @discardableResult
    public func index(path: String) throws -> String {
        // Indexing a home folder takes minutes: a connection of its own, so
        // searches on the shared one are not held up meanwhile.
        // No time limit: a very large folder can take longer than an hour.
        let s = try longCall(longJobConnection(), .index(path: path), as: Wire.IndexStats.self)
        return s.incremental
            ? "\(s.added) added, \(s.removed) removed, \(s.renamed) renamed"
            : "\(s.files) files, \(s.folders) folders"
    }

    @discardableResult
    public func forget(path: String) throws -> Bool {
        try call(.forget(path: path), as: Bool.self)
    }

    // MARK: query guidance (spec §8.1, §8.2, §8.9)

    // Cheap enough to call on every keystroke. Answered by the daemon; when
    // it is not running (it is never started for these) or does not have
    // the op, the bundled registry copy answers instead (Guidance.swift).

    /// The hint line under the query box; `cursor` in Unicode scalars.
    public func hint(query: String, cursor: Int, lang: String) -> String? {
        if let answer = guide(.hint(query: query, cursor: cursor, lang: lang), as: String.self) { return answer }
        return LaserdotKit.hint(query: query, cursor: cursor, lang: lang)
    }

    /// The cheat-sheet section id (`File`, `Video`, …) under the caret.
    public func sectionAt(query: String, cursor: Int) -> String? {
        if let answer = guide(.sectionAt(query: query, cursor: cursor), as: String.self) { return answer }
        return LaserdotKit.sectionAt(query: query, cursor: cursor)
    }

    /// Autocomplete for the token under the caret.
    public func completions(query: String, cursor: Int, lang: String) -> [Completion] {
        if let answer = guide(.completions(query: query, cursor: cursor, lang: lang), as: [Completion].self) {
            return answer ?? [] // null: nothing to complete
        }
        return LaserdotKit.completions(query: query, cursor: cursor, lang: lang)
    }

    /// Every filter, for the cheat sheet, in `lang`.
    public func filters(lang: String) -> [FilterInfo] {
        if let answer = guide(.filters(lang: lang), as: [FilterInfo].self), let list = answer { return list }
        return LaserdotKit.filters(lang: lang)
    }

    /// The plain-text cheat sheet.
    public func cheatSheet(lang: String) -> String {
        if let answer = guide(.cheatSheet(lang: lang), as: String.self), let text = answer { return text }
        return LaserdotKit.cheatSheet(lang: lang)
    }

    /// Whether the last guidance call was answered by the daemon (not the
    /// bundled fallback).
    public private(set) var guidanceFromDaemon = false

    /// The daemon's answer, as `.some(value)` (the value may be null), or
    /// nil to fall back to the bundled registry.
    func guide<T: Decodable>(_ request: DaemonRequest, as type: T.Type) -> T?? {
        guideLock.lock()
        defer { guideLock.unlock() }
        guidanceFromDaemon = false
        if guideUnsupported.contains(request.op) { return nil }
        if guideClient == nil {
            replaceStaleDaemon()
            guard let c = DaemonClient.connectLive(socketPath: socketPath) else { return nil }
            c.setTimeout(5)
            guideClient = c
            guideUnsupported.removeAll()
        }
        do {
            let value = try guideClient?.callNullable(request, as: T.self)
            guidanceFromDaemon = true
            return .some(value)
        } catch DaemonClientError.remote(let e) {
            if e.code == "unsupported" { guideUnsupported.insert(request.op) }
            return nil
        } catch {
            guideClient = nil // broken: reconnect next time
            return nil
        }
    }

    // MARK: lifecycle

    /// Stop the daemon, whoever started it, and never start one again
    /// (Erase All Data: nothing may write the index while it is deleted).
    public func retire() {
        lock.lock()
        retired = true
        lock.unlock()
        shutdown()
        if let live = DaemonClient.connectLive(socketPath: socketPath) {
            _ = try? live.call(.shutdown, as: String.self)
        }
        let deadline = Date().addingTimeInterval(5)
        while DaemonClient.connectLive(socketPath: socketPath) != nil && Date() < deadline { usleep(50_000) }
    }

    /// Stop a daemon this engine started (one started elsewhere keeps running).
    public func shutdown() {
        lock.lock()
        let c = client
        client = nil
        let p = child
        child = nil
        lock.unlock()
        guard let p else { return }
        _ = try? c?.call(.shutdown, as: String.self)
        let deadline = Date().addingTimeInterval(5)
        while p.isRunning && Date() < deadline { usleep(20_000) }
        if p.isRunning { p.terminate() }
    }

    // MARK: watchdog

    /// The daemon answers a ping within `timeout` seconds, on a connection of
    /// its own (so a slow request on the shared one does not count).
    public func isAlive(timeout: TimeInterval = 5) -> Bool {
        guard let c = try? DaemonClient(socketPath: socketPath, timeout: timeout) else { return false }
        return (try? c.call(.ping, as: String.self)) == "pong"
    }

    /// Whether the daemon this app started is still running (nil: this app
    /// did not start it).
    public var childRunning: Bool? {
        child.map(\.isRunning)
    }

    /// The daemon this app started (the self-test kills it to check the
    /// watchdog).
    public var childPID: Int32? {
        child.map(\.processIdentifier)
    }

    /// The watchdog: stop a daemon that stopped answering and start a fresh
    /// one. The process is ended first (without the shared lock, which a
    /// request stuck on the hung daemon may hold): that also breaks the stuck
    /// request, so the lock frees.
    public func restartDaemon() {
        if retired { return }
        if let p = child, p.isRunning {
            p.terminate()
            let deadline = Date().addingTimeInterval(3)
            while p.isRunning && Date() < deadline { usleep(50_000) }
            if p.isRunning { kill(p.processIdentifier, SIGKILL) }
        }
        lock.lock()
        child = nil
        client = nil
        lock.unlock()
        _ = status() // starts the bundled daemon again
    }

    /// Run one request on the shared connection: reconnect once if it broke,
    /// and start the bundled daemon if nothing answers.
    public func call<T: Decodable>(_ request: DaemonRequest, as type: T.Type = T.self) throws -> T {
        lock.lock()
        defer { lock.unlock() }
        if retired { throw LaserdotError.unavailable("Laserdot is stopping") }
        if unsupported.contains(request.op) {
            throw LaserdotError.unsupported("`\(request.op)` is not supported by this laserdotd")
        }
        if let c = client {
            do {
                return try c.call(request, as: T.self)
            } catch DaemonClientError.remote(let e) {
                throw remote(e, request.op)
            } catch {
                client = nil // broken: reconnect below
            }
        }
        replaceStaleDaemon()
        let c: DaemonClient
        if let live = DaemonClient.connectLive(socketPath: socketPath) {
            c = live
        } else {
            c = try startDaemon()
        }
        client = c
        unsupported.removeAll()
        do {
            return try c.call(request, as: T.self)
        } catch DaemonClientError.remote(let e) {
            throw remote(e, request.op)
        } catch {
            client = nil
            throw LaserdotError.unavailable("\(error)")
        }
    }

    /// Map a refusal, remembering unsupported ops. Called with `lock` held.
    func remote(_ e: RemoteError, _ op: String) -> LaserdotError {
        let err = LaserdotError(e)
        if case .unsupported = err { unsupported.insert(op) }
        return err
    }

    func markUnsupported(_ op: String) {
        lock.lock()
        unsupported.insert(op)
        lock.unlock()
    }

    /// Start the bundled daemon and wait for it to answer. Called with
    /// `lock` held.
    func startDaemon() throws -> DaemonClient {
        guard let exe = daemonPath else { throw LaserdotError.unavailable("laserdotd is not running") }
        if let old = child, old.isRunning { old.terminate() }
        let p = Process()
        p.executableURL = URL(fileURLWithPath: exe)
        p.arguments = ["--db", dbPath, "--socket", socketPath]
        p.standardInput = FileHandle.nullDevice
        p.standardOutput = FileHandle.nullDevice
        let errPipe = Pipe()
        p.standardError = errPipe
        logLock.lock()
        log = []
        logLock.unlock()
        // Forward the daemon's log to ours, keeping the last lines for errors.
        errPipe.fileHandleForReading.readabilityHandler = { [weak self] h in
            let data = h.availableData
            guard !data.isEmpty else {
                h.readabilityHandler = nil
                return
            }
            FileHandle.standardError.write(data)
            guard let self, let text = String(data: data, encoding: .utf8) else { return }
            self.logLock.lock()
            self.log.append(contentsOf: text.split(separator: "\n").map(String.init))
            if self.log.count > 20 { self.log.removeFirst(self.log.count - 20) }
            self.logLock.unlock()
        }
        do {
            try p.run()
        } catch {
            throw LaserdotError.unavailable("cannot start \(exe): \(error.localizedDescription)")
        }
        child = p
        let deadline = Date().addingTimeInterval(10)
        while true {
            if let c = DaemonClient.connectLive(socketPath: socketPath) { return c }
            if !p.isRunning || Date() > deadline {
                usleep(100_000) // let the log drain
                logLock.lock()
                let tail = log.joined(separator: "\n")
                logLock.unlock()
                let why = p.isRunning ? "laserdotd did not start in time" : "laserdotd exited (\(p.terminationStatus))"
                throw LaserdotError.unavailable("\(why): \(tail)")
            }
            usleep(50_000)
        }
    }
}
