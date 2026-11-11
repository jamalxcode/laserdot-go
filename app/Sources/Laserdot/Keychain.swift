import Foundation
import Security

/// Drive passphrases the user chose to remember, in the login Keychain
/// (never in Laserdot's own files). One item per drive, keyed by its id.
enum Keychain {
    static let service = "Laserdot drive passphrase"

    private static func query(_ account: String?) -> [String: Any] {
        var q: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                kSecAttrService as String: service]
        if let account { q[kSecAttrAccount as String] = account }
        return q
    }

    @discardableResult
    static func save(_ secret: String, for drive: String) -> OSStatus {
        SecItemDelete(query(drive) as CFDictionary)
        var q = query(drive)
        q[kSecValueData as String] = Data(secret.utf8)
        q[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlockedThisDeviceOnly
        q[kSecAttrLabel as String] = service
        return SecItemAdd(q as CFDictionary, nil)
    }

    static func load(for drive: String) -> String? {
        var q = query(drive)
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let data = out as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    static func forget(_ drive: String) {
        SecItemDelete(query(drive) as CFDictionary)
    }

    /// How many passphrases are remembered (the Privacy page shows it).
    static func count() -> Int {
        var q = query(nil)
        q[kSecReturnAttributes as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitAll
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess, let items = out as? [Any] else { return 0 }
        return items.count
    }

    static func forgetAll() {
        SecItemDelete(query(nil) as CFDictionary)
    }
}
