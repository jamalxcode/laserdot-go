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
}
