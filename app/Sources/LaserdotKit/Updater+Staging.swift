import Foundation

extension Updater {
    // MARK: staging (so installing on quit is instant)

    /// Do the slow part of an install ahead of time: sign the candidate,
    /// keep the running version aside for going back, and copy the update
    /// next to where it goes (a hidden folder in the same place, so the
    /// final swap is a rename). `finishStaged` then only swaps it in.
    public func stage(_ c: UpdateCandidate, to target: URL? = nil) throws -> StagedUpdate {
        defer { try? FileManager.default.removeItem(at: c.workDir) }
        let dest = target ?? installTarget
        if let signer {
            do {
                try signer.sign(c.app)
                lastSigningError = nil
            } catch {
                lastSigningError = (error as? LocalizedError)?.errorDescription ?? "\(error)"
            }
        }
        discardStaged(in: dest.deletingLastPathComponent())
        keepPrevious(dest)
        let staged = dest.deletingLastPathComponent()
            .appendingPathComponent("\(Self.stagedPrefix)\(c.version.short)-\(UUID().uuidString).app")
        do {
            try Self.run("/usr/bin/ditto", [c.app.path, staged.path])
        } catch {
            try? FileManager.default.removeItem(at: staged)
            throw UpdateError.installFailed((error as? LocalizedError)?.errorDescription ?? "\(error)")
        }
        _ = try? Self.run("/usr/bin/xattr", ["-dr", "com.apple.quarantine", staged.path])
        return StagedUpdate(app: staged, version: c.version, target: dest)
    }

    /// Swap a staged update in (a rename: instant) and return where it is.
    @discardableResult
    public func finishStaged(_ s: StagedUpdate) throws -> URL {
        let fm = FileManager.default
        guard let v = AppVersion(bundle: s.app), currentVersion.map({ $0 < v }) ?? true else {
            try? fm.removeItem(at: s.app)
            throw UpdateError.notAnApp
        }
        do {
            if fm.fileExists(atPath: s.target.path) {
                _ = try fm.replaceItemAt(s.target, withItemAt: s.app, backupItemName: nil, options: [])
            } else {
                try fm.moveItem(at: s.app, to: s.target)
            }
        } catch {
            throw UpdateError.installFailed(error.localizedDescription)
        }
        return s.target
    }

    /// The staged update left in `dir` by an earlier run, if it is still
    /// newer than the running app (older ones are removed).
    public func stagedUpdate(in dir: URL? = nil) -> StagedUpdate? {
        let dest = installTarget
        let folder = dir ?? dest.deletingLastPathComponent()
        let names = (try? FileManager.default.contentsOfDirectory(atPath: folder.path)) ?? []
        var best: StagedUpdate?
        for n in names where n.hasPrefix(Self.stagedPrefix) && n.hasSuffix(".app") {
            let app = folder.appendingPathComponent(n)
            guard let v = AppVersion(bundle: app), currentVersion.map({ $0 < v }) ?? true else {
                try? FileManager.default.removeItem(at: app)
                continue
            }
            if let b = best, !(b.version < v) {
                try? FileManager.default.removeItem(at: app)
                continue
            }
            if let b = best { try? FileManager.default.removeItem(at: b.app) }
            best = StagedUpdate(app: app, version: v, target: folder.appendingPathComponent(dest.lastPathComponent))
        }
        return best
    }

    /// Remove staged updates from `dir`.
    public func discardStaged(in dir: URL? = nil) {
        let folder = dir ?? installTarget.deletingLastPathComponent()
        for n in (try? FileManager.default.contentsOfDirectory(atPath: folder.path)) ?? [] where n.hasPrefix(Self.stagedPrefix) {
            try? FileManager.default.removeItem(at: folder.appendingPathComponent(n))
        }
    }

    static let stagedPrefix = ".Laserdot-staged-"
}
