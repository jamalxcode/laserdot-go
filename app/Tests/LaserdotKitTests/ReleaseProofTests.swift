import Foundation
@testable import LaserdotKit
import XCTest

/// An update is installed only with proof that Laserdot's release workflow
/// built it (ReleaseProof).
final class ReleaseProofTests: XCTestCase {
    func testObjectIdentifiersAreEncodedAsInCertificates() {
        XCTAssertEqual(DER.oid("2.5.29.17"), [0x55, 0x1D, 0x11])
        XCTAssertEqual(DER.oid("1.3.6.1.5.5.7.3.3"), [0x2B, 0x06, 0x01, 0x05, 0x05, 0x07, 0x03, 0x03])
        XCTAssertEqual(DER.oid("1.3.6.1.4.1.57264.1.8"), [0x2B, 0x06, 0x01, 0x04, 0x01, 0x83, 0xBF, 0x30, 0x01, 0x08])
    }

    /// Sigstore's embedded certificates are readable and still valid.
    func testTheEmbeddedCertificateAuthorityIsReadable() throws {
        for b64 in [ReleaseProof.sigstoreIntermediate, ReleaseProof.sigstoreRoot] {
            let der = try XCTUnwrap(Data(base64Encoded: b64))
            XCTAssertNotNil(SecCertificateCreateWithData(nil, der as CFData))
            XCTAssertLessThan(try CertInfo(der: der).notBefore, Date())
        }
    }

    func testAnUpdateWithoutProofIsRefused() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("proof-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: dir) }
        let file = dir.appendingPathComponent("Laserdot-9.9.9.dmg")
        try Data("not really a disk image".utf8).write(to: file)
        XCTAssertThrowsError(try ReleaseProof.verify(file: file))
        // Garbage in place of the proof is refused too, without a crash.
        try Data("{\"nonsense\": true}".utf8).write(to: ReleaseProof.proofURL(for: file))
        XCTAssertThrowsError(try ReleaseProof.verify(file: file))
        XCTAssertThrowsError(try Updater(current: dir, signer: nil).prepare(file: file)) { e in
            guard case UpdateError.noProof = e else { return XCTFail("\(e)") }
        }
    }

    /// CI: a real proof GitHub made a moment ago for a file built by this
    /// repository's ci workflow (LASERDOT_PROOF_FILE, its bundle in
    /// LASERDOT_PROOF_BUNDLE, the workflow in LASERDOT_PROOF_WORKFLOW). It is
    /// accepted for that file and that workflow only.
    func testARealProofFromGitHub() throws {
        let env = ProcessInfo.processInfo.environment
        guard let file = env["LASERDOT_PROOF_FILE"], let bundle = env["LASERDOT_PROOF_BUNDLE"],
              let workflow = env["LASERDOT_PROOF_WORKFLOW"] else {
            throw XCTSkip("set by CI after GitHub attests a build")
        }
        let repo = env["LASERDOT_PROOF_REPO"] ?? ReleaseProof.Identity.laserdot.repository
        let data = try Data(contentsOf: URL(fileURLWithPath: bundle))
        let digest = try ReleaseProof.sha256Hex(of: URL(fileURLWithPath: file))
        let me = ReleaseProof.Identity(repository: repo, workflow: workflow)
        XCTAssertNoThrow(try ReleaseProof.verify(sha256: digest, bundle: data, identity: me))

        // A changed file, another workflow, another repository: refused.
        let other = String(repeating: "0", count: 64)
        XCTAssertThrowsError(try ReleaseProof.verify(sha256: other, bundle: data, identity: me))
        XCTAssertThrowsError(try ReleaseProof.verify(sha256: digest, bundle: data,
                                                     identity: .init(repository: repo, workflow: ".github/workflows/other.yml")))
        XCTAssertThrowsError(try ReleaseProof.verify(sha256: digest, bundle: data,
                                                     identity: .init(repository: "someone/else", workflow: workflow)))
        // A tampered signature: refused.
        var json = try XCTUnwrap(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        var env2 = try XCTUnwrap(json["dsseEnvelope"] as? [String: Any])
        var sigs = try XCTUnwrap(env2["signatures"] as? [[String: Any]])
        var sig = try XCTUnwrap(Data(base64Encoded: try XCTUnwrap(sigs[0]["sig"] as? String)))
        sig[sig.count - 1] ^= 0x01
        sigs[0]["sig"] = sig.base64EncodedString()
        env2["signatures"] = sigs
        json["dsseEnvelope"] = env2
        let tampered = try JSONSerialization.data(withJSONObject: json)
        XCTAssertThrowsError(try ReleaseProof.verify(sha256: digest, bundle: tampered, identity: me))
    }
}
