import Foundation
import XCTest
@testable import LaserdotKit

/// Show in Finder reaches a file inside a package (Finder would stop
/// outside it); an ordinary file is selected as usual.
final class RevealPlanTests: XCTestCase {
    func testAFileInsideAPackageIsShownInItsOwnFolder() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("rp-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: dir) }
        let media = dir.appendingPathComponent("Notes/Sheet.app/Contents/Media")
        try FileManager.default.createDirectory(at: media, withIntermediateDirectories: true)
        let picture = media.appendingPathComponent("Screenshot.png")
        try Data([0]).write(to: picture)
        let plain = dir.appendingPathComponent("Notes/plain.txt")
        try Data([0]).write(to: plain)

        XCTAssertEqual(RevealPlan.plan(for: plain.path), .select)
        let package = dir.appendingPathComponent("Notes/Sheet.app").standardizedFileURL.path
        XCTAssertEqual(RevealPlan.plan(for: picture.path),
                       .insidePackage(folder: (picture.path as NSString).deletingLastPathComponent, package: package))
        XCTAssertEqual(RevealPlan.outermostPackage(containing: package), nil, "the package itself is not inside one")
    }
}
