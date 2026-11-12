import Foundation
import Security

/// macOS remembers which folders an app may read (Desktop, Documents,
/// drives…) by the app's signature. Releases are signed ad hoc, which macOS
/// sees as a new app on every update, so it asks again. Instead of keeping a
/// signing key anywhere else (the owner's choice), Laserdot makes one on this
/// Mac the first time it installs an update and signs every update with it
/// before installing: from then on each version has the same signature and
/// macOS keeps its answers.
///
/// The key lives in a keychain file of its own in
/// ~/Library/Application Support/Laserdot Signing/, locked except while
/// signing, readable only by this user account. Erase All Laserdot Data
/// deletes it.
public struct LocalSigner {
    public static let identityName = "Laserdot (this Mac)"

    public let dir: URL

    public static var defaultDir: URL {
        FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Application Support/Laserdot Signing")
    }

    public init(dir: URL = LocalSigner.defaultDir) {
        self.dir = dir
    }

    var keychain: URL { dir.appendingPathComponent("signing.keychain-db") }
    var passwordFile: URL { dir.appendingPathComponent("keychain-password") }

    /// The key exists already.
    public var hasKey: Bool {
        FileManager.default.fileExists(atPath: keychain.path) && FileManager.default.fileExists(atPath: passwordFile.path)
    }

    /// Sign `app` (and everything inside it) with this Mac's key, making the
    /// key first if there is none.
    public func sign(_ app: URL) throws {
        let password = try ensureKey()
        try security(["unlock-keychain", "-p", password, keychain.path])
        defer { _ = try? security(["lock-keychain", keychain.path]) }
        // codesign finds the certificate through the search list; the
        // keychain is on it only while signing.
        let list = searchList()
        try security(["list-keychains", "-d", "user", "-s"] + list + [keychain.path])
        defer { _ = try? security(["list-keychains", "-d", "user", "-s"] + list) }
        let hash = try identityHash()
        try Updater.run("/usr/bin/codesign", ["--force", "--deep", "--keychain", keychain.path, "--sign", hash, app.path])
        try Updater.run("/usr/bin/codesign", ["--verify", "--deep", "--strict", app.path])
    }

    /// Delete the key (Erase All Laserdot Data).
    public func forget() {
        _ = try? security(["delete-keychain", keychain.path])
        try? FileManager.default.removeItem(at: dir)
    }

    // MARK: making the key

    func ensureKey() throws -> String {
        let fm = FileManager.default
        if hasKey, let pw = try? String(contentsOf: passwordFile, encoding: .utf8), !pw.isEmpty {
            return pw
        }
        // A half-made key from an earlier try: start over.
        _ = try? security(["delete-keychain", keychain.path])
        try? fm.removeItem(at: dir)
        try fm.createDirectory(at: dir, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let work = dir.appendingPathComponent("making-\(UUID().uuidString)")
        try fm.createDirectory(at: work, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        defer { try? fm.removeItem(at: work) }

        let config = work.appendingPathComponent("cert.cnf")
        try """
        [req]
        distinguished_name = dn
        prompt = no
        [dn]
        CN = \(Self.identityName)
        [ext]
        basicConstraints = critical, CA:false
        keyUsage = critical, digitalSignature
        extendedKeyUsage = critical, codeSigning
        """.write(to: config, atomically: true, encoding: .utf8)
        let key = work.appendingPathComponent("key.pem")
        let cert = work.appendingPathComponent("cert.pem")
        let p12 = work.appendingPathComponent("key.p12")
        let p12Password = Self.randomHex()
        try Updater.run("/usr/bin/openssl", ["req", "-x509", "-newkey", "rsa:3072", "-sha256", "-days", "7300", "-nodes",
                                             "-config", config.path, "-extensions", "ext",
                                             "-keyout", key.path, "-out", cert.path])
        try Updater.run("/usr/bin/openssl", ["pkcs12", "-export", "-inkey", key.path, "-in", cert.path,
                                             "-name", Self.identityName, "-out", p12.path,
                                             "-passout", "pass:\(p12Password)"])

        let password = Self.randomHex()
        try security(["create-keychain", "-p", password, keychain.path])
        // create-keychain may add it to the search list; keep that list as it was.
        try? security(["list-keychains", "-d", "user", "-s"] + searchList().filter { $0 != keychain.path })
        try security(["set-keychain-settings", keychain.path]) // no automatic lock while signing
        try security(["unlock-keychain", "-p", password, keychain.path])
        try security(["import", p12.path, "-k", keychain.path, "-P", p12Password, "-T", "/usr/bin/codesign"])
        // codesign may use the key without asking.
        try security(["set-key-partition-list", "-S", "apple-tool:,apple:,codesign:", "-s", "-k", password, keychain.path])
        try security(["lock-keychain", keychain.path])
        try password.write(to: passwordFile, atomically: true, encoding: .utf8)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: passwordFile.path)
        return password
    }

    /// The SHA-1 of the signing identity (the certificate is self-signed,
    /// so it is picked by hash rather than as a "valid" identity).
    func identityHash() throws -> String {
        let out = try security(["find-identity", "-p", "codesigning", keychain.path])
        for line in out.split(separator: "\n") where line.contains(Self.identityName) {
            for word in line.split(separator: " ") where word.count == 40 && word.allSatisfy(\.isHexDigit) {
                return String(word)
            }
        }
        throw UpdateError.installFailed("the signing key is missing from its keychain")
    }

    func searchList() -> [String] {
        let out = (try? security(["list-keychains", "-d", "user"])) ?? ""
        return out.split(separator: "\n")
            .map { $0.trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\"")) }
            .filter { !$0.isEmpty }
    }

    @discardableResult
    func security(_ args: [String]) throws -> String {
        try Updater.run("/usr/bin/security", args)
    }

    static func randomHex() -> String {
        var bytes = [UInt8](repeating: 0, count: 24)
        if SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes) != errSecSuccess {
            bytes = (0..<24).map { _ in UInt8.random(in: 0...255) }
        }
        return bytes.map { String(format: "%02x", $0) }.joined()
    }
}
