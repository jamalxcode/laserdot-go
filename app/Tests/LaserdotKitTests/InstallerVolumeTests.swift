import Foundation
import XCTest
@testable import LaserdotKit

/// A disk image opened to install an app is not offered for indexing; a
/// real volume (and a plain folder) is.
final class InstallerVolumeTests: XCTestCase {
    private var dir: URL!

    override func setUpWithError() throws {
        dir = FileManager.default.temporaryDirectory.appendingPathComponent("iv-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    override func tearDown() {
        try? FileManager.default.removeItem(at: dir)
    }

    private func run(_ exe: String, _ args: [String]) -> Int32 {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: exe)
        p.arguments = args
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        guard (try? p.run()) != nil else { return -1 }
        p.waitUntilExit()
        return p.terminationStatus
    }

    func testAnAppInstallerImageIsRecognisedAndAFolderIsNot() throws {
        // The installer's contents: an app and the shortcut to Applications.
        let src = dir.appendingPathComponent("Laser Email")
        let app = src.appendingPathComponent("Laser Email.app/Contents")
        try FileManager.default.createDirectory(at: app, withIntermediateDirectories: true)
        try FileManager.default.createSymbolicLink(atPath: src.appendingPathComponent("Applications").path,
                                                   withDestinationPath: "/Applications")
        XCTAssertTrue(InstallerVolume.looksLikeInstaller(mount: src.path))

        // A plain folder of files is not one.
        let docs = dir.appendingPathComponent("Docs")
        try FileManager.default.createDirectory(at: docs, withIntermediateDirectories: true)
        try "hi".write(to: docs.appendingPathComponent("note.txt"), atomically: true, encoding: .utf8)
        XCTAssertFalse(InstallerVolume.looksLikeInstaller(mount: docs.path))
        XCTAssertFalse(InstallerVolume.isInstaller(mount: docs.path, readOnly: false))
        XCTAssertEqual(InstallerVolume.isDiskImage(mount: "/"), false, "the startup disk is not a disk image")

        // The real thing: a read-only disk image, mounted.
        let img = dir.appendingPathComponent("installer.dmg")
        guard run("/usr/bin/hdiutil", ["create", "-quiet", "-srcfolder", src.path, "-volname", "Laser Email",
                                       "-format", "UDZO", img.path]) == 0 else {
            throw XCTSkip("hdiutil could not make a disk image here")
        }
        let mount = dir.appendingPathComponent("mnt")
        guard run("/usr/bin/hdiutil", ["attach", "-quiet", "-nobrowse", "-readonly", "-mountpoint", mount.path,
                                       img.path]) == 0 else {
            throw XCTSkip("hdiutil could not mount the disk image here")
        }
        defer { _ = run("/usr/bin/hdiutil", ["detach", "-quiet", "-force", mount.path]) }
        XCTAssertEqual(InstallerVolume.isDiskImage(mount: mount.path), true)
        XCTAssertTrue(InstallerVolume.isInstaller(mount: mount.path, readOnly: true))
    }
}
