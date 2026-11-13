import Foundation
import Security

/// Updating Laserdot (spec §7.4) without the app ever touching the network
/// (pillar 1). The browser downloads a release (`Laserdot-<version>.dmg`
/// from the Releases page); the app finds it in Downloads (or the user picks
/// the file), unpacks it locally, checks it and replaces itself.
///
/// Checks, all local:
/// - it is a Laserdot bundle (same bundle identifier) carrying `laserdotd`;
/// - its version is newer than the running one;
/// - when the running app is signed with a Developer ID: the update is
///   validly signed (nested code included) by the same team and Gatekeeper
///   accepts it (notarized, ticket stapled). An unsigned build accepts
///   unsigned updates and says so.
public enum UpdateError: Error, LocalizedError, Equatable {
    case unsupportedFile(String)
    case extractFailed(String)
    case notAnApp
    case wrongApp(String)
    case missingDaemon
    case notNewer(current: String, candidate: String)
    case badSignature(String)
    case differentDeveloper(expected: String, found: String)
    case notNotarized(String)
    case installFailed(String)

    public var errorDescription: String? {
        switch self {
        case .unsupportedFile(let f): return "\(f) is not a .dmg, .zip or .app"
        case .extractFailed(let m): return "could not open the update: \(m)"
        case .notAnApp: return "the file holds no Laserdot app"
        case .wrongApp(let id): return "the file holds another app (\(id)), not Laserdot"
        case .missingDaemon: return "the update is incomplete (no laserdotd inside)"
        case .notNewer(let c, let n): return "the update (\(n)) is not newer than this version (\(c))"
        case .badSignature(let m): return "the update's signature is not valid: \(m)"
        case .differentDeveloper(let e, let f): return "the update is signed by another developer (\(f), expected \(e))"
        case .notNotarized(let m): return "macOS does not accept the update (not notarized): \(m)"
        case .installFailed(let m): return "could not install the update: \(m)"
        }
    }
}

/// CFBundleShortVersionString + CFBundleVersion, compared numerically part by
/// part ("0.10.0" > "0.9.3"; then the build number).
public struct AppVersion: Comparable, CustomStringConvertible, Sendable {
    public let short: String
    public let build: String

    public init(short: String, build: String) {
        self.short = short
        self.build = build
    }

    /// The version in a bundle's Info.plist.
    public init?(bundle url: URL) {
        guard let info = NSDictionary(contentsOf: url.appendingPathComponent("Contents/Info.plist")),
              let short = info["CFBundleShortVersionString"] as? String else { return nil }
        self.init(short: short, build: (info["CFBundleVersion"] as? String) ?? "0")
    }

    private static func parts(_ s: String) -> [Int] {
        s.split(whereSeparator: { !$0.isNumber }).map { Int($0) ?? 0 }
    }

    private static func compare(_ x: [Int], _ y: [Int]) -> Int {
        for i in 0..<max(x.count, y.count) {
            let (p, q) = (i < x.count ? x[i] : 0, i < y.count ? y[i] : 0)
            if p != q { return p < q ? -1 : 1 }
        }
        return 0
    }

    public static func < (a: AppVersion, b: AppVersion) -> Bool {
        let c = compare(parts(a.short), parts(b.short))
        if c != 0 { return c < 0 }
        return compare(parts(a.build), parts(b.build)) < 0
    }

    public static func == (a: AppVersion, b: AppVersion) -> Bool { !(a < b) && !(b < a) }

    public var description: String { "\(short) (\(build))" }
}

/// An unpacked, checked update ready to install.
public struct UpdateCandidate {
    public let app: URL
    public let version: AppVersion
    /// What is new in this version (Contents/Resources/WhatsNew.txt), if any.
    public var whatsNew: String? { Updater.whatsNew(in: app) }
    /// The file it came from.
    public let source: URL
    /// The running app is not signed with a Developer ID, so signatures were
    /// not required.
    public let unsigned: Bool
    let workDir: URL
}

public final class Updater {
    /// Where releases are published; opened in the user's browser (the app
    /// itself never connects anywhere).
    public static let releasesPage = URL(string: "https://github.com/jamalxcode/laserdot-go/releases/latest")!

    /// The running app's bundle.
    public let current: URL
    public let bundleID: String
    public let currentVersion: AppVersion?

    /// Where the version before the last update is kept, for "Go back".
    public let previousDir: URL

    public static var defaultPreviousDir: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/Laserdot Previous Version")
    }

    /// Signs each update with this Mac's own key before it is installed, so
    /// macOS keeps Laserdot's permissions across updates (nil: leave the
    /// release's ad hoc signature).
    public let signer: LocalSigner?
    /// Why the last update could not be signed (it was installed anyway).
    public private(set) var lastSigningError: String?

    public init(current: URL = Bundle.main.bundleURL, previousDir: URL = Updater.defaultPreviousDir,
                signer: LocalSigner? = LocalSigner()) {
        self.signer = signer
        self.previousDir = previousDir
        self.current = current.standardizedFileURL
        let info = NSDictionary(contentsOf: current.appendingPathComponent("Contents/Info.plist"))
        self.bundleID = (info?["CFBundleIdentifier"] as? String) ?? "app.laserdot.Laserdot"
        self.currentVersion = AppVersion(bundle: current)
    }

    // MARK: finding downloads

    /// Laserdot downloads in `folder` (default ~/Downloads), newest first:
    /// Laserdot*.dmg, Laserdot*.zip.
    public static func downloads(in folder: URL? = nil) -> [URL] {
        let fm = FileManager.default
        let dir = folder ?? fm.homeDirectoryForCurrentUser.appendingPathComponent("Downloads")
        let items = (try? fm.contentsOfDirectory(at: dir, includingPropertiesForKeys: [.contentModificationDateKey])) ?? []
        let found = items.filter {
            $0.lastPathComponent.lowercased().hasPrefix("laserdot") && ["dmg", "zip"].contains($0.pathExtension.lowercased())
        }
        func date(_ u: URL) -> Date {
            (try? u.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate) ?? .distantPast
        }
        return found.sorted { date($0) > date($1) }
    }

    /// The newest download that is a valid, newer Laserdot; nil when there is
    /// none. Candidates that fail are skipped (an older or broken download).
    public func newestUpdate(in folder: URL? = nil) -> UpdateCandidate? {
        for file in Self.downloads(in: folder) {
            if let c = try? prepare(file: file) { return c }
        }
        return nil
    }

    // MARK: unpack and check

    /// Unpack `file` (.dmg, .zip or .app) into a private folder, find the
    /// app and check it. Nothing is installed yet.
    public func prepare(file: URL) throws -> UpdateCandidate {
        let fm = FileManager.default
        let work = fm.temporaryDirectory.appendingPathComponent("laserdot-update-\(UUID().uuidString)")
        try fm.createDirectory(at: work, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        do {
            let app = try unpack(file, into: work)
            return try check(app, source: file, workDir: work)
        } catch {
            try? fm.removeItem(at: work)
            throw error
        }
    }

    /// Remove an update that will not be installed.
    public func discard(_ c: UpdateCandidate) {
        try? FileManager.default.removeItem(at: c.workDir)
    }

    private func unpack(_ file: URL, into work: URL) throws -> URL {
        let out = work.appendingPathComponent("unpacked")
        switch file.pathExtension.lowercased() {
        case "zip":
            try Self.run("/usr/bin/ditto", ["-x", "-k", file.path, out.path])
            // A CI artifact is a zip holding the release zip: unpack one more.
            if Self.findApp(in: out) == nil,
               let inner = (try? FileManager.default.contentsOfDirectory(at: out, includingPropertiesForKeys: nil))?
                .first(where: { $0.pathExtension.lowercased() == "zip" }) {
                try Self.run("/usr/bin/ditto", ["-x", "-k", inner.path, out.path])
            }
        case "dmg":
            let mount = work.appendingPathComponent("mount")
            try FileManager.default.createDirectory(at: mount, withIntermediateDirectories: true)
            // hdiutil sometimes answers "Resource temporarily unavailable":
            // try again a few times before giving up.
            var attempt = 0
            while true {
                do {
                    try Self.run("/usr/bin/hdiutil", ["attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mount.path, file.path])
                    break
                } catch {
                    attempt += 1
                    if attempt >= 5 { throw error }
                    Thread.sleep(forTimeInterval: Double(attempt))
                }
            }
            defer { _ = try? Self.run("/usr/bin/hdiutil", ["detach", "-force", mount.path]) }
            guard let app = Self.findApp(in: mount) else { throw UpdateError.notAnApp }
            try Self.run("/usr/bin/ditto", [app.path, out.appendingPathComponent(app.lastPathComponent).path])
        case "app":
            try Self.run("/usr/bin/ditto", [file.path, out.appendingPathComponent(file.lastPathComponent).path])
        default:
            throw UpdateError.unsupportedFile(file.lastPathComponent)
        }
        guard let app = Self.findApp(in: out) else { throw UpdateError.notAnApp }
        return app
    }

    /// The first .app within two levels.
    static func findApp(in dir: URL) -> URL? {
        let fm = FileManager.default
        let level = (try? fm.contentsOfDirectory(at: dir, includingPropertiesForKeys: nil)) ?? []
        if let app = level.first(where: { $0.pathExtension == "app" }) { return app }
        for sub in level where (try? sub.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true {
            let inner = (try? fm.contentsOfDirectory(at: sub, includingPropertiesForKeys: nil)) ?? []
            if let app = inner.first(where: { $0.pathExtension == "app" }) { return app }
        }
        return nil
    }

    func check(_ app: URL, source: URL, workDir: URL) throws -> UpdateCandidate {
        let info = NSDictionary(contentsOf: app.appendingPathComponent("Contents/Info.plist"))
        guard let id = info?["CFBundleIdentifier"] as? String else { throw UpdateError.notAnApp }
        guard id == bundleID else { throw UpdateError.wrongApp(id) }
        guard FileManager.default.isExecutableFile(atPath: app.appendingPathComponent("Contents/MacOS/laserdotd").path) else {
            throw UpdateError.missingDaemon
        }
        guard let version = AppVersion(bundle: app) else { throw UpdateError.notAnApp }
        if let cur = currentVersion, !(cur < version) {
            throw UpdateError.notNewer(current: cur.description, candidate: version.description)
        }
        let team = Self.teamIdentifier(of: current)
        if let team {
            try Self.verifySignature(app, bundleID: bundleID, team: team)
            try Self.assessGatekeeper(app)
        }
        return UpdateCandidate(app: app, version: version, source: source, unsigned: team == nil, workDir: workDir)
    }

    /// The Developer ID team that signed a bundle; nil when unsigned or
    /// ad-hoc signed.
    public static func teamIdentifier(of bundle: URL) -> String? {
        var code: SecStaticCode?
        guard SecStaticCodeCreateWithPath(bundle as CFURL, [], &code) == errSecSuccess, let code else { return nil }
        var info: CFDictionary?
        guard SecCodeCopySigningInformation(code, SecCSFlags(rawValue: kSecCSSigningInformation), &info) == errSecSuccess,
              let dict = info as? [String: Any] else { return nil }
        return dict[kSecCodeInfoTeamIdentifier as String] as? String
    }

    static func verifySignature(_ app: URL, bundleID: String, team: String) throws {
        var code: SecStaticCode?
        guard SecStaticCodeCreateWithPath(app as CFURL, [], &code) == errSecSuccess, let code else {
            throw UpdateError.badSignature("unreadable")
        }
        let text = "anchor apple generic and identifier \"\(bundleID)\" and certificate leaf[subject.OU] = \"\(team)\""
        var req: SecRequirement?
        guard SecRequirementCreateWithString(text as CFString, [], &req) == errSecSuccess else {
            throw UpdateError.badSignature("bad requirement")
        }
        let flags = SecCSFlags(rawValue: kSecCSCheckAllArchitectures | kSecCSCheckNestedCode | kSecCSStrictValidate)
        var err: Unmanaged<CFError>?
        if SecStaticCodeCheckValidityWithErrors(code, flags, req, &err) != errSecSuccess {
            let found = teamIdentifier(of: app) ?? "unsigned"
            if found != team { throw UpdateError.differentDeveloper(expected: team, found: found) }
            let msg = err.map { CFErrorCopyDescription($0.takeRetainedValue()) as String } ?? "invalid"
            throw UpdateError.badSignature(msg)
        }
    }

    static func assessGatekeeper(_ app: URL) throws {
        do {
            try run("/usr/sbin/spctl", ["--assess", "--type", "execute", app.path])
        } catch let UpdateError.extractFailed(m) {
            throw UpdateError.notNotarized(m)
        }
    }

    // MARK: install

    /// Where an install goes: over the running app when it can be replaced,
    /// else /Applications/Laserdot.app (the running copy is on a disk image,
    /// or macOS runs it from a read-only "translocated" copy because it was
    /// opened straight from Downloads).
    public var installTarget: URL {
        let path = current.path
        let dir = current.deletingLastPathComponent().path
        let readOnly = (try? current.resourceValues(forKeys: [.volumeIsReadOnlyKey]).volumeIsReadOnly) == true
        if path.contains("/AppTranslocation/") || readOnly || !FileManager.default.isWritableFile(atPath: dir) {
            return URL(fileURLWithPath: "/Applications/Laserdot.app")
        }
        return current
    }

    /// The running copy is not in an Applications folder.
    public var isOutsideApplications: Bool {
        let p = current.path
        return !(p.hasPrefix("/Applications/") || p.contains("/Applications/Laserdot.app"))
    }

    /// Install the candidate, replacing the app at the install target
    /// atomically, and return where it now lives. Stop the daemon first so
    /// the new app starts its own `laserdotd`.
    @discardableResult
    public func install(_ c: UpdateCandidate, to target: URL? = nil,
                        step: (InstallStep) -> Void = { _ in }) throws -> URL {
        defer { try? FileManager.default.removeItem(at: c.workDir) }
        let dest = target ?? installTarget
        if let signer {
            step(.signing)
            // Best effort: an update is never held back because it could not
            // be signed (macOS would then only ask its questions again).
            do {
                try signer.sign(c.app)
                lastSigningError = nil
            } catch {
                lastSigningError = (error as? LocalizedError)?.errorDescription ?? "\(error)"
            }
        }
        step(.placing)
        keepPrevious(dest)
        return try Self.place(c.app, at: dest)
    }

    /// What install is doing (for the update window).
    public enum InstallStep: Sendable { case signing, placing }

    // MARK: going back

    private var previousApp: URL { previousDir.appendingPathComponent("Laserdot.app") }

    /// Copy the app about to be replaced aside (best effort: an update never
    /// fails because the copy could not be made).
    func keepPrevious(_ app: URL) {
        let fm = FileManager.default
        guard fm.fileExists(atPath: app.path), AppVersion(bundle: app) != nil else { return }
        try? fm.createDirectory(at: previousDir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let staged = previousDir.appendingPathComponent(".staged-\(UUID().uuidString).app")
        guard (try? Self.run("/usr/bin/ditto", [app.path, staged.path])) != nil else {
            try? fm.removeItem(at: staged)
            return
        }
        try? fm.removeItem(at: previousApp)
        if (try? fm.moveItem(at: staged, to: previousApp)) == nil { try? fm.removeItem(at: staged) }
    }

    /// The kept version, when it is older than the running one.
    public var previousVersion: AppVersion? {
        guard let v = AppVersion(bundle: previousApp) else { return nil }
        if let cur = currentVersion, !(v < cur) { return nil }
        return v
    }

    /// Put the kept version back where the app is, and return it. The
    /// version being left becomes the kept one (so going back can be undone
    /// by updating again).
    @discardableResult
    public func goBack(to target: URL? = nil) throws -> URL {
        guard previousVersion != nil else { throw UpdateError.notAnApp }
        let fm = FileManager.default
        let dest = target ?? installTarget
        let old = previousDir.appendingPathComponent(".going-back-\(UUID().uuidString).app")
        try fm.moveItem(at: previousApp, to: old)
        defer { try? fm.removeItem(at: old) }
        keepPrevious(dest)
        return try Self.place(old, at: dest)
    }

    /// Copy the running app into /Applications (first launch from a disk
    /// image or from Downloads) and return the copy.
    @discardableResult
    public func moveToApplications(_ applications: URL = URL(fileURLWithPath: "/Applications")) throws -> URL {
        try Self.place(current, at: applications.appendingPathComponent("Laserdot.app"))
    }

    /// Put `app` at `target`: staged beside it, then swapped in atomically.
    static func place(_ app: URL, at target: URL) throws -> URL {
        let fm = FileManager.default
        let dir = target.deletingLastPathComponent()
        let staged = dir.appendingPathComponent(".Laserdot-update-\(UUID().uuidString).app")
        do {
            try run("/usr/bin/ditto", [app.path, staged.path])
            // Downloads carry a quarantine flag; the copy was checked, and
            // keeping the flag would ask the user again on every update.
            _ = try? run("/usr/bin/xattr", ["-dr", "com.apple.quarantine", staged.path])
            if fm.fileExists(atPath: target.path) {
                _ = try fm.replaceItemAt(target, withItemAt: staged, backupItemName: nil, options: [])
            } else {
                try fm.moveItem(at: staged, to: target)
            }
        } catch let UpdateError.extractFailed(m) {
            try? fm.removeItem(at: staged)
            throw UpdateError.installFailed(m)
        } catch let e as UpdateError {
            try? fm.removeItem(at: staged)
            throw e
        } catch {
            try? fm.removeItem(at: staged)
            throw UpdateError.installFailed(error.localizedDescription)
        }
        return target
    }

    /// Open `app` once this process has exited.
    public static func relaunch(_ app: URL) {
        let pid = ProcessInfo.processInfo.processIdentifier
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/sh")
        p.arguments = ["-c", "while kill -0 \(pid) 2>/dev/null; do sleep 0.2; done; /usr/bin/open \"$0\"", app.path]
        try? p.run()
    }

    // MARK: helpers

    /// Run a local tool; a non-zero exit is `extractFailed` with its last
    /// output line.
    @discardableResult
    /// An app's release notes, written by bundle.sh from CHANGELOG.md.
    /// The release notes of every version after `since` up to this app's
    /// own, newest first, from the app's copy of CHANGELOG.md. Versions
    /// marked `<!-- quiet -->` (fixes nobody sees) are left out; nil when
    /// nothing is left (or the app has no Changelog.md: then WhatsNew.txt).
    public static func notes(in app: URL, since: String?) -> String? {
        let f = app.appendingPathComponent("Contents/Resources/Changelog.md")
        guard let text = try? String(contentsOf: f, encoding: .utf8) else { return whatsNew(in: app) }
        let mine = AppVersion(bundle: app)
        let floor = since.map { AppVersion(short: $0, build: "0") }
        var out: [String] = []
        for section in text.components(separatedBy: "\n## ").dropFirst() {
            let lines = section.components(separatedBy: "\n")
            let version = lines[0].trimmingCharacters(in: .whitespaces)
            let v = AppVersion(short: version, build: "0")
            if let mine, AppVersion(short: mine.short, build: "0") < v { continue } // newer than this app
            if let floor, !(floor < v) { break } // sections are newest first
            let body = lines.dropFirst().joined(separator: "\n")
            if body.contains("<!-- quiet -->") { continue }
            let trimmed = body.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmed.isEmpty { out.append(version + "\n" + trimmed) }
            if floor == nil { break } // no starting point: this version only
        }
        return out.isEmpty ? nil : out.joined(separator: "\n\n")
    }

    public static func whatsNew(in app: URL) -> String? {
        let f = app.appendingPathComponent("Contents/Resources/WhatsNew.txt")
        guard let s = try? String(contentsOf: f, encoding: .utf8) else { return nil }
        let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.isEmpty ? nil : t
    }

    static func run(_ exe: String, _ args: [String]) throws -> String {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: exe)
        p.arguments = args
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = pipe
        p.standardInput = FileHandle.nullDevice
        do { try p.run() } catch { throw UpdateError.extractFailed("\(exe): \(error.localizedDescription)") }
        let out = String(decoding: pipe.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
        p.waitUntilExit()
        guard p.terminationStatus == 0 else {
            let last = out.split(separator: "\n").last.map(String.init) ?? "exit \(p.terminationStatus)"
            throw UpdateError.extractFailed(last.trimmingCharacters(in: .whitespaces))
        }
        return out
    }
}
