import CryptoKit
import Foundation
import Security

/// Proof that an update was built by Laserdot's own release workflow on
/// GitHub, checked offline before anything is installed.
///
/// The release workflow asks GitHub for a build attestation of the disk
/// image (actions/attest-build-provenance). GitHub's build gets a short-lived
/// certificate from Sigstore's public certificate authority that names the
/// repository and workflow that ran, signs a statement holding the image's
/// SHA-256 with it, and records that in Sigstore's public log. No signing key
/// is stored anywhere: only a workflow run of this repository can get such a
/// certificate. The proof is published next to the image
/// (Laserdot-<version>.dmg.sigstore.json); the updater helper downloads both.
///
/// Laserdot then checks, without the network: the certificate chains to
/// Sigstore's root (embedded below) and was valid when it was used; it was
/// issued to GitHub Actions for `jamalxcode/laserdot-go`'s release workflow;
/// the statement is signed by it; and the statement names this file's
/// SHA-256. (The public log entry is not re-checked offline; the certificate
/// alone already requires a run of this repository's workflow.)
public enum ReleaseProof {
    /// Who may have built an update.
    public struct Identity: Equatable, Sendable {
        public let repository: String
        public let workflow: String

        public init(repository: String, workflow: String) {
            self.repository = repository
            self.workflow = workflow
        }

        public static let laserdot = Identity(repository: "jamalxcode/laserdot-go", workflow: ".github/workflows/release.yml")

        /// The certificate's URI: …/<workflow>@<ref>.
        var signerPrefix: String { "https://github.com/\(repository)/\(workflow)@" }
    }

    public static let githubIssuer = "https://token.actions.githubusercontent.com"

    /// Where the proof of `file` is kept.
    public static func proofURL(for file: URL) -> URL {
        file.deletingLastPathComponent().appendingPathComponent(file.lastPathComponent + ".sigstore.json")
    }

    /// Check the proof of `file` (at proofURL(for:)).
    public static func verify(file: URL, identity: Identity = .laserdot) throws {
        let proof = proofURL(for: file)
        guard let data = try? Data(contentsOf: proof) else {
            throw ProofError("the update has no proof that it was built by Laserdot's release workflow")
        }
        try verify(sha256: try sha256Hex(of: file), bundle: data, identity: identity)
    }

    /// Check a Sigstore bundle against a file's SHA-256 (lowercase hex).
    public static func verify(sha256 digest: String, bundle: Data, identity: Identity) throws {
        let b = try parseBundle(bundle)
        let leafCert = try certificate(b.leaf)
        let leaf = try CertInfo(der: b.leaf)

        // 1. Issued by Sigstore's certificate authority, valid when used.
        try checkChain(leafCert, notBefore: leaf.notBefore)
        // 2. Issued to this repository's release workflow on GitHub Actions.
        guard leaf.ekuCodeSigning else { throw ProofError("the certificate is not for code signing") }
        guard leaf.issuer == githubIssuer else {
            throw ProofError("the certificate was not issued to GitHub Actions (\(leaf.issuer ?? "none"))")
        }
        guard leaf.uris.contains(where: { $0.hasPrefix(identity.signerPrefix) }) else {
            throw ProofError("the update was not built by \(identity.repository)'s release workflow (\(leaf.uris.joined(separator: ", ")))")
        }
        // 3. The statement is signed with that certificate.
        try checkSignature(leafCert, payloadType: b.payloadType, payload: b.payload, signature: b.signature)
        // 4. The statement names this file.
        guard b.payloadType == "application/vnd.in-toto+json" else { throw ProofError("unexpected statement type \(b.payloadType)") }
        let statement = try JSONDecoder().decode(Statement.self, from: b.payload)
        guard statement._type.hasPrefix("https://in-toto.io/Statement/") else { throw ProofError("not an in-toto statement") }
        guard statement.subject.contains(where: { $0.digest["sha256"]?.lowercased() == digest.lowercased() }) else {
            throw ProofError("the proof is for another file (the download was changed)")
        }
    }

    public static func sha256Hex(of file: URL) throws -> String {
        let h = try FileHandle(forReadingFrom: file)
        defer { try? h.close() }
        var hasher = SHA256()
        while let chunk = try h.read(upToCount: 1 << 20), !chunk.isEmpty {
            hasher.update(data: chunk)
        }
        return hasher.finalize().map { String(format: "%02x", $0) }.joined()
    }

    // MARK: the bundle

    struct Parsed {
        var leaf: Data
        var payloadType: String
        var payload: Data
        var signature: Data
    }

    private struct SigstoreBundle: Decodable {
        struct RawBytes: Decodable { let rawBytes: String }
        struct Chain: Decodable { let certificates: [RawBytes] }
        struct Material: Decodable {
            let certificate: RawBytes?
            let x509CertificateChain: Chain?
        }
        struct Signature: Decodable { let sig: String }
        struct Envelope: Decodable {
            let payload: String
            let payloadType: String
            let signatures: [Signature]
        }
        let verificationMaterial: Material
        let dsseEnvelope: Envelope
    }

    private struct Statement: Decodable {
        struct Subject: Decodable { let digest: [String: String] }
        let _type: String
        let subject: [Subject]
    }

    static func parseBundle(_ data: Data) throws -> Parsed {
        // One JSON bundle; a JSON Lines file holds one per line (the first is used).
        let decoder = JSONDecoder()
        let firstLine = String(decoding: data, as: UTF8.self).split(whereSeparator: \.isNewline).first.map { Data($0.utf8) }
        guard let b = (try? decoder.decode(SigstoreBundle.self, from: data))
            ?? firstLine.flatMap({ try? decoder.decode(SigstoreBundle.self, from: $0) }) else {
            throw ProofError("the proof is unreadable")
        }
        guard let leafB64 = b.verificationMaterial.certificate?.rawBytes ?? b.verificationMaterial.x509CertificateChain?.certificates.first?.rawBytes,
              let leaf = Data(base64Encoded: leafB64),
              let payload = Data(base64Encoded: b.dsseEnvelope.payload),
              let sigB64 = b.dsseEnvelope.signatures.first?.sig, let sig = Data(base64Encoded: sigB64) else {
            throw ProofError("the proof is incomplete")
        }
        return Parsed(leaf: leaf, payloadType: b.dsseEnvelope.payloadType, payload: payload, signature: sig)
    }

    // MARK: checks

    static func certificate(_ der: Data) throws -> SecCertificate {
        guard let c = SecCertificateCreateWithData(nil, der as CFData) else { throw ProofError("the certificate is unreadable") }
        return c
    }

    static func checkChain(_ leaf: SecCertificate, notBefore: Date) throws {
        let intermediate = try certificate(Data(base64Encoded: sigstoreIntermediate)!)
        let root = try certificate(Data(base64Encoded: sigstoreRoot)!)
        var trust: SecTrust?
        guard SecTrustCreateWithCertificates([leaf, intermediate] as CFArray, SecPolicyCreateBasicX509(), &trust) == errSecSuccess,
              let trust else { throw ProofError("the certificate could not be checked") }
        SecTrustSetAnchorCertificates(trust, [root] as CFArray)
        SecTrustSetAnchorCertificatesOnly(trust, true)
        SecTrustSetNetworkFetchAllowed(trust, false)
        // Sigstore certificates last ten minutes: check them as of when this
        // one was issued (the signature was made within that time).
        SecTrustSetVerifyDate(trust, notBefore.addingTimeInterval(30) as CFDate)
        var err: CFError?
        guard SecTrustEvaluateWithError(trust, &err) else {
            let why = err.map { CFErrorCopyDescription($0) as String } ?? "untrusted"
            throw ProofError("the certificate is not from Sigstore: \(why)")
        }
    }

    static func checkSignature(_ leaf: SecCertificate, payloadType: String, payload: Data, signature: Data) throws {
        guard let key = SecCertificateCopyKey(leaf) else { throw ProofError("the certificate has no key") }
        // DSSE's pre-authentication encoding of what was signed.
        var pae = Data("DSSEv1 \(payloadType.utf8.count) \(payloadType) \(payload.count) ".utf8)
        pae.append(payload)
        let attrs = SecKeyCopyAttributes(key) as? [String: Any]
        let bits = attrs?[kSecAttrKeySizeInBits as String] as? Int ?? 256
        let alg: SecKeyAlgorithm = bits > 256 ? .ecdsaSignatureMessageX962SHA384 : .ecdsaSignatureMessageX962SHA256
        var err: Unmanaged<CFError>?
        guard SecKeyVerifySignature(key, alg, pae as CFData, signature as CFData, &err) else {
            _ = err?.takeRetainedValue()
            throw ProofError("the proof's signature does not match")
        }
    }

    // MARK: Sigstore's certificate authority (public; from its trusted root)

    /// CN=sigstore-intermediate, O=sigstore.dev
    static let sigstoreIntermediate = "MIICGjCCAaGgAwIBAgIUALnViVfnU0brJasmRkHrn/UnfaQwCgYIKoZIzj0EAwMwKjEVMBMGA1UEChMMc2lnc3RvcmUuZGV2MREwDwYDVQQDEwhzaWdzdG9yZTAeFw0yMjA0MTMyMDA2MTVaFw0zMTEwMDUxMzU2NThaMDcxFTATBgNVBAoTDHNpZ3N0b3JlLmRldjEeMBwGA1UEAxMVc2lnc3RvcmUtaW50ZXJtZWRpYXRlMHYwEAYHKoZIzj0CAQYFK4EEACIDYgAE8RVS/ysH+NOvuDZyPIZtilgUF9NlarYpAd9HP1vBBH1U5CV77LSS7s0ZiH4nE7Hv7ptS6LvvR/STk798LVgMzLlJ4HeIfF3tHSaexLcYpSASr1kS0N/RgBJz/9jWCiXno3sweTAOBgNVHQ8BAf8EBAMCAQYwEwYDVR0lBAwwCgYIKwYBBQUHAwMwEgYDVR0TAQH/BAgwBgEB/wIBADAdBgNVHQ4EFgQU39Ppz1YkEZb5qNjpKFWixi4YZD8wHwYDVR0jBBgwFoAUWMAeX5FFpWapesyQoZMi0CrFxfowCgYIKoZIzj0EAwMDZwAwZAIwPCsQK4DYiZYDPIaDi5HFKnfxXx6ASSVmERfsynYBiX2X6SJRnZU84/9DZdnFvvxmAjBOt6QpBlc4J/0DxvkTCqpclvziL6BCCPnjdlIB3Pu3BxsPmygUY7Ii2zbdCdliiow="
    /// CN=sigstore, O=sigstore.dev (both valid until 2031-10-05)
    static let sigstoreRoot = "MIIB9zCCAXygAwIBAgIUALZNAPFdxHPwjeDloDwyYChAO/4wCgYIKoZIzj0EAwMwKjEVMBMGA1UEChMMc2lnc3RvcmUuZGV2MREwDwYDVQQDEwhzaWdzdG9yZTAeFw0yMTEwMDcxMzU2NTlaFw0zMTEwMDUxMzU2NThaMCoxFTATBgNVBAoTDHNpZ3N0b3JlLmRldjERMA8GA1UEAxMIc2lnc3RvcmUwdjAQBgcqhkjOPQIBBgUrgQQAIgNiAAT7XeFT4rb3PQGwS4IajtLk3/OlnpgangaBclYpsYBr5i+4ynB07ceb3LP0OIOZdxexX69c5iVuyJRQ+Hz05yi+UF3uBWAlHpiS5sh0+H2GHE7SXrk1EC5m1Tr19L9gg92jYzBhMA4GA1UdDwEB/wQEAwIBBjAPBgNVHRMBAf8EBTADAQH/MB0GA1UdDgQWBBRYwB5fkUWlZql6zJChkyLQKsXF+jAfBgNVHSMEGDAWgBRYwB5fkUWlZql6zJChkyLQKsXF+jAKBggqhkjOPQQDAwNpADBmAjEAj1nHeXZp+13NWBNa+EDsDP8G1WWg1tCMWP/WHPqpaVo0jhsweNFZgSs0eE7wYI4qAjEA2WB9ot98sIkoF3vZYdd3/VtWB5b9TNMea7Ix/stJ5TfcLLeABLE4BNJOsQ4vnBHJ"
}

public struct ProofError: Error, LocalizedError, Equatable {
    public let message: String
    init(_ m: String) { message = m }
    public var errorDescription: String? { message }
}

/// What the checks need from a certificate, read from its DER encoding.
struct CertInfo {
    var notBefore = Date.distantPast
    var uris: [String] = []
    var issuer: String?
    var ekuCodeSigning = false

    init(der: Data) throws {
        let bytes = [UInt8](der)
        guard let cert = DER.read(bytes[...])?.node, let tbs = cert.children.first else {
            throw ProofError("the certificate is unreadable")
        }
        var kids = tbs.children
        if kids.first?.tag == 0xA0 { kids.removeFirst() } // version
        // serial, signature algorithm, issuer, validity, subject, key, …
        guard kids.count >= 6, let start = kids[3].children.first, let t = DER.time(start) else {
            throw ProofError("the certificate's dates are unreadable")
        }
        notBefore = t
        guard let exts = kids.first(where: { $0.tag == 0xA3 })?.children.first?.children else { return }
        for ext in exts {
            let parts = ext.children
            guard let oid = parts.first, oid.tag == 0x06, let value = parts.last, value.tag == 0x04 else { continue }
            switch Array(oid.content) {
            case DER.oid("2.5.29.17"): // subjectAltName
                for name in DER.read(value.content)?.node.children ?? [] where name.tag == 0x86 {
                    uris.append(String(decoding: name.content, as: UTF8.self))
                }
            case DER.oid("2.5.29.37"): // extendedKeyUsage
                let codeSigning = DER.oid("1.3.6.1.5.5.7.3.3")
                ekuCodeSigning = (DER.read(value.content)?.node.children ?? []).contains { Array($0.content) == codeSigning }
            case DER.oid("1.3.6.1.4.1.57264.1.8"): // OIDC issuer (DER string)
                if let s = DER.read(value.content)?.node { issuer = String(decoding: s.content, as: UTF8.self) }
            case DER.oid("1.3.6.1.4.1.57264.1.1"): // OIDC issuer (raw, older)
                if issuer == nil { issuer = String(decoding: value.content, as: UTF8.self) }
            default:
                break
            }
        }
    }
}

/// A minimal DER reader: enough to walk a certificate.
struct DER {
    let tag: UInt8
    let content: ArraySlice<UInt8>

    var children: [DER] {
        var out: [DER] = []
        var rest = content
        while !rest.isEmpty, let r = DER.read(rest) {
            out.append(r.node)
            rest = r.rest
        }
        return out
    }

    static func read(_ b: ArraySlice<UInt8>) -> (node: DER, rest: ArraySlice<UInt8>)? {
        var i = b.startIndex
        guard i < b.endIndex else { return nil }
        let tag = b[i]
        i += 1
        guard i < b.endIndex else { return nil }
        var len = Int(b[i])
        i += 1
        if len & 0x80 != 0 {
            let n = len & 0x7f
            guard n >= 1, n <= 4, i + n <= b.endIndex else { return nil }
            len = 0
            for _ in 0..<n {
                len = len << 8 | Int(b[i])
                i += 1
            }
        }
        guard len >= 0, i + len <= b.endIndex else { return nil }
        return (DER(tag: tag, content: b[i..<i + len]), b[(i + len)...])
    }

    /// The content bytes of an object identifier written in dots.
    static func oid(_ dotted: String) -> [UInt8] {
        let parts = dotted.split(separator: ".").compactMap { UInt64($0) }
        guard parts.count >= 2 else { return [] }
        var out: [UInt8] = []
        func base128(_ v: UInt64) {
            var stack: [UInt8] = [UInt8(v & 0x7f)]
            var v = v >> 7
            while v > 0 {
                stack.append(UInt8(v & 0x7f) | 0x80)
                v >>= 7
            }
            out.append(contentsOf: stack.reversed())
        }
        base128(parts[0] * 40 + parts[1])
        for p in parts.dropFirst(2) { base128(p) }
        return out
    }

    /// UTCTime (YYMMDDHHMMSSZ) or GeneralizedTime (YYYYMMDDHHMMSSZ).
    static func time(_ n: DER) -> Date? {
        let s = String(decoding: n.content, as: UTF8.self)
        let f = DateFormatter()
        f.locale = Locale(identifier: "en_US_POSIX")
        f.timeZone = TimeZone(identifier: "UTC")
        switch n.tag {
        case 0x17:
            f.dateFormat = "yyMMddHHmmss'Z'"
            // X.509: two-digit years 50–99 are 19xx.
            f.twoDigitStartDate = DateComponents(calendar: Calendar(identifier: .gregorian), timeZone: TimeZone(identifier: "UTC"), year: 1950).date
        case 0x18:
            f.dateFormat = "yyyyMMddHHmmss'Z'"
        default:
            return nil
        }
        return f.date(from: s)
    }
}
