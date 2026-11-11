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

// MARK: records for the UI

public struct Hit: Hashable, Sendable {
    public var path: String
    public var name: String
    public var kind: String
    public var isDir: Bool
    public var size: UInt64
    /// Unix seconds.
    public var modified: Int64
    /// Ready-to-show badges (spec §10.3): dimensions, video codec + HDR,
    /// first audio track, duration.
    public var badges: [String]
    public var typo: Bool
    /// The drive this result is on; `nil` for this Mac's index.
    public var volume: String?
    /// Found by its contents, not its name (spec §2.4.2).
    public var content: Bool

    init(_ h: Wire.Hit) {
        var badges: [String] = []
        if let w = h.width, let hh = h.height { badges.append("\(w)×\(hh)") }
        if let m = h.media {
            if let v = m.videoCodec { badges.append(([v] + m.hdr).joined(separator: " ")) }
            if let a = m.audio.first { badges.append(m.audio.count > 1 ? "\(a) +\(m.audio.count - 1)" : a) }
            if let d = m.durationMs, d >= 1000 {
                let s = d / 1000
                badges.append(
                    s >= 3600
                        ? String(format: "%llu:%02llu:%02llu", s / 3600, s / 60 % 60, s % 60)
                        : String(format: "%llu:%02llu", s / 60, s % 60))
            }
        }
        path = h.path
        name = h.name
        kind = h.kind
        isDir = h.isDir
        size = h.size
        modified = h.modified
        self.badges = badges
        typo = h.typo
        volume = h.volume
        content = h.content
    }
}

public struct Results: Sendable {
    public var total: UInt64
    public var hits: [Hit]
    public var elapsedMs: Double
    public var typoFallback: Bool
    /// Non-fatal notes, e.g. "`rez:` is not a filter".
    public var notes: [String]
}

public struct DaemonStatus: Sendable {
    public var running: Bool
    public var version: String
    public var protocolVersion: Int
    /// The daemon runs with network access denied by the OS (spec §7.1).
    public var networkDenied: Bool
    public var watcher: String
    public var entries: UInt64
    public var folders: [String]
    /// A folder being indexed now, if the daemon reports it.
    public var indexing: Wire.IndexProgress? = nil

    static let down = DaemonStatus(
        running: false, version: "", protocolVersion: 0, networkDenied: false, watcher: "", entries: 0, folders: [])
}

/// A zero-results suggestion (spec §8.3).
public struct CoachingInfo: Hashable, Sendable {
    /// `index_folder`, `fix_value`, `relax` or `show_syntax`.
    public var kind: String
    /// The query to run instead (fix_value, relax).
    public var query: String?
    /// What was removed (relax) or replaced (fix_value).
    public var from: String?
    /// The replacement value (fix_value).
    public var to: String?
    /// The folder to index (index_folder).
    public var path: String?
    /// Results the new query finds.
    public var count: UInt64
}

/// A live count for the cheat sheet (spec §8.9.5).
public struct ValueCountInfo: Hashable, Sendable {
    public var key: String
    public var value: String
    public var count: UInt64
}

/// Content search state for the menu (spec §2.4).
public struct ContentInfo: Sendable {
    public var folders: [String]
    public var documents: UInt64
    public var skipped: UInt64
    public var pending: UInt64
    public var maxBytes: UInt64
    public var skipCode: Bool = false
    /// content.db's size on disk.
    public var bytes: UInt64 = 0
}

/// One file in a duplicate group (spec §4).
public struct DuplicateFile: Hashable, Sendable {
    public var path: String
    public var size: UInt64
    /// Unix seconds.
    public var modified: Int64
    /// The drive it is on; `nil` for this Mac.
    public var volume: String?
    /// `3840×2160 · HDR · 0.12 bpp · 5.1`
    public var summary: String
    /// The recommended copy to keep.
    public var keep: Bool
}

public struct DuplicateGroup: Sendable {
    /// `exact`, `similar_image` or `reencoded_video`.
    public var kind: String
    /// Best first.
    public var files: [DuplicateFile]
    public var reclaimable: UInt64
}

public struct DuplicateReport: Sendable {
    public var groups: [DuplicateGroup]
    public var reclaimable: UInt64
    public var filesChecked: UInt64
    public var filesRead: UInt64
    public var unreadable: UInt64
    /// `nil`: re-encoded videos could not be compared here.
    public var videoDecoder: String?
    public var elapsedMs: UInt64
}

/// A group as the user decided it: what stays, what goes.
public struct CleanupSet: Sendable {
    public var keep: [DuplicateFile]
    public var remove: [DuplicateFile]

    public init(keep: [DuplicateFile], remove: [DuplicateFile]) {
        self.keep = keep
        self.remove = remove
    }
}

/// The dry run shown before anything moves (spec §4.4).
public struct CleanupPreview: Sendable {
    public var files: Int
    public var bytes: UInt64
    public var skipped: [String]
}

public struct CleanupOutcome: Sendable {
    public var moved: Int
    public var bytes: UInt64
    public var skipped: [String]
    public var failed: [String]
}

/// A mounted drive for the Drives menu (spec §9.6).
public struct Drive: Hashable, Sendable {
    /// The drive's identity (its index's volume id), once it has an index.
    public var id: String?
    public var name: String
    public var mount: String
    /// `unindexed`, `attached`, `detached`, `locked` or `unreadable`.
    public var state: String
    /// Why the index is unreadable, when it is.
    public var detail: String?
    public var readOnly: Bool
    public var encrypted: Bool
    /// The index is on the drive (false: kept on this Mac).
    public var onDrive: Bool
    public var complete: Bool
    public var entries: UInt64
    public var totalBytes: UInt64
    public var freeBytes: UInt64
    public var indexBytes: UInt64
}

/// What indexing a drive will take, for the prompt (spec §9.3).
public struct DrivePlan: Sendable {
    public var name: String
    public var files: UInt64
    public var folders: UInt64
    public var bytes: UInt64
    public var indexEstimateBytes: UInt64
    public var location: String
    public var onDrive: Bool
}

// MARK: the engine

/// The app's handle on laserdotd (spec §13.2): the app never opens the index
/// itself. Calls are serialized on one shared connection, which is reopened
/// once if it broke; if nothing answers, the bundled daemon is started.
/// Blocking — call it off the main thread.
public final class Engine: @unchecked Sendable {
    public let dbPath: String
    public let socketPath: String
    private let daemonPath: String?
    private let lock = NSLock()
    private var client: DaemonClient?
    private var child: Process?
    private var log: [String] = []
    private let logLock = NSLock()
    /// Ops this daemon answered `unsupported`; forgotten on reconnect.
    private var unsupported = Set<String>()
    /// Set by `retire()`: no more requests, and no daemon is started again.
    private var retired = false
    /// Guidance runs on a connection of its own, so typing never waits for
    /// an index or a scan on the shared one.
    private let guideLock = NSLock()
    private var guideClient: DaemonClient?
    private var guideUnsupported = Set<String>()

    /// The version the bundled daemon reports; a running daemon of another
    /// version (left over from before an update, or after a force quit) is
    /// stopped and the bundled one started instead.
    private let expectedVersion: String?
    private let versionLock = NSLock()
    private var versionChecked = false

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
    private func replaceStaleDaemon() {
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
            watcher: s.watcher, entries: s.entries, folders: roots.map(\.path), indexing: s.indexing)
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
        let s: Wire.IndexStats
        if let own = DaemonClient.connectLive(socketPath: socketPath) {
            own.setTimeout(3600) // a large home folder can take a while
            do {
                s = try own.call(.index(path: path), as: Wire.IndexStats.self)
            } catch DaemonClientError.remote(let e) {
                throw LaserdotError(e)
            } catch {
                throw LaserdotError.unavailable("\(error)")
            }
        } else {
            s = try call(.index(path: path), as: Wire.IndexStats.self)
        }
        return s.incremental
            ? "\(s.added) added, \(s.removed) removed, \(s.renamed) renamed"
            : "\(s.files) files, \(s.folders) folders"
    }

    @discardableResult
    public func forget(path: String) throws -> Bool {
        try call(.forget(path: path), as: Bool.self)
    }

    // MARK: drives (spec §9)

    public func drives() throws -> [Drive] {
        try call(.drives, as: [Wire.DriveInfo].self).map { d in
            Drive(
                id: d.id, name: d.name, mount: d.mount, state: d.state.state, detail: d.state.detail, readOnly: d.readOnly,
                encrypted: d.encrypted, onDrive: d.onDrive, complete: d.complete, entries: d.entries,
                totalBytes: d.totalBytes, freeBytes: d.freeBytes, indexBytes: d.indexBytes)
        }
    }

    public func drivePlan(mount: String) throws -> DrivePlan {
        let p = try call(.drivePlan(mount: mount), as: Wire.DrivePlan.self)
        return DrivePlan(
            name: p.name, files: p.files, folders: p.folders, bytes: p.bytes,
            indexEstimateBytes: p.indexEstimateBytes, location: p.location, onDrive: p.onDrive)
    }

    /// Index a drive; a passphrase encrypts the index. Returns a summary.
    @discardableResult
    public func driveIndex(mount: String, passphrase: String?) throws -> String {
        let s = try call(.driveIndex(mount: mount, passphrase: passphrase), as: Wire.IndexStats.self)
        return "\(s.files) files, \(s.folders) folders"
    }

    /// Attach an encrypted drive index with its passphrase.
    public func driveUnlock(mount: String, passphrase: String) throws {
        _ = try call(.driveAttach(mount: mount, passphrase: passphrase), as: Wire.DriveInfo.self)
    }

    /// Search an indexed drive again (after "Stop Searching").
    public func driveAttach(mount: String) throws {
        _ = try call(.driveAttach(mount: mount, passphrase: nil), as: Wire.DriveInfo.self)
    }

    @discardableResult
    public func driveDetach(mount: String) throws -> Bool {
        try call(.driveDetach(mount: mount), as: Bool.self)
    }

    @discardableResult
    public func driveRebuild(mount: String, passphrase: String?) throws -> String {
        let s = try call(.driveRebuild(mount: mount, passphrase: passphrase), as: Wire.IndexStats.self)
        return "\(s.files) files, \(s.folders) folders"
    }

    @discardableResult
    public func driveDelete(mount: String) throws -> Bool {
        try call(.driveDelete(mount: mount), as: Bool.self)
    }

    public func driveEject(mount: String) throws {
        _ = try call(.driveEject(mount: mount), as: String.self)
    }

    // MARK: guidance from the index (spec §8.3, §8.9.5)

    /// Suggestions for a query that found nothing.
    public func coach(query: String) throws -> [CoachingInfo] {
        try call(.coach(query: query), as: [Wire.Coaching].self).map { c in
            switch c.kind {
            case "relax":
                return CoachingInfo(kind: c.kind, query: c.query, from: c.removed, to: nil, path: nil, count: c.count)
            case "fix_value":
                return CoachingInfo(kind: c.kind, query: c.query, from: c.from, to: c.to, path: nil, count: c.count)
            case "index_folder":
                return CoachingInfo(kind: c.kind, query: nil, from: nil, to: nil, path: c.path, count: 0)
            default:
                return CoachingInfo(kind: c.kind, query: nil, from: nil, to: nil, path: nil, count: 0)
            }
        }
    }

    /// Live counts for every enumerated filter value.
    public func valueCounts() throws -> [ValueCountInfo] {
        try call(.valueCounts, as: [Wire.ValueCount].self).map {
            ValueCountInfo(key: $0.key, value: $0.value, count: $0.count)
        }
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
    private func guide<T: Decodable>(_ request: DaemonRequest, as type: T.Type) -> T?? {
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

    // MARK: content search (spec §2.4)

    public func contentInfo() throws -> ContentInfo {
        info(try call(.contentStatus, as: Wire.ContentStatus.self))
    }

    /// Leave source code out of content search (or put it back).
    public func setContentSkipCode(_ skip: Bool) throws -> ContentInfo {
        var settings = try call(.contentStatus, as: Wire.ContentStatus.self).settings ?? .defaultForHome
        settings.skipCode = skip
        _ = try call(.setContentSettings(settings: settings), as: Wire.ContentStatus.self)
        return try contentInfo()
    }

    /// What fills a folder ("" = the indexed folders), largest first.
    /// Words from inside a document around the query's words ("" when none).
    public func snippet(path: String, query: String) throws -> String {
        try call(.snippet(path: path, query: query), as: String.self)
    }

    public func storage(path: String, limit: Int = 60) throws -> Wire.StorageReport {
        try call(.storage(path: path, limit: limit), as: Wire.StorageReport.self)
    }

    /// Pause background work (contents, Tier 2) or resume it.
    /// Returns whether background work is now paused (nil: no answer).
    @discardableResult
    public func setBackground(paused: Bool) -> Bool? {
        (try? call(.background(paused: paused), as: [String: Bool].self))?["paused"]
    }

    /// Read the text of new and changed documents now (it also happens in
    /// the background on its own).
    public func updateContent() throws {
        _ = try call(.updateContent, as: Discard.self)
    }

    private struct Discard: Decodable {
        init(from decoder: Decoder) throws {}
    }

    /// Index the text of files in `folder` too (or stop, with `add` false).
    public func setContentFolder(folder: String, add: Bool) throws -> ContentInfo {
        var settings = try call(.contentStatus, as: Wire.ContentStatus.self).settings ?? .defaultForHome
        settings.roots.removeAll { $0 == folder }
        if add { settings.roots.append(folder) }
        _ = try call(.setContentSettings(settings: settings), as: Wire.ContentStatus.self)
        return try contentInfo()
    }

    private func info(_ st: Wire.ContentStatus) -> ContentInfo {
        let settings = st.settings ?? .defaultForHome
        return ContentInfo(
            folders: settings.roots, documents: st.documents, skipped: st.skipped, pending: st.pending,
            maxBytes: settings.maxBytes, skipCode: settings.skipCode, bytes: st.bytes)
    }

    // MARK: duplicates (spec §4)

    /// Find duplicates among files matching `scope` (empty: everything).
    public func findDuplicates(scope: String, exactOnly: Bool) throws -> DuplicateReport {
        let request = DaemonRequest.dupes(options: Wire.DupeOptions(scope: scope, images: !exactOnly, videos: !exactOnly))
        // A scan can take minutes: use a connection of its own so searches
        // on the shared one are not held up.
        let r: Wire.DupeReport
        if supports(request.op), let own = DaemonClient.connectLive(socketPath: socketPath) {
            do {
                r = try own.call(request, as: Wire.DupeReport.self)
            } catch DaemonClientError.remote(let e) {
                let err = LaserdotError(e)
                if case .unsupported = err { markUnsupported(request.op) }
                throw err
            } catch {
                throw LaserdotError.unavailable("\(error)")
            }
        } else {
            r = try call(request, as: Wire.DupeReport.self)
        }
        return DuplicateReport(
            groups: r.groups.map { g in
                DuplicateGroup(
                    kind: g.kind,
                    files: g.members.map {
                        DuplicateFile(
                            path: $0.path, size: $0.size, modified: $0.modified, volume: $0.volume,
                            summary: $0.summary, keep: $0.keep)
                    },
                    reclaimable: g.reclaimable)
            },
            reclaimable: r.reclaimable, filesChecked: r.files, filesRead: r.read, unreadable: r.unreadable,
            videoDecoder: r.videoDecoder, elapsedMs: r.ms)
    }

    /// Dry run: what would move (`folder` = `nil`: to the Trash).
    public func cleanupPreview(sets: [CleanupSet], folder: String?) throws -> CleanupPreview {
        let p = try call(.cleanupPlan(request: cleanupRequest(sets, folder)), as: Wire.CleanupPlan.self)
        return CleanupPreview(files: p.moves.count, bytes: p.bytes, skipped: p.skipped)
    }

    /// Move the files to the Trash (or `folder`), after the user confirmed.
    public func cleanup(sets: [CleanupSet], folder: String?) throws -> CleanupOutcome {
        let r = try call(.cleanup(request: cleanupRequest(sets, folder)), as: Wire.CleanupResult.self)
        return CleanupOutcome(moved: r.moved.count, bytes: r.bytes, skipped: r.skipped, failed: r.failed)
    }

    private func cleanupRequest(_ sets: [CleanupSet], _ folder: String?) -> Wire.CleanupRequest {
        let item = { (f: DuplicateFile) in Wire.CleanupItem(path: f.path, size: f.size, modified: f.modified) }
        return Wire.CleanupRequest(
            groups: sets.map { Wire.CleanupGroup(keep: $0.keep.map(item), remove: $0.remove.map(item)) },
            dest: folder.map { Wire.CleanupDest.folder($0) } ?? Wire.CleanupDest.trash)
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
    private func remote(_ e: RemoteError, _ op: String) -> LaserdotError {
        let err = LaserdotError(e)
        if case .unsupported = err { unsupported.insert(op) }
        return err
    }

    private func markUnsupported(_ op: String) {
        lock.lock()
        unsupported.insert(op)
        lock.unlock()
    }

    /// Start the bundled daemon and wait for it to answer. Called with
    /// `lock` held.
    private func startDaemon() throws -> DaemonClient {
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
