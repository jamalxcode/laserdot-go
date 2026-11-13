import Foundation
@testable import LaserdotKit
import XCTest

/// The offline half of updating (spec §7.4): finding, unpacking, checking
/// and installing a downloaded Laserdot. The network half lives in the
/// Laserdot Updater helper.
final class UpdaterTests: XCTestCase {
    private var dir: URL!

    override func setUpWithError() throws {
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("updater-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: dir)
    }

    /// A minimal Laserdot.app at `parent/name`.
    @discardableResult
    private func fakeApp(_ parent: URL, version: String, build: String = "1", id: String = "app.laserdot.Laserdot",
                         daemon: Bool = true, name: String = "Laserdot.app") throws -> URL {
        let app = parent.appendingPathComponent(name)
        let macos = app.appendingPathComponent("Contents/MacOS")
        try FileManager.default.createDirectory(at: macos, withIntermediateDirectories: true)
        let info: [String: Any] = ["CFBundleIdentifier": id, "CFBundleShortVersionString": version,
                                   "CFBundleVersion": build, "CFBundleExecutable": "Laserdot"]
        let data = try PropertyListSerialization.data(fromPropertyList: info, format: .xml, options: 0)
        try data.write(to: app.appendingPathComponent("Contents/Info.plist"))
        for exe in daemon ? ["Laserdot", "laserdotd"] : ["Laserdot"] {
            let f = macos.appendingPathComponent(exe)
            try Data("#!/bin/sh\n".utf8).write(to: f)
            try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: f.path)
        }
        return app
    }

    private func zip(_ app: URL, to out: URL) throws {
        try Updater.run("/usr/bin/ditto", ["-c", "-k", "--keepParent", app.path, out.path])
    }

    func testVersionsCompareNumerically() {
        XCTAssertLessThan(AppVersion(short: "0.9.3", build: "9"), AppVersion(short: "0.10.0", build: "1"))
        XCTAssertLessThan(AppVersion(short: "1.2", build: "3"), AppVersion(short: "1.2.0", build: "4"))
        XCTAssertEqual(AppVersion(short: "1.2", build: "3"), AppVersion(short: "1.2.0", build: "3"))
        XCTAssertFalse(AppVersion(short: "2.0.0", build: "1") < AppVersion(short: "1.9.9", build: "99"))
    }

    func testFindsLaserdotDownloadsNewestFirst() throws {
        let dl = dir.appendingPathComponent("Downloads")
        try FileManager.default.createDirectory(at: dl, withIntermediateDirectories: true)
        for (n, age) in [("Laserdot-0.2.0.dmg", 100.0), ("Laserdot-0.3.0.dmg", 10.0), ("other.dmg", 1.0), ("Laserdot.txt", 1.0)] {
            let f = dl.appendingPathComponent(n)
            try Data().write(to: f)
            try FileManager.default.setAttributes([.modificationDate: Date().addingTimeInterval(-age)], ofItemAtPath: f.path)
        }
        XCTAssertEqual(Updater.downloads(in: dl).map(\.lastPathComponent), ["Laserdot-0.3.0.dmg", "Laserdot-0.2.0.dmg"])
    }

    func testPreparesFromZipDmgAndNestedZipAndRejectsOldOrForeign() throws {
        let installed = try fakeApp(dir.appendingPathComponent("Applications"), version: "0.1.0")
        let up = Updater(current: installed, signer: nil, proof: nil)
        let newer = try fakeApp(dir.appendingPathComponent("new"), version: "0.2.0", build: "7")

        let z = dir.appendingPathComponent("Laserdot-0.2.0.zip")
        try zip(newer, to: z)
        let c = try up.prepare(file: z)
        XCTAssertEqual(c.version.description, "0.2.0 (7)")
        XCTAssertTrue(c.unsigned)
        up.discard(c)

        // A CI artifact: a zip holding the release zip.
        let outer = dir.appendingPathComponent("artifact")
        try FileManager.default.createDirectory(at: outer, withIntermediateDirectories: true)
        try FileManager.default.copyItem(at: z, to: outer.appendingPathComponent("Laserdot-unsigned.zip"))
        let nested = dir.appendingPathComponent("Laserdot-artifact.zip")
        try Updater.run("/usr/bin/ditto", ["-c", "-k", outer.appendingPathComponent("Laserdot-unsigned.zip").path, nested.path])
        up.discard(try up.prepare(file: nested))

        let dmg = dir.appendingPathComponent("Laserdot-0.2.0.dmg")
        try Updater.run("/usr/bin/hdiutil", ["create", "-volname", "Laserdot", "-srcfolder", newer.deletingLastPathComponent().path,
                                            "-ov", "-format", "UDZO", dmg.path])
        up.discard(try up.prepare(file: dmg))

        let old = try fakeApp(dir.appendingPathComponent("old"), version: "0.1.0")
        let oz = dir.appendingPathComponent("old.zip")
        try zip(old, to: oz)
        XCTAssertThrowsError(try up.prepare(file: oz)) { e in
            guard case UpdateError.notNewer = e else { return XCTFail("\(e)") }
        }
        let other = try fakeApp(dir.appendingPathComponent("other"), version: "9.0", id: "com.example.Other")
        let otz = dir.appendingPathComponent("other.zip")
        try zip(other, to: otz)
        XCTAssertThrowsError(try up.prepare(file: otz)) { e in
            XCTAssertEqual(e as? UpdateError, .wrongApp("com.example.Other"))
        }
        let broken = try fakeApp(dir.appendingPathComponent("broken"), version: "3.0", daemon: false)
        let bz = dir.appendingPathComponent("broken.zip")
        try zip(broken, to: bz)
        XCTAssertThrowsError(try up.prepare(file: bz)) { e in XCTAssertEqual(e as? UpdateError, .missingDaemon) }

        // The newest valid download wins; the old one is skipped.
        let dl = dir.appendingPathComponent("dl")
        try FileManager.default.createDirectory(at: dl, withIntermediateDirectories: true)
        try FileManager.default.copyItem(at: oz, to: dl.appendingPathComponent("Laserdot-0.1.0.zip"))
        try FileManager.default.copyItem(at: z, to: dl.appendingPathComponent("Laserdot-0.2.0.zip"))
        XCTAssertEqual(up.newestUpdate(in: dl)?.version.short, "0.2.0")
    }

    func testInstallReplacesTheAppAndMoveToApplicationsCopies() throws {
        let apps = dir.appendingPathComponent("Applications")
        let installed = try fakeApp(apps, version: "0.1.0")
        let up = Updater(current: installed, previousDir: dir.appendingPathComponent("prev"), signer: nil, proof: nil)
        XCTAssertEqual(up.installTarget, installed.standardizedFileURL)
        let z = dir.appendingPathComponent("u.zip")
        try zip(try fakeApp(dir.appendingPathComponent("new"), version: "0.2.0"), to: z)
        let target = try up.install(try up.prepare(file: z))
        XCTAssertEqual(AppVersion(bundle: target)?.short, "0.2.0")
        let left = try FileManager.default.contentsOfDirectory(atPath: apps.path)
        XCTAssertEqual(left, ["Laserdot.app"], "no staging copies left behind")

        let elsewhere = try fakeApp(dir.appendingPathComponent("Downloads"), version: "0.4.0")
        let fresh = dir.appendingPathComponent("FreshApplications")
        try FileManager.default.createDirectory(at: fresh, withIntermediateDirectories: true)
        let moved = try Updater(current: elsewhere, signer: nil, proof: nil).moveToApplications(fresh)
        XCTAssertEqual(AppVersion(bundle: moved)?.short, "0.4.0")
        XCTAssertTrue(Updater(current: elsewhere, signer: nil, proof: nil).isOutsideApplications)
    }

    /// An update keeps the version it replaces; "Go back" puts it back.
    func testGoBackRestoresTheVersionBeforeTheUpdate() throws {
        let apps = dir.appendingPathComponent("Applications")
        let prev = dir.appendingPathComponent("prev")
        let installed = try fakeApp(apps, version: "0.7.0")
        let old = Updater(current: installed, previousDir: prev, signer: nil, proof: nil)
        XCTAssertNil(old.previousVersion, "nothing kept before any update")
        let z = dir.appendingPathComponent("u.zip")
        try zip(try fakeApp(dir.appendingPathComponent("new"), version: "0.8.0"), to: z)
        try old.install(try old.prepare(file: z))
        XCTAssertEqual(AppVersion(bundle: installed)?.short, "0.8.0")

        // The relaunched 0.8.0 offers 0.7.0.
        let new = Updater(current: installed, previousDir: prev, signer: nil, proof: nil)
        XCTAssertEqual(new.previousVersion?.short, "0.7.0")
        let back = try new.goBack()
        XCTAssertEqual(AppVersion(bundle: back)?.short, "0.7.0")
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: apps.path), ["Laserdot.app"])
        // 0.8.0 is now the kept one, newer than the running app: not offered.
        XCTAssertNil(Updater(current: installed, previousDir: prev, signer: nil, proof: nil).previousVersion)
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: prev.path), ["Laserdot.app"])
    }

    /// An update made ready ahead of time sits hidden beside the app; the
    /// install on quit is then a rename. A later run finds it again, and a
    /// copy that is no longer newer is removed.
    func testStagedUpdateInstallsByRenameAndIsFoundAgain() throws {
        let apps = dir.appendingPathComponent("Applications")
        let prev = dir.appendingPathComponent("prev")
        let installed = try fakeApp(apps, version: "0.7.0")
        let u = Updater(current: installed, previousDir: prev, signer: nil, proof: nil)
        let z = dir.appendingPathComponent("u.zip")
        try zip(try fakeApp(dir.appendingPathComponent("new"), version: "0.8.0"), to: z)
        let st = try u.stage(try u.prepare(file: z))
        XCTAssertEqual(st.version.short, "0.8.0")
        XCTAssertEqual(AppVersion(bundle: installed)?.short, "0.7.0", "staging installs nothing")
        XCTAssertEqual(u.previousVersion, nil, "the kept copy is the running version, not older")
        XCTAssertEqual(AppVersion(bundle: prev.appendingPathComponent("Laserdot.app"))?.short, "0.7.0")

        // Laserdot restarted before quitting: the staged copy is found again.
        let again = Updater(current: installed, previousDir: prev, signer: nil, proof: nil).stagedUpdate()
        XCTAssertEqual(again?.app.path, st.app.path)
        XCTAssertEqual(again?.target.path, installed.path)
        XCTAssertEqual(again?.version.short, "0.8.0")
        try u.finishStaged(st)
        XCTAssertEqual(AppVersion(bundle: installed)?.short, "0.8.0")
        XCTAssertEqual(try FileManager.default.contentsOfDirectory(atPath: apps.path), ["Laserdot.app"])

        // A staged copy that is not newer than the running app is removed.
        let stale = apps.appendingPathComponent(".Laserdot-staged-0.8.0-old.app")
        try FileManager.default.copyItem(at: installed, to: stale)
        XCTAssertNil(u.stagedUpdate())
        XCTAssertFalse(FileManager.default.fileExists(atPath: stale.path))
    }

    /// Updates are signed with this Mac's own key, made on first use: two
    /// versions then have the same designated requirement, which is what
    /// macOS ties its permissions to.
    func testUpdatesAreSignedWithThisMacsKeyAndKeepTheSameSignature() throws {
        let signer = LocalSigner(dir: dir.appendingPathComponent("signing"))
        defer { signer.forget() }
        let apps = dir.appendingPathComponent("Applications")
        let installed = try realApp(apps, version: "0.9.1")
        var requirements: [String] = []
        for v in ["0.9.2", "0.9.3"] {
            let up = Updater(current: installed, previousDir: dir.appendingPathComponent("prev"), signer: signer, proof: nil)
            let z = dir.appendingPathComponent("u-\(v).zip")
            try zip(try realApp(dir.appendingPathComponent("new-\(v)"), version: v), to: z)
            let target = try up.install(try up.prepare(file: z))
            XCTAssertNil(up.lastSigningError)
            XCTAssertEqual(AppVersion(bundle: target)?.short, v)
            let info = try Updater.run("/usr/bin/codesign", ["-dvv", target.path])
            XCTAssertTrue(info.contains("Authority=\(LocalSigner.identityName)"), info)
            _ = try Updater.run("/usr/bin/codesign", ["--verify", "--deep", "--strict", target.path])
            requirements.append(try Updater.run("/usr/bin/codesign", ["-d", "-r-", target.path]))
        }
        XCTAssertEqual(requirements[0], requirements[1], "same signature from one update to the next")
        XCTAssertTrue(signer.hasKey)
        XCTAssertFalse(signer.searchList().contains(signer.keychain.path), "the key's keychain is not left on the search list")
        signer.forget()
        XCTAssertFalse(FileManager.default.fileExists(atPath: signer.dir.path))
    }

    /// A Laserdot.app whose executables are real programs (codesign signs Mach-O files).
    private func realApp(_ parent: URL, version: String) throws -> URL {
        let app = try fakeApp(parent, version: version)
        for exe in ["Laserdot", "laserdotd"] {
            let f = app.appendingPathComponent("Contents/MacOS/\(exe)")
            try FileManager.default.removeItem(at: f)
            try FileManager.default.copyItem(atPath: "/usr/bin/true", toPath: f.path)
        }
        return app
    }

    /// What's New after an update: every version since the one the user had,
    /// newest first; quiet (fixes-only) versions and newer ones left out.
    func testNotesSinceTheLastVersionSkipQuietOnes() throws {
        let app = try fakeApp(dir.appendingPathComponent("A"), version: "0.9.6")
        let res = app.appendingPathComponent("Contents/Resources")
        try FileManager.default.createDirectory(at: res, withIntermediateDirectories: true)
        try """
        # Changelog

        Intro.

        ## 0.9.7

        - Not out yet.

        ## 0.9.6

        - Columns can be resized.

        ## 0.9.5

        <!-- quiet -->
        - Fixed a typo.

        ## 0.9.4

        - Refresh button.

        ## 0.9.3

        - Thumbnails.
        """.write(to: res.appendingPathComponent("Changelog.md"), atomically: true, encoding: .utf8)
        XCTAssertEqual(Updater.notes(in: app, since: "0.9.3"), "0.9.6\n- Columns can be resized.\n\n0.9.4\n- Refresh button.")
        XCTAssertEqual(Updater.notes(in: app, since: nil), "0.9.6\n- Columns can be resized.")
        XCTAssertNil(Updater.notes(in: app, since: "0.9.6"))
        // Only a quiet version since: nothing to show.
        let quiet = try fakeApp(dir.appendingPathComponent("B"), version: "0.9.5")
        try FileManager.default.createDirectory(at: quiet.appendingPathComponent("Contents/Resources"), withIntermediateDirectories: true)
        try FileManager.default.copyItem(at: res.appendingPathComponent("Changelog.md"),
                                         to: quiet.appendingPathComponent("Contents/Resources/Changelog.md"))
        XCTAssertNil(Updater.notes(in: quiet, since: "0.9.4"))
    }

    /// The real bundled app (CI sets LASERDOT_APP), signed as an update is
    /// on the user's Mac: every nested part carries the key, the signature
    /// verifies, and the app launches and keeps running.
    func testSignsTheRealBuiltApp() throws {
        guard let path = ProcessInfo.processInfo.environment["LASERDOT_APP"] else {
            throw XCTSkip("set LASERDOT_APP to the bundled Laserdot.app")
        }
        let copy = dir.appendingPathComponent("Laserdot.app")
        try Updater.run("/usr/bin/ditto", [path, copy.path])
        let signer = LocalSigner(dir: dir.appendingPathComponent("signing"))
        defer { signer.forget() }
        try signer.sign(copy)
        _ = try Updater.run("/usr/bin/codesign", ["--verify", "--deep", "--strict", copy.path])
        for part in ["", "Contents/Helpers/Laserdot Updater.app", "Contents/MacOS/laserdotd"] {
            let info = try Updater.run("/usr/bin/codesign", ["-dvv", copy.appendingPathComponent(part).path])
            XCTAssertTrue(info.contains("Authority=\(LocalSigner.identityName)"), "\(part): \(info)")
        }
        let p = Process()
        p.executableURL = copy.appendingPathComponent("Contents/MacOS/Laserdot")
        var env = ProcessInfo.processInfo.environment
        env["LASERDOT_DB"] = dir.appendingPathComponent("data/index.db").path
        p.environment = env
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        try p.run()
        Thread.sleep(forTimeInterval: 6)
        XCTAssertTrue(p.isRunning, "the signed app quit at launch")
        p.terminate()
        _ = try? Updater.run("/usr/bin/pkill", ["-f", copy.appendingPathComponent("Contents/MacOS/laserdotd").path])
    }

    func testStatusRoundTrips() throws {
        let f = dir.appendingPathComponent("update.json")
        let s = UpdateStatus(state: .downloaded, checkedAt: Date(timeIntervalSince1970: 1_800_000_000), latest: "0.3.0", file: "/x/Laserdot-0.3.0.dmg")
        try s.save(to: f)
        XCTAssertEqual(UpdateStatus.load(from: f), s)
        XCTAssertNil(UpdateStatus.load(from: dir.appendingPathComponent("missing.json")))
    }
}

final class UpdateProgressTests: XCTestCase {
    /// The helper writes how far it has got; the app reads it back for its
    /// update window.
    func testProgressRoundTripsAndGivesAFraction() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("progress-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let url = dir.appendingPathComponent("update-progress.json")
        XCTAssertNil(UpdateProgress.load(from: url))
        UpdateProgress(phase: .downloading, received: 25, total: 100, version: "1.2.3").save(to: url)
        let p = try XCTUnwrap(UpdateProgress.load(from: url))
        XCTAssertEqual(p.phase, .downloading)
        XCTAssertEqual(p.version, "1.2.3")
        XCTAssertEqual(p.fraction ?? -1, 0.25, accuracy: 0.0001)
        XCTAssertNil(UpdateProgress(phase: .checking).fraction, "checking has no size: a moving bar")
        XCTAssertNil(UpdateProgress(phase: .downloading, received: 5).fraction, "size not known yet")
        XCTAssertEqual(UpdateProgress.defaultURL.deletingLastPathComponent(), UpdateStatus.defaultURL.deletingLastPathComponent())
    }
}
