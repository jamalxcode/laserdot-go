import Foundation

/// Drives, index guidance, content search and duplicates (the engine's
/// calls beyond searching).
extension Engine {
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
    /// Look at every indexed folder again: deleted files leave the index,
    /// new and changed ones come in. Returns (added, removed).
    @discardableResult
    public func refresh() throws -> (added: UInt64, removed: UInt64) {
        let u = try call(.refresh, as: Wire.UpdateStats.self)
        return (u.added, u.removed)
    }

    /// What the indexed folders' last full index could not read.
    public func unreadable() throws -> Wire.Unreadable {
        try call(.unreadable, as: Wire.Unreadable.self)
    }

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
    /// How far the running duplicate scan has got (nil: none, or an older
    /// daemon). The scan has a connection of its own, so this answers.
    public func dupeProgress() -> Wire.DupeProgress? {
        (try? call(.status, as: Wire.Status.self))?.dupes
    }

    /// Stop the running duplicate scan early. findDuplicates then returns
    /// what it found so far; what it read is kept for the next scan.
    @discardableResult
    public func stopDuplicates() -> Bool {
        (try? call(.dupesStop, as: Bool.self)) ?? false
    }

    public func findDuplicates(scope: String, exactOnly: Bool) throws -> DuplicateReport {
        let request = DaemonRequest.dupes(options: Wire.DupeOptions(scope: scope, images: !exactOnly, videos: !exactOnly))
        // A scan can take minutes: use a connection of its own so searches
        // on the shared one are not held up.
        let r: Wire.DupeReport
        if supports(request.op), let own = DaemonClient.connectLive(socketPath: socketPath) {
            // No time limit: on big drives reading same-size files whole can
            // take far longer than ten minutes (the window shows progress).
            // If laserdotd stops, the connection closes and this returns.
            own.setTimeout(0)
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
            videoDecoder: r.videoDecoder, elapsedMs: r.ms, stopped: r.stopped)
    }

    /// Groups per cleanup request: tens of thousands of duplicates go in
    /// batches (each request stays small, and the window can show progress).
    public static let cleanupBatch = 500

    /// Dry run: what would move (`folder` = `nil`: to the Trash). `progress`
    /// gets files checked so far and in all.
    public func cleanupPreview(sets: [CleanupSet], folder: String?,
                               progress: ((Int, Int) -> Void)? = nil) throws -> CleanupPreview {
        var out = CleanupPreview(files: 0, bytes: 0, skipped: [])
        let total = sets.reduce(0) { $0 + $1.remove.count }
        var done = 0
        let line = longJobConnection()
        for start in stride(from: 0, to: sets.count, by: Self.cleanupBatch) {
            let batch = Array(sets[start..<min(sets.count, start + Self.cleanupBatch)])
            let p = try longCall(line, .cleanupPlan(request: cleanupRequest(batch, folder)), as: Wire.CleanupPlan.self)
            out.files += p.moves.count
            out.bytes += p.bytes
            out.skipped += p.skipped
            done += batch.reduce(0) { $0 + $1.remove.count }
            progress?(done, total)
        }
        return out
    }

    /// Move the files to the Trash (or `folder`), after the user confirmed.
    /// `progress` gets files handled so far and in all.
    public func cleanup(sets: [CleanupSet], folder: String?,
                        progress: ((Int, Int) -> Void)? = nil) throws -> CleanupOutcome {
        var out = CleanupOutcome(moved: 0, bytes: 0, skipped: [], failed: [])
        let total = sets.reduce(0) { $0 + $1.remove.count }
        var done = 0
        let line = longJobConnection()
        for start in stride(from: 0, to: sets.count, by: Self.cleanupBatch) {
            let batch = Array(sets[start..<min(sets.count, start + Self.cleanupBatch)])
            let r = try longCall(line, .cleanup(request: cleanupRequest(batch, folder)), as: Wire.CleanupResult.self)
            out.moved += r.moved.count
            out.bytes += r.bytes
            out.skipped += r.skipped
            out.failed += r.failed
            out.movedPaths += r.moved.map(\.from)
            done += batch.reduce(0) { $0 + $1.remove.count }
            progress?(done, total)
        }
        return out
    }

    /// A connection of its own for a long job, with no time limit (nil: use
    /// the shared one). Searches on the shared connection go on meanwhile.
    func longJobConnection() -> DaemonClient? {
        guard let c = DaemonClient.connectLive(socketPath: socketPath) else { return nil }
        c.setTimeout(0)
        return c
    }

    /// One call on a long job's connection (or the shared one). It is never
    /// sent twice: a broken connection is an error, not a retry.
    func longCall<T: Decodable>(_ own: DaemonClient?, _ request: DaemonRequest, as type: T.Type) throws -> T {
        guard let own else { return try call(request, as: type) }
        do {
            return try own.call(request, as: type)
        } catch DaemonClientError.remote(let e) {
            let err = LaserdotError(e)
            if case .unsupported = err { markUnsupported(request.op) }
            throw err
        } catch {
            throw LaserdotError.unavailable("\(error)")
        }
    }

    private func cleanupRequest(_ sets: [CleanupSet], _ folder: String?) -> Wire.CleanupRequest {
        let item = { (f: DuplicateFile) in Wire.CleanupItem(path: f.path, size: f.size, modified: f.modified) }
        return Wire.CleanupRequest(
            groups: sets.map { Wire.CleanupGroup(keep: $0.keep.map(item), remove: $0.remove.map(item)) },
            dest: folder.map { Wire.CleanupDest.folder($0) } ?? Wire.CleanupDest.trash)
    }
}
